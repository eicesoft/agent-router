package templates

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// renderFor renders the config for one tool. When t.Config exists, the gateway
// provider is merged into the parsed document so the user's other settings —
// and their original field ordering — survive; otherwise a fresh document is
// minted.
func renderFor(g *Generator, t Tool) string {
	if t.Config == "" {
		t = Resolve(t)
	}
	if t.Shape == "omp" {
		return renderYAML(g, t)
	}
	// Codex 的 config.toml 不是 JSON，走不了下面的 parseDoc 路径；交给专门的
	// TOML 分支，避免解析失败后把用户的 TOML 覆盖成 JSON。
	if t.Shape == "codex-toml" {
		return renderCodexTOML(g, t)
	}
	if t.Config != "" {
		if data, err := os.ReadFile(t.Config); err == nil && len(bytes.TrimSpace(data)) > 0 {
			if document, ok := parseDoc(data); ok {
				mergeProvider(document, g, t)
				out, _ := json.MarshalIndent(document, "", "  ")
				return string(out) + "\n"
			}
		}
	}
	// Fresh document: render the tool-appropriate shape from scratch.
	document := newDoc()
	mergeProvider(document, g, t)
	out, _ := json.MarshalIndent(document, "", "  ")
	return string(out) + "\n"
}

// renderYAML merges the gateway into an omp models.yml, preserving the file's
// bytes verbatim outside the gateway block: only the provider entry named after
// the generator is inserted or replaced, so the user's flow-styled mappings,
// comments, and layout survive a merge untouched. A fresh document is minted
// when the file is missing or not parseable.
func renderYAML(g *Generator, t Tool) string {
	if t.Config != "" {
		if data, err := os.ReadFile(t.Config); err == nil && len(bytes.TrimSpace(data)) > 0 {
			if node, ok := parseOMP(data); ok {
				return mergeOMPBytes(node, data, g)
			}
		}
	}
	document := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mergeOMP(document, g)
	return marshalOMP(document)
}

// mergeOMPBytes splices the gateway provider block into an existing models.yml
// without re-encoding the whole file: it replaces the block when the provider
// key already exists, appends it under providers otherwise. Everything outside
// the gateway block is preserved byte-for-byte, so flow-styled mappings, blank
// lines, and comments keep their original formatting.
func mergeOMPBytes(node *yaml.Node, data []byte, g *Generator) string {
	providers := yamlFind(node, "providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		// providers missing or not a mapping: fall back to full re-encode so
		// the gateway still lands in the document.
		mergeOMP(node, g)
		return marshalOMP(node)
	}
	lines := bytes.Split(data, []byte("\n"))
	hadNL := len(lines) > 0 && len(lines[len(lines)-1]) == 0
	if hadNL {
		lines = lines[:len(lines)-1]
	}
	body := gatewayBody(g)
	bodyLines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	// gatewayBody emits "providers:\n  agent-router:...\n"; the file already
	// has the top-level providers: key, so drop its line and keep the
	// two-space-indented provider block to splice underneath.
	if len(bodyLines) > 0 && string(bytes.TrimSpace(bodyLines[0])) == "providers:" {
		bodyLines = bodyLines[1:]
	}
	if hadNL {
		bodyLines = append(bodyLines, []byte{})
	}

	if keyNode := yamlFindKey(providers, g.providerName); keyNode != nil {
		// Replace the existing entry: lines from its key line up to (not
		// including) the next sibling's key line, or end of file when last.
		start, end := keyNode.Line, len(lines)+1
		for i := 0; i+1 < len(providers.Content); i += 2 {
			if providers.Content[i] == keyNode {
				if i+2 < len(providers.Content) {
					end = providers.Content[i+2].Line // next sibling key line
				}
				break
			}
		}
		out := make([][]byte, 0, len(lines)+8)
		out = append(out, lines[:start-1]...)
		out = append(out, bodyLines...)
		out = append(out, lines[end-1:]...)
		return string(bytes.Join(out, []byte("\n")))
	}

	// Append after the last provider's content (or after providers: when the
	// map is empty).
	insertEnd := providers.Line
	if n := lastChild(providers); len(providers.Content) > 0 {
		insertEnd = blockEnd(n)
	}
	out := make([][]byte, 0, len(lines)+8)
	out = append(out, lines[:insertEnd]...)
	out = append(out, bodyLines...)
	out = append(out, lines[insertEnd:]...)
	return string(bytes.Join(out, []byte("\n")))
}

// lastChild returns the final value node among a mapping's key/value pairs.
func lastChild(m *yaml.Node) *yaml.Node {
	if len(m.Content) == 0 {
		return m
	}
	return m.Content[len(m.Content)-1]
}

// blockEnd returns the last line (1-based, inclusive) occupied by a node and
// every descendant.
func blockEnd(n *yaml.Node) int {
	last := n.Line
	if len(n.Content) > 0 {
		for _, c := range n.Content {
			if end := blockEnd(c); end > last {
				last = end
			}
		}
	}
	return last
}

