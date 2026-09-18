package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestSkill creates a skill directory with the given SKILL.md content.
func writeTestSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseFrontmatter(t *testing.T) {
	source := "---\nname: code-review\ndescription: \"Review stuff.\"\n---\n\nBody here."
	name, description := parseFrontmatter(source)
	if name != "code-review" || description != "Review stuff." {
		t.Fatalf("got %q / %q", name, description)
	}
	name, description = parseFrontmatter("no frontmatter at all")
	if name != "" || description != "" {
		t.Fatalf("expected empty, got %q / %q", name, description)
	}
}

func TestParseFrontmatterMultiline(t *testing.T) {
	// Literal block scalar ("|") folded onto one line with spaces.
	block := "---\nname: computer-use\ndescription: |\n  Drive the user's desktop\n  clicking, typing,\n  scrolling.\nversion: 2.0.0\n---\n"
	name, description := parseFrontmatter(block)
	if name != "computer-use" {
		t.Fatalf("name = %q", name)
	}
	want := "Drive the user's desktop clicking, typing, scrolling."
	if description != want {
		t.Fatalf("description = %q, want %q", description, want)
	}
	// Folded scalar with chomping indicator (">-").
	folded := "---\nname: orchestration\ndescription: >-\n  Use Orca for coordination:\n  threaded messages, blocking flows.\n---\n"
	_, description = parseFrontmatter(folded)
	want = "Use Orca for coordination: threaded messages, blocking flows."
	if description != want {
		t.Fatalf("folded description = %q, want %q", description, want)
	}
	// Plain scalar continued on indented lines.
	plain := "---\nname: x\ndescription: Helps users discover\n  and install agent skills.\n---\n"
	_, description = parseFrontmatter(plain)
	want = "Helps users discover and install agent skills."
	if description != want {
		t.Fatalf("plain description = %q, want %q", description, want)
	}
	// Nested maps under other keys must not leak into name/description.
	nested := "---\nname: y\ndescription: short one.\nmetadata:\n  hermes:\n    fake: injected\n---\n"
	name, description = parseFrontmatter(nested)
	if name != "y" || description != "short one." {
		t.Fatalf("nested leaked: name=%q description=%q", name, description)
	}
}

func TestListAndConflict(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeTestSkill(t, rootA, "alpha", "---\nname: alpha\ndescription: a\n---\n")
	writeTestSkill(t, rootB, "alpha", "---\nname: alpha\ndescription: dup\n---\n")
	writeTestSkill(t, rootA, "beta", "---\nname: beta\ndescription: b\n---\n")
	// A plain directory without SKILL.md is skipped.
	if err := os.MkdirAll(filepath.Join(rootA, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}

	roots := []Root{{Path: rootA, Source: SourceUser}, {Path: rootB, Source: SourcePlugin}}
	skills, conflicts := List(roots)
	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}
	if !conflicts["alpha"] || conflicts["beta"] {
		t.Fatalf("conflicts = %v", conflicts)
	}
}

func TestListDisabledRootAndMissingFrontmatter(t *testing.T) {
	root := t.TempDir()
	disabled := t.TempDir()
	writeTestSkill(t, disabled, "gamma", "---\nname: gamma\ndescription: g\n---\n")
	writeTestSkill(t, root, "delta", "just body, no frontmatter")

	skills, _ := List([]Root{{Path: root, Source: SourceUser}, {Path: disabled, Source: SourceDisabled}})
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	if byName["gamma"].Enabled {
		t.Fatal("gamma in disabled root should be disabled")
	}
	if !byName["delta"].MissingFrontmatter {
		t.Fatal("delta should be flagged missing frontmatter")
	}
	if byName["delta"].Name != "delta" {
		t.Fatalf("delta should fall back to dir name, got %q", byName["delta"].Name)
	}
}

// moveSkill simulates SetEnabled's core operation without touching the real
// home directory: it moves a dir between two roots and reloads it.
func TestMoveRoundTrip(t *testing.T) {
	root := t.TempDir()
	disabled := t.TempDir()
	dir := writeTestSkill(t, root, "toggle", "---\nname: toggle\ndescription: t\n---\n")

	moved := filepath.Join(disabled, "toggle")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	skill, ok := loadSkill(moved, Root{Path: disabled, Source: SourceDisabled})
	if !ok || skill.Enabled {
		t.Fatalf("skill after disable = %+v", skill)
	}
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	skill, ok = loadSkill(dir, Root{Path: root, Source: SourceUser})
	if !ok || !skill.Enabled {
		t.Fatalf("skill after enable = %+v", skill)
	}
}

func TestSetEnabledRefusesUnknownRoot(t *testing.T) {
	dir := writeTestSkill(t, t.TempDir(), "outside", "---\nname: outside\ndescription: o\n---\n")
	if _, err := SetEnabled(dir, false); err == nil {
		t.Fatal("expected refusal for directory outside known roots")
	}
}

func TestSaveBodyFrontmatterCut(t *testing.T) {
	original := "---\nname: keep\n# a user comment\ndescription: keep-desc\n---\n\nold body\n"
	rest, ok := strings.CutPrefix(original, "---\n")
	if !ok {
		t.Fatal("prefix cut failed")
	}
	front, _, found := strings.Cut(rest, "\n---")
	if !found {
		t.Fatal("closing cut failed")
	}
	frontmatter := "---\n" + front + "\n---\n"
	want := "---\nname: keep\n# a user comment\ndescription: keep-desc\n---\n"
	if frontmatter != want {
		t.Fatalf("frontmatter = %q", frontmatter)
	}
}

