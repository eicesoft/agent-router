// Package settings persists small application-wide preferences (gateway
// address, UI theme) in the SQLite database as a key/value table.
package settings

import (
	"database/sql"
	"fmt"
	"strconv"
)

const (
	KeyHost         = "gateway_host"
	KeyPort         = "gateway_port"
	KeyTheme        = "theme"
	KeyDefaultModel = "default_model"
)

// Settings is the flat view of every stored preference. Port is the gateway
// listen port, Host the bind address ("127.0.0.1" or "0.0.0.0"), Theme the UI
// color scheme, and DefaultModel the client model used when no explicit
// selection exists.
type Settings struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Theme        string `json:"theme"`
	DefaultModel string `json:"defaultModel"`
}

// Defaults applied when a key is missing from the store, matching the values
// hardcoded in app.go before settings existed.
var Defaults = Settings{Host: "127.0.0.1", Port: 9400, Theme: "light"}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY, value TEXT NOT NULL
)`); err != nil {
		return nil, fmt.Errorf("create settings table: %w", err)
	}
	return &Store{db: db}, nil
}

// Get returns the stored settings, falling back to Defaults for absent keys.
func (s *Store) Get() (Settings, error) {
	out := Defaults
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return out, fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return out, fmt.Errorf("scan setting: %w", err)
		}
		switch key {
		case KeyHost:
			out.Host = value
		case KeyPort:
			port, err := strconv.Atoi(value)
			if err == nil {
				out.Port = port
			}
		case KeyTheme:
			out.Theme = value
		case KeyDefaultModel:
			out.DefaultModel = value
		}
	}
	return out, rows.Err()
}

// Save upserts every non-zero field of in.
func (s *Store) Save(in Settings) error {
	for _, kv := range []struct{ key, value string }{
		{KeyHost, in.Host},
		{KeyPort, strconv.Itoa(in.Port)},
		{KeyTheme, in.Theme},
		{KeyDefaultModel, in.DefaultModel},
	} {
		if kv.value == "" || kv.value == "0" {
			continue
		}
		if _, err := s.db.Exec(
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			kv.key, kv.value,
		); err != nil {
			return fmt.Errorf("save setting %s: %w", kv.key, err)
		}
	}
	return nil
}

// Address is the "host:port" listen address for the gateway.
func (s Settings) Address() string {
	host := s.Host
	if host == "" {
		host = Defaults.Host
	}
	port := s.Port
	if port == 0 {
		port = Defaults.Port
	}
	return fmt.Sprintf("%s:%d", host, port)
}
