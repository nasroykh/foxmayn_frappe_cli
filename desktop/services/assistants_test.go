package services

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
)

// newAssistants is an AssistantsService over a temp home laid out like
// Windows, with ffc at a fixed path and a config holding two sites.
func newAssistants(t *testing.T, ffcFound bool) (*AssistantsService, mcpinstall.Env, *[][]string) {
	t.Helper()
	root := t.TempDir()
	env := mcpinstall.Env{
		Home:      filepath.Join(root, "home"),
		ConfigDir: filepath.Join(root, "home", "AppData", "Roaming"),
		GOOS:      "windows",
		Getenv:    func(string) string { return "" },
		LookPath:  func(string) (string, error) { return "", exec.ErrNotFound },
	}
	ran := &[][]string{}
	env.Run = func(name string, args ...string) ([]byte, error) {
		*ran = append(*ran, append([]string{name}, args...))
		return nil, nil
	}
	cfgPath := filepath.Join(root, "ffc", "config.yaml")
	store := sitesetup.Store{Path: cfgPath}
	if err := store.Init("prod", config.SiteConfig{URL: "https://erp.example.com", APIKey: "k", APISecret: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add("staging", config.SiteConfig{URL: "https://staging.example.com", APIKey: "k", APISecret: "s"}); err != nil {
		t.Fatal(err)
	}
	loc := newFFCLocator("windows", env.Home)
	loc.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	loc.getenv = func(string) string { return "" }
	loc.isFile = func(string) bool { return false }
	if ffcFound {
		loc.lookPath = func(string) (string, error) { return filepath.Join(root, "bin", "ffc.exe"), nil }
	}
	loc.version = func(string) (string, error) { return "v1.10.0", nil }
	s := NewAssistantsService(cfgPath, loc)
	s.defaultConfig = func() (string, error) { return cfgPath, nil }
	s.env = func() (mcpinstall.Env, error) { return env, nil }
	return s, env, ran
}

func find(l AssistantList, id string) Assistant {
	for _, a := range l.Assistants {
		if a.ID == id {
			return a
		}
	}
	return Assistant{}
}

func TestAssistantsConnectLifecycle(t *testing.T) {
	s, env, _ := newAssistants(t, true)

	l, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Assistants) != 5 || l.Assistants[0].ID != mcpinstall.ClaudeDesktop || l.EntryName != "frappe" || !l.FFC.Found {
		t.Fatalf("list = %+v", l)
	}
	cd := find(l, mcpinstall.ClaudeDesktop)
	if cd.Status != StatusNotConnected || cd.Detected || cd.Hint == "" {
		t.Errorf("claude desktop before = %+v", cd)
	}

	// Claude Desktop's folder exists once it has run: detected.
	if err := os.MkdirAll(filepath.Join(env.ConfigDir, "Claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	req := ConnectRequest{Client: mcpinstall.ClaudeDesktop, Site: "staging", ReadOnly: true}
	p, err := s.Preview(req)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Changed || p.Replaces || !p.CreatesFile || !p.CanApply || !strings.Contains(p.Diff, `"--site"`) || p.EntryName != "frappe" ||
		strings.Join(p.Server[1:], " ") != "mcp --site staging --read-only" {
		t.Errorf("preview = %+v", p)
	}
	res, err := s.Connect(req)
	if err != nil || !res.Changed || res.Backup != "" || res.Hint == "" {
		t.Fatalf("connect = %+v, %v", res, err)
	}
	cd = find(mustList(t, s), mcpinstall.ClaudeDesktop)
	if cd.Status != StatusConnected || cd.Site != "staging" || !cd.ReadOnly || !cd.Detected {
		t.Errorf("claude desktop after = %+v", cd)
	}

	// Connecting again with the same settings changes nothing.
	if p, _ := s.Preview(req); p.Changed {
		t.Errorf("same settings preview = %+v", p)
	}
	// Other settings replace the entry and back the old file up.
	res, err = s.Connect(ConnectRequest{Client: mcpinstall.ClaudeDesktop})
	if err != nil || res.Backup == "" {
		t.Fatalf("update = %+v, %v", res, err)
	}
	cd = find(mustList(t, s), mcpinstall.ClaudeDesktop)
	if cd.Status != StatusConnected || cd.Site != "" || cd.ReadOnly {
		t.Errorf("follow default = %+v", cd)
	}

	// An entry the app did not write is "different settings".
	path := cd.ConfigPath
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"frappe":{"command":"node","args":["old.js"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cd := find(mustList(t, s), mcpinstall.ClaudeDesktop); cd.Status != StatusDifferent {
		t.Errorf("foreign entry = %+v", cd)
	}
	// A file that does not parse is an error, and is never written.
	if err := os.WriteFile(path, []byte(`{"mcpServers": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cd := find(mustList(t, s), mcpinstall.ClaudeDesktop); cd.Status != StatusError || cd.Error == "" {
		t.Errorf("broken file = %+v", cd)
	}
	if _, err := s.Connect(ConnectRequest{Client: mcpinstall.ClaudeDesktop}); code(err) != CodeFailed {
		t.Errorf("connect over a broken file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"mcpServers": [` {
		t.Errorf("broken file was changed: %s", b)
	}
}

func mustList(t *testing.T, s *AssistantsService) AssistantList {
	t.Helper()
	l, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestAssistantsCodexAndCursor(t *testing.T) {
	s, env, _ := newAssistants(t, true)
	for _, id := range []string{mcpinstall.Codex, mcpinstall.Cursor, mcpinstall.VSCode} {
		if _, err := s.Connect(ConnectRequest{Client: id, Site: "prod"}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if a := find(mustList(t, s), id); a.Status != StatusConnected || a.Site != "prod" || !a.Detected {
			t.Errorf("%s = %+v", id, a)
		}
	}
	b, _ := os.ReadFile(filepath.Join(env.Home, ".codex", "config.toml"))
	if !strings.Contains(string(b), "[mcp_servers.frappe]") {
		t.Errorf("codex config = %s", b)
	}
}

func TestAssistantsClaudeCode(t *testing.T) {
	s, env, ran := newAssistants(t, true)
	if a := find(mustList(t, s), mcpinstall.ClaudeCode); a.Detected || a.Status != StatusNotConnected {
		t.Errorf("without claude = %+v", a)
	}
	p, err := s.Preview(ConnectRequest{Client: mcpinstall.ClaudeCode})
	if err != nil || p.CanApply || p.Problem == "" || len(p.Commands) != 1 || !strings.Contains(p.Commands[0], "add-json") {
		t.Errorf("preview without claude = %+v, %v", p, err)
	}
	if _, err := s.Connect(ConnectRequest{Client: mcpinstall.ClaudeCode}); code(err) != CodeUnavailable {
		t.Errorf("connect without claude: %v", err)
	}

	claude := filepath.Join(env.Home, "bin", "claude.exe")
	env.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return claude, nil
		}
		return "", exec.ErrNotFound
	}
	s.env = func() (mcpinstall.Env, error) { return env, nil }
	if a := find(mustList(t, s), mcpinstall.ClaudeCode); !a.Detected {
		t.Errorf("with claude = %+v", a)
	}
	if _, err := s.Connect(ConnectRequest{Client: mcpinstall.ClaudeCode, ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if len(*ran) != 1 || (*ran)[0][0] != claude || (*ran)[0][1] != "mcp" || (*ran)[0][2] != "add-json" {
		t.Errorf("ran = %q", *ran)
	}
}

func TestAssistantsNeedFFC(t *testing.T) {
	s, env, _ := newAssistants(t, false)
	if _, err := s.Preview(ConnectRequest{Client: mcpinstall.Cursor}); code(err) != CodeFFCMissing {
		t.Errorf("preview without ffc: %v", err)
	}
	// An existing entry still shows, as "different" (it cannot be matched
	// without knowing the binary).
	if err := os.MkdirAll(filepath.Join(env.Home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Home, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"frappe":{"type":"stdio","command":"C:\\ffc.exe","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := mustList(t, s)
	if l.FFC.Found || find(l, mcpinstall.Cursor).Status != StatusDifferent || find(l, mcpinstall.Codex).Status != StatusNotConnected {
		t.Errorf("list without ffc = %+v", l)
	}
}

func TestAssistantsRejectBadInput(t *testing.T) {
	s, _, _ := newAssistants(t, true)
	if _, err := s.Connect(ConnectRequest{Client: "chatgpt"}); code(err) != CodeInvalid {
		t.Errorf("unknown client: %v", err)
	}
	if _, err := s.Connect(ConnectRequest{Client: mcpinstall.Cursor, Site: "nope"}); code(err) != CodeInvalid {
		t.Errorf("unknown site: %v", err)
	}
	if _, err := s.Disconnect("chatgpt"); code(err) != CodeInvalid {
		t.Errorf("disconnect unknown client: %v", err)
	}
}

func TestDisconnectUsesPlanRemove(t *testing.T) {
	s, _, _ := newAssistants(t, true)
	if _, err := s.Connect(ConnectRequest{Client: mcpinstall.Cursor}); err != nil {
		t.Fatal(err)
	}
	var asked []string
	s.planRemove = func(client, name string, env mcpinstall.Env) (*mcpinstall.Change, error) {
		asked = append(asked, client, name)
		return nil, errors.New("boom")
	}
	if _, err := s.Disconnect(mcpinstall.Cursor); code(err) != CodeFailed {
		t.Errorf("disconnect error: %v", err)
	}
	if strings.Join(asked, " ") != "cursor frappe" {
		t.Errorf("planRemove got %q", asked)
	}
}

func TestDisconnectRemovesTheEntry(t *testing.T) {
	s, env, _ := newAssistants(t, true)
	for _, id := range []string{mcpinstall.Cursor, mcpinstall.Codex, mcpinstall.VSCode} {
		if _, err := s.Connect(ConnectRequest{Client: id, Site: "prod"}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		p, err := s.PreviewDisconnect(id)
		if err != nil || !p.Changed || !p.Replaces || !p.CanApply || p.CreatesFile || p.Diff == "" {
			t.Fatalf("%s preview = %+v, %v", id, p, err)
		}
		res, err := s.Disconnect(id)
		if err != nil || !res.Changed || res.Backup == "" || res.Hint == "" {
			t.Fatalf("%s disconnect = %+v, %v", id, res, err)
		}
		if a := find(mustList(t, s), id); a.Status != StatusNotConnected {
			t.Errorf("%s after = %+v", id, a)
		}
		// Nothing left to remove: no change, no error.
		if res, err := s.Disconnect(id); err != nil || res.Changed {
			t.Errorf("%s again = %+v, %v", id, res, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(env.Home, ".codex", "config.toml"))
	if strings.Contains(string(b), "mcp_servers.frappe") {
		t.Errorf("codex config still has the entry: %s", b)
	}
}

func TestDisconnectClaudeCode(t *testing.T) {
	s, env, ran := newAssistants(t, true)
	claude := filepath.Join(env.Home, "bin", "claude.exe")
	env.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return claude, nil
		}
		return "", exec.ErrNotFound
	}
	s.env = func() (mcpinstall.Env, error) { return env, nil }
	// The state file shows the entry, so the remove command is planned.
	state := `{"mcpServers":{"frappe":{"type":"stdio","command":"C:\\ffc.exe","args":["mcp"]}}}`
	if err := os.MkdirAll(env.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Home, ".claude.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewDisconnect(mcpinstall.ClaudeCode)
	if err != nil || !p.Changed || len(p.Commands) != 1 || !strings.Contains(p.Commands[0], "mcp remove --scope user frappe") {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	if _, err := s.Disconnect(mcpinstall.ClaudeCode); err != nil {
		t.Fatal(err)
	}
	if len(*ran) != 1 || (*ran)[0][0] != claude || strings.Join((*ran)[0][1:], " ") != "mcp remove --scope user frappe" {
		t.Errorf("ran = %q", *ran)
	}
}

// A config other than the CLI's default (FFC_CONFIG) goes into each entry as
// --config, and an entry with it still reads as connected.
func TestAssistantsPinNonDefaultConfig(t *testing.T) {
	s, env, _ := newAssistants(t, true)
	s.defaultConfig = func() (string, error) { return filepath.Join(env.Home, ".config", "ffc", "config.yaml"), nil }
	if _, err := s.Connect(ConnectRequest{Client: mcpinstall.Cursor, Site: "prod"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(env.Home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal([]string{"mcp", "--config", s.configPath, "--site", "prod"})
	var got struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if g, _ := json.Marshal(got.MCPServers["frappe"].Args); string(g) != string(want) {
		t.Errorf("args = %s, want %s", g, want)
	}
	if a := find(mustList(t, s), mcpinstall.Cursor); a.Status != StatusConnected || a.Site != "prod" {
		t.Errorf("cursor = %+v", a)
	}
}
