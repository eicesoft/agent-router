package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Codex 只把 model_context_window 当作「请求值」，真正生效的是模型元数据里的
// max_context_window：对未知模型 Codex 用兜底元数据（272000，乘 95% 后 258400，
// 也就是用户看到的 256k）。所以「把上游的 1M 报给 Codex」只有一条路——用
// model_catalog_json 提供一份自己的模型目录。
//
// 两条实测约束（CLI 0.154.0）：
//   - 这份目录是**整份替换**，不是与内置目录合并：目录里没写的模型名会掉回兜底元数据。
//   - 每个条目必须自带 model_messages / base_instructions，否则 Codex 直接拒绝加载
//     整份 config.toml（错误形如 "model X is missing both base_instructions and
//     model_messages.instructions_template"）。
//
// 因此条目一律从 Codex 自己的目录里克隆一条模板，再改写窗口——不手抄系统提示词，
// 它跟着 Codex 版本走，也不会在仓库里留一份会过期的副本。
const codexCatalogFile = "agent-router-models.json"

// codexCatalogWindow 是声明给 Codex 的上下文窗口。上游支持 1M，但 Codex 实际可用多少
// 由条目里的 effective_context_window_percent 决定（模板带的是 95%，会留出输出余量），
// 所以这里只声明上游真实支持的上限，不替 Codex 决定压缩阈值。
const codexCatalogWindow = 1_000_000

// codexCatalogPath 返回目录文件的位置：与 config.toml 同目录。config.toml 里写相对
// 文件名，Codex 按 CODEX_HOME 解析它（实测确认），所以两份文件必须挨着。
func codexCatalogPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), codexCatalogFile)
}

// codexCatalogHomes 列出可能存放 Codex 自产目录的目录，按优先级排列：先看
// config.toml 所在目录（也就是本应用写入 / 读取的那一份），再看 $CODEX_HOME。
func codexCatalogHomes(configPath string) []string {
	out := make([]string, 0, 2)
	if dir := filepath.Dir(configPath); dir != "" && dir != "." {
		out = append(out, dir)
	}
	if home := os.Getenv("CODEX_HOME"); home != "" {
		out = append(out, home)
	}
	return out
}

// readCodexCatalogTemplate 从 Codex 自己缓存的模型目录里挑一条当模板。
//
// 用的是 models_cache.json：Codex 拉取目录后自己落盘的那份，结构就是本目录要的形状。
// 找不到时返回 false——调用方必须整段跳过而不是硬写一份，否则会把用户的 Codex 配置
// 弄成加载不了的状态。
func readCodexCatalogTemplate(configPath string) (map[string]any, bool) {
	for _, dir := range codexCatalogHomes(configPath) {
		data, err := os.ReadFile(filepath.Join(dir, "models_cache.json"))
		if err != nil {
			continue
		}
		if entry, ok := pickCodexTemplate(data); ok {
			return entry, true
		}
	}
	return nil, false
}

