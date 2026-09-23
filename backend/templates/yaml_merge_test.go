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
	// apiKey references the env var holding the gateway key
	if !strings.Contains(out, "apiKey: AGENT_ROUTER_API_KEY") {
		t.Errorf("apiKey should be the AGENT_ROUTER_API_KEY env var:\n%s", out)
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

// TestOMPModelMetadata guards the mapping metadata flowing into omp's config:
// window sizes land as contextWindow / maxTokens, capabilities are narrowed to
// omp's accepted input literals, and unset sizes omit the keys.
func TestOMPModelMetadata(t *testing.T) {
	// Isolate HOME so Render mints a fresh document instead of merging the
	// real machine's models.yml, which would put unrelated providers — and
	// their input literals — under the assertions.
	t.Setenv("HOME", t.TempDir())
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{
		{ID: "m1", Name: "m1", InputContextSize: 200000, OutputSize: 64000,
			InputTypes: []string{"text", "image", "audio"}},
		{ID: "m2", Name: "m2"},
	})
	tool := Tool{ID: ToolOMP, Name: "OMP", CLI: "omp", configRel: ".omp/agent/models.yml", MultiProvider: true, Shape: "omp"}
	out := g.Render(tool)

	for _, want := range []string{"contextWindow: 200000", "maxTokens: 64000", "input: [text, image]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in omp config:\n%s", want, out)
		}
	}
	// omp only accepts text/image literals.
	if strings.Contains(out, "audio") {
		t.Errorf("unsupported input literal leaked into omp config:\n%s", out)
	}
	// Unset sizes omit the keys instead of writing zeros.
	if strings.Contains(out, "contextWindow: 0") || strings.Contains(out, "maxTokens: 0") {
		t.Errorf("unset size must stay out of the config:\n%s", out)
	}
}
