package plugin

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// compressMinRunes ignores short conversational turns so ordinary chat is
// never rewritten. Tool dumps and long logs clear this easily.
const compressMinRunes = 480

// compressionIntensity maps the six caveman levels onto three input-compression
// gears. 文* shares its English counterpart's aggressiveness — classical
// Chinese is an output register, not a different payload cutter.
func compressionIntensity(level string) int {
	switch NormalizeCavemanLevel(level) {
	case CavemanLevelLite, CavemanLevelWenyanLite:
		return 1
	case CavemanLevelUltra, CavemanLevelWenyanUltra:
		return 3
	default:
		return 2
	}
}

// CompressChatMessages rewrites compressible text inside OpenAI-style messages
// (tool results, long JSON/log dumps). Structural fields — tool_calls,
// tool_call_id, non-text parts — stay byte-identical.
func CompressChatMessages(messages []ChatMessage, level string) []ChatMessage {
	intensity := compressionIntensity(level)
	out := make([]ChatMessage, len(messages))
	copy(out, messages)
	for i, m := range out {
		// Assistant tool_calls arguments are protocol, not prose.
		if len(m.ToolCalls) > 0 {
			continue
		}
		role := m.Role
		// Tool results and oversized turns are the proxy-side win.
		if role != "tool" && !contentWorthCompressing(m.Content) {
			continue
		}
		out[i].Content = compressContent(m.Content, intensity)
	}
	return out
}

// CompressAnthropicMessages is the /v1/messages counterpart. tool_use /
// tool_result blocks keep their structure; only text payloads are cut.
func CompressAnthropicMessages(messages []AnthropicMessage, level string) []AnthropicMessage {
	intensity := compressionIntensity(level)
	out := make([]AnthropicMessage, len(messages))
	copy(out, messages)
	for i, m := range out {
		if !contentWorthCompressing(m.Content) && !hasToolResult(m.Content) {
			continue
		}
		out[i].Content = compressContent(m.Content, intensity)
	}
	return out
}

func contentWorthCompressing(raw json.RawMessage) bool {
	return len([]rune(pluginTextForCompress(raw))) >= compressMinRunes
}

func pluginTextForCompress(raw json.RawMessage) string {
	// Reuse the shared text extractor (string or text parts).
	return contentText(raw)
}

func hasToolResult(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// compressContent walks string-or-parts content and rewrites eligible text.
func compressContent(raw json.RawMessage, intensity int) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		next := compressText(s, intensity)
		if next == s {
			return raw
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return raw
		}
		return encoded
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		changed := false
		for i, part := range parts {
			next, ok := compressPart(part, intensity)
			if ok {
				parts[i] = next
				changed = true
			}
		}
		if !changed {
			return raw
		}
		encoded, err := json.Marshal(parts)
		if err != nil {
			return raw
		}
		return encoded
	}
	return raw
}

// compressPart handles text parts, tool_result wrappers, and input_json fields.
func compressPart(raw json.RawMessage, intensity int) (json.RawMessage, bool) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return raw, false
	}
	changed := false

	if textRaw, ok := generic["text"]; ok {
		var text string
		if err := json.Unmarshal(textRaw, &text); err == nil {
			if next := compressText(text, intensity); next != text {
				encoded, err := json.Marshal(next)
				if err == nil {
					generic["text"] = encoded
					changed = true
				}
			}
		}
	}

	// Anthropic tool_result carries content as string or nested blocks.
	if contentRaw, ok := generic["content"]; ok {
		if next, ok := compressMaybeContent(contentRaw, intensity); ok {
			generic["content"] = next
			changed = true
		}
	}

	// OpenAI tool calls sometimes nest JSON arguments as a string field.
	if argsRaw, ok := generic["input_json"]; ok {
		var args string
		if err := json.Unmarshal(argsRaw, &args); err == nil {
			if next := compressText(args, intensity); next != args {
				encoded, err := json.Marshal(next)
				if err == nil {
					generic["input_json"] = encoded
					changed = true
				}
			}
		}
	}

	if !changed {
		return raw, false
	}
	encoded, err := json.Marshal(generic)
	if err != nil {
		return raw, false
	}
	return encoded, true
}

func compressMaybeContent(raw json.RawMessage, intensity int) (json.RawMessage, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		next := compressText(s, intensity)
		if next == s {
			return raw, false
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return raw, false
		}
		return encoded, true
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err == nil {
		changed := false
		for i, block := range blocks {
			if next, ok := compressPart(block, intensity); ok {
				blocks[i] = next
				changed = true
			}
		}
		if !changed {
			return raw, false
		}
		encoded, err := json.Marshal(blocks)
		if err != nil {
			return raw, false
		}
		return encoded, true
	}
	return raw, false
}

