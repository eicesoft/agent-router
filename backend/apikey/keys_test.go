package apikey

import (
	"path/filepath"
	"testing"

	"agent-router/backend/storage"
)

func TestSaveAuthenticateToggleDelete(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(Key{ID: "k1", Name: "Test", Key: "ar-secret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if saved.CreatedAt == "" || saved.UpdatedAt == "" {
		t.Fatalf("timestamps were not set: %#v", saved)
	}
	if !store.Authenticate("ar-secret") {
		t.Fatal("valid key was rejected")
	}
	if id, name, ok := store.Lookup("ar-secret"); !ok || id != "k1" || name != "Test" {
		t.Fatalf("unexpected key lookup: id=%q name=%q ok=%t", id, name, ok)
	}
	if store.Authenticate("wrong") {
		t.Fatal("invalid key was accepted")
	}
	if err := store.SetEnabled("k1", false); err != nil {
		t.Fatal(err)
	}
	if store.Authenticate("ar-secret") {
		t.Fatal("disabled key was accepted")
	}
	if err := store.SetEnabled("k1", true); err != nil {
		t.Fatal(err)
	}
	if !store.Authenticate("ar-secret") {
		t.Fatal("re-enabled key was rejected")
	}
	if err := store.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if store.Authenticate("ar-secret") {
		t.Fatal("deleted key was accepted")
	}
	if len(store.List()) != 0 {
		t.Fatalf("expected no keys, got %#v", store.List())
	}
}

func TestSaveGeneratesKeyWhenEmpty(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(Key{ID: "k1", Name: "Auto", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Key == "" {
		t.Fatal("expected a generated key")
	}
	if !store.Authenticate(saved.Key) {
		t.Fatal("generated key was rejected")
	}
}

func TestSaveRejectsDuplicateKeyValue(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(Key{ID: "k1", Name: "First", Key: "ar-same"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(Key{ID: "k2", Name: "Second", Key: "ar-same"}); err == nil {
		t.Fatal("expected duplicate key value to be rejected")
	}
}
