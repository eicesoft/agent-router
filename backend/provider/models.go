package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// catalogRequest is one candidate URL for a provider's model catalog.
type catalogRequest struct {
	url     string
	headers map[string]string
}

// modelCatalogRequests lists the candidate catalog URLs for a provider Base URL,
// most likely first.
//
// Only OpenAI-compatible services publish a /models route. An Anthropic endpoint
// speaks /v1/messages and has no catalog of its own, so a 404 there is expected:
// gateways that front an Anthropic app with an OpenAI-compatible one (Aliyun
// token-plan, for one) publish the same models on the sibling route instead.
// Those are listed as fallbacks so the picker still works.
func modelCatalogRequests(kind Kind, base *url.URL, apiKey string) []catalogRequest {
	root := strings.TrimRight(base.String(), "/")
	if kind != KindAnthropic {
		return []catalogRequest{{url: root + "/models", headers: map[string]string{"Authorization": "Bearer " + apiKey}}}
	}
	// 候选地址上两套认证头都带上：Anthropic 路由只认 x-api-key，同源的
	// OpenAI 兼容路由通常只认 Bearer，上游会忽略用不上的那个。
	headers := map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
		"Authorization":     "Bearer " + apiKey,
	}
	requests := []catalogRequest{
		{url: root + "/models", headers: headers},
		{url: root + "/v1/models", headers: headers},
	}
	// 阿里云 token-plan 把 Anthropic 应用挂在 /apps/<name> 下，模型目录只在
	// 主机根的 /compatible-mode/v1/models 上，即把 /apps/<name> 整段换成它。
	if segments := strings.Split(strings.Trim(base.Path, "/"), "/"); len(segments) >= 2 {
		sibling := *base
		sibling.Path = "/compatible-mode/v1"
		requests = append(requests, catalogRequest{url: strings.TrimRight(sibling.String(), "/") + "/models", headers: headers})
	}
	return requests
}

// FetchModels loads a provider's model catalog. Credentials are only used for
// this request. Candidates are tried in order and the first one that answers
// wins; 401/403 stops the search because the URL is right and the credential is
// the problem.
func FetchModels(ctx context.Context, client *http.Client, kind Kind, baseURL, apiKey string) ([]AvailableModel, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid provider Base URL")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API Key is required to fetch models")
	}

	candidates := modelCatalogRequests(kind, parsed, apiKey)
	attempts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		path := candidate.url
		if u, err := url.Parse(candidate.url); err == nil {
			path = u.Path
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate.url, nil)
		if err != nil {
			return nil, fmt.Errorf("create model request: %w", err)
		}
		for name, value := range candidate.headers {
			req.Header.Set(name, value)
		}
		response, err := client.Do(req)
		if err != nil {
			if len(candidates) > 1 && ctx.Err() == nil {
				attempts = append(attempts, path+" → "+err.Error())
				continue
			}
			return nil, fmt.Errorf("fetch models: %w", err)
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			models, err := decodeModelCatalog(response.Body)
			response.Body.Close()
			if err != nil {
				return nil, err
			}
			return models, nil
		}
		status, code := response.Status, response.StatusCode
		response.Body.Close()
		attempts = append(attempts, path+" → "+status)
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			break
		}
	}
	return nil, fmt.Errorf("fetch models: %s", strings.Join(attempts, ", "))
}

// decodeModelCatalog accepts both the OpenAI (data[]) and the alternative
// models[] shapes an upstream may answer with.
func decodeModelCatalog(body io.Reader) ([]AvailableModel, error) {
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
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
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
