package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-router/backend/config"
	"agent-router/backend/credential"
	"agent-router/backend/provider"
	"agent-router/backend/usage"
)

type Server struct {
	registry    *provider.Registry
	mappings    *config.MappingStore
	credentials *credential.Pool
	keys        KeyVerifier
	adapters    AdapterSet
	usage       *usage.SQLiteTracker
	client      *http.Client

	// stallTimeout is how long a streaming upstream body may stay silent
	// before the gateway gives up on it; see upstreamStallTimeout.
	stallTimeout time.Duration

	// mu guards http, which Start, Close and Running all touch. The UI proxy
	// toggle can call Start/Close while a request is being served.
	mu   sync.Mutex
	http *http.Server
}

type KeyVerifier interface {
	Lookup(token string) (id, name string, ok bool)
}

// upstreamHeaderTimeout bounds reaching the upstream, not the whole exchange.
// An http.Client.Timeout is a deadline on the entire body read, so a long
// completion was cut off at exactly 120s mid-stream and the client reported
// "OpenAI completions stream closed before a finish_reason was received". The
// request log agreed: every truncated row landed at 120.0xx s with no finish
// chunk and no [DONE]. A streaming body is bounded by the request context
// (the client hanging up) instead; an upstream that stalls mid-stream is left
// to the client's own idle timeout, which it already enforces.
const upstreamHeaderTimeout = 120 * time.Second

func upstreamClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = upstreamHeaderTimeout
	return &http.Client{Transport: transport}
}

// upstreamStallTimeout bounds how long a streaming upstream body may stay
// silent. ResponseHeaderTimeout covers the wait for headers, but body reads
// are otherwise unbounded: a half-open proxied connection (TUN fake-ip mode
// leaves the socket ESTABLISHED while forwarding nothing) parks the handler
// in Body.Read forever, so the client waits on "will retry" and the request
// never even reaches request_logs — logEvent runs only after the stream ends.
// The window matches the header timeout; a healthy SSE body resets it with
// every chunk, so only a genuinely silent upstream trips it. It lives on the
// Server (not a package constant) so tests can shorten it.
const upstreamStallTimeout = 120 * time.Second

// stallCloser closes the wrapped body when it stays silent past the window.
// http.Response.Body is safe to Close concurrently with a blocked Read, and
// the Close unblocks it, turning a wedged handler into an ordinary read error
// the streaming loops already know how to report.
type stallCloser struct {
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
}

func newStallCloser(body io.ReadCloser, timeout time.Duration) *stallCloser {
	s := &stallCloser{body: body, timeout: timeout}
	s.timer = time.AfterFunc(timeout, func() { _ = body.Close() })
	return s
}

func (s *stallCloser) Read(p []byte) (int, error) {
	n, err := s.body.Read(p)
	if err == nil && n > 0 {
		s.timer.Reset(s.timeout)
	}
	return n, err
}

func (s *stallCloser) Close() error {
	s.timer.Stop()
	return s.body.Close()
}

// New wires the gateway. Upstream keys are reached only through the credential
// pool, so this server never holds a single key itself; the pool is the one
// owner of which key serves a request.
func New(registry *provider.Registry, mappings *config.MappingStore, credentials *credential.Pool, usageTracker *usage.SQLiteTracker, keys KeyVerifier) *Server {
	client := upstreamClient()
	return &Server{registry: registry, mappings: mappings, credentials: credentials, keys: keys, adapters: NewAdapterSet(client), client: client, usage: usageTracker, stallTimeout: upstreamStallTimeout}
}

// stallGuarded wraps a streaming upstream body so silence is fatal instead of
// permanent. Non-streaming exchanges are left alone: their bodies are short
// and the error surface is the client's own timeout.
func (s *Server) stallGuarded(body io.ReadCloser) io.ReadCloser {
	timeout := s.stallTimeout
	if timeout <= 0 {
		timeout = upstreamStallTimeout
	}
	return newStallCloser(body, timeout)
}
func (s *Server) Start(address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http != nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /v1/models", s.listModels)
	mux.HandleFunc("POST /v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("POST /v1/messages", s.messagesHandler)
	// Codex CLI 只支持 Responses 线协议，网关在这里把它转成 Chat Completions 转发。
	mux.HandleFunc("POST /v1/responses", s.responses)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	s.http = server
	go func() { _ = server.Serve(listener) }()
	return nil
}

// drainTimeout bounds how long Close waits for in-flight requests. Streaming
// completions are the long pole (p95 ~51s, p99 ~120s in the request log), so
// the window matches the upstream client timeout: a request that would have
// outlived its own upstream deadline is not worth keeping the process alive for.
const drainTimeout = 120 * time.Second

// Close stops the listener and drops the server reference, so a later Start
// listens again. Leaving the reference behind made Running report "running"
// after a stop and turned every later Start into a silent no-op, which left
// the gateway dead until the app was restarted.
//
// It drains rather than guillotines. The old server.Close() killed every
// active connection at once, so stopping the gateway — or a table-stakes
// wails-dev rebuild — landed mid-stream and the client saw a bare
// "socket connection was closed unexpectedly" instead of the rest of its
// completion. Shutdown lets a request in flight finish first.
func (s *Server) Close() error {
	s.mu.Lock()
	server := s.http
	s.http = nil
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		// Drained requests outlive the window (or the process is exiting):
		// fall back to the immediate close so the listener never leaks.
		return server.Close()
	}
	return nil
}
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.http != nil
}

