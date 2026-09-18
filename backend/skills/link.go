package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LinkState describes how one entry in a CLI's skills directory relates to the
// skills this app manages.
type LinkState string

const (
	// LinkLinked: a symlink this app would create, pointing at a managed skill.
	LinkLinked LinkState = "linked"
	// LinkMissing: no entry yet — linking is available.
	LinkMissing LinkState = "missing"
	// LinkExternal: an entry this app did not create (a real directory, or a
	// link pointing elsewhere). Shown, never touched.
	LinkExternal LinkState = "external"
	// LinkBroken: a symlink whose source is gone — a skill that was deleted
	// after being linked. Removable.
	LinkBroken LinkState = "broken"
)

// SkillLink is one row of the skills-directory view: a managed skill and the
// entry (if any) that publishes it to a CLI.
type SkillLink struct {
	Name string `json:"name"`
	// Dir is the skill's source directory, or for a broken link the path it
	// points at (which no longer exists) — enough for the UI to remove it.
	Dir    string    `json:"dir"`
	Target string    `json:"target"`
	State  LinkState `json:"state"`
}

// Links merges the managed skills with whatever already sits in targetDir, so
// the UI can offer linking for the missing ones and display the rest as-is. A
// targetDir that does not exist yet is not an error — nothing is linked.
func Links(targetDir string, managed []Skill) ([]SkillLink, error) {
	pending := make(map[string]Skill, len(managed))
	for _, skill := range managed {
		pending[filepath.Base(skill.Dir)] = skill
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", targetDir, err)
	}
	var out []SkillLink
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// The entry name is the link name, so a same-named managed skill is
		// spoken for either way; it must not also be offered as linkable.
		skill, known := pending[name]
		delete(pending, name)
		target := filepath.Join(targetDir, name)
		dest, err := resolvedLink(target)
		switch {
		case err != nil:
			// A real file or directory the user put there themselves.
			out = append(out, SkillLink{Name: name, Target: target, State: LinkExternal})
		// dirExists follows the link, so a skill deleted after being linked
		// falls through to the broken case below instead of reading as linked.
		case known && dest == filepath.Clean(skill.Dir) && dirExists(target):
			out = append(out, SkillLink{Name: name, Dir: skill.Dir, Target: target, State: LinkLinked})
		case !dirExists(target):
			out = append(out, SkillLink{Name: name, Dir: dest, Target: target, State: LinkBroken})
		default:
			out = append(out, SkillLink{Name: name, Dir: dest, Target: target, State: LinkExternal})
		}
	}
	for name, skill := range pending {
		out = append(out, SkillLink{
			Name:   name,
			Dir:    skill.Dir,
			Target: filepath.Join(targetDir, name),
			State:  LinkMissing,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetLink creates or removes the symlink publishing one skill into a CLI's
// skills directory. Removing only unlinks the symlink — the source directory is
// never touched — and an entry this app did not create is never overwritten.
func SetLink(skillDir, targetDir string, linked bool) error {
	target := filepath.Join(targetDir, filepath.Base(skillDir))
	dest, err := resolvedLink(target)
	if err != nil {
		if !linked {
			return nil // nothing of ours to unlink
		}
		if _, statErr := os.Lstat(target); statErr == nil {
			return fmt.Errorf("%s 已被占用", target)
		}
		if !dirExists(skillDir) {
			return fmt.Errorf("skill 目录不存在: %s", skillDir)
		}
		if !Managed(skillDir) {
			return errors.New("只能链接 app 管理的 skill")
		}
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("创建 skills 目录: %w", err)
		}
		if err := os.Symlink(skillDir, target); err != nil {
			return fmt.Errorf("创建链接: %w", err)
		}
		return nil
	}
	if dest != filepath.Clean(skillDir) {
		return fmt.Errorf("%s 已被占用（指向 %s）", target, dest)
	}
	if linked {
		return nil
	}
	// Remove the link itself; RemoveAll would follow it into the source.
	return os.Remove(target)
}

// Managed reports whether dir sits under a skills root this app may publish —
// the user's own root or a plugin cache. The disabled holding area is excluded:
// a disabled skill is meant to be invisible to every CLI.
func Managed(dir string) bool {
	for _, root := range DefaultRoots() {
		if root.Source == SourceDisabled {
			continue
		}
		if strings.HasPrefix(dir, root.Path+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// resolvedLink returns the absolute path a symlink points at. A missing path, a
// real directory, and a real file all come back as errors.
func resolvedLink(target string) (string, error) {
	dest, err := os.Readlink(target)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(target), dest)
	}
	return filepath.Clean(dest), nil
}
