package envcfg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
	return dir
}

func TestParseAssignment(t *testing.T) {
	for _, tc := range []struct {
		line, want string
		ok         bool
	}{
		{`export AGENT_ROUTER_API_KEY='ar-secret'`, "ar-secret", true},
		{`export AGENT_ROUTER_API_KEY="ar-secret"`, "ar-secret", true},
		{`export AGENT_ROUTER_API_KEY=ar-secret`, "ar-secret", true},
		{`  export AGENT_ROUTER_API_KEY=ar-secret  `, "ar-secret", true},
		{`AGENT_ROUTER_API_KEY=ar-secret`, "ar-secret", true},
		{`$env:AGENT_ROUTER_API_KEY = "ar-secret"`, "ar-secret", true},
		{`export OTHER_KEY=ar-secret`, "", false},
		{`# export AGENT_ROUTER_API_KEY=x`, "", false},
		{``, "", false},
	} {
		got, ok := parseAssignment(tc.line, "sh")
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseAssignment(%q) = %q,%v want %q,%v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestEnsureAssignmentReplacesAndIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rc files are not used on Windows")
	}
	dir := tempHome(t)
	path := filepath.Join(dir, ".zshrc")
	const key = "ar-secret-123"

	if err := ensureAssignment(rcFile{path: path, style: "sh"}, key); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if !strings.Contains(string(first), "export AGENT_ROUTER_API_KEY='ar-secret-123'") {
		t.Fatalf("first write missing assignment:\n%s", first)
	}

	// An older, different value must be replaced (single occurrence left).
	if err := ensureAssignment(rcFile{path: path, style: "sh"}, "ar-older"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if got := strings.Count(string(second), "AGENT_ROUTER_API_KEY="); got != 1 {
		t.Fatalf("expected exactly one assignment, got %d:\n%s", got, second)
	}
	if strings.Contains(string(second), "ar-secret-123") {
		t.Fatalf("stale value survived replace:\n%s", second)
	}
	if !strings.Contains(string(second), "ar-older") {
		t.Fatalf("new value missing after replace:\n%s", second)
	}

	if value, ok := scanRC(rcFile{path: path, style: "sh"}); !ok || value != "ar-older" {
		t.Fatalf("scanRC after double-write = %q,%v", value, ok)
	}
}

func TestFindKeyProcessEnvWins(t *testing.T) {
	t.Setenv(EnvVar, "from-process")
	if value, ok := FindKey(); !ok || value != "from-process" {
		t.Fatalf("FindKey with process env = %q,%v", value, ok)
	}
}

func TestFindKeyFromRC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rc files are not used on Windows")
	}
	dir := tempHome(t)
	path := filepath.Join(dir, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export AGENT_ROUTER_API_KEY='rc-value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, "")
	os.Unsetenv(EnvVar)
	if value, ok := FindKey(); !ok || value != "rc-value" {
		t.Fatalf("FindKey via rc = %q,%v", value, ok)
	}
}

func TestMaskValue(t *testing.T) {
	if got := maskValue("ar-0123456789abcdef"); got != "ar-0••••cdef" {
		t.Errorf("maskValue(long) = %q", got)
	}
	if got := maskValue("short"); got != "••••••••" {
		t.Errorf("maskValue(short) = %q", got)
	}
	if got := maskValue(""); got != "" {
		t.Errorf("maskValue(empty) = %q", got)
	}
}

func TestWriteKeyIdempotentAcrossRCs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rc files are not used on Windows")
	}
	dir := tempHome(t)
	// Pre-seed a stale value in .zshrc and .profile.
	zshrc := filepath.Join(dir, ".zshrc")
	profile := filepath.Join(dir, ".profile")
	if err := os.MkdirAll(filepath.Dir(zshrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zshrc, []byte("export AGENT_ROUTER_API_KEY='stale'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte("export OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv(EnvVar)

	status, err := WriteKey("ar-new")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Written || !status.MatchesKey {
		t.Fatalf("unexpected status: %+v", status)
	}
	data, _ := os.ReadFile(zshrc)
	if !strings.Contains(string(data), "ar-new") || strings.Contains(string(data), "stale") {
		t.Fatalf(".zshrc after write:\n%s", data)
	}
	profileData, _ := os.ReadFile(profile)
	if !strings.Contains(string(profileData), "ar-new") || !strings.Contains(string(profileData), "OTHER=1") {
		t.Fatalf(".profile lost unrelated lines:\n%s", profileData)
	}

	// Idempotent: a second run still matches and does not duplicate.
	status2, err := WriteKey("ar-new")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(zshrc)
	if got := strings.Count(string(data), "AGENT_ROUTER_API_KEY="); got != 1 {
		t.Fatalf("duplicate assignment after second write (%d):\n%s", got, data)
	}
	_ = status2
	_ = status
}
