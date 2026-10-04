package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// TestExitCodes runs one command per exit code against the fake site.
func TestExitCodes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *frappetest.Site)
		cfg   func(t *testing.T, s *frappetest.Site) string // default: API key
		args  []string
		stdin string
		code  int
	}{
		{name: "ok", args: []string{"get-doc", "-d", "ToDo", "-n", "a"}, code: exitOK},
		{name: "unknown flag", args: []string{"get-doc", "--nope"}, code: exitUsage},
		{name: "unknown command", args: []string{"no-such-command"}, code: exitUsage},
		{name: "unexpected argument", args: []string{"ping", "extra"}, code: exitUsage},
		{name: "missing required flag", args: []string{"delete-doc", "-d", "ToDo", "--yes"}, code: exitUsage},
		{name: "bad limit", args: []string{"list-docs", "-d", "ToDo", "--limit", "-1"}, code: exitUsage},
		{name: "bad filters", args: []string{"list-docs", "-d", "ToDo", "--filters", "nope"}, code: exitUsage},
		{name: "bad bulk input", args: []string{"bulk-delete", "-d", "ToDo", "--file", "-", "--yes"}, stdin: "[{}]", code: exitUsage},
		{name: "no bulk input", args: []string{"bulk-delete", "-d", "ToDo", "--yes"}, code: exitUsage},
		{
			name: "wrong password",
			cfg: func(t *testing.T, s *frappetest.Site) string {
				return writeTestConfig(t, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    username: Administrator\n    password: wrong\n", s.URL))
			},
			args: []string{"get-doc", "-d", "ToDo", "-n", "a"}, code: exitAuth,
		},
		{
			name: "401",
			setup: func(s *frappetest.Site) {
				s.Handle("GET /api/resource/ToDo/a", frappetest.ErrorHandler(&frappetest.Error{Status: 401, ExcType: "AuthenticationError", Message: "Invalid API key"}))
			},
			args: []string{"get-doc", "-d", "ToDo", "-n", "a"}, code: exitAuth,
		},
		{name: "not found", args: []string{"get-doc", "-d", "ToDo", "-n", "missing"}, code: exitNotFound},
		{
			name: "permission",
			setup: func(s *frappetest.Site) {
				s.Handle("GET /api/resource/ToDo/a", frappetest.ErrorHandler(frappetest.Permission("No permission for ToDo")))
			},
			args: []string{"get-doc", "-d", "ToDo", "-n", "a"}, code: exitPermission,
		},
		{
			name: "validation",
			setup: func(s *frappetest.Site) {
				s.Handle("POST /api/resource/ToDo", frappetest.ErrorHandler(frappetest.Validation("Description is mandatory")))
			},
			args: []string{"create-doc", "-d", "ToDo", "--data", `{"x":1}`}, code: exitValidation,
		},
		{name: "duplicate", args: []string{"create-doc", "-d", "ToDo", "--data", `{"name":"a"}`}, code: exitValidation},
		{
			name: "server error",
			setup: func(s *frappetest.Site) {
				s.Handle("POST /api/resource/ToDo", frappetest.ErrorHandler(&frappetest.Error{Status: 500, ExcType: "Exception", Message: "boom"}))
			},
			args: []string{"create-doc", "-d", "ToDo", "--data", `{"x":1}`}, code: exitNetwork,
		},
		{
			name: "connection refused",
			cfg: func(t *testing.T, _ *frappetest.Site) string {
				return writeTestConfig(t, "default_site: t\nsites:\n  t:\n    url: \"http://127.0.0.1:1\"\n    api_key: k\n    api_secret: s\n")
			},
			args: []string{"get-doc", "-d", "ToDo", "-n", "a"}, code: exitNetwork,
		},
		{name: "partial bulk", args: []string{"bulk-delete", "-d", "ToDo", "--names", "a,missing", "--yes"}, code: exitPartial},
		{
			name: "missing config",
			cfg: func(t *testing.T, _ *frappetest.Site) string {
				return filepath.Join(t.TempDir(), "none.yaml")
			},
			args: []string{"get-doc", "-d", "ToDo", "-n", "a"}, code: exitGeneric,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := frappetest.New(t)
			s.Add("ToDo", map[string]interface{}{"name": "a", "description": "x"})
			if tc.setup != nil {
				tc.setup(s)
			}
			cfg := fakeConfig(t, s, "apikey")
			if tc.cfg != nil {
				cfg = tc.cfg(t, s)
			}
			r := runFFC(t, cfg, tc.stdin, tc.args...)
			if r.Code != tc.code {
				t.Errorf("exit code = %d, want %d (err: %v)", r.Code, tc.code, r.Err)
			}
		})
	}
}

