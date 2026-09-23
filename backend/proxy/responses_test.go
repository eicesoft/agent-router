package proxy

import (
	"encoding/json"
	"io"
	"net"
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

// responsesFixture 起一个假上游与一套已配好的网关，返回的 server 可直接调
// s.responses。上游收到的请求体通过 captured 暴露给用例断言。
func responsesFixture(t *testing.T, upstream http.HandlerFunc) (*Server, *usage.SQLiteTracker, *string) {
	t.Helper()
	captured := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*captured = string(body)
		upstream(w, r)
	}))
	t.Cleanup(server.Close)

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: server.URL, APIKeyRef: "provider/test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "codex-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	tracker := usage.NewSQLiteTracker(db)
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, tracker, fakeKeys{valid: "ar-local"})
	return s, tracker, captured
}

func postResponses(t *testing.T, s *Server, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.responses(response, req)
	return response
}

// ssePayloads 抽出响应里所有 data: 行的 JSON，便于按事件类型断言。
func ssePayloads(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &payload); err != nil {
			t.Fatalf("bad sse frame %q: %v", line, err)
		}
		out = append(out, payload)
	}
	return out
}

func eventTypes(payloads []map[string]any) []string {
	out := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		out = append(out, payload["type"].(string))
	}
	return out
}

