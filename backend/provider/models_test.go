package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestFetchModelsLoadsOpenAICompatibleCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1","created":100},{"id":"gpt-4o-mini","created":200}]}`))
	}))
	defer server.Close()

	models, err := FetchModels(context.Background(), server.Client(), KindCompatible, server.URL+"/v1", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if expected := []AvailableModel{{ID: "gpt-4o-mini", Created: 200}, {ID: "gpt-4.1", Created: 100}}; !reflect.DeepEqual(models, expected) {
		t.Fatalf("models = %#v, want %#v", models, expected)
	}
}

// Anthropic 应用本身没有模型目录：/models 404，得回落到同源的 OpenAI 兼容路由。
func TestFetchModelsFallsBackToSiblingCatalogForAnthropic(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path != "/compatible-mode/v1/models" {
			http.NotFound(w, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3","created":100}]}`))
	}))
	defer server.Close()

	models, err := FetchModels(context.Background(), server.Client(), KindAnthropic, server.URL+"/apps/anthropic", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if expected := []AvailableModel{{ID: "glm-5.3", Created: 100}}; !reflect.DeepEqual(models, expected) {
		t.Fatalf("models = %#v, want %#v", models, expected)
	}
	want := []string{"/apps/anthropic/models", "/apps/anthropic/v1/models", "/compatible-mode/v1/models"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// 认证失败说明地址是对的、Key 有问题，不该再拿同一个 Key 去试别的地址。
func TestFetchModelsStopsAtUnauthorized(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := FetchModels(context.Background(), server.Client(), KindAnthropic, server.URL+"/apps/anthropic", "sk-bad")
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := []string{"/apps/anthropic/models"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %q, want the 401 status", err)
	}
}

// 全部候选地址都 404 时，报错要把试过的路径列出来，否则用户只看到一句 404。
func TestFetchModelsReportsEveryAttemptedCatalogPath(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, err := FetchModels(context.Background(), server.Client(), KindAnthropic, server.URL+"/apps/anthropic", "sk-test")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, path := range []string{"/apps/anthropic/models", "/apps/anthropic/v1/models", "/compatible-mode/v1/models"} {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("error = %q, want it to mention %s", err, path)
		}
	}
}

// OpenAI 兼容服务不回落：只有 /models 一个候选地址。
func TestFetchModelsDoesNotProbeFallbacksForCompatibleKind(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		http.NotFound(w, request)
	}))
	defer server.Close()

	_, err := FetchModels(context.Background(), server.Client(), KindCompatible, server.URL+"/v1", "sk-test")
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := []string{"/v1/models"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}
