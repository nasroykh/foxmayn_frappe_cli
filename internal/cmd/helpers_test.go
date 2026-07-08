package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := atomicWriteFile(path, []byte("hello: world\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Overwrite and confirm no leftover temp files remain (M9).
	if err := atomicWriteFile(path, []byte("hello: again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ffc-tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestUpsertSiteIntoNullSites proves a site is not silently dropped when the
// config's `sites:` key exists but is null/empty (M8).
func TestUpsertSiteIntoNullSites(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := os.WriteFile(path, []byte("default_site: dev\nsites:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	siteYAML := "url: \"https://x.example\"\napi_key: \"k\"\napi_secret: \"s\"\n"
	if err := upsertSiteInConfig(path, "dev", siteYAML); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var cfg struct {
		Sites map[string]map[string]string `yaml:"sites"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sites["dev"]; !ok {
		t.Errorf("site 'dev' was dropped when sites: was null\n%s", raw)
	}
}

// TestWriteConfigSessionYAMLInjection proves a password containing YAML
// metacharacters round-trips correctly through the config parser (M10).
func TestWriteConfigSessionYAMLInjection(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	pw := `p"a\b#c:d`
	if err := writeConfigSession(path, "dev", "https://x.example", "user@example.com", pw); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Sites map[string]struct {
			Password string `yaml:"password"`
		} `yaml:"sites"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("generated config does not parse: %v\n%s", err, raw)
	}
	if got := cfg.Sites["dev"].Password; got != pw {
		t.Errorf("password round-trip = %q, want %q", got, pw)
	}
}
