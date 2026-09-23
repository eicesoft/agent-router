// Package skills discovers and manages agent skill directories on the user's
// machine. The filesystem is the single source of truth — no SQLite mirror —
// so every operation re-reads the directories. A skill is a folder containing
// SKILL.md with YAML frontmatter (name, description). Disabling a skill moves
// its directory out of the scanned roots into ~/.agents/skills-disabled so no
// CLI can load it; enabling moves it back.
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source labels where a skills root comes from. "user" roots are directly
// editable; plugin roots are managed by CLI tools and read-only by convention.
type Source string

const (
	SourceUser     Source = "user"
	SourcePlugin   Source = "plugin"
	SourceDisabled Source = "disabled"
)

// Root is one scanned skills directory.
type Root struct {
	Path   string `json:"path"`
	Source Source `json:"source"`
}

// Skill is one discovered skill directory.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Dir         string    `json:"dir"`
	Root        string    `json:"root"`
	Source      Source    `json:"source"`
	Enabled     bool      `json:"enabled"`
	Files       []string  `json:"files"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// MissingFrontmatter marks directories whose SKILL.md lacks the
	// name/description header — they still load in some CLIs but should be
	// flagged in the UI.
	MissingFrontmatter bool `json:"missingFrontmatter"`
	// TokenEstimate approximates what this skill costs in context per session:
	// the SKILL.md frontmatter between the "---" fences (name, description and
	// any other metadata), which is what CLIs load to advertise the skill.
	// Progressive disclosure is about keeping that number small, so the UI
	// surfaces it.
	TokenEstimate int `json:"tokenEstimate"`
}

// Detail is one skill's full content, loaded on demand so listings stay
// light.
type Detail struct {
	Skill
	Body string `json:"body"`
}

const skillFile = "SKILL.md"

// DisabledRoot is the holding directory disabled skills are moved into, one
// level beside the user skills root so no CLI scans it.
func DisabledRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".agents", "skills-disabled"), nil
}

// DefaultRoots returns the skills directories worth showing: the user's own
// ~/.agents/skills, the disabled holding area ~/.agents/skills-disabled, and
// plugin caches discovered under ~/.zcode/cli/plugins/cache (newest version
// per plugin only).
func DefaultRoots() []Root {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var roots []Root
	if user := filepath.Join(home, ".agents", "skills"); dirExists(user) {
		roots = append(roots, Root{Path: user, Source: SourceUser})
	}
	if disabled, err := DisabledRoot(); err == nil && dirExists(disabled) {
		roots = append(roots, Root{Path: disabled, Source: SourceDisabled})
	}
	roots = append(roots, pluginRoots(home)...)
	return roots
}

// pluginRoots walks the zcode plugin cache and returns one skills root per
// plugin, preferring the highest version directory.
func pluginRoots(home string) []Root {
	cache := filepath.Join(home, ".zcode", "cli", "plugins", "cache")
	entries, err := os.ReadDir(cache)
	if err != nil {
		return nil
	}
	var roots []Root
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillsDir := newestVersionSkills(filepath.Join(cache, entry.Name()))
		if skillsDir != "" {
			roots = append(roots, Root{Path: skillsDir, Source: SourcePlugin})
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Path < roots[j].Path })
	return roots
}

// newestVersionSkills picks the skills dir inside the highest semver-ish
// version folder of a plugin, e.g. browser-use/0.4.2/skills.
func newestVersionSkills(pluginDir string) string {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return ""
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() && strings.TrimLeft(entry.Name(), "0123456789.") != entry.Name() {
			versions = append(versions, entry.Name())
		}
	}
	if len(versions) == 0 {
		return ""
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersion(versions[i], versions[j]) > 0
	})
	skillsDir := filepath.Join(pluginDir, versions[0], "skills")
	if !dirExists(skillsDir) {
		return ""
	}
	return skillsDir
}

// compareVersion compares dotted numeric version strings; returns >0 if a
// sorts after b.
func compareVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var ai, bi int
		fmt.Sscanf(as[i], "%d", &ai)
		fmt.Sscanf(bs[i], "%d", &bi)
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return len(as) - len(bs)
}

// List scans every root and returns all skills: enabled first, then
// disabled, each group sorted by name. Enabled skills whose name collides
// with another enabled skill are flagged via the returned conflicts set
// (name -> true).
func List(roots []Root) ([]Skill, map[string]bool) {
	var out []Skill
	seen := map[string]int{}
	conflicts := map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			// dirExists follows symlinks, so a skill linked in from elsewhere
			// (e.g. ego-browser -> ~/.local/share/ego/ego-skills) lists like a
			// real directory. A dangling link fails the Stat and is skipped.
			dir := filepath.Join(root.Path, entry.Name())
			if strings.HasPrefix(entry.Name(), ".") || !dirExists(dir) {
				continue
			}
			skill, ok := loadSkill(dir, root)
			if !ok {
				continue
			}
			if skill.Enabled {
				if _, dup := seen[skill.Name]; dup {
					conflicts[skill.Name] = true
				}
				seen[skill.Name]++
			}
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Source < out[j].Source
	})
	return out, conflicts
}

// loadSkill reads one skill directory. ok=false when there is no SKILL.md
// (disabled dirs need one to know their name).
func loadSkill(dir string, root Root) (Skill, bool) {
	meta, err := readSkillMD(dir)
	if err != nil {
		return Skill{}, false
	}
	info, err := os.Stat(meta.path)
	var updated time.Time
	if err == nil {
		updated = info.ModTime()
	}
	name := meta.name
	// Skills in the disabled holding root are never loadable by CLIs.
	enabled := root.Source != SourceDisabled
	files := listFiles(dir)
	// Skills without a frontmatter name fall back to the directory name so
	// the UI always has something to show.
	if name == "" {
		name = filepath.Base(dir)
	}
	skill := Skill{
		Name:               name,
		Description:        meta.description,
		Dir:                dir,
		Root:               root.Path,
		Source:             root.Source,
		Enabled:            enabled,
		Files:              files,
		UpdatedAt:          updated,
		MissingFrontmatter: meta.name == "",
		TokenEstimate:      estimateTokens(meta.frontmatter),
	}
	return skill, true
}

// skillMD is the parsed SKILL.md header plus its path.
type skillMD struct {
	path        string
	name        string
	description string
	// frontmatter is the text between the two "---" fences — the part a CLI
	// has to load before it can decide whether the skill is worth opening.
	frontmatter string
}

// readSkillMD locates SKILL.md (case-insensitive fallback) and parses its
// frontmatter. A missing file is an error; empty frontmatter is not.
func readSkillMD(dir string) (skillMD, error) {
	path := filepath.Join(dir, skillFile)
	if !fileExists(path) {
		// Some skills ship skill.md lower-case.
		lower := filepath.Join(dir, strings.ToLower(skillFile))
		if !fileExists(lower) {
			return skillMD{}, fmt.Errorf("no %s in %s", skillFile, dir)
		}
		path = lower
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return skillMD{}, err
	}
	// CRLF loses the "\n---" fence match and yields a silent empty
	// frontmatter, so normalize line endings before parsing. It also keeps
	// the estimate from counting stray carriage returns as content.
	source := normalizeNewlines(string(data))
	out := skillMD{path: path, frontmatter: frontmatterOf(source)}
	out.name, out.description = parseFrontmatter(source)
	return out, nil
}

// normalizeNewlines converts CRLF and lone CR line endings to LF.
func normalizeNewlines(source string) string {
	if !strings.ContainsRune(source, '\r') {
		return source
	}
	return strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
}

// frontmatterOf returns the text between the opening and closing "---" fences.
// It is "" when the file has no frontmatter block.
func frontmatterOf(source string) string {
	rest, ok := strings.CutPrefix(source, "---\n")
	if !ok {
		return ""
	}
	block, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return ""
	}
	return block
}

// estimateTokens approximates the token count of a skill's metadata (the
// frontmatter block between the "---" fences). That is what a CLI loads to
// advertise the skill, so it is the recurring per-session cost; the body only
// enters the context once the skill actually runs. The characters-per-token
// ratio is a heuristic that suits mixed English/Chinese prose better than
// len/4 alone.
func estimateTokens(content string) int {
	if content == "" {
		return 0
	}
	chars := 0.0
	for _, r := range content {
		if r >= 0x4e00 && r <= 0x9fff {
			chars += 1.7 // CJK glyphs cost roughly an extra token each
			continue
		}
		chars++
	}
	tokens := int(chars/4 + 0.5)
	if tokens < 1 {
		return 1
	}
	return tokens
}

// parseFrontmatter extracts name and description from the leading "---"
// YAML block. Multi-line values — block scalars ("|", ">") and plain scalars
// folded onto indented continuation lines — are joined with single spaces, so
// the UI always gets the description as one flat line. Missing fields come
// back empty.
func parseFrontmatter(source string) (name, description string) {
	block := frontmatterOf(source)
	if block == "" {
		return "", ""
	}
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		// Indented lines are nested structure or continuation content; they
		// are consumed together with the key line above them.
		if isIndented(lines[i]) {
			continue
		}
		key, value, found := strings.Cut(lines[i], ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "name" && key != "description" {
			continue
		}
		var parts []string
		if !isBlockScalarHeader(value) {
			parts = append(parts, value)
		}
		for i+1 < len(lines) && isIndented(lines[i+1]) {
			i++
			parts = append(parts, strings.TrimSpace(lines[i]))
		}
		joined := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
		joined = strings.Trim(joined, `"'`)
		switch key {
		case "name":
			name = joined
		case "description":
			description = joined
		}
	}
	return name, description
}

