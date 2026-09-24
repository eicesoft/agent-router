// Package credential owns the pool of upstream API keys for each provider:
// which key serves a request, how keys rotate, and which keys are cooling down
// or permanently invalid.
//
// It deliberately keeps only references in SQLite. The token itself still lives
// exclusively in the OS Keychain, exactly as the single-key design did: a row
// here carries an opaque secret_ref, never the secret.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"agent-router/backend/secret"
)

// Mode is the strategy a provider uses to pick among its keys.
type Mode string

const (
	// ModeSession pins one session to one key. Beyond spreading quota this is a
	// correctness win: Anthropic and OpenAI isolate prompt caches per key, so a
	// session that migrates between keys loses its cache.
	ModeSession Mode = "session"
	// ModeRoundRobin advances a cursor across the usable keys.
	ModeRoundRobin Mode = "round_robin"
	// ModeLeastUsed prefers the key with the fewest requests per unit weight.
	ModeLeastUsed Mode = "least_used"
	// ModeRandom picks uniformly among the usable keys.
	ModeRandom Mode = "random"
)

// NormalizeMode maps arbitrary input onto a known strategy, defaulting to
// session stickiness rather than an accidentally stateless pool.
func NormalizeMode(value string) Mode {
	switch Mode(strings.TrimSpace(value)) {
	case ModeRoundRobin:
		return ModeRoundRobin
	case ModeLeastUsed:
		return ModeLeastUsed
	case ModeRandom:
		return ModeRandom
	default:
		return ModeSession
	}
}

// Credential lifecycle states. Invalid is permanent until an operator resets
// it, because a rejected key does not repair itself.
const (
	StatusActive  = "active"
	StatusInvalid = "invalid"
)

const (
	// cooldownRateLimited matches a typical per-minute quota window.
	cooldownRateLimited = 60 * time.Second
	// cooldownBase backs off repeated transport/5xx failures, doubling up to
	// cooldownMax so a genuinely broken key stops being retried every request.
	cooldownBase = 60 * time.Second
	cooldownMax  = 5 * time.Minute
	// sessionTTL is how long an idle session keeps its key binding. Long enough
	// to cover a working session, short enough that abandoned ones are swept.
	sessionTTL = 30 * time.Minute
	// sweepInterval bounds how often the session table is garbage collected;
	// sweeping happens inline on Acquire rather than from a resident goroutine.
	sweepInterval = 5 * time.Minute
)

// Credential is one upstream API key belonging to a provider. The Keychain
// reference is unexported so it can never reach the UI through JSON.
type Credential struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
	Name       string `json:"name"`
	Mask       string `json:"mask"`
	Enabled    bool   `json:"enabled"`
	Weight     int    `json:"weight"`
	Status     string `json:"status"`
	LastError  string `json:"lastError"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`

	secretRef string
}

// MaskSecret renders a key as the short display form (ss****sfg) the UI and
// request logs show instead of the id. Only the head and tail are revealed, and
// the caller always has the plaintext in hand already — either from the
// Keychain read it was about to perform anyway or from the lease it just
// acquired — so deriving a mask never adds a Keychain round trip.
//
// This is the one value derived from a secret that is allowed to be persisted:
// it is shown in the UI, and a log row must stay readable after the credential
// is deleted. It is deliberately too short to be usable as a key.
func MaskSecret(secret string) string {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return ""
	}
	// Too short to split without revealing most of the value; hide it entirely.
	if len(trimmed) <= 5 {
		return strings.Repeat("*", len(trimmed))
	}
	return trimmed[:2] + "****" + trimmed[len(trimmed)-3:]
}

// Attempt is the outcome of one upstream exchange, which the pool turns into
// health state for the key that served it.
type Attempt struct {
	StatusCode int
	Err        error
	// RetryAfter is the upstream's own hint (429 responses); zero means unset.
	RetryAfter time.Duration
}

// Lease is the key chosen for one request, together with the plaintext the
// caller must put on the wire.
type Lease struct {
	CredentialID   string
	CredentialName string
	// Mask is the display form recorded in the request log. It is safe to
	// persist, unlike secret.
	Mask string

	secret string
}

// Secret returns the plaintext token. This is the single point where a
// credential leaves the Keychain, so callers must never log or persist it.
func (l Lease) Secret() string { return l.secret }