// EffectiveMappings exposes the enabled client models the gateway currently
// serves, combining saved edits with automatic mappings. Templates use this so
// generated configs list exactly what the proxy can route.
func (s *Server) EffectiveMappings() []config.ModelMapping {
	out := make([]config.ModelMapping, 0, len(s.effectiveMappings()))
	for _, mapping := range s.effectiveMappings() {
		if _, ok := s.registry.Get(mapping.ProviderID); ok {
			out = append(out, mapping)
		}
	}
	return out
}

// listModels exposes the currently routable client model names in the OpenAI
// List Models response shape. A mapping is only useful to clients when both
// the mapping and its target provider are enabled.
func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireLocalKey(w, r); !ok {
		return
	}
	models := make([]Model, 0)
	for _, mapping := range s.effectiveMappings() {
		p, ok := s.registry.Get(mapping.ProviderID)
		if !ok {
			continue
		}
		models = append(models, Model{
			ID:      mapping.ClientModel,
			Object:  "model",
			Created: 0,
			OwnedBy: p.ID,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ModelList{Object: "list", Data: models})
}

// effectiveMappings combines saved edits with the automatic mappings displayed
// in the UI. A saved mapping for a provider/upstream model pair takes
// precedence, including a disabled mapping which intentionally suppresses its
// automatic counterpart.
func (s *Server) effectiveMappings() []config.ModelMapping {
	stored := s.mappings.List()
	byRoute := make(map[string]config.ModelMapping, len(stored))
	models := make([]config.ModelMapping, 0, len(stored))
	seenClientModels := make(map[string]struct{})
	for _, mapping := range stored {
		byRoute[mapping.ProviderID+"\x00"+mapping.UpstreamModel] = mapping
		p, ok := s.registry.Get(mapping.ProviderID)
		if !mapping.Enabled || !ok || !p.Enabled {
			continue
		}
		models = append(models, mapping)
		seenClientModels[mapping.ClientModel] = struct{}{}
	}
	for _, p := range s.registry.List() {
		if !p.Enabled {
			continue
		}
		for _, upstreamModel := range p.Models {
			if _, edited := byRoute[p.ID+"\x00"+upstreamModel]; edited {
				continue
			}
			clientModel := defaultClientModel(p, upstreamModel)
			if _, duplicate := seenClientModels[clientModel]; duplicate {
				continue
			}
			models = append(models, config.ModelMapping{
				ID:            "auto-" + p.ID + "-" + upstreamModel,
				ClientModel:   clientModel,
				ProviderID:    p.ID,
				UpstreamModel: upstreamModel,
				Enabled:       true,
			})
			seenClientModels[clientModel] = struct{}{}
		}
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ClientModel < models[j].ClientModel
	})
	return models
}

func (s *Server) resolveMapping(clientModel string) (config.ModelMapping, bool) {
	name := strings.TrimSpace(clientModel)
	for _, mapping := range s.effectiveMappings() {
		for _, candidate := range mapping.Names() {
			if candidate == name {
				return mapping, true
			}
		}
	}
	return config.ModelMapping{}, false
}

func defaultClientModel(p provider.Provider, upstreamModel string) string {
	if prefix := strings.TrimSpace(p.ModelPrefix); prefix != "" {
		return prefix + "/" + upstreamModel
	}
	return p.Name + " / " + upstreamModel
}

// maxRequestBodyBytes caps the client request the gateway reads. The old 4 MiB
// cap rejected ordinary agent traffic: a single base64 screenshot plus tool
// history passes it. 32 MiB matches the binding upstream limit (Anthropic's
// Messages API), so nothing a provider would have accepted is refused here;
// the cap survives only to bound memory against a runaway local client.
const maxRequestBodyBytes = 32 << 20

