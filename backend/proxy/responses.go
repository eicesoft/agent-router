package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-router/backend/provider"
	"agent-router/backend/usage"
)

// responsesRequest 是 Codex CLI 等客户端发来的 OpenAI Responses 请求。字段取自
// openai/codex 的 ResponsesApiRequest，只保留本网关需要读懂的部分：其余字段
// （store / include / service_tier / stream_options / client_metadata 等）反序列化
// 时被忽略，因为它们在 Chat Completions 线协议上没有对应物。
type responsesRequest struct {
	Model             string              `json:"model"`
	Instructions      string              `json:"instructions"`
	Input             []json.RawMessage   `json:"input"`
	Tools             []json.RawMessage   `json:"tools"`
	ToolChoice        json.RawMessage     `json:"tool_choice"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls"`
	Reasoning         *responsesReasoning `json:"reasoning"`
	Text              *responsesText      `json:"text"`
	MaxOutputTokens   *int                `json:"max_output_tokens"`
	Temperature       *float64            `json:"temperature"`
	TopP              *float64            `json:"top_p"`
	Stream            bool                `json:"stream"`
	// promptCacheKey 是 Codex 每轮都会带的会话指纹。它不转发给上游（老兼容上游
	// 对未知字段的容忍度不确定），只用来选凭据，使 session 模式的粘性与上游按 key
	// 隔离的 prompt cache 对齐。
	PromptCacheKey string `json:"prompt_cache_key"`
}

type responsesReasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

type responsesText struct {
	Verbosity string          `json:"verbosity"`
	Format    json.RawMessage `json:"format"`
}

// defaultNamespace 是 Codex 给顶层 function / custom 工具的默认 namespace：省略
// namespace 字段与写 "functions" 等价（ToolName::with_default_namespace）。
const defaultNamespace = "functions"

// 函数名的字符集与长度按 Chat Completions 的约定收窄（^[a-zA-Z0-9_-]{1,64}$）。
// 上游普遍照这个校验，点号会被直接拒掉。
const maxToolNameLen = 64

// toolRoute 是一个工具在 Codex 那边的身份。Codex 按 (namespace, name) 二元组解析
// 工具调用（见 codex-rs/protocol/src/tool_name.rs 的 ToolName），不认
// "collaboration.spawn_agent" 这种点号名；Chat 上游又只收一个扁平函数名，所以网关
// 在两者之间做一层改名，用 emitted / byCodex 两张表双向映射。
type toolRoute struct {
	namespace string // Codex 的 namespace；"functions" 归一化成空串（下发时省略）
	name      string // Codex 侧的裸名
	custom    bool   // freeform 工具：响应侧必须回 custom_tool_call，入参是原始字符串
}

// upstreamBase 是加去重后缀前的基础函数名：默认 namespace 的工具保持裸名（与顶层
// 工具一致），其余前置 namespace 用 "__" 连接——与 Codex code mode 给嵌套工具拼
// JS 标识符的写法一致。
func (r toolRoute) upstreamBase() string {
	if r.namespace == "" {
		return r.name
	}
	return r.namespace + "__" + r.name
}

// toolPlan 是一次请求内的工具名映射表。
type toolPlan struct {
	emitted map[string]toolRoute // 下发给上游的名字 → Codex 侧身份
	byCodex map[string]string    // Codex 侧身份 → 下发给上游的名字
}

func newToolPlan() *toolPlan {
	return &toolPlan{emitted: map[string]toolRoute{}, byCodex: map[string]string{}}
}

func canonicalNamespace(namespace string) string {
	if namespace == defaultNamespace {
		return ""
	}
	return namespace
}

func codexToolKey(namespace, name string) string {
	return canonicalNamespace(namespace) + "\x00" + name
}

// register 登记一个工具并返回下发给上游的函数名。同一个 (namespace, name) 重复出现
// 时复用首次分配的名字，只有摊平后真的撞名才追加后缀。
func (p *toolPlan) register(namespace, name string, custom bool) string {
	key := codexToolKey(namespace, name)
	if emitted, ok := p.byCodex[key]; ok {
		return emitted
	}
	route := toolRoute{namespace: canonicalNamespace(namespace), name: name, custom: custom}
	base := sanitizeToolName(route.upstreamBase())
	emitted := base
	for n := 2; ; n++ {
		if _, taken := p.emitted[emitted]; !taken {
			break
		}
		emitted = withNameSuffix(base, n)
	}
	p.emitted[emitted] = route
	p.byCodex[key] = emitted
	return emitted
}

