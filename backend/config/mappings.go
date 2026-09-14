package config

import (
	"database/sql"
	"errors"
	"sort"
	"sync"
)

type ModelMapping struct {
	ID            string `json:"id"`
	ClientModel   string `json:"clientModel"`
	ProviderID    string `json:"providerId"`
	UpstreamModel string `json:"upstreamModel"`
	Enabled       bool   `json:"enabled"`
}
type MappingStore struct {
	mu    sync.RWMutex
	db    *sql.DB
	items map[string]ModelMapping
}

func NewMappingStore(db *sql.DB) (*MappingStore, error) {
	s := &MappingStore{db: db, items: map[string]ModelMapping{}}
	rows, err := db.Query(`SELECT id,client_model,provider_id,upstream_model,enabled FROM model_mappings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelMapping
		var enabled int
		if err := rows.Scan(&m.ID, &m.ClientModel, &m.ProviderID, &m.UpstreamModel, &enabled); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		s.items[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(s.items) == 0 {
		_, _ = s.Save(ModelMapping{ID: "default-gpt", ClientModel: "gpt-4.1", ProviderID: "openai", UpstreamModel: "gpt-4.1", Enabled: true})
	}
	return s, nil
}
func (s *MappingStore) List() []ModelMapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ModelMapping, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientModel < out[j].ClientModel })
	return out
}
func (s *MappingStore) Save(input ModelMapping) (ModelMapping, error) {
	if input.ID == "" || input.ClientModel == "" || input.ProviderID == "" || input.UpstreamModel == "" {
		return ModelMapping{}, errors.New("all mapping fields are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO model_mappings(id,client_model,provider_id,upstream_model,enabled) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET client_model=excluded.client_model,provider_id=excluded.provider_id,upstream_model=excluded.upstream_model,enabled=excluded.enabled`, input.ID, input.ClientModel, input.ProviderID, input.UpstreamModel, input.Enabled)
	if err != nil {
		return ModelMapping{}, err
	}
	s.items[input.ID] = input
	return input, nil
}

func (s *MappingStore) DeleteByProvider(providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM model_mappings WHERE provider_id = ?`, providerID); err != nil {
		return err
	}
	for id, mapping := range s.items {
		if mapping.ProviderID == providerID {
			delete(s.items, id)
		}
	}
	return nil
}

func (s *MappingStore) Resolve(clientModel string) (ModelMapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if item.ClientModel == clientModel && item.Enabled {
			return item, true
		}
	}
	return ModelMapping{}, false
}
