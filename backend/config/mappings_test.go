package config

import (
	"path/filepath"
	"testing"

	"agent-router/backend/storage"
)

func TestDeleteByProviderRemovesOnlyTargetMappings(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []ModelMapping{
		{ID: "target", ClientModel: "target-model", ProviderID: "target-provider", UpstreamModel: "upstream", Enabled: true},
		{ID: "other", ClientModel: "other-model", ProviderID: "other-provider", UpstreamModel: "upstream", Enabled: true},
	} {
		if _, err := store.Save(mapping); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteByProvider("target-provider"); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range store.List() {
		if mapping.ProviderID == "target-provider" {
			t.Fatalf("target mapping remained: %#v", mapping)
		}
	}
	if _, ok := store.Resolve("other-model"); !ok {
		t.Fatal("unrelated mapping was removed")
	}
}
