package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agent-router/backend/config"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

type fakeSecrets map[string]string

func (f fakeSecrets) Set(string, string) error       { return nil }
func (f fakeSecrets) Get(key string) (string, error) { return f[key], nil }
func (f fakeSecrets) Delete(string) error            { return nil }

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
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Enabled: true})
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
	s := New(registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
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
	full, err := usage.NewSQLiteTracker(db).GetRequestLog(logs.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.RequestBody, `"content":"hello"`) {
		t.Fatalf("unexpected full request body: %s", full.RequestBody)
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
	if _, err := registry.Save(provider.Provider{ID: "disabled", Name: "Disabled", BaseURL: "https://example.org", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []config.ModelMapping{
		{ID: "routable", ClientModel: "mapped-model", ProviderID: "enabled", UpstreamModel: "edited-upstream", Enabled: true},
		{ID: "mapping-disabled", ClientModel: "disabled-mapping", ProviderID: "enabled", UpstreamModel: "disabled-upstream", Enabled: false},
		{ID: "provider-disabled", ClientModel: "unavailable-model", ProviderID: "disabled", UpstreamModel: "upstream-model", Enabled: true},
	} {
		if _, err := mappings.Save(mapping); err != nil {
			t.Fatal(err)
		}
	}

	s := New(registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
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
	}
	if !found {
		t.Fatalf("mapped-model missing from response: %#v", result.Data)
	}
	if !foundAutomatic {
		t.Fatalf("automatic model missing from response: %#v", result.Data)
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
	s := New(registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
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
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Enabled: true})
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
	s := New(registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})
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
