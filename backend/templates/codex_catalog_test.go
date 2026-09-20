package templates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 回归：config.toml 里的 model_catalog_json 只是一个指针，指向的文件必须由同一次
// Write 落盘。只写指针时 Codex 启动即报 "No such file or directory (os error 2)"，
// 连会话都开不了（CLI 0.154.0 实测）。
func TestWriteCodexWritesCatalogBesideConfig(t *testing.T) {
	tool, dir := writableCodexTool(t)
	g := codexGenWith("gpt-5.6-sol").
		WithModels([]string{"gpt-5.6-sol"}).
		WithCodexCatalogTemplate(map[string]any{"slug": "x", "base_instructions": "sys"})

	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}

	main, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), `model_catalog_json = "agent-router-models.json"`) {
		t.Fatalf("config.toml does not point at the catalog:\n%s", main)
	}

	// 指针必须落在 config.toml 同目录，否则 Codex 按 CODEX_HOME 解析时找不到。
	data, err := os.ReadFile(filepath.Join(dir, "agent-router-models.json"))
	if err != nil {
		t.Fatalf("catalog file not written beside config: %v", err)
	}
	var doc struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("catalog is not valid JSON: %v\n%s", err, data)
	}
	if len(doc.Models) != 1 || doc.Models[0].Slug != "gpt-5.6-sol" {
		t.Fatalf("catalog models = %+v, want just gpt-5.6-sol", doc.Models)
	}
}

// 拿不到模板（Codex 没跑过）时整段跳过：既不写指针也不写文件，宁可退回兜底窗口。
func TestWriteCodexSkipsCatalogWithoutTemplate(t *testing.T) {
	tool, dir := writableCodexTool(t)
	t.Setenv("CODEX_HOME", "")
	g := codexGenWith("gpt-5.6-sol").WithModels([]string{"gpt-5.6-sol"})

	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(main), "model_catalog_json") {
		t.Errorf("catalog pointer written without a template:\n%s", main)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-router-models.json")); err == nil {
		t.Errorf("catalog file written without a template")
	}
}

// 悬空指针：config.toml 还指着上一版写入的目录，但文件已经没了（用户清过缓存）。
// 这时必须把那一行摘掉，否则 Codex 仍会以同样的 os error 2 拒绝加载。
func TestWriteCodexRemovesDanglingCatalogPointer(t *testing.T) {
	tool, dir := writableCodexTool(t)
	t.Setenv("CODEX_HOME", "")
	path := filepath.Join(dir, "config.toml")
	body := "model = \"m1\"\nmodel_provider = \"agent-router\"\n" +
		`model_catalog_json = "agent-router-models.json"` + "\n\n[other]\nkey = \"v\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := codexGenWith("m1").Write(tool); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "model_catalog_json") {
		t.Errorf("dangling catalog pointer survived:\n%s", out)
	}
	if !strings.Contains(string(out), "[other]") {
		t.Errorf("unrelated table lost:\n%s", out)
	}
}

// 目录文件仍在时必须保留那一行：那是上一版写入的有效指针，删掉等于白丢 1M 窗口。
func TestWriteCodexKeepsLiveCatalogPointer(t *testing.T) {
	tool, dir := writableCodexTool(t)
	t.Setenv("CODEX_HOME", "")
	path := filepath.Join(dir, "config.toml")
	body := "model = \"m1\"\nmodel_provider = \"agent-router\"\n" +
		`model_catalog_json = "agent-router-models.json"` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-router-models.json"), []byte(`{"models":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := codexGenWith("m1").Write(tool); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `model_catalog_json = "agent-router-models.json"`) {
		t.Errorf("live catalog pointer dropped:\n%s", out)
	}
}
