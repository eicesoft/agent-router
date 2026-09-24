//go:build windows

package secret

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows Credential Manager (advapi32 CredReadW/CredWriteW/CredDeleteW)
// persists generic credentials for the current user across reboots — the
// counterpart of the macOS Keychain. Without this, secrets lived only in
// process memory: after a restart Acquire could not read them, marked every
// credential invalid, and the proxy reported "no API key configured".
//
// Target names are service + "/" + account (e.g.
// com.agentrouter.credentials/provider/openai/…), unique per secret_ref.

const (
	credTypeGeneric         = 1 // CRED_TYPE_GENERIC
	credPersistLocalMachine = 2 // CRED_PERSIST_LOCAL_MACHINE — survives relogin
	credWriteFlagUpdate     = 1 // CRED_WRITE_FLAG_UPDATE
)

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

// winCredential mirrors CREDENTIALW. Field order and pointer width must match
// the native layout; Go inserts the same padding the C compiler does after
// CredentialBlobSize.
type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// credStore is the Windows Credential Manager-backed Store.
type credStore struct {
	mu sync.Mutex
}

// New returns the platform secret store.
func New() Store { return &credStore{} }

func credTarget(account string) (*uint16, error) {
	return windows.UTF16PtrFromString(service + "/" + account)
}

func (s *credStore) Set(account, value string) error {
	if account == "" || value == "" {
		return errors.New("key reference and secret are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := credTarget(account)
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString(account)
	if err != nil {
		return err
	}
	blob := []byte(value)
	cred := &winCredential{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	r, _, callErr := procCredWriteW.Call(
		uintptr(unsafe.Pointer(cred)),
		credWriteFlagUpdate,
	)
	if r == 0 {
		return callErr
	}
	return nil
}

func (s *credStore) Get(account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(account)
}

func (s *credStore) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := credTarget(account)
	if err != nil {
		return err
	}
	r, _, callErr := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
	)
	if r == 0 {
		// Already gone is success: Delete stays idempotent with the in-memory
		// fallback, and pool.Delete has already removed the row.
		if _, getErr := s.getLocked(account); getErr == ErrNotFound {
			return nil
		}
		return callErr
	}
	return nil
}

// getLocked is Get without taking the mutex; callers already hold it.
func (s *credStore) getLocked(account string) (string, error) {
	target, err := credTarget(account)
	if err != nil {
		return "", err
	}
	var cred *winCredential
	r, _, _ := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if r == 0 || cred == nil {
		return "", ErrNotFound
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return "", ErrNotFound
	}
	blob := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	return string(blob), nil
}
