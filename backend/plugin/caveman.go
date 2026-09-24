package plugin

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed caveman_skill.md
var cavemanSkillMD string

// Caveman intensity levels (skill dial). "文*" is the wenyan (classical
// Chinese) family from the upstream caveman skill.
const (
	CavemanLevelLite        = "lite"
	CavemanLevelFull        = "full"
	CavemanLevelUltra       = "ultra"
	CavemanLevelWenyanLite  = "wenyan-lite"
	CavemanLevelWenyanFull  = "wenyan-full"
	CavemanLevelWenyanUltra = "wenyan-ultra"
)

// DefaultCavemanLevel matches the upstream skill default.
const DefaultCavemanLevel = CavemanLevelFull

// CavemanLevels lists valid levels in UI order.
var CavemanLevels = []string{
	CavemanLevelLite,
	CavemanLevelFull,
	CavemanLevelUltra,
	CavemanLevelWenyanLite,
	CavemanLevelWenyanFull,
	CavemanLevelWenyanUltra,
}

// NormalizeCavemanLevel maps unknown/empty config to the default level.
func NormalizeCavemanLevel(level string) string {
	level = strings.TrimSpace(level)
	for _, known := range CavemanLevels {
		if level == known {
			return level
		}
	}
	return DefaultCavemanLevel
}

// CavemanRules returns the embedded skill body with the active level pinned
// at the top so every request is self-contained (no session memory).
func CavemanRules(level string) string {
	level = NormalizeCavemanLevel(level)
	body := strings.TrimSpace(stripYAMLFrontmatter(cavemanSkillMD))
	return "Active caveman level for this request: **" + level + "**.\n\n" + body
}

func stripYAMLFrontmatter(src string) string {
	trimmed := strings.TrimPrefix(src, "\ufeff")
	if !strings.HasPrefix(trimmed, "---\n") {
		return trimmed
	}
	rest := trimmed[4:]
	if idx := strings.Index(rest, "\n---\n"); idx >= 0 {
		return rest[idx+5:]
	}
	return trimmed
}

// InjectCavemanChat prepends/appends the caveman rules as system content.
// Existing system text is kept first so client system instructions still
// outrank the style pack (same precedence as the upstream skill).
func InjectCavemanChat(messages []ChatMessage, rules string) []ChatMessage {
	if rules == "" {
		return messages
	}
	for i, m := range messages {
		if m.Role != "system" && m.Role != "developer" {
			continue
		}
		m.Content = appendSystemText(m.Content, rules)
		messages[i] = m
		return messages
	}
	out := make([]ChatMessage, 0, len(messages)+1)
	out = append(out, ChatMessage{Role: "system", Content: marshalSystemText(rules)})
	out = append(out, messages...)
	return out
}

// InjectCavemanAnthropic appends the rules to the top-level system field.
func InjectCavemanAnthropic(system json.RawMessage, rules string) json.RawMessage {
	if rules == "" {
		return system
	}
	if len(system) == 0 || isNullJSON(system) {
		return marshalSystemText(rules)
	}
	return appendSystemText(system, rules)
}

func isNullJSON(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func marshalSystemText(text string) json.RawMessage {
	encoded, err := json.Marshal(text)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// appendSystemText joins rules onto an OpenAI/Anthropic system payload,
// whether that payload is a bare string or a content-part array.
func appendSystemText(system json.RawMessage, rules string) json.RawMessage {
	var s string
	if err := json.Unmarshal(system, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return marshalSystemText(rules)
		}
		return marshalSystemText(s + "\n\n" + rules)
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(system, &parts); err == nil {
		// Prefer extending the last text part so we do not invent a second
		// block the upstream might order differently.
		for i := len(parts) - 1; i >= 0; i-- {
			var p struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(parts[i], &p); err == nil && (p.Type == "text" || p.Type == "") {
				p.Text = p.Text + "\n\n" + rules
				encoded, err := json.Marshal(p)
				if err == nil {
					parts[i] = encoded
					out, err := json.Marshal(parts)
					if err == nil {
						return out
					}
				}
				break
			}
		}
		encoded, err := json.Marshal(map[string]string{"type": "text", "text": rules})
		if err == nil {
			parts = append(parts, encoded)
			if out, err := json.Marshal(parts); err == nil {
				return out
			}
		}
	}
	// Unrecognized shape: replace with a plain string rather than drop rules.
	return marshalSystemText(rules)
}