func TestExitCodeInterrupted(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "a"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := runFFCCtx(t, ctx, fakeConfig(t, s, "apikey"), "", "get-doc", "-d", "ToDo", "-n", "a")
	if r.Code != exitInterrupted {
		t.Errorf("exit code = %d, want %d (err: %v)", r.Code, exitInterrupted, r.Err)
	}
}

func TestJSONErrorOnStderr(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo")
	r := runFFC(t, fakeConfig(t, s, "apikey"), "", "--json", "get-doc", "-d", "ToDo", "-n", "missing")
	if r.Code != exitNotFound {
		t.Fatalf("exit code = %d", r.Code)
	}
	if strings.TrimSpace(r.Stdout) != "" {
		t.Errorf("stdout = %q, want no data on failure", r.Stdout)
	}
	var e errorJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stderr)), &e); err != nil {
		t.Fatalf("stderr is not one JSON error: %v\n%s", err, r.Stderr)
	}
	got := e.Error
	if got.Code != "not_found" || got.ExitCode != exitNotFound || got.Status != http.StatusNotFound ||
		got.ExcType != "DoesNotExistError" || !strings.Contains(got.Message, "not found") {
		t.Errorf("error JSON = %+v", got)
	}

	// Usage errors are JSON too once --json has been parsed.
	r = runFFC(t, fakeConfig(t, s, "apikey"), "", "--json", "list-docs", "-d", "ToDo", "--limit", "-1")
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stderr)), &e); err != nil || e.Error.Code != "usage" || e.Error.ExitCode != exitUsage {
		t.Errorf("usage error JSON = %+v, %v (%s)", e.Error, err, r.Stderr)
	}
}

func TestTextErrorOnStderr(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo")
	r := runFFC(t, fakeConfig(t, s, "apikey"), "", "get-doc", "-d", "ToDo", "-n", "missing")
	if !strings.Contains(r.Stderr, "not found") || strings.HasPrefix(strings.TrimSpace(r.Stderr), "{") {
		t.Errorf("stderr = %q, want the plain message", r.Stderr)
	}
}

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvSelection(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "a"})
	cfg := fakeConfig(t, s, "password") // t: password, other: API key

	t.Run("FFC_SITE selects the site", func(t *testing.T) {
		t.Setenv("FFC_SITE", "other")
		before := s.Logins()
		if r := runFFC(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "a"); r.Err != nil {
			t.Fatal(r.Err)
		}
		if s.Logins() != before {
			t.Error("FFC_SITE=other still logged in to the password site")
		}
	})
	t.Run("--site wins over FFC_SITE", func(t *testing.T) {
		t.Setenv("FFC_SITE", "nope")
		if r := runFFC(t, cfg, "", "--site", "other", "get-doc", "-d", "ToDo", "-n", "a"); r.Err != nil {
			t.Fatal(r.Err)
		}
	})
	t.Run("FFC_CONFIG names the config", func(t *testing.T) {
		t.Setenv("FFC_CONFIG", cfg)
		t.Setenv("FFC_SITE", "other")
		if r := runFFC(t, "", "", "get-doc", "-d", "ToDo", "-n", "a"); r.Err != nil {
			t.Fatal(r.Err)
		}
	})
	t.Run("invalid FFC_TIMEOUT is a usage error", func(t *testing.T) {
		t.Setenv("FFC_TIMEOUT", "soon")
		if r := runFFC(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "a"); r.Code != exitUsage {
			t.Errorf("exit code = %d, want %d (%v)", r.Code, exitUsage, r.Err)
		}
	})
	t.Run("FFC_TIMEOUT applies", func(t *testing.T) {
		t.Setenv("FFC_TIMEOUT", "1ns")
		t.Setenv("FFC_SITE", "other")
		if r := runFFC(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "a"); r.Code != exitNetwork {
			t.Errorf("exit code = %d, want %d (%v)", r.Code, exitNetwork, r.Err)
		}
	})
}

// Without a terminal (tests have none) prompts fail fast as usage errors.
func TestNoInputPrompts(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "a"})
	cfg := fakeConfig(t, s, "apikey")

	r := runFFC(t, cfg, "", "delete-doc", "-d", "ToDo", "-n", "a")
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "--yes") {
		t.Errorf("delete-doc without --yes: code %d, stderr %q", r.Code, r.Stderr)
	}
	if n := len(s.RequestsTo(http.MethodDelete, "/api/resource/ToDo/a")); n != 0 {
		t.Errorf("%d DELETE requests sent without confirmation", n)
	}

	r = runFFC(t, filepath.Join(t.TempDir(), "new.yaml"), "", "--no-input", "init")
	if r.Code != exitUsage || strings.Contains(r.Stderr, "TTY") {
		t.Errorf("init --no-input: code %d, stderr %q", r.Code, r.Stderr)
	}
}
