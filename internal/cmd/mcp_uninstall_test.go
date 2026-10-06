package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const uninstallCursorOld = "{\n  // mine\n  \"mcpServers\": {\n    \"other\": {\"command\": \"x\"},\n    \"frappe\": {\"command\": \"ffc\", \"args\": [\"mcp\"]}\n  }\n}\n"

func (it *installT) writeCursor(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(it.cursorPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(it.cursorPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPUninstallPrintWritesNothing(t *testing.T) {
	it := newInstallT(t)
	it.writeCursor(t, uninstallCursorOld)
	r := runFFC(t, installConfig(t), "", "mcp", "uninstall", "--client", "cursor", "--print")
	if r.Code != 0 {
		t.Fatalf("code %d: %v\n%s", r.Code, r.Err, r.Stderr)
	}
	for _, s := range []string{"--- " + it.cursorPath(), `-    "frappe": {"command": "ffc", "args": ["mcp"]}`, `-    "other": {"command": "x"},`, `+    "other": {"command": "x"}`} {
		if !strings.Contains(r.Stdout, s) {
			t.Errorf("stdout misses %q:\n%s", s, r.Stdout)
		}
	}
	if !strings.Contains(r.Stderr, `Removing the "frappe" entry from `+it.cursorPath()) {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if b, _ := os.ReadFile(it.cursorPath()); string(b) != uninstallCursorOld {
		t.Fatal("file changed")
	}
	if m, _ := filepath.Glob(it.cursorPath() + ".ffc-*.bak"); len(m) != 0 {
		t.Fatalf("backups: %v", m)
	}
}

func TestMCPUninstallYesWritesWithBackup(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	it.writeCursor(t, uninstallCursorOld)
	r := runFFC(t, cfg, "", "mcp", "uninstall", "--client", "cursor", "--yes")
	if r.Code != 0 {
		t.Fatalf("code %d: %v\n%s", r.Code, r.Err, r.Stderr)
	}
	want := "{\n  // mine\n  \"mcpServers\": {\n    \"other\": {\"command\": \"x\"}\n  }\n}\n"
	if b, _ := os.ReadFile(it.cursorPath()); string(b) != want {
		t.Fatalf("content:\n%s", b)
	}
	matches, _ := filepath.Glob(it.cursorPath() + ".ffc-*.bak")
	if len(matches) != 1 {
		t.Fatalf("backups: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != uninstallCursorOld {
		t.Error("backup content")
	}
	for _, s := range []string{`Removed the "frappe" entry from ` + it.cursorPath(), "Backup: " + matches[0], "Restart Cursor"} {
		if !strings.Contains(r.Stderr, s) {
			t.Errorf("stderr misses %q:\n%s", s, r.Stderr)
		}
	}
	if r.Stdout != "" {
		t.Errorf("stdout: %q", r.Stdout)
	}

	// Again: nothing to remove, exit 0, no new backup.
	r = runFFC(t, cfg, "", "mcp", "uninstall", "--client", "cursor", "--yes")
	if r.Code != 0 || !strings.Contains(r.Stderr, `has no "frappe" entry; nothing to remove`) {
		t.Fatalf("code %d\n%s", r.Code, r.Stderr)
	}
	if m, _ := filepath.Glob(it.cursorPath() + ".ffc-*.bak"); len(m) != 1 {
		t.Fatalf("backups: %v", m)
	}
}

func TestMCPUninstallNothingToRemove(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	// No file: not created, even without --yes or a terminal.
	r := runFFC(t, cfg, "", "mcp", "uninstall", "--client", "cursor")
	if r.Code != 0 || !strings.Contains(r.Stderr, "does not exist; nothing to remove") {
		t.Fatalf("code %d\n%s", r.Code, r.Stderr)
	}
	if _, err := os.Stat(filepath.Dir(it.cursorPath())); err == nil {
		t.Fatal("directory created")
	}
	// Another name.
	it.writeCursor(t, uninstallCursorOld)
	r = runFFC(t, cfg, "", "--json", "mcp", "uninstall", "--client", "cursor", "--name", "nope")
	if r.Code != 0 {
		t.Fatalf("code %d\n%s", r.Code, r.Stderr)
	}
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &res); err != nil {
		t.Fatalf("%v: %s", err, r.Stdout)
	}
	if res["changed"] != false || res["applied"] != false || res["name"] != "nope" {
		t.Fatalf("result %v", res)
	}
}

func TestMCPUninstallUsageErrors(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	it.writeCursor(t, uninstallCursorOld)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"missing client": {nil, "--client is required"},
		"bad client":     {[]string{"--client", "chatgpt"}, "want one of claude-code"},
		"bad name":       {[]string{"--client", "cursor", "--name", "a.b"}, "--name"},
		"print and yes":  {[]string{"--client", "cursor", "--print", "--yes"}, "print"},
		"no read-only":   {[]string{"--client", "cursor", "--read-only"}, "unknown flag"},
		"no terminal":    {[]string{"--client", "cursor"}, "pass --yes"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runFFC(t, cfg, "", append([]string{"mcp", "uninstall"}, tc.args...)...)
			if r.Code != 2 || !strings.Contains(r.Stderr, tc.want) {
				t.Fatalf("code %d, want 2 with %q:\n%s", r.Code, tc.want, r.Stderr)
			}
		})
	}
	if b, _ := os.ReadFile(it.cursorPath()); string(b) != uninstallCursorOld {
		t.Fatal("file changed")
	}
}

func TestMCPUninstallJSON(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	t.Setenv("CODEX_HOME", filepath.Join(it.home, "codex"))
	path := filepath.Join(it.home, "codex", "config.toml")
	old := "model = \"o3\"\n\n[mcp_servers.frappe]\ncommand = \"ffc\"\n\n[mcp_servers.frappe.env]\nX = \"1\"\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	r := runFFC(t, cfg, "", "--json", "mcp", "uninstall", "--client", "codex", "--print")
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
	if b, _ := os.ReadFile(path); string(b) != old {
		t.Fatal("--print wrote the file")
	}

	r = runFFC(t, cfg, "", "--json", "mcp", "uninstall", "--client", "codex", "--yes")
	if r.Code != 0 {
		t.Fatalf("code %d: %s", r.Code, r.Stderr)
	}
	res = nil
	if err := json.Unmarshal([]byte(r.Stdout), &res); err != nil {
		t.Fatalf("%v: %s", err, r.Stdout)
	}
	var keys []string
	for k := range res {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "applied,backup,changed,client,command,name,path" {
		t.Errorf("keys %v", keys)
	}
	cmdList, ok := res["command"].([]interface{})
	if res["applied"] != true || res["changed"] != true || res["name"] != "frappe" || !ok || len(cmdList) != 0 {
		t.Fatalf("result %v", res)
	}
	backup, _ := res["backup"].(string)
	if b, err := os.ReadFile(backup); err != nil || string(b) != old {
		t.Fatalf("backup %q: %v", backup, err)
	}
	if strings.Contains(r.Stdout, "@@") || strings.Contains(r.Stderr, "@@") {
		t.Error("diff printed in JSON mode")
	}
	if b, _ := os.ReadFile(path); string(b) != "model = \"o3\"\n" {
		t.Fatalf("toml:\n%q", b)
	}
}

func TestMCPUninstallClaudeCode(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	state := filepath.Join(it.home, ".claude.json")
	if err := os.MkdirAll(it.home, 0o700); err != nil {
		t.Fatal(err)
	}

	// No state file: nothing to remove, claude never run.
	it.claudePath = filepath.Join(it.home, "bin", "claude")
	r := runFFC(t, cfg, "", "mcp", "uninstall", "--client", "claude-code", "--yes")
	if r.Code != 0 || !strings.Contains(r.Stderr, "nothing to remove") || len(it.calls) != 0 {
		t.Fatalf("code %d calls %q\n%s", r.Code, it.calls, r.Stderr)
	}

	stateJSON := `{"mcpServers": {"frappe": {"command": "npx", "args": []}}}`
	if err := os.WriteFile(state, []byte(stateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	r = runFFC(t, cfg, "", "mcp", "uninstall", "--client", "claude-code", "--print")
	if r.Code != 0 || strings.TrimSpace(r.Stdout) != "claude mcp remove --scope user frappe" || len(it.calls) != 0 {
		t.Fatalf("code %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}

	// Not on PATH: a usage error that prints the command.
	it.claudePath = ""
	r = runFFC(t, cfg, "", "mcp", "uninstall", "--client", "claude-code", "--yes")
	if r.Code != 2 || !strings.Contains(r.Stderr, "not on PATH") || !strings.Contains(r.Stderr, "claude mcp remove --scope user frappe") {
		t.Fatalf("code %d\n%s", r.Code, r.Stderr)
	}

	it.claudePath = filepath.Join(it.home, "bin", "claude")
	r = runFFC(t, cfg, "", "mcp", "uninstall", "--client", "claude-code", "--yes")
	if r.Code != 0 {
		t.Fatalf("code %d: %v\n%s", r.Code, r.Err, r.Stderr)
	}
	if len(it.calls) != 1 || it.calls[0][0] != it.claudePath || strings.Join(it.calls[0][1:], " ") != "mcp remove --scope user frappe" {
		t.Fatalf("calls %q", it.calls)
	}
	if !strings.Contains(r.Stderr, `Removed "frappe" from Claude Code's user config`) || !strings.Contains(r.Stderr, "no diff, no backup") {
		t.Errorf("stderr: %s", r.Stderr)
	}
	if b, _ := os.ReadFile(state); string(b) != stateJSON {
		t.Fatal("the state file was written")
	}
}

func TestMCPUninstallClaudeBatchRefusedBeforePrompt(t *testing.T) {
	it := newInstallT(t)
	cfg := installConfig(t)
	if err := os.MkdirAll(it.home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(it.home, ".claude.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	it.goos = "windows"
	it.claudePath = filepath.Join(it.home, "npm", "claude.cmd")
	r := runFFC(t, cfg, "", "--json", "mcp", "uninstall", "--client", "claude-code")
	if r.Code != 2 || !strings.Contains(r.Stderr, "batch file") || !strings.Contains(r.Stderr, "claude mcp remove --scope user frappe") ||
		strings.Contains(r.Stderr, "pass --yes") || len(it.calls) != 0 {
		t.Fatalf("code %d calls %q\n%s", r.Code, it.calls, r.Stderr)
	}
}
