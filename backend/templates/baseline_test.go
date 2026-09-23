package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 关键行为：pi 类勾选写入子集后，不带任何显式勾选重新打开必须回到该子集——
// 用户报的 bug 就是「写入之后下次打开还是选择了全部模型」。
func TestSelectedModelsReadsSubsetFromDisk(t *testing.T) {
	tool := Tool{ID: ToolPI, Name: "pi", CLI: "pi", Shape: "pi",
		Config: filepath.Join(t.TempDir(), "models.json")}
	g := codexGenWith("m1", "m2", "m3")
	if _, err := g.WithModels([]string{"m1", "m3"}).Write(tool); err != nil {
		t.Fatal(err)
	}
	got := codexGenWith("m1", "m2", "m3").SelectedModels(tool)
	if len(got) != 2 || got[0] != "m1" || got[1] != "m3" {
		t.Fatalf("selection = %v, want [m1 m3] from disk", got)
	}
}

// ai-sdk 形状（provider.<name>.models 是对象）同样回读。
func TestSelectedModelsReadsAISDKBaseline(t *testing.T) {
	tool := Tool{ID: ToolOpenCode, Name: "OpenCode", CLI: "opencode", Shape: "ai-sdk",
		Config: filepath.Join(t.TempDir(), "opencode.json")}
	g := codexGenWith("m1", "m2")
	if _, err := g.WithModels([]string{"m2"}).Write(tool); err != nil {
		t.Fatal(err)
	}
	got := codexGenWith("m1", "m2").SelectedModels(tool)
	if len(got) != 1 || got[0] != "m2" {
		t.Fatalf("selection = %v, want [m2] from disk", got)
	}
}

// 首次使用（文件缺失、或配置里没有本网关条目）仍默认全选，旧语义不变。
func TestSelectedModelsWithoutGatewayEntryStaysAll(t *testing.T) {
	tool := Tool{ID: ToolPI, Name: "pi", CLI: "pi", Shape: "pi",
		Config: filepath.Join(t.TempDir(), "models.json")}
	got := codexGenWith("m1", "m2").SelectedModels(tool)
	if len(got) != 2 {
		t.Fatalf("missing file = %v, want all", got)
	}
	// 文件存在但只有别人的 provider：本网关从没写入过。
	if err := os.WriteFile(tool.Config,
		[]byte(`{"providers":{"other":{"models":[{"id":"x"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = codexGenWith("m1", "m2").SelectedModels(tool)
	if len(got) != 2 {
		t.Fatalf("foreign entry = %v, want all", got)
	}
}

// 显式清空（models: []）就是真清空，不能被当成「没写过」回退成全选。
func TestSelectedModelsKeepsExplicitEmptyFromDisk(t *testing.T) {
	tool := Tool{ID: ToolPI, Name: "pi", CLI: "pi", Shape: "pi",
		Config: filepath.Join(t.TempDir(), "models.json")}
	g := codexGenWith("m1", "m2")
	if _, err := g.WithModels([]string{}).Write(tool); err != nil {
		t.Fatal(err)
	}
	if got := codexGenWith("m1", "m2").SelectedModels(tool); len(got) != 0 {
		t.Fatalf("selection = %v, want empty", got)
	}
}

// 预览内容与勾选必须同源：重开后的 diff 不能显示「把没勾的模型加回去」。
func TestBaselineRenderMatchesSelection(t *testing.T) {
	tool := Tool{ID: ToolPI, Name: "pi", CLI: "pi", Shape: "pi",
		Config: filepath.Join(t.TempDir(), "models.json")}
	g := codexGenWith("m1", "m2", "m3")
	if _, err := g.WithModels([]string{"m1", "m3"}).Write(tool); err != nil {
		t.Fatal(err)
	}
	content := codexGenWith("m1", "m2", "m3").WithBaseline(tool).Render(tool)
	if !strings.Contains(content, `"id": "m1"`) || !strings.Contains(content, `"id": "m3"`) {
		t.Errorf("content lost the selection:\n%s", content)
	}
	if strings.Contains(content, `"id": "m2"`) {
		t.Errorf("content adds back the unchecked model:\n%s", content)
	}
}

// OMP 写入已存在文件走字节拼接路径：勾选必须真正落盘，重开也能读回来。
func TestOMPSelectionSurvivesWriteAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.yml")
	if err := os.WriteFile(path, []byte("providers:\n  other:\n    baseUrl: http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := Tool{ID: ToolOMP, Name: "OMP", CLI: "omp", Shape: "omp", Config: path}
	g := codexGenWith("m1", "m2", "m3")
	if _, err := g.WithModels([]string{"m2"}).Write(tool); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: m2") ||
		strings.Contains(string(data), "id: m1") ||
		strings.Contains(string(data), "id: m3") {
		t.Fatalf("gateway block must list only the selection:\n%s", data)
	}
	got := codexGenWith("m1", "m2", "m3").SelectedModels(tool)
	if len(got) != 1 || got[0] != "m2" {
		t.Fatalf("selection = %v, want [m2] from disk", got)
	}
}

// ListToolTemplates 用同一个 Generator 遍历所有工具：WithBaseline 必须返回
// 副本，原地改会把一个工具的基线泄漏给下一个。
func TestWithBaselineDoesNotMutateSharedGenerator(t *testing.T) {
	tool := Tool{ID: ToolPI, Name: "pi", CLI: "pi", Shape: "pi",
		Config: filepath.Join(t.TempDir(), "models.json")}
	g := codexGenWith("m1", "m2", "m3")
	if _, err := g.WithModels([]string{"m1"}).Write(tool); err != nil {
		t.Fatal(err)
	}
	g = codexGenWith("m1", "m2", "m3") // 重新构造，selected == nil
	based := g.WithBaseline(tool)
	if based == g {
		t.Fatal("WithBaseline returned the shared generator instead of a copy")
	}
	if g.selected != nil {
		t.Fatal("WithBaseline mutated the shared generator")
	}
	if got := based.SelectedModels(tool); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("based selection = %v, want [m1]", got)
	}
}