// isIndented reports whether a frontmatter line starts with whitespace, i.e.
// it is nested under the previous top-level key.
func isIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// isBlockScalarHeader reports whether a value is empty or a YAML block scalar
// indicator ("|", ">", with optional "-" "+" / digit modifiers), meaning the
// real content lives in the indented lines below.
func isBlockScalarHeader(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value[0] == '|' || value[0] == '>'
}

// listFiles returns the relative paths of every file under dir (recursive,
// cap 100 to keep listings bounded).
//
// Never returns nil: the result crosses the Wails JSON boundary, where a nil
// slice becomes null and the UI's .files.length read throws.
func listFiles(dir string) []string {
	out := []string{}
	// A skill can sit in a scan root as a symlink (e.g.
	// ~/.agents/skills/ego-browser -> ~/.local/share/ego/ego-skills). WalkDir
	// does not follow a symlinked root: it stats the root with Lstat semantics,
	// sees a non-directory, and skips it via the path == root guard below —
	// leaving the list empty. Walk the resolved path instead.
	root := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		root = resolved
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == root || entry.IsDir() || len(out) >= 100 {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Get loads one skill's full body content.
func Get(dir string) (Detail, error) {
	var root Root
	for _, candidate := range DefaultRoots() {
		if strings.HasPrefix(dir, candidate.Path+string(os.PathSeparator)) {
			root = candidate
			break
		}
	}
	if root.Path == "" {
		return Detail{}, fmt.Errorf("skill directory %q is not under a known skills root", dir)
	}
	skill, ok := loadSkill(dir, root)
	if !ok {
		return Detail{}, fmt.Errorf("no SKILL.md in %s", dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, skillFile))
	if err != nil {
		if lower, lowerErr := os.ReadFile(filepath.Join(dir, strings.ToLower(skillFile))); lowerErr == nil {
			data = lower
		} else {
			return Detail{}, fmt.Errorf("read SKILL.md: %w", err)
		}
	}
	return Detail{Skill: skill, Body: string(data)}, nil
}

// SetEnabled moves a user skill between ~/.agents/skills (enabled) and
// ~/.agents/skills-disabled (disabled). Moving out of the scanned root is
// what makes the disable real for every CLI. Returns the skill at its new
// location. Plugin caches are managed by the CLI that owns them and are
// refused.
func SetEnabled(dir string, enabled bool) (Skill, error) {
	if !fileExists(filepath.Join(dir, skillFile)) && !fileExists(filepath.Join(dir, strings.ToLower(skillFile))) {
		return Skill{}, fmt.Errorf("skill %q not found", dir)
	}
	if !isUserRoot(dir) {
		return Skill{}, errors.New("插件目录下的 skill 由对应的 CLI 管理，不能在这里启停")
	}
	disabledRoot, err := DisabledRoot()
	if err != nil {
		return Skill{}, err
	}
	home, _ := os.UserHomeDir()
	userRoot := filepath.Join(home, ".agents", "skills")

	var source, target string
	if enabled {
		source = filepath.Join(disabledRoot, filepath.Base(dir))
		target = filepath.Join(userRoot, filepath.Base(dir))
	} else {
		source = dir
		target = filepath.Join(disabledRoot, filepath.Base(dir))
	}
	if !dirExists(source) {
		return Skill{}, fmt.Errorf("skill 目录不存在: %s", source)
	}
	if dirExists(target) {
		return Skill{}, fmt.Errorf("目标目录已存在: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Skill{}, fmt.Errorf("prepare target root: %w", err)
	}
	if err := os.Rename(source, target); err != nil {
		return Skill{}, fmt.Errorf("move skill directory: %w", err)
	}
	var root Root
	if enabled {
		root = Root{Path: userRoot, Source: SourceUser}
	} else {
		root = Root{Path: disabledRoot, Source: SourceDisabled}
	}
	skill, ok := loadSkill(target, root)
	if !ok {
		return Skill{}, fmt.Errorf("reload moved skill: %s", target)
	}
	return skill, nil
}

// isUserRoot reports whether dir lives in a root this app manages: the user
// skills root or the disabled holding area. Plugin caches are excluded.
func isUserRoot(dir string) bool {
	for _, root := range DefaultRoots() {
		if (root.Source == SourceUser || root.Source == SourceDisabled) &&
			strings.HasPrefix(dir, root.Path+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// Delete removes a user skill directory after verifying it really is one.
func Delete(dir string) error {
	if !isUserRoot(dir) {
		return errors.New("只能删除用户目录下的 skill")
	}
	if !fileExists(filepath.Join(dir, skillFile)) && !fileExists(filepath.Join(dir, strings.ToLower(skillFile))) {
		return fmt.Errorf("skill %q not found", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	return nil
}

// SaveBody rewrites a user skill's SKILL.md body (frontmatter is preserved
// as-is so user-edited metadata and ordering survive).
func SaveBody(dir string, body string) error {
	if !isUserRoot(dir) {
		return errors.New("只能编辑用户目录下的 skill")
	}
	path, err := skillMDPath(dir)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read SKILL.md: %w", err)
	}
	source := string(data)
	// Keep everything up to and including the closing "---" of the
	// frontmatter, then swap in the new body.
	if rest, ok := strings.CutPrefix(source, "---\n"); ok {
		if front, _, found := strings.Cut(rest, "\n---"); found {
			frontmatter := "---\n" + front + "\n---\n"
			if !strings.HasSuffix(body, "\n") && body != "" {
				body += "\n"
			}
			return os.WriteFile(path, []byte(frontmatter+"\n"+body), 0o644)
		}
	}
	if !strings.HasSuffix(body, "\n") && body != "" {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func skillMDPath(dir string) (string, error) {
	path := filepath.Join(dir, skillFile)
	if fileExists(path) {
		return path, nil
	}
	lower := filepath.Join(dir, strings.ToLower(skillFile))
	if fileExists(lower) {
		return lower, nil
	}
	return "", fmt.Errorf("no SKILL.md in %s", dir)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
