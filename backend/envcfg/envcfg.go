// Package envcfg writes the local gateway key into the user's environment as
// AGENT_ROUTER_API_KEY, so clients that only support an env-var credential
// (opencode, mimocode, omp) can reach the gateway without pasting the key into
// their config.
//
// How the value is persisted depends on the platform:
//
//   - macOS / Linux: the login and interactive shell files are updated with a
//     per-shell assignment — POSIX shells get `export AGENT_ROUTER_API_KEY=...`,
//     fish gets `set -gx AGENT_ROUTER_API_KEY ...`. Files are only touched when
//     they already exist or belong to the user's current shell, so unrelated rc
//     files are never created or polluted. Existing assignments to the same
//     variable are replaced in place, keeping repeated exports idempotent.
//     New terminals pick the variable up; the current process needs a restart.
//   - Windows: the persistent per-user environment (HKCU\Environment, via the
//     registry and setx) is updated, so new processes launched after the write
//     inherit the variable. Existing shells need a restart to see it.
//
// The package never reads the key from the process environment into the UI
// (secrets stay in the OS keychain / SQLite); it only *sets* the env var so
// downstream CLIs can authenticate against http://127.0.0.1:9400.
package envcfg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvVar is the canonical environment variable for the local gateway key.
const EnvVar = "AGENT_ROUTER_API_KEY"

// Status describes one key's state on disk / in the environment.
type Status struct {
	VarName     string `json:"varName"`
	Value       string `json:"value"`       // existing value, masked
	Set         bool   `json:"set"`         // an entry already exists
	MatchesKey  bool   `json:"matches"`     // the existing entry equals the key
	Written     bool   `json:"written"`     // this call persisted the value
	OS          string `json:"os"`          // runtime.GOOS
	ProfilePath string `json:"profilePath"` // file(s) updated, for the UI
	Error       string `json:"error"`
}

// rcFile is one shell configuration file and the syntax used inside it.
type rcFile struct {
	path  string
	style string // "sh" | "fish"
}

// candidateRCs lists every shell file the user could source on macOS/Linux,
// in priority order. Detection scans all of them so an existing assignment is
// found regardless of which shell the user actually runs.
func candidateRCs() []rcFile {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("USERPROFILE")
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	out := []rcFile{
		{filepath.Join(home, ".zshrc"), "sh"},
		{filepath.Join(home, ".bashrc"), "sh"},
		{filepath.Join(home, ".zprofile"), "sh"},
		{filepath.Join(home, ".profile"), "sh"},
	}
	if p, ok := fishConfigPath(); ok {
		out = append(out, rcFile{p, "fish"})
	}
	return out
}

func fishConfigPath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config", "fish", "config.fish"), true
}

// writeTargets selects which shell files to update: candidates that already
// exist (the user actively uses them) or files belonging to the user's current
// shell. It falls back to .zshrc + .profile when nothing matched, so a value is
// always persisted even when the app cannot determine the login shell.
func writeTargets() []rcFile {
	shell := filepath.Base(os.Getenv("SHELL"))
	if shell == "-zsh" {
		shell = "zsh"
	}
	matches := func(f rcFile) bool {
		base := filepath.Base(f.path)
		switch shell {
		case "zsh":
			return base == ".zshrc" || base == ".zprofile"
		case "fish":
			return base == "config.fish"
		default: // bash, sh, or unknown -> POSIX login files
			return base == ".bashrc" || base == ".profile"
		}
	}
	targets := make([]rcFile, 0, 3)
	for _, f := range candidateRCs() {
		if _, err := os.Stat(f.path); err == nil || matches(f) {
			targets = append(targets, f)
		}
	}
	if len(targets) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("USERPROFILE")
		}
		targets = []rcFile{
			{filepath.Join(home, ".zshrc"), "sh"},
			{filepath.Join(home, ".profile"), "sh"},
		}
	}
	return targets
}

// FindKey locates an AGENT_ROUTER_API_KEY value already present for the user:
// first the current process environment, then the shell shell files, then (on
// Windows) the persistent user environment. ok reports a match.
func FindKey() (value string, ok bool) {
	if value, ok := os.LookupEnv(EnvVar); ok && value != "" {
		return value, true
	}
	for _, f := range candidateRCs() {
		if value, ok := scanRC(f); ok {
			return value, true
		}
	}
	if runtime.GOOS == "windows" {
		if value, ok := windowsUserEnv(); ok {
			return value, true
		}
	}
	return "", false
}

