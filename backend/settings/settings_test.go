package settings

import (
	"path/filepath"
	"testing"

	"agent-router/backend/storage"
)

func TestDefaultsAndSave(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Address() != "127.0.0.1:9400" || got.Theme != "light" {
		t.Fatalf("defaults wrong: %+v", got)
	}
	if err := store.Save(Settings{
		Host:         "0.0.0.0",
		Port:         8123,
		Theme:        "dark",
		DefaultModel: "provider / model",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Address() != "0.0.0.0:8123" || got.Theme != "dark" ||
		got.DefaultModel != "provider / model" {
		t.Fatalf("save lost: %+v", got)
	}
}
