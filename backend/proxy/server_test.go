package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-router/backend/config"
	"agent-router/backend/credential"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

type fakeSecrets map[string]string

func (f fakeSecrets) Set(string, string) error       { return nil }
func (f fakeSecrets) Get(key string) (string, error) { return f[key], nil }
func (f fakeSecrets) Delete(string) error            { return nil }

// fakeStore is a secret.Store the credential pool can actually provision, which
// the read-only fakeSecrets above cannot (its Set is a no-op). Tests keep using
// fakeSecrets for the legacy single-key fixture and this for pooled keys.
type fakeStore map[string]string

func (s fakeStore) Set(account, value string) error { s[account] = value; return nil }
func (s fakeStore) Get(account string) (string, error) {
	value, ok := s[account]
	if !ok {
		return "", errors.New("secret not found")
	}
	return value, nil
}
func (s fakeStore) Delete(account string) error { delete(s, account); return nil }

// newTestServer wires the gateway over a real credential pool. The pre-pool
// fakeSecrets map describes legacy credentials: each entry is stored under its
// own reference and adopted by the pool on first use, which is exactly the
// upgrade path a single-key install takes.
func newTestServer(db *sql.DB, registry *provider.Registry, mappings *config.MappingStore, secrets fakeSecrets, tracker *usage.SQLiteTracker, keys KeyVerifier) *Server {
	store := fakeStore{}
	for account, value := range secrets {
		store[account] = value
	}
	pool, err := credential.NewPool(db, store)
	if err != nil {
		panic(err)
	}
	return New(registry, mappings, pool, tracker, keys)
}

type fakeKeys struct{ valid string }

func (f fakeKeys) Lookup(token string) (string, string, bool) {
	return "key-test", "测试 Token", token != "" && token == f.valid
}

func TestChatCompletionsMapsAndForwardsCompatibleRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("missing authorization")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"upstream-model"`) {
			t.Errorf("mapping was not applied: %s", body)
		}
		// pi and other modern clients send the OpenAI "developer" role; upstreams
		// that only know system/assistant/user/tool must never see it verbatim.
		if strings.Contains(string(body), `"role":"developer"`) {
			t.Errorf("developer role leaked upstream: %s", body)
		}
		if !strings.Contains(string(body), `"role":"system"`) {
			t.Errorf("developer role was not normalized to system: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}}`))
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Aliases: []string{"client-alias"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","messages":[{"role":"developer","content":"you are helpful"},{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	req.Header.Set("User-Agent", "pi/0.85.1")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "chatcmpl-1") {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	if got := usage.NewSQLiteTracker(db).Summary(); got.Requests != 1 || got.InputTokens != 5 || got.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", got)
	}
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 || logs.Items[0].TokenID != "key-test" || logs.Items[0].TokenName != "测试 Token" || logs.Items[0].ProviderName != "Test" || logs.Items[0].ClientModel != "client-model" || logs.Items[0].UpstreamModel != "upstream-model" || !strings.HasPrefix(logs.Items[0].RequestBody, `{"model":"client-model"`) || strings.Contains(logs.Items[0].RequestBody, `"content":"hello"`) || logs.Items[0].InputTokens != 5 || logs.Items[0].OutputTokens != 3 || logs.Items[0].CachedInputTokens != 2 || logs.Items[0].ReasoningOutputTokens != 1 {
		t.Fatalf("unexpected request log: %+v", logs.Items)
	}
	if logs.Items[0].UserAgent != "pi/0.85.1" {
		t.Fatalf("user agent not recorded: %+v", logs.Items[0])
	}
	full, err := usage.NewSQLiteTracker(db).GetRequestLog(logs.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.RequestBody, `"content":"hello"`) {
		t.Fatalf("unexpected full request body: %s", full.RequestBody)
	}
}