// lookup 找出上游这个函数名对应哪个 Codex 工具。未知名字返回 false：模型可能凭空
// 编了一个函数名，交给 Codex 报「unsupported call」比网关替它猜更诚实。
func (p *toolPlan) lookup(name string) (toolRoute, bool) {
	if p == nil {
		return toolRoute{}, false
	}
	route, ok := p.emitted[name]
	return route, ok
}

// emittedName 是反向查询。Codex 历史里的 function_call 只带 (namespace, name)，
// 再回给上游时必须用同一个函数名，否则上游会认为模型调了个不在工具表里的函数。
func (p *toolPlan) emittedName(namespace, name string) (string, bool) {
	if p == nil {
		return "", false
	}
	emitted, ok := p.byCodex[codexToolKey(namespace, name)]
	return emitted, ok
}

func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "tool"
	}
	if len(out) > maxToolNameLen {
		out = out[:maxToolNameLen]
	}
	return out
}

func withNameSuffix(base string, n int) string {
	suffix := "_" + strconv.Itoa(n)
	if len(base)+len(suffix) > maxToolNameLen {
		base = base[:maxToolNameLen-len(suffix)]
	}
	return base + suffix
}

// toChat 把 Responses 请求转成本网关内部的 Chat Completions 请求。返回的 plan 是本
// 次请求的工具名映射表，响应侧靠它把上游的 tool_calls 还原成 Codex 认的 item；
// dropped 是被丢弃的 hosted 工具名，写进日志备查。
func (in responsesRequest) toChat() (Request, *toolPlan, []string) {
	messages := make([]Message, 0, len(in.Input)+1)
	if strings.TrimSpace(in.Instructions) != "" {
		messages = append(messages, Message{Role: "system", Content: jsonString(in.Instructions)})
	}

	// 工具表必须先于 input 解析完：Responses Lite 把工具放在 input 的
	// additional_tools item 里（此时顶层 tools 与 instructions 都留空），而历史里
	// function_call 的换名依赖映射表已经建好。
	plan := newToolPlan()
	rawTools := append([]json.RawMessage(nil), in.Tools...)
	for _, raw := range in.Input {
		if nested, ok := additionalToolsItem(raw); ok {
			rawTools = append(rawTools, nested...)
		}
	}
	tools, dropped := responsesToolsToChat(rawTools, plan)

	for _, raw := range in.Input {
		if message, ok := responsesItemToChat(raw, plan); ok {
			messages = appendOrMergeToolCall(messages, message)
		}
	}

	out := Request{
		Model:             in.Model,
		Messages:          messages,
		Stream:            in.Stream,
		Temperature:       in.Temperature,
		TopP:              in.TopP,
		Tools:             tools,
		ToolChoice:        responsesToolChoiceToChat(in.ToolChoice, plan),
		ParallelToolCalls: in.ParallelToolCalls,
		MaxTokens:         in.MaxOutputTokens,
	}
	if in.Reasoning != nil && in.Reasoning.Effort != "" {
		out.ReasoningEffort = &in.Reasoning.Effort
	}
	if in.Text != nil {
		if in.Text.Verbosity != "" {
			out.Verbosity = &in.Text.Verbosity
		}
		if format := responsesTextFormatToChat(in.Text.Format); len(format) > 0 {
			out.ResponseFormat = format
		}
	}
	// 上游只在被要求时才在流里报用量；不强制就等于日志恒为 0 token（与
	// anthropicViaOpenAI 的处理一致）。
	if in.Stream {
		out.StreamOptions = map[string]bool{"include_usage": true}
	}
	return out, plan, dropped
}

