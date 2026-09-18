package templates

import (
	"os"
	"path/filepath"
	"testing"
)

// The catalog's skillsRel is what publishes a CLI's skills directory to the UI;
// a typo there silently hides the whole skills feature for that tool.
func TestResolveSkillsDir(t *testing.T) {
	var opencode Tool
	for _, tool := range Tools() {
		if tool.ID == ToolOpenCode {
			opencode = tool
		}
	}
	if opencode.ID == "" {
		t.Fatal("opencode missing from catalog")
	}
	if opencode.SkillsDir != "" {
		t.Fatal("SkillsDir must stay empty until Resolve fills it")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	want := filepath.Join(home, ".config", "opencode", "skills")
	if got := Resolve(opencode).SkillsDir; got != want {
		t.Fatalf("SkillsDir = %q, want %q", got, want)
	}
	// Tools without a skillsRel get none, so the UI hides their entry point.
	for _, tool := range Tools() {
		if tool.ID == ToolOpenCode {
			continue
		}
		if dir := Resolve(tool).SkillsDir; dir != "" {
			t.Errorf("%s: unexpected SkillsDir %q", tool.ID, dir)
		}
	}
}
