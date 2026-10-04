// Package relsig signs and verifies a release's checksums.txt with Ed25519.
//
// The release workflow signs checksums.txt with a private key kept in a GitHub
// secret and publishes the signature as checksums.txt.sig. `ffc update` embeds
// the public keys (keys.go) and refuses a release whose checksums.txt does not
// carry a valid signature from one of them, so replacing the release assets is
// not enough to ship a binary to existing installs.
//
// Keys are the base64 (standard encoding) of a raw Ed25519 public key (32
// bytes) or private key seed (32 bytes). A signature file is the base64 of the
// raw 64-byte signature followed by a newline.
package relsig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SignatureName is the release asset that holds the signature of checksums.txt.
const SignatureName = "checksums.txt.sig"

// MaxSignatureBytes bounds the signature download. A valid one is 89 bytes.
const MaxSignatureBytes = 4 << 10

// domain is prepended to the signed bytes so a signature made with this key
// can never be replayed as a signature over anything else.
const domain = "ffc release checksums v1\n"

func message(checksums []byte) []byte {
	m := make([]byte, 0, len(domain)+len(checksums))
	m = append(m, domain...)
	return append(m, checksums...)
}

// GenerateKey returns a new key pair as base64 strings: the public key and the
// private key seed.
func GenerateKey() (publicKey, seed string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv.Seed()), nil
}

func decodeSeed(seed string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil {
		return nil, fmt.Errorf("private key is not base64: %w", err)
	}
	if len(raw) != ed25519.SeedSize {
		return nil, fmt.Errorf("private key is %d bytes, want %d", len(raw), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

// PublicKey returns the base64 public key that belongs to a base64 seed.
func PublicKey(seed string) (string, error) {
	priv, err := decodeSeed(seed)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
}

// Sign signs checksums with the base64 seed and returns the signature file
// contents.
func Sign(seed string, checksums []byte) ([]byte, error) {
	priv, err := decodeSeed(seed)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(priv, message(checksums))
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n"), nil
}

// ErrNoKeys means the build embeds no release key, so nothing can be verified.
var ErrNoKeys = errors.New("this ffc build has no release signing key")

// Verify reports whether sigFile is a valid signature of checksums by one of
// publicKeys. A malformed key is an error, not a key that is skipped.
func Verify(publicKeys []string, checksums, sigFile []byte) error {
	if len(publicKeys) == 0 {
		return ErrNoKeys
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigFile)))
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	msg := message(checksums)
	for _, k := range publicKeys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("embedded release key %q is malformed", k)
		}
		if ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
			return nil
		}
	}
	return errors.New("signature does not match any release key")
}
