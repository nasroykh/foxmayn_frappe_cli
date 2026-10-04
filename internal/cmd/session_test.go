package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// passwordSite is a fake Frappe site with username/password auth that counts
// logins and logouts, so tests can prove every CLI session is ended.
type passwordSite struct {
	logins, logouts atomic.Int32
}

func (p *passwordSite) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/method/login":
			n := p.logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: fmt.Sprintf("sid%d", n), Path: "/"})
			_, _ = w.Write([]byte(`{"message":"Logged In"}`))
		case "/api/method/logout":
			p.logouts.Add(1)
			_, _ = w.Write([]byte(`{}`))
		case "/api/method/frappe.ping":
			_, _ = w.Write([]byte(`{"message":"pong"}`))
		case "/api/resource/ToDo/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"exc_type":"DoesNotExistError"}`))
		case "/api/resource/ToDo/a", "/api/resource/ToDo/b":
			_, _ = w.Write([]byte(`{"data":{"name":"a"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runCLI runs the root command with args against a temporary config whose
// default site is url, and returns what the command wrote to stdout. Global
// flags are reset afterwards.
func runCLI(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FFC_NO_UPDATE_CHECK", "1")
	for _, k := range []string{"FFC_API_KEY", "FFC_API_SECRET", "FFC_URL"} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    username: u\n    password: p\n  other:\n    url: %q\n    api_key: k\n    api_secret: s\n", url, url)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = out
	defer func() {
		os.Stdout = stdout
		_ = out.Close()
		configPath, siteName, jsonOutput = "", "", false
	}()

	rootCmd.SetArgs(append([]string{"--config", cfg, "--json"}, args...))
	runErr := rootCmd.ExecuteContext(context.Background())
	b, _ := os.ReadFile(out.Name())
	return string(b), runErr
}

func TestCLIEndsPasswordSessions(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"get-doc", []string{"get-doc", "-d", "ToDo", "-n", "a"}, false},
		{"get-doc error", []string{"get-doc", "-d", "ToDo", "-n", "missing"}, true},
		{"ping", []string{"ping"}, false},
		{"bulk-delete", []string{"bulk-delete", "-d", "ToDo", "--names", "a,b", "--yes"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			site := &passwordSite{}
			_, err := runCLI(t, site.serve(t), tc.args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if in, out := site.logins.Load(), site.logouts.Load(); in != 1 || out != 1 {
				t.Errorf("logins = %d, logouts = %d; want 1 and 1", in, out)
			}
		})
	}
}

func TestSiteListJSONDefaultIsBool(t *testing.T) {
	out, err := runCLI(t, "http://127.0.0.1:1", "site", "list")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := map[string]interface{}{}
	for _, r := range rows {
		got[r["name"].(string)] = r["default"]
	}
	if got["t"] != true || got["other"] != false {
		t.Errorf("default flags = %v, want t=true other=false", got)
	}
}
