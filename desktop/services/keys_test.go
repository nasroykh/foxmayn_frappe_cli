package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

const testKey = "sk-test-0123456789abcdefWXYZ"

// memStore is an in-memory keyStore, so no test touches the real keychain.
type memStore struct{ m map[string]string }

func newMemKeys() *Keys { return &Keys{store: &memStore{m: map[string]string{}}} }

func (s *memStore) Set(service, user, secret string) error {
	s.m[service+"\x00"+user] = secret
	return nil
}

func (s *memStore) Get(service, user string) (string, error) {
	v, ok := s.m[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (s *memStore) Delete(service, user string) error {
	if _, ok := s.m[service+"\x00"+user]; !ok {
		return keyring.ErrNotFound
	}
	delete(s.m, service+"\x00"+user)
	return nil
}

// failStore fails every call with err. echo, when set, derives the Set
// error text from the secret, the worst case for leaking.
type failStore struct {
	err  error
	echo func(secret string) string
}

func (f failStore) Set(_, _, secret string) error {
	if f.echo != nil {
		return errors.New(f.err.Error() + " " + f.echo(secret))
	}
	return f.err
}
func (f failStore) Get(_, _ string) (string, error) { return "", f.err }
func (f failStore) Delete(_, _ string) error        { return f.err }

func errCode(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	return e.Code
}

func TestKeysRoundTrip(t *testing.T) {
	k := newMemKeys()
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
	b, _ := json.Marshal(st)
	if string(b) != `{"set":true,"last4":"WXYZ"}` {
		t.Fatalf("status JSON = %s", b)
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
	k := newMemKeys()
	if err := k.Set("p", "  \t"+testKey+"\n "); err != nil {
		t.Fatal(err)
	}
	if got, _ := k.Get("p"); got != testKey {
		t.Fatalf("stored %q", got)
	}
}

func TestKeysLast4Rules(t *testing.T) {
	k := newMemKeys()
	for _, tc := range []struct{ key, want string }{
		{"abcdefghijk", ""},      // 11
		{"abcdefghijkl", "ijkl"}, // 12
		{"short", ""},
		{strings.Repeat("é", 11), ""},                     // 11 runes, 22 bytes
		{strings.Repeat("a", 8) + "αβγδ", "αβγδ"},         // 12 runes
		{strings.Repeat("日", 12), strings.Repeat("日", 4)}, // 12 runes
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
	if err := newMemKeys().Delete("nothing"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestKeysInvalidInput(t *testing.T) {
	k := newMemKeys()
	for _, tc := range []struct{ id, key string }{
		{"p", ""},
		{"p", "   "},
		{"p", strings.Repeat("a", maxKeyBytes+1)},
		{"", testKey},
		{" anthropic", testKey},
		{"anthropic ", testKey},
		{"Anthropic", testKey},
		{"a\"b", testKey},
		{"a‮b", testKey},
		{"a\x00b", testKey},
		{"a\nb", testKey},
		{"-a", testKey},
		{strings.Repeat("a", 65), testKey},
	} {
		err := k.Set(tc.id, tc.key)
		if err == nil || errCode(t, err) != CodeInvalid {
			t.Errorf("Set(%q, len %d) = %v; want invalid", tc.id, len(tc.key), err)
		}
	}
	for _, id := range []string{"anthropic", "open-router", "local.1", "a_b", strings.Repeat("a", 64)} {
		if err := k.Set(id, strings.Repeat("a", maxKeyBytes)); err != nil {
			t.Errorf("Set(%q) rejected: %v", id, err)
		}
	}
	if _, err := k.Get(""); errCode(t, err) != CodeInvalid {
		t.Error("Get with empty id should be invalid")
	}
	if _, err := k.Status("a\x01"); errCode(t, err) != CodeInvalid {
		t.Error("Status with control char id should be invalid")
	}
	if err := k.Delete("Upper"); errCode(t, err) != CodeInvalid {
		t.Error("Delete with upper-case id should be invalid")
	}
}

func TestKeysErrorMapping(t *testing.T) {
	k := &Keys{store: failStore{err: errors.New("secret service unavailable")}}

	err := k.Set("p", testKey)
	if errCode(t, err) != CodeUnavailable {
		t.Fatalf("Set: %v", err)
	}
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

	big := &Keys{store: failStore{err: keyring.ErrSetDataTooBig}}
	if err = big.Set("p", testKey); errCode(t, err) != CodeInvalid {
		t.Fatalf("Set too big: %v", err)
	}
}

// assertNoLeak fails when the error text or its JSON holds any fragment.
func assertNoLeak(t *testing.T, v any, fragments ...string) {
	t.Helper()
	var text string
	if e, ok := v.(error); ok {
		text = e.Error()
		var se *Error
		if errors.As(e, &se) {
			v = se
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	text += string(b)
	for _, f := range fragments {
		if strings.Contains(text, f) {
			t.Fatalf("fragment %q leaked in %q", f, text)
		}
	}
}

func TestKeysNoLeak(t *testing.T) {
	k := newMemKeys()
	if err := k.Set("p", testKey); err != nil {
		t.Fatal(err)
	}
	st, _ := k.Status("p")
	assertNoLeak(t, st, testKey, "sk-test")

	// Too long, with the key inside it.
	long := testKey + strings.Repeat("a", maxKeyBytes)
	err := k.Set("p", long)
	if errCode(t, err) != CodeInvalid {
		t.Fatalf("too long: %v", err)
	}
	assertNoLeak(t, err, testKey, "sk-test")

	prefix := func(s string) string { return s[:10] }
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for name, echo := range map[string]func(string) string{
		"full":   func(s string) string { return s },
		"prefix": prefix,
		"base64": b64,
	} {
		bad := &Keys{store: failStore{err: errors.New("down"), echo: echo}}
		err := bad.Set("p", testKey)
		if errCode(t, err) != CodeUnavailable {
			t.Fatalf("%s: %v", name, err)
		}
		assertNoLeak(t, err, testKey, prefix(testKey), b64(testKey), "sk-test", "c2st")
	}
	// A too-big error from a backend that echoes the key.
	big := &Keys{store: failStore{err: keyring.ErrSetDataTooBig, echo: func(s string) string { return s }}}
	assertNoLeak(t, big.Set("p", testKey), testKey, "sk-test")
}
