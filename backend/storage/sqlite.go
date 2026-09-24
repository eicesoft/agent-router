package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
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
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
  api_key_ref TEXT NOT NULL, icon TEXT NOT NULL DEFAULT '', model_prefix TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, models_json TEXT NOT NULL DEFAULT '[]', available_models_json TEXT NOT NULL DEFAULT '[]', dev_id TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS deleted_providers (
  id TEXT PRIMARY KEY
);
-- client_model 刻意不带 UNIQUE：同一个客户端模型名可以挂多条映射（不同提供商），
-- 由 proxy 按顺序 failover。见 backend/config/mappings.go 的 ResolveAll。
CREATE TABLE IF NOT EXISTS model_mappings (
  id TEXT PRIMARY KEY, client_model TEXT NOT NULL, provider_id TEXT NOT NULL,
  upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  aliases_json TEXT NOT NULL DEFAULT '[]',
  input_types_json TEXT NOT NULL DEFAULT '[]',
  input_context_size INTEGER NOT NULL DEFAULT 0,
  output_size INTEGER NOT NULL DEFAULT 0,
  input_price REAL NOT NULL DEFAULT 0,
  output_price REAL NOT NULL DEFAULT 0,
  cache_read_price REAL NOT NULL DEFAULT 0
);
-- 同名链级策略：一条 failover 链共享一个起点规则（默认 failover）。
-- 见 backend/config/mappings.go 的 ChainMode。
CREATE TABLE IF NOT EXISTS model_chain_modes (
  client_model TEXT PRIMARY KEY, mode TEXT NOT NULL
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
  user_agent TEXT NOT NULL DEFAULT '',
  request_body TEXT NOT NULL DEFAULT '', response_body TEXT NOT NULL DEFAULT '',
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_input_tokens INTEGER NOT NULL DEFAULT 0, reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
  success INTEGER NOT NULL, latency_ms INTEGER NOT NULL DEFAULT 0, error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at DESC, id DESC);
