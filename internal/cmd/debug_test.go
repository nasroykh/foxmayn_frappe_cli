package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// debugTRun runs ffc with the trace on and turns it off again afterwards,
// so clients built directly by later tests are not traced.
func debugTRun(t *testing.T, cfg, stdin string, args ...string) cliResult {
	t.Helper()
	t.Cleanup(func() { client.Debug = client.DebugOff })
	return runFFC(t, cfg, stdin, args...)
}

// TestDebugNeverPrintsSecrets is the golden rule of --debug: whatever the
// auth method, credentials never reach the trace, bodies included.
func TestDebugNeverPrintsSecrets(t *testing.T) {
	for _, auth := range []string{"apikey", "password", "oauth"} {
		t.Run(auth, func(t *testing.T) {
			s := cmdTSite(t)
			cfg := fakeConfig(t, s, auth)
			r := debugTRun(t, cfg, "", "create-doc", "-d", "ToDo", "--debug=body", "--json",
				"--data", `{"description":"d","new_password":"pw-in-body","api_secret":"secret-in-body"}`)
			cmdTOK(t, r)
			r2 := debugTRun(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "TD-1", "--debug=body")
			cmdTOK(t, r2)
			trace := r.Stderr + r2.Stderr

			secrets := []string{frappetest.APISecret, frappetest.Token, `"` + frappetest.Password + `"`, "pw-in-body", "secret-in-body"}
			for _, req := range s.Requests() {
				if c, err := (&http.Request{Header: req.Header}).Cookie("sid"); err == nil {
					secrets = append(secrets, "sid="+c.Value)
				}
			}
			for _, secret := range secrets {
				if strings.Contains(trace, secret) {
					t.Errorf("trace contains %q:\n%s", secret, trace)
				}
			}
			for _, want := range []string{"debug #", "> POST ", "/api/resource/ToDo", "< 200", `"new_password":"***"`} {
				if !strings.Contains(trace, want) {
					t.Errorf("trace lacks %q:\n%s", want, trace)
				}
			}
			// Data output is untouched: the trace is stderr only.
			var doc map[string]interface{}
			if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil || doc["description"] != "d" {
				t.Errorf("stdout = %q (%v)", r.Stdout, err)
			}
		})
	}
}

func TestDebugBasic(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "apikey")
	r := debugTRun(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "nope", "--debug")
	if r.Code != exitNotFound {
		t.Fatalf("exit %d: %v", r.Code, r.Err)
	}
	line := strings.SplitN(r.Stderr, "\n", 2)[0]
	for _, want := range []string{"debug #", "GET ", "/api/resource/ToDo/nope", "→ 404 DoesNotExistError", "received"} {
		if !strings.Contains(line, want) {
			t.Errorf("trace line %q lacks %q", line, want)
		}
	}
	if strings.Contains(r.Stderr, "Authorization") {
		t.Errorf("basic trace shows headers:\n%s", r.Stderr)
	}

	// FFC_DEBUG turns it on; the flag wins.
	cliEnv = map[string]string{"FFC_DEBUG": "body"}
	t.Cleanup(func() { cliEnv = nil })
	if r := debugTRun(t, cfg, "", "ping"); !strings.Contains(r.Stderr, "> GET ") {
		t.Errorf("FFC_DEBUG=body: %s", r.Stderr)
	}
	if r := debugTRun(t, cfg, "", "ping", "--debug=off"); strings.Contains(r.Stderr, "debug #") {
		t.Errorf("--debug=off traced: %s", r.Stderr)
	}
	// An invalid value fails before any request.
	cliEnv = map[string]string{"FFC_DEBUG": "loud"}
	before := len(s.Requests())
	if r := debugTRun(t, cfg, "", "ping"); r.Code != exitUsage || len(s.Requests()) != before {
		t.Errorf("FFC_DEBUG=loud: exit %d, err %v, %d requests", r.Code, r.Err, len(s.Requests())-before)
	}
	cliEnv = nil
	if r := debugTRun(t, cfg, "", "ping", "--debug=loud"); r.Code != exitUsage {
		t.Errorf("--debug=loud: exit %d", r.Code)
	}
}

// TestDebugOAuthRefreshRedacted traces a token refresh: the client secret,
// both refresh tokens and the new access token stay out of the trace.
func TestDebugOAuthRefreshRedacted(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "a"})
	s.Handle("POST "+tokenPath, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + frappetest.Token + `","refresh_token":"rt-new-value","expires_in":3600,"token_type":"Bearer"}`))
	}))
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	body := "default_site: t\nsites:\n  t:\n    url: " + s.URL + "\n    oauth_client_id: cid\n    oauth_client_secret: cs-value\n" +
		"    access_token: at-old-value\n    refresh_token: rt-old-value\n    token_expiry: 1\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := debugTRun(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "a", "--debug=body")
	cmdTOK(t, r)
	if len(s.RequestsTo(http.MethodPost, tokenPath)) != 1 || !strings.Contains(r.Stderr, tokenPath) {
		t.Fatalf("no traced refresh:\n%s", r.Stderr)
	}
	for _, secret := range []string{"cs-value", "rt-old-value", "rt-new-value", "at-old-value", frappetest.Token} {
		if strings.Contains(r.Stderr, secret) {
			t.Errorf("trace contains %q:\n%s", secret, r.Stderr)
		}
	}
}
