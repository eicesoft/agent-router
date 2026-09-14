package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"

	"agent-router/backend/agent"
	"agent-router/backend/apikey"
	"agent-router/backend/config"
	"agent-router/backend/envcfg"
	"agent-router/backend/provider"
	"agent-router/backend/proxy"
	"agent-router/backend/secret"
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
	return &App{
		db:        db,
		providers: providers,
		mappings:  mappings,
		agents:    agent.NewStore(),
		usage:     tracker,
		secrets:   keychain,
		keys:      keys,
		proxy:     proxy.New(providers, mappings, keychain, tracker, keys),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.proxy.Start("127.0.0.1:9400"); err != nil {
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
	}
}

// SetProxyRunning starts or stops the local gateway. Stopping refuses while
// Requests are in flight is unnecessary; Close shuts the listener, active
// handlers are drained by the process lifetime.
func (a *App) SetProxyRunning(enabled bool) error {
	if enabled {
		return a.proxy.Start("127.0.0.1:9400")
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

// DeleteProvider removes its local configuration, model mappings, and stored
// API key. Deleted catalog providers are not re-added on the next startup.
func (a *App) DeleteProvider(id string) error {
	p, ok := a.providers.Get(id)
	if !ok {
		return fmt.Errorf("provider not found")
	}
	if _, err := a.secrets.Get(p.APIKeyRef); err == nil {
		if err := a.secrets.Delete(p.APIKeyRef); err != nil {
			return fmt.Errorf("delete provider API key: %w", err)
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

// SetProviderAPIKey stores only the credential in the OS Keychain. SQLite keeps
// the provider's opaque APIKeyRef, never the token itself.
func (a *App) SetProviderAPIKey(providerID, apiKey string) error {
	p, ok := a.providers.Get(providerID)
	if !ok {
		return fmt.Errorf("provider not found")
	}
	return a.secrets.Set(p.APIKeyRef, apiKey)
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
			secret, err := a.secrets.Get(existing.APIKeyRef)
			if err != nil {
				return nil, fmt.Errorf("read provider API Key: %w", err)
			}
			apiKey = secret
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
// provider and by model for the usage dashboard.
func (a *App) GetUsageBreakdown() usage.Breakdown {
	return usage.Breakdown{
		Providers: a.usage.UsageByProvider(),
		Models:    a.usage.UsageByModel(),
		Keys:      a.usage.UsageByKey(),
	}
}

// GatewayHost is the local router address the generated templates point at.
func (a *App) GatewayHost() string { return "http://127.0.0.1:9400" }

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
		ID:            tool.ID,
		Name:          tool.Name,
		CLI:           tool.CLI,
		Installed:     tool.Installed(),
		ConfigPath:    tool.Config,
		Exists:        toolExists(tool.Config),
		Current:       string(current),
		Content:       g.Render(tool),
		MultiProvider: tool.MultiProvider,
		ModelSlots:    slots,
		Routable:      g.Routable(),
		SlotModels:    g.SlotModels(tool),
	}
}

func (a *App) templateGenerator() *templates.Generator {
	g := templates.NewGenerator(a.GatewayHost(), "agent-router", a.routableModels())
	for _, key := range a.keys.List() {
		if key.Enabled && key.Key != "" {
			return g.WithAuthToken(key.Key)
		}
	}
	return g
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
}
