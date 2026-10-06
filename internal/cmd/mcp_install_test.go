package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
)

// installT points every client config at a temp home and the running binary
// at a fixed path. claude is "found" when claudePath is set; its calls are
// recorded and never run.
type installT struct {
	home, exe  string
	claudePath string
	calls      [][]string
}

func newInstallT(t *testing.T) *installT {
	t.Helper()
	it := &installT{home: filepath.Join(t.TempDir(), "home")}
	it.exe = filepath.Join(it.home, "bin", "ffc")
	t.Setenv("HOME", it.home)
	t.Setenv("USERPROFILE", it.home)
	t.Setenv("APPDATA", filepath.Join(it.home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(it.home, ".config"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	oldExe, oldEnv := mcpInstallExecutable, mcpInstallEnv
	t.Cleanup(func() { mcpInstallExecutable, mcpInstallEnv = oldExe, oldEnv })
	mcpInstallExecutable = func() (string, error) { return it.exe, nil }
	mcpInstallEnv = func() (mcpinstall.Env, error) {
		env, err := mcpinstall.DefaultEnv()
		env.LookPath = func(name string) (string, error) {
			if it.claudePath == "" || name != "claude" {
				return "", exec.ErrNotFound
			}
			return it.claudePath, nil
		}
		env.Run = func(name string, args ...string) ([]byte, error) {
			it.calls = append(it.calls, append([]string{name}, args...))
			return nil, nil
		}
		return env, err
	}
	return it
}

func (it *installT) cursorPath() string { return filepath.Join(it.home, ".cursor", "mcp.json") }

func installConfig(t *testing.T) string {
	return fakeConfig(t, &frappetest.Site{URL: "http://127.0.0.1:1"}, "apikey")
}

func TestMCPInstallPrintWritesNothing(t *testing.T) {
	it := newInstallT(t)
	r := runFFC(t, installConfig(t), "", "mcp", "install", "--client", "cursor", "--print")
	if r.Code != 0 {
		t.Fatalf("code %d: %v\n%s", r.Code, r.Err, r.Stderr)
	}
	exeJSON, _ := json.Marshal(it.exe)
	for _, s := range []string{"--- /dev/null", "+++ " + it.cursorPath(), `+      "command": ` + string(exeJSON) + ",", `"type": "stdio"`} {
		if !strings.Contains(r.Stdout, s) {
			t.Errorf("stdout misses %q:\n%s", s, r.Stdout)
		}
	}
	if !strings.Contains(r.Stderr, "will be created") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if _, err := os.Stat(it.cursorPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file written: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(it.cursorPath())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("directory created")
	}
}

func TestMCPInstallYesWritesWithBackup(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	old := "{\n  // mine\n  \"mcpServers\": {\n    \"frappe\": {\"command\": \"npx\", \"args\": [\"foxmayn-frappe-mcp\"]}\n  }\n}\n"
	if err := os.MkdirAll(filepath.Dir(it.cursorPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(it.cursorPath(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	// --site T matches the site "t" case-insensitively; the exact name is
	// pinned.
	r := runFFC(t, cfg, "", "mcp", "install", "--client", "cursor", "--site", "T", "--read-only", "--yes")
	if r.Code != 0 {
		t.Fatalf("code %d: %v\n%s", r.Code, r.Err, r.Stderr)
	}
	got, _ := os.ReadFile(it.cursorPath())
	var doc struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	src := strings.Replace(string(got), "  // mine\n", "", 1)
	if err := json.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	e := doc.MCPServers["frappe"]
	if e.Type != "stdio" || e.Command != it.exe || strings.Join(e.Args, " ") != "mcp --site t --read-only" {
		t.Fatalf("entry %+v", e)
	}
	if !strings.Contains(string(got), "// mine") {
		t.Error("comment lost")
	}
	matches, _ := filepath.Glob(it.cursorPath() + ".ffc-*.bak")
	if len(matches) != 1 {
		t.Fatalf("backups: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != old {
		t.Error("backup content")
	}
	for _, s := range []string{"Replacing the \"frappe\" entry", "-    \"frappe\": {\"command\": \"npx\"", "Backup: " + matches[0], "Restart Cursor"} {
		if !strings.Contains(r.Stderr, s) {
			t.Errorf("stderr misses %q:\n%s", s, r.Stderr)
		}
	}

	// Running it again changes nothing and makes no backup.
	r = runFFC(t, cfg, "", "mcp", "install", "--client", "cursor", "--site", "t", "--read-only", "--yes")
	if r.Code != 0 || !strings.Contains(r.Stderr, "already up to date") {
		t.Fatalf("code %d\n%s", r.Code, r.Stderr)
	}
	if m, _ := filepath.Glob(it.cursorPath() + ".ffc-*.bak"); len(m) != 1 {
		t.Fatalf("backups: %v", m)
	}
}

func TestMCPInstallUsageErrors(t *testing.T) {
	newInstallT(t)
	cfg := installConfig(t)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"missing client": {nil, "--client is required"},
		"bad client":     {[]string{"--client", "chatgpt"}, "want one of claude-code"},
		"bad name":       {[]string{"--client", "cursor", "--name", "a.b"}, "--name"},
		"unknown site":   {[]string{"--client", "cursor", "--site", "nope"}, `--site "nope"`},
		"print and yes":  {[]string{"--client", "cursor", "--print", "--yes"}, "print"},
		"no terminal":    {[]string{"--client", "cursor"}, "pass --yes"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runFFC(t, cfg, "", append([]string{"mcp", "install"}, tc.args...)...)
			if r.Code != 2 || !strings.Contains(r.Stderr, tc.want) {
				t.Fatalf("code %d, want 2 with %q:\n%s", r.Code, tc.want, r.Stderr)
			}
		})
	}
}

func TestMCPInstallJSON(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	t.Setenv("CODEX_HOME", filepath.Join(it.home, "codex"))
	path := filepath.Join(it.home, "codex", "config.toml")

	r := runFFC(t, cfg, "", "--json", "mcp", "install", "--client", "codex", "--print")
	if r.Code != 0 {
		t.Fatalf("code %d: %s", r.Code, r.Stderr)
	}
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &res); err != nil {
		t.Fatalf("%v: %s", err, r.Stdout)
	}
	if res["client"] != "codex" || res["path"] != path || res["changed"] != true || res["applied"] != false || res["backup"] != "" {
		t.Fatalf("print result %v", res)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("--print wrote the file")
	}

	r = runFFC(t, cfg, "", "--json", "mcp", "install", "--client", "codex", "--yes")
	if r.Code != 0 {
		t.Fatalf("code %d: %s", r.Code, r.Stderr)
	}
	res = nil
	if err := json.Unmarshal([]byte(r.Stdout), &res); err != nil {
		t.Fatalf("%v: %s", err, r.Stdout)
	}
	keys := []string{"applied", "backup", "changed", "client", "command", "name", "path", "server"}
	if len(res) != len(keys) {
		t.Errorf("keys %v", res)
	}
	server, _ := res["server"].([]interface{})
	if res["applied"] != true || res["name"] != "frappe" || len(server) != 2 || server[0] != it.exe || server[1] != "mcp" {
		t.Fatalf("result %v", res)
	}
	if strings.Contains(r.Stdout, "@@") || strings.Contains(r.Stderr, "@@") {
		t.Error("diff printed in JSON mode")
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "[mcp_servers.frappe]\ncommand = ") {
		t.Fatalf("toml:\n%s", b)
	}
}

func TestMCPInstallClaudeCode(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)

	// Not on PATH: --print still shows the command; a real run is a usage
	// error that prints it.
	r := runFFC(t, cfg, "", "mcp", "install", "--client", "claude-code", "--print")
	if r.Code != 0 || !strings.Contains(r.Stdout, "claude mcp add-json --scope user frappe ") {
		t.Fatalf("code %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	r = runFFC(t, cfg, "", "mcp", "install", "--client", "claude-code", "--yes")
	if r.Code != 2 || !strings.Contains(r.Stderr, "not on PATH") || !strings.Contains(r.Stderr, "claude mcp add-json") {
		t.Fatalf("code %d\n%s", r.Code, r.Stderr)
	}

	it.claudePath = filepath.Join(it.home, "bin", "claude")
	r = runFFC(t, cfg, "", "mcp", "install", "--client", "claude-code", "--yes")
	if r.Code != 0 {
		t.Fatalf("code %d: %v\n%s", r.Code, r.Err, r.Stderr)
	}
	if len(it.calls) != 1 {
		t.Fatalf("calls %q", it.calls)
	}
	call := it.calls[0]
	if call[0] != it.claudePath || strings.Join(call[1:6], " ") != "mcp add-json --scope user frappe" {
		t.Fatalf("call %q", call)
	}
	var entry struct {
		Type, Command string
		Args          []string
	}
	if err := json.Unmarshal([]byte(call[6]), &entry); err != nil || entry.Type != "stdio" || entry.Command != it.exe || strings.Join(entry.Args, " ") != "mcp" {
		t.Fatalf("json %s: %v", call[6], err)
	}
	if !strings.Contains(r.Stderr, "no diff, no backup") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(it.home, ".claude.json")); err == nil {
		t.Fatal("~/.claude.json written")
	}
}

func TestIsGoRunBuild(t *testing.T) {
	tmp := t.TempDir()
	for exe, want := range map[string]bool{
		filepath.Join(tmp, "go-build123", "b001", "exe", "ffc"): true,
		filepath.Join(tmp, "bin", "ffc"):                        false,
		filepath.Join(t.TempDir(), "go-build1", "ffc"):          false, // another temp dir
	} {
		if got := isGoRunBuild(exe, tmp); got != want {
			t.Errorf("%s: %v, want %v", exe, got, want)
		}
	}
	if isGoRunBuild(filepath.Join(tmp, "go-build1", "ffc"), "") {
		t.Error("empty temp dir matched")
	}
}
