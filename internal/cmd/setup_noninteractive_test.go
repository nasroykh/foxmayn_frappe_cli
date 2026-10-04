package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func mustRead(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func assertMode0600(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func wantCode(t *testing.T, r cliResult, code int) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("exit = %d, want %d (err %v, stderr %q)", r.Code, code, r.Err, r.Stderr)
	}
}

func TestInitAPIKeyStdin(t *testing.T) {
	site := frappetest.New(t)
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	r := runFFC(t, path, frappetest.APISecret+"\n", "init", "--name", "prod", "--url", site.URL,
		"--api-key", frappetest.APIKey, "--api-secret-stdin")
	wantCode(t, r, 0)
	cfg := mustRead(t, path)
	s := cfg.Sites["prod"]
	if cfg.DefaultSite != "prod" || s.URL != site.URL || s.APIKey != frappetest.APIKey || s.APISecret != frappetest.APISecret {
		t.Fatalf("config = %+v", cfg)
	}
	assertMode0600(t, path)
	if _, err := config.Load("", path); err != nil {
		t.Fatal(err)
	}
}

func TestSiteAddPasswordStdinLoginLogout(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	r := runFFC(t, path, frappetest.Password+"\n", "site", "add", "--name", "pw", "--url", site.URL,
		"--username", frappetest.Username, "--password-stdin")
	wantCode(t, r, 0)
	if site.Logins() != 1 || site.Logouts() != 1 {
		t.Fatalf("logins=%d logouts=%d, want 1/1", site.Logins(), site.Logouts())
	}
	cfg := mustRead(t, path)
	if s := cfg.Sites["pw"]; s.Username != frappetest.Username || s.Password != frappetest.Password {
		t.Fatalf("pw site = %+v", s)
	}
	if cfg.DefaultSite != "t" || len(cfg.Sites) != 3 {
		t.Fatalf("other sites disturbed: %+v", cfg)
	}
	assertMode0600(t, path)
}

func TestSiteAddSecretFromEnv(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	cliEnv = map[string]string{"FFC_API_SECRET": frappetest.APISecret}
	defer func() { cliEnv = nil }()
	r := runFFC(t, path, "", "site", "add", "--name", "envsite", "--url", site.URL, "--api-key", frappetest.APIKey)
	wantCode(t, r, 0)
	if s := mustRead(t, path).Sites["envsite"]; s.APISecret != frappetest.APISecret || s.APIKey != frappetest.APIKey {
		t.Fatalf("site = %+v", s)
	}
}

func TestSetupWrongSecret(t *testing.T) {
	site := frappetest.New(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	r := runFFC(t, path, "wrong\n", "init", "--name", "x", "--url", site.URL,
		"--api-key", frappetest.APIKey, "--api-secret-stdin")
	wantCode(t, r, 3)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config written despite bad credentials (stat err %v)", err)
	}
}

func TestSetupMissingURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	r := runFFC(t, path, frappetest.APISecret+"\n", "--no-input", "init", "--name", "x",
		"--api-key", frappetest.APIKey, "--api-secret-stdin")
	wantCode(t, r, 2)
	if !strings.Contains(r.Stderr, "--url") {
		t.Fatalf("stderr %q does not name --url", r.Stderr)
	}
	r = runFFC(t, path, "", "--no-input", "init", "--name", "x", "--url", "http://127.0.0.1:1", "--api-key", "k")
	wantCode(t, r, 2) // no secret source
	r = runFFC(t, path, "p\n", "init", "--name", "x", "--url", "http://127.0.0.1:1", "--api-key", "k", "--username", "u", "--api-secret-stdin")
	wantCode(t, r, 2) // conflicting credential sets
}

func TestSiteAddExistingNeedsForce(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	other := frappetest.New(t)
	args := []string{"site", "add", "--name", "other", "--url", other.URL, "--api-key", frappetest.APIKey, "--api-secret-stdin"}
	r := runFFC(t, path, frappetest.APISecret+"\n", args...)
	wantCode(t, r, 2)
	if got := mustRead(t, path).Sites["other"].URL; got != site.URL {
		t.Fatalf("site replaced without --force: %s", got)
	}
	r = runFFC(t, path, frappetest.APISecret+"\n", append(args, "--force")...)
	wantCode(t, r, 0)
	if got := mustRead(t, path).Sites["other"].URL; got != other.URL {
		t.Fatalf("url = %s, want %s", got, other.URL)
	}
}

