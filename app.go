package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"

	"agent-router/backend/agent"
	"agent-router/backend/apikey"
	"agent-router/backend/config"
	"agent-router/backend/credential"
	"agent-router/backend/envcfg"
	"agent-router/backend/provider"
	"agent-router/backend/proxy"
	"agent-router/backend/secret"
	"agent-router/backend/settings"
	"agent-router/backend/skills"
	"agent-router/backend/storage"
	"agent-router/backend/templates"
	"agent-router/backend/usage"
)

// App is the desktop-facing application facade. Its exported methods are bound
// to the React UI by Wails.
type App struct {
	ctx       context.Context
	db        *sql.DB
	providers *provider.Registry
	mappings  *config.MappingStore
	agents    *agent.Store
	usage     *usage.SQLiteTracker
	secrets   secret.Store
	keys      *apikey.Store
	proxy     *proxy.Server
	settings  *settings.Store
	// credentials is the pool of upstream keys. The proxy reaches keys only
	// through it, so it is the single owner of which key serves a request.
	credentials *credential.Pool

	// gatewayAddr is the listen address the proxy was last started with;
	// SaveSettings updates it when the host/port changes. Address changes
	// restart the proxy rather than killing in-flight requests.
	gatewayAddr string

	// playgroundCancel aborts the演练场 request in flight; guarded by
	// playgroundMu because Wails runs each binding on its own goroutine.
	playgroundMu     sync.Mutex
	playgroundCancel context.CancelFunc
}

func NewApp() *App {
	db, err := storage.Open()
	if err != nil {
		panic(fmt.Errorf("open application database: %w", err))
	}
	providers, err := provider.NewRegistry(db)
	if err != nil {
		panic(fmt.Errorf("load providers: %w", err))
	}
	keychain := secret.New()
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		panic(fmt.Errorf("load model mappings: %w", err))
	}
	tracker := usage.NewSQLiteTracker(db)
	keys, err := apikey.NewStore(db)
	if err != nil {
		panic(fmt.Errorf("load local api keys: %w", err))
	}
	prefs, err := settings.NewStore(db)
	if err != nil {
		panic(fmt.Errorf("load settings: %w", err))
	}
	current, err := prefs.Get()
	if err != nil {
		panic(fmt.Errorf("read settings: %w", err))
	}
	credentials, err := credential.NewPool(db, keychain)
	if err != nil {
		panic(fmt.Errorf("load provider credentials: %w", err))
	}
	return &App{
		gatewayAddr: current.Address(),
		db:          db,
		providers:   providers,
		mappings:    mappings,
		agents:      agent.NewStore(),
		usage:       tracker,
		secrets:     keychain,
		keys:        keys,
		proxy:       proxy.New(providers, mappings, credentials, tracker, keys),
		settings:    prefs,
		credentials: credentials,
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.proxy.Start(a.gatewayAddr); err != nil {
		panic(fmt.Errorf("start local proxy: %w", err))
	}
}
func (a *App) Shutdown(ctx context.Context) { _ = a.proxy.Close(); _ = a.db.Close() }

func (a *App) GetBootstrap() Bootstrap {
	return Bootstrap{
		Providers:    a.providers.List(),
		Mappings:     a.mappings.List(),
		Agents:       a.agents.List(),
		Usage:        a.usage.Summary(),
		APIKeys:      a.keys.List(),
		ProxyRunning: a.proxy.Running(),
		Settings:     a.appSettings(),
		ChainModes:   a.mappings.ChainModes(),
	}
}

func (a *App) appSettings() settings.Settings {
	current, err := a.settings.Get()
	if err != nil {
		return settings.Defaults
	}
	return current
}

// SaveSettings persists preferences. Gateway host/port changes restart the
// proxy on the new address immediately; the theme is applied by the UI.
func (a *App) SaveSettings(input settings.Settings) (settings.Settings, error) {
	if input.Port < 1 || input.Port > 65535 {
		return a.appSettings(), fmt.Errorf("端口必须在 1-65535 之间")
	}
	if input.Theme != "light" && input.Theme != "dark" {
		input.Theme = "light"
	}
	if input.Host == "" {
		input.Host = settings.Defaults.Host
	}
	if err := a.settings.Save(input); err != nil {
		return a.appSettings(), err
	}
	addr := input.Address()
	if addr != a.gatewayAddr {
		_ = a.proxy.Close()
		if err := a.proxy.Start(addr); err != nil {
			return a.appSettings(), fmt.Errorf("restart proxy on %s: %w", addr, err)
		}
		a.gatewayAddr = addr
	}
	return a.appSettings(), nil
}

