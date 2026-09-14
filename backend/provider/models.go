package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// FetchModels loads an OpenAI-compatible provider's model catalog from its
// configured /models endpoint. Credentials are only used for this request.
func FetchModels(ctx context.Context, client *http.Client, kind Kind, baseURL, apiKey string) ([]AvailableModel, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid provider Base URL")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API Key is required to fetch models")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(parsed.String(), "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create model request: %w", err)
	}
	if kind == KindAnthropic {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch models: upstream returned %s", response.Status)
	}

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"data"`
		Models []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Created int64  `json:"created"`
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}

	seen := map[string]AvailableModel{}
	for _, item := range payload.Data {
		if item.ID != "" {
			seen[item.ID] = AvailableModel{ID: item.ID, Created: item.Created}
		}
	}
	for _, item := range payload.Models {
		id := item.ID
		if id == "" {
			id = item.Name
		}
		if id != "" {
			if existing, ok := seen[id]; !ok || item.Created > existing.Created {
				seen[id] = AvailableModel{ID: id, Created: item.Created}
			}
		}
	}
	models := make([]AvailableModel, 0, len(seen))
	for model := range seen {
		models = append(models, seen[model])
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Created != models[j].Created {
			return models[i].Created > models[j].Created
		}
		return models[i].ID < models[j].ID
	})
	return models, nil
}

func decodeAvailableModels(raw string) []AvailableModel {
	var models []AvailableModel
	if json.Unmarshal([]byte(raw), &models) == nil {
		return models
	}
	var legacy []string
	if json.Unmarshal([]byte(raw), &legacy) != nil {
		return nil
	}
	models = make([]AvailableModel, 0, len(legacy))
	for _, model := range legacy {
		models = append(models, AvailableModel{ID: model})
	}
	return models
}
