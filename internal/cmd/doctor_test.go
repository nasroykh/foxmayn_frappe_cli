package cmd

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
)

// doctorTHome gives the test its own home (the MCP state and the update
// check live there) and cache.
func doctorTHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return home
}

// doctorTRun runs `ffc doctor --json` and indexes the checks by id.
func doctorTRun(t *testing.T, cfg string, args ...string) (map[string]map[string]interface{}, cliResult) {
	t.Helper()
	r := runFFC(t, cfg, "", append([]string{"--json", "doctor"}, args...)...)
	checks := map[string]map[string]interface{}{}
	for _, c := range cmdTRows(t, r) {
		checks[fmt.Sprint(c["check"])] = c
	}
	return checks, r
}

func doctorTWant(t *testing.T, checks map[string]map[string]interface{}, id, status string) map[string]interface{} {
	t.Helper()
	c, ok := checks[id]
	if !ok {
		t.Fatalf("no %s check in %v", id, checkIDs(checks))
	}
	if c["status"] != status {
		t.Fatalf("%s is %v (%v), want %s", id, c["status"], c["message"], status)
	}
	return c
}

func checkIDs(checks map[string]map[string]interface{}) []string {
	var ids []string
	for id := range checks {
		ids = append(ids, id)
	}
	return ids
}