// decodeBody reads one JSON request body under maxRequestBodyBytes. Being over
// the cap is reported with the limit spelled out — the raw MaxBytesReader error
// ("http: request body too large") gave the client no idea what to shrink.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
				fmt.Sprintf("request body exceeds the %d MiB gateway limit", maxRequestBodyBytes>>20))
			return false
		}
		writeError(w, 400, "invalid_request_error", err.Error())
		return false
	}
	return true
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	tokenID, tokenName, authenticated := s.requireLocalKey(w, r)
	if !authenticated {
		return
	}
	started := time.Now()
	var input Request
	if !decodeBody(w, r, &input) {
		return
	}
	// OpenAI's newer "developer" role (pi and other modern clients) is not
	// accepted by every OpenAI-compatible upstream; system is its equivalent.
	for i := range input.Messages {
		if input.Messages[i].Role == "developer" {
			input.Messages[i].Role = "system"
		}
	}
	// Keep the client-facing request before replacing its model with the
	// upstream name. The log deliberately never includes the provider API key.
	requestBody, _ := json.Marshal(input)
	userAgent := r.Header.Get("User-Agent")
	mapping, ok := s.resolveMapping(input.Model)
	if !ok {
		writeError(w, 404, "model_not_found", "no enabled mapping for model "+input.Model)
		return
	}
	p, ok := s.registry.Get(mapping.ProviderID)
	if !ok || !p.Enabled {
		writeError(w, 503, "provider_unavailable", "target provider is unavailable")
		return
	}
	// The session identity is derived before a key is chosen: in session mode it
	// decides which key serves this conversation, so it must be known up front.
	session := sessionIdentity(r, firstMessageText(input.Messages, "system"), firstMessageText(input.Messages, "user"))
	var served requestCredential
	logEvent := func(success bool, responseBody, errorMessage string, tokens tokenUsage) {
		_ = s.usage.Record(usage.Event{
			TokenID: tokenID, TokenName: tokenName, ProviderID: p.ID, ProviderName: p.Name,
			ClientModel: mapping.ClientModel, UpstreamModel: mapping.UpstreamModel,
			UserAgent:   userAgent,
			RequestBody: string(requestBody), ResponseBody: responseBody,
			InputTokens: tokens.Input, OutputTokens: tokens.Output,
			CachedInputTokens: tokens.CachedInput, ReasoningOutputTokens: tokens.ReasoningOutput,
			Success:   success,
			LatencyMS: int(time.Since(started).Milliseconds()), ErrorMessage: errorMessage,
			CredentialID: served.id, CredentialName: served.name, CredentialMask: served.mask,
		})
	}
	input.Model = mapping.UpstreamModel
	response, lease, err := s.withCredential(r.Context(), p, session, func(key string) (*http.Response, error) {
		return s.adapters.For(p.Kind).Do(r.Context(), p, key, input)
	})
	if err != nil {
		status, kind, message := credentialError(err, p.Name)
		writeError(w, status, kind, message)
		logEvent(false, "", message, tokenUsage{})
		return
	}
	served.set(lease)
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		logEvent(false, string(body), fmt.Sprintf("upstream status %d", response.StatusCode), tokenUsage{})
		return
	}
	for _, header := range []string{"Content-Type", "Cache-Control"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	if input.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		buffer := make([]byte, 32*1024)
		var captured strings.Builder
		// A read error before EOF means the upstream stream was cut mid-flight.
		// The bytes piped so far still go to the client (which can tell the
		// stream never reached [DONE]), but the log must record the truncation:
		// success=1 with no [DONE] looked like a healthy request while the
		// client was busy retrying it.
		var streamErr string
		body := s.stallGuarded(response.Body)
		for {
			n, readErr := body.Read(buffer)
			if n > 0 {
				_, _ = w.Write(buffer[:n])
				captured.Write(buffer[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				streamErr = fmt.Sprintf("upstream stream ended early: %v", readErr)
				break
			}
		}
		_ = body.Close()
		if streamErr != "" {
			logEvent(false, captured.String(), streamErr, tokensFromResponse([]byte(captured.String())))
			return
		}
		logEvent(true, captured.String(), "", tokensFromResponse([]byte(captured.String())))
		return
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		writeError(w, 502, "upstream_error", "could not read upstream response")
		logEvent(false, "", "could not read upstream response", tokenUsage{})
		return
	}
	w.WriteHeader(200)
	_, _ = w.Write(body)
	logEvent(true, string(body), "", tokensFromResponse(body))
}

func (s *Server) messagesHandler(w http.ResponseWriter, r *http.Request) {
	// Claude Desktop sends x-api-key, not Authorization Bearer.
	token := r.Header.Get("x-api-key")
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
	}
	tokenID, tokenName, authenticated := s.keys.Lookup(token)
	if !authenticated {
		writeError(w, 401, "authentication_error", "invalid api key")
		return
	}
	started := time.Now()

	var input AnthropicRequest
	if !decodeBody(w, r, &input) {
		return
	}
	userAgent := r.Header.Get("User-Agent")

	mapping, ok := s.resolveMapping(input.Model)
	if !ok {
		writeError(w, 404, "model_not_found", "no enabled mapping for model "+input.Model)
		return
	}
	p, ok := s.registry.Get(mapping.ProviderID)
	if !ok || !p.Enabled {
		writeError(w, 503, "provider_unavailable", "target provider is unavailable")
		return
	}
	// Anthropic carries the system prompt as a top-level field, not a message,
	// so the conversation head is built from it plus the first user turn.
	session := sessionIdentity(r, extractAnthropicSystem(input.System), firstAnthropicMessageText(input.Messages, "user"))
	requestBody, _ := json.Marshal(input)

	var served requestCredential
	logEvent := func(success bool, responseBody, errorMessage string, tokens tokenUsage) {
		_ = s.usage.Record(usage.Event{
			TokenID: tokenID, TokenName: tokenName, ProviderID: p.ID, ProviderName: p.Name,
			ClientModel: mapping.ClientModel, UpstreamModel: mapping.UpstreamModel,
			UserAgent:   userAgent,
			RequestBody: string(requestBody), ResponseBody: responseBody,
			InputTokens: tokens.Input, OutputTokens: tokens.Output,
			CachedInputTokens: tokens.CachedInput, ReasoningOutputTokens: tokens.ReasoningOutput,
			Success:   success,
			LatencyMS: int(time.Since(started).Milliseconds()), ErrorMessage: errorMessage,
			CredentialID: served.id, CredentialName: served.name, CredentialMask: served.mask,
		})
	}

	// KindAnthropic upstream: passthrough (swap model name + auth, pipe body).
	if p.Kind == provider.KindAnthropic {
		s.anthropicPassthrough(w, r, p, session, mapping, input, logEvent, &served)
		return
	}

	// Other kinds: convert Anthropic -> OpenAI -> route -> convert response back.
	s.anthropicViaOpenAI(w, r, p, session, mapping, input, logEvent, &served)
}

