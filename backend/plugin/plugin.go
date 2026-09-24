// Package plugin persists feature-plugin state (enable flags and cumulative
// token stats) and hosts the built-in plugin catalog. Request rewriting lives
// in the proxy; this package is the registry the UI and proxy both read.
package plugin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"unicode"
)

// Kind classifies where a plugin sits on the request path.
type Kind string

const (
	KindInput  Kind = "input"
	KindOutput Kind = "output"
)

// IDs of built-in plugins.
const (
	IDSessionStrip = "session_strip"
	IDCaveman      = "caveman"
)

// Info is the UI-facing row for one plugin.
type Info struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        Kind   `json:"kind"`
	HasConfig   bool   `json:"hasConfig"`
	Enabled     bool   `json:"enabled"`
	// Config is the editable settings map (e.g. caveman level). Empty for
	// plugins without configuration.
	Config map[string]string `json:"config"`
	// InputTokens / OutputTokens are cumulative estimates across requests that
	// ran while the plugin was enabled. OutputTokens is the post-transform
	// count ("压缩后"); SavedTokens is the cumulative difference (输入−压缩后);
	// CompressionRate is 1 - output/input, 0 when no input.
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	SavedTokens     int64   `json:"savedTokens"`
	CompressionRate float64 `json:"compressionRate"`
	// Response-side observation while this plugin was applied (upstream
	// completion tokens). Baseline* accumulates the same metric on successful
	// requests where the plugin did NOT run, so the UI can compare averages
	// without a synthetic control harness.
	ResponseTokens    int64   `json:"responseTokens"`
	ResponseCount     int64   `json:"responseCount"`
	BaselineTokens    int64   `json:"baselineTokens"`
	BaselineCount     int64   `json:"baselineCount"`
	ResponseAvg       float64 `json:"responseAvg"`
	BaselineAvg       float64 `json:"baselineAvg"`
	OutputSavingsRate float64 `json:"outputSavingsRate"`
}

// Delta is one request's plugin effect for request_logs: how many estimated
// tokens the transform removed. Zero means the plugin did not run.
type Delta struct {
	PluginID string
	Before   int64
	After    int64
}

// Saved is the compression difference (输入 − 压缩后), floored at 0.
func (d Delta) Saved() int64 {
	if d.Before <= d.After {
		return 0
	}
	return d.Before - d.After
}

// Applied reports whether an input plugin actually ran on this request.
func (d Delta) Applied() bool {
	return d.PluginID != ""
}

// Definition is the static catalog entry; runtime enabled/stats come from SQLite.
type Definition struct {
	ID          string
	Name        string
	Description string
	Kind        Kind
	HasConfig   bool
}

// catalog is the built-in list. Missing rows in plugins default to off.
var catalog = []Definition{
	{
		ID:          IDSessionStrip,
		Name:        "会话去重",
		Description: "跨轮次去除同一请求中先前已出现的文本区块；无损——只省略重复，不摘要、不丢弃新内容。",
		Kind:        KindInput,
		HasConfig:   false,
	},
	{
		ID:   IDCaveman,
		Name: "Caveman",
		Description: "按级别（Lite/Full/Ultra/文*）压缩请求里的长工具结果/日志/JSON，" +
			"并注入风格规则让模型回话更短；规则已内嵌，免安装。统计含输入净节省与输出均值对比。",
		Kind:      KindInput,
		HasConfig: true,
	},
}

// Store reads/writes plugin enable flags and stats.
type Store struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewStore creates the plugins table if needed and returns the store.
func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS plugins (
  id TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  config_json TEXT NOT NULL DEFAULT '{}',
  response_tokens INTEGER NOT NULL DEFAULT 0,
  response_count INTEGER NOT NULL DEFAULT 0,
  baseline_tokens INTEGER NOT NULL DEFAULT 0,
  baseline_count INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		return nil, fmt.Errorf("create plugins table: %w", err)
	}
	// Older installs miss later columns; CREATE IF NOT EXISTS will not add them.
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"config_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"response_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"response_count", "INTEGER NOT NULL DEFAULT 0"},
		{"baseline_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"baseline_count", "INTEGER NOT NULL DEFAULT 0"},
	} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('plugins') WHERE name = ?`, col.name).Scan(&exists); err != nil {
			return nil, fmt.Errorf("probe plugins.%s: %w", col.name, err)
		}
		if exists == 0 {
			if _, err := db.Exec(`ALTER TABLE plugins ADD COLUMN ` + col.name + ` ` + col.ddl); err != nil {
				return nil, fmt.Errorf("migrate plugins.%s: %w", col.name, err)
			}
		}
	}
	return &Store{db: db}, nil
}

// List merges the catalog with stored enable flags and stats.
func (s *Store) List() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, 0, len(catalog))
	for _, def := range catalog {
		info := Info{
			ID:          def.ID,
			Name:        def.Name,
			Description: def.Description,
			Kind:        def.Kind,
			HasConfig:   def.HasConfig,
			Config:      map[string]string{},
		}
		var enabled int
		var configJSON string
		err := s.db.QueryRow(
			`SELECT enabled, input_tokens, output_tokens, config_json,
			        response_tokens, response_count, baseline_tokens, baseline_count
			 FROM plugins WHERE id = ?`,
			def.ID,
		).Scan(&enabled, &info.InputTokens, &info.OutputTokens, &configJSON,
			&info.ResponseTokens, &info.ResponseCount, &info.BaselineTokens, &info.BaselineCount)
		if err == nil {
			info.Enabled = enabled != 0
			if configJSON != "" {
				_ = json.Unmarshal([]byte(configJSON), &info.Config)
				if info.Config == nil {
					info.Config = map[string]string{}
				}
			}
		}
		if def.ID == IDCaveman {
			info.Config["level"] = NormalizeCavemanLevel(info.Config["level"])
		}
		info.CompressionRate = rate(info.InputTokens, info.OutputTokens)
		info.SavedTokens = info.InputTokens - info.OutputTokens
		if info.SavedTokens < 0 {
			info.SavedTokens = 0
		}
		if info.ResponseCount > 0 {
			info.ResponseAvg = float64(info.ResponseTokens) / float64(info.ResponseCount)
		}
		if info.BaselineCount > 0 {
			info.BaselineAvg = float64(info.BaselineTokens) / float64(info.BaselineCount)
		}
		if info.ResponseAvg > 0 && info.BaselineAvg > 0 && info.BaselineAvg > info.ResponseAvg {
			info.OutputSavingsRate = 1 - info.ResponseAvg/info.BaselineAvg
		}
		out = append(out, info)
	}
	return out
}

// Enabled reports whether the plugin should run. Missing rows default to off.
func (s *Store) Enabled(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var enabled int
	err := s.db.QueryRow(`SELECT enabled FROM plugins WHERE id = ?`, id).Scan(&enabled)
	return err == nil && enabled != 0
}

// SetEnabled upserts the enable flag for a known plugin id.
func (s *Store) SetEnabled(id string, enabled bool) error {
	if !known(id) {
		return fmt.Errorf("unknown plugin %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	flag := 0
	if enabled {
		flag = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO plugins (id, enabled) VALUES (?, ?)
		 ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled`,
		id, flag,
	)
	if err != nil {
		return fmt.Errorf("set plugin %s: %w", id, err)
	}
	return nil
}