// The alias is a second name for the same route: the request must take the
// identical path as the client model name, and the log still records the
// canonical client model rather than whatever name the caller happened to use.
func TestChatCompletionsRoutesAliasToUpstreamModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"upstream-model"`) {
			t.Errorf("alias was not resolved to the upstream model: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-alias","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Aliases: []string{" client-alias ", "client-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 || logs.Items[0].ClientModel != "client-model" || logs.Items[0].UpstreamModel != "upstream-model" {
		t.Fatalf("alias request was not logged against its mapping: %+v", logs.Items)
	}
}

func TestTokensFromResponseReadsSSEUsageDetails(t *testing.T) {
	body := []byte("data: {\"choices\":[],\"usage\":null}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":88,\"completion_tokens\":2526,\"prompt_tokens_details\":{\"cached_tokens\":12},\"completion_tokens_details\":{\"reasoning_tokens\":878}}}\n\ndata: [DONE]\n")
	got := tokensFromResponse(body)
	if got.Input != 88 || got.Output != 2526 || got.CachedInput != 12 || got.ReasoningOutput != 878 {
		t.Fatalf("unexpected token usage: %+v", got)
	}
}

func TestListModelsReturnsOnlyRoutableMappings(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "enabled", Name: "Enabled", BaseURL: "https://example.com", ModelPrefix: "local", Models: []string{"automatic-model", "edited-upstream", "disabled-upstream"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "disabled", Name: "Disabled", BaseURL: "https://example.org", Models: []string{"upstream-model"}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []config.ModelMapping{
		{ID: "routable", ClientModel: "mapped-model", ProviderID: "enabled", UpstreamModel: "edited-upstream", Aliases: []string{"mapped-alias"}, Enabled: true},
		{ID: "mapping-disabled", ClientModel: "disabled-mapping", ProviderID: "enabled", UpstreamModel: "disabled-upstream", Aliases: []string{"disabled-alias"}, Enabled: false},
		{ID: "provider-disabled", ClientModel: "unavailable-model", ProviderID: "disabled", UpstreamModel: "upstream-model", Enabled: true},
	} {
		if _, err := mappings.Save(mapping); err != nil {
			t.Fatal(err)
		}
	}

	s := newTestServer(db, registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.listModels(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content type = %q", contentType)
	}
	var result ModelList
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Object != "list" {
		t.Fatalf("object = %q", result.Object)
	}
	var found bool
	var foundAutomatic bool
	for _, model := range result.Data {
		if model.ID == "mapped-model" {
			found = true
			if model.Object != "model" || model.OwnedBy != "enabled" || model.Created != 0 {
				t.Fatalf("mapped model = %#v", model)
			}
		}
		if model.ID == "local/automatic-model" {
			foundAutomatic = true
			if model.Object != "model" || model.OwnedBy != "enabled" || model.Created != 0 {
				t.Fatalf("automatic model = %#v", model)
			}
		}
		if model.ID == "disabled-mapping" || model.ID == "unavailable-model" || model.ID == "local/edited-upstream" || model.ID == "local/disabled-upstream" {
			t.Fatalf("unroutable model returned: %#v", model)
		}
		// 别名只用于命中路由，不是网关对外宣告的模型名：它不该出现在
		// /v1/models，也不该被生成的 CLI 配置（同一份 EffectiveMappings）列出。
		if model.ID == "mapped-alias" || model.ID == "disabled-alias" {
			t.Fatalf("alias leaked into the model list: %#v", model)
		}
	}
	if !found {
		t.Fatalf("mapped-model missing from response: %#v", result.Data)
	}
	if !foundAutomatic {
		t.Fatalf("automatic model missing from response: %#v", result.Data)
	}
}

func TestEffectiveMappingsKeepsAliasesFromFailoverChain(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []provider.Provider{
		{ID: "primary", Name: "Primary", BaseURL: "https://primary.example.com", Models: []string{"primary-model"}, Enabled: true},
		{ID: "backup", Name: "Backup", BaseURL: "https://backup.example.com", Models: []string{"backup-model"}, Enabled: true},
	} {
		if _, err := registry.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []config.ModelMapping{
		{ID: "a-primary-route", ClientModel: "shared-model", ProviderID: "primary", UpstreamModel: "primary-model", Enabled: true},
		{ID: "z-backup-route", ClientModel: "shared-model", ProviderID: "backup", UpstreamModel: "backup-model", Aliases: []string{"codex-alias"}, Enabled: true},
	} {
		if _, err := mappings.Save(mapping); err != nil {
			t.Fatal(err)
		}
	}

	s := newTestServer(db, registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{})
	var got *config.ModelMapping
	for _, mapping := range s.EffectiveMappings() {
		if mapping.ClientModel == "shared-model" {
			got = &mapping
			break
		}
	}
	if got == nil {
		t.Fatal("shared-model missing from effective mappings")
	}
	if got.ProviderID != "primary" || len(got.Aliases) != 1 || got.Aliases[0] != "codex-alias" {
		t.Fatalf("effective mapping = %+v, want primary route carrying the backup alias", got)
	}
}

func TestLocalKeyIsRequired(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	unauthorized := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	s.listModels(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d", response.Code)
	}
	wrong := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	wrong.Header.Set("Authorization", "Bearer wrong")
	response = httptest.NewRecorder()
	s.listModels(response, wrong)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d", response.Code)
	}
	missing := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"anything","messages":[]}`))
	response = httptest.NewRecorder()
	s.chatCompletions(response, missing)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("chat without key status = %d", response.Code)
	}
}

