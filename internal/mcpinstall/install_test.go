package mcpinstall

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/hujson"
)

// testEnv lays out a home directory for goos under a temp dir.
func testEnv(t *testing.T, goos string) Env {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	var cfg string
	switch goos {
	case "windows":
		cfg = filepath.Join(home, "AppData", "Roaming")
	case "darwin":
		cfg = filepath.Join(home, "Library", "Application Support")
	default:
		cfg = filepath.Join(home, ".config")
	}
	return Env{
		Home:      home,
		ConfigDir: cfg,
		GOOS:      goos,
		Getenv:    func(string) string { return "" },
		LookPath:  func(string) (string, error) { return "", exec.ErrNotFound },
	}
}

var testSrv = Server{Name: "frappe", Command: `C:\Program Files\ffc\ffc.exe`, Args: []string{"mcp", "--site", "prod"}}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustPlan(t *testing.T, client string, srv Server, env Env) *Change {
	t.Helper()
	c, err := Plan(client, srv, env)
	if err != nil {
		t.Fatalf("Plan(%s): %v", client, err)
	}
	return c
}

func TestConfigPaths(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		env := testEnv(t, goos)
		want := map[string]string{
			ClaudeCode:    filepath.Join(env.Home, ".claude.json"),
			ClaudeDesktop: filepath.Join(env.ConfigDir, "Claude", "claude_desktop_config.json"),
			Cursor:        filepath.Join(env.Home, ".cursor", "mcp.json"),
			VSCode:        filepath.Join(env.ConfigDir, "Code", "User", "mcp.json"),
			Codex:         filepath.Join(env.Home, ".codex", "config.toml"),
		}
		for _, client := range Clients {
			got, err := ConfigPath(client, env)
			if err != nil || got != want[client] {
				t.Errorf("%s/%s: got %q, %v; want %q", goos, client, got, err, want[client])
			}
			c := mustPlan(t, client, testSrv, env)
			if c.Path != want[client] {
				t.Errorf("%s/%s: Plan path %q, want %q", goos, client, c.Path, want[client])
			}
		}
	}

	env := testEnv(t, "linux")
	vars := map[string]string{"CODEX_HOME": filepath.Join(env.Home, "cx"), "CLAUDE_CONFIG_DIR": filepath.Join(env.Home, "cc")}
	env.Getenv = func(k string) string { return vars[k] }
	if got, _ := ConfigPath(Codex, env); got != filepath.Join(vars["CODEX_HOME"], "config.toml") {
		t.Errorf("CODEX_HOME: %q", got)
	}
	if got, _ := ConfigPath(ClaudeCode, env); got != filepath.Join(vars["CLAUDE_CONFIG_DIR"], ".claude.json") {
		t.Errorf("CLAUDE_CONFIG_DIR: %q", got)
	}
	if _, err := ConfigPath("chatgpt", env); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown client: %v", err)
	}
	if _, err := Plan("chatgpt", testSrv, env); !errors.Is(err, ErrInvalid) {
		t.Errorf("Plan unknown client: %v", err)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"frappe", "Frappe_Prod-2", "_x", "x-", strings.Repeat("a", 64)} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "--help", "a b", "a.b", "a/b", `a"b`, "é", strings.Repeat("a", 65), "a\n"} {
		if err := ValidName(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", bad, err)
		}
	}
	srv := testSrv
	srv.Name = "a.b"
	if _, err := Plan(Cursor, srv, testEnv(t, "linux")); !errors.Is(err, ErrInvalid) {
		t.Errorf("Plan bad name: %v", err)
	}
	srv = testSrv
	srv.Args = []string{"mcp", "\xff"}
	if _, err := Plan(Cursor, srv, testEnv(t, "linux")); !errors.Is(err, ErrInvalid) {
		t.Errorf("Plan invalid UTF-8: %v", err)
	}
}