// health is runtime-only state: it is intentionally not persisted, so a restart
// clears cool-downs and gives every key a fresh chance.
type health struct {
	cooldownUntil time.Time
	backoff       int
	requests      int
}

// sessionBinding pins a session fingerprint to a credential until it expires.
type sessionBinding struct {
	credentialID string
	expiresAt    time.Time
}

// Pool resolves credentials for providers and tracks their health. It mirrors
// the SQLite rows in memory the same way provider.Registry does, so request
// handling never touches the database.
type Pool struct {
	mu       sync.Mutex
	db       *sql.DB
	secrets  secret.Store
	items    map[string][]Credential
	state    map[string]*health
	sessions map[string]sessionBinding
	cursors  map[string]int
	// legacyChecked marks providers already probed for a pre-pool key, so the
	// self-heal query runs at most once per provider per process.
	legacyChecked map[string]bool
	lastSweep     time.Time

	// now is injectable because rotation, cool-down and TTL assertions are
	// otherwise wall-clock dependent and flaky.
	now func() time.Time
}

// NewPool loads the credential rows and adopts any pre-existing single key as
// the first credential, so an upgraded install keeps working without a manual
// migration step.
func NewPool(db *sql.DB, secrets secret.Store) (*Pool, error) {
	p := &Pool{
		db:            db,
		secrets:       secrets,
		items:         map[string][]Credential{},
		state:         map[string]*health{},
		sessions:      map[string]sessionBinding{},
		cursors:       map[string]int{},
		legacyChecked: map[string]bool{},
		lastSweep:     time.Now(),
		now:           time.Now,
	}
	if err := p.load(); err != nil {
		return nil, err
	}
	p.backfillMasks()
	p.healMissingSecrets()
	return p, nil
}

// errSecretMissing is the LastError written when a row's secret cannot be read
// from the OS store. It is distinct from an upstream 401/403 rejection so heal
// only revives keys that lost their secret reference, never keys the provider
// rejected.
const errSecretMissing = "secret not found in keychain"

// healMissingSecrets reactivates credentials marked invalid solely because
// their secret was unreadable (process-memory store lost them on restart, a
// Keychain reset, a restored backup). When the secret is available again the
// key is fine; upstream-rejected keys keep their own LastError and stay invalid.
//
// Without this, a Windows restart before Credential Manager support left every
// pool row permanently invalid and the proxy reported "no API key configured"
// even after the secret store was fixed.
func (p *Pool) healMissingSecrets() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for providerID, items := range p.items {
		changed := false
		for i, item := range items {
			if item.Status != StatusInvalid || item.LastError != errSecretMissing {
				continue
			}
			if _, err := p.secrets.Get(item.secretRef); err != nil {
				continue
			}
			item.Status = StatusActive
			item.LastError = ""
			item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			items[i] = item
			p.persistLocked(item)
			changed = true
		}
		if changed {
			p.items[providerID] = items
		}
	}
}

func (p *Pool) load() error {
	// Ordered by insertion, not by id: created_at only has second resolution, so
	// several credentials added in one second would otherwise come back ordered
	// by their random ids and the rotation cursor would not survive a restart.
	// rowid is the table's insertion order.
	rows, err := p.db.Query(`SELECT id,provider_id,name,mask,secret_ref,enabled,weight,status,last_error,created_at,updated_at FROM provider_credentials ORDER BY provider_id, rowid`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c Credential
		var enabled int
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Name, &c.Mask, &c.secretRef, &enabled, &c.Weight, &c.Status, &c.LastError, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		c.Enabled = enabled == 1
		p.items[c.ProviderID] = append(p.items[c.ProviderID], c)
	}
	return rows.Err()
}