// appendOrMergeToolCall 把同一轮的 assistant 正文与 tool_calls 合成一条消息。
// Codex 的并行调用在 input 里是多个独立 function_call item，但 Chat 上游要求它们
// 属于同一条 assistant 消息；正文先于调用到达时也必须并进同一条。拆开会让上游
// 看到「先一句没有调用的正文就结束」的回合，模型会模仿这个模式空谈不干活。
func appendOrMergeToolCall(messages []Message, message Message) []Message {
	last := len(messages) - 1
	if last < 0 || message.Role != "assistant" || messages[last].Role != "assistant" {
		return append(messages, message)
	}
	prev := &messages[last]
	// 纯正文之间不合并：那是两段独立发言。
	if len(prev.ToolCalls) == 0 && len(message.ToolCalls) == 0 {
		return append(messages, message)
	}
	if len(message.Content) > 0 && string(message.Content) != "null" {
		if len(prev.Content) == 0 || string(prev.Content) == "null" {
			prev.Content = message.Content
		}
	}
	if len(message.ToolCalls) == 0 {
		return messages
	}
	if len(prev.ToolCalls) == 0 {
		prev.ToolCalls = message.ToolCalls
		return messages
	}
	var existing, added []json.RawMessage
	if json.Unmarshal(prev.ToolCalls, &existing) != nil || json.Unmarshal(message.ToolCalls, &added) != nil {
		return append(messages, message)
	}
	prev.ToolCalls = json.RawMessage(mustJSON(append(existing, added...)))
	return messages
}

// additionalToolsItem 认出 Responses Lite 的 {"type":"additional_tools"} item 并
// 取出它携带的工具定义。Lite 模式下 Codex 不下发顶层 tools、也不下发 instructions，
// 全部工具的声明都塞在这个 item 的 tools 里：直接子项是 namespace（functions /
// clock / collaboration / mcp__*）或 hosted 工具。这个 item 本身不是消息，不能当
// 聊天内容转发。
func additionalToolsItem(raw json.RawMessage) ([]json.RawMessage, bool) {
	var item struct {
		Type  string            `json:"type"`
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &item) != nil || item.Type != "additional_tools" {
		return nil, false
	}
	return item.Tools, true
}

// responsesTextFormatToChat 把 Responses 的 text.format 翻成 Chat 的 response_format。
// 两者的 json_schema 形状不同：Responses 把它平铺在 format 上，Chat 要裹一层
// {"json_schema":{name,schema,strict}}。原样转发会被上游当成未知类型拒掉
// （百炼回 "This response_format type is unavailable now"，整轮对话失败）。
func responsesTextFormatToChat(format json.RawMessage) json.RawMessage {
	if len(format) == 0 || string(format) == "null" {
		return nil
	}
	var parsed struct {
		Type   string          `json:"type"`
		Name   string          `json:"name"`
		Strict bool            `json:"strict"`
		Schema json.RawMessage `json:"schema"`
	}
	if json.Unmarshal(format, &parsed) != nil || parsed.Type != "json_schema" {
		return format
	}
	name := parsed.Name
	if name == "" {
		name = "response"
	}
	return json.RawMessage(mustJSON(map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name": name, "strict": parsed.Strict, "schema": parsed.Schema,
		},
	}))
}

// responsesItemToChat 把 input 数组里的一条 item 转成一条 Chat 消息。无法表达的
// item（reasoning、web_search_call、compaction、工具声明等）返回 false 被丢弃：它们
// 要么是上游自己产生的元数据，要么是 hosted 工具的记录，Chat 上游不认。
func responsesItemToChat(raw json.RawMessage, plan *toolPlan) (Message, bool) {
	var item struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Name      string          `json:"name"`
		Namespace string          `json:"namespace"`
		Arguments string          `json:"arguments"`
		Input     string          `json:"input"`
		CallID    string          `json:"call_id"`
		Output    json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return Message{}, false
	}

	switch item.Type {
	case "message":
		role := item.Role
		if role == "" {
			role = "user"
		}
		// developer 是 OpenAI 的新角色名，部分兼容上游只认 system（与
		// chatCompletions 的改写一致）。
		if role == "developer" {
			role = "system"
		}
		return Message{Role: role, Content: responsesContentToChat(item.Content)}, true

	case "function_call", "custom_tool_call":
		// 历史里的调用记的是 Codex 侧的名字，必须换回下发给上游的函数名，否则上游
		// 看到的是一个不在自己工具表里的函数。映射表里没有的（理论不该出现）原样
		// 透传，让上游自己去判断。
		name := item.Name
		if emitted, ok := plan.emittedName(item.Namespace, item.Name); ok {
			name = emitted
		}
		// Responses 把参数放 arguments（JSON 字符串），custom 工具放 input（原始
		// 字符串）。Chat 侧统一成一个 assistant 的 tool_calls。
		arguments := item.Arguments
		if item.Type == "custom_tool_call" {
			arguments = mustJSON(map[string]string{"input": item.Input})
		}
		call := map[string]any{
			"id": item.CallID, "type": "function",
			"function": map[string]string{"name": name, "arguments": arguments},
		}
		// 同一条 assistant 消息里的多个调用在 Codex 的 input 里是分开的 item，
		// 这里逐条成一消息；Chat 上游接受连续的 assistant 消息。
		return Message{Role: "assistant", ToolCalls: json.RawMessage(mustJSON([]any{call}))}, true

	case "function_call_output", "custom_tool_call_output":
		return Message{Role: "tool", ToolCallID: item.CallID, Content: jsonString(functionOutputText(item.Output))}, true
	}
	return Message{}, false
}

