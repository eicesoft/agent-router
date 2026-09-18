package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-router/backend/provider"
)

type Adapter interface {
	Do(context.Context, provider.Provider, string, Request) (*http.Response, error)
}
type AdapterSet struct {
	compatible Adapter
	anthropic  Adapter
}

func NewAdapterSet(client *http.Client) AdapterSet {
	return AdapterSet{compatible: Compatible{Client: client}, anthropic: Anthropic{Client: client}}
}
func (s AdapterSet) For(kind provider.Kind) Adapter {
	if kind == provider.KindAnthropic {
		return s.anthropic
	}
	return s.compatible
}

// Compatible forwards the OpenAI wire format to OpenAI and OpenAI-compatible services.
type Compatible struct{ Client *http.Client }

func (a Compatible) Do(ctx context.Context, p provider.Provider, secret string, input Request) (*http.Response, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	return a.Client.Do(req)
}

// Anthropic translates the subset shared by the OpenAI chat API. It accepts
// text messages and converts Anthropic's JSON response back to OpenAI shape.
type Anthropic struct{ Client *http.Client }

func (a Anthropic) Do(ctx context.Context, p provider.Provider, secret string, input Request) (*http.Response, error) {
	if input.Stream {
		return a.stream(ctx, p, secret, input)
	}
	body, err := anthropicRequestBody(input, false)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", secret)
	req.Header.Set("anthropic-version", "2023-06-01")
	raw, err := a.Client.Do(req)
	if err != nil || raw.StatusCode >= 300 {
		return raw, err
	}
	defer raw.Body.Close()
	var source struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(raw.Body).Decode(&source); err != nil {
		return nil, err
	}
	text := ""
	for _, c := range source.Content {
		text += c.Text
	}
	result := map[string]any{"id": source.ID, "object": "chat.completion", "model": input.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": source.Usage.Input, "completion_tokens": source.Usage.Output, "total_tokens": source.Usage.Input + source.Usage.Output}}
	out, _ := json.Marshal(result)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(out))}, nil
}

// anthropicRequestBody builds the /v1/messages payload shared by the buffered
// and streaming Anthropic paths. Sampling, tool, stop and thinking parameters
// the OpenAI client sent are carried over; max_tokens defaults to 1024 because
// Anthropic requires it and the OpenAI wire format makes it optional.
func anthropicRequestBody(input Request, stream bool) (map[string]any, error) {
	type amsg struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	messages := make([]amsg, 0, len(input.Messages))
	system := ""
	for _, m := range input.Messages {
		var content string
		if err := json.Unmarshal(m.Content, &content); err != nil {
			return nil, fmt.Errorf("Anthropic adapter currently supports text content: %w", err)
		}
		calls := openAIToolCalls(m.ToolCalls)
		switch {
		case m.Role == "system":
			system += content + "\n"
		case m.Role == "tool":
			// OpenAI sends a tool's result as its own message; Anthropic carries
			// it as a tool_result block in the following user turn. Forwarded as a
			// bare "tool" role the upstream rejects the whole request.
			messages = append(messages, amsg{Role: "user", Content: []any{map[string]any{
				"type": "tool_result", "tool_use_id": m.ToolCallID, "content": content,
			}}})
		case len(calls) > 0:
			blocks := make([]any, 0, len(calls)+1)
			if content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": content})
			}
			for _, call := range calls {
				args := json.RawMessage(call.Arguments)
				if !json.Valid(args) {
					args = json.RawMessage(`{}`)
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": args})
			}
			messages = append(messages, amsg{Role: "assistant", Content: blocks})
		default:
			messages = append(messages, amsg{Role: m.Role, Content: content})
		}
	}
	body := map[string]any{"model": input.Model, "max_tokens": 1024, "messages": messages}
	if stream {
		body["stream"] = true
	}
	if input.MaxTokens != nil {
		body["max_tokens"] = *input.MaxTokens
	}
	if input.Temperature != nil {
		body["temperature"] = *input.Temperature
	}
	if input.TopP != nil {
		body["top_p"] = *input.TopP
	}
	if input.Stop != nil {
		body["stop_sequences"] = input.Stop
	}
	if len(input.Tools) > 0 {
		body["tools"] = openAIToolsToAnthropic(input.Tools)
	}
	if len(input.ToolChoice) > 0 {
		if choice := openAIToolChoiceToAnthropic(input.ToolChoice); choice != nil {
			body["tool_choice"] = choice
		}
	}
	// Anthropic 只认 {"type":"enabled","budget_tokens":N}。客户端传来的 thinking
	// 原样转发；只给了 reasoning_effort（演练场的思考档位）时按档位换算预算，
	// 否则这一档对 Anthropic 上游完全不起作用。
	if len(input.Thinking) > 0 && string(input.Thinking) != "null" {
		body["thinking"] = input.Thinking
	} else if budget := thinkingBudget(input.ReasoningEffort); budget > 0 {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	}
	// 开了扩展思考就必须抬高 max_tokens：Anthropic 要求 max_tokens 大于
	// budget_tokens，默认的 1024 会让请求直接 400。
	if body["thinking"] != nil && input.MaxTokens == nil {
		body["max_tokens"] = reasoningFloorMaxTokens
	}
	if system != "" {
		body["system"] = strings.TrimSpace(system)
	}
	return body, nil
}