// scanRC reads one shell file and returns the value of an existing assignment
// to EnvVar, if present.
func scanRC(f rcFile) (string, bool) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := parseAssignment(line, f.style); ok {
			return value, true
		}
	}
	return "", false
}

// parseAssignment recognizes an assignment to EnvVar in the shell dialect used
// by the file. It returns the unquoted value.
func parseAssignment(line, style string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	switch style {
	case "fish":
		// `set -gx AGENT_ROUTER_API_KEY 'value'` (flags optional)
		if !strings.HasPrefix(trimmed, "set ") || !strings.Contains(trimmed, EnvVar) {
			return "", false
		}
		if i := strings.Index(trimmed, EnvVar); i >= 0 {
			return unquote(strings.TrimSpace(trimmed[i+len(EnvVar):])), true
		}
	case "sh":
		for _, prefix := range []string{"export " + EnvVar + "=", EnvVar + "="} {
			if strings.HasPrefix(trimmed, prefix) {
				return unquote(strings.TrimPrefix(trimmed, prefix)), true
			}
		}
	}
	for _, prefix := range []string{"$env:" + EnvVar} {
		if strings.HasPrefix(trimmed, prefix) {
			if i := strings.IndexRune(trimmed, '='); i >= 0 {
				return unquote(strings.TrimSpace(trimmed[i+1:])), true
			}
		}
	}
	return "", false
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// StatusFor reports the environment's current state for EnvVar relative to key
// (the local API key the UI wants to export).
func StatusFor(key string) Status {
	existing, found := FindKey()
	return Status{
		VarName:    EnvVar,
		Value:      maskValue(existing),
		Set:        found,
		MatchesKey: found && strings.EqualFold(existing, key),
		OS:         runtime.GOOS,
	}
}

// WriteKey persists key as AGENT_ROUTER_API_KEY for the current user,
// replacing any existing assignment. It returns a Status describing the result,
// with ProfilePath naming the files updated (macOS/Linux).
func WriteKey(key string) (Status, error) {
	if strings.TrimSpace(key) == "" {
		return Status{VarName: EnvVar}, errors.New("api key is required")
	}
	switch runtime.GOOS {
	case "windows":
		if err := windowsSetUserEnv(key); err != nil {
			return Status{VarName: EnvVar}, err
		}
		status := StatusFor(key)
		status.Written = true
		status.MatchesKey = true
		return status, nil
	case "darwin", "linux":
		written := make([]string, 0, 3)
		for _, f := range writeTargets() {
			if err := ensureAssignment(f, key); err == nil {
				written = append(written, f.path)
			}
		}
		if len(written) == 0 {
			return Status{VarName: EnvVar}, errors.New("no writable shell profile found")
		}
		status := StatusFor(key)
		status.Written = true
		status.ProfilePath = strings.Join(written, ", ")
		return status, nil
	default:
		return Status{VarName: EnvVar}, fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
}

// ensureAssignment ensures path contains an assignment of EnvVar to key —
// appending it when absent, replacing any prior assignment in place. The file
// is created (mode 0600, since it now holds a credential) when missing; an
// existing file keeps its permission bits.
func ensureAssignment(f rcFile, key string) error {
	data, err := os.ReadFile(f.path)
	var existed bool
	switch {
	case err == nil:
		existed = true
	case os.IsNotExist(err):
		data = nil
	default:
		return err
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	kept := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		if _, ok := parseAssignment(line, f.style); ok {
			continue // replace below
		}
		kept = append(kept, line)
	}
	// Drop a trailing blank so the appended assignment lands on its own line.
	if len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	kept = append(kept, assignmentLine(f.style, key))
	out := strings.Join(kept, "\n") + "\n"

	if existed {
		if info, err := os.Stat(f.path); err == nil {
			return os.WriteFile(f.path, []byte(out), info.Mode().Perm())
		}
	}
	if dirErr := os.MkdirAll(filepath.Dir(f.path), 0o755); dirErr != nil {
		return dirErr
	}
	return os.WriteFile(f.path, []byte(out), 0o600)
}

// assignmentLine renders the shell-specific assignment for one value.
func assignmentLine(style, value string) string {
	switch style {
	case "fish":
		return fmt.Sprintf("set -gx %s '%s'", EnvVar, value)
	default:
		return fmt.Sprintf("export %s='%s'", EnvVar, value)
	}
}

func maskValue(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "••••••••"
	}
	return value[:4] + "••••" + value[len(value)-4:]
}
