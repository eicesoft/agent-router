package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// toChat 把 Responses 请求转成本网关内部的 Chat Completions 请求。返回的
// customTools 是「freeform 工具名集合」，响应侧靠它把上游的 tool_calls 还原成
// Codex 的 custom_tool_call item；dropped 是被丢弃的 hosted 工具名，写进日志备查。
func (in responsesRequest) toChat() (Request, map[string]bool, []string) {
	messages := make([]Message, 0, len(in.Input)+1)
	if strings.TrimSpace(in.Instructions) != "" {
		messages = append(messages, Message{Role: "system", Content: jsonString(in.Instructions)})
	}

	customTools := map[string]bool{}
	tools, dropped := responsesToolsToChat(in.Tools, customTools)
	for _, raw := range in.Input {
		if message, ok := responsesItemToChat(raw, customTools); ok {
			messages = append(messages, message)
		}
	}

	out := Request{
		Model:             in.Model,
		Messages:          messages,
		Stream:            in.Stream,
		Temperature:       in.Temperature,
		TopP:              in.TopP,
		Tools:             tools,
		ToolChoice:        responsesToolChoiceToChat(in.ToolChoice, customTools),
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
		if len(in.Text.Format) > 0 && string(in.Text.Format) != "null" {
			out.ResponseFormat = in.Text.Format
		}
	}
	// 上游只在被要求时才在流里报用量；不强制就等于日志恒为 0 token（与
	// anthropicViaOpenAI 的处理一致）。
	if in.Stream {
		out.StreamOptions = map[string]bool{"include_usage": true}
	}
	return out, customTools, dropped
}

// responsesItemToChat 把 input 数组里的一条 item 转成一条 Chat 消息。无法表达的
// item（reasoning、web_search_call、compaction 等）返回 false 被丢弃：它们要么是
// 上游自己产生的元数据，要么是 hosted 工具的记录，Chat 上游不认。
func responsesItemToChat(raw json.RawMessage, customTools map[string]bool) (Message, bool) {
	var item struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Name      string          `json:"name"`
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
		// Responses 把参数放 arguments（JSON 字符串），custom 工具放 input（原始
		// 字符串）。Chat 侧统一成一个 assistant 的 tool_calls。
		arguments := item.Arguments
		if item.Type == "custom_tool_call" {
			arguments = mustJSON(map[string]string{"input": item.Input})
		}
		call := map[string]any{
			"id": item.CallID, "type": "function",
			"function": map[string]string{"name": item.Name, "arguments": arguments},
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
func responsesToolsToChat(raws []json.RawMessage, customTools map[string]bool) (json.RawMessage, []string) {
	if len(raws) == 0 {
		return nil, nil
	}
	tools := make([]any, 0, len(raws))
	dropped := make([]string, 0)
	for _, raw := range raws {
		var tool struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Format      json.RawMessage `json:"format"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}
		switch tool.Type {
		case "function":
			tools = append(tools, chatFunctionTool(tool.Name, tool.Description, tool.Parameters))
		case "custom":
			// Codex 的 freeform 工具（apply_patch 等）带 lark grammar，Chat 上游
			// 没有这个概念。包成只有一个字符串入参的 function，模型用原始文本填
			// input；响应侧再展开回 custom_tool_call，Codex 就能照常执行。
			customTools[tool.Name] = true
			tools = append(tools, chatFunctionTool(tool.Name, tool.Description, freeformInputSchema(tool.Format)))
		default:
			// web_search / tool_search / namespace 是 hosted 工具，Chat 上游无法
			// 执行；下发只会让模型调用一个不存在的函数。web_search 这类形状没有
			// name 字段，所以退回记类型名，日志里能看出丢的是什么。
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
// 选中某个函数，Chat 需要多一层 function 包装。
func responsesToolChoiceToChat(raw json.RawMessage, customTools map[string]bool) json.RawMessage {
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
	return json.RawMessage(mustJSON(map[string]any{
		"type":     "function",
		"function": map[string]string{"name": choice.Name},
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
	// 本端点只覆盖 Chat Completions 类上游：Responses↔Anthropic 的双向转换没有
	// 实现，与其发一个上游看不懂的请求，不如明确拒掉。
	if p.Kind == provider.KindAnthropic {
		writeError(w, 501, "not_implemented", "the responses endpoint does not support Anthropic providers; map this model to an OpenAI-compatible provider")
		return
	}

	chatRequest, customTools, droppedTools := input.toChat()
	clientModel := input.Model
	session := responsesSession(r, input)

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
	// 被丢弃的 hosted 工具记进日志文本，便于排查「模型为什么没去搜索」。成功时
	// 也写：这是一个可以解释行为的提示，而不是失败原因，成败由 Success 列决定。
	droppedNote := ""
	if len(droppedTools) > 0 {
		droppedNote = "dropped hosted tools: " + strings.Join(droppedTools, ", ")
	}

	chatRequest.Model = mapping.UpstreamModel
	response, lease, err := s.withCredential(r.Context(), p, session, func(key string) (*http.Response, error) {
		return s.adapters.For(p.Kind).Do(r.Context(), p, key, chatRequest)
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
		tokens, streamErr := pipeChatStreamToResponses(w, s.stallGuarded(response.Body), clientModel, customTools, &captured)
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
	out, tokens := chatResponseToResponses(body, clientModel, customTools)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
	logEvent(true, string(out), droppedNote, tokens)
}
