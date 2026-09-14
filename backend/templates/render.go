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
	yamlSet(entry, "apiKey", yamlScalar(""))
	yamlSet(entry, "api", yamlScalar("openai-completions"))
	yamlSet(entry, "auth", yamlScalar("apiKey"))
	yamlSet(entry, "models", ompModels(g.routable))
	return []byte(marshalOMP(document))
}

// mergeProvider stamps the gateway provider into the tool's document,
// dispatched by the tool's declared config shape. Each branch swaps in the
// right provider key so writes are idempotent.
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

// mergeAIIDSK handles opencode/mimocode: provider.<name>{npm,options,models}.
// The tool supports multiple providers, so the gateway is merged alongside the
// user's existing ones.
func mergeAIIDSK(document *doc, g *Generator) {
	providers := document.object("provider")
	models := newDoc()
	for _, m := range g.models() {
		entry := newDoc()
		entry.set("name", m.Name)
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
		"baseUrl", g.gateway+"/v1",
		"api", "openai-completions",
		"apiKey", "",
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

func piModels(models []Model) []any {
	out := make([]any, 0, len(models))
	for _, m := range models {
		out = append(out, ordered(
			"id", m.ID,
			"name", m.Name,
			"input", []string{"text"},
			"reasoning", true,
		))
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
		model := g.slotModel(slot.Key, i)
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

// Write persists merged config for one tool, creating parent directories.
// Before overwriting, an existing config is backed up beside it as
// "<filename>.<yyyyMMddHHmmss>" so a mistaken merge is recoverable.
func (g *Generator) Write(t Tool) (string, error) {
	fullPath := Resolve(t).Config
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", err
	}
	if data, err := os.ReadFile(fullPath); err == nil {
		backupPath := fullPath + "." + time.Now().Format("20060102150405")
		if err := os.WriteFile(backupPath, data, 0o644); err != nil {
			return "", err
		}
	}
	content := g.Render(t)
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fullPath, nil
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
