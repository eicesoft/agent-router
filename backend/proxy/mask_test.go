package proxy

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agent-router/backend/config"
	"agent-router/backend/credential"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

// End-to-end: a real request through the gateway must produce a log row whose
// credential identity is the masked key, with the plaintext nowhere in the row.
func TestE2EMaskEndToEnd(t *testing.T) {
	const key = "sk-ant-api03-ZZZsecretVALUE999"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != key {
			t.Errorf("upstream received %q, want the real key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer upstream.Close()

	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, _ := provider.NewRegistry(db)
	if _, err := registry.Save(provider.Provider{ID: "test", Name: "Test", Kind: provider.KindCompatible, BaseURL: upstream.URL, Models: []string{"upstream-model"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mappings, _ := config.NewMappingStore(db)
	if _, err := mappings.Save(config.ModelMapping{ID: "m", ClientModel: "client-model", ProviderID: "test", UpstreamModel: "upstream-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	pool, err := credential.NewPool(db, fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add("test", "主账号", key); err != nil {
		t.Fatal(err)
	}
	server := New(registry, mappings, pool, usage.NewSQLiteTracker(db), fakeKeys{valid: "ar-local"}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ar-local")
	rec := httptest.NewRecorder()
	server.chatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	logs, err := usage.NewSQLiteTracker(db).ListRequestLogs(1, 20, usage.RequestLogFilter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 {
		t.Fatalf("expected one log row, got %d", len(logs.Items))
	}
	item := logs.Items[0]
	t.Logf("credentialId=%q name=%q mask=%q", item.CredentialID, item.CredentialName, item.CredentialMask)
	if item.CredentialMask != "sk****999" {
		t.Fatalf("mask = %q", item.CredentialMask)
	}
	// Scan every column of the row for the plaintext.
	var row string
	if err := db.QueryRow(`SELECT COALESCE(token_id,'')||COALESCE(token_name,'')||COALESCE(provider_id,'')||COALESCE(provider_name,'')||COALESCE(client_model,'')||COALESCE(upstream_model,'')||COALESCE(user_agent,'')||COALESCE(credential_id,'')||COALESCE(credential_name,'')||COALESCE(credential_mask,'') FROM request_logs`).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row, key) {
		t.Fatal("plaintext key present in the log row")
	}

	// The per-credential usage breakdown must carry the mask too.
	stats := usage.NewSQLiteTracker(db).UsageByCredential(nil)
	if len(stats) != 1 || stats[0].Mask != "sk****999" {
		t.Fatalf("breakdown = %+v", stats)
	}
	// And the pool row must hold no plaintext.
	var ref, name, mask string
	if err := db.QueryRow(`SELECT secret_ref,name,mask FROM provider_credentials`).Scan(&ref, &name, &mask); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ref+name+mask, key) {
		t.Fatal("plaintext key present in provider_credentials")
	}
	t.Logf("provider_credentials: ref=%q name=%q mask=%q", ref, name, mask)
}