-- 覆盖索引：使用统计与日志筛选只需小列，避免扫描含大体积请求/响应体的大表。
CREATE INDEX IF NOT EXISTS idx_request_logs_token ON request_logs(token_id, token_name, input_tokens, output_tokens, cached_input_tokens, reasoning_output_tokens, success);
CREATE INDEX IF NOT EXISTS idx_request_logs_filter ON request_logs(token_id, provider_name, client_model, success);
CREATE TABLE IF NOT EXISTS local_api_keys (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, api_key TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
-- 上游凭据池：每行只保存 Keychain 引用（secret_ref），token 本体仍然绝不落库。
-- 一个 provider 可以有多行，由 backend/credential 决定谁服务当前请求。
CREATE TABLE IF NOT EXISTS provider_credentials (
  id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
  secret_ref TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1,
  weight INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'active',
  last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_provider_credentials_provider ON provider_credentials(provider_id, created_at);`); err != nil {
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
		{"request_logs", "user_agent", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "cached_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"request_logs", "reasoning_output_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"usage_events", "cached_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"usage_events", "reasoning_output_tokens", "INTEGER NOT NULL DEFAULT 0"},
		// 凭据池：providers.credential_mode 记录该 provider 的选择策略；
		// request_logs 记录实际服务的凭据，供 UI 按 key 聚合用量。
		{"providers", "credential_mode", "TEXT NOT NULL DEFAULT 'session'"},
		{"request_logs", "credential_id", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "credential_name", "TEXT NOT NULL DEFAULT ''"},
		// 掩码是唯一落库的上游密钥派生值（如 sk****xyz），供 UI 与日志显示。
		// 它在明文已经在手的位置派生，不额外读 Keychain；见 credential.MaskSecret。
		// request_logs 快照一份，使凭据被删除后历史日志仍可读。
		{"provider_credentials", "mask", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "credential_mask", "TEXT NOT NULL DEFAULT ''"},
		// 映射别名：客户端可用这些名字命中同一条映射，但别名不出现在
		// /v1/models 与生成给 CLI 的模型列表里，仅用于请求路由。
		{"model_mappings", "aliases_json", "TEXT NOT NULL DEFAULT '[]'"},
		// models.dev 目录 id：提供商选择器写入，保存时据此从 api.json 同步模型价格。
		{"providers", "dev_id", "TEXT NOT NULL DEFAULT ''"},
		// 映射的模型元数据：输入模态（text/image/…）与输入上下文、输出大小
		// （tokens，0 表示未设置），仅配置存储与 UI 展示，网关行为不消费。
		{"model_mappings", "input_types_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"model_mappings", "input_context_size", "INTEGER NOT NULL DEFAULT 0"},
		{"model_mappings", "output_size", "INTEGER NOT NULL DEFAULT 0"},
		// 单价（USD / 百万 tokens，默认 0 即未设置）：仅 UI 配置展示，不参与计费。
		{"model_mappings", "input_price", "REAL NOT NULL DEFAULT 0"},
		{"model_mappings", "output_price", "REAL NOT NULL DEFAULT 0"},
		{"model_mappings", "cache_read_price", "REAL NOT NULL DEFAULT 0"},
		// 输入插件对本条请求的压缩差异（估算 token）。plugin_id 为空表示未跑插件。
		// plugin_deltas_json 记录同一次请求里所有生效插件的 before/after/saved。
		{"request_logs", "plugin_id", "TEXT NOT NULL DEFAULT ''"},
		{"request_logs", "plugin_before_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"request_logs", "plugin_after_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"request_logs", "plugin_saved_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"request_logs", "plugin_deltas_json", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, column.table, column.name).Scan(&exists); err != nil {
			db.Close()
			return nil, err
		}
		if exists == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + column.table + ` ADD COLUMN ` + column.name + ` ` + column.ddl); err != nil {
				db.Close()
				return nil, fmt.Errorf("migrate %s.%s: %w", column.table, column.name, err)
			}
		}
	}
	// 补列出来的存量行 input_types_json 是 '[]'：模型默认都有 text 能力，
	// 一次性矫正为 ["text"]，抽屉里才能看到 text 处于勾选状态。
	// 幂等：已是 ["text"] 或用户配置过的行不再匹配。
	if _, err := db.Exec(`UPDATE model_mappings SET input_types_json = '["text"]' WHERE input_types_json = '[]'`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate model_mappings input types: %w", err)
	}
	// 旧库的 model_mappings 带 client_model UNIQUE，挡死了同名多路由。SQLite 不能直接
	// 删约束，只能整表重建；按 sqlite_master 里的建表语句判断，所以幂等。放在补列之后，
	// 重建时 aliases_json 一定已经存在。
	var mappingsDDL string
	if err := db.QueryRow(`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'model_mappings'`).Scan(&mappingsDDL); err != nil {
		db.Close()
		return nil, err
	}
	if strings.Contains(mappingsDDL, "client_model TEXT NOT NULL UNIQUE") {
		if _, err := db.Exec(`
CREATE TABLE model_mappings_rebuilt (
  id TEXT PRIMARY KEY, client_model TEXT NOT NULL, provider_id TEXT NOT NULL,
  upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  aliases_json TEXT NOT NULL DEFAULT '[]',
  input_types_json TEXT NOT NULL DEFAULT '[]',
  input_context_size INTEGER NOT NULL DEFAULT 0,
  output_size INTEGER NOT NULL DEFAULT 0,
  input_price REAL NOT NULL DEFAULT 0,
  output_price REAL NOT NULL DEFAULT 0,
  cache_read_price REAL NOT NULL DEFAULT 0
);
INSERT INTO model_mappings_rebuilt(id, client_model, provider_id, upstream_model, enabled, aliases_json, input_types_json, input_context_size, output_size, input_price, output_price, cache_read_price)
  SELECT id, client_model, provider_id, upstream_model, enabled, aliases_json, input_types_json, input_context_size, output_size, input_price, output_price, cache_read_price FROM model_mappings;
DROP TABLE model_mappings;
ALTER TABLE model_mappings_rebuilt RENAME TO model_mappings;`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate model_mappings client_model unique: %w", err)
		}
	}
	// 使用统计的覆盖索引必须建在补列之后：老库重建前根本没有 credential_id，
	// 内联 DDL 里的 CREATE INDEX IF NOT EXISTS 会在建表后立刻执行而失败。
	// 聚合按「维度 × 路由(provider, model)」出粒度再算费（见 backend/usage/cost.go），
	// 索引必须带上 provider_id/client_model，否则 SQLite 会裸扫 request_logs
	// 的 body 溢出页（每次打开使用情况页抖几百毫秒）。
	// 旧索引列不够宽：CREATE INDEX IF NOT EXISTS 对已存在的窄索引无效，先 DROP。
	if _, err := db.Exec(`
DROP INDEX IF EXISTS idx_request_logs_credential;
DROP INDEX IF EXISTS idx_request_logs_token;
DROP INDEX IF EXISTS idx_usage_events_provider;
DROP INDEX IF EXISTS idx_usage_events_model;
CREATE INDEX IF NOT EXISTS idx_request_logs_credential ON request_logs(credential_id, credential_name, credential_mask, provider_id, client_model, success, input_tokens, output_tokens, cached_input_tokens, reasoning_output_tokens);
CREATE INDEX IF NOT EXISTS idx_request_logs_token ON request_logs(token_id, token_name, provider_id, client_model, success, input_tokens, output_tokens, cached_input_tokens, reasoning_output_tokens);
CREATE INDEX IF NOT EXISTS idx_usage_events_route ON usage_events(provider_id, model, success, input_tokens, output_tokens, cached_input_tokens, reasoning_output_tokens);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate usage indexes: %w", err)
	}
	return db, nil
}