// responsesContentToChat 把 Responses 的 content 数组转成 Chat 的 content。全是
// 文本时压成纯字符串（多数上游对纯文本最兼容）；含图片时保留 parts 数组。
func responsesContentToChat(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return jsonString("")
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
		FileID   string `json:"file_id"`
		Detail   string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		// 有些客户端直接给字符串形式的 content。
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return jsonString(text)
		}
		return jsonString("")
	}

	var text strings.Builder
	hasImage := false
	chatParts := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			text.WriteString(part.Text)
			chatParts = append(chatParts, map[string]any{"type": "text", "text": part.Text})
		case "input_image":
			hasImage = true
			url := part.ImageURL
			if url == "" && part.FileID != "" {
				url = part.FileID
			}
			image := map[string]any{"url": url}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			chatParts = append(chatParts, map[string]any{"type": "image_url", "image_url": image})
		}
	}
	if !hasImage {
		return jsonString(text.String())
	}
	return json.RawMessage(mustJSON(chatParts))
}

// responsesToolsToChat 转换工具定义。Responses 的工具是扁平的
// {type,name,description,parameters}，Chat 需要嵌套的 {type,function:{...}}。
//
// Responses Lite 还会把工具裹进 {"type":"namespace"}：namespace 里的名字是裸的
// （functions 里就装 exec / wait / apply_patch），直接丢掉等于整份工具表蒸发，
// 模型只能照着 system 提示里的样子把调用当正文吐出来。所以这里递归展开，按 Codex
// 的 (namespace, name) 语义把名字摊平成一个上游能校验的函数名，并登记进 plan。
func responsesToolsToChat(raws []json.RawMessage, plan *toolPlan) (json.RawMessage, []string) {
	tools := make([]any, 0, len(raws))
	dropped := make([]string, 0)
	for _, raw := range raws {
		var tool struct {
			Type        string            `json:"type"`
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Parameters  json.RawMessage   `json:"parameters"`
			Format      json.RawMessage   `json:"format"`
			Tools       []json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}
		switch tool.Type {
		case "namespace":
			// namespace 本身不是可调用的工具，只是一层分组标签；摊平之后模型直接
			// 调里面的成员。
			tools = append(tools, flattenNamespace(tool.Name, tool.Tools, plan, &dropped)...)
		case "function":
			name := plan.register("", tool.Name, false)
			tools = append(tools, chatFunctionTool(name, tool.Description, tool.Parameters))
		case "custom":
			// Codex 的 freeform 工具（exec、apply_patch）带 lark grammar，Chat 上游
			// 没有这个概念。包成只有一个字符串入参的 function，模型用原始文本填
			// input；响应侧再展开回 custom_tool_call，Codex 就能照常执行。
			name := plan.register("", tool.Name, true)
			tools = append(tools, chatFunctionTool(name, tool.Description, freeformInputSchema(tool.Format)))
		default:
			// web_search / tool_search 是 hosted 工具，Chat 上游无法执行；下发只会
			// 让模型调用一个不存在的函数。web_search 这类形状没有 name 字段，所以
			// 退回记类型名，日志里能看出丢的是什么。
			if tool.Name != "" {
				dropped = append(dropped, tool.Name)
			} else if tool.Type != "" {
				dropped = append(dropped, tool.Type)
			}
		}
	}
	if len(tools) == 0 {
		return nil, dropped
	}
	return json.RawMessage(mustJSON(tools)), dropped
}

