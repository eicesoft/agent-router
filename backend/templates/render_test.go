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

// TestSlotBaselineSurvivesReopen guards the persistence contract: a slot the
// user saved to settings.json must render back from disk on the next preview,
// not from the catalog-order fallback. Without it, every write looks lost the
// moment the panel is reopened, because the fallback is re-derived from the
// current (sorted) routable list.
func TestSlotBaselineSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	target := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"chosen/opus","ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":"chosen/opus"}}`
	if err := os.WriteFile(target, []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := Tool{
		ID:         ToolClaude,
		Name:       "Claude Code",
		CLI:        "claude",
		configRel:  ".claude/settings.json",
		Shape:      "claude-env",
		ModelSlots: claudeSlots(),
	}
	// The fallback for index 2 (OPUS) is "aaa/fallback" — deliberately not the
	// saved value, so a regression cannot pass by coincidence.
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{
		{ID: "aaa/fallback", Name: "aaa/fallback"},
		{ID: "bbb/fallback", Name: "bbb/fallback"},
		{ID: "ccc/fallback", Name: "ccc/fallback"},
		{ID: "ddd/fallback", Name: "ddd/fallback"},
	})

	resolved := Resolve(tool)
	if got := resolved.SlotBaseline["OPUS"]; got != "chosen/opus" {
		t.Fatalf("SlotBaseline[OPUS] = %q, want the on-disk value", got)
	}
	if got := g.SlotModels(resolved)["OPUS"]; got != "chosen/opus" {
		t.Errorf("SlotModels()[OPUS] = %q, want the saved value to win over the fallback", got)
	}

	// A slot with no on-disk entry still falls back to catalog order.
	if got := g.SlotModels(resolved)["HAIKU"]; got != "bbb/fallback" {
		t.Errorf("SlotModels()[HAIKU] = %q, want the catalog-order fallback", got)
	}

	// An explicit selection still outranks both layers.
	explicit := g.WithSlotModels(map[string]string{"OPUS": "explicit/pick"})
	if got := explicit.SlotModels(resolved)["OPUS"]; got != "explicit/pick" {
		t.Errorf("SlotModels()[OPUS] = %q, want the explicit selection to win", got)
	}
}

// TestSlotBaselineAbsentFile checks the empty cases: no file, unparsable file,
// and a non-env shape all leave the baseline nil so rendering is unaffected.
func TestSlotBaselineAbsentFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	tool := Tool{
		ID:         ToolClaude,
		CLI:        "claude",
		configRel:  ".claude/settings.json",
		Shape:      "claude-env",
		ModelSlots: claudeSlots(),
	}
	if got := Resolve(tool).SlotBaseline; got != nil {
		t.Errorf("SlotBaseline with no config = %v, want nil", got)
	}

	target := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(tool).SlotBaseline; got != nil {
		t.Errorf("SlotBaseline with unparsable config = %v, want nil", got)
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

// TestPICompatSessionAffinity guards the pi-only compat block: the gateway's
// provider entry opts into session affinity headers, and a re-render replaces
// the user's existing compat object instead of duplicating it.
func TestPICompatSessionAffinity(t *testing.T) {
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{
		{ID: "m1", Name: "m1"},
	})

	// Fresh document: compat lands first, matching the hand-written layout.
	fresh := newDoc()
	mergePI(fresh, g)
	out, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"compat":{"sendSessionAffinityHeaders":true}`) {
		t.Errorf("compat block missing from fresh pi config:\n%s", out)
	}

	// Merge over a user document that already carries a compat object: the
	// gateway entry is replaced wholesale, not merged or duplicated.
	document, ok := parseDoc([]byte(`{"providers":{"agent-router":{"compat":{"stale":1}}},"defaultProvider":"other"}`))
	if !ok {
		t.Fatal("fixture did not parse")
	}
	mergePI(document, g)
	out, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "stale") {
		t.Errorf("stale compat keys survived re-render:\n%s", s)
	}
	if !strings.Contains(s, `"sendSessionAffinityHeaders":true`) {
		t.Errorf("compat block missing after merge:\n%s", s)
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

// TestParseDocJSONC verifies .jsonc configs (kilo, mimocode) parse despite
// comments and trailing commas — including a comma followed by a comment
// before the closing brace, which is only trailing once comments are gone.
// A parse failure here would silently drop the user's whole config.
func TestParseDocJSONC(t *testing.T) {
	data := []byte(`{
		// provider list
		"provider": {
			"mine": {
				"apiKey": "sk-\"quoted\"", // inline note
				"limit": {"context": 1, /* block */ "output": 2,}
			},
		},
	}`)
	document, ok := parseDoc(data)
	if !ok {
		t.Fatal("jsonc document did not parse")
	}
	provider, _ := document.vals["provider"].(*doc)
	if provider == nil {
		t.Fatal("provider missing")
	}
	mine, _ := provider.vals["mine"].(*doc)
	if mine == nil {
		t.Fatal("mine missing")
	}
	if mine.vals["apiKey"] != `sk-"quoted"` {
		t.Errorf("apiKey = %v, want escaped string preserved", mine.vals["apiKey"])
	}
	limit, _ := mine.vals["limit"].(*doc)
	if limit == nil || limit.vals["output"] != json.Number("2") {
		t.Errorf("limit = %v, want both keys after block-comment trailing comma", mine.vals["limit"])
	}
}
