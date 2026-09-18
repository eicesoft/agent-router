package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// codexConfigFixture 贴近本机真实的 ~/.codex/config.toml：顶层标量与注释、一个
// 用户的 provider 表、以及 [desktop] / [plugins."…"] 这类嵌套表。合并必须只动
// agent-router 相关的行，其余逐字节保留。
const codexConfigFixture = `# 我的 Codex 配置
model = "gpt-6-astra"
model_reasoning_effort = "ultra"

[desktop]
theme = "dark"
show-context-window-usage = true

[model_providers.omniroute]
name = "OmniRoute"
base_url = "http://localhost:20128/v1"
wire_api = "responses"
env_key = "OPENAI_API_KEY"

[plugins."browser@openai-bundled"]
enabled = true
`

func writeCodexConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexTool(configPath string) Tool {
	return Tool{
		ID: ToolID("codex"), Name: "Codex", CLI: "codex",
		Config: configPath, Shape: "codex-toml",
		ModelSlots: []ModelSlot{{Key: "MODEL", Label: "默认模型"}},
	}
}

// writableCodexTool 供走 Write 的用例使用，返回 tool 与它将要写入的目录。
//
// Write 会在 Config 为空时按 configRel 从 home 解析路径。home 取自
// os.UserHomeDir()，而它会读 $HOME，所以这里把 HOME 指到临时目录：既不碰用户真实
// 的 ~/.codex/config.toml，又能覆盖到真实的 Resolve 路径逻辑。
func writableCodexTool(t *testing.T) (Tool, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := codexTool("")
	tool.configRel = filepath.Join(".codex", "config.toml")
	return tool, dir
}

func codexGenerator() *Generator {
	return NewGenerator("http://127.0.0.1:9400", "agent-router", []Model{{ID: "m1", Name: "m1"}})
}

func TestRenderCodexTOMLFresh(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "config.toml")
	out := codexGenerator().Render(codexTool(missing))
	for _, want := range []string{
		`model = "m1"`,
		`model_provider = "agent-router"`,
		"[model_providers.agent-router]",
		`base_url = "http://127.0.0.1:9400/v1"`,
		`wire_api = "responses"`,
		`env_key = "AGENT_ROUTER_API_KEY"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fresh render missing %q:\n%s", want, out)
		}
	}
}

// 这是本次改动最硬的约束：用户配置里的注释、空行、字段顺序与嵌套表必须原样留下，
// 只有 agent-router 的 provider 表与顶层两行被改动。
func TestMergeCodexTOMLPreservesEverythingElse(t *testing.T) {
	path := writeCodexConfig(t, codexConfigFixture)
	out := codexGenerator().Render(codexTool(path))

	// 用户原有的 provider 与嵌套表必须逐字保留。
	for _, keep := range []string{
		"# 我的 Codex 配置",
		`model_reasoning_effort = "ultra"`,
		"[desktop]",
		`theme = "dark"`,
		`show-context-window-usage = true`,
		"[model_providers.omniroute]",
		`name = "OmniRoute"`,
		`base_url = "http://localhost:20128/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`[plugins."browser@openai-bundled"]`,
		"enabled = true",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("merge dropped %q:\n%s", keep, out)
		}
	}
	if !strings.Contains(out, "[model_providers.agent-router]") {
		t.Errorf("merge did not add the gateway provider:\n%s", out)
	}
	// 顶层 model 是原地替换，不是新增第二行。
	if strings.Count(out, "\nmodel = ") != 1 || strings.Contains(out, "gpt-6-astra") {
		t.Errorf("top-level model not replaced in place:\n%s", out)
	}
	if !strings.Contains(out, `model = "m1"`) || !strings.Contains(out, `model_provider = "agent-router"`) {
		t.Errorf("gateway model wiring missing:\n%s", out)
	}
	// 顶层键必须仍在第一个表头之前，否则 TOML 会把它们解析成那个表的键。
	if idx := strings.Index(out, "model_provider = "); idx < 0 || idx > strings.Index(out, "[desktop]") {
		t.Errorf("top-level keys must precede the first table header:\n%s", out)
	}

	// 除被替换/新增的那几行外，其余行必须与原文完全一致。
	preserved := strings.Split(codexConfigFixture, "\n")
	for _, line := range preserved {
		switch strings.TrimSpace(line) {
		case `model = "gpt-6-astra"`, "":
			continue
		}
		if !strings.Contains(out, line) {
			t.Errorf("line changed or lost: %q", line)
		}
	}
}

// 表体替换时不能吞掉表与下一个表之间的分隔空行。
func TestMergeCodexTOMLKeepsTableSeparator(t *testing.T) {
	body := "model = \"m1\"\n\n[model_providers.agent-router]\nname = \"old\"\nbase_url = \"http://old/v1\"\n\n[other]\nkey = \"v\"\n"
	path := writeCodexConfig(t, body)
	out := codexGenerator().Render(codexTool(path))
	if !strings.Contains(out, "\n\n[other]") {
		t.Errorf("separator blank line before [other] lost:\n%s", out)
	}
	if strings.Contains(out, "http://old/v1") {
		t.Errorf("old provider body survived:\n%s", out)
	}
}