// anthropicPassthrough forwards an Anthropic-format request to an Anthropic
// upstream with just the model name and auth swapped. Streaming is piped
// through. It builds its own request rather than going through an adapter, so
// the credential pool is reached via withCredential's closure, which is what
// keeps this path under the same rotation and failover rules as the others.
func (s *Server) anthropicPassthrough(w http.ResponseWriter, r *http.Request, p provider.Provider, session string, mapping config.ModelMapping, input AnthropicRequest, logEvent func(bool, string, string, tokenUsage), served *requestCredential) {
	input.Model = mapping.UpstreamModel
	body, err := json.Marshal(input)
	if err != nil {
		writeError(w, 500, "internal_error", "serialization failure")
		logEvent(false, "", err.Error(), tokenUsage{})
		return
	}

	upstreamURL := strings.TrimRight(p.BaseURL, "/") + "/v1/messages"
	resp, lease, err := s.withCredential(r.Context(), p, session, func(key string) (*http.Response, error) {
		// A fresh reader per attempt: the body is consumed by the first try, so
		// a retry built on the same reader would send an empty request.
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		return s.client.Do(req)
	})
	if err != nil {
		status, kind, message := credentialError(err, p.Name)
		writeError(w, status, kind, message)
		logEvent(false, "", message, tokenUsage{})
		return
	}
	served.set(lease)
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	if resp.StatusCode >= 300 {
		w.WriteHeader(resp.StatusCode)
		data, _ := io.ReadAll(resp.Body)
		_, _ = w.Write(data)
		logEvent(false, string(data), fmt.Sprintf("upstream status %d", resp.StatusCode), tokenUsage{})
		return
	}

	if input.Stream {
		w.Header().Set("X-Accel-Buffering", "no")
	}
	w.WriteHeader(resp.StatusCode)

	if input.Stream {
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		var captured strings.Builder
		// Same truncation bookkeeping as chatCompletions: a passthrough stream
		// that dies mid-flight is a failed exchange, not a successful one.
		var streamErr string
		body := s.stallGuarded(resp.Body)
		for {
			n, readErr := body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				captured.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				streamErr = fmt.Sprintf("upstream stream ended early: %v", readErr)
				break
			}
		}
		_ = body.Close()
		if streamErr != "" {
			logEvent(false, captured.String(), streamErr, tokensFromResponse([]byte(captured.String())))
			return
		}
		logEvent(true, captured.String(), "", tokensFromResponse([]byte(captured.String())))
		return
	}

	data, _ := io.ReadAll(resp.Body)
	_, _ = w.Write(data)
	logEvent(true, string(data), "", tokensFromResponse(data))
}

