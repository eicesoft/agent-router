package templates

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// ToolID identifies a supported CLI whose configuration can be generated.
type ToolID string

const (
	ToolOpenCode ToolID = "opencode"
	ToolMimoCode ToolID = "mimocode"
	ToolPI       ToolID = "pi"
	ToolClaude   ToolID = "claude"
	ToolOMP      ToolID = "omp"
)

// catalogJSON embeds the declarative tool catalog so new tools with an
// existing config shape can be added (and shipped on upgrade) by editing the
// JSON alone.
//
//go:embed catalog.json
var catalogJSON []byte

// Tool describes one target CLI: its binary name, the on-disk config file
// (resolved per OS from the user's home directory), and how it is rendered.
// modelNames maps a routable client model id to its display name.
type Tool struct {
	ID        ToolID
	Name      string
	CLI       string
	Config    string // absolute path, resolved at load
	configRel string // OS-independent relative path
	// SkillsDir is the CLI's global skills directory (absolute), empty for
	// tools without one. Symlinking a skill into it is how a skill managed by
	// this app becomes visible to that CLI.
	SkillsDir string
	skillsRel string // OS-independent relative path, "" when unsupported
	// MultiProvider marks tools whose config holds several providers side by
	// side (agent-router is merged alongside the user's existing providers).
	// Tools without it (Claude Code) get the gateway written over their single
	// upstream instead.
	MultiProvider bool

	// Shape selects the merge routine for the config's structure: "ai-sdk"
	// (opencode/mimocode provider.<name>), "pi" (providers.<name> list), or
	// "claude-env" (settings.json env overwrite).
	Shape string `json:"shape"`

	// ModelSlots lists the named model selection slots a tool exposes (e.g.
	// Claude Code's FABLE/HAIKU/OPUS/SONNET tiers). Empty for tools that list
	// their routable models flatly. The list is catalog-driven so new slots —
	// or new tools reusing an env-slot shape — ship without code changes.
	ModelSlots []ModelSlot

	// SlotBaseline is the per-slot model id already present in Config, keyed
	// by slot, read at resolve time. It is the base layer of the effective
	// slot assignment: re-opening the config must show what the user last
	// saved, not the catalog-order fallback, or every write looks lost. Empty
	// when the file is absent, unparsable, or a slot was never written.
	SlotBaseline map[string]string
}

// ModelSlot is one named model assignment a tool's config exposes: the key
// seeds the generated env-var name (e.g. ANTHROPIC_DEFAULT_<KEY>_MODEL) and
// label is the human-facing caption.
type ModelSlot struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// catalogEntry mirrors one entry of catalog.json.
type catalogEntry struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	CLI           string      `json:"cli"`
	ConfigRel     string      `json:"configRel"`
	SkillsRel     string      `json:"skillsRel"`
	MultiProvider bool        `json:"multiProvider"`
	Shape         string      `json:"shape"`
	ModelSlots    []ModelSlot `json:"modelSlots"`
}

// Tools returns the supported tool templates in catalog order.
func Tools() []Tool {
	var catalog struct {
		Tools []catalogEntry `json:"tools"`
	}
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		// The embedded catalog is compile-time data; a decode failure is a
		// programming error, so panic rather than serve an empty tool list.
		panic(fmt.Errorf("parse embedded tool catalog: %w", err))
	}
	tools := make([]Tool, 0, len(catalog.Tools))
	for _, e := range catalog.Tools {
		tools = append(tools, Tool{
			ID:            ToolID(e.ID),
			Name:          e.Name,
			CLI:           e.CLI,
			configRel:     filepath.FromSlash(e.ConfigRel),
			skillsRel:     filepath.FromSlash(e.SkillsRel),
			MultiProvider: e.MultiProvider,
			Shape:         e.Shape,
			ModelSlots:    e.ModelSlots,
		})
	}
	return tools
}

// Resolve fills in the per-OS config path from the user's home directory.
// The path is expressed relative to home on every platform; the config parent
// directory is created before writing, so Windows paths that would normally
// live under %APPDATA% are intentionally not special-cased here.
func Resolve(t Tool) Tool {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("USERPROFILE")
	}
	t.Config = filepath.Join(home, t.configRel)
	if t.skillsRel != "" {
		t.SkillsDir = filepath.Join(home, t.skillsRel)
	}
	t.SlotBaseline = readSlotBaseline(t)
	return t
}

