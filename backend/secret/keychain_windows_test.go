//go:build windows

package secret

import (
	"strconv"
	"testing"
	"time"
)

// TestCredentialStoreRoundTrip catches invalid CredWriteW parameters that only
// fail against the real Windows Credential Manager.
func TestCredentialStoreRoundTrip(t *testing.T) {
	store := New()
	account := "test/" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const value = "test-secret"

	if err := store.Set(account, value); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Delete(account); err != nil {
			t.Errorf("Delete failed: %v", err)
		}
	})

	got, err := store.Get(account)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != value {
		t.Fatalf("Get = %q, want %q", got, value)
	}
}