func TestJSONNewFile(t *testing.T) {
	env := testEnv(t, "linux")
	c := mustPlan(t, Cursor, testSrv, env)
	want := `{
  "mcpServers": {
    "frappe": {
      "type": "stdio",
      "command": "C:\\Program Files\\ffc\\ffc.exe",
      "args": [
        "mcp",
        "--site",
        "prod"
      ]
    }
  }
}
`
	if c.Old != nil || string(c.New) != want || !c.Changed() || c.Replaces {
		t.Fatalf("old=%q replaces=%v new=\n%s", c.Old, c.Replaces, c.New)
	}
	if d := c.Diff(); !strings.HasPrefix(d, "--- /dev/null\n+++ "+c.Path+"\n@@ -0,0 +1,13 @@\n+{\n") {
		t.Errorf("diff:\n%s", d)
	}

	// An empty or blank file counts as {}.
	for _, blank := range []string{"", " \n\t\n"} {
		writeFile(t, c.Path, blank)
		c2 := mustPlan(t, Cursor, testSrv, env)
		if c2.Old == nil || string(c2.Old) != blank || string(c2.New) != want {
			t.Errorf("blank %q: old=%q new=\n%s", blank, c2.Old, c2.New)
		}
	}
}

func TestJSONKeepsCommentsAndOtherServers(t *testing.T) {
	env := testEnv(t, "windows")
	path, _ := ConfigPath(VSCode, env)
	old := "// VS Code MCP servers\n{\n\t\"inputs\": [\n\t\t{\"id\": \"token\", \"type\": \"promptString\"}, // keep\n\t],\n\t\"servers\": {\n\t\t/* a block comment */\n\t\t\"github\": {\n\t\t\t\"type\": \"http\",\n\t\t\t\"url\": \"https://api.githubcopilot.com/mcp/\",\n\t\t}, // trailing comment\n\t},\n}\n"
	writeFile(t, path, old)
	c := mustPlan(t, VSCode, testSrv, env)
	want := "// VS Code MCP servers\n{\n\t\"inputs\": [\n\t\t{\"id\": \"token\", \"type\": \"promptString\"}, // keep\n\t],\n\t\"servers\": {\n\t\t/* a block comment */\n\t\t\"github\": {\n\t\t\t\"type\": \"http\",\n\t\t\t\"url\": \"https://api.githubcopilot.com/mcp/\",\n\t\t}, // trailing comment\n" +
		"\t\t\"frappe\": {\n\t\t\t\"type\": \"stdio\",\n\t\t\t\"command\": \"C:\\\\Program Files\\\\ffc\\\\ffc.exe\",\n\t\t\t\"args\": [\n\t\t\t\t\"mcp\",\n\t\t\t\t\"--site\",\n\t\t\t\t\"prod\"\n\t\t\t]\n\t\t},\n\t},\n}\n"
	if string(c.New) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", c.New, want)
	}
	if c.Replaces {
		t.Error("Replaces on a new entry")
	}
}

func TestJSONTopKeyMissing(t *testing.T) {
	env := testEnv(t, "darwin")
	path, _ := ConfigPath(ClaudeDesktop, env)
	old := "{\n    \"globalShortcut\": \"Ctrl+Space\"\n}\n"
	writeFile(t, path, old)
	c := mustPlan(t, ClaudeDesktop, Server{Name: "frappe", Command: "/usr/local/bin/ffc", Args: []string{"mcp"}}, env)
	want := "{\n    \"globalShortcut\": \"Ctrl+Space\",\n    \"mcpServers\": {\n        \"frappe\": {\n            \"command\": \"/usr/local/bin/ffc\",\n            \"args\": [\n                \"mcp\"\n            ]\n        }\n    }\n}\n"
	if string(c.New) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", c.New, want)
	}
	// Claude Desktop gets no "type"; the result is strict JSON.
	var v map[string]interface{}
	if err := json.Unmarshal(c.New, &v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(c.New), `"type"`) {
		t.Error("claude-desktop entry has a type")
	}
}

