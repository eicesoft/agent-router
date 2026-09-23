package proxy

import (
	"database/sql"
	"encoding/json"
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

// chainTestServer wires two providers that both serve the same client model name,
// each with one pooled key. Failover order follows the mapping IDs, so "first" is
// tried before "second".
func chainTestServer(t *testing.T, primaryURL, backupURL string) (*Server, *sql.DB) {
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
	for _, p := range []provider.Provider{
		{ID: "primary", Name: "Primary", Kind: provider.KindCompatible, BaseURL: primaryURL, Models: []string{"gpt-4.1-primary"}, Enabled: true},
		{ID: "backup", Name: "Backup", Kind: provider.KindCompatible, BaseURL: backupURL, Models: []string{"gpt-4.1-backup"}, Enabled: true},
	} {
		if _, err := registry.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := mappings.DeleteByProvider("openai"); err != nil {
		t.Fatal(err)
	}
	// 同一个客户端模型名，两家提供商：这就是用户要的「同名不同家」。
	for _, m := range []config.ModelMapping{
		{ID: "m-1", ClientModel: "gpt-4.1", ProviderID: "primary", UpstreamModel: "gpt-4.1-primary", Aliases: []string{"gpt-latest"}, Enabled: true},
		{ID: "m-2", ClientModel: "gpt-4.1", ProviderID: "backup", UpstreamModel: "gpt-4.1-backup", Aliases: []string{"gpt-latest"}, Enabled: true},
	} {
		if _, err := mappings.Save(m); err != nil {
			t.Fatal(err)
		}
	}
	pool, err := credential.NewPool(db, fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add("primary", "p-key", "sk-primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add("backup", "b-key", "sk-backup"); err != nil {
		t.Fatal(err)
	}
	return New(registry, mappings, pool, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}), db
}

func okBody(id string) string {
	return `{"id":"` + id + `","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
}

// 同名链上第一家被拒时，请求必须落到第二家，客户端看到成功响应，日志记的是应答的那家。
func TestSameClientModelFailsOverToSecondProvider(t *testing.T) {
	var mu sync.Mutex
	var primaryHits []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		mu.Lock()
		primaryHits = append(primaryHits, body)
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		mu.Lock()
		primaryHits = append(primaryHits, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody("chatcmpl-backup")))
	}))
	defer backup.Close()

	server, db := chainTestServer(t, primary.URL, backup.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	server.chatCompletions(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "chatcmpl-backup") {
		t.Fatalf("client did not get the backup provider's response: %s", response.Body.String())
	}
	mu.Lock()
	if len(primaryHits) != 2 {
		t.Fatalf("expected both providers hit once, got %d: %v", len(primaryHits), primaryHits)
	}
	// 每家收到的是自己的上游模型名，不是客户端名。
	if !strings.Contains(primaryHits[0], `"model":"gpt-4.1-primary"`) || !strings.Contains(primaryHits[1], `"model":"gpt-4.1-backup"`) {
		t.Fatalf("upstream model names not swapped per route: %v", primaryHits)
	}
	mu.Unlock()

	// 用量与日志必须记应答的那家，否则同名链上根本看不出是谁服务的。
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("expected one log row, got %d", len(logs.Items))
	}
	item := logs.Items[0]
	if item.ProviderName != "Backup" || item.UpstreamModel != "gpt-4.1-backup" || item.ClientModel != "gpt-4.1" || !item.Success {
		t.Fatalf("log attributed the wrong route: %+v", item)
	}
}

// 第一家健康时不能碰第二家：正常请求只发一次上游，避免重复计费。
func TestSameClientModelUsesPrimaryWhenHealthy(t *testing.T) {
	var mu sync.Mutex
	backupHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody("chatcmpl-primary")))
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		backupHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody("chatcmpl-backup")))
	}))
	defer backup.Close()

	server, _ := chainTestServer(t, primary.URL, backup.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	server.chatCompletions(response, req)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "chatcmpl-primary") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if backupHits != 0 {
		t.Fatalf("backup provider was called on a healthy request: %d hits", backupHits)
	}
}

// 4xx（非 401/403/429）描述的是请求本身，换一家也会同样失败，所以不能 failover。
func TestSameClientModelDoesNotFailOverOnBadRequest(t *testing.T) {
	backupHits := 0
	var mu sync.Mutex
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		backupHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody("chatcmpl-backup")))
	}))
	defer backup.Close()

	server, _ := chainTestServer(t, primary.URL, backup.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	server.chatCompletions(response, req)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if backupHits != 0 {
		t.Fatalf("a 400 was retried on another provider: %d hits", backupHits)
	}
}

// /v1/models 与生成给 CLI 的列表按名字去重：一条名字背后几家提供商是内部细节。
func TestListModelsCollapsesSameNameRoutes(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backup.Close()
	server, _ := chainTestServer(t, primary.URL, backup.URL)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ar-local")
	response := httptest.NewRecorder()
	server.listModels(response, req)

	var list ModelList
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, model := range list.Data {
		counts[model.ID]++
	}
	if counts["gpt-4.1"] != 1 {
		t.Fatalf("same-name routes leaked as two advertised models: %+v", counts)
	}
}
func readBody(r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	return string(body)
}

// anthropicChainTestServer 是 chainTestServer 的 Anthropic 版：两家都是
// KindAnthropic，走 /v1/messages 透传路径。
func anthropicChainTestServer(t *testing.T, primaryURL, backupURL string) (*Server, *sql.DB) {
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
	for _, p := range []provider.Provider{
		{ID: "primary", Name: "Primary", Kind: provider.KindAnthropic, BaseURL: primaryURL, Models: []string{"claude-x-primary"}, Enabled: true},
		{ID: "backup", Name: "Backup", Kind: provider.KindAnthropic, BaseURL: backupURL, Models: []string{"claude-x-backup"}, Enabled: true},
	} {
		if _, err := registry.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := mappings.DeleteByProvider("openai"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []config.ModelMapping{
		{ID: "m-1", ClientModel: "claude-x", ProviderID: "primary", UpstreamModel: "claude-x-primary", Enabled: true},
		{ID: "m-2", ClientModel: "claude-x", ProviderID: "backup", UpstreamModel: "claude-x-backup", Enabled: true},
	} {
		if _, err := mappings.Save(m); err != nil {
			t.Fatal(err)
		}
	}
	pool, err := credential.NewPool(db, fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add("primary", "p-key", "sk-primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add("backup", "b-key", "sk-backup"); err != nil {
		t.Fatal(err)
	}
	return New(registry, mappings, pool, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}), db
}

// /v1/messages 的同名链同样要跨提供商 failover，日志记应答的那家。
func TestAnthropicMessagesFailsOverToSecondProvider(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("backup path = %s", r.URL.Path)
		}
		body := readBody(r)
		if !strings.Contains(body, `"model":"claude-x-backup"`) {
			t.Errorf("backup did not get its own upstream model: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-backup","content":[{"type":"text","text":"hi"}]}`))
	}))
	defer backup.Close()

	server, db := anthropicChainTestServer(t, primary.URL, backup.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-x","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "ar-local")
	response := httptest.NewRecorder()
	server.messagesHandler(response, req)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "msg-backup") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("expected one log row, got %d", len(logs.Items))
	}
	item := logs.Items[0]
	if item.ProviderName != "Backup" || item.UpstreamModel != "claude-x-backup" || !item.Success {
		t.Fatalf("log attributed the wrong route: %+v", item)
	}
}

