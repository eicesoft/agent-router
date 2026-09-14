package templates

import (
	"strings"
	"testing"
)

// mergeOMPBytes keeps bytes outside the gateway block byte-for-byte, so a
// user's flow-styled mappings and comments survive a merge. These tests cover
// the append and replace paths with in-memory configs.

func TestMergeOMPBytesAppendPreservesFlow(t *testing.T) {
	orig := `providers:
  router:
    baseUrl: http://localhost:20128/v1
    cost: { input: 0.8, output: 2.7, cacheRead: 0.1, cacheWrite: 1.25 }
`
	node, ok := parseOMP([]byte(orig))
	if !ok {
		t.Fatal("parseOMP failed")
	}
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "m1", Name: "m1"}})
	out := mergeOMPBytes(node, []byte(orig), g)

	// existing router block untouched, including its flow-style cost line
	if !strings.Contains(out, "cost: { input: 0.8, output: 2.7, cacheRead: 0.1, cacheWrite: 1.25 }") {
		t.Fatalf("flow style lost:\n%s", out)
	}
	// gateway block appended, indented under providers
	if !strings.Contains(out, "\n  agent-router:\n    baseUrl: http://127.0.0.1:9400/v1\n") {
		t.Fatalf("gateway block missing or misindented:\n%s", out)
	}
	// only one agent-router key
	if strings.Count(out, "agent-router:") != 1 {
		t.Fatalf("agent-router should appear once, got %d:\n%s", strings.Count(out, "agent-router:"), out)
	}
}

func TestMergeOMPBytesReplaceUpdatesBlock(t *testing.T) {
	orig := `providers:
  router:
    baseUrl: http://localhost:20128/v1
  agent-router:
    baseUrl: http://127.0.0.1:9400/v1
    models:
      - id: old
        name: old
  zed:
    foo: bar
`
	node, ok := parseOMP([]byte(orig))
	if !ok {
		t.Fatal("parseOMP failed")
	}
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "new", Name: "new"}})
	out := mergeOMPBytes(node, []byte(orig), g)

	// single agent-router block, updated model
	if strings.Count(out, "agent-router:") != 1 {
		t.Fatalf("agent-router should appear once, got %d:\n%s", strings.Count(out, "agent-router:"), out)
	}
	if !strings.Contains(out, "id: new") || strings.Contains(out, "id: old") {
		t.Fatalf("gateway block not updated:\n%s", out)
	}
	// siblings before and after preserved
	if !strings.Contains(out, "http://localhost:20128/v1") || !strings.Contains(out, "zed:") {
		t.Fatalf("sibling providers lost:\n%s", out)
	}
}

func TestMergeOMPBytesEmptyProviders(t *testing.T) {
	orig := "providers:\n"
	node, ok := parseOMP([]byte(orig))
	if !ok {
		t.Fatal("parseOMP failed")
	}
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "m1", Name: "m1"}})
	out := mergeOMPBytes(node, []byte(orig), g)
	if !strings.Contains(out, "\n  agent-router:") {
		t.Fatalf("gateway block not inserted under empty providers:\n%q", out)
	}
}
