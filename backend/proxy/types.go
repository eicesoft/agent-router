package proxy

import "encoding/json"

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Name    string          `json:"name,omitempty"`
	// Tool-call roundtrip: assistant history carries tool_calls, tool results
	// carry tool_call_id. Dropping either breaks upstream tool state.
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}
type Request struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	Stream        bool            `json:"stream,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
	// Tool and sampling parameters agent clients (opencode, Cline, ...) send.
	// json.RawMessage passes string-or-object shapes through untouched.
	Tools             json.RawMessage `json:"tools,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	Stop              json.RawMessage `json:"stop,omitempty"`
	Seed              *int            `json:"seed,omitempty"`
	FrequencyPenalty  *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty   *float64        `json:"presence_penalty,omitempty"`
	ResponseFormat    json.RawMessage `json:"response_format,omitempty"`
	// Thinking / reasoning controls. Clients set reasoning_effort (OpenAI
	// o-series and DeepSeek/Gemini-compatible gateways), thinking (Anthropic's
	// {"type":"enabled","budget_tokens":N}, which some OpenAI-compatible
	// upstreams also accept), or provider-specific extras below. All are passed
	// through verbatim; cross-format mapping happens in anthropicViaOpenAI.
	ReasoningEffort    *string         `json:"reasoning_effort,omitempty"`
	Thinking           json.RawMessage `json:"thinking,omitempty"`
	Verbosity          *string         `json:"verbosity,omitempty"`
	ChatTemplateKwargs json.RawMessage `json:"chat_template_kwargs,omitempty"`
	EnableThinking     *bool           `json:"enable_thinking,omitempty"`
}

// Anthropic request/response types for the /v1/messages endpoint.
type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	System      json.RawMessage    `json:"system,omitempty"`
	MaxTokens   int                `json:"max_tokens,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	TopK        *int               `json:"top_k,omitempty"`
	StopSeq     json.RawMessage    `json:"stop_sequences,omitempty"`
	Tools       json.RawMessage    `json:"tools,omitempty"`
	ToolChoice  json.RawMessage    `json:"tool_choice,omitempty"`
	Metadata    json.RawMessage    `json:"metadata,omitempty"`
	// Extended thinking. Forwarded verbatim to Anthropic upstreams; mapped to
	// reasoning_effort when the upstream is OpenAI-compatible.
	Thinking json.RawMessage `json:"thinking,omitempty"`
}

type AnthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// tool_use blocks. Input is raw so the model's arguments reach the client
	// verbatim, key order included.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type AnthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []AnthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
}
type OpenAIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code,omitempty"`
	} `json:"error"`
}

// Model and ModelList match the OpenAI List Models response schema.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}
