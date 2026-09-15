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
// and streaming Anthropic paths.
func anthropicRequestBody(input Request, stream bool) (map[string]any, error) {
	type amsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	messages := make([]amsg, 0, len(input.Messages))
	system := ""
	for _, m := range input.Messages {
		var content string
		if err := json.Unmarshal(m.Content, &content); err != nil {
			return nil, fmt.Errorf("Anthropic adapter currently supports text content: %w", err)
		}
		if m.Role == "system" {
			system += content + "\n"
		} else {
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
	if system != "" {
		body["system"] = strings.TrimSpace(system)
	}
	return body, nil
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
		pw.CloseWithError(anthropicStreamToOpenAI(raw.Body, input.Model, pw))
	}()
	return &http.Response{StatusCode: raw.StatusCode, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr}, nil
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
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
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
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage usagePayload `json:"usage"`
			} `json:"message"`
			Usage usagePayload `json:"usage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
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
			case "input_json_delta":
				if err := send(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": blocks[event.Index], "function": map[string]any{"arguments": event.Delta.PartialJSON}}}}, nil)); err != nil {
					return err
				}
			}
		case "message_delta":
			usage = usage.merge(event.Usage.tokenUsage())
			finish = openAIFinishReason(event.Delta.StopReason)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
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
