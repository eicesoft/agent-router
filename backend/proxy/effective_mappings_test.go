package proxy

import (
	"path/filepath"
	"testing"

	"agent-router/backend/config"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

// 回归（Bug：Agent 配置里还能选中已取消勾选的上游模型）：上游模型在提供商里
// 被取消勾选后，指向它的已保存映射行必须从 EffectiveMappings 消失——它是
// /v1/models、Agent 配置清单与路由的共同来源。
func TestEffectiveMappingsHonorsProviderModelSelection(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	// 提供商两个模型都勾选着。
	if _, err := registry.Save(provider.Provider{ID: "p", Name: "P", BaseURL: "https://example.com", Enabled: true, Models: []string{"keep", "drop"}}); err != nil {
		t.Fatal(err)
	}
	// "drop" 上有一条已保存映射行（编辑过 / 指派过 Codex alias 都会物化出这种行）。
	if _, err := mappings.Save(config.ModelMapping{ID: "m-drop", ClientModel: "drop-client", ProviderID: "p", UpstreamModel: "drop", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 用户在提供商抽屉里取消勾选 "drop"。
	if _, err := registry.Save(provider.Provider{ID: "p", Name: "P", BaseURL: "https://example.com", Enabled: true, Models: []string{"keep"}}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{})
	for _, m := range s.EffectiveMappings() {
		t.Logf("effective: client=%q upstream=%q", m.ClientModel, m.UpstreamModel)
		if m.UpstreamModel == "drop" {
			t.Errorf("已取消勾选的上游模型 %q 仍出现在 EffectiveMappings", m.UpstreamModel)
		}
	}

	// 纯自动映射（无保存行）同样要跟着勾选走。
	if _, err := registry.Save(provider.Provider{ID: "q", Name: "Q", BaseURL: "https://example.org", Enabled: true, Models: []string{"keep2", "drop2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "q", Name: "Q", BaseURL: "https://example.org", Enabled: true, Models: []string{"keep2"}}); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.EffectiveMappings() {
		if m.ProviderID == "q" && m.UpstreamModel == "drop2" {
			t.Errorf("已取消勾选模型的自动映射仍出现在 EffectiveMappings")
		}
	}
}
