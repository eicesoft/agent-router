package templates

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRealOMPModelsYML exercises the merge against the actual models.yml on
// this machine, asserting the real file parses, comments survive, the gateway
// provider lands in the providers list, and the result still reparses as YAML.
func TestRealOMPModelsYML(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	data, err := os.ReadFile(home + "/.omp/agent/models.yml")
	if err != nil {
		t.Skip("no real models.yml")
	}
	node, ok := parseOMP(data)
	if !ok {
		t.Fatalf("parseOMP failed on real file (%d bytes):\n%s", len(data), strings.TrimSpace(string(data))[:200])
	}
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "bl/zhipu/GLM-5.3", Name: "GLM-5.3"}})
	mergeOMP(node, g)
	out := marshalOMP(node)

	// gateway provider present in providers list, and its entry is well-formed
	if !strings.Contains(out, "agent-router:\n") {
		t.Errorf("gateway provider missing from merged output:\n%.600s", out)
	}
	if !strings.Contains(out, "bl/zhipu/GLM-5.3") {
		t.Errorf("gateway model missing from merged output:\n%.600s", out)
	}
	// comments must survive a merge+reprint
	if strings.HasPrefix(string(data), "providers:") && !strings.Contains(out, "#") {
		t.Errorf("comments were lost in merged output:\n%.600s", out)
	}
	// merged output must stay valid YAML
	var roundtrip yaml.Node
	if err := yaml.Unmarshal([]byte(out), &roundtrip); err != nil {
		t.Fatalf("merged yaml does not reparse: %v\n%.300s", err, out)
	}
	if len(out) < 400 {
		t.Errorf("output too small (comment loss?):\n%q", out)
	}
}