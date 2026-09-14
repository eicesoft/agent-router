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
	return t
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
}

// Model is one routable client model: the id resolvable by the gateway and its
// human-readable display name.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
// where present, otherwise the automatic fallback. Cleared slots are omitted.
func (g *Generator) SlotModels(t Tool) map[string]string {
	out := make(map[string]string, len(t.ModelSlots))
	for i, slot := range t.ModelSlots {
		if model := g.slotModel(slot.Key, i); model != "" {
			out[slot.Key] = model
		}
	}
	return out
}

// slotModel resolves one slot: explicit selection when slotModels is set,
// otherwise the catalog-order fallback (slot i -> routable[i]).
func (g *Generator) slotModel(slot string, index int) string {
	if g.slotModels != nil {
		return g.slotModels[slot]
	}
	return modelAt(g.routable, index)
}

// Render produces the config content for the tool, merging an existing config
// file when one is present. The tool's path is resolved from home at render
// time so the preview always reflects the current machine.
func (g *Generator) Render(t Tool) string { return renderFor(g, t) }