// doctorTConfig writes a 0600 config in its own 0700 directory.
func doctorTConfig(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ffc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoctorHealthy(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
	checks, r := doctorTRun(t, cfg)
	if r.Err != nil || r.Code != 0 {
		t.Fatalf("exit %d, %v\n%s", r.Code, r.Err, r.Stdout)
	}
	want := []string{"config.file", "config.parse", "config.lock", "net.tls", "net.reachable",
		"net.clock", "auth.valid", "server.versions", "server.api_v2", "mcp.daemon", "update.check"}
	if runtime.GOOS != "windows" { // permission bits are not checked on Windows
		want = append(want, "config.dir")
	}
	for _, id := range want {
		c := doctorTWant(t, checks, id, "pass")
		if _, ok := c["hint"]; !ok {
			t.Errorf("%s has no hint key", id)
		}
	}
	if msg := checks["auth.valid"]["message"].(string); !strings.Contains(msg, "Administrator") || !strings.Contains(msg, "api_key") {
		t.Errorf("auth.valid: %s", msg)
	}
	if msg := checks["server.versions"]["message"].(string); !strings.Contains(msg, "frappe 16.36.1") {
		t.Errorf("server.versions: %s", msg)
	}
	if _, has := checks["auth.oauth_token"]; has {
		t.Error("auth.oauth_token belongs to OAuth sites only")
	}
	// The array's shape is fixed: exactly these four keys.
	for _, c := range cmdTRows(t, r) {
		if len(c) != 4 {
			t.Errorf("check %v has %d keys, want check, status, message, hint", c, len(c))
		}
	}
	if strings.Contains(r.Stdout+r.Stderr, frappetest.APISecret) {
		t.Error("the API secret was printed")
	}
}

func TestDoctorHumanOutput(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: k\n    api_secret: s\n", s.URL))
	if err := os.Chmod(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	r := runFFC(t, cfg, "", "doctor")
	want := []string{"config.file", "net.reachable"}
	if runtime.GOOS != "windows" {
		want = append(want, "chmod 600")
	}
	cmdTHas(t, r.Stdout, want...)
	if r.Code != 1 {
		t.Errorf("exit %d, want 1", r.Code)
	}
}

func TestDoctorBadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	doctorTHome(t)
	s := frappetest.New(t)
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
	if err := os.Chmod(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	checks, r := doctorTRun(t, cfg)
	f := doctorTWant(t, checks, "config.file", "fail")
	if !strings.Contains(f["hint"].(string), "chmod 600 "+cfg) || !strings.Contains(f["message"].(string), "0644") {
		t.Errorf("config.file: %v", f)
	}
	d := doctorTWant(t, checks, "config.dir", "warn")
	if !strings.Contains(d["hint"].(string), "chmod 700") {
		t.Errorf("config.dir: %v", d)
	}
	// The rest of the checks still ran.
	doctorTWant(t, checks, "auth.valid", "pass")
	if r.Code != 1 || r.Err == nil || !strings.Contains(r.Err.Error(), "1 check(s) failed") {
		t.Errorf("exit %d, %v: a failed check must exit 1", r.Code, r.Err)
	}
}

func TestDoctorWarningsExitZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	doctorTHome(t)
	s := frappetest.New(t)
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
	if err := os.Chmod(filepath.Dir(cfg), 0o750); err != nil {
		t.Fatal(err)
	}
	checks, r := doctorTRun(t, cfg)
	doctorTWant(t, checks, "config.dir", "warn")
	if r.Code != 0 {
		t.Errorf("a warning must not fail the run: exit %d", r.Code)
	}
}

func TestDoctorUnreachable(t *testing.T) {
	doctorTHome(t)
	cfg := doctorTConfig(t, "default_site: t\nsites:\n  t:\n    url: http://127.0.0.1:1\n    api_key: k\n    api_secret: s\n")
	checks, r := doctorTRun(t, cfg)
	c := doctorTWant(t, checks, "net.reachable", "fail")
	if !strings.Contains(c["message"].(string), "no answer from http://127.0.0.1:1") || c["hint"] == "" {
		t.Errorf("net.reachable: %v", c)
	}
	// What needs the site is not reported as if it had run.
	for _, id := range []string{"auth.valid", "server.versions", "server.api_v2", "net.clock"} {
		if _, ok := checks[id]; ok {
			t.Errorf("%s ran against an unreachable site", id)
		}
	}
	// The local checks still run.
	doctorTWant(t, checks, "config.file", "pass")
	doctorTWant(t, checks, "mcp.daemon", "pass")
	if r.Code != 1 {
		t.Errorf("exit %d, want 1", r.Code)
	}
}

func TestDoctorNotAFrappeSite(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	s.Handle("GET /api/method/frappe.ping", frappetest.HTMLPage(200))
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: k\n    api_secret: s\n", s.URL))
	checks, _ := doctorTRun(t, cfg)
	c := doctorTWant(t, checks, "net.reachable", "fail")
	if !strings.Contains(c["message"].(string), "not like a Frappe site") {
		t.Errorf("net.reachable: %v", c)
	}
}

func TestDoctorClockSkew(t *testing.T) {
	doctorTHome(t)
	for _, c := range []struct {
		name   string
		offset time.Duration
		status string
		want   string
	}{
		{"in step", 0, "pass", "agrees"},
		{"a few seconds is noise", 20 * time.Second, "pass", ""},
		{"server behind by 3 minutes", -3 * time.Minute, "warn", "ahead of"},
		{"server ahead by 2 hours", 2 * time.Hour, "fail", "behind the server"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := frappetest.New(t)
			offset := c.offset
			s.Handle("GET /api/method/frappe.ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Date", time.Now().Add(offset).UTC().Format(http.TimeFormat))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":"pong"}`))
			}))
			cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
			checks, r := doctorTRun(t, cfg)
			got := doctorTWant(t, checks, "net.clock", c.status)
			if !strings.Contains(got["message"].(string), c.want) {
				t.Errorf("message %q lacks %q", got["message"], c.want)
			}
			if c.status == "fail" && (r.Code != 1 || !strings.Contains(got["hint"].(string), "NTP")) {
				t.Errorf("exit %d, hint %q", r.Code, got["hint"])
			}
		})
	}
	// No Date header at all is a warning, not a crash.
	d := &doctor{}
	d.checkClock("", time.Now())
	d.checkClock("yesterday-ish", time.Now())
	if d.checks[0].Status != checkWarn || d.checks[1].Status != checkWarn {
		t.Errorf("checks = %+v", d.checks)
	}
}