func TestResponsesRequiresLocalKey(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"codex-model","input":[]}`))
	response := httptest.NewRecorder()
	s.responses(response, req)
	if response.Code != 401 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "authentication_error") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestResponsesUnknownModelIs404(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	response := postResponses(t, s, `{"model":"nope","input":[]}`)
	if response.Code != 404 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "model_not_found") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

// Anthropic 上游没有实现 Responses↔Anthropic 双向转换，必须明确拒绝而不是把
// 一个上游看不懂的请求发出去。
func TestResponsesRejectsAnthropicProvider(t *testing.T) {
	captured := new(string)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.URL.Path
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
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, APIKeyRef: "provider/test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "m", ClientModel: "codex-model", ProviderID: "test", UpstreamModel: "up", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"})

	response := postResponses(t, s, `{"model":"codex-model","input":[]}`)
	if response.Code != 501 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if *captured != "" {
		t.Fatalf("upstream was called: %s", *captured)
	}
}

// Codex 发来的完整请求必须被翻成 Chat Completions：instructions 成 system 消息、
// input 逐条映射、模型换成上游名、工具由扁平变嵌套。
func TestResponsesConvertsRequestAndNonStreamingReply(t *testing.T) {
	s, tracker, captured := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello from upstream"}}],"usage":{"prompt_tokens":11,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`)
	})

	payload := `{
		"model":"codex-model",
		"instructions":"you are a coding agent",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"function_call","name":"shell","arguments":"{\"command\":\"ls\"}","call_id":"call_1"},
			{"type":"function_call_output","call_id":"call_1","output":"a.txt"},
			{"type":"reasoning","id":"rs_old","summary":[{"type":"summary_text","text":"thinking"}]}
		],
		"tools":[
			{"type":"function","name":"shell","description":"run a command","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}},
			{"type":"custom","name":"apply_patch","description":"patch files","format":{"type":"grammar","syntax":"lark","definition":"start: patch"}},
			{"type":"web_search"}
		],
		"reasoning":{"effort":"high","summary":"auto"},
		"max_output_tokens":500,
		"stream":false
	}`
	response := postResponses(t, s, payload)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	// 上游收到了什么
	var sent Request
	if err := json.Unmarshal([]byte(*captured), &sent); err != nil {
		t.Fatalf("upstream body = %s: %v", *captured, err)
	}
	if sent.Model != "upstream-model" {
		t.Fatalf("upstream model = %q", sent.Model)
	}
	if len(sent.Messages) != 4 {
		t.Fatalf("messages = %d: %s", len(sent.Messages), *captured)
	}
	if sent.Messages[0].Role != "system" || !strings.Contains(string(sent.Messages[0].Content), "coding agent") {
		t.Fatalf("system message = %+v", sent.Messages[0])
	}
	if sent.Messages[1].Role != "user" || !strings.Contains(string(sent.Messages[1].Content), "hi") {
		t.Fatalf("user message = %+v", sent.Messages[1])
	}
	if sent.Messages[2].Role != "assistant" || !strings.Contains(string(sent.Messages[2].ToolCalls), "shell") {
		t.Fatalf("assistant tool call = %+v", sent.Messages[2])
	}
	// 关键：tool_call_id 必须原样保留，否则上游无法把结果关联回调用。
	if sent.Messages[3].Role != "tool" || sent.Messages[3].ToolCallID != "call_1" {
		t.Fatalf("tool message = %+v", sent.Messages[3])
	}
	if sent.ReasoningEffort == nil || *sent.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %v", sent.ReasoningEffort)
	}
	if sent.MaxTokens == nil || *sent.MaxTokens != 500 {
		t.Fatalf("max_tokens = %v", sent.MaxTokens)
	}

	// 工具：function 保留、custom 包成字符串入参、web_search 丢弃
	tools := string(sent.Tools)
	if !strings.Contains(tools, `"type":"function"`) || !strings.Contains(tools, `"shell"`) {
		t.Fatalf("tools = %s", tools)
	}
	if !strings.Contains(tools, `"apply_patch"`) || !strings.Contains(tools, `"required":["input"]`) {
		t.Fatalf("custom tool wrapper missing: %s", tools)
	}
	if strings.Contains(tools, "web_search") {
		t.Fatalf("hosted tool should be dropped: %s", tools)
	}

	// 回给 Codex 的非流式响应
	var reply map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reply = %s: %v", response.Body.String(), err)
	}
	if reply["object"] != "response" || reply["status"] != "completed" {
		t.Fatalf("reply = %s", response.Body.String())
	}
	if reply["model"] != "codex-model" {
		t.Fatalf("reply model = %v", reply["model"])
	}
	output := reply["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output = %s", response.Body.String())
	}
	message := output[0].(map[string]any)
	if message["type"] != "message" || message["role"] != "assistant" {
		t.Fatalf("output item = %v", message)
	}
	content := message["content"].([]any)[0].(map[string]any)
	if content["type"] != "output_text" || content["text"] != "hello from upstream" {
		t.Fatalf("content = %v", content)
	}

	// usage 必须三个字段齐全，且缓存/推理计数不丢
	usageMap := reply["usage"].(map[string]any)
	if usageMap["input_tokens"].(float64) != 11 || usageMap["output_tokens"].(float64) != 4 || usageMap["total_tokens"].(float64) != 15 {
		t.Fatalf("usage = %v", usageMap)
	}
	if usageMap["input_tokens_details"].(map[string]any)["cached_tokens"].(float64) != 3 {
		t.Fatalf("cached_tokens lost: %v", usageMap)
	}
	if usageMap["output_tokens_details"].(map[string]any)["reasoning_tokens"].(float64) != 2 {
		t.Fatalf("reasoning_tokens lost: %v", usageMap)
	}

	// 落库：模型映射与 token 都要记进请求日志
	logs, err := tracker.ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	if logs.Items[0].ClientModel != "codex-model" || logs.Items[0].UpstreamModel != "upstream-model" {
		t.Fatalf("log = %+v", logs.Items[0])
	}
	if logs.Items[0].InputTokens != 11 || logs.Items[0].OutputTokens != 4 {
		t.Fatalf("log tokens = %+v", logs.Items[0])
	}
}

// Codex 的并行调用在 input 里是多条独立 function_call，但 Chat 上游要求同一轮
// 调用合成一条 assistant tool_calls，再接完整数量的 tool 消息。拆成多条 assistant
// 会让 DeepSeek 等上游报 insufficient tool messages。
func TestResponsesMergesParallelToolCalls(t *testing.T) {
	s, _, captured := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})

	payload := `{
		"model":"codex-model",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"check"}]},
			{"type":"function_call","name":"shell","arguments":"{\"command\":\"ls\"}","call_id":"call_1"},
			{"type":"function_call","name":"shell","arguments":"{\"command\":\"pwd\"}","call_id":"call_2"},
			{"type":"function_call_output","call_id":"call_1","output":"a.txt"},
			{"type":"function_call_output","call_id":"call_2","output":"/tmp"}
		],
		"tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object","properties":{}}}],
		"stream":false
	}`
	response := postResponses(t, s, payload)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var sent Request
	if err := json.Unmarshal([]byte(*captured), &sent); err != nil {
		t.Fatalf("upstream body = %s: %v", *captured, err)
	}
	if len(sent.Messages) != 4 {
		t.Fatalf("messages = %+v", sent.Messages)
	}
	if sent.Messages[1].Role != "assistant" || !strings.Contains(string(sent.Messages[1].ToolCalls), `"call_1"`) || !strings.Contains(string(sent.Messages[1].ToolCalls), `"call_2"`) {
		t.Fatalf("parallel calls not merged: %+v", sent.Messages[1])
	}
	if sent.Messages[2].Role != "tool" || sent.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("first tool result = %+v", sent.Messages[2])
	}
	if sent.Messages[3].Role != "tool" || sent.Messages[3].ToolCallID != "call_2" {
		t.Fatalf("second tool result = %+v", sent.Messages[3])
	}
}