func TestLoadSkillParsesMeta(t *testing.T) {
	root := t.TempDir()
	dir := writeTestSkill(t, root, "detail", "---\nname: detail\ndescription: d\n---\n\nsome body")
	skill, ok := loadSkill(dir, Root{Path: root, Source: SourceUser})
	if !ok {
		t.Fatal("loadSkill failed")
	}
	if skill.Name != "detail" || skill.Description != "d" || !skill.Enabled {
		t.Fatalf("skill = %+v", skill)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	if got := estimateTokens("abcd"); got != 1 {
		t.Fatalf("4 ascii chars = %d, want 1", got)
	}
	// CJK glyphs count heavier than ASCII: 4 ideographs ≈ 2 tokens, where a
	// pure len/4 ratio would say 1.
	if got := estimateTokens("技能管理"); got != 2 {
		t.Fatalf("4 CJK chars = %d, want 2", got)
	}
	// The estimate must grow with content so the UI ordering is meaningful.
	small := estimateTokens("name: a\ndescription: b")
	large := estimateTokens(strings.Repeat("word ", 500))
	if large <= small {
		t.Fatalf("estimate did not grow: small=%d large=%d", small, large)
	}
}

func TestFrontmatterOf(t *testing.T) {
	source := "---\nname: find-skills\ndescription: Helps users discover skills.\ndisable-model-invocation: true\n---\n\nBody here."
	want := "name: find-skills\ndescription: Helps users discover skills.\ndisable-model-invocation: true"
	if got := frontmatterOf(source); got != want {
		t.Fatalf("frontmatterOf = %q, want %q", got, want)
	}
	if got := frontmatterOf("no frontmatter at all"); got != "" {
		t.Fatalf("unfenced source = %q, want empty", got)
	}
	// An unterminated block is not frontmatter.
	if got := frontmatterOf("---\nname: dangling\n"); got != "" {
		t.Fatalf("unterminated = %q, want empty", got)
	}
}

// Skills shipped on Windows carry CRLF fences; without normalization the
// frontmatter lookup silently returns empty and the skill looks metadata-free.
func TestReadSkillMDNormalizesCRLF(t *testing.T) {
	root := t.TempDir()
	crlf := strings.ReplaceAll(
		"---\nname: win\ndescription: shipped on windows\n---\n\nbody",
		"\n", "\r\n")
	dir := writeTestSkill(t, root, "win", crlf)
	meta, err := readSkillMD(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.name != "win" || meta.description != "shipped on windows" {
		t.Fatalf("meta = %+v", meta)
	}
	if want := "name: win\ndescription: shipped on windows"; meta.frontmatter != want {
		t.Fatalf("frontmatter = %q, want %q", meta.frontmatter, want)
	}
	skill, ok := loadSkill(dir, Root{Path: root, Source: SourceUser})
	if !ok {
		t.Fatal("loadSkill failed")
	}
	if skill.TokenEstimate <= 0 {
		t.Fatalf("TokenEstimate = %d for a CRLF file", skill.TokenEstimate)
	}
}

// The estimate must cover only the frontmatter block: the body is loaded when
// the skill runs, not when the CLI advertises it.
func TestLoadSkillTokenEstimateCountsFrontmatterOnly(t *testing.T) {
	root := t.TempDir()
	frontmatter := "name: est\ndescription: d"
	body := strings.Repeat("body ", 500)
	dir := writeTestSkill(t, root, "est", "---\n"+frontmatter+"\n---\n\n"+body)
	skill, ok := loadSkill(dir, Root{Path: root, Source: SourceUser})
	if !ok {
		t.Fatal("loadSkill failed")
	}
	if want := estimateTokens(frontmatter); skill.TokenEstimate != want {
		t.Fatalf("TokenEstimate = %d, want %d", skill.TokenEstimate, want)
	}
	// A large body must not leak into the estimate.
	if skill.TokenEstimate > 50 {
		t.Fatalf("TokenEstimate = %d looks like it counted the body", skill.TokenEstimate)
	}
}

// A skill linked into a scan root is a symlink. WalkDir does not follow a
// symlinked root, so Files came back nil, Marshalled to JSON null, and the UI
// threw "null is not an object (evaluating 'skill.files.length')".
func TestListFilesSymlinkedRoot(t *testing.T) {
	root := t.TempDir()
	target := writeTestSkill(t, t.TempDir(), "linked", "---\nname: linked\ndescription: l\n---\n")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "extra.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	skills, _ := List([]Root{{Path: root, Source: SourceUser}})
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	got := skills[0].Files
	if got == nil {
		t.Fatal("Files is nil; it serializes to JSON null and breaks the UI")
	}
	if len(got) != 2 {
		t.Fatalf("Files = %v, want SKILL.md and nested/extra.md", got)
	}
	// Relative to the skill, not to the symlink's resolved target.
	for _, name := range got {
		if filepath.IsAbs(name) || strings.Contains(name, "..") {
			t.Fatalf("Files entry %q is not a clean relative path", name)
		}
	}
}

// A skill directory with no files at all must still serialize as [] rather
// than null.
func TestListFilesNeverNil(t *testing.T) {
	dir := t.TempDir()
	if got := listFiles(filepath.Join(dir, "does-not-exist")); got == nil {
		t.Fatal("listFiles returned nil for a missing directory")
	}
	if got := listFiles(t.TempDir()); got == nil {
		t.Fatal("listFiles returned nil for an empty directory")
	}
}
