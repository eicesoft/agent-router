//go:build darwin

package secret

import (
	"errors"
	"os/exec"
	"strings"
)

// keychain is the macOS Keychain implementation, reached via the security CLI.
type keychain struct{}

// New returns the platform secret store.
func New() Store { return &keychain{} }

func (k *keychain) Set(account, value string) error {
	if account == "" || value == "" {
		return errors.New("key reference and secret are required")
	}
	return exec.Command("security", "add-generic-password", "-U", "-a", account, "-s", service, "-w", value).Run()
}

func (k *keychain) Get(account string) (string, error) {
	output, err := exec.Command("security", "find-generic-password", "-a", account, "-s", service, "-w").Output()
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(output)), nil
}

func (k *keychain) Delete(account string) error {
	return exec.Command("security", "delete-generic-password", "-a", account, "-s", service).Run()
}
