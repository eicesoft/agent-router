package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withDevCatalogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// macOS UserConfigDir uses $HOME/Library/Application Support, not XDG.
	// Point HOME at the temp tree so both platforms stay inside the test dir.
	t.Setenv("HOME", dir)
	return dir
}

func TestRefreshAndListDevCatalog(t *testing.T) {
	withDevCatalogDir(t)
	body := `{
	  "alibaba-token-plan-cn": {
	    "id": "alibaba-token-plan-cn",
	    "env": ["ALIBABA_TOKEN_PLAN_API_KEY"],
	    "npm": "@ai-sdk/openai-compatible",
	    "api": "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
	    "name": "Alibaba Token Plan (China)",
	    "doc": "https://www.alibabacloud.com/help/zh/model-studio/token-plan-overview",
	    "models": {"m": {"id": "m"}}
	  },
	  "zeta": {
	    "id": "zeta",
	    "api": "https://zeta.example/v1",
	    "name": "Zeta",
	    "doc": "https://zeta.example"
	  }
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	// Redirect the fetch URL to the test server via a custom client transport
	// is overkill: call the download path through a rewritten URL helper test.
	if err := refreshDevCatalogFrom(context.Background(), server.Client(), server.URL+"/api.json"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	path, err := DevCatalogPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "alibaba-token-plan-cn") || !strings.Contains(string(raw), `"models"`) {
		t.Fatalf("cache should keep the raw body, got %q", raw)
	}
	items := ListDevCatalog()
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}
	if items[0].Name != "Alibaba Token Plan (China)" || items[1].Name != "Zeta" {
		t.Fatalf("order: %+v", items)
	}
	if items[0].API == "" || items[0].Doc == "" {
		t.Fatalf("missing fields: %+v", items[0])
	}
}

func TestListDevCatalogMissingFile(t *testing.T) {
	withDevCatalogDir(t)
	if items := ListDevCatalog(); len(items) != 0 {
		t.Fatalf("want empty, got %d", len(items))
	}
}

func TestDevModelPricesReadsCostBlocks(t *testing.T) {
	withDevCatalogDir(t)
	body := `{
	  "openai": {
	    "id": "openai",
	    "name": "OpenAI",
	    "models": {
	      "gpt-4.1": {"id": "gpt-4.1", "cost": {"input": 2, "output": 8, "cache_read": 0.5}},
	      "no-cost": {"id": "no-cost"},
	      "partial": {"id": "partial", "cost": {"input": 1.5}}
	    }
	  },
	  "other": {"id": "other", "name": "Other", "models": {"m": {"cost": {"input": 9}}}}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	if err := refreshDevCatalogFrom(context.Background(), server.Client(), server.URL+"/api.json"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	prices := DevModelPrices("openai")
	if len(prices) != 2 {
		t.Fatalf("prices=%#v", prices)
	}
	gpt := prices["gpt-4.1"]
	if gpt.Input != 2 || gpt.Output != 8 || gpt.CacheRead != 0.5 {
		t.Fatalf("gpt-4.1 cost: %#v", gpt)
	}
	if partial := prices["partial"]; partial.Input != 1.5 || partial.Output != 0 {
		t.Fatalf("partial cost: %#v", partial)
	}
	if _, ok := prices["no-cost"]; ok {
		t.Fatal("model without cost should be omitted")
	}
	if DevModelPrices("") != nil {
		t.Fatal("empty dev id should skip")
	}
	if DevModelPrices("missing") != nil {
		t.Fatal("unknown provider should be nil")
	}
}

func TestRefreshDevCatalogKeepsCacheOnFailure(t *testing.T) {
	withDevCatalogDir(t)
	path, err := DevCatalogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"keep":{"id":"keep","name":"Keep","api":"https://x","doc":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refreshDevCatalogFrom(context.Background(), http.DefaultClient, "http://127.0.0.1:1/api.json"); err == nil {
		t.Fatal("want error")
	}
	items := ListDevCatalog()
	if len(items) != 1 || items[0].ID != "keep" {
		t.Fatalf("cache should survive: %+v", items)
	}
}

func TestRefreshAndListDevModels(t *testing.T) {
	withDevCatalogDir(t)
	body := `{
	  "openai/gpt-4.1": {
	    "id": "openai/gpt-4.1",
	    "name": "GPT-4.1",
	    "description": "Flagship model",
	    "reasoning": false,
	    "tool_call": true,
	    "structured_output": true,
	    "release_date": "2025-04-14",
	    "modalities": {"input": ["text", "image"], "output": ["text"]},
	    "limit": {"context": 1047576, "output": 32768}
	  },
	  "anthropic/claude-sonnet-4-5": {
	    "id": "anthropic/claude-sonnet-4-5",
	    "name": "Claude Sonnet 4.5",
	    "description": "Balanced Claude model",
	    "reasoning": true,
	    "tool_call": true,
	    "structured_output": false,
	    "release_date": "2025-09-29",
	    "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]},
	    "limit": {"context": 200000, "output": 64000}
	  }
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	path, err := DevModelsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshDevFile(context.Background(), server.Client(), server.URL+"/models.json", path); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "openai/gpt-4.1") {
		t.Fatalf("cache should keep the raw body, got %q", raw)
	}
	items := ListDevModels()
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}
	// Sorted by display name: Claude before GPT.
	if items[0].Name != "Claude Sonnet 4.5" || items[1].Name != "GPT-4.1" {
		t.Fatalf("order: %+v", items)
	}
	if !items[0].Reasoning || items[0].StructuredOutput {
		t.Fatalf("capability flags: %+v", items[0])
	}
	if items[1].Limit.Context != 1047576 || items[1].Limit.Output != 32768 {
		t.Fatalf("limits: %+v", items[1])
	}
	if len(items[0].Modalities.Input) != 3 || items[0].Modalities.Input[2] != "pdf" {
		t.Fatalf("modalities: %+v", items[0].Modalities)
	}
}

func TestListDevModelsMissingFile(t *testing.T) {
	withDevCatalogDir(t)
	if items := ListDevModels(); len(items) != 0 {
		t.Fatalf("want empty, got %d", len(items))
	}
}

func TestRefreshDevModelsKeepsCacheOnFailure(t *testing.T) {
	withDevCatalogDir(t)
	path, err := DevModelsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"keep":{"id":"keep","name":"Keep","description":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refreshDevFile(context.Background(), http.DefaultClient, "http://127.0.0.1:1/models.json", path); err == nil {
		t.Fatal("want error")
	}
	items := ListDevModels()
	if len(items) != 1 || items[0].ID != "keep" {
		t.Fatalf("cache should survive: %+v", items)
	}
}
