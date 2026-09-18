package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-router/backend/config"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

// The UI toggle calls Start/Close repeatedly. After a stop, the gateway must be
// observable as stopped and a later start must listen again.
func TestProxyLifecycleRestartsAfterClose(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(db, registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{})

	addr := "127.0.0.1:19301"
	if err := s.Start(addr); err != nil {
		t.Fatalf("initial start: %v", err)
	}
	if !s.Running() {
		t.Fatal("Running() = false right after Start")
	}
	if conn, err := net.Dial("tcp", addr); err != nil {
		t.Fatalf("port not listening after Start: %v", err)
	} else {
		_ = conn.Close()
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if s.Running() {
		t.Error("Running() = true after Close")
	}

	if err := s.Start(addr); err != nil {
		t.Fatalf("restart: %v", err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("port not listening after restart: %v", err)
	}
	_ = conn.Close()
	_ = s.Close()
}

// A completion that outlives the upstream client's timeout must not be cut off
// mid-stream: the client then sees "stream closed before a finish_reason was
// received" instead of the rest of its answer. Only the response header is
// bounded; the body is bounded by the request context.
func TestStreamingCompletionOutlivesHeaderTimeout(t *testing.T) {
	if upstreamClient().Timeout != 0 {
		t.Fatal("upstream client still caps the whole exchange, not just the header")
	}
	if got := upstreamClient().Transport.(*http.Transport).ResponseHeaderTimeout; got != upstreamHeaderTimeout {
		t.Fatalf("response header timeout = %v, want %v", got, upstreamHeaderTimeout)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		// The body is read after the header timeout would have expired; with a
		// whole-exchange Timeout this read would fail and truncate the stream.
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
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
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, APIKeyRef: "provider/test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "test-map", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	tracker := usage.NewSQLiteTracker(db)
	s := newTestServer(db, registry, mappings, fakeSecrets{"provider/test": "sk-test"}, tracker, fakeKeys{valid: "ar-local"})
	// A whole-exchange Timeout would abort this body read; only the header is bounded.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	s.chatCompletions(response, req)

	body := response.Body.String()
	if !strings.Contains(body, `"finish_reason":"stop"`) || !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream was truncated before its terminal event: %s", body)
	}
	logs, err := tracker.ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 || !logs.Items[0].Success {
		t.Fatalf("stream not logged as successful: %+v", logs.Items)
	}
}
