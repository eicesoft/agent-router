package templates

import (
	"strings"
	"testing"
)

func TestMergeOMPPreservesOrder(t *testing.T) {
	orig := `providers:
  router:
    baseUrl: http://localhost:20128/v1
    apiKey: sk-old
    api: openai-completions
    auth: apiKey
    models:
      - id: bl/qwen3.8-flash
        name: qwen3.8-flash
`
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "m1", Name: "m1"}})
	node, ok := parseOMP([]byte(orig))
	if !ok {
		t.Fatal("parseOMP failed")
	}
	mergeOMP(node, g)
	out := marshalOMP(node)

	// existing provider kept, gateway merged
	if !strings.Contains(out, "localhost:20128") || !strings.Contains(out, "127.0.0.1:9400") {
		t.Errorf("merge lost a provider:\n%s", out)
	}
	// gateway provider fields present and ordered within its own block
	blockStart := strings.Index(out, "agent-router:")
	if blockStart < 0 {
		t.Fatalf("missing agent-router:\n%s", out)
	}
	block := out[blockStart:]
	idx := []string{"baseUrl:", "apiKey:", "api:", "auth:", "models:"}
	pos := -1
	for _, s := range idx {
		i := strings.Index(block, s)
		if i < 0 {
			t.Fatalf("missing %q in gateway block:\n%s", s, block)
		}
		if i < pos {
			t.Errorf("key %q out of order", s)
		}
		pos = i
	}
	// quoted empty apiKey stays a string
	if !strings.Contains(out, "apiKey: \"\"") {
		t.Errorf("apiKey should be empty string:\n%s", out)
	}
}

func TestRenderOMPFresh(t *testing.T) {
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "m1", Name: "m1-name"}})
	tool := Tool{ID: ToolOMP, Name: "OMP", CLI: "omp", configRel: ".omp/agent/models.yml", MultiProvider: true, Shape: "omp"}
	out := g.Render(tool)
	if !strings.Contains(out, "providers:") || !strings.Contains(out, "agent-router:") || !strings.Contains(out, "id: m1") {
		t.Errorf("fresh omp render wrong:\n%s", out)
	}
}