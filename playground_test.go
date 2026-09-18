package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-router/backend/apikey"
	"agent-router/backend/config"
	"agent-router/backend/credential"
	"agent-router/backend/provider"
	"agent-router/backend/proxy"
	"agent-router/backend/settings"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

// fakeSecrets is a writable in-memory secret store: the credential pool must be
// able to provision a key through it, so Set cannot be a no-op.
type fakeSecrets map[string]string

func (f fakeSecrets) Set(account, value string) error { f[account] = value; return nil }
func (f fakeSecrets) Get(account string) (string, error) {
	value, ok := f[account]
	if !ok {
		return "", errors.New("secret not found")
	}
	return value, nil
}
func (f fakeSecrets) Delete(account string) error { delete(f, account); return nil }

// freeAddr reserves a loopback port and releases it, so the proxy can bind it.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

// newTestApp builds an App whose gateway routes client-model to upstream, and
// captures every playground frame instead of emitting Wails events.
func newTestApp(t *testing.T, upstream http.Handler) (*App, *[]PlaygroundChunk) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(provider.Provider{
		ID: "test", Name: "Test", Kind: provider.KindCompatible,
		BaseURL: server.URL, APIKeyRef: "provider/test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{
		ID: "m", ClientModel: "client-model", ProviderID: "test",
		UpstreamModel: "upstream-model", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	keys, err := apikey.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Save(apikey.Key{Name: "测试", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	prefs, err := settings.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	secrets := fakeSecrets{"provider/test": "sk-test"}
	credentials, err := credential.NewPool(db, secrets)
	if err != nil {
		t.Fatal(err)
	}

	app := &App{ctx: context.Background(), db: db, providers: registry,
		mappings: mappings, keys: keys, settings: prefs,
		secrets: secrets, usage: usage.NewSQLiteTracker(db), credentials: credentials}
	app.proxy = proxy.New(registry, mappings, app.credentials, app.usage, keys)
	app.gatewayAddr = freeAddr(t)
	if err := app.proxy.Start(app.gatewayAddr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.proxy.Close() })

	frames := &[]PlaygroundChunk{}
	previous := emitPlayground
	emitPlayground = func(_ context.Context, chunk PlaygroundChunk) {
		*frames = append(*frames, chunk)
	}
	t.Cleanup(func() { emitPlayground = previous })
	return app, frames
}

func sseHandler(body string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	})
}

func userMessage() []proxy.Message {
	return []proxy.Message{{Role: "user", Content: []byte(`"hi"`)}}
}

func TestPlaygroundChatRelaysFramesAndTerminates(t *testing.T) {
	app, frames := newTestApp(t, sseHandler(
		"data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: [DONE]\n\n", 200))

	result, err := app.PlaygroundChat("run-1", "client-model", userMessage(), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 200 {
		t.Fatalf("status = %d", result.Status)
	}

	var data []string
	for _, frame := range *frames {
		if frame.RunID != "run-1" {
			t.Fatalf("unexpected runId %q", frame.RunID)
		}
		if !frame.Done {
			data = append(data, frame.Data)
		}
	}
	want := []string{
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	if strings.Join(data, "|") != strings.Join(want, "|") {
		t.Fatalf("frames = %v", data)
	}
	// The UI finalizes the stream on the terminator, so it must arrive last.
	if last := (*frames)[len(*frames)-1]; !last.Done {
		t.Fatalf("last frame is not the terminator: %+v", last)
	}
	// The run went through the real HTTP path, so it is logged like any client.
	// The client returns as soon as it reads [DONE], which can precede the
	// gateway's own log write by a hair — hence the poll.
	logged := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if app.usage.Summary().Requests == 1 {
			logged = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !logged {
		t.Fatalf("request was not logged: %+v", app.usage.Summary())
	}
}

// A gateway error must still emit the terminator, or the panel stays stuck
// showing a live stream.
func TestPlaygroundChatTerminatesOnGatewayError(t *testing.T) {
	app, frames := newTestApp(t, sseHandler("boom", 500))

	if _, err := app.PlaygroundChat("run-2", "client-model", userMessage(), ""); err == nil {
		t.Fatal("expected an error for a 500 upstream")
	}
	if len(*frames) != 1 || !(*frames)[0].Done {
		t.Fatalf("expected exactly one terminator frame, got %+v", *frames)
	}
}

func TestPlaygroundChatRejectsUnmappedModel(t *testing.T) {
	app, _ := newTestApp(t, sseHandler("data: [DONE]\n\n", 200))

	_, err := app.PlaygroundChat("run-3", "nope", userMessage(), "")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected a 404 for an unmapped model, got %v", err)
	}
}

// The gateway is only reachable with an enabled local key.
func TestPlaygroundChatRequiresEnabledKey(t *testing.T) {
	app, _ := newTestApp(t, sseHandler("data: [DONE]\n\n", 200))
	for _, key := range app.keys.List() {
		if err := app.keys.SetEnabled(key.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.PlaygroundChat("run-4", "client-model", userMessage(), ""); err == nil ||
		!strings.Contains(err.Error(), "本地密钥") {
		t.Fatalf("expected a missing-key error, got %v", err)
	}
}

// CancelPlayground unblocks a run parked on an upstream that never finishes.
func TestPlaygroundCancelEndsRunInFlight(t *testing.T) {
	app, _ := newTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))

	returned := make(chan error, 1)
	go func() {
		_, err := app.PlaygroundChat("run-5", "client-model", userMessage(), "")
		returned <- err
	}()
	// Wait until the request is actually in flight before cancelling.
	deadline := time.Now().Add(2 * time.Second)
	for app.proxy.Running() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if cancel := app.playgroundCancel; cancel != nil {
			break
		}
	}
	app.CancelPlayground()

	select {
	case err := <-returned:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the run")
	}
}

// 思考档位只在 low/medium/high 时发出 reasoning_effort；off/空串必须不带该字段，
// 否则上游会替不想要思考的一次普通对话烧掉输出预算。
func TestPlaygroundSendsReasoningEffort(t *testing.T) {
	var received map[string]json.RawMessage
	app, _ := newTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))

	if _, err := app.PlaygroundChat("run-6", "client-model", userMessage(), "off"); err != nil {
		t.Fatal(err)
	}
	if _, present := received["reasoning_effort"]; present {
		t.Fatalf("off sent reasoning_effort: %s", received["reasoning_effort"])
	}

	if _, err := app.PlaygroundChat("run-7", "client-model", userMessage(), "high"); err != nil {
		t.Fatal(err)
	}
	if string(received["reasoning_effort"]) != `"high"` {
		t.Fatalf("high missing from the request: %s", received["reasoning_effort"])
	}
}
