package templates

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// renderCodexTOML 渲染 Codex CLI 的 ~/.codex/config.toml。
//
// 与 omp 的 YAML 分支同一思路：TOML 解析器只用来「定位」要改的行，写回仍是按行
// splice，所以用户配置里的注释、空行、字段顺序与 [desktop] / [plugins."…"] 这类
// 嵌套表全都原样保留。Codex 的配置文件通常带着大量注释和嵌套表，整份重新序列化会
// 把它们一次抹掉。
//
// 解析失败时不覆盖：返回原文件内容，UI 的「生成配置」与「当前配置」因此没有差异，
// 用户能看出没生效。绝不能退回「新建文档」——那会把 TOML 覆盖成 JSON。
func renderCodexTOML(g *Generator, t Tool) string {
	if t.Config != "" {
		if data, err := os.ReadFile(t.Config); err == nil && len(bytes.TrimSpace(data)) > 0 {
			if out, ok := mergeCodexTOML(data, g, t); ok {
				return out
			}
			return string(data)
		}
	}
	return strings.Join(renderFreshCodexTOML(g, t), "\n") + "\n"
}

// renderFreshCodexTOML 为不存在的配置文件生成一份完整内容，按行返回。
func renderFreshCodexTOML(g *Generator, t Tool) []string {
	lines := make([]string, 0, 8)
	if model := g.slotModel(t, "MODEL", 0); model != "" {
		lines = append(lines, fmt.Sprintf("model = %q", model))
		lines = append(lines, fmt.Sprintf("model_provider = %q", g.providerName))
		lines = append(lines, "")
	}
	return append(lines, codexProviderBlock(g)...)
}

// codexProviderBlock 渲染 [model_providers.<name>] 段。
//
// wire_api 只能是 responses —— Codex 目前唯一支持的线协议，也正是本网关新增
// /v1/responses 的原因。env_key 沿用 tools 包的统一命名，与 backend/envcfg 写进
// 用户 shell 的变量名一致，所以装完 key 就不用再手工导出。
func codexProviderBlock(g *Generator) []string {
	return []string{
		fmt.Sprintf("[model_providers.%s]", g.providerName),
		fmt.Sprintf("name = %q", codexProviderDisplayName),
		fmt.Sprintf("base_url = %q", g.gateway+"/v1"),
		`wire_api = "responses"`,
		fmt.Sprintf("env_key = %q", envFor(g.providerName)),
	}
}

const codexProviderDisplayName = "Agent Router"

// codexProfileExt is the suffix Codex appends to a profile name to find its file:
// `--profile work` loads `$CODEX_HOME/work.config.toml`.
const codexProfileExt = ".config.toml"

// codexProfile is one generated `--profile` entry: the sanitized name Codex
// accepts on the command line and the client model it selects.
type codexProfile struct {
	Name  string
	Model string
}

// codexProfileName turns a client model id into a name Codex accepts for
// --profile. The value is validated as [A-Za-z0-9_-]+ (codex-rs config_types.rs,
// ProfileV2Name::from_str), and measured against CLI 0.154.0: "gpt-5.6-sol" is
// rejected outright, so the dots and slashes in model ids like
// "Ybl/deepseek-v4.1-flash" must be folded to '-' before anything is written.
//
// Folding is lossy in principle ("a/b" and "a-b" both become "a-b"), so callers
// must dedupe the results rather than assume uniqueness.
func codexProfileName(model string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range model {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			// 其余字符（包括 '.' '/' 空格）都折成 '-'，且不重复。
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "model"
	}
	return name
}

// codexProfiles lists the profiles to generate for the selected models, each
// pairing the sanitized name with the model it selects. Names collide after
// folding, so the first model to claim a name keeps it and later ones get a
// numeric suffix: the mapping must stay one-to-one or a profile would silently
// select a different model than its name suggests.
func codexProfiles(models []Model) []codexProfile {
	used := make(map[string]bool, len(models))
	out := make([]codexProfile, 0, len(models))
	for _, m := range models {
		base := codexProfileName(m.ID)
		name := base
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		used[name] = true
		out = append(out, codexProfile{Name: name, Model: m.ID})
	}
	return out
}

// codexProfileBody 渲染一份 profile 文件。只写 model 与 model_provider：provider
// 表在主 config.toml 里，Codex 会把两层配置合并（实测 CLI 0.154.0 确认），所以
// 各 profile 不必重复 base_url 与 env_key——改一次网关地址只需改一处。
func codexProfileBody(g *Generator, model string) []string {
	return []string{
		fmt.Sprintf("model = %q", model),
		fmt.Sprintf("model_provider = %q", g.providerName),
	}
}