// 同名链里混进不同线格式时，/v1/messages 只保留与链首同格式的那些：Anthropic 透传与
// OpenAI 转换发的是两种请求体，跨格式 failover 会把对端看不懂的 body 发过去。
func TestFilterRoutesByKindKeepsOnlyHeadFormat(t *testing.T) {
	routes := []routeTarget{
		{mapping: config.ModelMapping{ID: "a"}, provider: provider.Provider{ID: "a", Kind: provider.KindAnthropic}},
		{mapping: config.ModelMapping{ID: "b"}, provider: provider.Provider{ID: "b", Kind: provider.KindCompatible}},
		{mapping: config.ModelMapping{ID: "c"}, provider: provider.Provider{ID: "c", Kind: provider.KindAnthropic}},
	}
	got := filterRoutesByKind(routes)
	if len(got) != 2 || got[0].provider.ID != "a" || got[1].provider.ID != "c" {
		t.Fatalf("kind filter dropped the wrong routes: %+v", got)
	}
	// 链首是 OpenAI 兼容时反过来：只留兼容的那些，顺序不变。
	openAIHead := []routeTarget{routes[1], routes[0], {mapping: config.ModelMapping{ID: "d"}, provider: provider.Provider{ID: "d", Kind: provider.KindCompatible}}}
	got = filterRoutesByKind(openAIHead)
	if len(got) != 2 || got[0].provider.ID != "b" || got[1].provider.ID != "d" {
		t.Fatalf("kind filter dropped the wrong routes: %+v", got)
	}
}

