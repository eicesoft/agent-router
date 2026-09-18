package provider

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindOpenAI     Kind = "openai"
	KindAnthropic  Kind = "anthropic"
	KindGemini     Kind = "gemini"
	KindCompatible Kind = "compatible"
)

type Provider struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Kind            Kind             `json:"kind"`
	BaseURL         string           `json:"baseUrl"`
	APIKeyRef       string           `json:"apiKeyRef"`
	Icon            string           `json:"icon"`
	ModelPrefix     string           `json:"modelPrefix"`
	Enabled         bool             `json:"enabled"`
	Models          []string         `json:"models"`
	AvailableModels []AvailableModel `json:"availableModels"`
	// CredentialMode selects how the credential pool picks among this provider's
	// keys: session stickiness, round robin, least used, or random. Empty is
	// normalized to session by the pool.
	CredentialMode string `json:"credentialMode"`
	UpdatedAt      string `json:"updatedAt"`
}

// AvailableModel retains the upstream identifier and its creation timestamp so
// cached catalogs can keep the same newest-first ordering without refetching.
type AvailableModel struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
}
type Catalog struct {
	Aliases   map[string]string `json:"aliases"`
	Version   int               `json:"version"`
	Providers []Provider        `json:"providers"`
}

//go:embed catalog.json
var catalogJSON []byte

func BuiltInCatalog() (Catalog, error) {
	var catalog Catalog
	err := json.Unmarshal(catalogJSON, &catalog)
	return catalog, err
}

type Registry struct {
	mu             sync.RWMutex
	db             *sql.DB
	items          map[string]Provider
	deleted        map[string]struct{}
	CatalogVersion int
}

