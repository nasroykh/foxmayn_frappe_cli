package relsig

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PublicKey(seed); err != nil || got != pub {
		t.Fatalf("PublicKey = %q, %v; want %q", got, err, pub)
	}
	other, _, _ := GenerateKey()
	sums := []byte("abc  ffc_1.6.1_linux_amd64.tar.gz\n")

	sig, err := Sign(seed, sums)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) > MaxSignatureBytes || !strings.HasSuffix(string(sig), "\n") {
		t.Fatalf("unexpected signature file %q", sig)
	}

	if err := Verify([]string{pub}, sums, sig); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
	// Any listed key may sign (rotation).
	if err := Verify([]string{other, pub}, sums, sig); err != nil {
		t.Errorf("second key rejected: %v", err)
	}
	// CRLF or missing newline in the signature file is tolerated.
	if err := Verify([]string{pub}, sums, []byte(strings.TrimSpace(string(sig))+"\r\n")); err != nil {
		t.Errorf("CRLF signature rejected: %v", err)
	}

	cases := map[string]struct {
		keys []string
		sums []byte
		sig  []byte
	}{
		"tampered checksums": {[]string{pub}, []byte("abd  ffc_1.6.1_linux_amd64.tar.gz\n"), sig},
		"wrong key":          {[]string{other}, sums, sig},
		"not base64":         {[]string{pub}, sums, []byte("!!!\n")},
		"short signature":    {[]string{pub}, sums, []byte(base64.StdEncoding.EncodeToString([]byte("short")))},
		"empty signature":    {[]string{pub}, sums, nil},
		"malformed key":      {[]string{"bm90IGEga2V5"}, sums, sig},
	}
	for name, c := range cases {
		if err := Verify(c.keys, c.sums, c.sig); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := Verify(nil, sums, sig); !errors.Is(err, ErrNoKeys) {
		t.Errorf("no keys: got %v, want ErrNoKeys", err)
	}
}

func TestSignatureIsDomainSeparated(t *testing.T) {
	// A signature over the bare checksums (no domain prefix) must not verify.
	pub, seed, _ := GenerateKey()
	priv, _ := decodeSeed(seed)
	sums := []byte("abc  x\n")
	bare := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	if err := Verify([]string{pub}, sums, []byte(bare)); err == nil {
		t.Fatal("signature without the domain prefix was accepted")
	}
}

func TestBadSeed(t *testing.T) {
	for _, s := range []string{"", "!!!", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := Sign(s, []byte("x")); err == nil {
			t.Errorf("Sign accepted seed %q", s)
		}
	}
}

// TestReleaseKeysConfigured keeps a build without a release key from shipping:
// such a build could never verify an update.
func TestReleaseKeysConfigured(t *testing.T) {
	if len(ReleaseKeys) == 0 {
		t.Fatal("ReleaseKeys is empty: add the release public key (go run ./tools/relsign keygen)")
	}
	for _, k := range ReleaseKeys {
		if pub, err := base64.StdEncoding.DecodeString(k); err != nil || len(pub) != ed25519.PublicKeySize {
			t.Fatalf("ReleaseKeys entry %q is not a base64 Ed25519 public key", k)
		}
	}
}

// TestInstallScriptInSync keeps install.sh, which verifies checksums.txt.sig
// with OpenSSL, on the same keys and signed-message prefix as ffc update.
func TestInstallScriptInSync(t *testing.T) {
	b, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	m := regexp.MustCompile(`(?m)^RELEASE_KEYS="([^"]*)"`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("install.sh: RELEASE_KEYS not found")
	}
	if got, want := strings.Fields(m[1]), ReleaseKeys; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("install.sh RELEASE_KEYS = %q, want %q (internal/relsig/keys.go)", got, want)
	}
	if want := "printf '" + strings.TrimSuffix(domain, "\n") + "\\n'"; !strings.Contains(script, want) {
		t.Errorf("install.sh does not sign-check with the prefix %q", want)
	}
}
