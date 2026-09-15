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
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test", Enabled: true})
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
	_, err = registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test", Enabled: true})
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
	s := New(registry, mappings, fakeSecrets{"provider/test": "sk-test"}, tracker, fakeKeys{valid: "ar-local"})
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