func TestJSONReplace(t *testing.T) {
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Cursor, env)
	old := `{
  "mcpServers": {
    "frappe": {
      "command": "npx",
      "args": ["-y", "foxmayn-frappe-mcp"],
      "env": {"FRAPPE_SITE": "x"}
    },
    "other": {"command": "other-server"}
  }
}
`
	writeFile(t, path, old)
	c := mustPlan(t, Cursor, Server{Name: "frappe", Command: "/opt/ffc", Args: []string{"mcp", "--read-only"}}, env)
	want := `{
  "mcpServers": {
    "frappe": {
      "type": "stdio",
      "command": "/opt/ffc",
      "args": [
        "mcp",
        "--read-only"
      ]
    },
    "other": {"command": "other-server"}
  }
}
`
	if string(c.New) != want || !c.Replaces {
		t.Fatalf("replaces=%v got:\n%s", c.Replaces, c.New)
	}
	d := c.Diff()
	for _, s := range []string{"-      \"command\": \"npx\",", "+      \"command\": \"/opt/ffc\",", "     \"other\": {\"command\": \"other-server\"}"} {
		if !strings.Contains(d, s) {
			t.Errorf("diff misses %q:\n%s", s, d)
		}
	}
}

func TestJSONIdempotent(t *testing.T) {
	env := testEnv(t, "linux")
	c := mustPlan(t, VSCode, testSrv, env)
	if _, err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	c2 := mustPlan(t, VSCode, testSrv, env)
	if c2.Changed() || c2.Diff() != "" || !c2.Replaces {
		t.Fatalf("second plan changes:\n%s", c2.Diff())
	}
	if b, err := c2.Apply(); err != nil || b != "" {
		t.Fatalf("apply unchanged: %q %v", b, err)
	}
	// The same entry formatted another way is up to date too.
	writeFile(t, c.Path, `{"servers": {"frappe": {"args": ["mcp", "--site", "prod"], "command": "C:\\Program Files\\ffc\\ffc.exe", "type": "stdio"}}}`)
	if c3 := mustPlan(t, VSCode, testSrv, env); c3.Changed() {
		t.Fatalf("reformatted entry changes:\n%s", c3.Diff())
	}
}

func TestJSONRefused(t *testing.T) {
	cases := map[string]string{
		"unparsable":       `{"mcpServers": {`,
		"array":            `[]`,
		"servers not obj":  `{"mcpServers": []}`,
		"duplicate top":    `{"mcpServers": {}, "mcpServers": {}}`,
		"duplicate entry":  `{"mcpServers": {"frappe": {}, "frappe": {}}}`,
		"garbage after":    `{} {}`,
		"comment unclosed": "{ /* x }",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t, "linux")
			path, _ := ConfigPath(Cursor, env)
			writeFile(t, path, content)
			_, err := Plan(Cursor, testSrv, env)
			if err == nil || !strings.Contains(err.Error(), "left untouched") {
				t.Fatalf("want a refusal, got %v", err)
			}
			if b, _ := os.ReadFile(path); string(b) != content {
				t.Fatal("file changed")
			}
		})
	}
}

func TestJSONTrailingCommaAndBOM(t *testing.T) {
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Cursor, env)
	old := "\xEF\xBB\xBF{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"}\n  }\n}"
	writeFile(t, path, old)
	c := mustPlan(t, Cursor, Server{Name: "frappe", Command: "/x", Args: []string{"mcp"}}, env)
	if !strings.HasPrefix(string(c.New), "\xEF\xBB\xBF{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"},\n    \"frappe\": {") {
		t.Fatalf("got:\n%q", c.New)
	}
	if strings.Contains(string(c.New), "},\n  }") {
		t.Fatalf("a trailing comma was added:\n%s", c.New)
	}
	var v interface{}
	if err := json.Unmarshal(c.New[3:], &v); err != nil {
		t.Fatalf("not strict JSON: %v", err)
	}

	// An empty servers object.
	writeFile(t, path, "{\n  \"mcpServers\": {}\n}\n")
	c = mustPlan(t, Cursor, Server{Name: "frappe", Command: "/x", Args: []string{"mcp"}}, env)
	want := "{\n  \"mcpServers\": {\n    \"frappe\": {\n      \"type\": \"stdio\",\n      \"command\": \"/x\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n"
	if string(c.New) != want {
		t.Fatalf("got:\n%s", c.New)
	}
}