// A system prompt or message containing a newline, quote or backslash must
// survive the Anthropic -> OpenAI conversion, and Anthropic tool definitions
// must come out in OpenAI shape: "type" is a required property of tools[0]
// upstream, and hand-built JSON string literals emitted raw control characters
// that surfaced to the client as a 502.
func TestAnthropicConversionEscapesTextAndReshapesTools(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("upstream body is not valid JSON: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-3","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","system":"line1\nline2 \"quoted\"","messages":[{"role":"user","content":"back\\slash"}],"max_tokens":16,
		"tools":[{"name":"Agent","description":"launch one","input_schema":{"properties":{"prompt":{"type":"string"}}}}],"tool_choice":{"type":"tool","name":"Agent"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var messages []struct{ Role, Content string }
	if err := json.Unmarshal(received["messages"], &messages); err != nil {
		t.Fatalf("bad messages: %v", err)
	}
	if len(messages) != 2 || messages[0].Content != "line1\nline2 \"quoted\"" || messages[1].Content != `back\slash` {
		t.Fatalf("content not preserved: %+v", messages)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(received["tools"], &tools); err != nil {
		t.Fatalf("bad tools: %v", err)
	}
	if len(tools) != 1 || string(tools[0]["type"]) != `"function"` {
		t.Fatalf("tools not reshaped for OpenAI: %s", received["tools"])
	}
	var function struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(tools[0]["function"], &function); err != nil {
		t.Fatalf("bad function: %v", err)
	}
	if function.Name != "Agent" || !strings.Contains(string(function.Parameters), `"type":"object"`) {
		t.Fatalf("schema without a type must default to object: %s", tools[0]["function"])
	}
	if !strings.Contains(string(received["tool_choice"]), `"function"`) {
		t.Fatalf("tool_choice not mapped: %s", received["tool_choice"])
	}

	// The log keeps what the Anthropic client received, not the OpenAI body that
	// crossed the wire upstream.
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("want 1 logged request, got %d", len(logs.Items))
	}
	full, err := usage.NewSQLiteTracker(db).GetRequestLog(logs.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.ResponseBody, `"type":"message"`) || strings.Contains(full.ResponseBody, `"object":"chat.completion"`) {
		t.Fatalf("response log is not the client-facing Anthropic body: %s", full.ResponseBody)
	}
}

func TestChatCompletionsForwardsToolParameters(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-2","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"file.txt"}],
		"tools":[{"type":"function","function":{"name":"codegraph","description":"explore code","parameters":{"type":"object","properties":{}}}}],
		"tool_choice":"auto","parallel_tool_calls":false,"top_p":0.9,"stop":["END"],"seed":42,
		"frequency_penalty":0.5,"presence_penalty":0.25,"response_format":{"type":"text"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	for _, field := range []string{"tools", "tool_choice", "parallel_tool_calls", "top_p", "stop", "seed", "frequency_penalty", "presence_penalty", "response_format"} {
		if _, ok := received[field]; !ok {
			t.Errorf("upstream request missing %q: %s", field, received)
		}
	}
	var history []map[string]json.RawMessage
	if err := json.Unmarshal(received["messages"], &history); err != nil {
		t.Fatalf("bad messages: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("messages = %s", received["messages"])
	}
	if string(history[1]["tool_calls"]) == "" || !strings.Contains(string(history[1]["tool_calls"]), `"call_1"`) {
		t.Errorf("assistant tool_calls dropped: %s", history[1])
	}
	if string(history[2]["tool_call_id"]) != `"call_1"` {
		t.Errorf("tool_call_id dropped: %s", history[2])
	}
	if string(received["tool_choice"]) != `"auto"` {
		t.Errorf("tool_choice = %s", received["tool_choice"])
	}
	if string(received["model"]) != `"upstream-model"` {
		t.Errorf("model = %s", received["model"])
	}
}

// Claude Code sends tool history as content blocks: tool_use in the assistant
// turn, tool_result in the following user turn. Both used to be flattened on
// the way to an OpenAI upstream, so every tool call looked like it never
// happened and the model retried the same call forever.
func TestAnthropicToolHistoryBecomesOpenAIToolMessages(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("upstream body is not valid JSON: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-4","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	// Two tool_result blocks in one user turn: OpenAI allows only one per message.
	payload := `{"model":"client-model","max_tokens":16,"messages":[
		{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"call_1","name":"bash","input":{"command":"ls"}},{"type":"tool_use","id":"call_2","name":"read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"file.txt"},{"type":"tool_result","tool_use_id":"call_2","content":[{"type":"text","text":"line one"}]}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}

	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(received["messages"], &messages); err != nil {
		t.Fatalf("bad messages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("want assistant + 2 tool messages, got %s", received["messages"])
	}
	if string(messages[0]["role"]) != `"assistant"` || !strings.Contains(string(messages[0]["tool_calls"]), `"call_2"`) {
		t.Fatalf("assistant tool_use not converted: %s", messages[0])
	}
	if !strings.Contains(string(messages[0]["content"]), "checking") {
		t.Fatalf("assistant text dropped: %s", messages[0])
	}
	for i, want := range []string{"call_1", "line one"} {
		message := messages[i+1]
		if string(message["role"]) != `"tool"` || string(message["tool_call_id"]) != `"`+want+`"` && !strings.Contains(string(message["content"]), want) {
			t.Fatalf("tool result %d not converted: %s", i, message)
		}
	}

	// The response side must answer with tool_use blocks, not an empty text block.
	var anthResp struct {
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &anthResp); err != nil {
		t.Fatalf("bad anthropic response: %v: %s", err, response.Body.String())
	}
	if len(anthResp.Content) != 1 || anthResp.Content[0].Type != "tool_use" || anthResp.Content[0].Name != "bash" {
		t.Fatalf("tool_calls not converted to tool_use: %s", response.Body.String())
	}
	if !strings.Contains(string(anthResp.Content[0].Input), `"command":"ls"`) {
		t.Fatalf("tool input lost: %s", anthResp.Content[0].Input)
	}
	if anthResp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %s", anthResp.StopReason)
	}
}

// Anthropic clients expect tool_use to arrive as content blocks. OpenAI streams
// arguments as string fragments, and only the complete JSON is a valid tool
// input, so the fragments are buffered into one input_json_delta per block.
func TestOpenAIStreamToolCallsBecomeAnthropicToolUseEvents(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"thinking"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"comm"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"read","arguments":"{}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	var captured strings.Builder
	recorder := httptest.NewRecorder()
	pipeOpenAIStreamToAnthropic(recorder, io.NopCloser(strings.NewReader(stream)), "client-model", &captured)

	var events []struct {
		Event string
		Data  map[string]json.RawMessage
	}
	for _, frame := range strings.Split(strings.TrimSpace(captured.String()), "\n\n") {
		lines := strings.Split(frame, "\n")
		if len(lines) != 2 {
			t.Fatalf("unexpected SSE frame: %q", frame)
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &data); err != nil {
			t.Fatalf("bad SSE data: %v: %s", err, lines[1])
		}
		events = append(events, struct {
			Event string
			Data  map[string]json.RawMessage
		}{strings.TrimPrefix(lines[0], "event: "), data})
	}

	var starts []map[string]json.RawMessage
	var deltas []map[string]json.RawMessage
	var stops []string
	text := ""
	for _, event := range events {
		if textDelta, ok := event.Data["delta"]; ok && strings.Contains(string(textDelta), "text_delta") {
			var delta struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(textDelta, &delta)
			text += delta.Text
		}
		switch event.Event {
		case "content_block_start":
			var block map[string]json.RawMessage
			_ = json.Unmarshal(event.Data["content_block"], &block)
			starts = append(starts, block)
		case "content_block_delta":
			deltas = append(deltas, event.Data)
		case "content_block_stop":
			stops = append(stops, string(event.Data["index"]))
		}
	}

	if text != "thinking" {
		t.Fatalf("text not streamed: %q", captured.String())
	}
	if len(starts) != 3 || string(starts[0]["type"]) != `"text"` {
		t.Fatalf("want text block + 2 tool_use blocks, got %d", len(starts))
	}
	if string(starts[1]["type"]) != `"tool_use"` || string(starts[1]["name"]) != `"bash"` || string(starts[2]["name"]) != `"read"` {
		t.Fatalf("tool blocks wrong: %v", starts)
	}
	// Each tool call gets its own tool_use block: index 0 holds the text.
	for i, want := range []string{"0", "1", "2"} {
		if stops[i] != want {
			t.Fatalf("blocks not closed in order: %v", stops)
		}
	}
	inputs := map[int]string{}
	for _, delta := range deltas {
		var payload struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
		}
		// The block index is an event field, not part of the delta payload.
		_ = json.Unmarshal(delta["index"], &payload.Index)
		if err := json.Unmarshal(delta["delta"], &payload); err != nil || payload.Type != "input_json_delta" {
			continue
		}
		var args struct {
			Partial string `json:"partial_json"`
		}
		_ = json.Unmarshal(delta["delta"], &args)
		partial := args.Partial
		if !json.Valid([]byte(partial)) {
			t.Fatalf("tool arguments not valid JSON: %s", partial)
		}
		inputs[payload.Index] = partial
	}
	if !strings.Contains(inputs[1], `"command":"ls"`) || inputs[2] != "{}" {
		t.Fatalf("tool arguments never emitted: %s", captured.String())
	}
}

// Reasoning models commonly stream their visible progress in
// delta.reasoning_content before they emit delta.content. Dropping those
// chunks leaves Claude Code with no downstream events for the whole reasoning
// phase, so the UI appears frozen until the final answer starts.
func TestOpenAIStreamReasoningBecomesAnthropicThinkingEvents(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"Inspecting styles..."},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":" found the selector."},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":"Fixed."},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	var captured strings.Builder
	recorder := httptest.NewRecorder()
	_, err := pipeOpenAIStreamToAnthropic(recorder, io.NopCloser(strings.NewReader(stream)), "client-model", &captured)
	if err != nil {
		t.Fatal(err)
	}

	body := captured.String()
	wants := []string{
		`"content_block":{"signature":"","thinking":"","type":"thinking"}`,
		`"delta":{"thinking":"Inspecting styles...","type":"thinking_delta"}`,
		`"delta":{"thinking":" found the selector.","type":"thinking_delta"}`,
		`"delta":{"signature":"agent-router-openai-reasoning","type":"signature_delta"}`,
		`"content_block":{"text":"","type":"text"}`,
		`"delta":{"text":"Fixed.","type":"text_delta"}`,
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in converted stream:\n%s", want, body)
		}
	}
	if strings.Index(body, `"type":"thinking"`) > strings.Index(body, `"type":"text"`) {
		t.Fatalf("thinking block must precede text block:\n%s", body)
	}
}

// Thinking and reasoning controls must survive the gateway. pi/omp-style
// clients send reasoning_effort or thinking on /v1/chat/completions, Claude
// Code sends thinking on /v1/messages. All three paths re-marshal the request
// (struct or map), so any field missing from the Go types is silently stripped.
func TestChatCompletionsForwardsThinkingParameters(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-6","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","messages":[{"role":"user","content":"hi"}],
		"reasoning_effort":"high","thinking":{"type":"enabled","budget_tokens":2048},
		"chat_template_kwargs":{"enable_thinking":true},"enable_thinking":true,"verbosity":"low"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if string(received["reasoning_effort"]) != `"high"` {
		t.Errorf("reasoning_effort stripped: %s", received["reasoning_effort"])
	}
	if !strings.Contains(string(received["thinking"]), `"budget_tokens":2048`) {
		t.Errorf("thinking stripped: %s", received["thinking"])
	}
	if !strings.Contains(string(received["chat_template_kwargs"]), `"enable_thinking":true`) {
		t.Errorf("chat_template_kwargs stripped: %s", received["chat_template_kwargs"])
	}
	if string(received["enable_thinking"]) != "true" {
		t.Errorf("enable_thinking stripped: %s", received["enable_thinking"])
	}
	if string(received["verbosity"]) != `"low"` {
		t.Errorf("verbosity stripped: %s", received["verbosity"])
	}
}

// An Anthropic client's thinking goes verbatim to an Anthropic upstream, and
// maps to reasoning_effort for an OpenAI-compatible one.
func TestMessagesHandlerForwardsAndMapsThinking(t *testing.T) {
	var anthropicReceived map[string]json.RawMessage
	anthropicUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &anthropicReceived); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"upstream-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer anthropicUpstream.Close()

	var openAIReceived map[string]json.RawMessage
	openAIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &openAIReceived); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-7","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer openAIUpstream.Close()

	newStack := func(baseURL, kind string) (*Server, func()) {
		db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
		if err != nil {
			t.Fatal(err)
		}
		registry, err := provider.NewRegistry(db)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.Kind(kind), BaseURL: baseURL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		mappings, err := config.NewMappingStore(db)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
		return s, func() { db.Close() }
	}

	// max_tokens stays above reasoningFloorMaxTokens so the effort mapping (not
	// the small-budget floor path) is what's under test here.
	payload := `{"model":"client-model","max_tokens":32768,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`

	s, closeDB := newStack(anthropicUpstream.URL, string(provider.KindAnthropic))
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)
	closeDB()
	if response.Code != http.StatusOK {
		t.Fatalf("anthropic upstream status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(string(anthropicReceived["thinking"]), `"budget_tokens":4096`) {
		t.Errorf("thinking stripped on anthropic passthrough: %s", anthropicReceived["thinking"])
	}

	s, closeDB = newStack(openAIUpstream.URL, string(provider.KindCompatible))
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response = httptest.NewRecorder()
	s.messagesHandler(response, req)
	closeDB()
	if response.Code != http.StatusOK {
		t.Fatalf("openai upstream status = %d: %s", response.Code, response.Body.String())
	}
	if string(openAIReceived["reasoning_effort"]) != `"medium"` {
		t.Errorf("thinking not mapped to reasoning_effort: %s", openAIReceived["reasoning_effort"])
	}

	// Claude Code sends {"type":"adaptive"}, which OpenAI-compatible upstreams
	// validate against enabled/disabled/auto and reject; it must not leak through.
	payload = `{"model":"client-model","max_tokens":16,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`
	openAIReceived = nil
	s, closeDB = newStack(openAIUpstream.URL, string(provider.KindCompatible))
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response = httptest.NewRecorder()
	s.messagesHandler(response, req)
	closeDB()
	if response.Code != http.StatusOK {
		t.Fatalf("openai upstream status = %d: %s", response.Code, response.Body.String())
	}
	if _, ok := openAIReceived["thinking"]; ok {
		t.Errorf("anthropic thinking leaked to openai upstream: %s", openAIReceived["thinking"])
	}
}

// An OpenAI-compatible upstream that dies mid-stream must not be translated
// into a well-formed Anthropic message: without the guard the gateway emitted
// message_delta + message_stop for whatever partial data it had, so Claude Code
// saw a complete-looking turn, retried blind, and the log said success while
// the user watched "Waiting for API response". The truncation must surface as
// an Anthropic error event and a failed log row.
func TestTruncatedOpenAIStreamBecomesAnthropicErrorEvent(t *testing.T) {
	// No [DONE], no finish_reason: the body simply ends, the way a wedged
	// proxy or a dropped upstream connection ends it.
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"partial"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"comm"}}]},"finish_reason":null}]}`,
		"",
	}, "\n\n")

	var captured strings.Builder
	recorder := httptest.NewRecorder()
	_, err := pipeOpenAIStreamToAnthropic(recorder, io.NopCloser(strings.NewReader(stream)), "client-model", &captured)
	if err == nil {
		t.Fatalf("truncated stream reported as complete: %s", captured.String())
	}

	body := captured.String()
	if strings.Contains(body, "event: message_stop") {
		t.Fatalf("message_stop emitted for a truncated stream: %s", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "ended without a finish_reason") {
		t.Fatalf("no error event for the client: %s", body)
	}
}

// The log must tell truncation and success apart: a streaming /v1/messages
// request whose upstream body dies mid-flight is a failed exchange even though
// bytes already reached the client.
func TestTruncatedUpstreamStreamIsLoggedAsFailed(t *testing.T) {
	// The handler returns when the upstream connection drops, so the server
	// must outlive the handler: close via a background timer, not defer.
	release := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-8","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		// Hold the body open, then let the server drop the connection without
		// [DONE] — the mid-stream death the proxy produced in the field.
		<-release
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)

	body := response.Body.String()
	if strings.Contains(body, "event: message_stop") {
		t.Fatalf("message_stop emitted for a truncated stream: %s", body)
	}
	if !strings.Contains(body, "event: error") {
		t.Fatalf("client got no error event: %s", body)
	}
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("want 1 logged request, got %d", len(logs.Items))
	}
	if logs.Items[0].Success || logs.Items[0].ErrorMessage == "" {
		t.Fatalf("truncated stream logged as successful: %+v", logs.Items[0])
	}
}

// An Anthropic upstream that never sends message_stop must not be converted
// into a complete-looking OpenAI stream: [DONE] would tell the client a
// finish_reason arrived that never did.
func TestTruncatedAnthropicStreamIsNotTerminated(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		"", // no message_delta, no message_stop
	}, "\n")
	var out strings.Builder
	err := anthropicStreamToOpenAI(strings.NewReader(stream), "client-model", &out)
	if err == nil {
		t.Fatalf("truncated anthropic stream reported as complete: %s", out.String())
	}
	if strings.Contains(out.String(), "data: [DONE]") {
		t.Fatalf("[DONE] emitted for a truncated stream: %s", out.String())
	}
}