// ProfilePreview 是即将生成的一份 Codex profile 文件。带上磁盘上的现状，UI 才能
// 像主配置那样给出对比，而不是只报一个「已写入」。
type ProfilePreview struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Current string `json:"current"`
	Content string `json:"content"`
}

// codexProfilePath 返回某个 profile 的文件路径。Codex 在 CODEX_HOME 下按
// `<name>.config.toml` 查找，即与 config.toml 同目录。
func codexProfilePath(configPath, name string) string {
	return filepath.Join(filepath.Dir(configPath), name+codexProfileExt)
}

// codexProfilePlan 为每个可路由模型算好 profile 名字。
//
// 名字必须在**全量**可路由列表上去重，不能按选中子集算：折换有损时（"a/b" 与 "a-b"
// 都折成 "a-b"），输入列表不同会让同一个模型拿到不同的文件名——用户改一次勾选，
// 磁盘上就多出一份指向同一模型的重复 profile。
func (g *Generator) codexProfilePlan() []codexProfile {
	return codexProfiles(g.routable)
}

// codexProfileIsOurs 判断磁盘上那份 profile 是不是本网关生成的。靠内容而非文件名：
// 目录里还有其它工具留下的成百个 profile，只有 model_provider 指向本网关且模型能
// 路由的那些才归我们管。
func (g *Generator) codexProfileIsOurs(t Tool, p codexProfile) bool {
	data, err := os.ReadFile(codexProfilePath(t.Config, p.Name))
	if err != nil {
		return false
	}
	var parsed struct {
		Model         string `toml:"model"`
		ModelProvider string `toml:"model_provider"`
	}
	if toml.Unmarshal(data, &parsed) != nil {
		return false
	}
	return parsed.ModelProvider == g.providerName && parsed.Model == p.Model
}

// codexProfileModels 决定要为哪些模型生成 profile。
//
// 这里的勾选语义与 ai-sdk 类工具不同：那边是「配置里列出哪些候选模型」，默认全部才
// 有意义；这边是「生成哪些文件」，默认全部会一次写出几十份。所以默认值取磁盘现状
// ——已经是本网关生成的 profile 对应的模型，重开面板看到的就是上次写入的结果（与
// SlotBaseline 同一原则，否则每次写入都像丢了选择）。一份都没有时才退到默认模型，
// 让首次写入至少产出与主配置对应的那一份。
//
// 用户显式勾过（selected 非 nil）就完全按勾选来，包括清空——那是有意的「不再生成」。
func (g *Generator) codexProfileModels(t Tool) []Model {
	if g.selected != nil {
		return g.models()
	}
	existing := make(map[string]struct{}, len(g.routable))
	for _, p := range g.codexProfilePlan() {
		if g.codexProfileIsOurs(t, p) {
			existing[p.Model] = struct{}{}
		}
	}
	if len(existing) == 0 {
		if model := g.slotModel(t, "MODEL", 0); model != "" {
			if m, ok := g.routableModel(model); ok {
				return []Model{m}
			}
		}
		return nil
	}
	out := make([]Model, 0, len(existing))
	for _, m := range g.routable {
		if _, ok := existing[m.ID]; ok {
			out = append(out, m)
		}
	}
	return out
}