// SetProxyRunning starts or stops the local gateway. Stopping refuses while
// Requests are in flight is unnecessary; Close shuts the listener, active
// handlers are drained by the process lifetime.
func (a *App) SetProxyRunning(enabled bool) error {
	if enabled {
		return a.proxy.Start(a.gatewayAddr)
	}
	return a.proxy.Close()
}

func (a *App) SaveProvider(input provider.Provider) (provider.Provider, error) {
	return a.providers.Save(input)
}

// FetchProviderIcon downloads favicon artwork for the provider editor.
func (a *App) FetchProviderIcon(baseURL string) (string, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return provider.FetchIcon(ctx, http.DefaultClient, baseURL)
}

func (a *App) ToggleProvider(id string, enabled bool) error {
	return a.providers.SetEnabled(id, enabled)
}

// DeleteProvider removes its local configuration, model mappings, and every
// stored credential. Deleted catalog providers are not re-added on the next
// startup.
func (a *App) DeleteProvider(id string) error {
	p, ok := a.providers.Get(id)
	if !ok {
		return fmt.Errorf("provider not found")
	}
	// The pool owns every key of this provider, including one adopted from the
	// legacy single-key reference; it deletes the Keychain entries with the rows.
	if err := a.credentials.DeleteByProvider(id); err != nil {
		return fmt.Errorf("delete provider credentials: %w", err)
	}
	// A legacy reference that was never adopted still holds a secret.
	if p.APIKeyRef != "" {
		if _, err := a.secrets.Get(p.APIKeyRef); err == nil {
			if err := a.secrets.Delete(p.APIKeyRef); err != nil {
				return fmt.Errorf("delete provider API key: %w", err)
			}
		}
	}
	if err := a.mappings.DeleteByProvider(id); err != nil {
		return fmt.Errorf("delete provider mappings: %w", err)
	}
	if _, err := a.providers.Delete(id); err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	return nil
}

// SetProviderAPIKey replaces a provider's primary credential. It keeps the
// single-key call shape for compatibility, but the value lands in the pool: the
// provider's existing first credential is updated when it has one, otherwise a
// new one is added. Writing straight to the legacy api_key_ref would create a
// second source of truth for the same key.
func (a *App) SetProviderAPIKey(providerID, apiKey string) error {
	p, ok := a.providers.Get(providerID)
	if !ok {
		return fmt.Errorf("provider not found")
	}
	if apiKey == "" {
		return nil
	}
	if existing := a.credentials.List(providerID); len(existing) > 0 {
		if err := a.credentials.ReplaceSecret(existing[0].ID, apiKey); err != nil {
			return err
		}
		return nil
	}
	// No pooled key yet: adopt the provider's own reference so the key stays at
	// the reference the rest of the system already knows.
	ref := p.APIKeyRef
	if ref == "" {
		ref = "provider/" + providerID
	}
	if err := a.secrets.Set(ref, apiKey); err != nil {
		return err
	}
	_, err := a.credentials.Adopt(providerID, ref, "默认密钥")
	return err
}

// ProviderCredential is the UI-facing view of one pooled key. It deliberately
// carries no secret material — not even a suffix, which would still be a
// fragment of the credential.
type ProviderCredential = credential.Credential

// ListProviderCredentials reports a provider's key pool.
func (a *App) ListProviderCredentials(providerID string) []ProviderCredential {
	a.credentials.AdoptLegacy(providerID)
	return a.credentials.List(providerID)
}

// AddProviderCredential stores a new upstream key in the Keychain and adds it to
// the provider's pool.
func (a *App) AddProviderCredential(providerID, name, apiKey string) (ProviderCredential, error) {
	if _, ok := a.providers.Get(providerID); !ok {
		return ProviderCredential{}, fmt.Errorf("provider not found")
	}
	return a.credentials.Add(providerID, name, apiKey)
}

// UpdateProviderCredential renames or reweights a pooled key.
func (a *App) UpdateProviderCredential(id, name string, weight int) (ProviderCredential, error) {
	return a.credentials.Update(id, name, weight)
}

// DeleteProviderCredential removes one pooled key and its Keychain entry.
func (a *App) DeleteProviderCredential(id string) error {
	return a.credentials.Delete(id)
}

// ToggleProviderCredential enables or disables one pooled key without deleting
// it, so its history and its place in the rotation survive.
func (a *App) ToggleProviderCredential(id string, enabled bool) error {
	return a.credentials.SetEnabled(id, enabled)
}