// 演练场发的是 reasoning_effort，不是 Anthropic 的 thinking。要让它对 Anthropic
// 系上游生效，适配器必须把档位换算成 thinking 预算，并抬高 max_tokens（Anthropic
// 要求 max_tokens 大于 budget_tokens）。
func TestAnthropicAdapterMapsReasoningEffortToThinkingBudget(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_3","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"upstream-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	request := Request{
		Model: "upstream-model", Messages: []Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		ReasoningEffort: ptrTo("high"),
	}
	if _, err := (Anthropic{Client: upstream.Client()}).Do(context.Background(), provider.Provider{
		ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test",
	}, "sk-test", request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(received["thinking"]), `"budget_tokens":16384`) {
		t.Fatalf("effort not mapped to a thinking budget: %s", received["thinking"])
	}
	if len(received["max_tokens"]) == 0 || string(received["max_tokens"]) == "1024" {
		t.Fatalf("max_tokens not raised for extended thinking: %s", received["max_tokens"])
	}
}

// Anthropic 上游的思考增量必须转成 OpenAI 的 reasoning_content，否则演练场在
// Anthropic 系提供商上只看到正文，思考块永远空着。
func TestAnthropicStreamRelaysThinkingDeltas(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Inspecting"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
	}, "\n")
	var out strings.Builder
	if err := anthropicStreamToOpenAI(strings.NewReader(stream), "client-model", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"reasoning_content":"Inspecting"`) {
		t.Fatalf("thinking delta dropped: %s", out.String())
	}
	if !strings.Contains(out.String(), `"content":"hi"`) {
		t.Fatalf("text delta missing: %s", out.String())
	}
}

// SSE 规范里冒号后的空格是可选的，阿里云 token-plan 的 Anthropic 端点写的是
// "data:{...}"。按 "data: " 判断会把整条流读成空流：客户端只看到连接断掉，
// 报 finish_reason 缺失，而事件其实一个不少。
func TestAnthropicStreamWithoutSpaceAfterDataColon(t *testing.T) {
	stream := strings.Join([]string{
		`event:message_start`,
		`data:{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event:content_block_delta`,
		`data:{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event:message_delta`,
		`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
		``,
		`event:message_stop`,
		`data:{"type":"message_stop"}`,
	}, "\n")
	var out strings.Builder
	if err := anthropicStreamToOpenAI(strings.NewReader(stream), "client-model", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"content":"hi"`) || !strings.Contains(out.String(), `"finish_reason":"stop"`) || !strings.Contains(out.String(), "data: [DONE]") {
		t.Fatalf("space-less SSE stream not converted: %s", out.String())
	}
}

// OpenAI 把工具结果放在独立的 "tool" 消息里，Anthropic 只认 user 消息里的
// tool_result 块，上一轮的 tool_calls 也要变成 tool_use 块。原样转发 role=tool
// 会被上游整条拒绝（400 Request body format invalid）。
func TestAnthropicAdapterConvertsToolMessages(t *testing.T) {
	var received struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_4","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"upstream-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	request := Request{Model: "upstream-model", Messages: []Message{
		{Role: "user", Content: json.RawMessage(`"ls"`)},
		{Role: "assistant", Content: json.RawMessage(`null`), ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]`)},
		{Role: "tool", Content: json.RawMessage(`"ok"`), ToolCallID: "call_1"},
	}}
	if _, err := (Anthropic{Client: upstream.Client()}).Do(context.Background(), provider.Provider{
		ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test",
	}, "sk-test", request); err != nil {
		t.Fatal(err)
	}
	if len(received.Messages) != 3 {
		t.Fatalf("want 3 upstream messages, got %d", len(received.Messages))
	}
	if received.Messages[1].Role != "assistant" || !strings.Contains(string(received.Messages[1].Content), `"tool_use"`) || !strings.Contains(string(received.Messages[1].Content), `"name":"bash"`) {
		t.Fatalf("assistant tool_calls not converted to tool_use: %s", received.Messages[1].Content)
	}
	if received.Messages[2].Role != "user" || !strings.Contains(string(received.Messages[2].Content), `"tool_result"`) || !strings.Contains(string(received.Messages[2].Content), `"tool_use_id":"call_1"`) {
		t.Fatalf("tool message not converted to tool_result: %s", received.Messages[2].Content)
	}
}

