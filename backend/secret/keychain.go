// Package secret stores upstream API keys outside SQLite. The value never
// lands in the application database; only an opaque reference does.
package secret

import "errors"

// service is the macOS Keychain service name and the Windows Credential
// Manager target-name prefix, so both platforms namespace entries the same way.
const service = "com.agentrouter.credentials"

// ErrNotFound is returned by Get when no secret is stored for the account.
var ErrNotFound = errors.New("secret not found")

// Store is the OS-backed secret store (macOS Keychain, Windows Credential
// Manager) with an in-memory fallback on platforms that have neither.
type Store interface {
	Set(account, value string) error
	Get(account string) (string, error)
	Delete(account string) error
}
