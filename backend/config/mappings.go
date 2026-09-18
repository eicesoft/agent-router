package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ModelMapping routes one client model name to a provider's upstream model.
//
// Aliases are extra client-side names that address the same route. They are
// deliberately kept out of List Models and out of the client configs the
// templates generate: an alias is a convenience for a caller that already knows
// the name, not a second model the gateway advertises.
type ModelMapping struct {
	ID            string   `json:"id"`
	ClientModel   string   `json:"clientModel"`
	ProviderID    string   `json:"providerId"`
	UpstreamModel string   `json:"upstreamModel"`
	Aliases       []string `json:"aliases"`
	Enabled       bool     `json:"enabled"`
}

// Names returns every client-side name that resolves to this mapping.
func (m ModelMapping) Names() []string {
	out := make([]string, 0, len(m.Aliases)+1)
	if m.ClientModel != "" {
		out = append(out, m.ClientModel)
	}
	out = append(out, m.Aliases...)
	return out
}

type MappingStore struct {
	mu    sync.RWMutex
	db    *sql.DB
	items map[string]ModelMapping
}

func NewMappingStore(db *sql.DB) (*MappingStore, error) {
	s := &MappingStore{db: db, items: map[string]ModelMapping{}}
	rows, err := db.Query(`SELECT id,client_model,provider_id,upstream_model,enabled,aliases_json FROM model_mappings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelMapping
		var enabled int
		var aliases string
		if err := rows.Scan(&m.ID, &m.ClientModel, &m.ProviderID, &m.UpstreamModel, &enabled, &aliases); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		m.Aliases = decodeAliases(aliases)
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
	// Aliases are matched after trimming (request bodies arrive whitespace-free,
	// but the UI text field is not), so store the trimmed form once here and let
	// every comparison use it verbatim.
	input.Aliases = normalizeNames(input.Aliases, input.ClientModel)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAliasCollisionsLocked(input); err != nil {
		return ModelMapping{}, err
	}
	aliases, err := json.Marshal(input.Aliases)
	if err != nil {
		return ModelMapping{}, err
	}
	_, err = s.db.Exec(`INSERT INTO model_mappings(id,client_model,provider_id,upstream_model,enabled,aliases_json) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET client_model=excluded.client_model,provider_id=excluded.provider_id,upstream_model=excluded.upstream_model,enabled=excluded.enabled,aliases_json=excluded.aliases_json`, input.ID, input.ClientModel, input.ProviderID, input.UpstreamModel, input.Enabled, string(aliases))
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

// Resolve finds the mapping a client model name routes to. An alias is
// interchangeable with the client model name it was declared beside, so both
// go through the same lookup.
func (s *MappingStore) Resolve(clientModel string) (ModelMapping, bool) {
	name := strings.TrimSpace(clientModel)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if !item.Enabled {
			continue
		}
		for _, candidate := range item.Names() {
			if candidate == name {
				return item, true
			}
		}
	}
	return ModelMapping{}, false
}

// normalizeNames trims each entry, drops blanks, duplicates, and any entry that
// merely repeats own — the client model name is already routed on its own, so
// storing it again would advertise a redundant alias.
func normalizeNames(names []string, own string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{own: {}}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// checkAliasCollisionsLocked rejects two mappings that answer to the same
// client-side name. Resolve returns whichever of them it meets first, so a
// colliding save would silently steal traffic from the other mapping.
func (s *MappingStore) checkAliasCollisionsLocked(input ModelMapping) error {
	for _, item := range s.items {
		if item.ID == input.ID {
			continue
		}
		taken := item.Names()
		for _, name := range input.Names() {
			for _, existing := range taken {
				if name == existing {
					return fmt.Errorf("model name %q is already used by mapping %q", name, item.ClientModel)
				}
			}
		}
	}
	return nil
}

func decodeAliases(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil
	}
	return normalizeNames(names, "")
}