// A stalled upstream body (half-open proxied connection: TCP ESTABLISHED,
// no bytes arriving) must not park the handler forever. The stall guard
// closes the body after the window, the stream is reported as ended early,
// and — unlike the wedged request in the field — it leaves a log row.
func TestStalledUpstreamStreamIsUnwedgedAndLogged(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-9","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		// Then silence: neither data nor connection close, indefinitely.
		<-release
	}))
	defer upstream.Close()
	time.AfterFunc(500*time.Millisecond, func() { close(release) })

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	s.stallTimeout = 50 * time.Millisecond

	payload := `{"model":"client-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.messagesHandler(response, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler still wedged on a stalled upstream body")
	}

	body := response.Body.String()
	if strings.Contains(body, "event: message_stop") {
		t.Fatalf("message_stop emitted for a stalled stream: %s", body)
	}
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 || logs.Items[0].Success || logs.Items[0].ErrorMessage == "" {
		t.Fatalf("stalled stream not logged as failed: %+v", logs.Items)
	}
}

// Claude Code's permission classifier sends a small max_tokens (2112) to a
// reasoning upstream: the model spends the whole budget thinking and returns
// content:[] with stop_reason max_tokens, so the client waits on an empty
// answer and retries — the gateway log showed six such 2112-token empty
// responses in one afternoon. Small max_tokens must skip reasoning_effort and
// get a floor so the model can still emit its verdict.
func TestSmallMaxTokensSkipsReasoningEffortAndGetsFloor(t *testing.T) {
	var openAIReceived map[string]json.RawMessage
	openAIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &openAIReceived); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-7","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer openAIUpstream.Close()

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: openAIUpstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"client-model","max_tokens":2112,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if _, ok := openAIReceived["reasoning_effort"]; ok {
		t.Errorf("reasoning_effort forwarded for small max_tokens: %s", openAIReceived["reasoning_effort"])
	}
	if got, want := string(openAIReceived["max_tokens"]), "8192"; got != want {
		t.Errorf("max_tokens = %s, want raised to %s", got, want)
	}

	// A generous budget keeps both the effort mapping and the client's own cap.
	openAIReceived = nil
	req = httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"client-model","max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "ar-local")
	response = httptest.NewRecorder()
	s.messagesHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if string(openAIReceived["reasoning_effort"]) != `"medium"` {
		t.Errorf("reasoning_effort not mapped for normal max_tokens: %s", openAIReceived["reasoning_effort"])
	}
	if string(openAIReceived["max_tokens"]) != "32000" {
		t.Errorf("max_tokens rewritten: %s", openAIReceived["max_tokens"])
	}
}

// An OpenAI client routing to an Anthropic upstream must keep its sampling,
// tool and thinking settings, and its messages must arrive as Anthropic shape.
// The old body builder hard-coded max_tokens=1024 and dropped everything else.
func TestAnthropicAdapterCarriesSamplingToolsAndThinking(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"upstream-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hi"}],
		"max_tokens":512,"temperature":0.2,"top_p":0.9,"stop":["END"],
		"tools":[{"type":"function","function":{"name":"bash","description":"run a command","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}],
		"tool_choice":{"type":"function","function":{"name":"bash"}},
		"thinking":{"type":"enabled","budget_tokens":2048}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}

	if string(received["max_tokens"]) != "512" {
		t.Errorf("max_tokens not honored: %s", received["max_tokens"])
	}
	if string(received["temperature"]) != "0.2" || string(received["top_p"]) != "0.9" {
		t.Errorf("sampling lost: temperature=%s top_p=%s", received["temperature"], received["top_p"])
	}
	if !strings.Contains(string(received["stop_sequences"]), `"END"`) {
		t.Errorf("stop not mapped to stop_sequences: %s", received["stop_sequences"])
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(received["tools"], &tools); err != nil || len(tools) != 1 {
		t.Fatalf("tools not in anthropic shape: %s", received["tools"])
	}
	if string(tools[0]["name"]) != `"bash"` || !strings.Contains(string(tools[0]["input_schema"]), `"command"`) {
		t.Errorf("tool not reshaped: %s", received["tools"])
	}
	if !strings.Contains(string(received["tool_choice"]), `"name":"bash"`) {
		t.Errorf("tool_choice not mapped: %s", received["tool_choice"])
	}
	if !strings.Contains(string(received["thinking"]), `"budget_tokens":2048`) {
		t.Errorf("thinking stripped: %s", received["thinking"])
	}
	var system string
	if err := json.Unmarshal(received["system"], &system); err != nil || system != "be brief" {
		t.Errorf("system lost: %s", received["system"])
	}
}

// Claude Code streams Anthropic SSE, which reports usage in two frames:
// input_tokens (plus the cache fields, which Anthropic counts separately) in
// message_start and the final output_tokens in message_delta.
func TestTokensFromResponseReadsAnthropicSSE(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":100,"cache_read_input_tokens":900,"cache_creation_input_tokens":7,"output_tokens":1}}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":50}}`,
		"",
	}, "\n"))
	got := tokensFromResponse(body)
	if got.Input != 1007 || got.CachedInput != 900 || got.Output != 50 {
		t.Fatalf("unexpected anthropic token usage: %+v", got)
	}
}

