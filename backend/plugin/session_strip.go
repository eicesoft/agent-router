package plugin

import (
	"encoding/json"
	"strings"
)

// ChatMessage is the plugin-package view of one OpenAI-style message. The
// proxy converts its own Message type at the boundary so this package does
// not import backend/proxy.
type ChatMessage struct {
	Role       string
	Content    json.RawMessage
	ToolCalls  json.RawMessage
	ToolCallID string
}

// AnthropicMessage is the /v1/messages view of one turn.
type AnthropicMessage struct {
	Role    string
	Content json.RawMessage
}

// textPart is one content part we understand for stripping. Non-text parts
// (images, input_audio, …) are opaque and always kept.
type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// contentShape is OpenAI/Anthropic content: either a bare string or an array.
type contentShape struct {
	isArray bool
	text    string
	parts   []json.RawMessage
}

func parseContent(raw json.RawMessage) contentShape {
	var out contentShape
	if len(raw) == 0 {
		return out
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		out.text = s
		return out
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		out.isArray = true
		out.parts = parts
	}
	return out
}

// StripChatMessages drops text that already appeared in an earlier message of
// the same request. First occurrence is always kept (lossless for the model).
// Tool-paired messages (tool_call_id / tool_calls) are never removed so the
// tool round-trip stays valid; their text still enters the seen-set.
func StripChatMessages(messages []ChatMessage) []ChatMessage {
	seen := make(map[string]struct{})
	out := make([]ChatMessage, 0, len(messages))
	for _, m := range messages {
		if m.ToolCallID != "" || len(m.ToolCalls) > 0 {
			registerText(seen, contentText(m.Content))
			out = append(out, m)
			continue
		}
		content := parseContent(m.Content)
		switch {
		case !content.isArray:
			if content.text == "" {
				out = append(out, m)
				continue
			}
			if _, dup := seen[content.text]; dup {
				continue
			}
			registerText(seen, content.text)
			out = append(out, m)
		default:
			kept, allDup := stripTextParts(content.parts, seen, "text")
			if allDup {
				continue
			}
			if len(kept) == len(content.parts) {
				out = append(out, m)
				continue
			}
			encoded, err := json.Marshal(kept)
			if err != nil {
				out = append(out, m)
				continue
			}
			m.Content = encoded
			out = append(out, m)
		}
	}
	return out
}

// StripAnthropicMessages is the /v1/messages counterpart. System stays on the
// request (not a turn); tool_use / tool_result blocks are structural and
// always kept.
func StripAnthropicMessages(messages []AnthropicMessage) []AnthropicMessage {
	seen := make(map[string]struct{})
	out := make([]AnthropicMessage, 0, len(messages))
	for _, m := range messages {
		content := parseContent(m.Content)
		switch {
		case !content.isArray:
			if content.text == "" {
				out = append(out, m)
				continue
			}
			if _, dup := seen[content.text]; dup {
				continue
			}
			registerText(seen, content.text)
			out = append(out, m)
		default:
			kept, allDup := stripTextParts(content.parts, seen, "text")
			if allDup {
				continue
			}
			if len(kept) == len(content.parts) {
				out = append(out, m)
				continue
			}
			encoded, err := json.Marshal(kept)
			if err != nil {
				out = append(out, m)
				continue
			}
			m.Content = encoded
			out = append(out, m)
		}
	}
	return out
}

// stripTextParts filters parts whose type matches wantType and whose exact
// text is already in seen. Returns kept parts and whether every original part
// was a filtered duplicate (callers then drop the whole message).
func stripTextParts(parts []json.RawMessage, seen map[string]struct{}, wantType string) ([]json.RawMessage, bool) {
	kept := make([]json.RawMessage, 0, len(parts))
	dropped := 0
	for _, raw := range parts {
		var p textPart
		if err := json.Unmarshal(raw, &p); err == nil && p.Type == wantType && p.Text != "" {
			if _, dup := seen[p.Text]; dup {
				dropped++
				continue
			}
			registerText(seen, p.Text)
		}
		kept = append(kept, raw)
	}
	return kept, len(parts) > 0 && dropped == len(parts)
}

// ContentText concatenates string or text-part content for stats.
func ContentText(raw json.RawMessage) string {
	return contentText(raw)
}

func contentText(raw json.RawMessage) string {
	content := parseContent(raw)
	if !content.isArray {
		return content.text
	}
	var b strings.Builder
	for _, part := range content.parts {
		var p textPart
		if err := json.Unmarshal(part, &p); err == nil && (p.Type == "text" || p.Type == "") {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func registerText(seen map[string]struct{}, text string) {
	if text != "" {
		seen[text] = struct{}{}
	}
}
