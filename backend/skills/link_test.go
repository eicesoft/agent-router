package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// sandboxHome points HOME at a temp dir so DefaultRoots (and therefore the
// Managed check) sees a throwaway ~/.agents/skills instead of the real one.
func sandboxHome(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	root = filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, root
}

// A managed skill links into a CLI's skills directory as a symlink and unlinks
// again without the source directory being touched.
func TestSetLinkRoundTrip(t *testing.T) {
	home, root := sandboxHome(t)
	dir := writeTestSkill(t, root, "alpha", "---\nname: alpha\ndescription: a\n---\n")
	target := filepath.Join(home, ".config", "opencode", "skills")
	link := filepath.Join(target, "alpha")

	if err := SetLink(dir, target, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	dest, err := os.Readlink(link)
	if err != nil || dest != dir {
		t.Fatalf("readlink = %q, %v; want %q", dest, err, dir)
	}
	// Linking twice is a no-op, not an error.
	if err := SetLink(dir, target, true); err != nil {
		t.Fatalf("re-link: %v", err)
	}

	if err := SetLink(dir, target, false); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link still present: %v", err)
	}
	// The whole point of unlinking with os.Remove: the source survives.
	if !fileExists(filepath.Join(dir, "SKILL.md")) {
		t.Fatal("unlink removed the skill source")
	}
	// Unlinking something that was never linked is a no-op.
	if err := SetLink(dir, target, false); err != nil {
		t.Fatalf("unlink missing: %v", err)
	}
}

func TestLinksStates(t *testing.T) {
	home, root := sandboxHome(t)
	linked := writeTestSkill(t, root, "linked", "---\nname: linked\ndescription: l\n---\n")
	free := writeTestSkill(t, root, "free", "---\nname: free\ndescription: f\n---\n")
	gone := writeTestSkill(t, root, "gone", "---\nname: gone\ndescription: g\n---\n")
	target := filepath.Join(home, ".config", "opencode", "skills")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetLink(linked, target, true); err != nil {
		t.Fatal(err)
	}
	// A dangling link: the skill was linked, then deleted.
	if err := os.Symlink(gone, filepath.Join(target, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	// An entry this app did not create.
	foreign := filepath.Join(target, "handmade")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}

	managed := []Skill{{Name: "linked", Dir: linked}, {Name: "free", Dir: free}, {Name: "gone", Dir: gone}}
	links, err := Links(target, managed)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]LinkState{}
	dirs := map[string]string{}
	for _, l := range links {
		got[l.Name] = l.State
		dirs[l.Name] = l.Dir
	}
	want := map[string]LinkState{
		"linked":   LinkLinked,
		"free":     LinkMissing,
		"gone":     LinkBroken,
		"handmade": LinkExternal,
	}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s = %q, want %q", name, got[name], state)
		}
	}
	// A broken row still carries the dead path, which is what the UI passes
	// back to unlink it.
	if dirs["gone"] != gone {
		t.Errorf("broken dir = %q, want %q", dirs["gone"], gone)
	}
}

// A missing target directory is a normal state, not an error: nothing is
// linked yet.
func TestLinksWithoutTargetDir(t *testing.T) {
	home, root := sandboxHome(t)
	dir := writeTestSkill(t, root, "solo", "---\nname: solo\ndescription: s\n---\n")
	links, err := Links(filepath.Join(home, ".config", "opencode", "skills"), []Skill{{Name: "solo", Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].State != LinkMissing {
		t.Fatalf("links = %+v", links)
	}
}

// A broken link can be removed by passing the path it points at.
func TestSetLinkRemovesBrokenLink(t *testing.T) {
	home, root := sandboxHome(t)
	dir := writeTestSkill(t, root, "doomed", "---\nname: doomed\ndescription: d\n---\n")
	target := filepath.Join(home, ".config", "opencode", "skills")
	if err := SetLink(dir, target, true); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(target, "doomed")
	if err := SetLink(dir, target, false); err != nil {
		t.Fatalf("remove broken link: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("broken link survived: %v", err)
	}
}

// Entries this app did not create are never overwritten or removed.
func TestSetLinkRefusesForeignEntry(t *testing.T) {
	home, root := sandboxHome(t)
	dir := writeTestSkill(t, root, "clash", "---\nname: clash\ndescription: c\n---\n")
	target := filepath.Join(home, ".config", "opencode", "skills")
	if err := os.MkdirAll(filepath.Join(target, "clash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetLink(dir, target, true); err == nil {
		t.Fatal("expected refusal to overwrite a real directory")
	}
	if err := SetLink(dir, target, false); err != nil {
		t.Fatalf("unlink of a foreign entry must not fail loudly, nor delete it: %v", err)
	}
	if !dirExists(filepath.Join(target, "clash")) {
		t.Fatal("foreign directory was deleted")
	}
	// A link pointing somewhere else is equally off limits.
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(target, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	if err := SetLink(filepath.Join(root, "elsewhere"), target, false); err == nil {
		t.Fatal("expected refusal to remove a link this app did not create")
	}
}

func TestSetLinkRefusesUnmanagedSkill(t *testing.T) {
	home, _ := sandboxHome(t)
	outside := writeTestSkill(t, t.TempDir(), "outside", "---\nname: outside\ndescription: o\n---\n")
	if err := SetLink(outside, filepath.Join(home, ".config", "opencode", "skills"), true); err == nil {
		t.Fatal("expected refusal for a skill outside the managed roots")
	}
}

// store.List must follow symlinked skill directories, otherwise skills linked
// in from elsewhere are invisible in the UI.
func TestListFollowsSymlinkedSkill(t *testing.T) {
	root := t.TempDir()
	real := writeTestSkill(t, t.TempDir(), "linked-in", "---\nname: linked-in\ndescription: li\n---\n")
	if err := os.Symlink(real, filepath.Join(root, "linked-in")); err != nil {
		t.Fatal(err)
	}
	// A dangling link is skipped rather than reported as a broken skill.
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}

	items, _ := List([]Root{{Path: root, Source: SourceUser}})
	if len(items) != 1 || items[0].Name != "linked-in" {
		t.Fatalf("items = %+v", items)
	}
}