// 重复渲染必须幂等：第二次的结果与第一次逐字节相同。
func TestMergeCodexTOMLIsIdempotent(t *testing.T) {
	path := writeCodexConfig(t, codexConfigFixture)
	g := codexGenerator()
	first := g.Render(codexTool(path))
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	second := g.Render(codexTool(path))
	if first != second {
		t.Errorf("render is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// 语法错误的配置绝不能被覆盖：输出等于输入，用户能看出没生效。
func TestMergeCodexTOMLRefusesToOverwriteBrokenConfig(t *testing.T) {
	broken := "model = \"m1\"\n[unclosed\nkey = \n"
	path := writeCodexConfig(t, broken)
	out := codexGenerator().Render(codexTool(path))
	if out != broken {
		t.Errorf("broken config was rewritten:\n%s", out)
	}
}

// provider 表恰好是文件里第一个表头时，顶层键的插入点与表的替换起点重合。此时顶层
// 键必须仍排在该表头之前，否则会变成表内的键。
func TestMergeCodexTOMLWhenGatewayTableIsFirst(t *testing.T) {
	body := "[model_providers.agent-router]\nname = \"old\"\n"
	path := writeCodexConfig(t, body)
	out := codexGenerator().Render(codexTool(path))
	if i, j := strings.Index(out, "model_provider = "), strings.Index(out, "[model_providers.agent-router]"); i < 0 || i > j {
		t.Fatalf("top-level key landed inside the table:\n%s", out)
	}
	if !strings.Contains(out, `model = "m1"`) {
		t.Errorf("model missing:\n%s", out)
	}
	// 结果本身必须是合法 TOML，且 model_provider 属于顶层。
	var parsed struct {
		Model         string         `toml:"model"`
		ModelProvider string         `toml:"model_provider"`
		Providers     map[string]any `toml:"model_providers"`
	}
	if err := toml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("merged output is not valid TOML: %v\n%s", err, out)
	}
	if parsed.ModelProvider != "agent-router" || parsed.Model != "m1" {
		t.Errorf("parsed = %+v", parsed)
	}
	if _, ok := parsed.Providers["agent-router"]; !ok {
		t.Errorf("provider table missing: %+v", parsed.Providers)
	}
}

// 没有表头、只有顶层键的文件。
func TestMergeCodexTOMLWithoutTables(t *testing.T) {
	path := writeCodexConfig(t, "model = \"old\"\n")
	out := codexGenerator().Render(codexTool(path))
	if !strings.Contains(out, "[model_providers.agent-router]") {
		t.Errorf("provider appended:\n%s", out)
	}
	if !strings.Contains(out, `model = "m1"`) {
		t.Errorf("model not replaced:\n%s", out)
	}
}

// 用户没有可路由模型时只补 provider 段，顶层键完全不动——把用户的 model 行换成
// 一条指向 agent-router 的 model_provider 会破坏他的配置。
func TestMergeCodexTOMLWithoutRoutableModels(t *testing.T) {
	path := writeCodexConfig(t, codexConfigFixture)
	g := NewGenerator("http://127.0.0.1:9400", "agent-router", nil)
	out := g.Render(codexTool(path))
	if !strings.Contains(out, "[model_providers.agent-router]") {
		t.Errorf("provider missing:\n%s", out)
	}
	// 用解析后的顶层字段断言，而不是子串匹配：[model_providers.*] 表名里也含
	// "model_provider"，子串匹配会误报。
	var parsed struct {
		Model         string `toml:"model"`
		ModelProvider string `toml:"model_provider"`
	}
	if err := toml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid TOML: %v\n%s", err, out)
	}
	if parsed.Model != "gpt-6-astra" {
		t.Errorf("user's own model was touched: %q", parsed.Model)
	}
	if parsed.ModelProvider != "" {
		t.Errorf("model_provider written without a model: %q", parsed.ModelProvider)
	}
}

// slot 基线：面板打开时应显示配置里已有的 model，而不是目录顺序的兜底值。
//
// 直接调 readSlotBaseline 而不是走 Resolve：Resolve 会用 configRel 覆盖 Config，
// 把测试指向用户真实的 home 目录。
func TestReadSlotBaselineForCodex(t *testing.T) {
	path := writeCodexConfig(t, codexConfigFixture)
	tool := codexTool(path)
	baseline := readSlotBaseline(tool)
	if got := baseline["MODEL"]; got != "gpt-6-astra" {
		t.Fatalf("SlotBaseline = %q", got)
	}
	// 已保存的选择优先于兜底值，否则重新打开面板会显得选择丢了。
	tool.SlotBaseline = baseline
	g := codexGenerator()
	if got := g.SlotModels(tool)["MODEL"]; got != "gpt-6-astra" {
		t.Fatalf("SlotModels = %q", got)
	}
}