func TestDoctorConfigProblems(t *testing.T) {
	doctorTHome(t)
	t.Run("unparsable file", func(t *testing.T) {
		// yaml.v3 quotes the offending value in its error; it can be a secret.
		cfg := doctorTConfig(t, "sites:\n  t:\n    url: http://x\n    token_expiry: SUPERSECRETVALUE\n")
		checks, r := doctorTRun(t, cfg)
		c := doctorTWant(t, checks, "config.parse", "fail")
		if strings.Contains(r.Stdout+r.Stderr, "SUPERSECRETVALUE") {
			t.Errorf("a config value was printed: %v", c["message"])
		}
		if _, ran := checks["net.reachable"]; ran {
			t.Error("network checks must not run without a site")
		}
	})
	t.Run("unknown site", func(t *testing.T) {
		cfg := doctorTConfig(t, "default_site: nope\nsites:\n  t:\n    url: http://x\n")
		checks, _ := doctorTRun(t, cfg)
		c := doctorTWant(t, checks, "config.parse", "fail")
		if !strings.Contains(c["message"].(string), `"nope"`) {
			t.Errorf("message %v", c["message"])
		}
	})
	t.Run("explicit config that does not exist", func(t *testing.T) {
		checks, r := doctorTRun(t, filepath.Join(t.TempDir(), "missing.yaml"))
		doctorTWant(t, checks, "config.file", "fail")
		if r.Code != 1 {
			t.Errorf("exit %d", r.Code)
		}
	})
	t.Run("a URL that is not http", func(t *testing.T) {
		cfg := doctorTConfig(t, "default_site: t\nsites:\n  t:\n    url: ftp://example.com\n    api_key: k\n    api_secret: s\n")
		checks, _ := doctorTRun(t, cfg)
		doctorTWant(t, checks, "net.reachable", "fail")
	})
	t.Run("a password in the URL is not echoed", func(t *testing.T) {
		cfg := doctorTConfig(t, "default_site: t\nsites:\n  t:\n    url: http://user:hunter2pw@127.0.0.1:1\n    api_key: k\n    api_secret: s\n")
		_, r := doctorTRun(t, cfg)
		if strings.Contains(r.Stdout+r.Stderr, "hunter2pw") {
			t.Error("the URL's password was printed")
		}
	})
}