// Codex 的 item 状态机要求正文 delta 归属一个已宣告的 item：没有先发
// output_item.added 就发 delta，它会报「OutputTextDelta without active item」并把
// delta 丢掉，流式逐字显示失效（只剩收尾那份完整内容兜底）。这个缺陷单测本来测不
// 出来，是端到端跑真实 Codex 才暴露的，所以在这里钉死。
func TestResponsesDeclaresTextItemBeforeDeltas(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"content":"he"}}]}`,
			`data: {"choices":[{"delta":{"content":"llo"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[]}`)
	payloads := ssePayloads(t, response.Body.String())

	// 找到第一条正文 delta 的位置，它之前必须有一个同 id 的 message item 被宣告。
	deltaAt, declaredID := -1, ""
	for i, payload := range payloads {
		switch payload["type"] {
		case "response.output_item.added":
			if item, ok := payload["item"].(map[string]any); ok && item["type"] == "message" {
				if id, ok := item["id"].(string); ok && id != "" {
					declaredID = id
				}
			}
		case "response.output_text.delta":
			if deltaAt < 0 {
				deltaAt = i
			}
		}
	}
	if deltaAt < 0 {
		t.Fatalf("no text deltas: %v", eventTypes(payloads))
	}
	if declaredID == "" {
		t.Fatalf("no message item declared before deltas: %v", eventTypes(payloads))
	}
	for i, payload := range payloads {
		if payload["type"] == "response.output_item.added" {
			if item, ok := payload["item"].(map[string]any); ok && item["type"] == "message" {
				if i > deltaAt {
					t.Fatalf("message item declared after the first delta: %v", eventTypes(payloads))
				}
				break
			}
		}
	}
	// added 与 done 必须是同一个 id，且只宣告一次。
	starts := 0
	for _, payload := range payloads {
		if payload["type"] != "response.output_item.added" {
			continue
		}
		if item, ok := payload["item"].(map[string]any); ok && item["id"] == declaredID {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("message item declared %d times, want 1: %v", starts, eventTypes(payloads))
	}
}

// 流式：created 开头、正文 delta 有序、completed 收尾且带完整 usage。
func TestResponsesStreamsTextDeltas(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"he"}}]}`,
			`data: {"choices":[{"delta":{"content":"llo"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %s", got)
	}
	payloads := ssePayloads(t, response.Body.String())
	types := eventTypes(payloads)
	if types[0] != "response.created" {
		t.Fatalf("first event = %s", types[0])
	}
	if types[len(types)-1] != "response.completed" {
		t.Fatalf("last event = %s (%v)", types[len(types)-1], types)
	}

	var deltas []string
	for _, payload := range payloads {
		if payload["type"] == "response.output_text.delta" {
			deltas = append(deltas, payload["delta"].(string))
		}
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("deltas = %v", deltas)
	}

	completed := payloads[len(payloads)-1]["response"].(map[string]any)
	if completed["id"] == "" || completed["id"] == nil {
		t.Fatal("completed response is missing id, Codex fails to parse it")
	}
	usageMap := completed["usage"].(map[string]any)
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if _, ok := usageMap[key]; !ok {
			t.Fatalf("usage missing %s: %v", key, usageMap)
		}
	}
	output := completed["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("completed output = %v", output)
	}
	if output[0].(map[string]any)["type"] != "message" {
		t.Fatalf("completed output item = %v", output[0])
	}
}

// 上游因 max_tokens 截断时，Responses 必须收成 incomplete，而不是 completed。
// Codex 只把 completed 当正常回合结束；误报会让它直接停下，留下半截任务。
func TestResponsesLengthFinishReportsIncomplete(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"content":"partial"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[]}`)
	body := response.Body.String()
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("length finish must not report completion: %s", body)
	}
	payloads := ssePayloads(t, body)
	last := payloads[len(payloads)-1]
	if last["type"] != "response.incomplete" {
		t.Fatalf("last event = %v: %v", last["type"], eventTypes(payloads))
	}
	envelope := last["response"].(map[string]any)
	if envelope["status"] != "incomplete" {
		t.Fatalf("status = %v", envelope["status"])
	}
	details := envelope["incomplete_details"].(map[string]any)
	if details["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete reason = %v", details["reason"])
	}
}