// openAIToolsToAnthropic rewrites OpenAI tool definitions
// thinkingBudget 把 reasoning_effort 档位换算成 Anthropic 的思考预算（token）。
// 无档位返回 0，调用方据此不发 thinking 字段。
func thinkingBudget(effort *string) int {
	if effort == nil {
		return 0
	}
	switch *effort {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	default:
		return 0
	}
}

// ({type:"function",function:{name,description,parameters}}) into Anthropic's
// ({name,description,input_schema}). Entries that do not parse are skipped
// rather than forwarded, since one malformed tool fails the whole request.
func openAIToolsToAnthropic(raw json.RawMessage) []map[string]any {
	var tools []struct {
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Function.Name == "" {
			continue
		}
		schema := tool.Function.Parameters
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name": tool.Function.Name, "description": tool.Function.Description, "input_schema": schema,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// openAIToolChoiceToAnthropic maps OpenAI's tool_choice onto Anthropic's
// (auto/any/tool). Unmapped values return nil and the field is omitted,
// leaving the upstream default in charge.
func openAIToolChoiceToAnthropic(raw json.RawMessage) map[string]any {
	var choice string
	if err := json.Unmarshal(raw, &choice); err == nil {
		switch choice {
		case "required":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		default:
			return nil
		}
	}
	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err != nil {
		return nil
	}
	switch named.Type {
	case "function":
		return map[string]any{"type": "tool", "name": named.Function.Name}
	case "any":
		return map[string]any{"type": "any"}
	case "none":
		return map[string]any{"type": "none"}
	default:
		return nil
	}
}

// stream forwards a streaming Anthropic request and rewrites the events into
// OpenAI SSE, the wire format the caller asked for. Only the frames that make a
// chat completion are emitted; Anthropic's ping and content_block_stop carry
// nothing an OpenAI client needs.
func (a Anthropic) stream(ctx context.Context, p provider.Provider, secret string, input Request) (*http.Response, error) {
	body, err := anthropicRequestBody(input, true)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", secret)
	req.Header.Set("anthropic-version", "2023-06-01")
	raw, err := a.Client.Do(req)
	if err != nil || raw.StatusCode >= 300 {
		return raw, err
	}

	pr, pw := io.Pipe()
	go func() {
		defer raw.Body.Close()
		defer pw.Close()
		pw.CloseWithError(anthropicStreamToOpenAI(newStallCloser(raw.Body, upstreamStallTimeout), input.Model, pw))
	}()
	return &http.Response{StatusCode: raw.StatusCode, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr}, nil
}

// sseData returns the payload of an SSE "data:" line. The space after the colon
// is optional in the SSE spec and not every gateway writes it — Aliyun's
// Anthropic-compatible endpoint sends "data:{...}" — so requiring the space made
// every event look absent and a healthy stream look truncated.
func sseData(line string) (string, bool) {
	payload, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(payload, " "), true
}

// anthropicStreamToOpenAI emits OpenAI SSE for one Anthropic event stream. The
// final chunk repeats the accumulated usage with an empty choices list, which is
// how OpenAI reports usage on a stream; tool calls arrive as fragments keyed by
// index, and Anthropic's input_json_delta already carries exactly the partial
// JSON string OpenAI expects.
func anthropicStreamToOpenAI(body io.Reader, clientModel string, out io.Writer) error {
	send := func(chunk map[string]any) error {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "data: %s\n\n", payload)
		return err
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": "chatcmpl-anthropic", "object": "chat.completion.chunk", "created": time.Now().Unix(),
			"model":   clientModel,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}

	var usage tokenUsage
	sentRole := false
	sawMessageStop := false
	ensureRole := func() error {
		if sentRole {
			return nil
		}
		sentRole = true
		return send(chunk(map[string]any{"role": "assistant"}, nil))
	}
	// Anthropic indexes every content block; OpenAI counts tool calls alone.
	blocks := map[int]int{}
	finish := "stop"
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Content struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage usagePayload `json:"usage"`
			} `json:"message"`
			Usage usagePayload `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "message_start":
			usage = usage.merge(event.Message.Usage.tokenUsage())
		case "content_block_start":
			if event.Content.Type != "tool_use" {
				continue
			}
			blocks[event.Index] = len(blocks)
			if err := ensureRole(); err != nil {
				return err
			}
			tool := map[string]any{
				"index": blocks[event.Index], "id": event.Content.ID, "type": "function",
				"function": map[string]any{"name": event.Content.Name},
			}
			if err := send(chunk(map[string]any{"tool_calls": []any{tool}}, nil)); err != nil {
				return err
			}
		case "content_block_delta":
			if err := ensureRole(); err != nil {
				return err
			}
			switch event.Delta.Type {
			case "text_delta":
				if err := send(chunk(map[string]any{"content": event.Delta.Text}, nil)); err != nil {
					return err
				}
			case "thinking_delta":
				// Anthropic 的思考增量必须原样转成 reasoning_content，
				// 否则演练场只在 Anthropic 系上游上看到正文没有思考。
				if err := send(chunk(map[string]any{"reasoning_content": event.Delta.Thinking}, nil)); err != nil {
					return err
				}
			case "input_json_delta":
				if err := send(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": blocks[event.Index], "function": map[string]any{"arguments": event.Delta.PartialJSON}}}}, nil)); err != nil {
					return err
				}
			}
		case "message_delta":
			usage = usage.merge(event.Usage.tokenUsage())
			finish = openAIFinishReason(event.Delta.StopReason)
		case "message_stop":
			sawMessageStop = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("anthropic stream ended early: %w", err)
	}
	if !sawMessageStop {
		// The upstream never closed the message. Emitting [DONE] anyway would
		// hand the OpenAI client a stream that looks complete but is missing
		// its answer, so the truncation travels as an error instead.
		return fmt.Errorf("anthropic stream ended without message_stop")
	}
	if sentRole {
		if err := send(chunk(map[string]any{}, finish)); err != nil {
			return err
		}
	}
	usageChunk := map[string]any{
		"id": "chatcmpl-anthropic", "object": "chat.completion.chunk", "created": time.Now().Unix(),
		"model": clientModel, "choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":     usage.Input,
			"completion_tokens": usage.Output,
			"total_tokens":      usage.Input + usage.Output,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": usage.CachedInput,
			},
		},
	}
	if err := send(usageChunk); err != nil {
		return err
	}
	_, err := io.WriteString(out, "data: [DONE]\n\n")
	return err
}

// openAIFinishReason maps Anthropic's stop_reason onto OpenAI's finish_reason.
func openAIFinishReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}
