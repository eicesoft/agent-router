//go:build !darwin && !windows

package secret

import (
	"errors"
	"sync"
)

// memStore is the process-memory fallback for platforms without a native
// secret store (Linux development). Secrets do not survive a restart — same
// limitation the Windows build had before Credential Manager support.
type memStore struct {
	mu       sync.RWMutex
	fallback map[string]string
}

// New returns the platform secret store.
func New() Store { return &memStore{fallback: map[string]string{}} }

func (k *memStore) Set(account, value string) error {
	if account == "" || value == "" {
		return errors.New("key reference and secret are required")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.fallback[account] = value
	return nil
}

func (k *memStore) Get(account string) (string, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	value, ok := k.fallback[account]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (k *memStore) Delete(account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.fallback, account)
	return nil
}
