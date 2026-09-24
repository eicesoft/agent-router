package plugin

import (
	"path/filepath"
	"strings"
	"testing"

	"agent-router/backend/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSessionStripDefaultsOff(t *testing.T) {
	store := newTestStore(t)
	if store.Enabled(IDSessionStrip) {
		t.Fatal("session strip should default to off")
	}
	list := store.List()
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	p := list[0]
	if p.ID != IDSessionStrip || p.Kind != KindInput || p.HasConfig || p.Enabled {
		t.Fatalf("unexpected meta: %+v", p)
	}
	caveman := list[1]
	if caveman.ID != IDCaveman || caveman.Kind != KindInput || !caveman.HasConfig || caveman.Enabled {
		t.Fatalf("unexpected caveman meta: %+v", caveman)
	}
	if caveman.Config["level"] != DefaultCavemanLevel {
		t.Fatalf("default level = %q, want %q", caveman.Config["level"], DefaultCavemanLevel)
	}
}

func TestCavemanLevelConfigRoundTrip(t *testing.T) {
	store := newTestStore(t)
	if err := store.SetConfig(IDCaveman, map[string]string{"level": "wenyan-lite"}); err != nil {
		t.Fatal(err)
	}
	if got := store.CavemanLevel(); got != CavemanLevelWenyanLite {
		t.Fatalf("level = %q", got)
	}
	if err := store.SetConfig(IDCaveman, map[string]string{"level": "nope"}); err != nil {
		t.Fatal(err)
	}
	if got := store.CavemanLevel(); got != DefaultCavemanLevel {
		t.Fatalf("invalid level should fall back, got %q", got)
	}
	if err := store.SetConfig("nope", map[string]string{}); err == nil {
		t.Fatal("unknown plugin config should error")
	}
}

func TestCavemanRulesPinLevel(t *testing.T) {
	rules := CavemanRules(CavemanLevelUltra)
	if !strings.Contains(rules, "Active caveman level for this request: **ultra**") {
		t.Fatalf("level pin missing")
	}
	if strings.HasPrefix(rules, "---") {
		t.Fatalf("frontmatter should be stripped")
	}
	if !strings.Contains(rules, "Respond terse like smart caveman") {
		t.Fatalf("skill body missing")
	}
}

func TestSetEnabledRoundTrip(t *testing.T) {
	store := newTestStore(t)
	if err := store.SetEnabled(IDSessionStrip, true); err != nil {
		t.Fatal(err)
	}
	if !store.Enabled(IDSessionStrip) {
		t.Fatal("expected enabled after set")
	}
	if err := store.SetEnabled("nope", true); err == nil {
		t.Fatal("unknown plugin should error")
	}
}

func TestRecordAccumulatesAndRates(t *testing.T) {
	store := newTestStore(t)
	store.Record(IDSessionStrip, 100, 60)
	store.Record(IDSessionStrip, 50, 50)
	list := store.List()
	p := list[0]
	if p.InputTokens != 150 || p.OutputTokens != 110 {
		t.Fatalf("totals = %d/%d, want 150/110", p.InputTokens, p.OutputTokens)
	}
	// (150-110)/150 = 40/150
	want := 40.0 / 150.0
	if diff := p.CompressionRate - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("rate = %v, want %v", p.CompressionRate, want)
	}
}
