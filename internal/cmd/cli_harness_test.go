package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestMain clears the selection variables a developer may have exported, so
// tests start from the config they write; tests set them with t.Setenv.
func TestMain(m *testing.M) {
	// runJQ re-runs the current executable, which is this test binary.
	if os.Getenv(jqChildEnv) == jqChildOn {
		os.Exit(jqChild(os.Stdin, os.Stdout))
	}
	for _, k := range []string{"FFC_SITE", "FFC_CONFIG", "FFC_TIMEOUT"} {
		_ = os.Unsetenv(k)
	}
	// The site version cache lives in the user cache directory: keep the
	// tests out of the developer's.
	cache, err := os.MkdirTemp("", "ffc-test-cache")
	if err != nil {
		panic(err)
	}
	sitecache.UserCacheDir = func() (string, error) { return cache, nil }
	code := m.Run()
	_ = os.RemoveAll(cache)
	os.Exit(code)
}

// cliResult is what one ffc invocation wrote and returned.
type cliResult struct {
	Stdout, Stderr string
	Err            error
	Code           int // process exit code
}

// fakeConfig writes a config whose default site "t" points at site with the
// given auth ("apikey", "password" or "oauth"). A second site "other"
// (API key, same URL) exists for site-selection tests. It returns the path.
func fakeConfig(t *testing.T, site *frappetest.Site, auth string) string {
	t.Helper()
	var creds string
	switch auth {
	case "apikey":
		creds = fmt.Sprintf("    api_key: %q\n    api_secret: %q\n", frappetest.APIKey, frappetest.APISecret)
	case "password":
		creds = fmt.Sprintf("    username: %q\n    password: %q\n", frappetest.Username, frappetest.Password)
	case "oauth":
		creds = fmt.Sprintf("    oauth_client_id: %q\n    access_token: %q\n    refresh_token: %q\n",
			frappetest.OAuthClientID, frappetest.Token, frappetest.RefreshToken)
	default:
		t.Fatalf("fakeConfig: unknown auth %q", auth)
	}
	body := fmt.Sprintf("default_site: t\nnumber_format: us\nsites:\n  t:\n    url: %q\n%s  other:\n    url: %q\n    api_key: %q\n    api_secret: %q\n",
		site.URL, creds, site.URL, frappetest.APIKey, frappetest.APISecret)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cliEnv holds environment variables runFFC sets after clearing the FFC_*
// ones; a test sets it and resets it to nil when done.
var cliEnv map[string]string

// runFFC runs ffc with args against the config at cfgPath ("" passes no
// --config), with stdin as
// standard input, and captures stdout and stderr. Every flag is reset
// afterwards, so runs do not leak into each other.
func runFFC(t *testing.T, cfgPath, stdin string, args ...string) cliResult {
	t.Helper()
	return runFFCCtx(t, context.Background(), cfgPath, stdin, args...)
}

// runFFCCtx is runFFC with the context the command runs under.
func runFFCCtx(t *testing.T, ctx context.Context, cfgPath, stdin string, args ...string) cliResult {
	t.Helper()
	t.Setenv("FFC_NO_UPDATE_CHECK", "1")
	t.Setenv("CI", "1") // no spinner
	for _, k := range []string{"FFC_API_KEY", "FFC_API_SECRET", "FFC_URL"} {
		t.Setenv(k, "")
	}
	for k, v := range cliEnv {
		t.Setenv(k, v)
	}

	dir := t.TempDir()
	files := map[string]*os.File{}
	for _, name := range []string{"stdin", "stdout", "stderr"} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}
	if _, err := files["stdin"].WriteString(stdin); err != nil {
		t.Fatal(err)
	}
	if _, err := files["stdin"].Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	// lipgloss prints tables through its own writer, bound to the real
	// stdout at init; point it at the capture file too.
	oldIn, oldOut, oldErr, oldTable := os.Stdin, os.Stdout, os.Stderr, lipgloss.Writer.Forward
	os.Stdin, os.Stdout, os.Stderr = files["stdin"], files["stdout"], files["stderr"]
	lipgloss.Writer.Forward = files["stdout"]
	defer func() {
		os.Stdin, os.Stdout, os.Stderr, lipgloss.Writer.Forward = oldIn, oldOut, oldErr, oldTable
		for _, f := range files {
			_ = f.Close()
		}
		resetFlags(rootCmd)
	}()

	// Cobra keeps the first context a subcommand ran with; give every
	// command this run's context.
	setContextAll(rootCmd, ctx)
	if cfgPath != "" {
		args = append([]string{"--config", cfgPath}, args...)
	}
	code, err := execute(ctx, args, files["stderr"])
	out, _ := os.ReadFile(files["stdout"].Name())
	errOut, _ := os.ReadFile(files["stderr"].Name())
	return cliResult{string(out), string(errOut), err, code}
}

// runCLI runs ffc with --json against a password-auth config pointing at url.
func runCLI(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	site := &frappetest.Site{URL: url}
	r := runFFC(t, fakeConfig(t, site, "password"), "", append([]string{"--json"}, args...)...)
	return r.Stdout, r.Err
}

// resetFlags restores every flag of c and its subcommands to its default.
func resetFlags(c *cobra.Command) {

	reset := func(f *pflag.Flag) {
		if s, ok := f.Value.(pflag.SliceValue); ok {
			// Set appends to a slice flag, and its DefValue is "[]".
			_ = s.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

func setContextAll(c *cobra.Command, ctx context.Context) {
	c.SetContext(ctx)
	for _, sub := range c.Commands() {
		setContextAll(sub, ctx)
	}
}
