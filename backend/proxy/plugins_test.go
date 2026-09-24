package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agent-router/backend/config"
	"agent-router/backend/plugin"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

func newPluginStore(t *testing.T) *plugin.Store {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := plugin.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func findDelta(t *testing.T, deltas []plugin.Delta, id string) plugin.Delta {
	t.Helper()
	for _, d := range deltas {
		if d.PluginID == id {
			return d
		}
	}
	t.Fatalf("delta for %s not found in %+v", id, deltas)
	return plugin.Delta{}
}

func TestApplyInputChatStripsWhenEnabled(t *testing.T) {
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDSessionStrip, true); err != nil {
		t.Fatal(err)
	}
	s := &Server{plugins: store}
	in := Request{Messages: []Message{
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
		{Role: "assistant", Content: json.RawMessage(`"Hi"`)},
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
		{Role: "user", Content: json.RawMessage(`"New"`)},
	}}
	out, deltas := s.applyInputChat(in)
	if len(out.Messages) != 3 {
		t.Fatalf("len = %d, want 3", len(out.Messages))
	}
	delta := findDelta(t, deltas, plugin.IDSessionStrip)
	if !delta.Applied() || delta.Saved() <= 0 {
		t.Fatalf("expected positive saved delta, got %+v", delta)
	}
	list := store.List()
	if list[0].InputTokens == 0 || list[0].OutputTokens >= list[0].InputTokens {
		t.Fatalf("stats not recorded: %+v", list[0])
	}
	if list[0].SavedTokens != list[0].InputTokens-list[0].OutputTokens {
		t.Fatalf("savedTokens mismatch: %+v", list[0])
	}
}

func TestApplyInputChatNoopWhenDisabled(t *testing.T) {
	store := newPluginStore(t)
	s := &Server{plugins: store}
	in := Request{Messages: []Message{
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
	}}
	out, deltas := s.applyInputChat(in)
	if len(out.Messages) != 2 {
		t.Fatalf("disabled plugin must not strip, len = %d", len(out.Messages))
	}
	if len(deltas) != 0 {
		t.Fatalf("disabled plugin must not report a delta: %+v", deltas)
	}
	if list := store.List(); list[0].InputTokens != 0 {
		t.Fatalf("disabled plugin must not record stats: %+v", list[0])
	}
}

func TestApplyInputChatNoopWhenStoreNil(t *testing.T) {
	s := &Server{}
	in := Request{Messages: []Message{
		{Role: "user", Content: json.RawMessage(`"x"`)},
		{Role: "user", Content: json.RawMessage(`"x"`)},
	}}
	out, deltas := s.applyInputChat(in)
	if len(out.Messages) != 2 {
		t.Fatalf("nil store must not strip, len = %d", len(out.Messages))
	}
	if len(deltas) != 0 {
		t.Fatalf("nil store must not report a delta: %+v", deltas)
	}
}

func TestApplyInputAnthropicStripsWhenEnabled(t *testing.T) {
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDSessionStrip, true); err != nil {
		t.Fatal(err)
	}
	s := &Server{plugins: store}
	in := AnthropicRequest{Messages: []AnthropicMessage{
		{Role: "user", Content: json.RawMessage(`"shared"`)},
		{Role: "assistant", Content: json.RawMessage(`"ok"`)},
		{Role: "user", Content: json.RawMessage(`"shared"`)},
	}}
	out, _ := s.applyInputAnthropic(in)
	if len(out.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Messages))
	}
}