func TestTOMLAppend(t *testing.T) {
	env := testEnv(t, "windows")
	path, _ := ConfigPath(Codex, env)
	old := `# Codex config
model = "gpt-5"
notes = """
[mcp_servers.frappe]
not a header
"""
matrix = [
  [1, 2],
  [3, 4],
]

[mcp_servers.other]
command = "other"
args = []
`
	writeFile(t, path, old)
	c := mustPlan(t, Codex, testSrv, env)
	want := old + `
[mcp_servers.frappe]
command = "C:\\Program Files\\ffc\\ffc.exe"
args = ["mcp", "--site", "prod"]
`
	if string(c.New) != want || c.Replaces {
		t.Fatalf("got:\n%s", c.New)
	}
	if !strings.Contains(c.Diff(), "+command = \"C:\\\\Program Files\\\\ffc\\\\ffc.exe\"\n") {
		t.Errorf("diff:\n%s", c.Diff())
	}

	// A missing file gets just the table.
	env2 := testEnv(t, "linux")
	c2 := mustPlan(t, Codex, Server{Name: "frappe", Command: `/a "b"/ffc`, Args: []string{"mcp"}}, env2)
	if string(c2.New) != "[mcp_servers.frappe]\ncommand = \"/a \\\"b\\\"/ffc\"\nargs = [\"mcp\"]\n" {
		t.Fatalf("new file:\n%s", c2.New)
	}
}

func TestTOMLReplace(t *testing.T) {
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Codex, env)
	old := "model = \"o3\"\r\n\r\n[mcp_servers.\"frappe\"] # old wrapper\r\ncommand = \"npx\"\r\nargs = [\r\n  \"-y\",\r\n  \"foxmayn-frappe-mcp\",\r\n]\r\n\r\n[mcp_servers.frappe.env]\r\nFRAPPE_URL = \"https://x\"\r\n\r\n# about the next table\r\n[profiles.fast]\r\nmodel = \"o4-mini\"\r\n"
	writeFile(t, path, old)
	srv := Server{Name: "frappe", Command: "/usr/bin/ffc", Args: []string{"mcp"}}
	c := mustPlan(t, Codex, srv, env)
	want := "model = \"o3\"\r\n\r\n[mcp_servers.frappe]\r\ncommand = \"/usr/bin/ffc\"\r\nargs = [\"mcp\"]\r\n\r\n# about the next table\r\n[profiles.fast]\r\nmodel = \"o4-mini\"\r\n"
	if string(c.New) != want || !c.Replaces {
		t.Fatalf("replaces=%v got:\n%q\nwant:\n%q", c.Replaces, c.New, want)
	}
	if _, err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	if c2 := mustPlan(t, Codex, srv, env); c2.Changed() {
		t.Fatalf("not idempotent:\n%s", c2.Diff())
	}
	// A file without a final newline whose entry is current is unchanged.
	writeFile(t, path, "[mcp_servers.frappe]\ncommand = \"/usr/bin/ffc\"\nargs = [\"mcp\"]")
	if c3 := mustPlan(t, Codex, srv, env); c3.Changed() {
		t.Fatalf("missing final newline counts as a change:\n%q", c3.New)
	}
}

func TestTOMLRefused(t *testing.T) {
	cases := map[string]string{
		"inline table":       "mcp_servers = { frappe = { command = \"x\" } }\n",
		"dotted root key":    "mcp_servers.frappe.command = \"x\"\n",
		"key under servers":  "[mcp_servers]\nfrappe = { command = \"x\" }\n",
		"dotted under table": "[mcp_servers]\nother.command = \"x\"\n",
		"array of tables":    "[[mcp_servers.frappe]]\ncommand = \"x\"\n",
		"duplicate table":    "[mcp_servers.frappe]\ncommand = \"a\"\n[mcp_servers.frappe]\ncommand = \"b\"\n",
		"unclosed string":    "a = \"\"\"\nb\n",
		"unclosed array":     "a = [\n1,\n",
		"bad header":         "[mcp_servers.frappe\n",
		"quoted servers key": "\"mcp_servers\".frappe.command = \"x\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t, "linux")
			path, _ := ConfigPath(Codex, env)
			writeFile(t, path, content)
			_, err := Plan(Codex, testSrv, env)
			if err == nil || !strings.Contains(err.Error(), "left untouched") {
				t.Fatalf("want a refusal, got %v", err)
			}
		})
	}
}

