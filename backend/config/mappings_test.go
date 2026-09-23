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

// 同一个客户端模型名可以挂多家提供商（按顺序 failover），但同一条「名字 → 提供商/上游
// 模型」路由不能重复：重复只会让 failover 把同一个请求发两遍、付两次钱。
func TestSameNameAcrossProvidersIsAllowed(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	// 空库会播种一条默认映射（default-gpt：openai/gpt-4.1），它正好与本用例的第一条
	// 构成重复路由，先清掉。
	if err := store.DeleteByProvider("openai"); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []ModelMapping{
		{ID: "first", ClientModel: "gpt-4.1", ProviderID: "openai", UpstreamModel: "gpt-4.1", Aliases: []string{"shortcut"}, Enabled: true},
		{ID: "second", ClientModel: "gpt-4.1", ProviderID: "azure", UpstreamModel: "gpt-4.1-deployment", Enabled: true},
	} {
		if _, err := store.Save(mapping); err != nil {
			t.Fatalf("same name on a different provider was rejected: %v", err)
		}
	}
	// 别名也算命中：它同样构成一条链。
	if _, err := store.Save(ModelMapping{ID: "third", ClientModel: "other", ProviderID: "google", UpstreamModel: "gpt-4.1", Aliases: []string{"shortcut"}, Enabled: true}); err != nil {
		t.Fatalf("alias shared with another provider's route was rejected: %v", err)
	}

	// 完全相同的路由（同提供商 + 同上游模型 + 同名字）仍然拒绝。
	if _, err := store.Save(ModelMapping{ID: "dupe", ClientModel: "gpt-4.1", ProviderID: "openai", UpstreamModel: "gpt-4.1", Enabled: true}); err == nil {
		t.Fatal("duplicate route on the same provider was accepted")
	}
	// 重新保存自己不算重复。
	if _, err := store.Save(ModelMapping{ID: "first", ClientModel: "gpt-4.1", ProviderID: "openai", UpstreamModel: "gpt-4.1", Aliases: []string{"shortcut", "extra"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// 链顺序必须可复现：ResolveAll 的结果就是 failover 顺序。
	for i := 0; i < 50; i++ {
		chain := store.ResolveAll("gpt-4.1")
		if len(chain) != 2 || chain[0].ID != "first" || chain[1].ID != "second" {
			t.Fatalf("unstable failover order: %#v", chain)
		}
	}
	if chain := store.ResolveAll("shortcut"); len(chain) != 2 || chain[0].ID != "first" || chain[1].ID != "third" {
		t.Fatalf("alias chain wrong: %#v", chain)
	}
	// Resolve 是链首，旧调用方（UI）继续拿到主路由。
	if head, ok := store.Resolve("gpt-4.1"); !ok || head.ID != "first" {
		t.Fatalf("Resolve did not return the chain head: %#v ok=%v", head, ok)
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

// 输入类型一个都没勾视为未配置：模型默认都有 text 能力，Save 落回 ["text"]。
func TestSaveDefaultsInputTypesToText(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(ModelMapping{ID: "m", ClientModel: "client-model", ProviderID: "p", UpstreamModel: "up", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.InputTypes) != 1 || saved.InputTypes[0] != "text" {
		t.Fatalf("empty input types should default to text: %#v", saved.InputTypes)
	}
}

// 输入类型与上下文/输出大小/单价要能跨重启留存，且 Save 前对输入类型做规整。
func TestModelMetaSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(ModelMapping{
		ID: "m", ClientModel: "client-model", ProviderID: "p", UpstreamModel: "up", Enabled: true,
		InputTypes:       []string{" text ", "image", "text"},
		InputContextSize: 128000,
		OutputSize:       32000,
		InputPrice:       2.5,
		OutputPrice:      10,
		CacheReadPrice:   0.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.InputTypes; len(got) != 2 || got[0] != "text" || got[1] != "image" {
		t.Fatalf("input types not normalized: %#v", got)
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
	mapping, ok := reloaded.Resolve("client-model")
	if !ok || mapping.InputContextSize != 128000 || mapping.OutputSize != 32000 || len(mapping.InputTypes) != 2 {
		t.Fatalf("model meta did not survive reload: %#v ok=%v", mapping, ok)
	}
	if mapping.InputPrice != 2.5 || mapping.OutputPrice != 10 || mapping.CacheReadPrice != 0.25 {
		t.Fatalf("prices did not survive reload: %#v", mapping)
	}
}

// 三个价格未填写时保持默认 0（= 未设置），不能被写成负数或残留旧值。
func TestSaveDefaultsPricesToZero(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(ModelMapping{ID: "m", ClientModel: "client-model", ProviderID: "p", UpstreamModel: "up", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if saved.InputPrice != 0 || saved.OutputPrice != 0 || saved.CacheReadPrice != 0 {
		t.Fatalf("prices should default to 0: %#v", saved)
	}
	// 旧字段不带价格时，Update 不应把已有价格弄丢（Save 是整行覆盖，重新写入 0）。
	saved.InputPrice = 1.5
	if _, err := store.Save(saved); err != nil {
		t.Fatal(err)
	}
	saved.InputPrice = 0
	if _, err := store.Save(saved); err != nil {
		t.Fatal(err)
	}
	reloaded, ok := store.Resolve("client-model")
	if !ok || reloaded.InputPrice != 0 || reloaded.OutputPrice != 0 || reloaded.CacheReadPrice != 0 {
		t.Fatalf("explicit zero prices not stored: %#v ok=%v", reloaded, ok)
	}
}
