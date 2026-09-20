package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// 旧库的 model_mappings 带 client_model UNIQUE，挡死了「同名多提供商」这条路由链。
// 迁移要整表重建，因此必须验证三件事：约束确实消失、旧行（含别名）原样保留、
// 重开库不重复迁移也不报错。
func TestMigrateDropsClientModelUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "CREATE TABLE model_mappings (id TEXT PRIMARY KEY, client_model TEXT NOT NULL UNIQUE, provider_id TEXT NOT NULL, upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, aliases_json TEXT NOT NULL DEFAULT '[]')"
	if _, err := raw.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	aliases := fmt.Sprintf("[%q]", "legacy-alias")
	if _, err := raw.Exec("INSERT INTO model_mappings(id, client_model, provider_id, upstream_model, enabled, aliases_json) VALUES('old', 'gpt-4.1', 'openai', 'gpt-4.1', 1, '" + aliases + "')"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	var ddl string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE name = 'model_mappings'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ddl, "UNIQUE") {
		t.Fatalf("client_model UNIQUE survived migration: %s", ddl)
	}
	var id, clientModel, stored string
	if err := db.QueryRow("SELECT id, client_model, aliases_json FROM model_mappings").Scan(&id, &clientModel, &stored); err != nil {
		t.Fatal(err)
	}
	if id != "old" || clientModel != "gpt-4.1" || !strings.Contains(stored, "legacy-alias") {
		t.Fatalf("existing row did not survive migration: %s %s %s", id, clientModel, stored)
	}
	// 迁移的目的：同名第二条能写进去。
	if _, err := db.Exec("INSERT INTO model_mappings(id, client_model, provider_id, upstream_model, enabled, aliases_json) VALUES('new', 'gpt-4.1', 'azure', 'gpt-4.1-deployment', 1, '[]')"); err != nil {
		t.Fatalf("same client_model on another provider was rejected: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 幂等：重开一次不应再迁移、不应丢行。
	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.QueryRow("SELECT COUNT(*) FROM model_mappings").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 mappings after reopen, got %d", count)
	}
}
