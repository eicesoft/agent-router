package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompressLogDropsNoiseAndKeepsErrors(t *testing.T) {
	var b strings.Builder
	b.WriteString("2024-01-01 INFO starting\n")
	for i := 0; i < 30; i++ {
		b.WriteString("DEBUG loop iteration\n")
	}
	b.WriteString("ERROR failed to save user id=42\n")
	b.WriteString("    at main.run(main.go:10)\n")
	// Enough kept lines that ultra's middle elision fires after noise is gone.
	for i := 0; i < 30; i++ {
		b.WriteString("WARN slow query\n")
	}
	b.WriteString("ERROR boom\n")
	src := b.String()
	if got := compressLog(src, 1); strings.Contains(got, "DEBUG loop") {
		t.Fatalf("lite should drop DEBUG:\n%s", got)
	} else if !strings.Contains(got, "ERROR failed to save") {
		t.Fatalf("lite must keep errors:\n%s", got)
	}
	ultra := compressLog(src, 3)
	if !strings.Contains(ultra, "lines elided") {
		t.Fatalf("ultra should elide middle:\n%s", ultra)
	}
	if !strings.Contains(ultra, "ERROR boom") {
		t.Fatalf("ultra must keep tail errors:\n%s", ultra)
	}
	if !strings.Contains(ultra, "ERROR failed to save") {
		t.Fatalf("ultra must keep head errors:\n%s", ultra)
	}
}

func TestCompressJSONFoldsLargeArrays(t *testing.T) {
	items := make([]map[string]any, 0, 40)
	for i := 0; i < 40; i++ {
		items = append(items, map[string]any{"id": i, "name": "row"})
	}
	payload := map[string]any{"error": "none", "rows": items}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := compressJSONText(string(raw), 2)
	if !strings.Contains(got, "_caveman_folded") {
		t.Fatalf("expected fold marker: %s", got)
	}
	if !strings.Contains(got, `"error"`) {
		t.Fatalf("error field must survive: %s", got)
	}
	if len(got) >= len(raw) {
		t.Fatalf("compressed should be smaller: %d vs %d", len(got), len(raw))
	}
}

func TestCompressChatMessagesOnlyTouchesLongToolPayloads(t *testing.T) {
	short := ChatMessage{Role: "user", Content: json.RawMessage(`"hi"`)}
	longLog := strings.Repeat("INFO line\n", 80) + "ERROR bad thing\n"
	tool := ChatMessage{Role: "tool", Content: json.RawMessage(`"` + longLog + `"`)}
	// Properly encode as JSON string
	encoded, err := json.Marshal(longLog)
	if err != nil {
		t.Fatal(err)
	}
	tool.Content = encoded

	out := CompressChatMessages([]ChatMessage{short, tool}, CavemanLevelFull)
	if string(out[0].Content) != string(short.Content) {
		t.Fatalf("short user message must not change")
	}
	if string(out[1].Content) == string(tool.Content) {
		t.Fatalf("long tool payload should compress")
	}
	var text string
	if err := json.Unmarshal(out[1].Content, &text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "INFO line") {
		t.Fatalf("INFO should be dropped at full: %s", text[:200])
	}
	if !strings.Contains(text, "ERROR bad thing") {
		t.Fatalf("ERROR must remain")
	}
}

func TestCompressionIntensityMapsWenyan(t *testing.T) {
	if compressionIntensity(CavemanLevelWenyanLite) != compressionIntensity(CavemanLevelLite) {
		t.Fatal("wenyan-lite intensity should match lite")
	}
	if compressionIntensity(CavemanLevelWenyanUltra) != compressionIntensity(CavemanLevelUltra) {
		t.Fatal("wenyan-ultra intensity should match ultra")
	}
}

func TestCompressChatLeavesToolCallsStructuresAlone(t *testing.T) {
	msg := ChatMessage{
		Role:      "assistant",
		Content:   json.RawMessage(`null`),
		ToolCalls: json.RawMessage(`[{"id":"1","function":{"name":"f","arguments":"{}"}}]`),
	}
	out := CompressChatMessages([]ChatMessage{msg}, CavemanLevelUltra)
	if string(out[0].ToolCalls) != string(msg.ToolCalls) {
		t.Fatalf("tool_calls must be untouched")
	}
}