// anthropicViaOpenAI converts an Anthropic request to OpenAI format, routes
// through the appropriate adapter, then converts the response back.
// Streaming for non-Anthropic upstreams is not supported yet.
func (s *Server) anthropicViaOpenAI(w http.ResponseWriter, r *http.Request, p provider.Provider, session string, mapping config.ModelMapping, input AnthropicRequest, logEvent func(bool, string, string, tokenUsage), served *requestCredential) {
	system := extractAnthropicSystem(input.System)
	openAIMsgs := make([]Message, 0, len(input.Messages)+1)
	if system != "" {
		openAIMsgs = append(openAIMsgs, Message{Role: "system", Content: jsonString(system)})
	}
	for _, m := range input.Messages {
		openAIMsgs = append(openAIMsgs, anthropicMessageToOpenAI(m)...)
	}

	openAIReq := Request{
		Model:       mapping.UpstreamModel,
		Messages:    openAIMsgs,
		Temperature: input.Temperature,
		TopP:        input.TopP,
		Stream:      input.Stream,
		Tools:       anthropicToolsToOpenAI(input.Tools),
		ToolChoice:  anthropicToolChoiceToOpenAI(input.ToolChoice),
	}
	// Anthropic thinking shapes ({"type":"enabled",...}, Claude Code's
	// {"type":"adaptive"}) are not valid on OpenAI-compatible upstreams,
	// which validate type against enabled/disabled/auto; only the
	// reasoning_effort analogue below is forwarded.
	effort := thinkingToReasoningEffort(input.Thinking)
	if input.MaxTokens > 0 {
		openAIReq.MaxTokens = &input.MaxTokens
	}
	// A reasoning model spends its output budget on invisible thinking before
	// emitting text, so a tight max_tokens yields an empty answer (Claude Code's
	// permission classifier sends max_tokens:2112 and got content:[] at exactly
	// 2112 output tokens, then hung and retried). Below the floor the reasoning
	// analogue is dropped so the whole budget goes to the visible answer.
	if input.MaxTokens > 0 && input.MaxTokens < reasoningFloorMaxTokens {
		effort = ""
		openAIReq.MaxTokens = ptrTo(reasoningFloorMaxTokens)
	}
	if effort != "" {
		openAIReq.ReasoningEffort = &effort
	}
	if input.Stream {
		// Anthropic reports usage on its own stream; an OpenAI-compatible upstream
		// only sends it when asked, and a client that did not opt in gets a stream
		// with no usage at all — which left the log at zero tokens.
		openAIReq.StreamOptions = map[string]bool{"include_usage": true}
	}

	response, lease, err := s.withCredential(r.Context(), p, session, func(key string) (*http.Response, error) {
		return s.adapters.For(p.Kind).Do(r.Context(), p, key, openAIReq)
	})
	if err != nil {
		status, kind, message := credentialError(err, p.Name)
		writeError(w, status, kind, message)
		logEvent(false, "", message, tokenUsage{})
		return
	}
	served.set(lease)
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		data, _ := io.ReadAll(response.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
		logEvent(false, string(data), fmt.Sprintf("upstream status %d", response.StatusCode), tokenUsage{})
		return
	}

	if input.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(200)
		var captured strings.Builder
		tokens, streamErr := pipeOpenAIStreamToAnthropic(w, s.stallGuarded(response.Body), input.Model, &captured)
		if streamErr != nil {
			logEvent(false, captured.String(), streamErr.Error(), tokens)
			return
		}
		logEvent(true, captured.String(), "", tokens)
		return
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		writeError(w, 502, "upstream_error", "could not read upstream response")
		logEvent(false, "", "could not read upstream response", tokenUsage{})
		return
	}

	anthResp := openAIResponseToAnthropic(data, input.Model)
	out, _ := json.Marshal(anthResp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
	// Log what the client received (Anthropic shape, client model name), not the
	// upstream's OpenAI body — matching the request side, which is logged before
	// the model name is swapped.
	logEvent(true, string(out), "", tokensFromResponse(data))
}

// jsonString encodes s as a JSON string literal. Hand-building `"`+s+`"` breaks
// on any newline, quote or backslash the model or client puts in the prompt.
func jsonString(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return b
}

// anthropicBlock is the shared shape of Anthropic content blocks. Only one of
// the fields below is set per block type.
type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// anthropicMessageToOpenAI converts one Anthropic message into the OpenAI
// message(s) that carry the same information. An assistant turn with tool_use
// blocks becomes an assistant message with tool_calls, and each tool_result
// block becomes its own role:"tool" message — Anthropic packs several results
// into one user turn, OpenAI allows exactly one per message. Dropping these was
// why Claude Code saw its tools vanish and retried every call.
func anthropicMessageToOpenAI(m AnthropicMessage) []Message {
	var blocks []anthropicBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		// Plain string content (the common case) is not a block array.
		return []Message{{Role: m.Role, Content: m.Content}}
	}

	var text strings.Builder
	var toolCalls []map[string]any
	var results []Message
	for _, block := range blocks {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			arguments := block.Input
			if len(arguments) == 0 || string(arguments) == "null" {
				arguments = json.RawMessage(`{}`)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": string(arguments)},
			})
		case "tool_result":
			results = append(results, Message{Role: "tool", Content: jsonString(extractAnthropicText(block.Content)), ToolCallID: block.ToolUseID})
		}
	}

	// A tool result carries no text of its own; the assistant's tool_calls are
	// what the upstream needs to see, and empty-content turns are rejected.
	if text.Len() == 0 && len(toolCalls) == 0 {
		if len(results) > 0 {
			return results
		}
		return []Message{{Role: m.Role, Content: jsonString("")}}
	}
	message := Message{Role: m.Role}
	if text.Len() > 0 {
		message.Content = jsonString(text.String())
	}
	if len(toolCalls) > 0 {
		encoded, err := json.Marshal(toolCalls)
		if err == nil {
			message.ToolCalls = encoded
		}
	}
	return append([]Message{message}, results...)
}

// anthropicToolsToOpenAI rewrites Anthropic tool definitions ({name, description,
// input_schema}) into OpenAI ones ({type: "function", function: {...}}) and adds
// the "type": "object" that OpenAI-compatible upstreams require. Anything that
// is not an Anthropic-shaped tool (already OpenAI, or unparsable) is dropped
// rather than forwarded: a malformed entry fails the whole upstream request.
func anthropicToolsToOpenAI(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return raw
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		schema := tool.InputSchema
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		schema = withObjectType(schema)
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": schema,
			},
		})
	}
	if len(out) == 0 {
		// Not Anthropic-shaped (or already OpenAI): forward untouched rather than
		// silently dropping the client's tools.
		return raw
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return encoded
}

