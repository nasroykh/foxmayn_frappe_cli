package services

import (
	"errors"
	"strings"
	"unicode"

	"github.com/zalando/go-keyring"
)

// keychainService is the service name provider keys are filed under in the
// OS keychain; the user field is the provider id.
const keychainService = "Foxmayn Frappe Desktop"

// maxKeyBytes bounds a pasted key. Real provider keys are well under 300.
const maxKeyBytes = 4096

// minLast4Len is the shortest key whose last four characters are shown.
const minLast4Len = 12

// keyStore is the keychain surface Keys needs; tests swap it.
type keyStore interface {
	Set(service, user, secret string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type osKeyStore struct{}

func (osKeyStore) Set(service, user, secret string) error { return keyring.Set(service, user, secret) }
func (osKeyStore) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}
func (osKeyStore) Delete(service, user string) error { return keyring.Delete(service, user) }

// KeyStatus says whether a provider has a key. It never carries the key.
type KeyStatus struct {
	Set bool `json:"set"`
	// Last4 is the last four characters of the key, only for keys of at
	// least 12 characters.
	Last4 string `json:"last4,omitempty"`
}

// Keys keeps provider API keys in the OS keychain. The key never goes to
// the web view, the store, events, logs or error text.
type Keys struct {
	store keyStore
}

// NewKeys returns Keys backed by the OS keychain.
func NewKeys() *Keys { return &Keys{store: osKeyStore{}} }

func validProviderID(id string) error {
	if strings.TrimSpace(id) == "" {
		return invalid("provider", "Choose a provider.")
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return invalid("provider", "The provider name has characters that are not allowed.")
		}
	}
	return nil
}

// Set stores the key for a provider, replacing any earlier one.
func (k *Keys) Set(providerID, key string) error {
	if err := validProviderID(providerID); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return invalid("key", "Paste the API key.")
	}
	if len(key) > maxKeyBytes {
		return invalid("key", "That key is too long.")
	}
	if err := k.store.Set(keychainService, providerID, key); err != nil {
		return keychainError("Saving the key", err, key)
	}
	return nil
}

// Get returns the stored key. Providers use it internally; it is not
// exposed to the web view.
func (k *Keys) Get(providerID string) (string, error) {
	if err := validProviderID(providerID); err != nil {
		return "", err
	}
	key, err := k.store.Get(keychainService, providerID)
	if err != nil {
		return "", keychainError("Reading the key", err, "")
	}
	return key, nil
}

// Status reports whether a key is set, with its last four characters when
// it is long enough for that to be safe.
func (k *Keys) Status(providerID string) (KeyStatus, error) {
	key, err := k.Get(providerID)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == CodeNotFound {
			return KeyStatus{}, nil
		}
		return KeyStatus{}, err
	}
	st := KeyStatus{Set: true}
	if r := []rune(key); len(r) >= minLast4Len {
		st.Last4 = string(r[len(r)-4:])
	}
	return st, nil
}

// Delete removes the key. A missing key is not an error.
func (k *Keys) Delete(providerID string) error {
	if err := validProviderID(providerID); err != nil {
		return err
	}
	err := k.store.Delete(keychainService, providerID)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return keychainError("Removing the key", err, "")
}

// keychainError maps a keychain failure to an *Error. secret is scrubbed
// from the detail text in case a backend echoes it.
func keychainError(what string, err error, secret string) error {
	if errors.Is(err, keyring.ErrNotFound) {
		return &Error{Code: CodeNotFound, Message: "No key is saved for this provider."}
	}
	e := &Error{Code: CodeUnavailable, Message: what + " failed. The system keychain is not available."}
	detail := err.Error()
	if secret != "" {
		detail = strings.ReplaceAll(detail, secret, "")
	}
	e.Detail = detail
	return e
}