func TestInitOverExistingNeedsForce(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	args := []string{"init", "--name", "fresh", "--url", site.URL, "--api-key", frappetest.APIKey, "--api-secret-stdin"}
	r := runFFC(t, path, frappetest.APISecret+"\n", args...)
	wantCode(t, r, 2)
	if cfg := mustRead(t, path); len(cfg.Sites) != 2 {
		t.Fatalf("config changed: %+v", cfg)
	}
	r = runFFC(t, path, frappetest.APISecret+"\n", append(args, "--force")...)
	wantCode(t, r, 0)
	cfg := mustRead(t, path)
	if len(cfg.Sites) != 1 || cfg.DefaultSite != "fresh" {
		t.Fatalf("config = %+v", cfg)
	}
	assertMode0600(t, path)
}

func TestSiteRemoveYesAndNoTerminal(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	r := runFFC(t, path, "", "site", "remove", "other")
	wantCode(t, r, 2)
	if len(mustRead(t, path).Sites) != 2 {
		t.Fatal("config changed without --yes")
	}
	r = runFFC(t, path, "", "site", "remove", "other", "--yes")
	wantCode(t, r, 0)
	cfg := mustRead(t, path)
	if _, ok := cfg.Sites["other"]; ok || len(cfg.Sites) != 1 {
		t.Fatalf("config = %+v", cfg)
	}
	assertMode0600(t, path)
}

func TestSiteRename(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	r := runFFC(t, path, "", "site", "rename", "t", "main")
	wantCode(t, r, 0)
	cfg := mustRead(t, path)
	if _, ok := cfg.Sites["t"]; ok || cfg.Sites["main"].URL != site.URL || cfg.DefaultSite != "main" {
		t.Fatalf("config = %+v", cfg)
	}
	assertMode0600(t, path)
	if _, err := config.Load("", path); err != nil {
		t.Fatal(err)
	}

	// A non-default site leaves default_site alone.
	wantCode(t, runFFC(t, path, "", "site", "rename", "other", "second"), 0)
	if cfg := mustRead(t, path); cfg.DefaultSite != "main" || cfg.Sites["second"].APIKey != frappetest.APIKey {
		t.Fatalf("config = %+v", cfg)
	}

	// Target exists, bad name, unknown source: errors, file unchanged.
	before, _ := os.ReadFile(path)
	if r := runFFC(t, path, "", "site", "rename", "main", "second"); r.Code == 0 {
		t.Fatal("rename onto existing name succeeded")
	}
	wantCode(t, runFFC(t, path, "", "site", "rename", "main", "bad name"), 2)
	if r := runFFC(t, path, "", "site", "rename", "nope", "x"); r.Code == 0 {
		t.Fatal("rename of unknown site succeeded")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("config changed by failed renames")
	}
}

func TestSiteEditURLRechecksCredentials(t *testing.T) {
	site := frappetest.New(t)
	path := fakeConfig(t, site, "apikey")
	second := frappetest.New(t)

	wantCode(t, runFFC(t, path, "", "site", "edit", "t"), 2) // no change flag

	r := runFFC(t, path, "", "site", "edit", "t", "--url", second.URL+"/ignored/path")
	wantCode(t, r, 0)
	if got := mustRead(t, path).Sites["t"].URL; got != second.URL {
		t.Fatalf("url = %s, want %s", got, second.URL)
	}
	if n := len(second.RequestsTo("GET", "/api/method/frappe.auth.get_logged_user")); n != 1 {
		t.Fatalf("new site saw %d credential checks, want 1", n)
	}
	assertMode0600(t, path)

	// An OAuth site cannot move: its client is registered with the old server.
	oauth := fakeConfig(t, site, "oauth")
	before, _ := os.ReadFile(oauth)
	wantCode(t, runFFC(t, oauth, "", "site", "edit", "t", "--url", second.URL), 2)
	if after, _ := os.ReadFile(oauth); string(after) != string(before) {
		t.Fatal("OAuth site edited")
	}

	// A site that rejects the stored credentials is not saved.
	bad := frappetest.New(t)
	bad.Handle("GET /api/method/frappe.auth.get_logged_user", frappetest.ErrorHandler(&frappetest.Error{Status: 401, ExcType: "AuthenticationError", Message: "no"}))
	r = runFFC(t, path, "", "site", "edit", "t", "--url", bad.URL)
	wantCode(t, r, 3)
	if got := mustRead(t, path).Sites["t"].URL; got != second.URL {
		t.Fatalf("url changed to %s despite failed check", got)
	}
}
