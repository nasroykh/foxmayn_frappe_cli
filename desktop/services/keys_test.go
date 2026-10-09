package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

const testKey = "sk-test-0123456789abcdefWXYZ"

func newTestKeys(t *testing.T) *Keys {
	t.Helper()
	keyring.MockInit()
	return NewKeys()
}

func errCode(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	return e.Code
}

func TestKeysRoundTrip(t *testing.T) {
	k := newTestKeys(t)
	if st, err := k.Status("anthropic"); err != nil || st.Set {
		t.Fatalf("fresh status = %+v, %v", st, err)
	}
	if err := k.Set("anthropic", testKey); err != nil {
		t.Fatal(err)
	}
	got, err := k.Get("anthropic")
	if err != nil || got != testKey {
		t.Fatalf("Get = %q, %v", got, err)
	}
	st, err := k.Status("anthropic")
	if err != nil || !st.Set || st.Last4 != "WXYZ" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if err := k.Delete("anthropic"); err != nil {
		t.Fatal(err)
	}
	if st, _ := k.Status("anthropic"); st.Set {
		t.Fatal("still set after delete")
	}
	if _, err := k.Get("anthropic"); errCode(t, err) != CodeNotFound {
		t.Fatalf("Get after delete: %v", err)
	}
}

func TestKeysTrimmed(t *testing.T) {
	k := newTestKeys(t)
	if err := k.Set("p", "  \t"+testKey+"\n "); err != nil {
		t.Fatal(err)
	}
	if got, _ := k.Get("p"); got != testKey {
		t.Fatalf("stored %q", got)
	}
}

func TestKeysLast4Rules(t *testing.T) {
	k := newTestKeys(t)
	for _, tc := range []struct{ key, want string }{
		{"abcdefghijk", ""},      // 11
		{"abcdefghijkl", "ijkl"}, // 12
		{"short", ""},
	} {
		if err := k.Set("p", tc.key); err != nil {
			t.Fatal(err)
		}
		st, err := k.Status("p")
		if err != nil || !st.Set || st.Last4 != tc.want {
			t.Errorf("key %q: status %+v, %v; want last4 %q", tc.key, st, err, tc.want)
		}
	}
}

func TestKeysDeleteMissing(t *testing.T) {
	k := newTestKeys(t)
	if err := k.Delete("nothing"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestKeysInvalidInput(t *testing.T) {
	k := newTestKeys(t)
	for _, tc := range []struct{ id, key string }{
		{"p", ""},
		{"p", "   "},
		{"p", strings.Repeat("a", maxKeyBytes+1)},
		{"", testKey},
		{"  ", testKey},
		{"a\x00b", testKey},
		{"a\nb", testKey},
	} {
		err := k.Set(tc.id, tc.key)
		if err == nil || errCode(t, err) != CodeInvalid {
			t.Errorf("Set(%q, len %d) = %v; want invalid", tc.id, len(tc.key), err)
		}
	}
	if err := k.Set("p", strings.Repeat("a", maxKeyBytes)); err != nil {
		t.Errorf("max length key rejected: %v", err)
	}
	if _, err := k.Get(""); errCode(t, err) != CodeInvalid {
		t.Error("Get with empty id should be invalid")
	}
	if _, err := k.Status("a\x01"); errCode(t, err) != CodeInvalid {
		t.Error("Status with control char id should be invalid")
	}
	if err := k.Delete(""); errCode(t, err) != CodeInvalid {
		t.Error("Delete with empty id should be invalid")
	}
}

// failStore fails every call and echoes the secret in its Set error, the
// worst case for leaking.
type failStore struct{ err error }

func (f failStore) Set(_, _, secret string) error {
	return errors.New(f.err.Error() + " secret=" + secret)
}
func (f failStore) Get(_, _ string) (string, error) { return "", f.err }
func (f failStore) Delete(_, _ string) error        { return f.err }

func TestKeysErrorMapping(t *testing.T) {
	boom := errors.New("secret service unavailable")
	k := &Keys{store: failStore{err: boom}}

	err := k.Set("p", testKey)
	if errCode(t, err) != CodeUnavailable {
		t.Fatalf("Set: %v", err)
	}
	assertNoKey(t, err)
	if _, err = k.Get("p"); errCode(t, err) != CodeUnavailable {
		t.Fatalf("Get: %v", err)
	}
	if _, err = k.Status("p"); errCode(t, err) != CodeUnavailable {
		t.Fatalf("Status: %v", err)
	}
	if err = k.Delete("p"); errCode(t, err) != CodeUnavailable {
		t.Fatalf("Delete: %v", err)
	}

	nf := &Keys{store: failStore{err: keyring.ErrNotFound}}
	if _, err = nf.Get("p"); errCode(t, err) != CodeNotFound {
		t.Fatalf("Get not found: %v", err)
	}
	if st, err := nf.Status("p"); err != nil || st.Set {
		t.Fatalf("Status not found = %+v, %v", st, err)
	}
	if err = nf.Delete("p"); err != nil {
		t.Fatalf("Delete not found: %v", err)
	}
}

func assertNoKey(t *testing.T, v any) {
	t.Helper()
	if e, ok := v.(error); ok {
		if strings.Contains(e.Error(), testKey) {
			t.Fatalf("error text has the key: %q", e.Error())
		}
		var se *Error
		if errors.As(e, &se) {
			v = se
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), testKey) {
		t.Fatalf("JSON has the key: %s", b)
	}
}

func TestKeysNoLeak(t *testing.T) {
	k := newTestKeys(t)
	if err := k.Set("p", testKey); err != nil {
		t.Fatal(err)
	}
	st, _ := k.Status("p")
	assertNoKey(t, st)

	// Errors from every path, including a key echoed by the backend.
	bad := &Keys{store: failStore{err: errors.New("down")}}
	assertNoKey(t, bad.Set("p", testKey))
	_, err := bad.Get("p")
	assertNoKey(t, err)
	_, err = bad.Status("p")
	assertNoKey(t, err)
	assertNoKey(t, bad.Delete("p"))
	assertNoKey(t, k.Set("p", strings.Repeat("a", maxKeyBytes+1)))
}