// withObjectType defaults a JSON schema with no "type" to an object schema;
// Anthropic schemas often omit it and upstreams reject tools without it.
func withObjectType(schema json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(schema, &fields); err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	if _, ok := fields["type"]; ok {
		return schema
	}
	var patched map[string]any
	if err := json.Unmarshal(schema, &patched); err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	patched["type"] = "object"
	encoded, err := json.Marshal(patched)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return encoded
}

// anthropicToolChoiceToOpenAI maps Anthropic's tool_choice (auto/any/tool) onto
// the OpenAI equivalents. "auto" or unknown values are left to the upstream
// default rather than forwarded in a shape it may reject.
func anthropicToolChoiceToOpenAI(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return raw
	}
	switch choice.Type {
	case "any":
		return json.RawMessage(`"required"`)
	case "tool":
		encoded, err := json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": choice.Name}})
		if err != nil {
			return nil
		}
		return encoded
	case "none":
		return json.RawMessage(`"none"`)
	default:
		return nil
	}
}

// reasoningFloorMaxTokens is the smallest max_tokens forwarded to a reasoning
// upstream. Claude Code's permission classifier asks for 2112; a reasoning
// model burns that entirely on invisible thinking and returns an empty answer.
const reasoningFloorMaxTokens = 8192

func ptrTo[T any](v T) *T { return &v }

// thinkingToReasoningEffort translates Anthropic's extended-thinking setting
// into the reasoning_effort an OpenAI-compatible upstream understands. Only
// adaptive-budget thinking has an effort analogue; a fixed budget or an
// unknown shape maps to nothing rather than a guess.
func thinkingToReasoningEffort(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var thinking struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &thinking); err != nil || thinking.Type != "enabled" {
		return ""
	}
	return "medium"
}

func extractAnthropicSystem(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []anthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil && len(blocks) > 0 {
		return blocks[0].Text
	}
	return ""
}

func extractAnthropicText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []anthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "text" {
				return b.Text
			}
		}
	}
	return ""
}

func openAIResponseToAnthropic(data []byte, clientModel string) AnthropicResponse {
	var openAI struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role      string          `json:"role"`
				Content   string          `json:"content"`
				ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}

	_ = json.Unmarshal(data, &openAI)

	resp := AnthropicResponse{
		ID:         openAI.ID,
		Type:       "message",
		Role:       "assistant",
		Model:      clientModel,
		StopReason: "end_turn",
		Content:    []AnthropicContent{},
	}

	if len(openAI.Choices) > 0 {
		c := openAI.Choices[0]
		if c.Message.Content != "" {
			resp.Content = append(resp.Content, AnthropicContent{Type: "text", Text: c.Message.Content})
		}
		for _, call := range openAIToolCalls(c.Message.ToolCalls) {
			resp.Content = append(resp.Content, AnthropicContent{Type: "tool_use", ID: call.ID, Name: call.Name, Input: json.RawMessage(call.Arguments)})
		}
		switch c.FinishReason {
		case "stop":
			resp.StopReason = "end_turn"
		case "length":
			resp.StopReason = "max_tokens"
		case "tool_calls":
			resp.StopReason = "tool_use"
		}
	}

	resp.Usage.Input = openAI.Usage.Input
	resp.Usage.Output = openAI.Usage.Output
	return resp
}

// openAIToolCall is one OpenAI tool call, streaming or complete.
type openAIToolCall struct {
	Index     int    `json:"index"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIToolCalls accepts both shapes OpenAI puts function data in: the nested
// {type, function:{name,arguments}} of completed responses and streams, and the
// flat {name, arguments} some OpenAI-compatible servers emit instead.
func openAIToolCalls(raw json.RawMessage) []openAIToolCall {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var entries []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Args     string `json:"arguments"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	out := make([]openAIToolCall, 0, len(entries))
	for _, entry := range entries {
		call := openAIToolCall{Index: entry.Index, ID: entry.ID, Name: entry.Name, Arguments: entry.Args}
		if entry.Function.Name != "" {
			call.Name = entry.Function.Name
		}
		if entry.Function.Arguments != "" {
			call.Arguments = entry.Function.Arguments
		}
		out = append(out, call)
	}
	return out
}