// readSlotBaseline reads the slot assignments already present in the tool's
// config, so a preview renders the user's saved choice instead of the
// catalog-order fallback. Two shapes carry a slot today: Claude Code stores each
// slot as ANTHROPIC_DEFAULT_<KEY>_MODEL in settings.json's env block, and Codex
// keeps its single MODEL slot in config.toml's top-level model key. A missing,
// unparsable, or slot-less file yields an empty map, which leaves every slot on
// the fallback.
func readSlotBaseline(t Tool) map[string]string {
	if len(t.ModelSlots) == 0 || t.Config == "" {
		return nil
	}
	data, err := os.ReadFile(t.Config)
	if err != nil {
		return nil
	}
	switch t.Shape {
	case "claude-env":
		return readClaudeSlotBaseline(data, t)
	case "codex-toml":
		// Codex 的 model 是单个顶层标量，所以只有一个名为 MODEL 的槽位。
		model := readCodexModel(data)
		if model == "" {
			return nil
		}
		return map[string]string{"MODEL": model}
	}
	return nil
}

// readClaudeSlotBaseline reads the ANTHROPIC_DEFAULT_<KEY>_MODEL values out of
// settings.json's env block.
func readClaudeSlotBaseline(data []byte, t Tool) map[string]string {
	document, ok := parseDoc(data)
	if !ok {
		return nil
	}
	env, ok := document.vals["env"].(*doc)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(t.ModelSlots))
	for _, slot := range t.ModelSlots {
		if value, ok := env.vals["ANTHROPIC_DEFAULT_"+slot.Key+"_MODEL"].(string); ok && value != "" {
			out[slot.Key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Installed reports whether the tool's binary is on PATH.
func (t Tool) Installed() bool {
	_, err := exec.LookPath(t.CLI)
	return err == nil
}

// Preview is what the UI needs to render one tool: install/format facts plus
// the generated config text. Current holds the raw on-disk config (empty when
// the file does not exist) so the UI can show a diff.
type Preview struct {
	ID         ToolID `json:"id"`
	Name       string `json:"name"`
	CLI        string `json:"cli"`
	Installed  bool   `json:"installed"`
	ConfigPath string `json:"configPath"`
	// SkillsPath is the CLI's global skills directory; empty when the tool has
	// none, and the UI hides the skills entry point.
	SkillsPath string `json:"skillsPath"`
	Exists     bool   `json:"exists"`
	Current    string `json:"current"`
	Content    string `json:"content"`
	// MultiProvider marks tools whose config lists several models side by side
	// (the UI offers a checklist), versus slot-based tools like Claude Code.
	MultiProvider bool `json:"multiProvider"`
	// ModelSlots lists the tool's selectable model slots (from the catalog)
	// and Routable the client models the user can assign into them.
	ModelSlots []ModelSlot `json:"modelSlots"`
	Routable   []Model     `json:"routable"`
	// SlotModels is the effective slot -> model id assignment the Content was
	// rendered from, so the UI can reflect (and restore) the default mapping.
	SlotModels map[string]string `json:"slotModels"`
	// Profiles lists the per-model files a write also produces (Codex only), so
	// the UI can show which `--profile` names become available.
	Profiles []ProfilePreview `json:"profiles"`
	// SelectedModels is the initial checked set for the model checklist. Its
	// meaning depends on the tool: for flat-list shapes (ai-sdk/pi/omp) it is
	// the subset already written under the gateway's entry — every routable
	// model only when the config has no gateway entry yet — for Codex it comes
	// from the profiles already on disk (the checklist chooses which files to
	// generate, and defaulting to all would write dozens). The UI must start
	// from this rather than assuming "all", or the meanings cannot both be right.
	SelectedModels []string `json:"selectedModels"`
}

// Generator renders and merges a single gateway provider into each tool's
// config file.
type Generator struct {
	gateway      string // local router base URL, e.g. http://127.0.0.1:9400
	providerName string // provider id embedded in the config, e.g. agent-router
	authToken    string // local gateway key for tools that send an auth header
	routable     []Model
	slotModels   map[string]string   // slot key -> routable model id (nil = auto)
	selected     map[string]struct{} // enabled routable ids (nil = all)
	// catalogTemplate 是克隆 Codex 模型目录用的元数据条目。生产路径不设置它，
	// 由 readCodexCatalogTemplate 从 Codex 自己的 models_cache.json 读；测试用
	// WithCodexCatalogTemplate 固定输入，免得断言依赖这台机器上装了哪个版本。
	catalogTemplate map[string]any
	// catalogAliases 是「客户端模型名 -> 它的别名」。别名与正名在网关上完全等价，
	// 所以 Codex 用别名当 model 时也必须能在目录里查到元数据，否则会掉回兜底窗口。
	// 别名不进 routable（它对任何模型清单都不可见），只在这里补进目录。
	catalogAliases map[string][]string
}

// Model is one routable client model: the id resolvable by the gateway and its
// human-readable display name. The metadata fields mirror what the mapping
// configured for it; 0 / empty means unset and the pi renderer omits the key,
// leaving pi's bundled catalog defaults in place.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	InputContextSize int      `json:"inputContextSize,omitempty"`
	OutputSize       int      `json:"outputSize,omitempty"`
	InputTypes       []string `json:"inputTypes,omitempty"`
}

func NewGenerator(gateway, providerName string, routable []Model) *Generator {
	sorted := append([]Model(nil), routable...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return &Generator{gateway: gateway, providerName: providerName, routable: sorted}
}

// WithAuthToken sets the local gateway key used by tools that authenticate via
// an auth header (Claude Code). Tools that embed no credential are unaffected.
func (g *Generator) WithAuthToken(token string) *Generator {
	g.authToken = token
	return g
}

// WithSlotModels applies explicit per-slot model selections. A nil map keeps
// the automatic catalog-order fallback; a slot mapped to "" clears it.
func (g *Generator) WithSlotModels(slotModels map[string]string) *Generator {
	g.slotModels = slotModels
	return g
}

// Routable returns the routable models the user can assign to slots. The copy
// is non-nil so it marshals as [] rather than null even when no models exist.
func (g *Generator) Routable() []Model {
	return append(make([]Model, 0, len(g.routable)), g.routable...)
}

// WithModels applies an explicit model subset for flat-list tools. A nil list
// keeps every routable model; an empty list disables them all.
func (g *Generator) WithModels(ids []string) *Generator {
	if ids == nil {
		g.selected = nil
		return g
	}
	g.selected = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		g.selected[id] = struct{}{}
	}
	return g
}

// WithCatalogAliases records extra client names that resolve to the same route as a
// routable model. Aliases are invisible in every model list by design, but Codex
// still needs metadata for them: when the user sets `model` to an alias, a catalog
// without that slug falls back to the 272k default window.
func (g *Generator) WithCatalogAliases(aliases map[string][]string) *Generator {
	g.catalogAliases = aliases
	return g
}

// models returns the routable models enabled for flat-list tools, preserving
// catalog order. Slot-based tools (Claude) ignore the subset and instead read
// slotModels, so mergeClaude never calls this.
func (g *Generator) models() []Model {
	if g.selected == nil {
		return g.routable
	}
	out := make([]Model, 0, len(g.routable))
	for _, m := range g.routable {
		if _, ok := g.selected[m.ID]; ok {
			out = append(out, m)
		}
	}
	return out
}

// SlotModels reports the effective per-slot assignment: explicit selections
// where present, then the value already written to the tool's config, and the
// catalog-order fallback last. Cleared slots are omitted.
func (g *Generator) SlotModels(t Tool) map[string]string {
	out := make(map[string]string, len(t.ModelSlots))
	for i, slot := range t.ModelSlots {
		if model := g.slotModel(t, slot.Key, i); model != "" {
			out[slot.Key] = model
		}
	}
	return out
}

// slotModel resolves one slot in precedence order: an explicit selection when
// slotModels is set, then the value the config file already holds (so a saved
// assignment survives re-opening the panel), then the catalog-order fallback
// (slot i -> routable[i]).
//
// The disk layer deliberately outranks the fallback: the fallback is derived
// from the current routable list, so once the user picks a model for a slot,
// re-deriving it would silently re-point the slot at a different model and
// make the saved choice look lost. A slot absent from the file stays on the
// fallback, which is what an unwritten slot means.
func (g *Generator) slotModel(t Tool, slot string, index int) string {
	if g.slotModels != nil {
		return g.slotModels[slot]
	}
	if model := t.SlotBaseline[slot]; model != "" {
		return model
	}
	return modelAt(g.routable, index)
}

// Render produces the config content for the tool, merging an existing config
// file when one is present. The tool's path is resolved from home at render
// time so the preview always reflects the current machine.
func (g *Generator) Render(t Tool) string { return renderFor(g, t) }