// ResetProviderCredentialStatus clears a key's invalid state or cool-down so an
// operator can retry it after fixing the upstream problem.
func (a *App) ResetProviderCredentialStatus(id string) error {
	return a.credentials.Reset(id)
}

// SetProviderCredentialMode records how the pool picks among a provider's keys:
// session (sticky per conversation), round_robin, least_used, or random.
func (a *App) SetProviderCredentialMode(providerID, mode string) error {
	if _, ok := a.providers.Get(providerID); !ok {
		return fmt.Errorf("provider not found")
	}
	return a.providers.SetCredentialMode(providerID, string(credential.NormalizeMode(mode)))
}

// FetchProviderModels loads an upstream catalog without persisting the API key
// supplied while a provider is being created or edited.
func (a *App) FetchProviderModels(providerID, kindName, baseURL, apiKey string) ([]provider.AvailableModel, error) {
	kind := provider.Kind(kindName)
	if kind == "" {
		kind = provider.KindCompatible
	}
	if existing, ok := a.providers.Get(providerID); ok {
		kind = existing.Kind
		if apiKey == "" {
			// Ask the pool rather than reading the legacy reference: the key in
			// use may be any pooled credential, not the first one.
			lease, err := a.credentials.Acquire(providerID, string(credential.ModeLeastUsed), "")
			if err != nil {
				return nil, fmt.Errorf("read provider API Key: %w", err)
			}
			apiKey = lease.Secret()
		}
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return provider.FetchModels(ctx, http.DefaultClient, kind, baseURL, apiKey)
}

func (a *App) SaveModelMapping(input config.ModelMapping) (config.ModelMapping, error) {
	return a.mappings.Save(input)
}

// SetChainMode records how a same-name chain picks its starting provider:
// "failover" (always the chain head, the default) or "round_robin" (each
// request starts one provider further along, then still fails over).
func (a *App) SetChainMode(clientModel, mode string) error {
	return a.mappings.SetChainMode(clientModel, mode)
}

func (a *App) SaveLocalAPIKey(input apikey.Key) (apikey.Key, error) {
	return a.keys.Save(input)
}

func (a *App) ToggleLocalAPIKey(id string, enabled bool) error {
	return a.keys.SetEnabled(id, enabled)
}

func (a *App) DeleteLocalAPIKey(id string) error {
	return a.keys.Delete(id)
}

// LocalAPIKeyEnvStatus reports whether a local API key already exists in the
// user's environment or shell profile as AGENT_ROUTER_API_KEY, and whether it
// matches the given key.
func (a *App) LocalAPIKeyEnvStatus(id string) (envcfg.Status, error) {
	key, ok := a.keys.Get(id)
	if !ok {
		return envcfg.Status{}, fmt.Errorf("api key not found")
	}
	return envcfg.StatusFor(key.Key), nil
}

// ExportLocalAPIKeyEnv writes the local API key's value into the user's
// environment as AGENT_ROUTER_API_KEY (persistent on Windows, shell profile on
// macOS/Linux), replacing any existing assignment.
func (a *App) ExportLocalAPIKeyEnv(id string) (envcfg.Status, error) {
	key, ok := a.keys.Get(id)
	if !ok {
		return envcfg.Status{}, fmt.Errorf("api key not found")
	}
	return envcfg.WriteKey(key.Key)
}

func (a *App) SaveAgentPreset(input agent.Preset) (agent.Preset, error) {
	return a.agents.Save(input)
}

// SkillSummary is the skills page listing: every discovered skill plus the
// roots they came from and the set of conflicting (duplicate) names.
type SkillSummary struct {
	Roots     []skills.Root  `json:"roots"`
	Skills    []skills.Skill `json:"skills"`
	Conflicts []string       `json:"conflicts"`
}

// ListSkills scans the user's skills directories. Pure filesystem, no cache —
// cheap enough to run on every page open.
func (a *App) ListSkills() (SkillSummary, error) {
	roots := skills.DefaultRoots()
	items, conflicts := skills.List(roots)
	names := make([]string, 0, len(conflicts))
	for name := range conflicts {
		names = append(names, name)
	}
	sort.Strings(names)
	return SkillSummary{Roots: roots, Skills: items, Conflicts: names}, nil
}

// GetSkill loads one skill's full SKILL.md content.
func (a *App) GetSkill(dir string) (skills.Detail, error) {
	return skills.Get(dir)
}

// ToggleSkill enables/disables a user skill by moving its directory between
// ~/.agents/skills and ~/.agents/skills-disabled. Returns the skill at its
// new location so the UI can update without guessing the moved path.
func (a *App) ToggleSkill(dir string, enabled bool) (skills.Skill, error) {
	return skills.SetEnabled(dir, enabled)
}

// DeleteSkill removes a user skill directory.
func (a *App) DeleteSkill(dir string) error {
	return skills.Delete(dir)
}

// SaveSkillBody rewrites a user skill's SKILL.md body, preserving frontmatter.
func (a *App) SaveSkillBody(dir string, body string) error {
	return skills.SaveBody(dir, body)
}

// ToolSkillLinks is one tool's skills directory plus how every managed skill
// relates to it (linked, linkable, or taken by something else).
type ToolSkillLinks struct {
	TargetDir string             `json:"targetDir"`
	Links     []skills.SkillLink `json:"links"`
}

// ListSkillLinks reports which managed skills are published into a tool's
// skills directory. A tool without one returns an empty target directory, which
// the UI reads as "no skills support".
func (a *App) ListSkillLinks(toolID string) (ToolSkillLinks, error) {
	dir, err := toolSkillsDir(toolID)
	if err != nil || dir == "" {
		return ToolSkillLinks{}, err
	}
	items, _ := skills.List(skills.DefaultRoots())
	links, err := skills.Links(dir, publishable(items))
	if err != nil {
		return ToolSkillLinks{}, err
	}
	return ToolSkillLinks{TargetDir: dir, Links: links}, nil
}

// SetSkillLink links or unlinks one managed skill in a tool's skills directory.
func (a *App) SetSkillLink(toolID string, skillDir string, linked bool) error {
	dir, err := toolSkillsDir(toolID)
	if err != nil {
		return err
	}
	if dir == "" {
		return fmt.Errorf("%s 没有 skills 目录", toolID)
	}
	return skills.SetLink(skillDir, dir, linked)
}

// toolSkillsDir resolves a tool's skills directory from the catalog; "" means
// the tool has no skills concept.
func toolSkillsDir(toolID string) (string, error) {
	for _, tool := range templates.Tools() {
		if string(tool.ID) == toolID {
			return templates.Resolve(tool).SkillsDir, nil
		}
	}
	return "", fmt.Errorf("未知工具: %s", toolID)
}

// publishable drops disabled skills: they live outside every CLI's scan path on
// purpose, so publishing one would undo the disable.
func publishable(items []skills.Skill) []skills.Skill {
	out := make([]skills.Skill, 0, len(items))
	for _, skill := range items {
		if skill.Source != skills.SourceDisabled {
			out = append(out, skill)
		}
	}
	return out
}

// ListRequestLogs supplies the paginated request history shown in the desktop UI.
func (a *App) ListRequestLogs(page, pageSize int, filter usage.RequestLogFilter) (usage.RequestLogPage, error) {
	return a.usage.ListRequestLogs(page, pageSize, filter)
}

// GetRequestLog fetches one log's full request/response bodies, kept out of
// listings so a page of large payloads stays within the UI IPC message size.
func (a *App) GetRequestLog(id int64) (usage.RequestLog, error) {
	return a.usage.GetRequestLog(id)
}

// GetUsageBreakdown reports aggregate token and request counts grouped by
// provider and by model for the usage dashboard. Provider rows are grouped by
// provider_id, whose raw value is an opaque ID, so the registry fills in the
// display name the same way UsageByKey carries token_name.
func (a *App) GetUsageBreakdown() usage.Breakdown {
	providers := a.usage.UsageByProvider()
	for i, stat := range providers {
		if p, ok := a.providers.Get(stat.Key); ok {
			providers[i].Name = p.Name
		}
	}
	return usage.Breakdown{
		Providers:   providers,
		Models:      a.usage.UsageByModel(),
		Keys:        a.usage.UsageByKey(),
		Credentials: a.usage.UsageByCredential(),
	}
}

// GatewayHost is the local router address the generated templates point at.
func (a *App) GatewayHost() string { return "http://" + a.gatewayAddr }

// ListToolTemplates previews the merged config for every supported CLI, using
// the automatic slot->model fallback.
func (a *App) ListToolTemplates() []templates.Preview {
	g := a.templateGenerator()
	previews := make([]templates.Preview, 0, len(templates.Tools()))
	for _, tool := range templates.Tools() {
		previews = append(previews, a.previewTemplate(g, tool))
	}
	return previews
}

// RenderToolTemplate re-renders one tool's preview with explicit selections,
// so the UI can update the diff before the user confirms a write. slotModels
// carries per-slot picks (Claude Code); modelIDs the enabled flat-list subset
// (nil = all) for multi-provider tools.
func (a *App) RenderToolTemplate(id templates.ToolID, slotModels map[string]string, modelIDs []string) (templates.Preview, error) {
	for _, tool := range templates.Tools() {
		if tool.ID == id {
			return a.previewTemplate(a.templateGenerator().WithSlotModels(slotModels).WithModels(modelIDs), tool), nil
		}
	}
	return templates.Preview{}, fmt.Errorf("unknown tool %q", id)
}

// WriteToolTemplate merges and writes the config for one tool, returning the
// absolute path written. slotModels carries per-slot picks; modelIDs the
// enabled flat-list subset (nil = all).
func (a *App) WriteToolTemplate(id templates.ToolID, slotModels map[string]string, modelIDs []string) (string, error) {
	for _, tool := range templates.Tools() {
		if tool.ID == id {
			return a.templateGenerator().WithSlotModels(slotModels).WithModels(modelIDs).Write(tool)
		}
	}
	return "", fmt.Errorf("unknown tool %q", id)
}

func (a *App) previewTemplate(g *templates.Generator, tool templates.Tool) templates.Preview {
	tool = templates.Resolve(tool)
	current, _ := os.ReadFile(tool.Config)
	slots := tool.ModelSlots
	if slots == nil {
		slots = []templates.ModelSlot{}
	}
	return templates.Preview{
		ID:             tool.ID,
		Name:           tool.Name,
		CLI:            tool.CLI,
		Installed:      tool.Installed(),
		ConfigPath:     tool.Config,
		SkillsPath:     tool.SkillsDir,
		Exists:         toolExists(tool.Config),
		Current:        string(current),
		Content:        g.Render(tool),
		MultiProvider:  tool.MultiProvider,
		ModelSlots:     slots,
		Routable:       g.Routable(),
		SlotModels:     g.SlotModels(tool),
		Profiles:       g.ProfilesPreview(tool),
		SelectedModels: g.SelectedModels(tool),
	}
}

func (a *App) templateGenerator() *templates.Generator {
	g := templates.NewGenerator(a.GatewayHost(), "agent-router", a.routableModels()).
		WithCatalogAliases(a.catalogAliases())
	for _, key := range a.keys.List() {
		if key.Enabled && key.Key != "" {
			return g.WithAuthToken(key.Key)
		}
	}
	return g
}

// catalogAliases maps a client model to its aliases. Aliases stay out of every
// model list (that is what makes them aliases), but the Codex model catalog must
// carry an entry for them too: a user who sets `model` to an alias would otherwise
// fall back to Codex's 272k default window.
func (a *App) catalogAliases() map[string][]string {
	out := make(map[string][]string, 8)
	for _, m := range a.proxy.EffectiveMappings() {
		if len(m.Aliases) == 0 {
			continue
		}
		out[m.ClientModel] = append(out[m.ClientModel], m.Aliases...)
	}
	return out
}

// routableModels flattens the proxy's effective mappings into the client
// models the templates list — both id and name set to the mapped client model
// name, the exact identifier the gateway can route.
func (a *App) routableModels() []templates.Model {
	out := make([]templates.Model, 0, 8)
	for _, m := range a.proxy.EffectiveMappings() {
		out = append(out, templates.Model{ID: m.ClientModel, Name: m.ClientModel})
	}
	return out
}

func toolExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ResolveModel is used by the forthcoming HTTP proxy to choose an upstream
// target without coupling transport code to UI configuration.
func (a *App) ResolveModel(clientModel string) (config.ModelMapping, error) {
	mapping, ok := a.mappings.Resolve(clientModel)
	if !ok {
		return config.ModelMapping{}, fmt.Errorf("no enabled mapping for model %q", clientModel)
	}
	return mapping, nil
}

type Bootstrap struct {
	Providers    []provider.Provider   `json:"providers"`
	Mappings     []config.ModelMapping `json:"mappings"`
	Agents       []agent.Preset        `json:"agents"`
	Usage        usage.Summary         `json:"usage"`
	APIKeys      []apikey.Key          `json:"apiKeys"`
	ProxyRunning bool                  `json:"proxyRunning"`
	Settings     settings.Settings     `json:"settings"`
	// ChainModes maps a client model name to its chain starting rule. Absent
	// means the default, "failover".
	ChainModes map[string]string `json:"chainModes"`
}
