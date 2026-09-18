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

// 别名与客户端模型名共用同一个查找表：两者都必须能命中，而停用后都不能命中。
func TestResolveHonorsAliases(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(ModelMapping{
		ID: "mapped", ClientModel: "client-model", ProviderID: "p", UpstreamModel: "up",
		// 前后空白要裁掉，与自己同名的别名要丢掉：它已经单独能命中。
		Aliases: []string{" client-alias ", "client-model", "second-alias"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Aliases) != 2 || saved.Aliases[0] != "client-alias" || saved.Aliases[1] != "second-alias" {
		t.Fatalf("unexpected aliases: %#v", saved.Aliases)
	}
	for _, name := range []string{"client-model", "client-alias", "second-alias"} {
		mapping, ok := store.Resolve(name)
		if !ok || mapping.ID != "mapped" {
			t.Fatalf("%q did not resolve to its mapping: %#v ok=%v", name, mapping, ok)
		}
	}
	if _, err := store.Save(ModelMapping{ID: "mapped", ClientModel: "client-model", ProviderID: "p", UpstreamModel: "up", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client-model", "client-alias"} {
		if _, ok := store.Resolve(name); ok {
			t.Fatalf("disabled mapping still resolved %q", name)
		}
	}
}

// 两个映射应答同一个名字时 Resolve 会按 map 遍历顺序任选其一，等于悄悄偷走
// 另一个映射的流量，因此在保存时就拒绝。
func TestSaveRejectsModelNameCollisions(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ModelMapping{ID: "first", ClientModel: "model-a", ProviderID: "p", UpstreamModel: "up-a", Aliases: []string{"shortcut"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ModelMapping{ID: "second", ClientModel: "model-b", ProviderID: "p", UpstreamModel: "up-b", Aliases: []string{"shortcut"}, Enabled: true}); err == nil {
		t.Fatal("duplicate alias across mappings was accepted")
	}
	if _, err := store.Save(ModelMapping{ID: "second", ClientModel: "model-b", ProviderID: "p", UpstreamModel: "up-b", Aliases: []string{"model-a"}, Enabled: true}); err == nil {
		t.Fatal("alias colliding with another mapping's client model was accepted")
	}
	if _, err := store.Save(ModelMapping{ID: "second", ClientModel: "model-a", ProviderID: "p", UpstreamModel: "up-b", Enabled: true}); err == nil {
		t.Fatal("duplicate client model was accepted")
	}
	// 重新保存自己不算冲突。
	if _, err := store.Save(ModelMapping{ID: "first", ClientModel: "model-a", ProviderID: "p", UpstreamModel: "up-a", Aliases: []string{"shortcut", "extra"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	saved, ok := store.Resolve("extra")
	if !ok || saved.ID != "first" {
		t.Fatalf("re-saved mapping lost its alias: %#v ok=%v", saved, ok)
	}
}

// 别名要能跨重启留存：它落在 model_mappings.aliases_json 上。
func TestAliasesSurviveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ModelMapping{ID: "m", ClientModel: "client-model", ProviderID: "p", UpstreamModel: "up", Aliases: []string{"alias-one", "alias-two"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reloaded, err := NewMappingStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	mapping, ok := reloaded.Resolve("alias-two")
	if !ok || mapping.ClientModel != "client-model" || len(mapping.Aliases) != 2 {
		t.Fatalf("aliases did not survive reload: %#v ok=%v", mapping, ok)
	}
}