func TestApplyInputChatCavemanInjectsSystem(t *testing.T) {
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDCaveman, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConfig(plugin.IDCaveman, map[string]string{"level": "ultra"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{plugins: store}
	in := Request{Messages: []Message{
		{Role: "system", Content: json.RawMessage(`"Be helpful."`)},
		{Role: "user", Content: json.RawMessage(`"Hi"`)},
	}}
	out, deltas := s.applyInputChat(in)
	if len(out.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("first role = %q", out.Messages[0].Role)
	}
	text := plugin.ContentText(out.Messages[0].Content)
	if !strings.Contains(text, "Be helpful.") {
		t.Fatalf("original system lost: %s", text)
	}
	if !strings.Contains(text, "Active caveman level for this request: **ultra**") {
		t.Fatalf("level pin missing: %s", text[:min(200, len(text))])
	}
	delta := findDelta(t, deltas, plugin.IDCaveman)
	if !delta.Applied() {
		t.Fatalf("caveman delta missing: %+v", delta)
	}
	// Injection grows the request; Saved() floors at 0 for honest stats.
	if delta.After <= delta.Before {
		t.Fatalf("caveman should grow input: before=%d after=%d", delta.Before, delta.After)
	}
	if delta.Saved() != 0 {
		t.Fatalf("caveman saved should be 0, got %d", delta.Saved())
	}
}

func TestApplyInputChatBothPluginsProduceTwoDeltas(t *testing.T) {
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDSessionStrip, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(plugin.IDCaveman, true); err != nil {
		t.Fatal(err)
	}
	s := &Server{plugins: store}
	in := Request{Messages: []Message{
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
		{Role: "assistant", Content: json.RawMessage(`"Hi"`)},
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
	}}
	out, deltas := s.applyInputChat(in)
	if len(deltas) != 2 {
		t.Fatalf("deltas = %d, want 2: %+v", len(deltas), deltas)
	}
	strip := findDelta(t, deltas, plugin.IDSessionStrip)
	caveman := findDelta(t, deltas, plugin.IDCaveman)
	if strip.Saved() <= 0 {
		t.Fatalf("strip should save: %+v", strip)
	}
	if caveman.After <= caveman.Before {
		t.Fatalf("caveman should grow: %+v", caveman)
	}
	// Hello/Hi/Hello → strip drops second Hello → 2 msgs; caveman prepends system → 3
	if len(out.Messages) != 3 {
		t.Fatalf("messages after both = %d, want 3", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("expected injected system first, got %q", out.Messages[0].Role)
	}
}

// End-to-end: enabled strip changes what the upstream receives; the request
// log still holds the original client body.
func TestChatCompletionsSessionStripUpstream(t *testing.T) {
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer upstream.Close()

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDSessionStrip, true); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}, store)

	body := `{"model":"client-model","messages":[` +
		`{"role":"user","content":"Hello"},` +
		`{"role":"assistant","content":"Hi"},` +
		`{"role":"user","content":"Hello"},` +
		`{"role":"user","content":"New"}` +
		`]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ar-local")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.chatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Count(upstreamBody, `"Hello"`) != 1 {
		t.Fatalf("upstream should see one Hello, body=%s", upstreamBody)
	}
	if !strings.Contains(upstreamBody, `"New"`) {
		t.Fatalf("upstream must keep the new turn: %s", upstreamBody)
	}
	// Log keeps the pre-plugin client body (both Hellos). ListRequestLogs may
	// truncate large bodies; fetch the full row the same way other tests do.
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("expected one request log, got %+v", logs.Items)
	}
	full, err := usage.NewSQLiteTracker(db).GetRequestLog(logs.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(full.RequestBody, `"Hello"`) != 2 {
		t.Fatalf("request log should keep original body: %s", full.RequestBody)
	}
	// Per-request compression 差异值 is recorded on the log row.
	if full.PluginID != plugin.IDSessionStrip {
		t.Fatalf("pluginId = %q, want session_strip", full.PluginID)
	}
	if full.PluginSavedTokens <= 0 || full.PluginBeforeTokens <= full.PluginAfterTokens {
		t.Fatalf("plugin delta not logged: before=%d after=%d saved=%d",
			full.PluginBeforeTokens, full.PluginAfterTokens, full.PluginSavedTokens)
	}
	if full.PluginBeforeTokens-full.PluginAfterTokens != full.PluginSavedTokens {
		t.Fatalf("saved != before-after: %+v", full)
	}
	if len(full.PluginDeltas) != 1 || full.PluginDeltas[0].PluginID != plugin.IDSessionStrip {
		t.Fatalf("pluginDeltas = %+v", full.PluginDeltas)
	}
}

func TestChatCompletionsCavemanAndStripMultiDelta(t *testing.T) {
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer upstream.Close()

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDSessionStrip, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(plugin.IDCaveman, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConfig(plugin.IDCaveman, map[string]string{"level": "wenyan-full"}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}, store)

	body := `{"model":"client-model","messages":[` +
		`{"role":"user","content":"Hello"},` +
		`{"role":"assistant","content":"Hi"},` +
		`{"role":"user","content":"Hello"},` +
		`{"role":"user","content":"New"}` +
		`]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ar-local")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.chatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(upstreamBody, "Active caveman level for this request: **wenyan-full**") {
		t.Fatalf("caveman rules missing upstream: %s", upstreamBody)
	}
	if strings.Count(upstreamBody, `"Hello"`) != 1 {
		t.Fatalf("upstream should see one Hello, body=%s", upstreamBody)
	}

	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	full, err := usage.NewSQLiteTracker(db).GetRequestLog(logs.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.PluginDeltas) != 2 {
		t.Fatalf("want 2 plugin deltas, got %+v", full.PluginDeltas)
	}
	byID := map[string]usage.PluginDelta{}
	for _, d := range full.PluginDeltas {
		byID[d.PluginID] = d
	}
	if strip, ok := byID[plugin.IDSessionStrip]; !ok || strip.Saved <= 0 {
		t.Fatalf("strip delta missing/saved: %+v", full.PluginDeltas)
	}
	caveman, ok := byID[plugin.IDCaveman]
	if !ok || caveman.After <= caveman.Before || caveman.Saved != 0 {
		t.Fatalf("caveman delta wrong: %+v", full.PluginDeltas)
	}
	// Legacy columns prefer the plugin that actually saved tokens.
	if full.PluginID != plugin.IDSessionStrip || full.PluginSavedTokens <= 0 {
		t.Fatalf("legacy columns = %q saved=%d", full.PluginID, full.PluginSavedTokens)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestApplyInputChatCavemanCompressesLongToolAndSavesNet(t *testing.T) {
	store := newPluginStore(t)
	if err := store.SetEnabled(plugin.IDCaveman, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConfig(plugin.IDCaveman, map[string]string{"level": "full"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{plugins: store}

	// Build a tool dump large enough that compression beats the ~1k-token
	// style-pack injection (net savings must be positive).
	var log strings.Builder
	for i := 0; i < 400; i++ {
		log.WriteString("DEBUG prefetch item number " + strconv.Itoa(i) + "\n")
	}
	log.WriteString("ERROR disk full while writing report\n")
	for i := 0; i < 200; i++ {
		log.WriteString("INFO tick " + strconv.Itoa(i) + "\n")
	}
	payload, err := json.Marshal(log.String())
	if err != nil {
		t.Fatal(err)
	}

	in := Request{Messages: []Message{
		{Role: "user", Content: json.RawMessage(`"run tests"`)},
		{Role: "assistant", Content: json.RawMessage(`"ok"`), ToolCalls: json.RawMessage(`[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{}"}}]`)},
		{Role: "tool", Content: payload, ToolCallID: "c1"},
	}}
	out, deltas := s.applyInputChat(in)
	delta := findDelta(t, deltas, plugin.IDCaveman)
	if !delta.Applied() {
		t.Fatalf("caveman delta missing: %+v", deltas)
	}
	if delta.Saved() <= 0 {
		t.Fatalf("expected net input savings after compress-inject, before=%d after=%d", delta.Before, delta.After)
	}
	// Style rules must still be present on the system message.
	found := false
	for _, m := range out.Messages {
		if m.Role == "system" && strings.Contains(plugin.ContentText(m.Content), "Active caveman level") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected injected system rules, messages=%d", len(out.Messages))
	}
	// Tool payload should be smaller than the original dump.
	for _, m := range out.Messages {
		if m.Role == "tool" {
			if len(plugin.ContentText(m.Content)) >= len(log.String()) {
				t.Fatalf("tool payload not compressed: %d >= %d", len(plugin.ContentText(m.Content)), len(log.String()))
			}
		}
	}
}

func TestRecordPluginOutputStatsArms(t *testing.T) {
	store := newPluginStore(t)
	s := &Server{plugins: store}
	// Caveman applied → response arm; session_strip not applied → baseline.
	s.recordPluginOutputStats([]plugin.Delta{{PluginID: plugin.IDCaveman, Before: 10, After: 5}}, 80, true)
	s.recordPluginOutputStats(nil, 120, true)
	var list []plugin.Info
	for _, info := range store.List() {
		list = append(list, info)
	}
	byID := map[string]plugin.Info{}
	for _, info := range list {
		byID[info.ID] = info
	}
	if byID[plugin.IDCaveman].ResponseTokens != 80 || byID[plugin.IDCaveman].ResponseCount != 1 {
		t.Fatalf("caveman response arm: %+v", byID[plugin.IDCaveman])
	}
	if byID[plugin.IDCaveman].BaselineTokens != 120 || byID[plugin.IDCaveman].BaselineCount != 1 {
		t.Fatalf("caveman baseline arm: %+v", byID[plugin.IDCaveman])
	}
	if byID[plugin.IDSessionStrip].ResponseCount != 0 {
		t.Fatalf("session_strip should not get response arm when not applied")
	}
	// session_strip never ran, so BOTH successful requests feed its baseline.
	if byID[plugin.IDSessionStrip].BaselineTokens != 200 || byID[plugin.IDSessionStrip].BaselineCount != 2 {
		t.Fatalf("session_strip baseline: %+v", byID[plugin.IDSessionStrip])
	}
	if byID[plugin.IDCaveman].OutputSavingsRate <= 0 {
		t.Fatalf("expected positive output savings rate, got %v", byID[plugin.IDCaveman].OutputSavingsRate)
	}
}
