package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"usr":"a","pwd":"hunter2"}`, `{"usr":"a","pwd":"***"}`},
		{`{"api_secret": "s3", "api_key":"k", "user":"u"}`, `{"api_secret": "***", "api_key":"***", "user":"u"}`},
		{`{"access_token":"t","refresh_token":"r","token_type":"Bearer"}`, `{"access_token":"***","refresh_token":"***","token_type":"***"}`},
		{`{"new_password":"x\"y","name":"n"}`, `{"new_password":"***","name":"n"}`},
		{`{"code":"c","code_verifier":"v","key":"reset"}`, `{"code":"***","code_verifier":"***","key":"***"}`},
		// Escaped inside a JSON string argument.
		{`{"doc":"{\"name\":\"u\",\"password\":\"p\"}"}`, `{"doc":"{\"name\":\"u\",\"password\":\"***\"}"}`},
		// Escaped quotes inside a nested value, and two levels of nesting.
		{`{"args":"{\"password\":\"he said \\\"hi\\\" ok\"}"}`, `{"args":"{\"password\":\"***\"}"}`},
		{`{"m":"[\"{\\\"pwd\\\":\\\"p\\\"}\"]"}`, `{"m":"[\"{\\\"pwd\\\":\\\"***\\\"}\"]"}`},
		// Numbers, arrays, objects and \u-escaped keys.
		{`{"pwd":12345,"api_secret":["a","b"],"secret":{"v":"x"},"\u0070assword":"p","n":1}`,
			`{"pwd":"***","api_secret":"***","secret":"***","\u0070assword":"***","n":1}`},
		// The value of frappe.client.set_value names its field apart.
		{`{"doctype":"User","fieldname":"new_password","value":"hunter2"}`, `{"doctype":"User","fieldname":"new_password","value":"***"}`},
		{`{"authorization":"token k:s"}`, `{"authorization":"***"}`},
		// Truncated bodies are still redacted, also when cut inside a value.
		{`{"sid":"abc","rows":[{"x":`, `{"sid":"***","rows":[{"x":`},
		{`{"a":1,"pwd": "trunc`, `{"a":1,"pwd": "***"`},
		{`{"a":1,"doc":"{\"pwd\":\"tr`, `{"a":1,"doc":"{\"pwd\":\"***\"`},
		{`{"secret":{"v":["x",`, `{"secret":"***"`},
		// Non-secret keys that merely contain a secret word's letters stay.
		{`{"description":"keep","postcode":"1000"}`, `{"description":"keep","postcode":"1000"}`},
	}
	for _, c := range cases {
		if got := redactJSON(c.in); got != c.want {
			t.Errorf("redactJSON(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestRedactURLAndHeaders(t *testing.T) {
	if got := redactURL("https://u:p@s.example/x"); strings.Contains(got, ":p@") {
		t.Errorf("redactURL kept the user info password: %s", got)
	}
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
		{"Proxy-Authorization", "Basic abc", "Basic ***"},
		{"X-API-Key", "abc", "***"},
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

func TestPlanBody(t *testing.T) {
	form := planBody([]byte("usr=a&pwd=hunter2"), "application/x-www-form-urlencoded")
	if form != "usr=a&pwd=***" {
		t.Errorf("form body = %v", form)
	}
	got := planBody(map[string]interface{}{
		"s":         `[{"pwd":5}]`,
		"fieldname": "new_password",
		"value":     "hunter2",
	}, "application/json")
	b, _ := json.Marshal(got)
	for _, secret := range []string{"5", "hunter2"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("planBody kept %s: %s", secret, b)
		}
	}
}
