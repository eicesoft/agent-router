package provider

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agent-router/backend/storage"
)

func TestFetchIconDiscoveryAndPersistence(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aSAAAAABJRU5ErkJggg==")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("credentials or query forwarded")
		}
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<html><link href='/assets/icon.png' rel='shortcut icon'></html>`))
		case "/assets/icon.png":
			w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	icon, err := FetchIcon(context.Background(), server.Client(), server.URL+"/v1?key=private")
	if err != nil || !strings.HasPrefix(icon, "data:image/png;base64,") {
		t.Fatalf("icon=%q err=%v", icon, err)
	}
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Save(Provider{ID: "custom", Name: "Custom", BaseURL: server.URL, Icon: icon})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err = NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := registry.Get("custom")
	if saved.Icon != icon {
		t.Fatal("icon did not survive database reopen")
	}
}

func TestFetchIconFallbackAndFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		success    bool
	}{
		{"ico", "\x00\x00\x01\x00\x01\x00", true},
		{"svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg>`, true},
		{"html", "<html>not an icon</html>", false},
		{"oversized", strings.Repeat("x", (1<<20)+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/favicon.ico" {
					w.Write([]byte(test.body))
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			_, err := FetchIcon(context.Background(), server.Client(), server.URL+"/v1")
			if (err == nil) != test.success {
				t.Fatalf("err=%v", err)
			}
		})
	}
	for _, address := range []string{"file:///tmp/icon", "https://user:secret@example.com", "invalid"} {
		if _, err := FetchIcon(context.Background(), http.DefaultClient, address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FetchIcon(ctx, http.DefaultClient, "https://example.com"); err == nil {
		t.Fatal("ignored cancellation")
	}
}