// flattenNamespace 展开一层 namespace 的成员。嵌套 namespace 递归处理：Codex 自己
// 目前只发一层，但多一层不会让谁变错。
func flattenNamespace(namespace string, raws []json.RawMessage, plan *toolPlan, dropped *[]string) []any {
	out := make([]any, 0, len(raws))
	for _, raw := range raws {
		var tool struct {
			Type        string            `json:"type"`
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Parameters  json.RawMessage   `json:"parameters"`
			Format      json.RawMessage   `json:"format"`
			Tools       []json.RawMessage `json:"tools"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			continue
		}
		switch tool.Type {
		case "namespace":
			out = append(out, flattenNamespace(tool.Name, tool.Tools, plan, dropped)...)
		case "function":
			name := plan.register(namespace, tool.Name, false)
			out = append(out, chatFunctionTool(name, namespacedDescription(namespace, tool.Description), tool.Parameters))
		case "custom":
			name := plan.register(namespace, tool.Name, true)
			out = append(out, chatFunctionTool(name, namespacedDescription(namespace, tool.Description), freeformInputSchema(tool.Format)))
		default:
			// tool_search 与 standalone web 搜索这类 hosted 工具混在 namespace 里，
			// 一样不能下发。
			*dropped = append(*dropped, tool.Type)
		}
	}
	return out
}

// namespacedDescription 在描述里保留 namespace，让模型知道用户嘴里的 "clock sleep"
// 对应的是同一个工具。
func namespacedDescription(namespace, description string) string {
	if namespace == "" || strings.HasPrefix(description, namespace+".") {
		return description
	}
	if strings.TrimSpace(description) == "" {
		return namespace
	}
	return namespace + ": " + description
}

func chatFunctionTool(name, description string, parameters json.RawMessage) map[string]any {
	if len(parameters) == 0 || string(parameters) == "null" {
		parameters = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name, "description": description, "parameters": parameters,
		},
	}
}

// freeformInputSchema 给 freeform 工具造一个只收字符串的 JSON Schema。描述里带上
// 语法名，让模型知道该往里填什么格式的正文。
func freeformInputSchema(format json.RawMessage) json.RawMessage {
	syntax := "text"
	if len(format) > 0 {
		var f struct {
			Syntax string `json:"syntax"`
		}
		if json.Unmarshal(format, &f) == nil && f.Syntax != "" {
			syntax = f.Syntax
		}
	}
	return json.RawMessage(mustJSON(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"input": map[string]any{"type": "string", "description": "Raw " + syntax + " payload."}},
		"required":             []string{"input"},
		"additionalProperties": false,
	}))
}

// responsesToolChoiceToChat 转换工具选择。Responses 用 {"type":"function","name":N}
// 选中某个函数，Chat 需要多一层 function 包装，函数名同样要换成上游认识的那个。
func responsesToolChoiceToChat(raw json.RawMessage, plan *toolPlan) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return raw
	}
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &choice) != nil || choice.Name == "" {
		return raw
	}
	// Responses 的 tool_choice 只给裸名，namespace 得靠映射表反查；查不到就按默认
	// namespace 处理（顶层工具的情形）。
	emitted := choice.Name
	for _, key := range []string{codexToolKey("", choice.Name), codexToolKey(choice.Name, "")} {
		if mapped, ok := plan.byCodex[key]; ok {
			emitted = mapped
			break
		}
	}
	return json.RawMessage(mustJSON(map[string]any{
		"type":     "function",
		"function": map[string]string{"name": emitted},
	}))
}

// functionOutputText 把工具的 output 压成文本。Codex 的工具结果可能是纯字符串，
// 也可能是结构化的 content items 数组。
func functionOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, part := range parts {
			b.WriteString(part.Text)
		}
		return b.String()
	}
	return string(raw)
}

// responsesSession 选出这一次请求的会话身份，供凭据池的 session 策略做粘性选择。
// Codex 每轮都带 prompt_cache_key，且它天然就是一段会话的指纹，因此优先级最高；
// 其次是客户端显式的会话头，最后才是对话开头的指纹。
func responsesSession(r *http.Request, in responsesRequest) string {
	if key := strings.TrimSpace(in.PromptCacheKey); key != "" {
		return key
	}
	system, firstUser := "", ""
	for _, raw := range in.Input {
		var item struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "message" {
			continue
		}
		var text strings.Builder
		for _, part := range item.Content {
			text.WriteString(part.Text)
		}
		switch item.Role {
		case "system", "developer":
			if system == "" {
				system = text.String()
			}
		case "user":
			if firstUser == "" {
				firstUser = text.String()
			}
		}
	}
	return sessionIdentity(r, system, firstUser)
}

// responses 处理 POST /v1/responses：把 Responses 请求转成 Chat Completions 调上游，
// 再把响应转回 Responses 形状。
func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	tokenID, tokenName, authenticated := s.requireLocalKey(w, r)
	if !authenticated {
		return
	}
	started := time.Now()
	var input responsesRequest
	if !decodeBody(w, r, &input) {
		return
	}
	// 日志里保留客户端真正发来的请求，模型名还没被换成上游名。
	requestBody, _ := json.Marshal(input)
	userAgent := r.Header.Get("User-Agent")

	routes := s.resolveRoutes(input.Model)
	if len(routes) == 0 {
		writeError(w, 404, "model_not_found", "no enabled mapping for model "+input.Model)
		return
	}
	// 本端点只覆盖 Chat Completions 类上游：Responses↔Anthropic 的双向转换没有
	// 实现，与其发一个上游看不懂的请求，不如明确拒掉。同名链上混进 Anthropic 路由时
	// 只把它剔出链尾——OpenAI 兼容的那几家仍能正常 failover；整条链都是 Anthropic
	// 才报 501。
	chat := routes[:0:0]
	for _, target := range routes {
		if target.provider.Kind != provider.KindAnthropic {
			chat = append(chat, target)
		}
	}
	if len(chat) == 0 {
		writeError(w, 501, "not_implemented", "the responses endpoint does not support Anthropic providers; map this model to an OpenAI-compatible provider")
		return
	}
	routes = chat

	chatRequest, plan, droppedTools := input.toChat()
	chatRequest, pluginDeltas := s.applyInputChat(chatRequest)
	clientModel := input.Model
	session := responsesSession(r, input)

	var served requestCredential
	// 日志记最终应答的那条路由，failover 后回填。
	route := routes[0]
	logEvent := func(success bool, responseBody, errorMessage string, tokens tokenUsage) {
		s.recordPluginOutputStats(pluginDeltas, tokens.Output, success)
		_ = s.usage.Record(usage.Event{
			TokenID: tokenID, TokenName: tokenName, ProviderID: route.provider.ID, ProviderName: route.provider.Name,
			ClientModel: route.mapping.ClientModel, UpstreamModel: route.mapping.UpstreamModel,
			UserAgent:   userAgent,
			RequestBody: string(requestBody), ResponseBody: responseBody,
			InputTokens: tokens.Input, OutputTokens: tokens.Output,
			CachedInputTokens: tokens.CachedInput, ReasoningOutputTokens: tokens.ReasoningOutput,
			Success:   success,
			LatencyMS: int(time.Since(started).Milliseconds()), ErrorMessage: errorMessage,
			CredentialID: served.id, CredentialName: served.name, CredentialMask: served.mask,
			PluginDeltas: pluginLogDeltas(pluginDeltas),
		})
	}
	// 被丢弃的 hosted 工具记进日志文本，便于排查「模型为什么没去搜索」。成功时
	// 也写：这是一个可以解释行为的提示，而不是失败原因，成败由 Success 列决定。
	droppedNote := ""
	if len(droppedTools) > 0 {
		droppedNote = "dropped hosted tools: " + strings.Join(droppedTools, ", ")
	}

	response, lease, target, err := s.withRoute(r.Context(), routes, session, func(t routeTarget, key string) (*http.Response, error) {
		payload := chatRequest
		payload.Model = t.mapping.UpstreamModel
		return s.adapters.For(t.provider.Kind).Do(r.Context(), t.provider, key, payload)
	})
	if err != nil {
		route = target
		status, kind, message := credentialError(err, target.provider.Name)
		writeError(w, status, kind, message)
		logEvent(false, "", message, tokenUsage{})
		return
	}
	route = target
	served.set(lease)
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		// 上游的错误体原样透传：Codex 解析 {"error":{message,type,code}}，与上游
		// OpenAI 兼容服务吐的形状一致，改写反而会丢信息。
		body, _ := io.ReadAll(response.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		logEvent(false, string(body), fmt.Sprintf("upstream status %d", response.StatusCode), tokenUsage{})
		return
	}

	if chatRequest.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(200)
		var captured strings.Builder
		tokens, streamErr := pipeChatStreamToResponses(w, s.stallGuarded(response.Body), clientModel, plan, &captured)
		if streamErr != nil {
			logEvent(false, captured.String(), streamErr.Error(), tokens)
			return
		}
		logEvent(true, captured.String(), droppedNote, tokens)
		return
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		writeError(w, 502, "upstream_error", "could not read upstream response")
		logEvent(false, "", "could not read upstream response", tokenUsage{})
		return
	}
	out, tokens := chatResponseToResponses(body, clientModel, plan)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
	logEvent(true, string(out), droppedNote, tokens)
}