// backfillMasks derives and persists masks for credentials stored before the
// column existed, or whose mask was only cached in memory by Acquire. It reads
// each Keychain entry once at startup, which is acceptable because it runs at
// most once per install; steady-state writes derive the mask from a plaintext
// already in hand. A credential whose secret is gone is left blank: the pool
// marks it invalid on first use anyway.
func (p *Pool) backfillMasks() {
	rows, err := p.db.Query(`SELECT id, provider_id, secret_ref FROM provider_credentials WHERE mask = ''`)
	if err != nil {
		return
	}
	type target struct{ id, providerID, ref string }
	var pending []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.providerID, &t.ref); err != nil {
			_ = rows.Close()
			return
		}
		pending = append(pending, t)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return
	}

	for _, t := range pending {
		value, err := p.secrets.Get(t.ref)
		if err != nil {
			continue
		}
		mask := MaskSecret(value)
		if mask == "" {
			continue
		}
		if _, err := p.db.Exec(`UPDATE provider_credentials SET mask = ? WHERE id = ?`, mask, t.id); err != nil {
			continue
		}
		p.mu.Lock()
		item, providerID, ok := p.findLocked(t.id)
		if ok {
			item.Mask = mask
			p.replaceLocked(providerID, item)
		}
		p.mu.Unlock()
	}
}

// List returns a provider's credentials in creation order, ready for the UI.
func (p *Pool) List(providerID string) []Credential {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := p.items[providerID]
	out := make([]Credential, 0, len(items))
	out = append(out, items...)
	return out
}

// ReplaceSecret rewrites the Keychain value behind an existing credential,
// keeping the credential's identity, position and health history. This is what
// makes "edit the provider's key" not add a second credential.
func (p *Pool) ReplaceSecret(id, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("api key is required")
	}
	p.mu.Lock()
	item, _, ok := p.findLocked(id)
	p.mu.Unlock()
	if !ok {
		return errors.New("credential not found")
	}
	if err := p.secrets.Set(item.secretRef, apiKey); err != nil {
		return fmt.Errorf("update credential in keychain: %w", err)
	}
	mask := MaskSecret(apiKey)
	// A replaced key deserves a fresh chance: the old health belonged to the
	// old value.
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.state, id)
	item.Status = StatusActive
	item.LastError = ""
	item.Mask = mask
	item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	p.replaceLocked(item.ProviderID, item)
	if _, err := p.db.Exec(`UPDATE provider_credentials SET mask = ?, status = ?, last_error = '', updated_at = ? WHERE id = ?`,
		mask, StatusActive, item.UpdatedAt, id); err != nil {
		return fmt.Errorf("update credential: %w", err)
	}
	return nil
}

// Adopt records a credential at an existing Keychain reference. The secret must
// already be stored there; this only registers the reference in the pool. The
// lookup also yields the mask, so adopting costs no extra Keychain read.
func (p *Pool) Adopt(providerID, ref, name string) (Credential, error) {
	providerID = strings.TrimSpace(providerID)
	ref = strings.TrimSpace(ref)
	if providerID == "" || ref == "" {
		return Credential{}, errors.New("provider id and secret reference are required")
	}
	value, err := p.secrets.Get(ref)
	if err != nil {
		return Credential{}, fmt.Errorf("no secret stored at %s: %w", ref, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	adopted := p.adoptRefLocked(providerID, ref, name, MaskSecret(value))
	if adopted == nil {
		return Credential{}, errors.New("credential already present")
	}
	return *adopted, nil
}

// AdoptLegacy forces the pre-pool single key to be adopted now rather than on
// the next request. The UI calls it so a provider that already has a Keychain
// key shows it as a credential instead of looking unconfigured.
func (p *Pool) AdoptLegacy(providerID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.legacyChecked, providerID)
	p.adoptLegacyLocked(providerID)
}

// HasCredentials reports whether a provider has any pooled key.
func (p *Pool) HasCredentials(providerID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items[providerID]) > 0
}

// adoptRefLocked registers a credential at ref. It returns nil when a row for
// that reference already exists. Callers hold p.mu.
func (p *Pool) adoptRefLocked(providerID, ref, name, mask string) *Credential {
	if name == "" {
		name = "默认密钥"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	adopted := Credential{
		ID:         legacyID(providerID, ref),
		ProviderID: providerID,
		Name:       name,
		Mask:       mask,
		Enabled:    true,
		Weight:     1,
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		secretRef:  ref,
	}
	// The id is derived from provider and ref, so repeated adoptions converge on
	// one row; ON CONFLICT guards the shared secret_ref uniqueness.
	result, err := p.db.Exec(`INSERT INTO provider_credentials(id,provider_id,name,mask,secret_ref,enabled,weight,status,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(secret_ref) DO NOTHING`,
		adopted.ID, adopted.ProviderID, adopted.Name, adopted.Mask, adopted.secretRef, 1, adopted.Weight, adopted.Status, "", adopted.CreatedAt, adopted.UpdatedAt)
	if err != nil {
		return nil
	}
	// A conflict means the row already existed and is already in the mirror.
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil
	}
	p.items[providerID] = append(p.items[providerID], adopted)
	return &adopted
}