// gatewayBody renders one provider mapping (with its children indented two
// spaces) so it can be spliced directly under the providers: key.
func gatewayBody(g *Generator) []byte {
	document := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	providers := yamlMapping(document, "providers")
	entry := yamlMapping(providers, g.providerName)
	yamlSet(entry, "baseUrl", yamlScalar(g.gateway+"/v1"))
	yamlSet(entry, "apiKey", yamlScalar(envFor(g.providerName)))
	yamlSet(entry, "api", yamlScalar("openai-completions"))
	yamlSet(entry, "auth", yamlScalar("apiKey"))
	// 与 mergeOMP 的 fresh 路径同源用 g.models()：用 g.routable 会在字节拼接
	// 路径上把用户收窄过的勾选静默扩成全量。
	yamlSet(entry, "models", ompModels(g.models()))
	return []byte(marshalOMP(document))
}

// mergeProvider stamps the gateway provider into the tool's document,
// dispatched by the tool's declared config shape. Each branch swaps in the
// right provider key so writes are idempotent.
//
// Only the JSON-shaped tools reach here: renderFor handles "omp" (YAML) and
// "codex-toml" before parsing, since neither is a JSON document.
func mergeProvider(document *doc, g *Generator, t Tool) {
	switch t.Shape {
	case "ai-sdk":
		mergeAIIDSK(document, g)
	case "pi":
		mergePI(document, g)
	case "claude-env":
		mergeClaude(document, g, t)
	}
}

// mergeAIIDSK handles opencode/mimocode/kilo: provider.<name>{npm,options,models}.
// The tool supports multiple providers, so the gateway is merged alongside the
// user's existing ones. Per-model window sizes and capabilities flow from the
// mapping's metadata; an unset field (0 / empty) is left unwritten so output
// stays identical to before metadata existed.
func mergeAIIDSK(document *doc, g *Generator) {
	providers := document.object("provider")
	models := newDoc()
	for _, m := range g.models() {
		entry := newDoc()
		entry.set("name", m.Name)
		if limit := aiSDKLimit(m); limit != nil {
			entry.set("limit", limit)
		}
		if len(m.InputTypes) > 0 {
			input := aiSDKInput(m.InputTypes)
			entry.set("attachment", anyNonText(input))
			entry.set("modalities", ordered("input", input))
		}
		models.set(m.ID, entry)
	}
	providers.set(g.providerName, ordered(
		"name", g.providerName,
		"env", []string{envFor(g.providerName)},
		"npm", "@ai-sdk/openai-compatible",
		"options", ordered(
			"baseURL", g.gateway+"/v1",
			"setCacheKey", true,
		),
		"models", models,
	))
}

// mergePI merges the gateway into pi's providers map. pi supports several
// providers side by side, so existing entries with different keys (e.g. the
// user's own 9router) are kept untouched; only the gateway's own key is
// replaced on re-render.
func mergePI(document *doc, g *Generator) {
	providers := document.object("providers")
	providers.set(g.providerName, ordered(
		// pi only: opt into session affinity headers so the gateway sees a
		// stable session_id on every request of a conversation.
		"compat", ordered(
			"sendSessionAffinityHeaders", true,
		),
		"baseUrl", g.gateway+"/v1",
		"api", "openai-completions",
		// pi treats a bare uppercase value as a literal key; the $ prefix makes
		// it interpolate AGENT_ROUTER_API_KEY from the environment.
		"apiKey", "$"+envFor(g.providerName),
		"models", piModels(g.models()),
	))
	document.set("defaultProvider", g.providerName)
	document.set("defaultModel", firstModel(g.models()))
}

// ordered builds a *doc from alternating key/value arguments, keeping the
// given order; values must already be marshalable (doc, slice, scalar).
func ordered(keysAndValues ...any) *doc {
	d := newDoc()
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		d.set(keysAndValues[i].(string), keysAndValues[i+1])
	}
	return d
}

// piModels renders pi's model list per its models.json schema: contextWindow
// and maxTokens carry the mapping's configured window sizes (pi uses them for
// compaction and output caps), input carries the configured capabilities, and
// an unset size (0) omits its key so pi falls back to its bundled catalog.
func piModels(models []Model) []any {
	out := make([]any, 0, len(models))
	for _, m := range models {
		entry := ordered(
			"id", m.ID,
			"name", m.Name,
			"input", textImageInput(m.InputTypes),
			"reasoning", true,
		)
		if m.InputContextSize > 0 {
			entry.set("contextWindow", m.InputContextSize)
		}
		if m.OutputSize > 0 {
			entry.set("maxTokens", m.OutputSize)
		}
		out = append(out, entry)
	}
	return out
}

// aiSDKLimit builds the ai-sdk shape's limit object, but only when both sizes
// are configured: kilo/opencode require context and output together inside
// limit, so a half-set object would fail validation.
func aiSDKLimit(m Model) *doc {
	if m.InputContextSize <= 0 || m.OutputSize <= 0 {
		return nil
	}
	return ordered("context", m.InputContextSize, "output", m.OutputSize)
}

