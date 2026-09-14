package templates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeSlots() []ModelSlot {
	return []ModelSlot{
		{Key: "FABLE", Label: "Fable"},
		{Key: "HAIKU", Label: "Haiku"},
		{Key: "OPUS", Label: "Opus"},
		{Key: "SONNET", Label: "Sonnet"},
	}
}

func TestEnvForUsesValidShellIdentifier(t *testing.T) {
	if got, want := envFor("agent-router"), "AGENT_ROUTER_API_KEY"; got != want {
		t.Fatalf("envFor(agent-router) = %q, want %q", got, want)
	}
}

func TestMergeClaudeSlots(t *testing.T) {
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{
		{ID: "bl/ZHIPU/GLM-5.3", Name: "GLM-5.3"},
		{ID: "bl/deepseek-v4-pro-0813", Name: "Deepseek-V4-Pro"},
		{ID: "bl/deepseek-v4-flash-0731", Name: "Deepseek-V4-Flash"},
		{ID: "bl/ZHIPU/GLM-5.3-Flash", Name: "GLM-5.3-Flash"},
	}).WithSlotModels(map[string]string{
		"FABLE":  "bl/ZHIPU/GLM-5.3",
		"HAIKU":  "bl/deepseek-v4-pro-0813",
		"OPUS":   "bl/deepseek-v4-flash-0731",
		"SONNET": "",
	})

	document := newDoc()
	env := document.object("env")
	// A stale override from an earlier configuration must be removed when the
	// user clears the slot this time.
	env.set("ANTHROPIC_DEFAULT_SONNET_MODEL", "stale")
	env.set("ANTHROPIC_DEFAULT_SONNET_MODEL_NAME", "stale-name")

	mergeClaude(document, g, Tool{ID: ToolClaude, Shape: "claude-env", ModelSlots: claudeSlots()})

	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	for _, want := range []string{
		`"ANTHROPIC_DEFAULT_FABLE_MODEL": "bl/ZHIPU/GLM-5.3"`,
		`"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME": "GLM-5.3"`,
		`"ANTHROPIC_DEFAULT_HAIKU_MODEL": "bl/deepseek-v4-pro-0813"`,
		`"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "Deepseek-V4-Pro"`,
		`"ANTHROPIC_DEFAULT_OPUS_MODEL": "bl/deepseek-v4-flash-0731"`,
		`"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "Deepseek-V4-Flash"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "SONNET") {
		t.Errorf("cleared SONNET slot leaked the stale override:\n%s", s)
	}
}

// TestModelsFilter guards the multi-provider model checklist: only the enabled
// subset lands in the generated flat-list config.
func TestModelsFilter(t *testing.T) {
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{
		{ID: "m1", Name: "m1"},
		{ID: "m2", Name: "m2"},
		{ID: "m3", Name: "m3"},
	}).WithModels([]string{"m1", "m3"})

	document := newDoc()
	mergeAIIDSK(document, g)
	out, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"m1"`, `"m3"`} {
		if !strings.Contains(s, want) {
			t.Errorf("enabled model %s missing:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"m2"`) {
		t.Errorf("disabled model m2 leaked:\n%s", s)
	}
}

// TestWriteBacksUp guards the pre-write backup rule: writing over an existing
// config leaves a timestamped copy beside it before the merge lands.
func TestWriteBacksUp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "m1", Name: "m1"}})
	tool := Tool{
		ID:         ToolClaude,
		Name:       "Claude Code",
		CLI:        "claude",
		configRel:  ".claude/settings.json",
		Shape:      "claude-env",
		ModelSlots: []ModelSlot{{Key: "OPUS", Label: "Opus"}},
	}

	target := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"env":{"ANTHROPIC_AUTH_TOKEN":"old"}}`
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := g.Write(tool)
	if err != nil {
		t.Fatal(err)
	}
	if path != target {
		t.Errorf("wrote to %q, want %q", path, target)
	}

	backups, err := filepath.Glob(target + ".*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("want exactly one backup, got %v (err %v)", backups, err)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Errorf("backup changed the pre-write bytes:\n%s", data)
	}

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "ANTHROPIC_BASE_URL") {
		t.Errorf("merged config missing gateway env:\n%s", written)
	}
}