var (
	logNoiseLine = regexp.MustCompile(`(?i)^\s*(debug|trace|info)\b`)
	logKeepLine  = regexp.MustCompile(`(?i)(error|fatal|panic|exception|traceback|warn|warning|failed|failure|\bat\s+\S+\.\w+:\d+)`)
)

// compressText picks a payload compressor by shape, then applies intensity.
func compressText(s string, intensity int) string {
	if len([]rune(s)) < compressMinRunes {
		return s
	}
	trimmed := strings.TrimSpace(s)
	switch {
	case looksLikeJSON(trimmed):
		return compressJSONText(trimmed, intensity)
	case looksLikeLog(s):
		return compressLog(s, intensity)
	default:
		return compressLongText(s, intensity)
	}
}

func looksLikeJSON(s string) bool {
	if s == "" {
		return false
	}
	if s[0] != '{' && s[0] != '[' {
		return false
	}
	return json.Valid([]byte(s))
}

func looksLikeLog(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) < 8 {
		return false
	}
	noise, keep := 0, 0
	sample := lines
	if len(sample) > 40 {
		sample = sample[:40]
	}
	for _, line := range sample {
		switch {
		case logNoiseLine.MatchString(line):
			noise++
		case logKeepLine.MatchString(line):
			keep++
		}
	}
	return noise >= 3 || (noise+keep >= 4 && strings.Contains(s, "\n"))
}

// compressJSONText re-encodes JSON compactly; at higher intensities it folds
// large homogeneous arrays down to head/tail samples.
func compressJSONText(s string, intensity int) string {
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return s
	}
	if intensity >= 2 {
		v = foldJSON(v, intensity)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return s
	}
	// Prefer the rewrite only when it is actually smaller (compact alone
	// already wins on pretty-printed payloads).
	if len(out) < len(s) {
		return string(out)
	}
	return s
}

func foldJSON(v any, intensity int) any {
	switch t := v.(type) {
	case map[string]any:
		for key, val := range t {
			// Always keep diagnostics.
			lower := strings.ToLower(key)
			if strings.Contains(lower, "error") ||
				strings.Contains(lower, "message") ||
				strings.Contains(lower, "status") ||
				strings.Contains(lower, "stack") {
				continue
			}
			t[key] = foldJSON(val, intensity)
		}
		return t
	case []any:
		if len(t) <= 12 {
			for i := range t {
				t[i] = foldJSON(t[i], intensity)
			}
			return t
		}
		keepHead := 4
		keepTail := 2
		if intensity >= 3 {
			keepHead = 2
			keepTail = 1
		}
		folded := make([]any, 0, keepHead+keepTail+1)
		for i := 0; i < keepHead && i < len(t); i++ {
			folded = append(folded, foldJSON(t[i], intensity))
		}
		folded = append(folded, map[string]any{
			"_caveman_folded": len(t) - keepHead - keepTail,
			"_note":           "middle items elided",
		})
		for i := len(t) - keepTail; i < len(t); i++ {
			if i >= keepHead {
				folded = append(folded, foldJSON(t[i], intensity))
			}
		}
		return folded
	default:
		return v
	}
}

// compressLog drops noise lines and optionally collapses the middle of long runs.
func compressLog(s string, intensity int) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	// Dedupe identical non-keep lines at full/ultra.
	seen := map[string]int{}
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if intensity >= 1 && logNoiseLine.MatchString(trimmed) && !logKeepLine.MatchString(trimmed) {
			continue
		}
		if intensity >= 2 && !logKeepLine.MatchString(trimmed) {
			key := strings.TrimSpace(trimmed)
			if key != "" {
				seen[key]++
				if seen[key] > 1 {
					continue
				}
			}
		}
		kept = append(kept, trimmed)
	}
	if intensity >= 3 && len(kept) > 24 {
		head := kept[:8]
		tail := kept[len(kept)-4:]
		mid := []string{sprintfFold(len(kept) - 12)}
		kept = append(append(head, mid...), tail...)
	}
	out := strings.Join(kept, "\n")
	if out == "" {
		return s
	}
	return out
}

func sprintfFold(n int) string {
	return "[caveman] " + strconv.Itoa(n) + " lines elided"
}

// compressLongText keeps head/tail for prose dumps that are neither JSON nor
// logs — search hits, HTML, CSV previews.
func compressLongText(s string, intensity int) string {
	if intensity < 2 {
		// lite: only strip blank-line runs
		return collapseBlankLines(s)
	}
	runes := []rune(s)
	if len(runes) < compressMinRunes*2 {
		return collapseBlankLines(s)
	}
	headN := 1200
	tailN := 400
	if intensity >= 3 {
		headN = 600
		tailN = 200
	}
	if len(runes) <= headN+tailN+40 {
		return collapseBlankLines(s)
	}
	head := string(runes[:headN])
	tail := string(runes[len(runes)-tailN:])
	elided := len(runes) - headN - tailN
	return head + "\n[caveman] " + strconv.Itoa(elided) + " chars elided\n" + tail
}

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
