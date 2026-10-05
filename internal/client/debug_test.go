package client

import (
	"strings"
	"testing"
)

func TestRedactJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"usr":"a","pwd":"hunter2"}`, `{"usr":"a","pwd":"***"}`},
		{`{"api_secret": "s3", "api_key":"k"}`, `{"api_secret": "***", "api_key":"k"}`},
		{`{"access_token":"t","refresh_token":"r","token_type":"Bearer"}`, `{"access_token":"***","refresh_token":"***","token_type":"***"}`},
		{`{"new_password":"x\"y","name":"n"}`, `{"new_password":"***","name":"n"}`},
		{`{"code":"c","code_verifier":"v","key":"reset"}`, `{"code":"***","code_verifier":"***","key":"***"}`},
		// Escaped inside a JSON string argument.
		{`{"doc":"{\"name\":\"u\",\"password\":\"p\"}"}`, `{"doc":"{\"name\":\"u\",\"password\":\"***\"}"}`},
		// Truncated bodies are still redacted.
		{`{"sid":"abc","rows":[{"x":`, `{"sid":"***","rows":[{"x":`},
		// Non-secret keys that merely contain a secret word's letters stay.
		{`{"description":"keep","postcode":"1000"}`, `{"description":"keep","postcode":"1000"}`},
	}
	for _, c := range cases {
		if got := RedactJSON(c.in); got != c.want {
			t.Errorf("RedactJSON(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestRedactURLAndHeaders(t *testing.T) {
	u := redactURL("https://s.example/api/method/x?usr=a&pwd=p&refresh_token=r&limit=5")
	for _, secret := range []string{"pwd=p", "refresh_token=r"} {
		if strings.Contains(u, secret) {
			t.Errorf("redactURL kept %s: %s", secret, u)
		}
	}
	if !strings.Contains(u, "limit=5") || !strings.Contains(u, "usr=a") {
		t.Errorf("redactURL dropped plain parameters: %s", u)
	}
	for _, c := range []struct{ k, v, want string }{
		{"Authorization", "token key:secret", "token ***"},
		{"Authorization", "Bearer abc", "Bearer ***"},
		{"Cookie", "sid=abc; theme=dark", "sid=***; theme=dark"},
		{"Set-Cookie", "sid=abc; Path=/; HttpOnly", "sid=***; Path=/; HttpOnly"},
		{"X-Frappe-CSRF-Token", "abc", "***"},
		{"Content-Type", "application/json", "application/json"},
	} {
		if got := redactHeader(c.k, c.v); got != c.want {
			t.Errorf("redactHeader(%s, %s) = %q, want %q", c.k, c.v, got, c.want)
		}
	}
	if got, want := redactForm("grant_type=refresh_token&refresh_token=r&client_id=c&code_verifier=v"),
		"grant_type=refresh_token&refresh_token=***&client_id=c&code_verifier=***"; got != want {
		t.Errorf("redactForm = %s, want %s", got, want)
	}
}
