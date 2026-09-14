package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Open creates the private application database. Upstream provider secrets
// deliberately do not live here: only their Keychain reference is persisted.
// Local gateway API keys are the deliberate exception and are stored here in
// plaintext so the local proxy can verify clients without OS Keychain access.
func Open() (*sql.DB, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "AgentRouter", "agent-router.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return OpenPath(path)
}

func OpenPath(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS providers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, base_url TEXT NOT NULL,
  api_key_ref TEXT NOT NULL, icon TEXT NOT NULL DEFAULT '', model_prefix TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, models_json TEXT NOT NULL DEFAULT '[]', available_models_json TEXT NOT NULL DEFAULT '[]', updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS deleted_providers (
  id TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS model_mappings (
  id TEXT PRIMARY KEY, client_model TEXT NOT NULL UNIQUE, provider_id TEXT NOT NULL,
  upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS usage_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, provider_id TEXT NOT NULL,
  model TEXT NOT NULL, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens INTEGER NOT NULL DEFAULT 0, reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
  success INTEGER NOT NULL, latency_ms INTEGER NOT NULL DEFAULT 0, error_message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS request_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL,
  token_id TEXT NOT NULL DEFAULT '', token_name TEXT NOT NULL DEFAULT '',
  provider_id TEXT NOT NULL, provider_name TEXT NOT NULL DEFAULT '', client_model TEXT NOT NULL, upstream_model TEXT NOT NULL,
  request_body TEXT NOT NULL DEFAULT '', response_body TEXT NOT NULL DEFAULT '',
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens INTEGER NOT NULL DEFAULT 0, reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
  success INTEGER NOT NULL, latency_ms INTEGER NOT NULL DEFAULT 0, error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS local_api_keys (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, api_key TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	var iconColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('providers') WHERE name = 'icon'`).Scan(&iconColumn); err != nil {
		db.Close()
		return nil, err
	}
	if iconColumn == 0 {
		if _, err := db.Exec(`ALTER TABLE providers ADD COLUMN icon TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate provider icon: %w", err)
		}
	}
	var availableModelsColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('providers') WHERE name = 'available_models_json'`).Scan(&availableModelsColumn); err != nil {
		db.Close()
		return nil, err
	}
	if availableModelsColumn == 0 {
		if _, err := db.Exec(`ALTER TABLE providers ADD COLUMN available_models_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate provider available models: %w", err)
		}
	}
	var modelPrefixColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('providers') WHERE name = 'model_prefix'`).Scan(&modelPrefixColumn); err != nil {
		db.Close()
		return nil, err
	}
	if modelPrefixColumn == 0 {
		if _, err := db.Exec(`ALTER TABLE providers ADD COLUMN model_prefix TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate provider model prefix: %w", err)
		}
	}
	for _, column := range []struct {
		table string
		name  string
		ddl   string
	}{
		{"request_logs", "token_id", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "token_name", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "provider_name", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "cached_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"request_logs", "reasoning_output_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"usage_events", "cached_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"usage_events", "reasoning_output_tokens", "INTEGER NOT NULL DEFAULT 0"},
	} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, column.table, column.name).Scan(&exists); err != nil {
			db.Close()
			return nil, err
		}
		if exists == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + column.table + ` ADD COLUMN ` + column.name + ` ` + column.ddl); err != nil {
				db.Close()
				return nil, fmt.Errorf("migrate request log %s: %w", column.name, err)
			}
		}
	}
	return db, nil
}
