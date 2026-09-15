package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-router/backend/config"
	"agent-router/backend/provider"
	"agent-router/backend/storage"
	"agent-router/backend/usage"
)

// The UI toggle calls Start/Close repeatedly. After a stop, the gateway must be
// observable as stopped and a later start must listen again.
func TestProxyLifecycleRestartsAfterClose(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := config.NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	s := New(registry, mappings, fakeSecrets{}, usage.NewSQLiteTracker(db), fakeKeys{})

	addr := "127.0.0.1:19301"
	if err := s.Start(addr); err != nil {
		t.Fatalf("initial start: %v", err)
	}
	if !s.Running() {
		t.Fatal("Running() = false right after Start")
	}
	if conn, err := net.Dial("tcp", addr); err != nil {
		t.Fatalf("port not listening after Start: %v", err)
	} else {
		_ = conn.Close()
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if s.Running() {
		t.Error("Running() = true after Close")
	}

	if err := s.Start(addr); err != nil {
		t.Fatalf("restart: %v", err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("port not listening after restart: %v", err)
	}
	_ = conn.Close()
	_ = s.Close()
}