// The request log must carry token counts for Claude Code, which talks to
// /v1/messages and streams through the Anthropic passthrough.
func TestAnthropicPassthroughLogsTokenUsage(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("missing upstream credential")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}

	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("want 1 logged request, got %d", len(logs.Items))
	}
	logged := logs.Items[0]
	if logged.InputTokens != 1000 || logged.CachedInputTokens != 900 || logged.OutputTokens != 50 {
		t.Fatalf("token usage not logged: %+v", logged)
	}
	if !logged.Success {
		t.Fatalf("request not logged as successful: %+v", logged)
	}
}

// An OpenAI-compatible upstream reports usage in its own final stream chunk,
// and only when the request opts in. The gateway adds the opt-in itself — the
// Anthropic client never sends one — and must put the counts where the client
// reads them: message_delta, which carries usage cumulatively.
func TestStreamingAnthropicToOpenAIRequestsAndReportsTokenUsage(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"id":"chatcmpl-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"chatcmpl-5","choices":[],"usage":{"prompt_tokens":40,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":30}}}`,
			`data: [DONE]`,
			"",
		}, "\n\n"))
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	s.messagesHandler(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}

	if !strings.Contains(string(received["stream_options"]), `"include_usage":true`) {
		t.Fatalf("usage was not requested from upstream: %s", received["stream_options"])
	}
	var delta struct {
		Usage struct {
			Input  int `json:"input_tokens"`
			Cached int `json:"cache_read_input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	found := false
	for _, frame := range strings.Split(response.Body.String(), "\n\n") {
		if !strings.HasPrefix(frame, "event: message_delta") {
			continue
		}
		found = true
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.Split(frame, "\n")[1], "data: ")), &delta); err != nil {
			t.Fatalf("bad message_delta: %v", err)
		}
	}
	if !found {
		t.Fatalf("no message_delta in stream: %s", response.Body.String())
	}
	if delta.Usage.Input != 40 || delta.Usage.Cached != 30 || delta.Usage.Output != 9 {
		t.Fatalf("message_delta usage not filled in: %s", response.Body.String())
	}

	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("want 1 logged request, got %d", len(logs.Items))
	}
	if logged := logs.Items[0]; logged.InputTokens != 40 || logged.CachedInputTokens != 30 || logged.OutputTokens != 9 {
		t.Fatalf("token usage not logged: %+v", logged)
	}
}

