package credential

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-router/backend/provider"
	"agent-router/backend/storage"
)

// fakeSecrets is an in-memory secret.Store. The Keychain is the one dependency
// the pool cannot exercise in CI, so it is substituted here.
type fakeSecrets map[string]string

func (f fakeSecrets) Set(account, value string) error {
	f[account] = value
	return nil
}

func (f fakeSecrets) Get(account string) (string, error) {
	value, ok := f[account]
	if !ok {
		return "", errors.New("secret not found")
	}
	return value, nil
}

func (f fakeSecrets) Delete(account string) error {
	delete(f, account)
	return nil
}

// newTestPool builds a pool over a fresh database with a controllable clock and
// a provider row already registered.
func newTestPool(t *testing.T, providerID string) (*Pool, fakeSecrets, *sql.DB) {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if providerID != "" {
		if _, err := db.Exec(`INSERT INTO providers(id,name,kind,base_url,api_key_ref,icon,model_prefix,enabled,models_json,available_models_json,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			providerID, "Test", "compatible", "https://example.com", "provider/"+providerID, "", "", 1, "[]", "[]", time.Now().UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	secrets := fakeSecrets{}
	pool, err := NewPool(db, secrets)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pool.now = func() time.Time { return clock }
	return pool, secrets, db
}

// addKeys registers n credentials and returns their ids in creation order.
func addKeys(t *testing.T, pool *Pool, providerID string, n int) []string {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := pool.Add(providerID, fmt.Sprintf("key-%d", i), fmt.Sprintf("sk-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	ids := pool.IDs(providerID)
	if len(ids) != n {
		t.Fatalf("expected %d credentials, got %d", n, len(ids))
	}
	return ids
}

// The cursor must actually advance, otherwise "polling" is a single key pool.
func TestRoundRobinAdvancesAcrossKeys(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	addKeys(t, pool, "test", 3)

	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		lease, err := pool.Acquire("test", string(ModeRoundRobin), "")
		if err != nil {
			t.Fatal(err)
		}
		seen[lease.CredentialID]++
		pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
	}
	if len(seen) != 3 {
		t.Fatalf("round robin used %d of 3 keys: %v", len(seen), seen)
	}
	for id, count := range seen {
		if count != 2 {
			t.Fatalf("key %s served %d of 6 requests, want 2: %v", id, count, seen)
		}
	}
}

// Stickiness is the whole point of session mode, and it must survive the turn
// boundary: a client resends its whole history every turn, so the fingerprint is
// taken from the head of the conversation. If the pool keyed on the last message
// instead, every turn would migrate and this test would catch it.
func TestSessionModePinsOneKeyAcrossTurns(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	addKeys(t, pool, "test", 3)

	// Turn 1 and turn 3 share a system prompt and first user message; only the
	// trailing history differs.
	first := Fingerprint("you are a helpful agent", "write a script")
	second := Fingerprint("you are a helpful agent", "write a script")
	if first != second {
		t.Fatal("fingerprint drifted across identical conversation heads")
	}

	var pinned string
	for turn := 0; turn < 5; turn++ {
		lease, err := pool.Acquire("test", string(ModeSession), first)
		if err != nil {
			t.Fatal(err)
		}
		if pinned == "" {
			pinned = lease.CredentialID
		} else if lease.CredentialID != pinned {
			t.Fatalf("turn %d migrated from %s to %s", turn, pinned, lease.CredentialID)
		}
		pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
	}
}

// A different conversation must be allowed to land on a different key, or the
// pool would be performing no balancing at all.
func TestSessionModeSpreadsDistinctSessions(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	addKeys(t, pool, "test", 3)

	used := map[string]bool{}
	for i := 0; i < 3; i++ {
		session := Fingerprint("system", fmt.Sprintf("conversation-%d", i))
		lease, err := pool.Acquire("test", string(ModeSession), session)
		if err != nil {
			t.Fatal(err)
		}
		pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
		used[lease.CredentialID] = true
	}
	if len(used) != 3 {
		t.Fatalf("three sessions used %d keys: %v", len(used), used)
	}
}

// A valid binding must outrank load balancing, and an expired one must give way
// to it. Load is staged by hand so the two behaviours are distinguishable: if
// re-picking happened to land on the same key, the test could not tell an
// expired binding from a live one.
func TestSessionBindingExpiresAndRepicks(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)
	session := Fingerprint("system", "user")

	lease, err := pool.Acquire("test", string(ModeSession), session)
	if err != nil {
		t.Fatal(err)
	}
	if lease.CredentialID != ids[0] {
		t.Fatalf("first session bound to %s, want the oldest key %s", lease.CredentialID, ids[0])
	}

	// Make the pinned key the least attractive one. The live binding must still
	// win: stickiness exists to keep a session on its key, not to re-balance it
	// mid-conversation.
	pool.mu.Lock()
	pool.stateOfLocked(ids[0]).requests = 10
	pool.stateOfLocked(ids[1]).requests = 0
	pool.mu.Unlock()
	pinned, err := pool.Acquire("test", string(ModeSession), session)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.CredentialID != ids[0] {
		t.Fatalf("live binding moved to %s despite being pinned to %s", pinned.CredentialID, ids[0])
	}

	// Expire the binding; the next request must follow the strategy and land on
	// the strictly lighter key.
	pool.mu.Lock()
	pool.sessions[session] = sessionBinding{credentialID: ids[0], expiresAt: pool.now().Add(-time.Second)}
	pool.mu.Unlock()
	repick, err := pool.Acquire("test", string(ModeSession), session)
	if err != nil {
		t.Fatal(err)
	}
	if repick.CredentialID != ids[1] {
		t.Fatalf("expired session stayed on %s, want the lighter key %s", repick.CredentialID, ids[1])
	}
}

// least_used must converge on the idle key rather than drifting.
func TestLeastUsedPrefersIdleKey(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 3)

	// Load the oldest key by hand so the other two stay idle. Driving this
	// through Acquire would not work: least_used re-compares on every call, so
	// its own balancing moves off the key being loaded.
	pool.mu.Lock()
	pool.stateOfLocked(ids[0]).requests = 5
	pool.mu.Unlock()

	lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatal(err)
	}
	if lease.CredentialID != ids[1] {
		t.Fatalf("least_used chose %s, want the idle oldest key %s", lease.CredentialID, ids[1])
	}
}

// Weighting must let an operator say one key has more headroom. Weighting is
// only observable when both keys carry traffic — an untouched key always has
// load 0 and wins — so this sets both sides and checks the crossover point.
func TestLeastUsedHonoursWeight(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)

	if _, err := pool.Update(ids[0], "heavy", 4); err != nil {
		t.Fatal(err)
	}

	// Both keys loaded, weighted one 3 requests to the other's 1. Per weight
	// that is 0.75 against 1.0, so the weighted key is still preferred.
	pool.mu.Lock()
	pool.stateOfLocked(ids[0]).requests = 3
	pool.stateOfLocked(ids[1]).requests = 1
	pool.mu.Unlock()
	lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatal(err)
	}
	if lease.CredentialID != ids[0] {
		t.Fatalf("3/4=0.75 vs 1/1=1.0 chose %s, want the weighted key %s", lease.CredentialID, ids[0])
	}

	// Same counts with the weight removed: 3 against 1, so now the other key is
	// lighter. This is what makes the weight observable rather than incidental.
	if _, err := pool.Update(ids[0], "heavy", 1); err != nil {
		t.Fatal(err)
	}
	pool.mu.Lock()
	pool.stateOfLocked(ids[0]).requests = 3
	pool.stateOfLocked(ids[1]).requests = 1
	pool.mu.Unlock()
	lease, err = pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatal(err)
	}
	if lease.CredentialID != ids[1] {
		t.Fatalf("3/1=3.0 vs 1/1=1.0 chose %s, want the lighter key %s", lease.CredentialID, ids[1])
	}
}

// A rate-limited key must leave the rotation instead of being re-picked, and it
// must return once the window passes.
func TestRateLimitedKeyCoolsDownThenReturns(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)
	clock := pool.now()

	first, err := pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatal(err)
	}
	if first.CredentialID != ids[0] {
		t.Fatalf("first acquire chose %s, want the oldest key %s", first.CredentialID, ids[0])
	}
	pool.Report(first.CredentialID, Attempt{StatusCode: 429, RetryAfter: 30 * time.Second})

	// While cooling down the other key serves, even though it looks busier.
	for i := 0; i < 3; i++ {
		lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
		if err != nil {
			t.Fatal(err)
		}
		if lease.CredentialID == first.CredentialID {
			t.Fatalf("cooling key %s was selected again", lease.CredentialID)
		}
		pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
	}

	// Past the window the key is eligible again.
	pool.now = func() time.Time { return clock.Add(31 * time.Second) }
	lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatal(err)
	}
	if lease.CredentialID != first.CredentialID {
		t.Fatalf("cooled key did not return, got %s want %s", lease.CredentialID, first.CredentialID)
	}
}

// An empty pool with every key cooling down must still serve, not fail.
func TestAllCoolingPoolDegradesInsteadOfFailing(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)

	for _, id := range ids {
		pool.Report(id, Attempt{StatusCode: 429})
	}
	lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatalf("degraded acquire failed: %v", err)
	}
	if lease.CredentialID == "" {
		t.Fatal("degraded acquire returned no credential")
	}
}

// 401/403 is permanent until reset: a rejected key does not repair itself.
func TestRejectedKeyIsPermanentlyExcludedUntilReset(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)

	pool.Report(ids[0], Attempt{StatusCode: 401})
	for i := 0; i < 4; i++ {
		lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
		if err != nil {
			t.Fatal(err)
		}
		if lease.CredentialID == ids[0] {
			t.Fatalf("invalid key %s served a request", ids[0])
		}
		pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
	}
	// The state is persisted so a restart does not resurrect the key.
	var status string
	if err := pool.db.QueryRow(`SELECT status FROM provider_credentials WHERE id = ?`, ids[0]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusInvalid {
		t.Fatalf("status = %q, want %q", status, StatusInvalid)
	}

	if err := pool.Reset(ids[0]); err != nil {
		t.Fatal(err)
	}
	items := pool.List("test")
	if items[0].Status != StatusActive {
		t.Fatalf("reset did not clear status: %+v", items[0])
	}
}

// A disabled key is excluded but stays listed so the operator keeps its place.
func TestDisabledKeyIsExcludedButListed(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)

	if err := pool.SetEnabled(ids[0], false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
		if err != nil {
			t.Fatal(err)
		}
		if lease.CredentialID == ids[0] {
			t.Fatalf("disabled key served a request")
		}
		pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
	}
	if len(pool.List("test")) != 2 {
		t.Fatalf("disabled key disappeared from the listing")
	}
}

// A row whose Keychain entry vanished must not fail every request forever.
func TestMissingSecretMarksKeyInvalid(t *testing.T) {
	pool, secrets, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)

	// Drop the Keychain entry behind the first credential.
	pool.mu.Lock()
	ref := pool.items["test"][0].secretRef
	pool.mu.Unlock()
	if err := secrets.Delete(ref); err != nil {
		t.Fatal(err)
	}

	// The first acquire may land on the broken key and report it.
	if _, err := pool.Acquire("test", string(ModeRoundRobin), ""); err != nil {
		t.Logf("expected failure on the broken key: %v", err)
	}
	lease, err := pool.Acquire("test", string(ModeLeastUsed), "")
	if err != nil {
		t.Fatalf("pool did not route around the broken key: %v", err)
	}
	if lease.CredentialID == ids[0] {
		t.Fatalf("broken key still selected")
	}
}

// An existing single-key install must keep working with no manual migration:
// the pool adopts providers.api_key_ref as its first credential.
func TestLegacySingleKeyIsAdopted(t *testing.T) {
	pool, secrets, _ := newTestPool(t, "legacy")
	if err := secrets.Set("provider/legacy", "sk-legacy"); err != nil {
		t.Fatal(err)
	}

	lease, err := pool.Acquire("legacy", string(ModeSession), "")
	if err != nil {
		t.Fatalf("legacy key was not adopted: %v", err)
	}
	if lease.Secret() != "sk-legacy" {
		t.Fatalf("adopted the wrong secret: %q", lease.Secret())
	}
	var stored string
	if err := pool.db.QueryRow(`SELECT secret_ref FROM provider_credentials WHERE provider_id = 'legacy'`).Scan(&stored); err != nil {
		t.Fatalf("adoption was not persisted: %v", err)
	}
	if stored != "provider/legacy" {
		t.Fatalf("secret_ref = %q, want the original reference", stored)
	}

	// Adoption is idempotent across restarts.
	reopened, err := NewPool(pool.db, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List("legacy")) != 1 {
		t.Fatalf("re-adoption duplicated the credential: %+v", reopened.List("legacy"))
	}
}

// A provider with no key configured must keep reporting no credentials rather
// than inventing one from an empty Keychain slot.
func TestProviderWithoutKeyHasNoCredentials(t *testing.T) {
	pool, _, _ := newTestPool(t, "empty")

	if _, err := pool.Acquire("empty", string(ModeSession), ""); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
}

func TestDeleteRemovesKeychainEntryAndRow(t *testing.T) {
	pool, secrets, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 2)

	if err := pool.Delete(ids[0]); err != nil {
		t.Fatal(err)
	}
	if len(pool.List("test")) != 1 {
		t.Fatalf("row survived delete: %+v", pool.List("test"))
	}
	// The deleted credential's Keychain entry must be gone with it; one key
	// remains, and no secret may outlive its row.
	if len(secrets) != 1 {
		t.Fatalf("keychain entry was not removed: %v", secrets)
	}
}

// DeleteByProvider must not strand secrets when a provider is removed.
func TestDeleteByProviderClearsEverything(t *testing.T) {
	pool, secrets, _ := newTestPool(t, "test")
	addKeys(t, pool, "test", 3)

	if err := pool.DeleteByProvider("test"); err != nil {
		t.Fatal(err)
	}
	if len(pool.List("test")) != 0 || len(secrets) != 0 {
		t.Fatalf("provider delete left state behind: rows=%d secrets=%d", len(pool.List("test")), len(secrets))
	}
}

// The registry mirror and the pool must agree on the provider id they key on.
func TestPoolSharesProviderIdsWithRegistry(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry, err := provider.NewRegistry(db)
	if err != nil {
		t.Fatal(err)
	}
	secrets := fakeSecrets{}
	pool, err := NewPool(db, secrets)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := registry.Save(provider.Provider{ID: "reg", Name: "Reg", BaseURL: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Add(saved.ID, "k", "sk-1"); err != nil {
		t.Fatal(err)
	}
	if !pool.HasCredentials(saved.ID) {
		t.Fatalf("pool cannot see credentials for %s", saved.ID)
	}
}

// Health accounting must survive concurrent request handling: the proxy serves
// requests on many goroutines and the pool is shared.
func TestPoolIsRaceFreeUnderConcurrency(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 4)

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				mode := []Mode{ModeSession, ModeRoundRobin, ModeLeastUsed, ModeRandom}[i%4]
				lease, err := pool.Acquire("test", string(mode), Fingerprint("s", fmt.Sprint(worker%5)))
				if err != nil {
					continue
				}
				switch i % 7 {
				case 0:
					pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
				case 1:
					pool.Report(lease.CredentialID, Attempt{StatusCode: 429, RetryAfter: time.Second})
				case 2:
					pool.Report(lease.CredentialID, Attempt{Err: errors.New("dial tcp: connection refused")})
				default:
					pool.Report(lease.CredentialID, Attempt{StatusCode: 200})
				}
			}
		}(worker)
	}
	wg.Wait()

	if len(pool.List("test")) != len(ids) {
		t.Fatalf("credential rows changed under concurrency: %d", len(pool.List("test")))
	}
}

func TestNormalizeModeDefaultsToSession(t *testing.T) {
	for _, input := range []string{"", "  ", "nonsense", string(ModeSession)} {
		if got := NormalizeMode(input); got != ModeSession {
			t.Fatalf("NormalizeMode(%q) = %q, want %q", input, got, ModeSession)
		}
	}
	if got := NormalizeMode(" least_used "); got != ModeLeastUsed {
		t.Fatalf("NormalizeMode did not trim: %q", got)
	}
}

func TestSessionHintPrefersExplicitHeaders(t *testing.T) {
	if got := SessionHint(map[string]string{"X-Session-Id": " abc "}); got != "abc" {
		t.Fatalf("SessionHint = %q", got)
	}
	if got := SessionHint(map[string]string{"X-Conversation-Id": "conv"}); got != "conv" {
		t.Fatalf("SessionHint = %q", got)
	}
	if got := SessionHint(map[string]string{}); got != "" {
		t.Fatalf("SessionHint = %q, want empty", got)
	}
}

// The mask is the one secret-derived value that reaches SQLite, so its shape is
// load-bearing: it must reveal only a head and a tail, and must never be the
// key itself.
func TestMaskSecretRevealsOnlyHeadAndTail(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"   ":          "",
		"sk-abcdefg":   "sk****efg",
		"abcde":        "*****",
		"ab":           "**",
		"  sk-key-1  ": "sk****y-1",
	}
	for input, want := range cases {
		if got := MaskSecret(input); got != want {
			t.Fatalf("MaskSecret(%q) = %q, want %q", input, got, want)
		}
	}
	// A long key must not survive verbatim in its mask.
	long := "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA"
	if mask := MaskSecret(long); strings.Contains(mask, "AAAAAAAA") {
		t.Fatalf("mask leaked the key body: %q", mask)
	}
}

// Adding a key must record its mask, because the request log has no other way
// to render a key the operator can recognise.
func TestAddRecordsMask(t *testing.T) {
	pool, _, db := newTestPool(t, "test")
	created, err := pool.Add("test", "主账号", "sk-abcdefghij")
	if err != nil {
		t.Fatal(err)
	}
	if created.Mask != "sk****hij" {
		t.Fatalf("Add returned mask %q", created.Mask)
	}
	if got := pool.List("test")[0].Mask; got != "sk****hij" {
		t.Fatalf("pool list mask = %q", got)
	}
	var stored string
	if err := db.QueryRow(`SELECT mask FROM provider_credentials WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "sk****hij" {
		t.Fatalf("persisted mask = %q", stored)
	}
	// The plaintext must not have been written anywhere in the row.
	var secretRef, name string
	if err := db.QueryRow(`SELECT secret_ref, name FROM provider_credentials WHERE id = ?`, created.ID).Scan(&secretRef, &name); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(secretRef, "abcdefghij") || strings.Contains(name, "abcdefghij") {
		t.Fatalf("plaintext leaked into the row: ref=%q name=%q", secretRef, name)
	}
}

// Replacing a key must refresh the mask, otherwise the log would show the
// previous key's identity forever.
func TestReplaceSecretRefreshesMask(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	created, err := pool.Add("test", "主账号", "sk-oldkey12345")
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.ReplaceSecret(created.ID, "sk-newkey67890"); err != nil {
		t.Fatal(err)
	}
	if got := pool.List("test")[0].Mask; got != "sk****890" {
		t.Fatalf("mask after replace = %q, want the new key's mask", got)
	}
}

// The lease carries the mask so the proxy can log it without reading the
// Keychain a second time.
func TestAcquireReturnsMask(t *testing.T) {
	pool, _, _ := newTestPool(t, "test")
	if _, err := pool.Add("test", "key-1", "sk-lease123456"); err != nil {
		t.Fatal(err)
	}
	lease, err := pool.Acquire("test", string(ModeRoundRobin), "")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mask != "sk****456" {
		t.Fatalf("lease mask = %q", lease.Mask)
	}
	if lease.Secret() != "sk-lease123456" {
		t.Fatalf("lease secret = %q", lease.Secret())
	}
}

// Credentials created before the mask column existed must be backfilled at
// startup, or the UI would show them with no identity at all.
func TestStartupBackfillsMissingMask(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO providers(id,name,kind,base_url,api_key_ref,icon,model_prefix,enabled,models_json,available_models_json,updated_at) VALUES('test','Test','compatible','https://example.com','provider/test','','',1,'[]','[]','x')`); err != nil {
		t.Fatal(err)
	}
	secrets := fakeSecrets{"provider/test/legacy": "sk-legacy987654"}
	// A row written by the previous schema: no mask.
	if _, err := db.Exec(`INSERT INTO provider_credentials(id,provider_id,name,secret_ref,enabled,weight,status,last_error,created_at,updated_at) VALUES('cred_old','test','旧 Key','provider/test/legacy',1,1,'active','','x','x')`); err != nil {
		t.Fatal(err)
	}
	pool, err := NewPool(db, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.List("test")[0].Mask; got != "sk****654" {
		t.Fatalf("backfilled mask = %q", got)
	}
	var stored string
	if err := db.QueryRow(`SELECT mask FROM provider_credentials WHERE id = 'cred_old'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "sk****654" {
		t.Fatalf("persisted backfill = %q", stored)
	}
}

// A key marked invalid only because its secret was unreadable must revive on
// the next NewPool once the secret is readable again. Upstream-rejected keys
// (different LastError) must stay invalid.
func TestNewPoolHealsMissingSecretInvalid(t *testing.T) {
	pool, secrets, db := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 1)

	pool.mu.Lock()
	ref := pool.items["test"][0].secretRef
	pool.mu.Unlock()
	if err := secrets.Delete(ref); err != nil {
		t.Fatal(err)
	}
	// First acquire sees the missing secret and marks the row invalid.
	if _, err := pool.Acquire("test", string(ModeSession), ""); err == nil {
		t.Fatal("expected failure when secret is gone")
	}
	if got := pool.List("test")[0].Status; got != StatusInvalid {
		t.Fatalf("status = %q, want %q", got, StatusInvalid)
	}
	var lastError string
	if err := db.QueryRow(`SELECT last_error FROM provider_credentials WHERE id = ?`, ids[0]).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError != errSecretMissing {
		t.Fatalf("last_error = %q, want %q", lastError, errSecretMissing)
	}

	// Secret returns (e.g. Credential Manager is available again).
	if err := secrets.Set(ref, "sk-healed123456"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPool(db, secrets)
	if err != nil {
		t.Fatal(err)
	}
	item := reopened.List("test")[0]
	if item.Status != StatusActive {
		t.Fatalf("status after heal = %q, want %q (last_error=%q)", item.Status, StatusActive, item.LastError)
	}
	if item.LastError != "" {
		t.Fatalf("last_error after heal = %q, want empty", item.LastError)
	}
	lease, err := reopened.Acquire("test", string(ModeSession), "")
	if err != nil {
		t.Fatalf("acquire after heal: %v", err)
	}
	if lease.Secret() != "sk-healed123456" {
		t.Fatalf("secret = %q", lease.Secret())
	}
}

// An upstream-rejected key must not be revived by healMissingSecrets.
func TestNewPoolDoesNotHealUpstreamRejected(t *testing.T) {
	pool, secrets, db := newTestPool(t, "test")
	ids := addKeys(t, pool, "test", 1)
	pool.Report(ids[0], Attempt{StatusCode: 401})
	if got := pool.List("test")[0].Status; got != StatusInvalid {
		t.Fatalf("status = %q, want invalid after 401", got)
	}
	// Secret is still present; heal must leave the rejection alone.
	if _, err := secrets.Get(pool.items["test"][0].secretRef); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPool(db, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List("test")[0].Status; got != StatusInvalid {
		t.Fatalf("status after reopen = %q, want still invalid", got)
	}
}
