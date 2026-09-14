package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
		return nil, fmt.Errorf("Anthropic streaming adapter is not enabled yet")
	}
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
	if input.MaxTokens != nil {
		body["max_tokens"] = *input.MaxTokens
	}
	if system != "" {
		body["system"] = strings.TrimSpace(system)
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