// pickCodexTemplate 解析 Codex 目录并选出一条模板条目。
//
// 优先非 responses-lite 的条目：网关今天对这些未知模型走的就是顶层工具表，克隆
// responses-lite 条目会顺手改掉工具下发方式，那是另一个改动。
func pickCodexTemplate(data []byte) (map[string]any, bool) {
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	type candidate struct {
		entry map[string]any
		rank  int
	}
	candidates := make([]candidate, 0, len(doc.Models))
	for _, m := range doc.Models {
		if !hasAgentPrompt(m) {
			continue
		}
		rank := 2
		if m["visibility"] == "list" {
			rank = 1
			if m["use_responses_lite"] == false {
				rank = 0
			}
		}
		candidates = append(candidates, candidate{entry: m, rank: rank})
	}
	if len(candidates) == 0 {
		return nil, false
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	return candidates[0].entry, true
}

// hasAgentPrompt 报告一条目录条目是否自带系统提示词。缺了它整份目录都加载不了，所以
// 这种条目只能跳过。
func hasAgentPrompt(m map[string]any) bool {
	if s, ok := m["base_instructions"].(string); ok && s != "" {
		return true
	}
	msgs, ok := m["model_messages"].(map[string]any)
	if !ok {
		return false
	}
	if s, ok := msgs["instructions_template"].(string); ok && s != "" {
		return true
	}
	return false
}

// codexCatalogEntry 由模板克隆出一条网关模型的目录条目。
//
// 只改「这个模型是什么」与窗口；提示词、推理档位、工具形态全部沿用模板，因为那些是
// Codex 与网关共同约定的线协议细节，本应用没有资格替它决定。
func codexCatalogEntry(template map[string]any, m Model) map[string]any {
	entry := make(map[string]any, len(template)+8)
	for k, v := range template {
		entry[k] = v
	}
	entry["slug"] = m.ID
	entry["display_name"] = m.Name
	entry["description"] = "通过 Agent Router 网关路由"
	entry["context_window"] = codexCatalogWindow
	entry["max_context_window"] = codexCatalogWindow
	entry["visibility"] = "list"
	// 模板自带的升级提示与「加速档」说的是 OpenAI 自家套餐，网关不提供，留着只会误导。
	delete(entry, "upgrade")
	delete(entry, "additional_speed_tiers")
	delete(entry, "service_tiers")
	return entry
}

// WithCodexCatalogTemplate 注入模板条目，供测试固定输入；生产路径不设置它，由
// readCodexCatalogTemplate 从磁盘读 Codex 自己的目录。
func (g *Generator) WithCodexCatalogTemplate(entry map[string]any) *Generator {
	g.catalogTemplate = entry
	return g
}

// codexCatalogModels 返回要写进目录的模型：与 profile 同一份勾选集合，所以 Codex 能
// 选的模型都有 1M 元数据，没选的模型不会白占体积。
func (g *Generator) codexCatalogModels(t Tool) []Model {
	if t.Shape != "codex-toml" {
		return nil
	}
	if aliases := g.codexCatalogAliases(); len(aliases) > 0 {
		return aliases
	}
	return g.codexProfileModels(t)
}

// codexCatalogAliases 返回已经挂到某条可路由映射上的 Codex 固定别名。
// 别名不进 g.routable，但 Codex 的 profile 与目录都必须用别名本身作为 model。
func (g *Generator) codexCatalogAliases() []Model {
	out := make([]Model, 0, len(codexModelAliases))
	for _, alias := range codexModelAliases {
		for _, m := range g.routable {
			for _, name := range g.catalogAliases[m.ID] {
				if name == alias {
					out = append(out, Model{ID: alias, Name: alias})
					break
				}
			}
		}
	}
	return out
}

// CodexCatalog 渲染 Codex 的模型目录内容。ok 为 false 表示这台机器上找不到可用的模板
// 条目（Codex 还没跑过、或缓存被清掉了），调用方必须整段跳过：写一份缺提示词的目录会让
// Codex 连 config.toml 都加载不了。
func (g *Generator) CodexCatalog(t Tool) (string, bool) {
	models := g.codexCatalogModels(t)
	if len(models) == 0 {
		return "", false
	}
	template := g.catalogTemplate
	if template == nil {
		var ok bool
		template, ok = readCodexCatalogTemplate(t.Config)
		if !ok {
			return "", false
		}
	}
	entries := make([]map[string]any, 0, len(models))
	for _, m := range models {
		entries = append(entries, codexCatalogEntry(template, m))
		// 别名与正名在网关上等价，所以同一份元数据也要挂在别名下。别名不进任何模型
		// 清单，但用户完全可以把 `model` 设成别名（本机就是这么用的），漏掉它就等于
		// 那条稳定的 256k 还在。
		for _, alias := range g.catalogAliases[m.ID] {
			entries = append(entries, codexCatalogEntry(template, Model{ID: alias, Name: alias}))
		}
	}
	body := map[string]any{"models": entries}
	out, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", false
	}
	return string(out) + "\n", true
}

// codexCatalogLine 是写进 config.toml 的那一行。相对文件名：Codex 按 CODEX_HOME 解析
// 它，而与 config.toml 同目录正是本应用写入的位置。
func codexCatalogLine() string {
	return fmt.Sprintf("model_catalog_json = %q", codexCatalogFile)
}
