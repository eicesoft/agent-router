package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"agent-router/backend/config"
	"agent-router/backend/provider"
	"agent-router/backend/secret"
	"agent-router/backend/usage"
)

type Server struct {
	registry *provider.Registry
	mappings *config.MappingStore
	secrets  secret.Store
	keys     KeyVerifier
	adapters AdapterSet
	usage    *usage.SQLiteTracker
	http     *http.Server
}

type KeyVerifier interface {
	Lookup(token string) (id, name string, ok bool)
}

func New(registry *provider.Registry, mappings *config.MappingStore, secrets secret.Store, usageTracker *usage.SQLiteTracker, keys KeyVerifier) *Server {
	return &Server{registry: registry, mappings: mappings, secrets: secrets, keys: keys, adapters: NewAdapterSet(&http.Client{Timeout: 120 * time.Second}), usage: usageTracker}
}
func (s *Server) Start(address string) error {
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
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(listener) }()
	return nil
}
func (s *Server) Close() error {
	if s.http == nil {
		return nil
	}
	return s.http.Close()
}
func (s *Server) Running() bool {
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
	for _, mapping := range s.effectiveMappings() {
		if mapping.ClientModel == clientModel {
			return mapping, true
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

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	tokenID, tokenName, authenticated := s.requireLocalKey(w, r)
	if !authenticated {
		return
	}
	started := time.Now()
	var input Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&input); err != nil {
		writeError(w, 400, "invalid_request_error", err.Error())
		return
	}
	// Keep the client-facing request before replacing its model with the
	// upstream name. The log deliberately never includes the provider API key.
	requestBody, _ := json.Marshal(input)
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
	key, err := s.secrets.Get(p.APIKeyRef)
	if err != nil {
		writeError(w, 424, "provider_credentials_missing", "no API key configured for "+p.Name)
		return
	}
	logEvent := func(success bool, responseBody, errorMessage string, tokens tokenUsage) {
		_ = s.usage.Record(usage.Event{
			TokenID: tokenID, TokenName: tokenName, ProviderID: p.ID, ProviderName: p.Name,
			ClientModel: mapping.ClientModel, UpstreamModel: mapping.UpstreamModel,
			RequestBody: string(requestBody), ResponseBody: responseBody,
			InputTokens: tokens.Input, OutputTokens: tokens.Output,
			CachedInputTokens: tokens.CachedInput, ReasoningOutputTokens: tokens.ReasoningOutput,
			Success:   success,
			LatencyMS: int(time.Since(started).Milliseconds()), ErrorMessage: errorMessage,
		})
	}
	input.Model = mapping.UpstreamModel
	response, err := s.adapters.For(p.Kind).Do(r.Context(), p, key, input)
	if err != nil {
		writeError(w, 502, "upstream_error", err.Error())
		logEvent(false, "", err.Error(), tokenUsage{})
		return
	}
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
		for {
			n, readErr := response.Body.Read(buffer)
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
				break
			}
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

// tokensFromResponse supports both a regular OpenAI-compatible response and
// SSE chunks emitted when a client opted into stream_options.include_usage.
type tokenUsage struct {
	Input           int
	Output          int
	CachedInput     int
	ReasoningOutput int
}

func (u tokenUsage) present() bool {
	return u.Input != 0 || u.Output != 0 || u.CachedInput != 0 || u.ReasoningOutput != 0
}

func tokensFromResponse(body []byte) tokenUsage {
	if usage, ok := usageFromJSON(body); ok {
		return usage
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		if usage, ok := usageFromJSON([]byte(line)); ok {
			return usage
		}
	}
	return tokenUsage{}
}

func usageFromJSON(body []byte) (tokenUsage, bool) {
	var payload struct {
		Usage struct {
			Input        int `json:"prompt_tokens"`
			Output       int `json:"completion_tokens"`
			InputDetails struct {
				Cached int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			OutputDetails struct {
				Reasoning int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return tokenUsage{}, false
	}
	usage := tokenUsage{
		Input: payload.Usage.Input, Output: payload.Usage.Output,
		CachedInput:     payload.Usage.InputDetails.Cached,
		ReasoningOutput: payload.Usage.OutputDetails.Reasoning,
	}
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