func NewRegistry(db *sql.DB) (*Registry, error) {
	r := &Registry{db: db, items: map[string]Provider{}, deleted: map[string]struct{}{}}
	if err := r.load(); err != nil {
		return nil, err
	}
	catalog, err := BuiltInCatalog()
	if err != nil {
		return nil, err
	}
	r.CatalogVersion = catalog.Version
	for _, item := range catalog.Providers {
		if _, deleted := r.deleted[item.ID]; deleted {
			continue
		}
		existing, ok := r.items[item.ID]
		if !ok {
			item.APIKeyRef = "provider/" + item.ID
			item.Enabled = false
			if item.ID == "openai" {
				item.Enabled = true
			}
			if _, err := r.Save(item); err != nil {
				return nil, err
			}
			continue
		}
		if existing.Icon == "" && item.Icon != "" {
			existing.Icon = item.Icon
			if _, err := r.Save(existing); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}
func (r *Registry) load() error {
	deletedRows, err := r.db.Query(`SELECT id FROM deleted_providers`)
	if err != nil {
		return err
	}
	for deletedRows.Next() {
		var id string
		if err := deletedRows.Scan(&id); err != nil {
			deletedRows.Close()
			return err
		}
		r.deleted[id] = struct{}{}
	}
	if err := deletedRows.Close(); err != nil {
		return err
	}
	if err := deletedRows.Err(); err != nil {
		return err
	}
	rows, err := r.db.Query(`SELECT id,name,kind,base_url,api_key_ref,icon,model_prefix,enabled,models_json,available_models_json,credential_mode,updated_at FROM providers`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p Provider
		var enabled int
		var models, availableModels string
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKeyRef, &p.Icon, &p.ModelPrefix, &enabled, &models, &availableModels, &p.CredentialMode, &p.UpdatedAt); err != nil {
			return err
		}
		p.Enabled = enabled == 1
		_ = json.Unmarshal([]byte(models), &p.Models)
		p.AvailableModels = decodeAvailableModels(availableModels)
		if len(p.AvailableModels) == 0 && len(p.Models) > 0 {
			p.AvailableModels = make([]AvailableModel, 0, len(p.Models))
			for _, model := range p.Models {
				p.AvailableModels = append(p.AvailableModels, AvailableModel{ID: model})
			}
		}
		r.items[p.ID] = p
	}
	return rows.Err()
}
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.items))
	for _, p := range r.items {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.items[id]
	return p, ok
}
func (r *Registry) Save(p Provider) (Provider, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	if p.BaseURL == "" {
		catalog, err := BuiltInCatalog()
		if err != nil {
			return Provider{}, err
		}
		name := strings.ToLower(p.Name)
		if alias, ok := catalog.Aliases[name]; ok {
			name = alias
		}
		for _, preset := range catalog.Providers {
			if (p.Kind != "" && p.Kind == preset.Kind) ||
				(p.Kind == "" && (name == strings.ToLower(preset.ID) || name == strings.ToLower(preset.Name))) {
				p.BaseURL = preset.BaseURL
				if p.Kind == "" {
					p.Kind = preset.Kind
				}
				if p.Icon == "" {
					p.Icon = preset.Icon
				}
				break
			}
		}
	}
	if p.ID == "" || p.Name == "" || p.BaseURL == "" {
		return Provider{}, errors.New("provider id, name and base URL are required")
	}
	if p.Kind == "" {
		p.Kind = KindCompatible
	}
	if p.APIKeyRef == "" {
		p.APIKeyRef = "provider/" + p.ID
	}
	if p.Icon == "" {
		p.Icon = string(p.Kind)
	}
	// A caller that does not carry the field (older UI payloads, automatic
	// catalog seeding) must not silently reset an operator's chosen strategy.
	// The mirror is read under the lock: Save itself locks later, and reading
	// r.items unlocked here would race with concurrent saves.
	if p.CredentialMode == "" {
		r.mu.RLock()
		existing, ok := r.items[p.ID]
		r.mu.RUnlock()
		if ok {
			p.CredentialMode = existing.CredentialMode
		}
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	models, _ := json.Marshal(p.Models)
	availableModels, _ := json.Marshal(p.AvailableModels)
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.Begin()
	if err != nil {
		return Provider{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO providers(id,name,kind,base_url,api_key_ref,icon,model_prefix,enabled,models_json,available_models_json,credential_mode,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,base_url=excluded.base_url,api_key_ref=excluded.api_key_ref,icon=excluded.icon,model_prefix=excluded.model_prefix,enabled=excluded.enabled,models_json=excluded.models_json,available_models_json=excluded.available_models_json,credential_mode=excluded.credential_mode,updated_at=excluded.updated_at`, p.ID, p.Name, p.Kind, p.BaseURL, p.APIKeyRef, p.Icon, p.ModelPrefix, p.Enabled, string(models), string(availableModels), p.CredentialMode, p.UpdatedAt); err != nil {
		return Provider{}, err
	}
	if _, err := tx.Exec(`DELETE FROM deleted_providers WHERE id = ?`, p.ID); err != nil {
		return Provider{}, err
	}
	if err := tx.Commit(); err != nil {
		return Provider{}, err
	}
	r.items[p.ID] = p
	delete(r.deleted, p.ID)
	return p, nil
}

// Delete removes a provider and records a tombstone so built-in catalog
// entries are not automatically restored on the next application start.
func (r *Registry) Delete(id string) (Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.items[id]
	if !ok {
		return Provider{}, errors.New("provider not found")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return Provider{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM providers WHERE id = ?`, id); err != nil {
		return Provider{}, err
	}
	if _, err := tx.Exec(`INSERT INTO deleted_providers(id) VALUES(?) ON CONFLICT(id) DO NOTHING`, id); err != nil {
		return Provider{}, err
	}
	if err := tx.Commit(); err != nil {
		return Provider{}, err
	}
	delete(r.items, id)
	r.deleted[id] = struct{}{}
	return p, nil
}
func (r *Registry) SetEnabled(id string, enabled bool) error {
	p, ok := r.Get(id)
	if !ok {
		return errors.New("provider not found")
	}
	p.Enabled = enabled
	_, err := r.Save(p)
	return err
}

// SetCredentialMode records how the credential pool should pick among this
// provider's keys. It is a separate call from Save so a credential-mode change
// never rewrites the rest of the provider row.
func (r *Registry) SetCredentialMode(id, mode string) error {
	p, ok := r.Get(id)
	if !ok {
		return errors.New("provider not found")
	}
	p.CredentialMode = strings.TrimSpace(mode)
	_, err := r.Save(p)
	return err
}