func TestTOMLString(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\me\ffc.exe`: `"C:\\Users\\me\\ffc.exe"`,
		`a"b`:                 `"a\"b"`,
		"tab\there":           `"tab\there"`,
		"\x01\x7f":            `"\u0001\u007F"`,
		"é":                   `"é"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	if d := UnifiedDiff("a", "b", []byte("x\n"), []byte("x\n")); d != "" {
		t.Errorf("equal inputs: %q", d)
	}
	var a, b []string
	for i := 1; i <= 20; i++ {
		a = append(a, string(rune('a'+i)))
	}
	b = append(b, a...)
	b[2] = "changed"
	b = append(b[:15], append([]string{"inserted"}, b[15:]...)...)
	d := UnifiedDiff("old", "new", []byte(strings.Join(a, "\n")+"\n"), []byte(strings.Join(b, "\n")+"\n"))
	want := "--- old\n+++ new\n" +
		"@@ -1,6 +1,6 @@\n b\n c\n-d\n+changed\n e\n f\n g\n" +
		"@@ -13,6 +13,7 @@\n n\n o\n p\n+inserted\n q\n r\n s\n"
	if d != want {
		t.Fatalf("got:\n%s\nwant:\n%s", d, want)
	}
	// CRLF is not shown; a removed file has an empty new side.
	if d := UnifiedDiff("old", "new", []byte("a\r\nb\r\n"), nil); d != "--- old\n+++ new\n@@ -1,2 +0,0 @@\n-a\n-b\n" {
		t.Fatalf("got %q", d)
	}
}

func TestApply(t *testing.T) {
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Cursor, env)

	// A new file: parent directories created, mode 0600, no backup.
	c := mustPlan(t, Cursor, testSrv, env)
	backup, err := c.Apply()
	if err != nil || backup != "" {
		t.Fatalf("apply: %q %v", backup, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(c.New) {
		t.Fatal("content")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		di, _ := os.Stat(filepath.Dir(path))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("modes: file %v dir %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}

	// A replacement keeps the mode and backs up the old content; a backup of
	// the same second is not overwritten.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 10, 6, 9, 5, 7, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	stamp := path + ".ffc-20261006-090507.bak"
	writeFile(t, stamp, "older backup")
	prev := string(got)
	c = mustPlan(t, Cursor, Server{Name: "frappe", Command: "/other", Args: []string{"mcp"}}, env)
	backup, err = c.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if backup != path+".ffc-20261006-090507-1.bak" {
		t.Fatalf("backup %q", backup)
	}
	if b, _ := os.ReadFile(backup); string(b) != prev {
		t.Error("backup content")
	}
	if b, _ := os.ReadFile(stamp); string(b) != "older backup" {
		t.Error("existing backup overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != string(c.New) {
		t.Error("new content")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
			t.Errorf("mode not kept: %v", fi.Mode().Perm())
		}
	}
	// No temp file is left next to it.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ffc-tmp-") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}

	// A file changed after Plan is not overwritten.
	c = mustPlan(t, Cursor, testSrv, env)
	writeFile(t, path, `{"mcpServers": {}}`)
	if _, err := c.Apply(); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("want a conflict, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"mcpServers": {}}` {
		t.Error("file overwritten")
	}
}

func TestApplySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Cursor, env)
	target := filepath.Join(env.Home, "dotfiles", "mcp.json")
	writeFile(t, target, "{}")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	c := mustPlan(t, Cursor, testSrv, env)
	if _, err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if b, _ := os.ReadFile(target); string(b) != string(c.New) {
		t.Fatal("target not written")
	}
}

// fakeClaude records claude CLI calls.
type fakeClaude struct {
	calls [][]string
	fail  func(args []string) ([]byte, error)
}

func (f *fakeClaude) run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.fail != nil {
		return f.fail(args)
	}
	return []byte("ok"), nil
}