// pipeOpenAIStreamToAnthropic reads OpenAI SSE chunks from body and writes
// Anthropic SSE events to w, capturing the generated Anthropic SSE in captured.
// It returns the upstream token usage, which the synthesized Anthropic events
// do not carry (message_start is written before the upstream reports it), and
// the error that ended an incomplete stream: when the upstream body dies
// mid-stream the closing events must not be emitted, or the client sees a
// normal end-of-turn instead of a broken exchange and retries the request as
// if nothing had happened.
func pipeOpenAIStreamToAnthropic(w io.Writer, body io.ReadCloser, clientModel string, captured *strings.Builder) (tokenUsage, error) {
	flusher, flushable := w.(http.Flusher)
	msgID := fmt.Sprintf("msg_%x", time.Now().UnixNano())

	// tee captured alongside the response writer for usage logging.
	dest := w
	if captured != nil {
		dest = io.MultiWriter(w, captured)
	}

	anthropicSSE(dest, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []any{}, "model": clientModel,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	if flushable {
		flusher.Flush()
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	sawDone := false
	blockStarted := false
	thinkingStarted := false
	blockIndex := 0
	thinkingIndex := 0
	textIndex := 0
	var textBuf strings.Builder
	var finishReason string
	var usage tokenUsage
	type toolBlock struct {
		index   int
		started bool
		pending strings.Builder
	}
	tools := map[int]*toolBlock{}

	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		if data == "[DONE]" {
			sawDone = true
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string          `json:"content"`
					ReasoningContent string          `json:"reasoning_content"`
					Reasoning        string          `json:"reasoning"`
					ToolCalls        json.RawMessage `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *usagePayload `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		content := ""
		reasoning := ""
		var calls json.RawMessage
		if len(chunk.Choices) > 0 {
			content = chunk.Choices[0].Delta.Content
			reasoning = chunk.Choices[0].Delta.ReasoningContent
			if reasoning == "" {
				reasoning = chunk.Choices[0].Delta.Reasoning
			}
			calls = chunk.Choices[0].Delta.ToolCalls
			if chunk.Choices[0].FinishReason != "" {
				finishReason = chunk.Choices[0].FinishReason
			}
		}
		if chunk.Usage != nil {
			usage = usage.merge(chunk.Usage.tokenUsage())
		}

		if reasoning != "" && !thinkingStarted {
			thinkingStarted = true
			thinkingIndex = blockIndex
			blockIndex++
			anthropicSSE(dest, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": thinkingIndex,
				"content_block": map[string]any{
					"type": "thinking", "thinking": "", "signature": "",
				},
			})
		}
		if reasoning != "" {
			anthropicSSE(dest, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": thinkingIndex,
				"delta": map[string]any{"type": "thinking_delta", "thinking": reasoning},
			})
			if flushable {
				flusher.Flush()
			}
		}

		// Anthropic content blocks are sequential. Close the synthetic thinking
		// block before opening the first visible text or tool-use block.
		if thinkingStarted && (content != "" || len(calls) > 0) {
			anthropicSSE(dest, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": thinkingIndex,
				"delta": map[string]any{
					"type": "signature_delta", "signature": "agent-router-openai-reasoning",
				},
			})
			anthropicSSE(dest, "content_block_stop", map[string]any{
				"type": "content_block_stop", "index": thinkingIndex,
			})
			thinkingStarted = false
			if flushable {
				flusher.Flush()
			}
		}

		if content != "" && !blockStarted {
			blockStarted = true
			textIndex = blockIndex
			blockIndex++
			anthropicSSE(dest, "content_block_start", map[string]any{
				"type":          "content_block_start",
				"index":         textIndex,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			if flushable {
				flusher.Flush()
			}
		}

		if content != "" {
			textBuf.WriteString(content)
			anthropicSSE(dest, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": textIndex,
				"delta": map[string]any{"type": "text_delta", "text": content},
			})
			if flushable {
				flusher.Flush()
			}
		}

		for _, call := range openAIToolCalls(calls) {
			block, ok := tools[call.Index]
			if !ok {
				block = &toolBlock{index: blockIndex}
				blockIndex++
				tools[call.Index] = block
			}
			if !block.started {
				block.started = true
				anthropicSSE(dest, "content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         block.index,
					"content_block": map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}},
				})
			}
			// A tool's input arrives as a JSON string, so while the arguments are
			// still incomplete they would not parse. They are buffered and replayed
			// as one delta once the block closes, rather than streamed incrementally.
			block.pending.WriteString(call.Arguments)
			if flushable {
				flusher.Flush()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		// The upstream body died mid-stream. Anthropic's protocol has an error
		// event for exactly this; emitting it lets the client distinguish a
		// broken exchange from a normal end-of-turn instead of retrying blind.
		anthropicSSE(dest, "error", map[string]any{
			"type": "error", "error": map[string]any{"type": "api_error", "message": "upstream stream ended early: " + err.Error()},
		})
		if flushable {
			flusher.Flush()
		}
		return usage, fmt.Errorf("upstream stream ended early: %w", err)
	}
	if !sawDone {
		// No [DONE] and no scanner error: the body simply ended, which a
		// well-behaved OpenAI-compatible upstream never does on a live stream.
		// Some emit only a finish_reason chunk, so treat a finish as delivered
		// only when one actually arrived.
		if finishReason == "" {
			anthropicSSE(dest, "error", map[string]any{
				"type": "error", "error": map[string]any{"type": "api_error", "message": "upstream stream ended without a finish_reason"},
			})
			if flushable {
				flusher.Flush()
			}
			return usage, fmt.Errorf("upstream stream ended without a finish_reason")
		}
		sawDone = true
	}

	if thinkingStarted {
		anthropicSSE(dest, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": thinkingIndex,
			"delta": map[string]any{
				"type": "signature_delta", "signature": "agent-router-openai-reasoning",
			},
		})
		anthropicSSE(dest, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": thinkingIndex,
		})
		if flushable {
			flusher.Flush()
		}
	}

	if blockStarted {
		anthropicSSE(dest, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": textIndex,
		})
		if flushable {
			flusher.Flush()
		}
	}

	// Tool blocks close in the order they opened, so a client that lost an
	// earlier block cannot be left with a hole before a later one.
	blocks := make([]*toolBlock, 0, len(tools))
	for _, block := range tools {
		blocks = append(blocks, block)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].index < blocks[j].index })
	for _, block := range blocks {
		arguments := strings.TrimSpace(block.pending.String())
		if !json.Valid([]byte(arguments)) {
			arguments = "{}"
		}
		anthropicSSE(dest, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": block.index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": arguments},
		})
		anthropicSSE(dest, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": block.index,
		})
		if flushable {
			flusher.Flush()
		}
	}

	// The input side only exists here: an OpenAI-compatible upstream reports the
	// whole accounting in its final chunk, which is read after message_start was
	// already written. Anthropic's message_delta usage is cumulative, so the input
	// and cache counts are reported here too — otherwise the client shows zero
	// input tokens and the cached share of the context with it.
	anthropicSSE(dest, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   mapFinishReason(finishReason),
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":                usage.Input,
			"cache_read_input_tokens":     usage.CachedInput,
			"cache_creation_input_tokens": 0,
			"output_tokens":               usage.Output,
		},
	})
	if flushable {
		flusher.Flush()
	}

	anthropicSSE(dest, "message_stop", map[string]any{"type": "message_stop"})
	if flushable {
		flusher.Flush()
	}
	return usage, nil
}

