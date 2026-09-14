package apikey

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

type Key struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Store struct {
	mu    sync.RWMutex
	db    *sql.DB
	items map[string]Key
}

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db, items: map[string]Key{}}
	rows, err := db.Query(`SELECT id,name,api_key,enabled,created_at,updated_at FROM local_api_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k Key
		var enabled int
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &enabled, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		k.Enabled = enabled == 1
		s.items[k.ID] = k
	}
	return s, rows.Err()
}

func (s *Store) List() []Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Key, 0, len(s.items))
	for _, k := range s.items {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the key with the given ID.
func (s *Store) Get(id string) (Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.items[id]
	return k, ok
}

func (s *Store) Save(input Key) (Key, error) {
	if input.Name == "" {
		return Key{}, errors.New("key name is required")
	}
	if input.ID == "" {
		generated, err := GenerateID()
		if err != nil {
			return Key{}, err
		}
		input.ID = generated
	}
	if input.Key == "" {
		generated, err := GenerateKey()
		if err != nil {
			return Key{}, err
		}
		input.Key = generated
	}
	now := time.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, existing := range s.items {
		if id != input.ID && existing.Key == input.Key {
			return Key{}, errors.New("api key already exists")
		}
	}
	if existing, ok := s.items[input.ID]; ok {
		input.CreatedAt = existing.CreatedAt
	}
	if input.CreatedAt == "" {
		input.CreatedAt = now
	}
	input.UpdatedAt = now
	if _, err := s.db.Exec(`INSERT INTO local_api_keys(id,name,api_key,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,api_key=excluded.api_key,enabled=excluded.enabled,updated_at=excluded.updated_at`, input.ID, input.Name, input.Key, input.Enabled, input.CreatedAt, input.UpdatedAt); err != nil {
		return Key{}, err
	}
	s.items[input.ID] = input
	return input, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return errors.New("api key not found")
	}
	if _, err := s.db.Exec(`DELETE FROM local_api_keys WHERE id = ?`, id); err != nil {
		return err
	}
	delete(s.items, id)
	return nil
}

func (s *Store) SetEnabled(id string, enabled bool) error {
	s.mu.RLock()
	k, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return errors.New("api key not found")
	}
	k.Enabled = enabled
	_, err := s.Save(k)
	return err
}

func (s *Store) Authenticate(token string) bool {
	_, _, ok := s.Lookup(token)
	return ok
}

// Lookup identifies the enabled local API key used by a client request. It
// returns only its ID and display name, never the key secret itself.
func (s *Store) Lookup(token string) (id, name string, ok bool) {
	if token == "" {
		return "", "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.items {
		if k.Enabled && k.Key == token {
			return k.ID, k.Name, true
		}
	}
	return "", "", false
}

func GenerateKey() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "ar-" + hex.EncodeToString(buf[:]), nil
}

func GenerateID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "key-" + hex.EncodeToString(buf[:]), nil
}
