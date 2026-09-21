package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// Codex 的 --profile 只接受 [A-Za-z0-9_-]+（codex-rs config_types.rs 的
// ProfileV2Name::from_str，并用 CLI 0.154.0 实测：带点的名字直接被拒）。模型名里
// 的 '/' 与 '.' 必须先折成 '-'，否则写出来的文件 Codex 根本加载不了。
func TestCodexProfileNameSanitizes(t *testing.T) {
	cases := map[string]string{
		"gpt-5.6-sol":             "gpt-5-6-sol",
		"Ybl/deepseek-v4.1-flash": "Ybl-deepseek-v4-1-flash",
		"cmd/Kimi-K2.7-Code":      "cmd-Kimi-K2-7-Code",
		"BL/deepseek-v4-pro":      "BL-deepseek-v4-pro",
		"weird  name//with..dots": "weird-name-with-dots",
		"trailing-dash--":         "trailing-dash",
		"":                        "model",
		"///":                     "model",
		"already_valid_name":      "already_valid_name",
	}
	for in, want := range cases {
		if got := codexProfileName(in); got != want {
			t.Errorf("codexProfileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 消毒是有损的（"a/b" 与 "a-b" 都变 "a-b"），所以必须去重：两个模型共用一个
// profile 名会让其中一个静默选中另一个模型。
func TestCodexProfilesDeduplicatesNames(t *testing.T) {
	models := []Model{
		{ID: "a/b"}, {ID: "a-b"}, {ID: "a.b"},
		{ID: "solo"},
	}
	profiles := codexProfiles(models)
	if len(profiles) != len(models) {
		t.Fatalf("profiles = %d, want %d", len(profiles), len(models))
	}
	seen := map[string]string{}
	for _, p := range profiles {
		if prev, dup := seen[p.Name]; dup {
			t.Fatalf("name %q used by both %q and %q", p.Name, prev, p.Model)
		}
		seen[p.Name] = p.Model
	}
	// 每个名字都必须能被 Codex 接受。
	for _, p := range profiles {
		for _, r := range p.Name {
			valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_'
			if !valid {
				t.Fatalf("name %q contains a character Codex rejects: %q", p.Name, r)
			}
		}
	}
	// 映射必须一一对应：名字不同但各自指回原本的模型。
	want := map[string]string{
		"a-b":   "a/b",
		"a-b-2": "a-b",
		"a-b-3": "a.b",
		"solo":  "solo",
	}
	for name, model := range want {
		var found string
		for _, p := range profiles {
			if p.Name == name {
				found = p.Model
			}
		}
		if found != model {
			t.Errorf("profile %q -> %q, want %q", name, found, model)
		}
	}
}

// profile 只写两行：provider 表在主 config.toml 里，Codex 会合并两层配置（实测
// CLI 0.154.0 确认）。重复写一份 provider 段会让网关地址出现两个真相。
func TestCodexProfileBodyOmitsProviderTable(t *testing.T) {
	g := codexGenerator()
	body := strings.Join(codexProfileBody(g, "gpt-5.6-sol"), "\n") + "\n"
	if strings.Contains(body, "[model_providers") || strings.Contains(body, "base_url") {
		t.Errorf("profile repeats the provider table:\n%s", body)
	}
	var parsed struct {
		Model         string `toml:"model"`
		ModelProvider string `toml:"model_provider"`
	}
	if err := toml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("profile body is not valid TOML: %v\n%s", err, body)
	}
	if parsed.Model != "gpt-5.6-sol" || parsed.ModelProvider != "agent-router" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func codexGenWith(models ...string) *Generator {
	list := make([]Model, 0, len(models))
	for _, m := range models {
		list = append(list, Model{ID: m, Name: m})
	}
	return NewGenerator("http://127.0.0.1:9400", "agent-router", list)
}

// Codex 面板的四个固定名字必须以别名本身生成 profile，而不是映射的正名。
func TestCodexProfilesUseAliases(t *testing.T) {
	tool, dir := writableCodexTool(t)
	g := codexGenWith("Ybl/deepseek-v4.1-flash").
		WithCatalogAliases(map[string][]string{
			"Ybl/deepseek-v4.1-flash": {"gpt-5.6-luna"},
		})
	preview := g.ProfilesPreview(tool)
	if len(preview) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview[0].Name != "gpt-5-6-luna" || preview[0].Model != "gpt-5.6-luna" {
		t.Fatalf("preview = %+v", preview[0])
	}
	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Model string `toml:"model"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "gpt-5-6-luna.config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Model != "gpt-5.6-luna" {
		t.Fatalf("profile model = %q, want gpt-5.6-luna", parsed.Model)
	}
}

// 固定别名必须优先拿到消毒后的规范 profile 名；否则普通同名路由会先占用
// gpt-5-6-sol，别名被挤成 gpt-5-6-sol-2，磁盘上看起来就像 luna 没生成。
func TestCodexProfileAliasWinsNameCollision(t *testing.T) {
	tool, _ := writableCodexTool(t)
	g := codexGenWith("gpt-5.6-sol", "gpt-5-6-sol").
		WithCatalogAliases(map[string][]string{
			"gpt-5-6-sol": {"gpt-5.6-sol"},
		})

	plan := g.codexProfilePlan()
	if len(plan) != 2 {
		t.Fatalf("plan = %+v, want alias and remaining model", plan)
	}
	if plan[0].Name != "gpt-5-6-sol" || plan[0].Model != "gpt-5.6-sol" {
		t.Fatalf("alias profile = %+v, want canonical gpt-5-6-sol", plan[0])
	}
	if plan[1].Name != "gpt-5-6-sol-2" || plan[1].Model != "gpt-5-6-sol" {
		t.Fatalf("colliding model profile = %+v, want suffixed name", plan[1])
	}

	preview := g.ProfilesPreview(tool)
	if len(preview) != 1 || preview[0].Name != "gpt-5-6-sol" || preview[0].Model != "gpt-5.6-sol" {
		t.Fatalf("preview = %+v, want the alias on the canonical profile", preview)
	}
}

// 一次写盘要同时产出主配置与每个选中模型一份 profile，用户不必手工建文件。
// 必须显式选中：默认不再全选（见 TestCodexProfileDefaultIsNotAll）。
func TestWriteCodexWritesProfiles(t *testing.T) {
	tool, dir := writableCodexTool(t)
	path := filepath.Join(dir, "config.toml")
	g := codexGenWith("gpt-5.6-sol", "Ybl/deepseek-v4.1-flash").
		WithModels([]string{"gpt-5.6-sol", "Ybl/deepseek-v4.1-flash"})

	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}
	// 主配置
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("main config not written: %v", err)
	}
	if !strings.Contains(string(main), "[model_providers.agent-router]") {
		t.Errorf("main config missing provider:\n%s", main)
	}
	// 两份 profile，名字已消毒
	for _, want := range []string{"gpt-5-6-sol.config.toml", "Ybl-deepseek-v4-1-flash.config.toml"} {
		data, err := os.ReadFile(filepath.Join(dir, want))
		if err != nil {
			t.Fatalf("profile %s not written: %v", want, err)
		}
		if !strings.Contains(string(data), `model_provider = "agent-router"`) {
			t.Errorf("profile %s missing provider wiring:\n%s", want, data)
		}
	}
	// profile 必须与主配置同目录：Codex 在 CODEX_HOME 下查找。
	if _, err := os.Stat(filepath.Join(dir, "gpt-5-6-sol.config.toml")); err != nil {
		t.Errorf("profile not beside the main config: %v", err)
	}
}

// 每个 profile 的 model 必须与文件名对应，不能都指向同一个模型。
func TestWriteCodexProfilesSelectTheirOwnModel(t *testing.T) {
	tool, dir := writableCodexTool(t)
	g := codexGenWith("gpt-5.6-sol", "gpt-5.6-terra").
		WithModels([]string{"gpt-5.6-sol", "gpt-5.6-terra"})
	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}
	for file, wantModel := range map[string]string{
		"gpt-5-6-sol.config.toml":   "gpt-5.6-sol",
		"gpt-5-6-terra.config.toml": "gpt-5.6-terra",
	} {
		var parsed struct {
			Model string `toml:"model"`
		}
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := toml.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Model != wantModel {
			t.Errorf("%s selects %q, want %q", file, parsed.Model, wantModel)
		}
	}
}

// 收窄选择时不能删除先前生成的 profile：目录里还有其它工具生成的成百个文件，按
// 「不在本次选中集合里」去删会误伤它们。
func TestWriteCodexProfilesNeverDeletesOthers(t *testing.T) {
	tool, dir := writableCodexTool(t)
	// 别的工具留下的文件，名字与我们的命名空间不重叠也不该被动。
	foreign := filepath.Join(dir, "bl-deepseek-v4-pro.config.toml")
	if err := os.WriteFile(foreign, []byte("model = \"BL/deepseek-v4-pro\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wide := codexGenWith("gpt-5.6-sol", "gpt-5.6-terra").
		WithModels([]string{"gpt-5.6-sol", "gpt-5.6-terra"})
	if _, err := wide.Write(tool); err != nil {
		t.Fatal(err)
	}
	// 收窄到只选一个模型。
	narrow := codexGenWith("gpt-5.6-sol").WithModels([]string{"gpt-5.6-sol"})
	if _, err := narrow.Write(tool); err != nil {
		t.Fatal(err)
	}

	if data, err := os.ReadFile(foreign); err != nil || !strings.Contains(string(data), "BL/deepseek-v4-pro") {
		t.Errorf("unrelated profile file was touched: %v %s", err, data)
	}
	if _, err := os.Stat(filepath.Join(dir, "gpt-5-6-terra.config.toml")); err != nil {
		t.Errorf("previously generated profile was deleted: %v", err)
	}
}

// 既有 profile 被覆盖时要留一份备份，与主配置同样的可恢复性。
func TestWriteCodexProfilesBackUpExisting(t *testing.T) {
	tool, dir := writableCodexTool(t)
	existing := filepath.Join(dir, "gpt-5-6-sol.config.toml")
	if err := os.WriteFile(existing, []byte("model = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := codexGenWith("gpt-5.6-sol").WithModels([]string{"gpt-5.6-sol"})
	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backups int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "gpt-5-6-sol.config.toml.") {
			backups++
		}
	}
	if backups == 0 {
		t.Errorf("no backup of the pre-existing profile: %v", entries)
	}
}

// ProfilesPreview 要带上磁盘现状，UI 才能像主配置那样显示对比而不是只报「已写入」。
func TestProfilesPreviewReportsDiskState(t *testing.T) {
	tool, _ := writableCodexTool(t)
	g := codexGenWith("gpt-5.6-sol").WithModels([]string{"gpt-5.6-sol"})

	preview := g.ProfilesPreview(tool)
	if len(preview) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview[0].Name != "gpt-5-6-sol" || preview[0].Model != "gpt-5.6-sol" {
		t.Fatalf("preview = %+v", preview[0])
	}
	if preview[0].Exists {
		t.Errorf("fresh preview should not claim an existing file: %+v", preview[0])
	}
	if !strings.Contains(preview[0].Content, `model = "gpt-5.6-sol"`) {
		t.Errorf("content = %s", preview[0].Content)
	}

	if _, err := g.Write(tool); err != nil {
		t.Fatal(err)
	}
	after := g.ProfilesPreview(tool)
	if !after[0].Exists || after[0].Current != after[0].Content {
		t.Errorf("preview did not pick up the written file: %+v", after[0])
	}
	// 幂等：再渲染一次内容不变，UI 不会永远显示「待同步」。
	if after[0].Current != after[0].Content {
		t.Errorf("render is not idempotent:\n%s\n---\n%s", after[0].Current, after[0].Content)
	}
}

// 非 codex 工具不该冒出 profile，否则 UI 会显示一堆无关的文件。
func TestProfilesPreviewEmptyForOtherShapes(t *testing.T) {
	g := codexGenWith("m1")
	omp := Tool{ID: ToolOMP, Name: "OMP", CLI: "omp", Shape: "omp", Config: filepath.Join(t.TempDir(), "models.yml")}
	if got := g.ProfilesPreview(omp); got != nil {
		t.Errorf("profiles for omp = %+v", got)
	}
}

// 关键行为：codex 的勾选默认**不是**全选。全选会在用户第一次打开面板时写出几十份
// profile 文件，而这份勾选的含义是「生成哪些文件」，与 ai-sdk 类「配置里列出哪些
// 候选模型」完全不同。
func TestCodexProfileDefaultIsNotAll(t *testing.T) {
	tool, _ := writableCodexTool(t)
	// 40 个可路由模型，模拟真实规模。
	ids := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		ids = append(ids, "model-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	g := codexGenWith(ids...).WithSlotModels(map[string]string{"MODEL": "model-a0"})

	got := g.SelectedModels(tool)
	if len(got) != 1 || got[0] != "model-a0" {
		t.Fatalf("default selection = %v, want just the default model", got)
	}
	if n := len(g.ProfilesPreview(tool)); n != 1 {
		t.Fatalf("default profiles = %d, want 1", n)
	}
}

// ai-sdk 类工具语义不变：候选清单默认全选，否则配置里什么模型都不列。
func TestSelectedModelsKeepsAllDefaultForNonCodex(t *testing.T) {
	g := codexGenWith("m1", "m2", "m3")
	aiSDK := Tool{ID: ToolOpenCode, Name: "OpenCode", CLI: "opencode", Shape: "ai-sdk"}
	got := g.SelectedModels(aiSDK)
	if len(got) != 3 {
		t.Fatalf("ai-sdk default = %v, want all three", got)
	}
}

// 重开面板要显示上次写入的结果，而不是退回默认模型——否则每次打开都像写入丢了
// （与 SlotBaseline 同一原则）。
func TestCodexProfileDefaultReflectsDisk(t *testing.T) {
	tool, dir := writableCodexTool(t)
	g := codexGenWith("alpha", "beta", "gamma")

	// 先写入 alpha 与 gamma 两份。
	if _, err := g.WithModels([]string{"alpha", "gamma"}).WriteProfiles(tool); err != nil {
		t.Fatal(err)
	}
	// 不带任何显式勾选重新打开。
	got := g.SelectedModels(tool)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "gamma" {
		t.Fatalf("selection = %v, want [alpha gamma] from disk", got)
	}
	for _, name := range []string{"alpha.config.toml", "gamma.config.toml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

// 其它工具留下的同名 profile 不能被当成本网关的：靠内容认领，不靠文件名。
func TestCodexProfileDefaultIgnoresForeignFiles(t *testing.T) {
	tool, dir := writableCodexTool(t)
	// 别的工具写的、指向另一个 provider 的同名文件。
	if err := os.WriteFile(filepath.Join(dir, "alpha.config.toml"),
		[]byte("model = \"alpha\"\nmodel_provider = \"omniroute\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := codexGenWith("alpha", "beta")
	// 磁盘上那份不属于本网关，所以回退到默认模型而不是认领 alpha。
	g = g.WithSlotModels(map[string]string{"MODEL": "beta"})
	got := g.SelectedModels(tool)
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("selection = %v, want [beta]: a foreign profile must not be adopted", got)
	}
}

// 显式清空就是真清空，不能被「回退到默认模型」悄悄覆盖。
func TestCodexProfileExplicitEmptyStaysEmpty(t *testing.T) {
	tool, _ := writableCodexTool(t)
	g := codexGenWith("alpha", "beta").
		WithSlotModels(map[string]string{"MODEL": "alpha"}).
		WithModels([]string{})

	if got := g.SelectedModels(tool); len(got) != 0 {
		t.Fatalf("selection = %v, want empty", got)
	}
	if n := len(g.ProfilesPreview(tool)); n != 0 {
		t.Fatalf("profiles = %d, want 0", n)
	}
	if _, err := g.WriteProfiles(tool); err != nil {
		t.Fatal(err)
	}
}

// profile 名字必须在全量列表上去重：按选中子集算会让同一模型改勾选后换名字，磁盘上
// 多出一份重复文件。
func TestCodexProfileNamesStableAcrossSelection(t *testing.T) {
	tool, _ := writableCodexTool(t)
	// a/b 与 a-b 折换后同名，冲突在后一个上体现。
	g := codexGenWith("a/b", "a-b", "solo")

	wide := g.WithModels([]string{"a/b", "a-b"})
	if _, err := wide.WriteProfiles(tool); err != nil {
		t.Fatal(err)
	}
	wideNames := map[string]bool{}
	for _, p := range wide.ProfilesPreview(tool) {
		wideNames[p.Name] = true
	}

	// 只选 a/b 时，它必须仍是宽选时的那个名字。
	narrow := g.WithModels([]string{"a/b"})
	preview := narrow.ProfilesPreview(tool)
	if len(preview) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if !wideNames[preview[0].Name] {
		t.Fatalf("name changed with the selection: %q not in %v", preview[0].Name, wideNames)
	}
	// 宽选时已写过这份文件，所以窄选必须看到它存在且内容一致（幂等），否则每次改
	// 勾选都会在磁盘上留下重复文件。
	if !preview[0].Exists {
		t.Errorf("narrowed render lost the file the wide write produced: %+v", preview[0])
	}
	if preview[0].Current != preview[0].Content {
		t.Errorf("narrowed render is not idempotent:\n%s\n---\n%s", preview[0].Current, preview[0].Content)
	}
}