func anthropicSSE(w io.Writer, event string, data map[string]any) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, mustJSON(data))
}

func mapFinishReason(r string) string {
	switch r {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return r
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// tokensFromResponse reads usage from an upstream response body. It supports a
// regular JSON body and SSE frames for both wire formats: OpenAI (usage in one
// chunk, when the client opted into stream_options.include_usage) and Anthropic
// (usage split across message_start's nested message.usage and message_delta's
// top-level usage).
type tokenUsage struct {
	Input           int
	Output          int
	CachedInput     int
	ReasoningOutput int
}

func (u tokenUsage) present() bool {
	return u.Input != 0 || u.Output != 0 || u.CachedInput != 0 || u.ReasoningOutput != 0
}

// merge keeps the larger of each field. Usage reaches the gateway split across
// frames, and some upstreams repeat cumulative totals per chunk, so the max is
// the safe way to combine them.
func (u tokenUsage) merge(v tokenUsage) tokenUsage {
	return tokenUsage{
		Input:           max(u.Input, v.Input),
		Output:          max(u.Output, v.Output),
		CachedInput:     max(u.CachedInput, v.CachedInput),
		ReasoningOutput: max(u.ReasoningOutput, v.ReasoningOutput),
	}
}

func tokensFromResponse(body []byte) tokenUsage {
	if usage, ok := usageFromJSON(body); ok {
		return usage
	}
	var merged tokenUsage
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		if usage, ok := usageFromJSON([]byte(line)); ok {
			merged = merged.merge(usage)
		}
	}
	return merged
}

// usagePayload carries both spellings of the same accounting. Anthropic counts
// cache reads/writes outside input_tokens, OpenAI counts cached inside
// prompt_tokens; usagePayload normalizes to the OpenAI convention so the cache
// hit rate stays a fraction of Input either way.
type usagePayload struct {
	Input           int `json:"prompt_tokens"`
	Output          int `json:"completion_tokens"`
	AnthropicInput  int `json:"input_tokens"`
	AnthropicOutput int `json:"output_tokens"`
	CacheRead       int `json:"cache_read_input_tokens"`
	CacheCreation   int `json:"cache_creation_input_tokens"`
	InputDetails    struct {
		Cached int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	OutputDetails struct {
		Reasoning int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (p usagePayload) tokenUsage() tokenUsage {
	return tokenUsage{
		Input:           p.Input + p.AnthropicInput + p.CacheRead + p.CacheCreation,
		Output:          max(p.Output, p.AnthropicOutput),
		CachedInput:     max(p.InputDetails.Cached, p.CacheRead),
		ReasoningOutput: p.OutputDetails.Reasoning,
	}
}

func usageFromJSON(body []byte) (tokenUsage, bool) {
	var payload struct {
		Usage   usagePayload `json:"usage"`
		Message struct {
			Usage usagePayload `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return tokenUsage{}, false
	}
	usage := payload.Usage.tokenUsage().merge(payload.Message.Usage.tokenUsage())
	return usage, usage.present()
}
func (s *Server) requireLocalKey(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if s.keys == nil {
		writeError(w, 401, "authentication_error", "local api key is not configured")
		return "", "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("api_key"))
	}
	id, name, ok := s.keys.Lookup(token)
	if !ok {
		writeError(w, 401, "authentication_error", "invalid local api key")
		return "", "", false
	}
	return id, name, true
}
func writeError(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": message, "type": kind}})
}