// routableModel 报告某个 id 是否可路由。
func (g *Generator) routableModel(id string) (Model, bool) {
	for _, m := range g.routable {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// SelectedModels 返回 UI 勾选列表应有的初始值，按 tool 的语义分派：ai-sdk/pi 类
// 默认全选（候选清单），codex 默认取磁盘现状或默认模型（生成哪些文件）。前端据此
// 渲染，不必自己猜规则。
func (g *Generator) SelectedModels(t Tool) []string {
	models := g.models()
	if t.Shape == "codex-toml" {
		models = g.codexProfileModels(t)
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

// ProfilesPreview 列出为当前选中的模型将生成的 profile 文件。非 codex 工具返回
// nil——只有 Codex 用「一份文件一个模型」的方式切换。
//
// Config 为空时先 resolve：预览要在真实路径上读磁盘现状，否则 UI 会以为每个文件都
// 不存在。这与 renderFor 的惯例一致。
func (g *Generator) ProfilesPreview(t Tool) []ProfilePreview {
	if t.Shape != "codex-toml" {
		return nil
	}
	if t.Config == "" {
		t = Resolve(t)
	}
	if t.Config == "" {
		return nil
	}
	selected := make(map[string]struct{})
	for _, m := range g.codexProfileModels(t) {
		selected[m.ID] = struct{}{}
	}
	plan := g.codexProfilePlan()
	out := make([]ProfilePreview, 0, len(selected))
	for _, p := range plan {
		if _, ok := selected[p.Model]; !ok {
			continue
		}
		path := codexProfilePath(t.Config, p.Name)
		current, err := os.ReadFile(path)
		out = append(out, ProfilePreview{
			Name:    p.Name,
			Model:   p.Model,
			Path:    path,
			Exists:  err == nil,
			Current: string(current),
			Content: strings.Join(codexProfileBody(g, p.Model), "\n") + "\n",
		})
	}
	return out
}

// WriteProfiles 为每个选中的模型写一份 profile 文件，返回写出的路径。
//
// 只增不改删：用户从 40 个模型收窄到 10 个时，先前生成的 30 份文件留在原地。目录
// 里还有其它工具生成的成百个 profile，按「不在本次选中集合里」去删会误伤它们，所以
// 清理交给用户。残留文件里若指向已不可路由的模型，运行时网关会回 404，不会静默走错。
func (g *Generator) WriteProfiles(t Tool) ([]string, error) {
	previews := g.ProfilesPreview(t)
	paths := make([]string, 0, len(previews))
	for _, p := range previews {
		if err := writeWithBackup(p.Path, p.Content); err != nil {
			return paths, err
		}
		paths = append(paths, p.Path)
	}
	return paths, nil
}

// codexEdits 是解析出来的、需要改写的三处位置（行号 1-based）。
type codexEdits struct {
	// providerStart/providerEnd 是 [model_providers.agent-router] 表的行区间
	// （含两端）；providerAbsent 表示该表不存在，需要追加。
	providerStart, providerEnd int
	providerAbsent             bool

	// modelLine / modelProviderLine 是顶层 model 与 model_provider 的行号，
	// 0 表示不存在。两条顶层键必须落在第一个表头之前：TOML 里出现在表头之后的键
	// 属于那张表，写成顶层键会被解析成别的意思。
	modelLine, modelProviderLine int
	firstTableLine               int
}

// codexEdit 是一次行区间替换。last == first-1 表示纯插入。
type codexEdit struct {
	first, last int
	// order 决定落在同一行的两个编辑谁先写：顶层键必须排在表头之前，否则它们会
	// 变成 [model_providers.<name>] 的键。
	order int
	lines []string
}

const (
	codexOrderTopLevel = 0
	codexOrderTable    = 1
)

// mergeCodexTOML 在已有的 config.toml 上做行级合并。返回 false 表示语法错误，
// 调用方应保持文件原样。
func mergeCodexTOML(data []byte, g *Generator, t Tool) (string, bool) {
	edits, ok := locateCodex(data, g.providerName)
	if !ok {
		return "", false
	}
	lines := strings.Split(string(data), "\n")
	trailingNewline := strings.HasSuffix(string(data), "\n")
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}

	model := g.slotModel(t, "MODEL", 0)

	replacements := make([]codexEdit, 0, 3)
	if edits.providerAbsent {
		block := codexProviderBlock(g)
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			// 末行有内容时补一个空行，避免新表与上一条配置贴在一起。
			block = append([]string{""}, block...)
		}
		replacements = append(replacements, codexEdit{
			first: len(lines) + 1, last: len(lines), order: codexOrderTable, lines: block,
		})
	} else {
		replacements = append(replacements, codexEdit{
			first: edits.providerStart, last: edits.providerEnd,
			order: codexOrderTable, lines: codexProviderBlock(g),
		})
	}

	// 顶层 model 与 model_provider。没有可路由模型时两者都不写：把用户的 model 行
	// 换成一条孤零零的 model_provider 会破坏他的配置，而指向 agent-router 却没有
	// 模型名本来也没有意义。那种情况下只补 provider 段。
	if model != "" {
		modelLine := fmt.Sprintf("model = %q", model)
		providerLine := fmt.Sprintf("model_provider = %q", g.providerName)

		switch {
		case edits.modelLine == 0 && edits.modelProviderLine == 0:
			// 两条都不存在：一起插到第一个表头之前。没有表头就插到文件末。
			insertAt := edits.firstTableLine
			if insertAt == 0 {
				insertAt = len(lines) + 1
			}
			replacements = append(replacements, codexEdit{
				first: insertAt, last: insertAt - 1, order: codexOrderTopLevel,
				lines: []string{modelLine, providerLine},
			})
		case edits.modelProviderLine == 0:
			// model 已存在，model_provider 缺失：替换 model 行时把 provider 一并写上，
			// 避免在同一位置插两次。
			replacements = append(replacements, codexEdit{
				first: edits.modelLine, last: edits.modelLine, order: codexOrderTopLevel,
				lines: []string{modelLine, providerLine},
			})
		case edits.modelLine == 0:
			// model_provider 已存在，model 缺失：同上，替换时把 model 补在它前面。
			replacements = append(replacements, codexEdit{
				first: edits.modelProviderLine, last: edits.modelProviderLine, order: codexOrderTopLevel,
				lines: []string{modelLine, providerLine},
			})
		default:
			replacements = append(replacements,
				codexEdit{
					first: edits.modelLine, last: edits.modelLine,
					order: codexOrderTopLevel, lines: []string{modelLine},
				},
				codexEdit{
					first: edits.modelProviderLine, last: edits.modelProviderLine,
					order: codexOrderTopLevel, lines: []string{providerLine},
				},
			)
		}
	}

	// 同一个位置上可能落两个编辑：provider 表恰好是文件里第一个表头时，顶层键的
	// 插入点与表的替换起点重合（前者必须写在表头之前，否则会变成表内的键）。按
	// 位置从后往前、同位置按 order 排序，然后把同位置的合并成一次替换。
	sort.SliceStable(replacements, func(i, j int) bool {
		if replacements[i].first != replacements[j].first {
			return replacements[i].first > replacements[j].first
		}
		return replacements[i].order < replacements[j].order
	})
	merged := make([]codexEdit, 0, len(replacements))
	for _, edit := range replacements {
		if n := len(merged); n > 0 && merged[n-1].first == edit.first {
			merged[n-1].lines = append(merged[n-1].lines, edit.lines...)
			merged[n-1].last = max(merged[n-1].last, edit.last)
			continue
		}
		merged = append(merged, edit)
	}

	out := lines
	for _, edit := range merged {
		first, last := max(edit.first, 1), min(edit.last, len(out))
		last = max(last, 0)
		if first > last+1 {
			continue
		}
		next := make([]string, 0, len(out)+len(edit.lines))
		next = append(next, out[:first-1]...)
		next = append(next, edit.lines...)
		next = append(next, out[last:]...)
		out = next
	}

	joined := strings.Join(out, "\n")
	if trailingNewline && !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return joined, true
}

// locateCodex 用 TOML 解析器找出需要改动的三处位置。解析器只用来定位，不做任何
// 重新序列化。
func locateCodex(data []byte, providerName string) (codexEdits, bool) {
	// 先用整份文档做一次语法校验：parser 的语法错误是延迟暴露的，逐表达式读的
	// 过程中拿不到「后面还有错误」这个信息。
	var probe map[string]any
	if err := toml.Unmarshal(data, &probe); err != nil {
		return codexEdits{}, false
	}

	edits := codexEdits{providerAbsent: true}
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			key, line := nodeKey(node, data)
			if edits.firstTableLine == 0 {
				edits.firstTableLine = line
			}
			if key == "model_providers."+providerName {
				edits.providerAbsent = false
				edits.providerStart = line
				edits.providerEnd = codexTableEnd(data, line)
			}
		case unstable.KeyValue:
			// 只有出现在任何表头之前的键才是顶层键；表内的同名键不属于顶层。
			if edits.firstTableLine != 0 {
				continue
			}
			key, line := nodeKey(node, data)
			switch key {
			case "model":
				edits.modelLine = line
			case "model_provider":
				edits.modelProviderLine = line
			}
		}
	}
	if err := parser.Error(); err != nil {
		return codexEdits{}, false
	}
	return edits, true
}

