package provider

import (
	"path/filepath"
	"reflect"
	"testing"

	"agent-router/backend/storage"
)

func TestRegistryPersistsAvailableAndEnabledModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	input := Provider{ID: "catalog-test", Name: "Catalog Test", Kind: KindCompatible, BaseURL: "https://example.com/v1", DevID: "openai", Models: []string{"gpt-4.1"}, AvailableModels: []AvailableModel{{ID: "gpt-4o-mini", Created: 200}, {ID: "gpt-4.1", Created: 100}}}
	if _, err := registry.Save(input); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reloaded, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	actual, ok := reloaded.Get(input.ID)
	if !ok {
		t.Fatal("saved provider was not loaded")
	}
	if !reflect.DeepEqual(actual.Models, input.Models) {
		t.Fatalf("enabled models = %#v, want %#v", actual.Models, input.Models)
	}
	if !reflect.DeepEqual(actual.AvailableModels, input.AvailableModels) {
		t.Fatalf("available models = %#v, want %#v", actual.AvailableModels, input.AvailableModels)
	}
	if actual.DevID != "openai" {
		t.Fatalf("devId = %q, want openai", actual.DevID)
	}
	// 旧 UI 不带 devId 时不能清掉已关联的 models.dev 条目。
	if _, err := reloaded.Save(Provider{ID: input.ID, Name: input.Name, Kind: input.Kind, BaseURL: input.BaseURL, Models: input.Models}); err != nil {
		t.Fatal(err)
	}
	if kept, _ := reloaded.Get(input.ID); kept.DevID != "openai" {
		t.Fatalf("devId cleared by payload without field: %q", kept.DevID)
	}
}

func TestRegistryDeletePersistsCatalogTombstone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("openai"); !ok {
		t.Fatal("built-in OpenAI provider was not seeded")
	}
	if _, err := registry.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("openai"); ok {
		t.Fatal("deleted provider is still listed")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reloaded, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get("openai"); ok {
		t.Fatal("deleted catalog provider was restored after restart")
	}
}

func TestRegistryUsesCatalogDefaultsForKnownNames(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, url string
		kind      Kind
	}{
		{" Codex ", "https://api.openai.com/v1", KindOpenAI},
		{"ANTHROPIC", "https://api.anthropic.com", KindAnthropic},
		{"Claude", "https://api.anthropic.com", KindAnthropic},
		{"Google Gemini", "https://generativelanguage.googleapis.com", KindGemini},
	} {
		saved, err := registry.Save(Provider{ID: "test-" + test.name, Name: test.name, BaseURL: "  "})
		if err != nil {
			t.Fatal(err)
		}
		if saved.BaseURL != test.url || saved.Kind != test.kind {
			t.Fatalf("%s: got %s %s", test.name, saved.BaseURL, saved.Kind)
		}
	}
	saved, err := registry.Save(Provider{ID: "override", Name: "Codex", BaseURL: " https://custom.example/v1 ", Kind: KindCompatible, Icon: "https://custom.example/icon.png"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.BaseURL != "https://custom.example/v1" || saved.Kind != KindCompatible || saved.Icon != "https://custom.example/icon.png" {
		t.Fatal("explicit settings overwritten")
	}
	if _, err := registry.Save(Provider{ID: "unknown", Name: "Unknown"}); err == nil {
		t.Fatal("unknown provider accepted without URL")
	}
}

func TestRegistryDefaultsFollowSelectedInterface(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		kind Kind
		url  string
	}{
		{"我的 Anthropic", KindAnthropic, "https://api.anthropic.com"},
		{"工作账号", KindOpenAI, "https://api.openai.com/v1"},
		{"备用", KindGemini, "https://generativelanguage.googleapis.com"},
		{"Codex", KindAnthropic, "https://api.anthropic.com"},
	} {
		p, err := registry.Save(Provider{ID: "selected-" + test.name, Name: test.name, Kind: test.kind})
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if p.BaseURL != test.url {
			t.Errorf("%s: URL=%s, want %s", test.name, p.BaseURL, test.url)
		}
	}
}