// 设置链策略为轮转后，连续请求必须交替落到两家；起点之后的顺序仍是 failover。
// 这是「同名不同提供商」真正分摊流量的开关，默认 failover 时永远只打链首。
func TestChainRoundRobinAlternatesProviders(t *testing.T) {
	counts := map[string]int{}
	var mu sync.Mutex
	handle := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			counts[id]++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(okBody("chatcmpl-" + id)))
		}
	}
	primary := httptest.NewServer(handle("primary"))
	defer primary.Close()
	backup := httptest.NewServer(handle("backup"))
	defer backup.Close()

	server, _ := chainTestServer(t, primary.URL, backup.URL)
	if err := server.mappings.SetChainMode("gpt-4.1", config.ChainRoundRobin); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ar-local")
		response := httptest.NewRecorder()
		server.chatCompletions(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, body = %s", i, response.Code, response.Body.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["primary"] != 2 || counts["backup"] != 2 {
		t.Fatalf("round-robin did not alternate: %v", counts)
	}
}

// 策略按声明的 clientModel 存在链首那张映射上，但请求可以用别名命中同一条链。
// 别名必须共享同一个轮换计数器，否则用别名调用时轮转根本不生效。
func TestChainRoundRobinFollowsAliases(t *testing.T) {
	counts := map[string]int{}
	var mu sync.Mutex
	handle := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			counts[id]++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(okBody("chatcmpl-" + id)))
		}
	}
	primary := httptest.NewServer(handle("primary"))
	defer primary.Close()
	backup := httptest.NewServer(handle("backup"))
	defer backup.Close()

	server, _ := chainTestServer(t, primary.URL, backup.URL)
	if err := server.mappings.SetChainMode("gpt-4.1", config.ChainRoundRobin); err != nil {
		t.Fatal(err)
	}
	// 用别名发四次；每次都必须命中链上两家，而不是永远只有一家。
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-latest","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ar-local")
		response := httptest.NewRecorder()
		server.chatCompletions(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, body = %s", i, response.Code, response.Body.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["primary"] != 2 || counts["backup"] != 2 {
		t.Fatalf("alias traffic did not rotate: %v", counts)
	}
}

// 起点轮换后某一家失败时，请求必须继续沿链往下走，而不是只试起点那一站。
func TestChainRoundRobinStillFailsOver(t *testing.T) {
	var mu sync.Mutex
	backupHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		backupHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody("chatcmpl-backup")))
	}))
	defer backup.Close()

	// 链首是 backup：轮转让第一次请求从 backup 开始（成功），第二次从被限流的
	// primary 开始并 failover 回 backup。两次都该成功，且都记在 backup 上。
	server, db := chainTestServer(t, backup.URL, primary.URL)
	if err := server.mappings.SetChainMode("gpt-4.1", config.ChainRoundRobin); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ar-local")
		response := httptest.NewRecorder()
		server.chatCompletions(response, req)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "chatcmpl-backup") {
			t.Fatalf("request %d: status = %d, body = %s", i, response.Code, response.Body.String())
		}
	}
	mu.Lock()
	if backupHits != 2 {
		t.Fatalf("backup did not serve both requests: %d", backupHits)
	}
	mu.Unlock()
	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range logs.Items {
		if item.ProviderName != "Primary" && item.ProviderName != "Backup" {
			t.Fatalf("unexpected provider in log: %+v", item)
		}
	}
}
