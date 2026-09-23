package main

import (
	"os"
	"path/filepath"
	"testing"

	"agent-router/backend/config"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
)

func newPriceTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	// DevModelPrices 读 UserConfigDir；与 provider 包测试同一套环境隔离。
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	db, err := storage.OpenPath(filepath.Join(dir, "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return &App{providers: reg, mappings: mappings}
}

func writeDevCatalog(t *testing.T, body string) {
	t.Helper()
	path, err := provider.DevCatalogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSaveProviderSyncsMappingPricesFromDevCatalog(t *testing.T) {
	app := newPriceTestApp(t)
	writeDevCatalog(t, `{
	  "openai": {
	    "id": "openai",
	    "name": "OpenAI",
	    "models": {
	      "gpt-4.1": {"cost": {"input": 2, "output": 8, "cache_read": 0.5}},
	      "gpt-4o-mini": {"cost": {"input": 0.15, "output": 0.6}}
	    }
	  }
	}`)

	// 已有映射：保存时用 api.json 覆盖价格，保留别名等其它字段。
	existing := config.ModelMapping{
		ID:            "keep-aliases",
		ClientModel:   "custom-name",
		ProviderID:    "p1",
		UpstreamModel: "gpt-4.1",
		Aliases:       []string{"alias-1"},
		Enabled:       true,
		InputTypes:    []string{"text", "image"},
		InputPrice:    99,
	}
	if _, err := app.mappings.Save(existing); err != nil {
		t.Fatal(err)
	}

	saved, err := app.SaveProvider(provider.Provider{
		ID:      "p1",
		Name:    "My OpenAI",
		Kind:    provider.KindCompatible,
		BaseURL: "https://example.com/v1",
		DevID:   "openai",
		Models:  []string{"gpt-4.1", "gpt-4o-mini", "unknown-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.DevID != "openai" {
		t.Fatalf("devId = %q", saved.DevID)
	}

	byRoute := map[string]config.ModelMapping{}
	for _, m := range app.mappings.List() {
		byRoute[m.ProviderID+"\x00"+m.UpstreamModel] = m
	}
	gpt, ok := byRoute["p1\x00gpt-4.1"]
	if !ok {
		t.Fatal("existing mapping route missing")
	}
	if gpt.InputPrice != 2 || gpt.OutputPrice != 8 || gpt.CacheReadPrice != 0.5 {
		t.Fatalf("prices not overwritten: %#v", gpt)
	}
	if gpt.ID != "keep-aliases" || gpt.ClientModel != "custom-name" || len(gpt.Aliases) != 1 || len(gpt.InputTypes) != 2 {
		t.Fatalf("non-price fields mutated: %#v", gpt)
	}

	mini, ok := byRoute["p1\x00gpt-4o-mini"]
	if !ok {
		t.Fatal("auto route was not persisted")
	}
	if mini.InputPrice != 0.15 || mini.OutputPrice != 0.6 {
		t.Fatalf("auto mapping prices: %#v", mini)
	}
	if mini.ClientModel != "My OpenAI / gpt-4o-mini" || !mini.Enabled {
		t.Fatalf("auto mapping defaults: %#v", mini)
	}
	if _, ok := byRoute["p1\x00unknown-model"]; ok {
		t.Fatal("model absent from api.json should not be persisted")
	}
}

func TestSaveProviderSkipsPriceSyncWithoutDevID(t *testing.T) {
	app := newPriceTestApp(t)
	writeDevCatalog(t, `{"openai":{"id":"openai","models":{"m":{"cost":{"input":1,"output":2}}}}}`)
	if _, err := app.SaveProvider(provider.Provider{
		ID: "p2", Name: "No Link", Kind: provider.KindCompatible,
		BaseURL: "https://example.com/v1", Models: []string{"m"},
	}); err != nil {
		t.Fatal(err)
	}
	// NewMappingStore 会预置 default-gpt，只断言本提供商没有被同步落库。
	for _, m := range app.mappings.List() {
		if m.ProviderID == "p2" {
			t.Fatalf("unexpected mapping without devId: %#v", m)
		}
	}
}
