package plugin

import "testing"

func chat(role, content string) ChatMessage {
	return ChatMessage{Role: role, Content: []byte(`"` + content + `"`)}
}

func TestStripChatDropsDuplicateAcrossTurns(t *testing.T) {
	in := []ChatMessage{
		chat("system", "You are helpful."),
		chat("user", "Hello"),
		chat("assistant", "Hi"),
		chat("user", "Hello"), // same text as first user turn
		chat("user", "New question"),
	}
	out := StripChatMessages(in)
	if len(out) != 4 {
		t.Fatalf("len(out) = %d, want 4 (duplicate user turn dropped)", len(out))
	}
	if ContentText(out[1].Content) != "Hello" || ContentText(out[3].Content) != "New question" {
		t.Fatalf("unexpected messages: %s", ContentText(out[3].Content))
	}
}

func TestStripChatKeepsFirstOccurrence(t *testing.T) {
	in := []ChatMessage{
		chat("user", "shared context"),
		chat("assistant", "ok"),
		chat("user", "shared context"),
	}
	out := StripChatMessages(in)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if ContentText(out[0].Content) != "shared context" {
		t.Fatalf("first occurrence was not kept: %q", ContentText(out[0].Content))
	}
}

func TestStripChatDropsDuplicateTextBlockOnly(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: []byte(`[{"type":"text","text":"file body"}]`)},
		{Role: "assistant", Content: []byte(`"done"`)},
		{Role: "user", Content: []byte(`[{"type":"text","text":"file body"},{"type":"text","text":"now fix tests"}]`)},
	}
	out := StripChatMessages(in)
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	text := ContentText(out[2].Content)
	if text != "now fix tests" {
		t.Fatalf("kept content = %q, want only the new block", text)
	}
}

func TestStripChatNeverDropsToolLegs(t *testing.T) {
	in := []ChatMessage{
		chat("user", "run it"),
		{Role: "assistant", ToolCalls: []byte(`[{"id":"c1","function":{"name":"x"}}]`), Content: []byte(`""`)},
		{Role: "tool", ToolCallID: "c1", Content: []byte(`"result"`)},
		// Identical tool result text must not remove the pairing message.
		{Role: "tool", ToolCallID: "c2", Content: []byte(`"result"`)},
	}
	out := StripChatMessages(in)
	if len(out) != 4 {
		t.Fatalf("len(out) = %d, want 4 (tool legs kept)", len(out))
	}
}

func TestStripAnthropicDropsDuplicateStringTurn(t *testing.T) {
	in := []AnthropicMessage{
		{Role: "user", Content: []byte(`"shared"`)},
		{Role: "assistant", Content: []byte(`"ok"`)},
		{Role: "user", Content: []byte(`"shared"`)},
	}
	out := StripAnthropicMessages(in)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
}

func TestStripAnthropicKeepsNonTextBlocks(t *testing.T) {
	in := []AnthropicMessage{
		{Role: "user", Content: []byte(`[{"type":"image","source":{"type":"base64","data":"xx"}}]`)},
		{Role: "user", Content: []byte(`[{"type":"image","source":{"type":"base64","data":"xx"}}]`)},
	}
	out := StripAnthropicMessages(in)
	// Images are not text-deduped: both messages stay (only exact text blocks strip).
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (non-text not stripped as text)", len(out))
	}
}

func TestEstimateTokensCountsCJK(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("empty = %d", got)
	}
	// 4 ASCII ≈ 1 token
	if got := EstimateTokens("abcd"); got != 1 {
		t.Fatalf("abcd = %d, want 1", got)
	}
	// 3 CJK = 3 tokens
	if got := EstimateTokens("你好啊"); got != 3 {
		t.Fatalf("CJK = %d, want 3", got)
	}
}
