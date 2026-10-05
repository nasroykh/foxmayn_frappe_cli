package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBulkDeleteFilters(t *testing.T) {
	t.Run("deletes the matches", func(t *testing.T) {
		s := cmdTSite(t)
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "bulk-delete", "-d", "ToDo", "--filters", `{"status":"Open"}`, "--yes")
		if r.Code != 0 {
			t.Fatalf("exit %d: %s", r.Code, r.Stderr)
		}
		for name, want := range map[string]bool{"TD-1": false, "TD-2": true, "TD-3": false} {
			if _, ok := s.Doc("ToDo", name); ok != want {
				t.Errorf("%s exists = %v, want %v", name, ok, want)
			}
		}
	})
	t.Run("asks without --yes", func(t *testing.T) {
		s := cmdTSite(t)
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "bulk-delete", "-d", "ToDo", "--filters", `[["status","=","Open"]]`)
		if r.Code != exitUsage || !strings.Contains(r.Stderr, "pass --yes") {
			t.Errorf("exit %d: %s", r.Code, r.Stderr)
		}
		if _, ok := s.Doc("ToDo", "TD-1"); !ok {
			t.Error("TD-1 deleted without confirmation")
		}
	})
	t.Run("no match", func(t *testing.T) {
		s := cmdTSite(t)
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "bulk-delete", "-d", "ToDo", "--filters", `{"status":"Cancelled"}`)
		if r.Code != 0 || !strings.Contains(r.Stderr, "No ToDo documents match the filters") {
			t.Errorf("exit %d: %s", r.Code, r.Stderr)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		s := cmdTSite(t)
		path := filepath.Join(t.TempDir(), "f.json")
		if err := os.WriteFile(path, []byte(`{"status":"Open"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "bulk-delete", "-d", "ToDo", "--filters", "@"+path, "--dry-run", "--json")
		if r.Code != 0 {
			t.Fatalf("exit %d: %s", r.Code, r.Stderr)
		}
		if n := strings.Count(r.Stdout, `"DELETE"`); n != 2 {
			t.Errorf("%d planned deletes, want 2: %s", n, r.Stdout)
		}
		if _, ok := s.Doc("ToDo", "TD-1"); !ok {
			t.Error("the dry run deleted TD-1")
		}
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"with names", []string{"--filters", `{"status":"Open"}`, "--names", "TD-1"}, "none of the others can be"},
		{"bad filters", []string{"--filters", `"Open"`}, "--filters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := cmdTSite(t)
			r := runFFC(t, fakeConfig(t, s, "apikey"), "", append([]string{"bulk-delete", "-d", "ToDo", "--yes"}, tc.args...)...)
			if r.Code != exitUsage || !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("exit %d: %s", r.Code, r.Stderr)
			}
			if _, ok := s.Doc("ToDo", "TD-1"); !ok {
				t.Error("TD-1 deleted")
			}
		})
	}
}

func TestBulkUpdateFilters(t *testing.T) {
	t.Run("sets the fields on the matches", func(t *testing.T) {
		s := cmdTSite(t)
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "bulk-update", "-d", "ToDo",
			"--filters", `{"status":"Open"}`, "--set", `{"status":"Closed","priority":9}`, "--yes")
		if r.Code != 0 {
			t.Fatalf("exit %d: %s", r.Code, r.Stderr)
		}
		for _, name := range []string{"TD-1", "TD-3"} {
			doc, _ := s.Doc("ToDo", name)
			if doc["status"] != "Closed" {
				t.Errorf("%s = %v", name, doc)
			}
		}
		if doc, _ := s.Doc("ToDo", "TD-2"); doc["priority"] == float64(9) {
			t.Errorf("TD-2 changed: %v", doc)
		}
	})
	t.Run("asks without --yes", func(t *testing.T) {
		s := cmdTSite(t)
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "bulk-update", "-d", "ToDo", "--filters", `{"status":"Open"}`, "--set", `{"status":"Closed"}`)
		if r.Code != exitUsage || !strings.Contains(r.Stderr, "pass --yes") {
			t.Errorf("exit %d: %s", r.Code, r.Stderr)
		}
		if doc, _ := s.Doc("ToDo", "TD-1"); doc["status"] != "Open" {
			t.Error("TD-1 changed without confirmation")
		}
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"filters without set", []string{"--filters", `{"status":"Open"}`}, "--filters and --set go together"},
		{"set without filters", []string{"--set", `{"status":"Closed"}`}, "--filters and --set go together"},
		{"set name", []string{"--filters", `{}`, "--set", `{"name":"x"}`}, "--set cannot change name"},
		{"empty set", []string{"--filters", `{}`, "--set", `{}`}, "at least one field"},
		{"yes alone", []string{"--data", `[{"name":"TD-1","status":"Closed"}]`, "--yes"}, "--yes applies to --filters only"},
		{"with data", []string{"--filters", `{}`, "--set", `{"a":1}`, "--data", `[]`}, "none of the others can be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := cmdTSite(t)
			r := runFFC(t, fakeConfig(t, s, "apikey"), "", append([]string{"bulk-update", "-d", "ToDo"}, tc.args...)...)
			if r.Code != exitUsage || !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("exit %d: %s", r.Code, r.Stderr)
			}
		})
	}
}