// 推理内容转成 Codex 认的 reasoning summary 事件。Codex 的
// reasoning_summary_text.delta 要求带 summary_index，且需先有 part.added。
func TestResponsesStreamsReasoningSummary(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"reasoning_content":"pondering"}}]}`,
			`data: {"choices":[{"delta":{"content":"answer"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[]}`)
	payloads := ssePayloads(t, response.Body.String())

	var sawPartAdded bool
	for _, payload := range payloads {
		switch payload["type"] {
		case "response.reasoning_summary_part.added":
			sawPartAdded = true
			if payload["summary_index"].(float64) != 0 {
				t.Fatalf("summary_index = %v", payload["summary_index"])
			}
		case "response.reasoning_summary_text.delta":
			if !sawPartAdded {
				t.Fatal("delta arrived before part.added")
			}
			if payload["summary_index"] == nil {
				t.Fatal("delta is missing summary_index, Codex drops it")
			}
			if payload["delta"].(string) != "pondering" {
				t.Fatalf("delta = %v", payload["delta"])
			}
		}
	}
	if !sawPartAdded {
		t.Fatalf("no reasoning events: %v", eventTypes(payloads))
	}

	completed := payloads[len(payloads)-1]["response"].(map[string]any)
	var sawReasoningItem bool
	for _, raw := range completed["output"].([]any) {
		item := raw.(map[string]any)
		if item["type"] == "reasoning" {
			sawReasoningItem = true
			summary := item["summary"].([]any)
			if summary[0].(map[string]any)["text"] != "pondering" {
				t.Fatalf("summary = %v", summary)
			}
		}
	}
	if !sawReasoningItem {
		t.Fatalf("completed output has no reasoning item: %v", completed["output"])
	}
}

// 工具调用只能靠 output_item.done 交付一份完整 item：Codex 忽略
// function_call_arguments.delta/done（见其 sse/responses.rs 的 match 分支）。
func TestResponsesDeliversCompleteToolCalls(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// 参数分三帧抵达，必须拼成一个完整 item。
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"shell","arguments":"{\"comm"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[]}`)
	payloads := ssePayloads(t, response.Body.String())

	var done map[string]any
	for _, payload := range payloads {
		if payload["type"] == "response.output_item.done" {
			done = payload["item"].(map[string]any)
		}
	}
	if done == nil {
		t.Fatalf("no output_item.done: %v", eventTypes(payloads))
	}
	if done["type"] != "function_call" {
		t.Fatalf("item type = %v", done["type"])
	}
	if done["name"] != "shell" || done["call_id"] != "call_9" {
		t.Fatalf("item = %v", done)
	}
	if done["arguments"] != `{"command":"ls"}` {
		t.Fatalf("arguments not reassembled: %v", done["arguments"])
	}
}

// freeform 工具（apply_patch）的调用必须回成 custom_tool_call，且 input 是原始
// 字符串而不是 {"input":...} 包装——Codex 把 custom_tool_call.input 当正文用。
func TestResponsesRestoresCustomToolCall(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_p","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\\n\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	payload := `{"model":"codex-model","stream":true,"tools":[{"type":"custom","name":"apply_patch","description":"patch","format":{"type":"grammar","syntax":"lark","definition":"x"}}],"input":[]}`
	response := postResponses(t, s, payload)
	payloads := ssePayloads(t, response.Body.String())

	var item map[string]any
	for _, payload := range payloads {
		if payload["type"] == "response.output_item.done" {
			item = payload["item"].(map[string]any)
		}
	}
	if item == nil {
		t.Fatalf("no output_item.done: %v", eventTypes(payloads))
	}
	if item["type"] != "custom_tool_call" {
		t.Fatalf("item type = %v (want custom_tool_call)", item["type"])
	}
	if item["input"] != "*** Begin Patch\n" {
		t.Fatalf("input = %q", item["input"])
	}
	if _, ok := item["arguments"]; ok {
		t.Fatalf("custom tool must not carry arguments: %v", item)
	}
}

// 上游流在客户端读到 EOF 前就被切断（拿到的是 RST 而不是干净的 EOF）时，不能发
// response.completed，否则 Codex 以为这是一次正常收尾并停止重试。
//
// 用连接劫持制造真实的断连：让 handler 正常返回只会给出干净的 EOF，走的是「没有
// finish_reason」那条分支，覆盖不到 scanner 报错这条路径。
func TestResponsesTruncatedStreamOmitsCompletion(t *testing.T) {
	s, tracker, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		flusher.Flush()
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream response writer is not a Hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		// 不发 [DONE]，也不做 TLS 关闭握手，直接 RST。
		conn.(*net.TCPConn).SetLinger(0)
		_ = conn.Close()
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[]}`)
	body := response.Body.String()
	if strings.Contains(body, "response.completed") {
		t.Fatalf("truncated stream must not report completion: %s", body)
	}
	if !strings.Contains(body, "response.failed") {
		t.Fatalf("expected response.failed: %s", body)
	}

	logs, err := tracker.ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	if logs.Items[0].Success {
		t.Fatalf("truncated stream logged as success: %+v", logs.Items[0])
	}
	if logs.Items[0].ErrorMessage == "" {
		t.Fatalf("no error recorded: %+v", logs.Items[0])
	}
}

// 流正常结束但从未给 finish_reason：同样算坏掉的交换。
func TestResponsesStreamWithoutFinishReasonFails(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		flusher.Flush()
	})

	response := postResponses(t, s, `{"model":"codex-model","stream":true,"input":[]}`)
	body := response.Body.String()
	if !strings.Contains(body, "response.failed") {
		t.Fatalf("expected response.failed: %s", body)
	}
	if strings.Contains(body, "response.completed") {
		t.Fatalf("must not complete: %s", body)
	}
}

// 上游的错误体原样透传，Codex 自己解析 {"error":{...}}。
func TestResponsesPassesUpstreamErrorThrough(t *testing.T) {
	s, tracker, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	})

	response := postResponses(t, s, `{"model":"codex-model","input":[]}`)
	if response.Code != 429 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "rate_limit_exceeded") {
		t.Fatalf("upstream body was rewritten: %s", response.Body.String())
	}

	logs, err := tracker.ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 || logs.Items[0].Success {
		t.Fatalf("log = %+v", logs.Items)
	}
}

// 被丢弃的 hosted 工具要在日志里留痕，便于排查「模型为什么没去搜索」。
func TestResponsesLogsDroppedHostedTools(t *testing.T) {
	s, tracker, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	})

	payload := `{"model":"codex-model","stream":false,"tools":[{"type":"web_search"},{"type":"function","name":"shell","parameters":{"type":"object"}}],"input":[]}`
	response := postResponses(t, s, payload)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	logs, err := tracker.ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	if !strings.Contains(logs.Items[0].ErrorMessage, "dropped hosted tools: web_search") {
		t.Fatalf("error message = %q", logs.Items[0].ErrorMessage)
	}
}

// 会话粘性：Codex 每轮都带 prompt_cache_key，它应直接决定凭据选择。
func TestResponsesSessionUsesPromptCacheKey(t *testing.T) {
	input := responsesRequest{PromptCacheKey: "cache-key-1"}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if got := responsesSession(req, input); got != "cache-key-1" {
		t.Fatalf("session = %q", got)
	}
	// 没有 prompt_cache_key 时退回显式会话头。
	withHeader := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	withHeader.Header.Set("X-Session-Id", "conv-9")
	if got := responsesSession(withHeader, responsesRequest{}); got != "conv-9" {
		t.Fatalf("session = %q", got)
	}
}
