package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Responses Lite（Codex 0.155 的 gpt-6-astra / gpt-5.6-* 默认形状）不下发顶层
// tools、也不下发 instructions，全部工具声明都裹在 input[0] 的 additional_tools
// 里，外层再套一层 namespace。这个形状曾经整份工具表被丢弃，模型在没有任何工具
// 定义的情况下把调用当正文吐出来。
func TestResponsesLiteExpandsAdditionalToolsNamespaces(t *testing.T) {
	s, _, captured := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})

	payload := `{
		"model":"codex-model",
		"instructions":"",
		"input":[
			{"type":"additional_tools","id":"at_1","role":"developer","tools":[
				{"type":"namespace","name":"functions","description":"","tools":[
					{"type":"custom","name":"exec","description":"Run JavaScript code","format":{"type":"grammar","syntax":"lark","definition":"start: a"}},
					{"type":"function","name":"wait","description":"Wait for a cell","parameters":{"type":"object","properties":{}}}
				]},
				{"type":"namespace","name":"clock","description":"Tools for reading time.","tools":[
					{"type":"function","name":"sleep","description":"Pause execution.","parameters":{"type":"object","properties":{}}}
				]},
				{"type":"namespace","name":"collaboration","description":"Sub-agents.","tools":[
					{"type":"function","name":"spawn_agent","description":"Spawn one.","parameters":{"type":"object","properties":{}}}
				]},
				{"type":"web_search","external_web_access":true}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
		],
		"tools":null,
		"tool_choice":"auto",
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
	if len(sent.Messages) != 1 {
		t.Fatalf("additional_tools must not become a message: %+v", sent.Messages)
	}

	tools := string(sent.Tools)
	// 默认 namespace 的工具保持裸名，嵌套 namespace 用 "__" 摊平。
	for _, want := range []string{`"name":"exec"`, `"name":"wait"`, `"name":"clock__sleep"`, `"name":"collaboration__spawn_agent"`} {
		if !strings.Contains(tools, want) {
			t.Fatalf("missing %s in tools = %s", want, tools)
		}
	}
	// hosted 工具仍然丢弃，且不能变成函数下发。
	if strings.Contains(tools, "web_search") {
		t.Fatalf("hosted tool leaked: %s", tools)
	}
	// freeform 工具包成单字符串入参。
	if !strings.Contains(tools, `"required":["input"]`) {
		t.Fatalf("custom tool wrapper missing: %s", tools)
	}
	// 点号会被上游的函数名校验拒掉。
	if strings.Contains(tools, `"name":"clock.sleep"`) {
		t.Fatalf("dotted tool name reached upstream: %s", tools)
	}
}

// 上游按摊平后的函数名回调用，网关必须还原成 Codex 的 (namespace, name) 二元组：
// 默认 namespace 省略字段，其余带上 namespace 字段。
func TestResponsesLiteRestoresNamespacedToolCalls(t *testing.T) {
	s, _, _ := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, frame := range []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"collaboration__spawn_agent","arguments":"{\"message\":\"hi\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"clock__sleep","arguments":"{\"duration_ms\":5}"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_3","function":{"name":"exec","arguments":"{\"input\":\"await tools.exec_command({cmd: 1})\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
			flusher.Flush()
		}
	})

	payload := `{"model":"codex-model","stream":true,"input":[
		{"type":"additional_tools","role":"developer","tools":[
			{"type":"namespace","name":"functions","description":"","tools":[
				{"type":"custom","name":"exec","description":"run","format":{"type":"grammar","syntax":"lark","definition":"x"}}
			]},
			{"type":"namespace","name":"clock","description":"time","tools":[
				{"type":"function","name":"sleep","description":"sleep","parameters":{"type":"object","properties":{}}}
			]},
			{"type":"namespace","name":"collaboration","description":"agents","tools":[
				{"type":"function","name":"spawn_agent","description":"spawn","parameters":{"type":"object","properties":{}}}
			]}
		]}
	]}`
	response := postResponses(t, s, payload)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	items := doneItems(t, response.Body.String())
	if len(items) != 3 {
		t.Fatalf("items = %v", items)
	}

	// 自定义 namespace：裸名 + namespace 字段。
	spawn := items[0]
	if spawn["type"] != "function_call" || spawn["name"] != "spawn_agent" || spawn["namespace"] != "collaboration" {
		t.Fatalf("spawn item = %v", spawn)
	}
	sleep := items[1]
	if sleep["name"] != "sleep" || sleep["namespace"] != "clock" {
		t.Fatalf("sleep item = %v", sleep)
	}
	// 默认 namespace：省略 namespace 字段，Codex 会归一化成 functions。
	exec := items[2]
	if exec["type"] != "custom_tool_call" || exec["name"] != "exec" {
		t.Fatalf("exec item = %v", exec)
	}
	if _, ok := exec["namespace"]; ok {
		t.Fatalf("default namespace must be omitted: %v", exec)
	}
	if exec["input"] != "await tools.exec_command({cmd: 1})" {
		t.Fatalf("exec input = %q", exec["input"])
	}
}

// 历史里的 function_call 只带 Codex 侧的名字，回给上游时必须换回摊平后的函数名，
// 否则上游看到的是一个不在自己工具表里的函数。
func TestResponsesRewritesHistoryToolCallNames(t *testing.T) {
	s, _, captured := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})

	payload := `{
		"model":"codex-model",
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"namespace","name":"collaboration","description":"agents","tools":[
					{"type":"function","name":"spawn_agent","description":"spawn","parameters":{"type":"object","properties":{}}}
				]}
			]},
			{"type":"function_call","name":"spawn_agent","namespace":"collaboration","arguments":"{}","call_id":"call_7"},
			{"type":"function_call_output","call_id":"call_7","output":"done"}
		],
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
	if len(sent.Messages) != 2 {
		t.Fatalf("messages = %+v", sent.Messages)
	}
	if !strings.Contains(string(sent.Messages[0].ToolCalls), "collaboration__spawn_agent") {
		t.Fatalf("history call not renamed: %s", sent.Messages[0].ToolCalls)
	}
	if sent.Messages[1].ToolCallID != "call_7" {
		t.Fatalf("tool result lost its call id: %+v", sent.Messages[1])
	}
}