func claudeEnv(t *testing.T, goos, tool string) (Env, *fakeClaude) {
	env := testEnv(t, goos)
	f := &fakeClaude{}
	env.LookPath = func(name string) (string, error) {
		if tool == "" || name != "claude" {
			return "", exec.ErrNotFound
		}
		return tool, nil
	}
	env.Run = f.run
	return env, f
}

func TestClaudeCode(t *testing.T) {
	srv := Server{Name: "frappe", Command: "/usr/local/bin/ffc", Args: []string{"mcp", "--site", "it's"}}
	addJSON := `{"type":"stdio","command":"/usr/local/bin/ffc","args":["mcp","--site","it's"]}`

	t.Run("absent", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		c := mustPlan(t, ClaudeCode, srv, env)
		if c.Old != nil || c.New != nil || c.Diff() != "" || !c.Changed() || c.Replaces || !c.ToolFound() {
			t.Fatalf("change: %+v", c)
		}
		lines := c.CommandLines()
		if len(lines) != 1 || lines[0] != `claude mcp add-json --scope user frappe '{"type":"stdio","command":"/usr/local/bin/ffc","args":["mcp","--site","it'\''s"]}'` {
			t.Fatalf("lines: %q", lines)
		}
		if b, err := c.Apply(); err != nil || b != "" {
			t.Fatal(b, err)
		}
		if len(f.calls) != 1 || strings.Join(f.calls[0], " ") != "/bin/claude mcp add-json --scope user frappe "+addJSON {
			t.Fatalf("calls: %q", f.calls)
		}
		if _, err := os.Stat(c.Path); err == nil {
			t.Fatal("the state file was written")
		}
	})

	t.Run("replace", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		writeFile(t, filepath.Join(env.Home, ".claude.json"), `{"projects": {}, "mcpServers": {"frappe": {"command": "npx", "args": []}}}`)
		c := mustPlan(t, ClaudeCode, srv, env)
		if !c.Replaces || len(c.Commands) != 2 || c.Commands[0][2] != "remove" {
			t.Fatalf("commands: %q", c.Commands)
		}
		if _, err := c.Apply(); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 2 || strings.Join(f.calls[0], " ") != "/bin/claude mcp remove --scope user frappe" {
			t.Fatalf("calls: %q", f.calls)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		writeFile(t, filepath.Join(env.Home, ".claude.json"), `{"mcpServers": {"frappe": {"type": "stdio", "command": "/usr/local/bin/ffc", "args": ["mcp", "--site", "it's"], "env": {}}}}`)
		c := mustPlan(t, ClaudeCode, srv, env)
		if c.Changed() || !c.Replaces {
			t.Fatalf("change: %q", c.Commands)
		}
		if _, err := c.Apply(); err != nil || len(f.calls) != 0 {
			t.Fatal(err, f.calls)
		}
	})

	t.Run("unreadable state retries on exists", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		writeFile(t, filepath.Join(env.Home, ".claude.json"), `{not json`)
		adds := 0
		f.fail = func(args []string) ([]byte, error) {
			if args[1] == "add-json" {
				adds++
				if adds == 1 {
					return []byte("MCP server frappe already exists in user config"), errors.New("exit status 1")
				}
			}
			return nil, nil
		}
		c := mustPlan(t, ClaudeCode, srv, env)
		if len(c.Commands) != 1 {
			t.Fatalf("commands: %q", c.Commands)
		}
		if _, err := c.Apply(); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, call := range f.calls {
			got = append(got, call[2])
		}
		if strings.Join(got, ",") != "add-json,remove,add-json" || !c.Replaces {
			t.Fatalf("calls: %q", got)
		}
	})

	t.Run("failure shows output", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		f.fail = func([]string) ([]byte, error) { return []byte("Invalid configuration"), errors.New("exit status 1") }
		c := mustPlan(t, ClaudeCode, srv, env)
		if _, err := c.Apply(); err == nil || !strings.Contains(err.Error(), "Invalid configuration") {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("not on PATH", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "")
		c := mustPlan(t, ClaudeCode, srv, env)
		if c.ToolFound() || len(c.CommandLines()) != 1 {
			t.Fatal("tool found")
		}
		if _, err := c.Apply(); !errors.Is(err, ErrClaudeNotFound) || !strings.Contains(err.Error(), "claude mcp add-json") || len(f.calls) != 0 {
			t.Fatalf("err %v calls %q", err, f.calls)
		}
	})

	t.Run("windows batch file and quoting", func(t *testing.T) {
		env, f := claudeEnv(t, "windows", `C:\npm\claude.CMD`)
		c := mustPlan(t, ClaudeCode, Server{Name: "frappe", Command: `C:\Program Files\ffc\ffc.exe`, Args: []string{"mcp"}}, env)
		if got := c.CommandLines()[0]; got != `claude mcp add-json --scope user frappe '{"type":"stdio","command":"C:\\Program Files\\ffc\\ffc.exe","args":["mcp"]}'` {
			t.Fatalf("line %s", got)
		}
		if err := c.Check(); !errors.Is(err, ErrClaudeBatch) || !strings.Contains(err.Error(), "claude mcp add-json --scope user frappe '") {
			t.Fatalf("Check: %v", err)
		}
		if _, err := c.Apply(); !errors.Is(err, ErrClaudeBatch) || !strings.Contains(err.Error(), "batch file") || len(f.calls) != 0 {
			t.Fatalf("err %v", err)
		}
	})
}