func TestDoctorLock(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
	lock := cfg + ".lock"
	if err := os.WriteFile(lock, []byte("1-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks, r := doctorTRun(t, cfg)
	doctorTWant(t, checks, "config.lock", "pass") // fresh: another ffc is writing
	if r.Code != 0 {
		t.Errorf("exit %d", r.Code)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	checks, r = doctorTRun(t, cfg)
	c := doctorTWant(t, checks, "config.lock", "warn")
	// The hint names the resolved path: on Windows that expands 8.3 short
	// names (RUNNER~1) in the temp directory.
	resolved, err := filepath.EvalSymlinks(lock)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c["hint"].(string), resolved) {
		t.Errorf("hint %q must name the lock file", c["hint"])
	}
	if _, err := os.Stat(lock); err != nil || r.Code != 0 {
		t.Errorf("doctor must not remove the lock (err %v, exit %d)", err, r.Code)
	}
}

func TestDoctorAuth(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	t.Run("rejected API key", func(t *testing.T) {
		cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: wrong\n    api_secret: wrong\n", s.URL))
		checks, r := doctorTRun(t, cfg)
		c := doctorTWant(t, checks, "auth.valid", "fail")
		if !strings.Contains(c["hint"].(string), "credentials") || r.Code != 1 {
			t.Errorf("%v (exit %d)", c, r.Code)
		}
		if _, ran := checks["server.versions"]; ran {
			t.Error("server checks must not run without a login")
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    username: Administrator\n    password: wrongpw\n", s.URL))
		checks, r := doctorTRun(t, cfg)
		doctorTWant(t, checks, "auth.valid", "fail")
		if strings.Contains(r.Stdout+r.Stderr, "wrongpw") {
			t.Error("the password was printed")
		}
	})
	t.Run("password login is closed again", func(t *testing.T) {
		before := s.Logouts()
		cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    username: %s\n    password: %s\n", s.URL, frappetest.Username, frappetest.Password))
		checks, _ := doctorTRun(t, cfg)
		doctorTWant(t, checks, "auth.valid", "pass")
		if s.Logouts() != before+1 {
			t.Error("doctor left its session open")
		}
	})
	t.Run("Guest", func(t *testing.T) {
		s := frappetest.New(t)
		s.SetUser("Guest")
		cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
		checks, _ := doctorTRun(t, cfg)
		doctorTWant(t, checks, "auth.valid", "fail")
	})
}

func TestDoctorOAuthToken(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	site := func(extra string) string {
		return doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    access_token: %q\n%s", s.URL, frappetest.Token, extra))
	}
	soon := time.Now().Add(2 * time.Hour).Unix()
	checks, _ := doctorTRun(t, site(fmt.Sprintf("    refresh_token: r\n    token_expiry: %d\n", soon)))
	c := doctorTWant(t, checks, "auth.oauth_token", "pass")
	if !strings.Contains(c["message"].(string), "expires in") {
		t.Errorf("%v", c)
	}
	doctorTWant(t, checks, "auth.valid", "pass")

	// About to expire, and nothing to renew it with.
	checks, _ = doctorTRun(t, site(fmt.Sprintf("    token_expiry: %d\n", time.Now().Add(20*time.Minute).Unix())))
	doctorTWant(t, checks, "auth.oauth_token", "warn")

	// Expired and no refresh token: the user must sign in again.
	checks, r := doctorTRun(t, site(fmt.Sprintf("    token_expiry: %d\n", time.Now().Add(-time.Hour).Unix())))
	c = doctorTWant(t, checks, "auth.oauth_token", "fail")
	if !strings.Contains(c["hint"].(string), "ffc site add --oauth") || r.Code != 1 {
		t.Errorf("%v (exit %d)", c, r.Code)
	}

	// Expired with a refresh token: doctor does not renew it. It says the next
	// command will, and sends nothing that changes state.
	n := len(s.Requests())
	checks, r = doctorTRun(t, site(fmt.Sprintf("    refresh_token: r\n    oauth_client_id: x\n    token_expiry: %d\n", time.Now().Add(-time.Hour).Unix())))
	c = doctorTWant(t, checks, "auth.oauth_token", "warn")
	if !strings.Contains(c["message"].(string), "the next command refreshes it") {
		t.Errorf("%v", c)
	}
	doctorTWant(t, checks, "auth.valid", "warn")
	if r.Code != 0 {
		t.Errorf("an expired token with a refresh token is a warning: exit %d", r.Code)
	}
	for _, req := range s.Requests()[n:] {
		if req.Method != http.MethodGet || strings.Contains(req.Path, "oauth") {
			t.Errorf("doctor sent %s %s: it must not renew the token", req.Method, req.Path)
		}
	}
	if strings.Contains(fmt.Sprint(c), frappetest.Token) {
		t.Error("the token was printed")
	}
}

func TestDoctorServer(t *testing.T) {
	doctorTHome(t)
	cfgFor := func(s *frappetest.Site) string {
		return doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n", s.URL, frappetest.APIKey, frappetest.APISecret))
	}
	t.Run("old Frappe", func(t *testing.T) {
		s := frappetest.New(t)
		s.SetApps(map[string]frappetest.App{"frappe": {Version: "14.80.0"}})
		s.DisableV2()
		checks, r := doctorTRun(t, cfgFor(s))
		c := doctorTWant(t, checks, "server.versions", "warn")
		if !strings.Contains(c["hint"].(string), "v14") {
			t.Errorf("%v", c)
		}
		// No v2 on v14 is expected.
		doctorTWant(t, checks, "server.api_v2", "pass")
		if r.Code != 0 {
			t.Errorf("exit %d", r.Code)
		}
	})
	t.Run("v16 without v2", func(t *testing.T) {
		s := frappetest.New(t)
		s.DisableV2()
		checks, _ := doctorTRun(t, cfgFor(s))
		doctorTWant(t, checks, "server.api_v2", "warn")
	})
	t.Run("versions unreadable", func(t *testing.T) {
		s := frappetest.New(t)
		s.HandleMethod("frappe.utils.change_log.get_versions", func(*http.Request, map[string]interface{}) (interface{}, error) {
			return nil, frappetest.Permission("no")
		})
		checks, r := doctorTRun(t, cfgFor(s))
		doctorTWant(t, checks, "server.versions", "warn")
		if r.Code != 0 {
			t.Errorf("exit %d", r.Code)
		}
	})
	t.Run("live, never through the cache", func(t *testing.T) {
		s := frappetest.New(t)
		cfg := cfgFor(s)
		site := &config.SiteConfig{Name: "t", URL: s.URL}
		path, _ := serverCachePath(site)
		_ = os.Remove(path) // the cache directory is shared: another test (or -count) may have filled it
		doctorTRun(t, cfg)
		doctorTRun(t, cfg)
		if versionCalls(s) != 2 {
			t.Errorf("every run reads the versions: %d calls", versionCalls(s))
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("doctor wrote the version cache")
		}
		// A cache another command wrote is neither used nor touched.
		old := &client.ServerInfo{URL: s.URL, FetchedAt: time.Now().UTC(), Apps: map[string]client.AppVersion{"frappe": {Version: "1.2.3"}}}
		writeServerCache(site, old)
		before, _ := os.ReadFile(path)
		checks, _ := doctorTRun(t, cfg)
		if msg := checks["server.versions"]["message"].(string); !strings.Contains(msg, "16.36.1") || strings.Contains(msg, "1.2.3") {
			t.Errorf("server.versions used the cache: %s", msg)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Error("doctor rewrote the version cache")
		}
		if r := runFFC(t, cfg, "", "doctor", "--refresh"); r.Code != exitUsage {
			t.Errorf("doctor has no --refresh: exit %d", r.Code)
		}
	})
}

// tlsTSite is an https site that answers frappe.ping.
func tlsTSite(t *testing.T) (*httptest.Server, *config.SiteConfig) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"pong"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &config.SiteConfig{URL: srv.URL}
}

func TestDoctorTLS(t *testing.T) {
	srv, cfg := tlsTSite(t)
	ctx := t.Context()
	t.Cleanup(func() { doctorTLSRoots = nil; doctorNow = time.Now })

	// The test certificate is signed by nobody the system trusts.
	d := &doctor{}
	if d.checkNetwork(ctx, cfg) {
		t.Error("an untrusted certificate must make the site unreachable")
	}
	if d.checks[0].Check != "net.tls" || d.checks[0].Status != checkFail || !strings.Contains(d.checks[0].Message, "TLS check failed") || !strings.Contains(d.checks[0].Hint, "no option to skip") {
		t.Errorf("untrusted: %+v", d.checks)
	}

	// Trusted, and far from expiry: the certificate comes from the probe's own response.
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	doctorTLSRoots = pool
	d = &doctor{}
	if !d.checkNetwork(ctx, cfg) {
		t.Fatalf("trusted: %+v", d.checks)
	}
	if d.checks[0].Check != "net.tls" || d.checks[0].Status != checkPass || !strings.Contains(d.checks[0].Message, "certificate valid until") {
		t.Errorf("trusted: %+v", d.checks[0])
	}

	// Close to its end.
	end := srv.Certificate().NotAfter
	doctorNow = func() time.Time { return end.Add(-3 * 24 * time.Hour) }
	d = &doctor{}
	d.checkNetwork(ctx, cfg)
	if d.checks[0].Status != checkWarn || !strings.Contains(d.checks[0].Message, "in 3 days") {
		t.Errorf("expiring: %+v", d.checks[0])
	}

	// Nothing listens.
	srv.Close()
	d = &doctor{}
	d.checkNetwork(ctx, cfg)
	if d.checks[0].Status != checkFail || !strings.Contains(d.checks[0].Message, "cannot open a connection") {
		t.Errorf("closed: %+v", d.checks[0])
	}
}

// The probe's one request carries the certificate: the server sees exactly one.
func TestDoctorTLSUsesOneConnection(t *testing.T) {
	var conns int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":"pong"}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns++
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	doctorTLSRoots = pool
	t.Cleanup(func() { doctorTLSRoots = nil })
	d := &doctor{}
	if !d.checkNetwork(t.Context(), &config.SiteConfig{URL: srv.URL}) {
		t.Fatalf("%+v", d.checks)
	}
	if conns != 1 {
		t.Errorf("the probe opened %d connections, want 1 (no separate TLS dial)", conns)
	}
}

// A site that redirects fails the check: reads follow the redirect, writes
// fail with "site redirected".
func TestDoctorRedirect(t *testing.T) {
	doctorTHome(t)
	s := frappetest.New(t)
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"pong"}`))
	}))
	t.Cleanup(moved.Close)
	s.Handle("GET /api/method/frappe.ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, moved.URL+"/api/method/frappe.ping", http.StatusMovedPermanently)
	}))
	cfg := doctorTConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: k\n    api_secret: s\n", s.URL))
	checks, r := doctorTRun(t, cfg)
	c := doctorTWant(t, checks, "net.reachable", "fail")
	if !strings.Contains(c["message"].(string), "site redirected") || !strings.Contains(c["hint"].(string), moved.URL) {
		t.Errorf("net.reachable: %v", c)
	}
	if strings.Contains(c["hint"].(string), "frappe.ping") {
		t.Errorf("the hint must name the site, not the probe path: %v", c["hint"])
	}
	if _, ran := checks["auth.valid"]; ran || r.Code != 1 {
		t.Errorf("auth ran: %v, exit %d", ran, r.Code)
	}
}

func TestDoctorPlainHTTP(t *testing.T) {
	for host, want := range map[string]string{
		"http://localhost:8000":   checkPass,
		"http://127.0.0.1":        checkPass,
		"http://[::1]:8000":       checkPass,
		"http://erp.localhost":    checkPass,
		"http://erp.example.com":  checkWarn,
		"http://192.168.1.20:80":  checkWarn,
		"http://localhost.evil.x": checkWarn,
	} {
		u, _ := url.Parse(host)
		d := &doctor{}
		d.checkTLS(u, nil, nil)
		if d.checks[0].Status != want {
			t.Errorf("%s: %+v, want %s", host, d.checks[0], want)
		}
		if want == checkWarn && !strings.Contains(d.checks[0].Hint, "https://") {
			t.Errorf("%s: hint %q", host, d.checks[0].Hint)
		}
	}
}

func TestDoctorMCPDaemon(t *testing.T) {
	home := doctorTHome(t)
	d := &doctor{}
	d.checkMCP()
	if d.checks[0].Status != checkPass {
		t.Fatalf("no daemon: %+v", d.checks[0])
	}

	// A state file naming a process that is gone.
	cmd := exec.Command(os.Args[0], "-test.run=NONE")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "ffc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPState(mcpState{PID: cmd.Process.Pid, Port: 1, Site: "t", Instance: "x", Token: "TOPSECRETTOKEN"}); err != nil {
		t.Fatal(err)
	}
	d = &doctor{}
	d.checkMCP()
	if d.checks[0].Status != checkWarn || !strings.Contains(d.checks[0].Message, "stale") {
		t.Errorf("stale: %+v", d.checks[0])
	}
	if _, err := os.Stat(mcpStatePath()); err != nil {
		t.Error("doctor must not remove the state file")
	}

	// A live process that does not answer on its port.
	if err := writeMCPState(mcpState{PID: os.Getpid(), Port: 1, Site: "t", Instance: "x", Token: "TOPSECRETTOKEN"}); err != nil {
		t.Fatal(err)
	}
	d = &doctor{}
	d.checkMCP()
	if d.checks[0].Status != checkWarn || !strings.Contains(d.checks[0].Message, "does not answer") || !strings.Contains(d.checks[0].Hint, "--force") {
		t.Errorf("unresponsive: %+v", d.checks[0])
	}

	// A damaged state file.
	if err := os.WriteFile(mcpStatePath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	d = &doctor{}
	d.checkMCP()
	if d.checks[0].Status != checkWarn {
		t.Errorf("damaged: %+v", d.checks[0])
	}
	for _, c := range d.checks {
		if strings.Contains(fmt.Sprint(c), "TOPSECRETTOKEN") {
			t.Error("the bearer token was printed")
		}
	}
}

// A running server whose state file is open to other users leaks its token.
func TestDoctorMCPStateFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	doctorTHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"service":%q,"instance":"abc"}`, mcpServiceName)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	var port int
	fmt.Sscan(u.Port(), &port)
	if err := writeMCPState(mcpState{PID: os.Getpid(), Port: port, Site: "t", Instance: "abc", Token: "TOPSECRETTOKEN"}); err != nil {
		t.Fatal(err)
	}
	d := &doctor{}
	d.checkMCP()
	if len(d.checks) != 1 || d.checks[0].Status != checkPass || !strings.Contains(d.checks[0].Message, "running") {
		t.Fatalf("running: %+v", d.checks)
	}
	if err := os.Chmod(mcpStatePath(), 0o644); err != nil {
		t.Fatal(err)
	}
	d = &doctor{}
	d.checkMCP()
	if len(d.checks) != 2 || d.checks[1].Check != "mcp.state_file" || d.checks[1].Status != checkFail {
		t.Fatalf("a world-readable token file must fail: %+v", d.checks)
	}
}

