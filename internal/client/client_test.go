package client

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResourcePathEscaping(t *testing.T) {
	tests := []struct {
		doctype string
		name    []string
		want    string
	}{
		{"ToDo", nil, "/api/resource/ToDo"},
		{"Sales Invoice", nil, "/api/resource/Sales%20Invoice"},
		{"Sales Invoice", []string{"SINV-0001"}, "/api/resource/Sales%20Invoice/SINV-0001"},
		// Naming series with slashes must not inject extra path segments (H2).
		{"Sales Invoice", []string{"INV/2025/001"}, "/api/resource/Sales%20Invoice/INV%2F2025%2F001"},
		// '#' must not become a fragment that silently drops (H2).
		{"Task", []string{"TD-1#x"}, "/api/resource/Task/TD-1%23x"},
		{"Task", []string{"a?b"}, "/api/resource/Task/a%3Fb"},
	}
	for _, tt := range tests {
		if got := resourcePath(tt.doctype, tt.name...); got != tt.want {
			t.Errorf("resourcePath(%q, %v) = %q, want %q", tt.doctype, tt.name, got, tt.want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"<b>hi</b> there":      "hi there",
		"plain":                "plain",
		`<a href="x">link</a>`: "link",
		"  <p>trim me</p>  ":   "trim me",
	}
	for in, want := range cases {
		if got := stripHTML(in); got != want {
			t.Errorf("stripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFrappeUserMessage(t *testing.T) {
	// Build Frappe's double-encoded _server_messages payload with two messages,
	// one containing HTML.
	inner1, _ := json.Marshal(map[string]string{"message": "<b>Field</b> not permitted"})
	inner2, _ := json.Marshal(map[string]string{"message": "Second error"})
	arr, _ := json.Marshal([]string{string(inner1), string(inner2)})
	body, _ := json.Marshal(map[string]string{"_server_messages": string(arr)})

	got := frappeUserMessage(body)
	// All messages are surfaced (L13), joined, HTML-stripped.
	if !strings.Contains(got, "Field not permitted") || !strings.Contains(got, "Second error") {
		t.Errorf("frappeUserMessage = %q, want both messages", got)
	}
	if strings.Contains(got, "<b>") {
		t.Errorf("frappeUserMessage = %q, HTML not stripped", got)
	}

	// Falls back to the exception field when there are no server messages.
	exBody, _ := json.Marshal(map[string]string{"exception": "frappe.exceptions.ValidationError: bad value"})
	if got := frappeUserMessage(exBody); got != "bad value" {
		t.Errorf("frappeUserMessage(exception) = %q, want %q", got, "bad value")
	}

	// Non-JSON body yields no message.
	if got := frappeUserMessage([]byte("<html>oops</html>")); got != "" {
		t.Errorf("frappeUserMessage(non-json) = %q, want empty", got)
	}
}

func TestSidFromCookies(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "system_user", Value: "yes"},
		{Name: "sid", Value: "abc123"},
	}
	if got := sidFromCookies(cookies); got != "abc123" {
		t.Errorf("sidFromCookies = %q, want %q", got, "abc123")
	}
	if got := sidFromCookies(nil); got != "" {
		t.Errorf("sidFromCookies(nil) = %q, want empty", got)
	}
}