func TestShellQuote(t *testing.T) {
	for _, tc := range []struct{ goos, in, want string }{
		{"linux", "frappe", "frappe"},
		{"linux", "--scope", "--scope"},
		{"linux", "a b", "'a b'"},
		{"linux", "it's", `'it'\''s'`},
		{"linux", "", "''"},
		{"windows", "it's", "'it''s'"},
		{"windows", `C:\x`, `'C:\x'`},
		{"windows", "@x", "'@x'"},
	} {
		if got := shellQuote(tc.goos, tc.in); got != tc.want {
			t.Errorf("%s %q: %s, want %s", tc.goos, tc.in, got, tc.want)
		}
	}
}

// entryOf parses a JSONC result and returns <top>.<name>.
func entryOf(t *testing.T, doc []byte, top, name string) map[string]interface{} {
	t.Helper()
	v, err := hujson.Standardize(append([]byte{}, doc...))
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, doc)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(v, &m); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	servers, _ := m[top].(map[string]interface{})
	e, _ := servers[name].(map[string]interface{})
	return e
}

func TestJSONBlockCommentBeforeClosingBrace(t *testing.T) {
	srv := Server{Name: "frappe", Command: "/x", Args: []string{"mcp"}}
	for name, tc := range map[string]struct{ old, keep string }{
		"in the servers object": {"{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"} /* note\n    end */ }\n}\n", "/* note\n    end */"},
		"at the top level":      {"{\n  \"x\": 1 /* a\n b */}", "/* a\n b */"},
	} {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t, "linux")
			path, _ := ConfigPath(Cursor, env)
			writeFile(t, path, tc.old)
			c := mustPlan(t, Cursor, srv, env)
			e := entryOf(t, c.New, "mcpServers", "frappe")
			if e == nil || e["command"] != "/x" {
				t.Fatalf("entry missing:\n%s", c.New)
			}
			if !strings.Contains(string(c.New), tc.keep) {
				t.Fatalf("comment changed:\n%s", c.New)
			}
			if _, err := c.Apply(); err != nil {
				t.Fatal(err)
			}
			if c2 := mustPlan(t, Cursor, srv, env); c2.Changed() {
				t.Fatalf("rerun changes again:\n%s", c2.Diff())
			}
		})
	}
}

