package cmd

import (
	"encoding/json"
	"testing"
)

func TestDocName(t *testing.T) {
	tests := []struct {
		in   interface{}
		want string
		ok   bool
	}{
		{"SINV-0001", "SINV-0001", true},
		{"", "", false},
		{float64(42), "42", true}, // integer-named DocTypes arrive as JSON numbers (L27)
		{json.Number("7"), "7", true},
		{nil, "", false},
		{true, "", false},
	}
	for _, tt := range tests {
		got, ok := docName(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("docName(%#v) = (%q,%v), want (%q,%v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestModuleFilter(t *testing.T) {
	if f, _ := moduleFilter(""); f != "" {
		t.Errorf("moduleFilter(\"\") = %q, want empty", f)
	}
	// A value containing a quote must be JSON-escaped, not break the filter (L28).
	f, err := moduleFilter(`Ac"counts`)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(f), &m); err != nil {
		t.Fatalf("moduleFilter produced invalid JSON %q: %v", f, err)
	}
	if m["module"] != `Ac"counts` {
		t.Errorf("module = %q, want %q", m["module"], `Ac"counts`)
	}
}