// aiSDKInput narrows the mapping's capabilities to the literals the ai-sdk
// shape accepts (text/audio/image/video/pdf — there is no "file"; a configured
// file capability is expressed as pdf, the only document type these tools
// handle), and a model with nothing left still needs text.
func aiSDKInput(types []string) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		switch t {
		case "text", "audio", "image", "video":
			out = append(out, t)
		case "file":
			out = append(out, "pdf")
		}
	}
	if len(out) == 0 {
		return []string{"text"}
	}
	return out
}

// anyNonText reports whether an input capability list contains anything beyond
// text — that is what attachment means in the ai-sdk shape (opencode derives
// it from input_modalities.some(t => t !== "text") when the upstream says).
func anyNonText(input []string) bool {
	for _, t := range input {
		if t != "text" {
			return true
		}
	}
	return false
}

// textImageInput narrows capabilities to the literals pi's and omp's schemas
// accept ("text" | "image"): audio/video/file would fail validation, and a
// model with nothing left still needs text.
func textImageInput(types []string) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		if t == "text" || t == "image" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []string{"text"}
	}
	return out
}

// mergeClaude rewrites settings.json's env so the gateway replaces the current
// Anthropic base URL, and each model slot maps to its selected routable model.
// Claude Code does not support multiple providers, so the env slots are
// overwritten rather than merged. The slot list comes from the catalog; a slot
// with no selection has its pair of env keys removed so stale overrides never
// survive a re-render.
func mergeClaude(document *doc, g *Generator, t Tool) {
	env := document.object("env")
	env.set("ANTHROPIC_BASE_URL", g.gateway)
	env.set("ANTHROPIC_AUTH_TOKEN", g.authToken)
	for i, slot := range t.ModelSlots {
		modelKey := "ANTHROPIC_DEFAULT_" + slot.Key + "_MODEL"
		nameKey := "ANTHROPIC_DEFAULT_" + slot.Key + "_MODEL_NAME"
		model := g.slotModel(t, slot.Key, i)
		if model == "" {
			env.delete(modelKey)
			env.delete(nameKey)
			continue
		}
		env.set(modelKey, model)
		env.set(nameKey, nameFor(g.routable, model))
	}
}

func modelAt(models []Model, index int) string {
	if index >= 0 && index < len(models) {
		return models[index].ID
	}
	return ""
}
func nameFor(models []Model, id string) string {
	for _, m := range models {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}
func firstModel(models []Model) string { return modelAt(models, 0) }

// Write persists merged config for one tool, creating parent directories. For
// Codex it also writes one `--profile` file per selected model, so a single
// click leaves the user able to switch models without hand-editing anything.
// Before overwriting, an existing file is backed up beside it as
// "<filename>.<yyyyMMddHHmmss>" so a mistaken merge is recoverable.
func (g *Generator) Write(t Tool) (string, error) {
	if t.Config == "" {
		t = Resolve(t)
	}
	// 目录文件必须与 config.toml 成对存在：config.toml 里那行 model_catalog_json 只是
	// 指针，文件缺失时 Codex 启动即报 "No such file or directory (os error 2)" 并拒绝
	// 加载配置，连会话都开不了。先写目录再写配置，写目录失败时配置还没被改成悬空
	// 状态。目录完全由本应用生成，不做备份——每次改勾选都留一份备份只会堆垃圾。
	if content, ok := g.CodexCatalog(t); ok {
		path := codexCatalogPath(t.Config)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	if err := writeWithBackup(t.Config, g.Render(t)); err != nil {
		return "", err
	}
	// 传 resolve 过的 tool：WriteProfiles 要用 t.Config 定位同目录，
	// 未 resolve 时它是空的，profile 会一个都写不出来。
	if _, err := g.WriteProfiles(t); err != nil {
		return t.Config, err
	}
	return t.Config, nil
}

// writeWithBackup skips unchanged content. It backs up a changed existing file
// as "<filename>.<yyyyMMddHHmmss>" so a mistaken merge stays recoverable.
func writeWithBackup(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if data, err := os.ReadFile(path); err == nil {
		if bytes.Equal(data, []byte(content)) {
			return nil
		}
		backupPath := path + "." + time.Now().Format("20060102150405")
		if err := os.WriteFile(backupPath, data, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func envFor(provider string) string {
	return upperSnake(provider) + "_API_KEY"
}

func upperSnake(v string) string {
	var b bytes.Buffer
	prevLower := false
	for _, c := range v {
		if c == '-' || c == ' ' || c == '/' {
			b.WriteByte('_')
			prevLower = false
			continue
		}
		upper := c >= 'A' && c <= 'Z'
		if prevLower && upper {
			b.WriteByte('_')
		}
		b.WriteRune(c)
		prevLower = c >= 'a' && c <= 'z'
	}
	return string(bytes.ToUpper(b.Bytes()))
}