func TestJSONResultCheck(t *testing.T) {
	entry := jsonEntry{Type: "stdio", Command: "/x", Args: []string{"mcp"}}
	good := `{"mcpServers": {"frappe": {"type": "stdio", "command": "/x", "args": ["mcp"]}}}`
	if err := checkJSONResult([]byte(good), "mcpServers", "frappe", entry, true); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"mcpServers": {}}`,
		`{"mcpServers": {/* "frappe": {} */}}`,
		`{"mcpServers": {"frappe": {"command": "/y"}}}`,
		`{"mcpServers": {"frappe": {"type": "stdio", "command": "/x", "args": ["mcp"],}}}`, // no longer strict
	} {
		if err := checkJSONResult([]byte(bad), "mcpServers", "frappe", entry, true); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestJSONKeepsCRLF(t *testing.T) {
	env := testEnv(t, "windows")
	path, _ := ConfigPath(VSCode, env)
	for _, old := range []string{
		"{\r\n\t\"servers\": {\r\n\t\t\"a\": {\"command\": \"a\"} // c\r\n\t}\r\n}\r\n",
		"{\r\n\t\"inputs\": []\r\n}\r\n",
		"{\r\n\t\"servers\": {\r\n\t\t\"frappe\": {\"command\": \"old\"}\r\n\t}\r\n}\r\n",
		" \r\n",
	} {
		writeFile(t, path, old)
		c := mustPlan(t, VSCode, testSrv, env)
		if strings.Count(string(c.New), "\n") != strings.Count(string(c.New), "\r\n") || !strings.Contains(string(c.New), "\r\n") {
			t.Errorf("mixed line endings for %q:\n%q", old, c.New)
		}
		if e := entryOf(t, c.New, "servers", "frappe"); e == nil || e["type"] != "stdio" {
			t.Errorf("entry missing for %q:\n%s", old, c.New)
		}
	}
}

func TestApplyReadOnly(t *testing.T) {
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Cursor, env)
	writeFile(t, path, "{}")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o200 != 0 {
		t.Skip("cannot make the file read-only here")
	}
	c := mustPlan(t, Cursor, testSrv, env)
	if err := c.Check(); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Check: %v", err)
	}
	if _, err := c.Apply(); !errors.Is(err, ErrReadOnly) || !strings.Contains(err.Error(), path) {
		t.Fatalf("Apply: %v", err)
	}
	if m, _ := filepath.Glob(path + ".ffc-*.bak"); len(m) != 0 {
		t.Fatalf("backup left behind: %v", m)
	}
	if b, _ := os.ReadFile(path); string(b) != "{}" {
		t.Fatal("file changed")
	}
}

func TestClaudeCodeRemovedThenAddFails(t *testing.T) {
	srv := Server{Name: "frappe", Command: "/usr/local/bin/ffc", Args: []string{"mcp"}}
	env, f := claudeEnv(t, "linux", "/bin/claude")
	writeFile(t, filepath.Join(env.Home, ".claude.json"), `{"mcpServers": {"frappe": {"command": "npx"}}}`)
	f.fail = func(args []string) ([]byte, error) {
		if args[1] == "add-json" {
			return []byte("Invalid configuration"), errors.New("exit status 1")
		}
		return nil, nil
	}
	c := mustPlan(t, ClaudeCode, srv, env)
	if err := c.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	_, err := c.Apply()
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	want := `claude mcp add-json --scope user frappe '{"type":"stdio","command":"/usr/local/bin/ffc","args":["mcp"]}'`
	for _, s := range []string{"Invalid configuration", `The old "frappe" entry was removed and the new one was not added`, want} {
		if !strings.Contains(msg, s) {
			t.Errorf("message misses %q:\n%s", s, msg)
		}
	}
	if len(f.calls) != 2 || f.calls[0][2] != "remove" {
		t.Errorf("calls %q", f.calls)
	}
}