// An Anthropic upstream streams its own event names; an OpenAI client must see
// chat.completion.chunk frames, tool calls included, and the log must still get
// the token counts that only the Anthropic frames carry.
func TestStreamingAnthropicUpstreamBecomesOpenAIChunks(t *testing.T) {
	var received map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v: %s", err, body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":1}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"bash","input":{}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":50}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tracker := usage.NewSQLiteTracker(db)
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, tracker, fakeKeys{valid: "ar-local"})
	payload := `{"model":"client-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)

	if string(received["stream"]) != "true" {
		t.Fatalf("streaming not forwarded: %s", received["stream"])
	}
	body := response.Body.String()
	text, arguments, finish := "", "", ""
	for _, frame := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		text += chunk.Choices[0].Delta.Content
		if calls := chunk.Choices[0].Delta.ToolCalls; len(calls) > 0 {
			if calls[0].ID != "" && calls[0].ID != "toolu_1" {
				t.Fatalf("tool call announced with a foreign id: %s", body)
			}
			arguments += calls[0].Function.Arguments
		}
		if chunk.Choices[0].FinishReason != "" {
			finish = chunk.Choices[0].FinishReason
		}
	}
	if text != "hi" || arguments != `{"cmd":"ls"}` || finish != "tool_calls" {
		t.Fatalf("bad chunks: text=%q args=%q finish=%q\n%s", text, arguments, finish, body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream not terminated: %s", body)
	}
	logs, err := tracker.ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("want one logged request, got %d", len(logs.Items))
	}
	if logged := logs.Items[0]; logged.InputTokens != 1000 || logged.CachedInputTokens != 900 || logged.OutputTokens != 50 {
		t.Fatalf("token usage not logged: %+v", logged)
	}
}

// A single screenshot or a long tool history puts a client request past the old
// 4 MiB cap, and the gateway then answered 400 "http: request body too large"
// for a request the provider would have accepted. Requests up to the upstream
// limit must reach the provider, and one past the gateway cap must say which
// limit was hit instead of quoting MaxBytesReader.
func TestLargeRequestBodyIsForwardedAndOversizeIsExplained(t *testing.T) {
	var received int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = len(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-5","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})

	// The old cap rejected this without ever calling the provider.
	large := `{"model":"client-model","messages":[{"role":"user","content":"` + strings.Repeat("a", 5<<20) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(large))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("5 MiB request refused: %d %s", response.Code, response.Body.String())
	}
	if received < 5<<20 {
		t.Fatalf("upstream saw %d bytes of a %d byte request", received, len(large))
	}

	oversized := `{"model":"client-model","messages":[{"role":"user","content":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(oversized))
	req.Header.Set("Authorization", "Bearer ar-local")
	response = httptest.NewRecorder()
	s.chatCompletions(response, req)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "32 MiB") {
		t.Fatalf("oversize error does not name the limit: %s", response.Body.String())
	}
}