// 摊平后真的撞名时给后来者加后缀：两个 namespace 里的同名工具必须各自可达。
func TestResponsesDeduplicatesCollidingToolNames(t *testing.T) {
	plan := newToolPlan()
	first := plan.register("", "sleep", false)
	second := plan.register("clock", "sleep", false)
	third := plan.register("cron", "sleep", false)
	if first == second || second == third || first == third {
		t.Fatalf("names collided: %q %q %q", first, second, third)
	}
	if first != "sleep" {
		t.Fatalf("first name = %q", first)
	}
	if route, ok := plan.lookup("clock__sleep"); !ok || route.namespace != "clock" {
		t.Fatalf("route = %+v ok=%v", route, ok)
	}
	// 同一个 (namespace, name) 重复登记要复用首次分配的名字。
	if again := plan.register("clock", "sleep", false); again != second {
		t.Fatalf("re-register changed the name: %q != %q", again, second)
	}
}

// 模型编出来的函数名不在映射表里时不要替它猜工具：原样回一个 function_call，
// 让 Codex 报 "unsupported call"。
func TestResponsesUnknownToolCallFallsBackToBareName(t *testing.T) {
	plan := newToolPlan()
	if _, ok := plan.lookup("invented"); ok {
		t.Fatal("unknown name resolved")
	}
	if name, ok := plan.emittedName("collaboration", "invented"); ok {
		t.Fatalf("unknown codex name resolved to %q", name)
	}
}

func doneItems(t *testing.T, body string) []map[string]any {
	t.Helper()
	var items []map[string]any
	for _, payload := range ssePayloads(t, body) {
		if payload["type"] != "response.output_item.done" {
			continue
		}
		item, ok := payload["item"].(map[string]any)
		if !ok {
			t.Fatalf("bad item: %v", payload["item"])
		}
		if item["type"] == "reasoning" {
			continue
		}
		items = append(items, item)
	}
	return items
}

// Codex 的 title 生成这类请求靠 text.format 指定 json_schema。原样转发会被上游
// 当成未知的 response_format 类型拒掉，必须转成 Chat 的嵌套形状。
func TestResponsesConvertsTextFormatToChatJSONSchema(t *testing.T) {
	s, _, captured := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`)
	})

	payload := `{
		"model":"codex-model",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"title please"}]}],
		"text":{"verbosity":"low","format":{"type":"json_schema","strict":true,"name":"codex_output_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}},
		"stream":false
	}`
	response := postResponses(t, s, payload)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(*captured), &sent); err != nil {
		t.Fatalf("upstream body = %s: %v", *captured, err)
	}
	format, ok := sent["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %s", *captured)
	}
	if format["type"] != "json_schema" {
		t.Fatalf("response_format type = %v", format["type"])
	}
	inner, ok := format["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema not wrapped: %v", format)
	}
	if inner["name"] != "codex_output_schema" || inner["strict"] != true {
		t.Fatalf("json_schema = %v", inner)
	}
	if _, ok := inner["schema"].(map[string]any); !ok {
		t.Fatalf("schema lost: %v", inner)
	}
}
