package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agent-router/backend/config"
	"agent-router/backend/credential"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

// pooledTestServer wires a gateway whose provider has several pooled keys.
// Credentials are added with the given name -> secret pairs.
func pooledTestServer(t *testing.T, upstream *httptest.Server, keys map[string]string) (*Server, *credential.Pool) {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	if _, err := mappings.Save(config.ModelMapping{ID: "m", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	pool, err := credential.NewPool(db, fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range keys {
		if _, err := pool.Add("test", name, value); err != nil {
			t.Fatal(err)
		}
	}
	// Round robin by default so a test that does not care about the strategy
	// cannot accidentally depend on session stickiness.
	if err := registry.SetCredentialMode("test", string(credential.ModeRoundRobin)); err != nil {
		t.Fatal(err)
	}
	return New(registry, mappings, pool, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}, nil), pool
}

// A rejected key must be retried on another key within the same request, and the
// client must see the successful response rather than the 401.
func TestFailoverRetriesOnAnotherKey(t *testing.T) {
	var mu sync.Mutex
	tried := []string{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		tried = append(tried, auth)
		attempt := len(tried)
		mu.Unlock()

		// The first key is rejected; the retry must succeed.
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	server, _ := pooledTestServer(t, upstream, map[string]string{"key-a": "sk-a", "key-b": "sk-b"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	server.chatCompletions(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "chatcmpl-ok") {
		t.Fatalf("client did not receive the successful response: %s", response.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tried) != 2 {
		t.Fatalf("expected 2 upstream attempts, got %d: %v", len(tried), tried)
	}
	if tried[0] == tried[1] {
		t.Fatalf("failover retried the same key: %v", tried)
	}
}

// A rate-limited key must not be selected again while another is available, so
// the *next* request must not use it.
func TestRateLimitedKeyIsSkippedOnNextRequest(t *testing.T) {
	var mu sync.Mutex
	byKey := map[string]int{}
	limited := "sk-a"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		byKey[auth]++
		mu.Unlock()
		if auth == limited {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	server, pool := pooledTestServer(t, upstream, map[string]string{"key-a": "sk-a", "key-b": "sk-b"})

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ar-local")
		response := httptest.NewRecorder()
		server.chatCompletions(response, req)
		return response
	}

	// Drive requests until the limited key is actually exercised. Round robin
	// may start on the healthy key, so asserting before that point would test
	// nothing: an untried key has no cool-down to respect.
	mu.Lock()
	started := byKey[limited]
	mu.Unlock()
	if started != 0 {
		t.Fatalf("limited key was used before the loop: %d", started)
	}
	for i := 0; i < 4; i++ {
		if response := send(); response.Code != http.StatusOK {
			t.Fatalf("request %d failed: %d %s", i, response.Code, response.Body.String())
		}
		mu.Lock()
		hit := byKey[limited]
		mu.Unlock()
		if hit > 0 {
			break
		}
	}
	mu.Lock()
	afterLimit := byKey[limited]
	mu.Unlock()
	if afterLimit == 0 {
		t.Fatalf("round robin never reached the limited key: %v", byKey)
	}

	// The limited key is now cooling down, so further requests must avoid it.
	for i := 0; i < 3; i++ {
		if response := send(); response.Code != http.StatusOK {
			t.Fatalf("post-cool request %d failed: %d %s", i, response.Code, response.Body.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if byKey[limited] != afterLimit {
		t.Fatalf("cooling key was selected again: %d -> %d", afterLimit, byKey[limited])
	}
	if !pool.HasCredentials("test") {
		t.Fatal("pool lost its credentials")
	}
}

// With several keys and session mode, consecutive turns of one conversation must
// keep arriving on the same upstream key, and the next request must not retry.
func TestSessionModeKeepsUpstreamKeyAcrossTurns(t *testing.T) {
	var mu sync.Mutex
	tried := []string{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		tried = append(tried, auth)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	server, pool := pooledTestServer(t, upstream, map[string]string{"key-a": "sk-a", "key-b": "sk-b"})
	if err := pool.SetEnabled(pool.IDs("test")[1], false); err != nil {
		t.Fatal(err)
	}
	// Only one key is enabled, so the interesting assertion is that the pool
	// picks it consistently and never retries the same key for one request.
	send := func(body string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer ar-local")
		response := httptest.NewRecorder()
		server.chatCompletions(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", response.Code, response.Body.String())
		}
	}
	// Same conversation head, growing history: the head is what identifies it.
	send(`{"model":"client-model","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"write a script"}]}`)
	send(`{"model":"client-model","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"write a script"},{"role":"assistant","content":"ok"},{"role":"user","content":"again"}]}`)

	mu.Lock()
	defer mu.Unlock()
	if len(tried) != 2 {
		t.Fatalf("expected one upstream attempt per request, got %d: %v", len(tried), tried)
	}
	if tried[0] != tried[1] {
		t.Fatalf("conversation migrated between keys: %v", tried)
	}
}

// A request that is the caller's fault (404) must not rotate away from a healthy
// key or add a retry.
func TestNonRetryableStatusDoesNotRotate(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"unknown model"}}`)
	}))
	defer upstream.Close()

	server, _ := pooledTestServer(t, upstream, map[string]string{"key-a": "sk-a", "key-b": "sk-b"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	server.chatCompletions(response, req)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want the upstream 404 relayed", response.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("a caller-side error was retried %d times", attempts)
	}
}

// The Anthropic passthrough builds its own request, so it needs its own coverage
// that it goes through the pool and reports the credential in the log.
func TestPassthroughUsesPoolAndLogsCredential(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-a" {
			t.Errorf("passthrough used key %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"upstream-model","usage":{"input_tokens":3,"output_tokens":2}}`))
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
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindAnthropic, BaseURL: upstream.URL, Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mappings.Save(config.ModelMapping{ID: "m", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	pool, err := credential.NewPool(db, fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add("test", "key-a", "sk-a"); err != nil {
		t.Fatal(err)
	}
	server := New(registry, mappings, pool, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	server.messagesHandler(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("expected one log row, got %d", len(logs.Items))
	}
	if logs.Items[0].CredentialID == "" || logs.Items[0].CredentialName != "key-a" {
		t.Fatalf("credential not attributed in the log: %+v", logs.Items[0])
	}
}