// codexTableEnd 返回从 startLine 开始的表体占据的最后一行（含）。表体到下一个表头
// 为止，但不吞掉紧邻下一个表头之前的空行——那是分隔用的，删掉会让新写的块与下一个
// 表贴在一起。
func codexTableEnd(data []byte, startLine int) int {
	total := len(strings.Split(string(data), "\n"))
	next := total
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind != unstable.Table && node.Kind != unstable.ArrayTable {
			continue
		}
		if _, at := nodeKey(node, data); at > startLine {
			next = at
			break
		}
	}
	end := next - 1
	lines := strings.Split(string(data), "\n")
	for end > startLine && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

// nodeKey 取一个节点的点分键名与它在文件里的行号。表头节点自身的 Raw 范围为空，
// 位置只能从它的键子节点上读。
func nodeKey(node *unstable.Node, data []byte) (string, int) {
	var parts []string
	line := 0
	for it := node.Key(); it.Next(); {
		keyNode := it.Node()
		parts = append(parts, string(keyNode.Data))
		if line == 0 {
			line = lineOf(data, keyNode.Raw.Offset)
		}
	}
	return strings.Join(parts, "."), line
}

func lineOf(data []byte, offset uint32) int {
	if int(offset) > len(data) {
		return 0
	}
	return strings.Count(string(data[:offset]), "\n") + 1
}

// readCodexModel 读现有的顶层 model，作为 slot 基线，使面板打开时显示用户当前
// 选中的模型，且重复渲染幂等。
func readCodexModel(data []byte) string {
	var doc struct {
		Model string `toml:"model"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return doc.Model
}
