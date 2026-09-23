package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DevCatalogURL is the public models.dev provider index. The raw response is
// cached on disk so the picker can render offline after the first fetch.
const DevCatalogURL = "https://models.dev/api.json"

// DevModelsURL is the models.dev model metadata index (capability picker).
const DevModelsURL = "https://models.dev/models.json"

// DevProvider is one entry from models.dev. Only the fields the picker needs
// are decoded; models are intentionally dropped (and never re-serialized).
type DevProvider struct {
	ID   string   `json:"id"`
	Env  []string `json:"env"`
	NPM  string   `json:"npm"`
	API  string   `json:"api"`
	Name string   `json:"name"`
	Doc  string   `json:"doc"`
}

// DevModelLimit is the context/output window from models.dev.
type DevModelLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

// DevModelModalities lists accepted input and produced output shapes.
type DevModelModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// DevModel is one entry from models.dev/models.json — capability metadata
// only. Benchmarks, links, and weights are dropped on decode.
type DevModel struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Description      string             `json:"description"`
	Reasoning        bool               `json:"reasoning"`
	ToolCall         bool               `json:"tool_call"`
	StructuredOutput bool               `json:"structured_output"`
	ReleaseDate      string             `json:"release_date"`
	Modalities       DevModelModalities `json:"modalities"`
	Limit            DevModelLimit      `json:"limit"`
}

// DevModelCost is one model's USD / 1M token prices from api.json. Missing
// fields decode as 0 (未设置), matching ModelMapping's price convention.
type DevModelCost struct {
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	CacheRead float64 `json:"cache_read"`
}

// devCatalogEntry is a partial api.json row: only the per-model cost block
// this path needs, so the rest of each model never enters memory as typed data.
type devCatalogEntry struct {
	Models map[string]struct {
		Cost *DevModelCost `json:"cost"`
	} `json:"models"`
}

var (
	devRefreshMu sync.Mutex
)

// DevCatalogPath is where the raw api.json body is stored beside the app DB.
func DevCatalogPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	return filepath.Join(dir, "AgentRouter", "models.dev.json"), nil
}

// DevModelsPath is where the raw models.json body is stored beside the app DB.
func DevModelsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	return filepath.Join(dir, "AgentRouter", "models.dev.models.json"), nil
}

// RefreshDevCatalog downloads models.dev/api.json and replaces the local
// cache. A failed fetch leaves the previous file intact so the picker still
// has something to show.
func RefreshDevCatalog(ctx context.Context, client *http.Client) error {
	return refreshDevCatalogFrom(ctx, client, DevCatalogURL)
}

// RefreshDevModels downloads models.dev/models.json and replaces the local
// cache. Failures keep the previous file so the capability picker still works.
func RefreshDevModels(ctx context.Context, client *http.Client) error {
	path, err := DevModelsPath()
	if err != nil {
		return err
	}
	return refreshDevFile(ctx, client, DevModelsURL, path)
}

func refreshDevCatalogFrom(ctx context.Context, client *http.Client, url string) error {
	path, err := DevCatalogPath()
	if err != nil {
		return err
	}
	return refreshDevFile(ctx, client, url, path)
}

func refreshDevFile(ctx context.Context, client *http.Client, url, path string) error {
	devRefreshMu.Lock()
	defer devRefreshMu.Unlock()
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create models.dev request: %w", err)
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch models.dev: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fetch models.dev: %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if err != nil {
		return fmt.Errorf("read models.dev body: %w", err)
	}
	if len(body) > 16<<20 {
		return fmt.Errorf("models.dev response too large")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("decode models.dev response: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create models.dev cache directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write models.dev cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace models.dev cache: %w", err)
	}
	return nil
}

// ListDevCatalog reads the cached api.json and returns its entries sorted by
// display name. A missing or corrupt cache is an empty list, not an error —
// the UI already has a background refresh path.
func ListDevCatalog() []DevProvider {
	path, err := DevCatalogPath()
	if err != nil {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]DevProvider
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	out := make([]DevProvider, 0, len(raw))
	for _, item := range raw {
		if item.Name == "" && item.ID != "" {
			item.Name = item.ID
		}
		if item.Name == "" {
			continue
		}
		if item.ID == "" {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if left, right := lower(out[i].Name), lower(out[j].Name); left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// DevModelPrices returns model ID → list prices for one models.dev provider
// entry in the cached api.json. A missing provider, missing cache, or models
// without a cost block yield no entry — callers treat that as 未同步.
func DevModelPrices(devID string) map[string]DevModelCost {
	if devID == "" {
		return nil
	}
	path, err := DevCatalogPath()
	if err != nil {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]devCatalogEntry
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	entry, ok := raw[devID]
	if !ok {
		return nil
	}
	out := make(map[string]DevModelCost, len(entry.Models))
	for id, model := range entry.Models {
		if model.Cost == nil {
			continue
		}
		out[id] = *model.Cost
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ListDevModels reads the cached models.json and returns its entries sorted by
// display name. A missing or corrupt cache is an empty list, not an error —
// the UI already has a background refresh path.
func ListDevModels() []DevModel {
	path, err := DevModelsPath()
	if err != nil {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]DevModel
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	out := make([]DevModel, 0, len(raw))
	for _, item := range raw {
		if item.Name == "" && item.ID != "" {
			item.Name = item.ID
		}
		if item.Name == "" || item.ID == "" {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if left, right := lower(out[i].Name), lower(out[j].Name); left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
