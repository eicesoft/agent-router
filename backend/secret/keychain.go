package secret

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

const service = "com.agentrouter.credentials"

type Store interface {
	Set(account, value string) error
	Get(account string) (string, error)
	Delete(account string) error
}

// Keychain uses macOS Keychain. Non-macOS development uses process memory only.
type Keychain struct {
	mu       sync.RWMutex
	fallback map[string]string
}

func New() *Keychain { return &Keychain{fallback: map[string]string{}} }
func (k *Keychain) Set(account, value string) error {
	if account == "" || value == "" {
		return errors.New("key reference and secret are required")
	}
	if runtime.GOOS != "darwin" {
		k.mu.Lock()
		defer k.mu.Unlock()
		k.fallback[account] = value
		return nil
	}
	return exec.Command("security", "add-generic-password", "-U", "-a", account, "-s", service, "-w", value).Run()
}
func (k *Keychain) Get(account string) (string, error) {
	if runtime.GOOS != "darwin" {
		k.mu.RLock()
		defer k.mu.RUnlock()
		value, ok := k.fallback[account]
		if !ok {
			return "", errors.New("secret not found")
		}
		return value, nil
	}
	output, err := exec.Command("security", "find-generic-password", "-a", account, "-s", service, "-w").Output()
	if err != nil {
		return "", errors.New("secret not found in Keychain")
	}
	return strings.TrimSpace(string(output)), nil
}
func (k *Keychain) Delete(account string) error {
	if runtime.GOOS != "darwin" {
		k.mu.Lock()
		defer k.mu.Unlock()
		delete(k.fallback, account)
		return nil
	}
	return exec.Command("security", "delete-generic-password", "-a", account, "-s", service).Run()
}
