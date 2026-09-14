package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
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