// The token file's mode is checked whenever the file exists, not only for a
// server that is running: a stale or wedged one still holds the token.
func TestDoctorMCPStateFileModeWithoutServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	home := doctorTHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".config", "ffc"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A live process that does not answer, and a state file open to everyone.
	if err := writeMCPState(mcpState{PID: os.Getpid(), Port: 1, Site: "t", Instance: "x", Token: "TOPSECRETTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mcpStatePath(), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &doctor{}
	d.checkMCP()
	if len(d.checks) != 2 || d.checks[0].Status != checkWarn || d.checks[1].Check != "mcp.state_file" || d.checks[1].Status != checkFail {
		t.Fatalf("unresponsive server: %+v", d.checks)
	}
	// The same for a damaged file.
	if err := os.WriteFile(mcpStatePath(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mcpStatePath(), 0o644); err != nil {
		t.Fatal(err)
	}
	d = &doctor{}
	d.checkMCP()
	if len(d.checks) != 2 || d.checks[1].Check != "mcp.state_file" || d.checks[1].Status != checkFail {
		t.Fatalf("damaged file: %+v", d.checks)
	}
	// A private file raises nothing.
	if err := os.Chmod(mcpStatePath(), 0o600); err != nil {
		t.Fatal(err)
	}
	d = &doctor{}
	d.checkMCP()
	if len(d.checks) != 1 {
		t.Errorf("0600: %+v", d.checks)
	}
	for _, c := range d.checks {
		if strings.Contains(fmt.Sprint(c), "TOPSECRETTOKEN") {
			t.Error("the bearer token was printed")
		}
	}
}

func TestDoctorUpdateCheck(t *testing.T) {
	home := doctorTHome(t)
	t.Setenv("FFC_NO_UPDATE_CHECK", "")
	old := version.Version
	t.Cleanup(func() { version.Version = old })

	d := &doctor{}
	d.checkUpdate()
	if d.checks[0].Status != checkPass || !strings.Contains(d.checks[0].Message, "no update check recorded") {
		t.Errorf("nothing recorded: %+v", d.checks[0])
	}

	dir := filepath.Join(home, ".config", "ffc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(latest string) {
		body := fmt.Sprintf(`{"checked_at":%q,"latest":%q}`, time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339), latest)
		if err := os.WriteFile(filepath.Join(dir, ".update_check.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ current, latest, status, want string }{
		{"v1.0.0", "v2.0.0", checkWarn, "v2.0.0 is available"},
		{"v2.0.0", "v2.0.0", checkPass, "is the latest"},
		{"v3.0.0", "v2.0.0", checkPass, "is the latest"},
		{"dev", "v2.0.0", checkPass, "development build"},
	} {
		version.Version = c.current
		write(c.latest)
		d = &doctor{}
		d.checkUpdate()
		if d.checks[0].Status != c.status || !strings.Contains(d.checks[0].Message, c.want) {
			t.Errorf("%s vs %s: %+v", c.current, c.latest, d.checks[0])
		}
		if c.status == checkWarn && d.checks[0].Hint != "ffc update" {
			t.Errorf("hint %q", d.checks[0].Hint)
		}
	}

	t.Setenv("FFC_NO_UPDATE_CHECK", "1")
	d = &doctor{}
	d.checkUpdate()
	if !strings.Contains(d.checks[0].Message, "off") {
		t.Errorf("disabled: %+v", d.checks[0])
	}
}