// Add stores a new upstream key in the Keychain and records its reference. The
// secret goes to the Keychain first: a row without a retrievable secret would
// fail every request, and a failed insert is easier to clean up than a missing
// secret.
func (p *Pool) Add(providerID, name, apiKey string) (Credential, error) {
	providerID = strings.TrimSpace(providerID)
	apiKey = strings.TrimSpace(apiKey)
	if providerID == "" {
		return Credential{}, errors.New("provider id is required")
	}
	if apiKey == "" {
		return Credential{}, errors.New("api key is required")
	}
	token, err := randomToken()
	if err != nil {
		return Credential{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	created := Credential{
		ID:         "cred_" + token,
		ProviderID: providerID,
		Name:       strings.TrimSpace(name),
		Mask:       MaskSecret(apiKey),
		Enabled:    true,
		Weight:     1,
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		secretRef:  secretRefFor(providerID, token),
	}
	if err := p.secrets.Set(created.secretRef, apiKey); err != nil {
		return Credential{}, fmt.Errorf("store credential in keychain: %w", err)
	}
	if _, err := p.db.Exec(`INSERT INTO provider_credentials(id,provider_id,name,mask,secret_ref,enabled,weight,status,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		created.ID, created.ProviderID, created.Name, created.Mask, created.secretRef, 1, created.Weight, created.Status, "", created.CreatedAt, created.UpdatedAt); err != nil {
		// Roll the Keychain entry back so a retry does not leak orphans.
		_ = p.secrets.Delete(created.secretRef)
		return Credential{}, fmt.Errorf("record credential: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items[created.ProviderID] = append(p.items[created.ProviderID], created)
	return created, nil
}

// Delete removes a credential and its Keychain entry.
func (p *Pool) Delete(id string) error {
	p.mu.Lock()
	item, providerID, ok := p.findLocked(id)
	p.mu.Unlock()
	if !ok {
		return errors.New("credential not found")
	}
	if _, err := p.db.Exec(`DELETE FROM provider_credentials WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	// A leftover Keychain entry is invisible but still sensitive, so a delete
	// that only half-succeeds is reported rather than silently ignored.
	if err := p.secrets.Delete(item.secretRef); err != nil {
		return fmt.Errorf("delete credential secret: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removeLocked(providerID, id)
	return nil
}

// DeleteByProvider removes every credential of a provider and the Keychain
// entries behind them; deleting a provider must not strand its secrets.
func (p *Pool) DeleteByProvider(providerID string) error {
	p.mu.Lock()
	items := append([]Credential(nil), p.items[providerID]...)
	p.mu.Unlock()
	var failed int
	for _, item := range items {
		if err := p.Delete(item.ID); err != nil {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("delete credentials for %s: %d of %d failed", providerID, failed, len(items))
	}
	return nil
}

// SetEnabled toggles whether a credential may serve requests. Disabled keys stay
// listed so the operator keeps their place in the pool.
func (p *Pool) SetEnabled(id string, enabled bool) error {
	if _, err := p.update(id, `UPDATE provider_credentials SET enabled = ?, updated_at = ? WHERE id = ?`,
		func(item *Credential) []any {
			item.Enabled = enabled
			return []any{boolToInt(enabled), item.UpdatedAt, id}
		}); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !enabled {
		p.unbindCredentialLocked(id)
	}
	return nil
}

// Reset clears a credential's permanent invalid state and its cool-down so an
// operator can retry a key after fixing the problem upstream.
func (p *Pool) Reset(id string) error {
	if _, err := p.update(id, `UPDATE provider_credentials SET status = ?, last_error = '', updated_at = ? WHERE id = ?`,
		func(item *Credential) []any {
			item.Status = StatusActive
			item.LastError = ""
			return []any{StatusActive, item.UpdatedAt, id}
		}); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.state, id)
	return nil
}

// Update rewrites a credential's display name and weight.
func (p *Pool) Update(id, name string, weight int) (Credential, error) {
	if weight < 1 {
		weight = 1
	}
	return p.update(id, `UPDATE provider_credentials SET name = ?, weight = ?, updated_at = ? WHERE id = ?`,
		func(item *Credential) []any {
			item.Name = strings.TrimSpace(name)
			item.Weight = weight
			return []any{item.Name, weight, item.UpdatedAt, id}
		})
}

func (p *Pool) update(id, query string, mutate func(*Credential) []any) (Credential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	item, providerID, ok := p.findLocked(id)
	if !ok {
		return Credential{}, errors.New("credential not found")
	}
	item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := p.db.Exec(query, mutate(&item)...); err != nil {
		return Credential{}, fmt.Errorf("update credential: %w", err)
	}
	p.replaceLocked(providerID, item)
	return item, nil
}

// Acquire chooses the key that serves one request. session is the caller's
// session fingerprint; an empty one means the pool cannot pin, so the strategy's
// stateless branch decides.
func (p *Pool) Acquire(providerID, mode, session string) (Lease, error) {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(now)
	p.adoptLegacyLocked(providerID)
	items := p.items[providerID]
	if len(items) == 0 {
		return Lease{}, ErrNoCredentials
	}
	chosen, ok := p.pickLocked(items, NormalizeMode(mode), session, now)
	if !ok {
		return Lease{}, ErrNoCredentials
	}
	value, err := p.secrets.Get(chosen.secretRef)
	if err != nil {
		// The row outlived its OS-store entry (Keychain reset, restored backup,
		// or a process-memory store after restart). Mark it invalid so the pool
		// routes around it; NewPool.healMissingSecrets revives it when the
		// secret becomes readable again.
		chosen.Status = StatusInvalid
		chosen.LastError = errSecretMissing
		p.replaceLocked(providerID, chosen)
		p.persistLocked(chosen)
		p.unbindCredentialLocked(chosen.ID)
		return Lease{}, fmt.Errorf("credential %q has no retrievable secret: %w", chosen.Name, err)
	}
	mask := MaskSecret(value)
	// Cache the mask on the in-memory row so the UI sees it without another
	// Keychain read. Persisting is left to backfillMasks: writing here would run
	// a database statement while holding the pool lock, on the request path.
	if chosen.Mask == "" && mask != "" {
		chosen.Mask = mask
		p.replaceLocked(providerID, chosen)
	}
	p.stateOfLocked(chosen.ID).requests++
	return Lease{CredentialID: chosen.ID, CredentialName: chosen.Name, Mask: mask, secret: value}, nil
}

// Report folds one upstream outcome back into the key's health. 2xx clears the
// cool-down; 401/403 marks the key permanently invalid; 429 and transport/5xx
// failures impose a cool-down so the pool stops preferring a sick key.
func (p *Pool) Report(credentialID string, attempt Attempt) {
	if credentialID == "" {
		return
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.stateOfLocked(credentialID)
	item, providerID, found := p.findLocked(credentialID)
	switch {
	case attempt.Err == nil && attempt.StatusCode < 300:
		state.backoff = 0
		state.cooldownUntil = time.Time{}
	case attempt.StatusCode == 401 || attempt.StatusCode == 403:
		state.cooldownUntil = time.Time{}
		state.backoff = 0
		if found && item.Status != StatusInvalid {
			item.Status = StatusInvalid
			item.LastError = fmt.Sprintf("upstream rejected the key with status %d", attempt.StatusCode)
			p.replaceLocked(providerID, item)
			p.persistLocked(item)
		}
		p.unbindCredentialLocked(credentialID)
	case attempt.StatusCode == 429:
		window := attempt.RetryAfter
		if window <= 0 {
			window = cooldownRateLimited
		}
		state.cooldownUntil = now.Add(window)
	case attempt.Err == nil && attempt.StatusCode == 404:
		// A 404 is the request's fault (unknown model or route), not the key's:
		// cooling the key down would rotate away from a healthy credential.
		return
	default:
		state.backoff++
		window := cooldownBase * time.Duration(1<<min(state.backoff-1, 3))
		if window > cooldownMax {
			window = cooldownMax
		}
		state.cooldownUntil = now.Add(window)
		if found {
			item.LastError = describeAttempt(attempt)
			p.replaceLocked(providerID, item)
		}
	}
}

// ErrNoCredentials is returned when a provider has no usable key at all, which
// the proxy surfaces as "no API key configured".
var ErrNoCredentials = errors.New("no provider credentials")

// IDs returns a provider's credential ids in creation order. Order carries
// meaning — rotation and least-used tie-breaks both start at the oldest key — so
// this must not sort: credential ids are random tokens, and their lexicographic
// order is unrelated to when they were added.
func (p *Pool) IDs(providerID string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.items[providerID]))
	for _, item := range p.items[providerID] {
		ids = append(ids, item.ID)
	}
	return ids
}

func (p *Pool) persistLocked(item Credential) {
	_, _ = p.db.Exec(`UPDATE provider_credentials SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		item.Status, item.LastError, time.Now().UTC().Format(time.RFC3339), item.ID)
}

func (p *Pool) stateOfLocked(id string) *health {
	state, ok := p.state[id]
	if !ok {
		state = &health{}
		p.state[id] = state
	}
	return state
}

func (p *Pool) findLocked(id string) (Credential, string, bool) {
	for providerID, items := range p.items {
		for _, item := range items {
			if item.ID == id {
				return item, providerID, true
			}
		}
	}
	return Credential{}, "", false
}

func (p *Pool) replaceLocked(providerID string, item Credential) {
	items := p.items[providerID]
	for i := range items {
		if items[i].ID == item.ID {
			items[i] = item
			p.items[providerID] = items
			return
		}
	}
}

func (p *Pool) removeLocked(providerID, id string) {
	items := p.items[providerID]
	out := items[:0]
	for _, item := range items {
		if item.ID != id {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		delete(p.items, providerID)
		return
	}
	p.items[providerID] = out
	delete(p.state, id)
	p.unbindCredentialLocked(id)
}

// unbindCredentialLocked drops every session pinned to a credential that just
// became unusable, so those sessions re-pick on their next request.
func (p *Pool) unbindCredentialLocked(credentialID string) {
	for key, binding := range p.sessions {
		if binding.credentialID == credentialID {
			delete(p.sessions, key)
		}
	}
}

// adoptLegacyLocked adopts the pre-pool providers.api_key_ref as a credential
// when a provider has none, which is what keeps an upgraded install working
// without a manual migration. It runs once per provider per process: without the
// guard every request to an unmigrated provider would hit the database.
func (p *Pool) adoptLegacyLocked(providerID string) {
	if p.legacyChecked[providerID] {
		return
	}
	p.legacyChecked[providerID] = true
	if len(p.items[providerID]) > 0 {
		return
	}
	var ref string
	if err := p.db.QueryRow(`SELECT api_key_ref FROM providers WHERE id = ?`, providerID).Scan(&ref); err != nil {
		return
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return
	}
	// Only adopt a reference that actually holds a secret; an empty Keychain
	// slot must keep reporting "no API key configured". The read also supplies
	// the mask, so adoption needs no second lookup.
	value, err := p.secrets.Get(ref)
	if err != nil {
		return
	}
	p.adoptRefLocked(providerID, ref, "默认密钥", MaskSecret(value))
}

func (p *Pool) sweepLocked(now time.Time) {
	if now.Sub(p.lastSweep) < sweepInterval {
		return
	}
	p.lastSweep = now
	for key, binding := range p.sessions {
		if !binding.expiresAt.After(now) {
			delete(p.sessions, key)
		}
	}
}

func secretRefFor(providerID, token string) string { return "provider/" + providerID + "/" + token }

// legacyID derives a stable id from the provider and its old reference so
// repeated adoptions converge on one row.
func legacyID(providerID, ref string) string {
	sum := sha256.Sum256([]byte(providerID + "\x00" + ref))
	return "cred_legacy_" + hex.EncodeToString(sum[:6])
}

func randomToken() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate credential id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// randomIndex returns a uniform value in [0,n) for ModeRandom. It reads
// crypto/rand directly rather than sharing package-level math state.
func randomIndex(n int) int {
	if n <= 1 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(value.Int64())
}

func describeAttempt(attempt Attempt) string {
	if attempt.Err != nil {
		return attempt.Err.Error()
	}
	return fmt.Sprintf("upstream status %d", attempt.StatusCode)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