// Record adds one request's pre/post token estimates to the plugin totals.
func (s *Store) Record(id string, before, after int64) {
	if before < 0 || after < 0 || !known(id) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(
		`INSERT INTO plugins (id, enabled, input_tokens, output_tokens) VALUES (?, 0, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   input_tokens = input_tokens + excluded.input_tokens,
		   output_tokens = output_tokens + excluded.output_tokens`,
		id, before, after,
	)
}

// RecordResponse accumulates upstream completion tokens for a request where
// the plugin was applied (output-side observation).
func (s *Store) RecordResponse(id string, outputTokens int64) {
	if outputTokens <= 0 || !known(id) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(
		`INSERT INTO plugins (id, enabled, response_tokens, response_count) VALUES (?, 0, ?, 1)
		 ON CONFLICT(id) DO UPDATE SET
		   response_tokens = response_tokens + excluded.response_tokens,
		   response_count = response_count + 1`,
		id, outputTokens,
	)
}

// RecordBaseline accumulates upstream completion tokens for a successful
// request where the plugin did NOT run — the comparison arm for averages.
func (s *Store) RecordBaseline(id string, outputTokens int64) {
	if outputTokens <= 0 || !known(id) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(
		`INSERT INTO plugins (id, enabled, baseline_tokens, baseline_count) VALUES (?, 0, ?, 1)
		 ON CONFLICT(id) DO UPDATE SET
		   baseline_tokens = baseline_tokens + excluded.baseline_tokens,
		   baseline_count = baseline_count + 1`,
		id, outputTokens,
	)
}

// Config returns the stored settings map for a known plugin (never nil).
func (s *Store) Config(id string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configLocked(id)
}

// SetConfig replaces one plugin's settings map. Unknown ids error; values are
// flat string maps so the Wails binding stays simple.
func (s *Store) SetConfig(id string, cfg map[string]string) error {
	if !known(id) {
		return fmt.Errorf("unknown plugin %q", id)
	}
	if cfg == nil {
		cfg = map[string]string{}
	}
	if id == IDCaveman {
		cfg["level"] = NormalizeCavemanLevel(cfg["level"])
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode plugin %s config: %w", id, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(
		`INSERT INTO plugins (id, enabled, config_json) VALUES (?, 0, ?)
		 ON CONFLICT(id) DO UPDATE SET config_json = excluded.config_json`,
		id, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("set plugin %s config: %w", id, err)
	}
	return nil
}

func (s *Store) configLocked(id string) map[string]string {
	out := map[string]string{}
	var configJSON string
	err := s.db.QueryRow(`SELECT config_json FROM plugins WHERE id = ?`, id).Scan(&configJSON)
	if err != nil || configJSON == "" {
		if id == IDCaveman {
			out["level"] = DefaultCavemanLevel
		}
		return out
	}
	if err := json.Unmarshal([]byte(configJSON), &out); err != nil || out == nil {
		out = map[string]string{}
	}
	if id == IDCaveman {
		out["level"] = NormalizeCavemanLevel(out["level"])
	}
	return out
}

// CavemanLevel is the configured intensity for the caveman plugin.
func (s *Store) CavemanLevel() string {
	return NormalizeCavemanLevel(s.Config(IDCaveman)["level"])
}

func known(id string) bool {
	for _, def := range catalog {
		if def.ID == id {
			return true
		}
	}
	return false
}

func rate(before, after int64) float64 {
	if before <= 0 {
		return 0
	}
	saved := float64(before - after)
	if saved < 0 {
		return 0
	}
	return saved / float64(before)
}

// EstimateTokens approximates token count for plugin statistics only (not
// billing). CJK characters count as one token each; everything else averages
// four runes per token — good enough for a compression ratio in the UI.
func EstimateTokens(s string) int64 {
	if s == "" {
		return 0
	}
	var cjk, other int64
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	tokens := cjk + (other+3)/4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3040 && r <= 0x30FF) || // kana
		(r >= 0xAC00 && r <= 0xD7AF) // hangul
}
