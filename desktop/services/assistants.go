package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// Connection states of an assistant.
const (
	StatusConnected    = "connected"     // the entry is there with these exact settings
	StatusDifferent    = "different"     // an entry of this name is there with other settings
	StatusNotConnected = "not_connected" // no entry
	StatusError        = "error"         // the client's config could not be read
)

var clientNames = map[string]string{
	mcpinstall.ClaudeDesktop: "Claude Desktop",
	mcpinstall.ClaudeCode:    "Claude Code",
	mcpinstall.Cursor:        "Cursor",
	mcpinstall.VSCode:        "VS Code",
	mcpinstall.Codex:         "Codex",
}

// clientOrder puts the chat app for everyday users first.
var clientOrder = []string{mcpinstall.ClaudeDesktop, mcpinstall.ClaudeCode, mcpinstall.Cursor, mcpinstall.VSCode, mcpinstall.Codex}

// Assistant is one AI client and its connection to ffc.
type Assistant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Detected: the client looks installed. Claude Code: the claude CLI is
	// on PATH (it is what the app runs). The others: the folder of their
	// user config exists (Claude Desktop, VS Code and Codex create it on
	// first start, Cursor its ~/.cursor folder). A client that was never
	// started counts as not detected; connecting still works for those.
	Detected bool   `json:"detected"`
	Status   string `json:"status"`
	// Site and ReadOnly are the settings of a connected entry: Site "" is
	// "follow the default site".
	Site     string `json:"site"`
	ReadOnly bool   `json:"readOnly"`
	// ConfigPath is the client's config file (Claude Code: its state file,
	// which only the claude CLI writes).
	ConfigPath string `json:"configPath"`
	Hint       string `json:"hint"`
	Error      string `json:"error,omitempty"`
}

// AssistantList is every supported client.
type AssistantList struct {
	FFC        FFCInfo     `json:"ffc"`
	EntryName  string      `json:"entryName"`
	Assistants []Assistant `json:"assistants"`
}

// ConnectRequest says how to connect a client.
type ConnectRequest struct {
	Client string `json:"client"`
	// Site pins one site; "" follows the default site.
	Site     string `json:"site"`
	ReadOnly bool   `json:"readOnly"`
}

// Preview is what connecting (or disconnecting) would change.
type Preview struct {
	Client    string `json:"client"`
	EntryName string `json:"entryName"`
	Path      string `json:"path"`
	// Changed: false when the entry is already exactly like this.
	Changed  bool `json:"changed"`
	Replaces bool `json:"replaces"`
	// CreatesFile: the config file does not exist yet.
	CreatesFile bool `json:"createsFile"`
	// Diff is the unified diff of the config file ("" for Claude Code).
	Diff string `json:"diff"`
	// Commands are the claude CLI commands (Claude Code only).
	Commands []string `json:"commands"`
	// Server is the entry's command line.
	Server []string `json:"server"`
	// CanApply is false when the change cannot be made here; Problem says
	// why (claude CLI missing, read-only file).
	CanApply bool   `json:"canApply"`
	Problem  string `json:"problem,omitempty"`
	Hint     string `json:"hint"`
}

// ApplyResult is the outcome of Connect or Disconnect.
type ApplyResult struct {
	Changed bool `json:"changed"`
	// Backup is the copy of the old config file ("" when there was none).
	Backup string `json:"backup"`
	Hint   string `json:"hint"`
}

// AssistantsService connects AI clients to ffc: it adds, updates or removes
// the MCP server entry that runs "ffc mcp" in each client's user config,
// through internal/mcpinstall (the same code as "ffc mcp install").
type AssistantsService struct {
	configPath string
	ffc        *FFCLocator
	env        func() (mcpinstall.Env, error)
	dirExists  func(string) bool
	planRemove func(client, name string, env mcpinstall.Env) (*mcpinstall.Change, error)
}

// NewAssistantsService builds the service. configPath is the ffc config
// whose sites can be pinned.
func NewAssistantsService(configPath string, ffc *FFCLocator) *AssistantsService {
	return &AssistantsService{
		configPath: configPath,
		ffc:        ffc,
		env:        desktopEnv,
		dirExists:  isDir,
		planRemove: planRemove,
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// desktopEnv is mcpinstall's environment, with the claude CLI run without a
// console window (the app has none).
func desktopEnv() (mcpinstall.Env, error) {
	env, err := mcpinstall.DefaultEnv()
	if err != nil {
		return env, err
	}
	env.Run = func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		hideWindow(cmd)
		return cmd.CombinedOutput()
	}
	return env, nil
}

// server is the entry for req, run by the ffc binary at exe.
func server(exe string, site string, readOnly bool) mcpinstall.Server {
	args := []string{"mcp"}
	if site != "" {
		args = append(args, "--site", site)
	}
	if readOnly {
		args = append(args, "--read-only")
	}
	return mcpinstall.Server{Name: mcpinstall.DefaultName, Command: exe, Args: args}
}

func (s *AssistantsService) siteNames() []string {
	cfg, err := config.Read(s.configPath)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Sites))
	for n := range cfg.Sites {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// List returns every client with its connection state.
func (s *AssistantsService) List() (AssistantList, error) {
	env, err := s.env()
	if err != nil {
		return AssistantList{}, newError(CodeFailed, "Your user folders could not be found.", err)
	}
	ffc := s.ffc.Info()
	out := AssistantList{FFC: ffc, EntryName: mcpinstall.DefaultName, Assistants: []Assistant{}}
	sites := s.siteNames()
	for _, id := range clientOrder {
		out.Assistants = append(out.Assistants, s.status(id, env, ffc, sites))
	}
	return out, nil
}

// status works out a client's state by planning the entry for every
// setting the app can write (follow the default or each site, read-only or
// not): an up-to-date plan names the settings. An entry that matches none
// (another ffc path, flags added by hand, the old Node installer) is
// "different".
func (s *AssistantsService) status(id string, env mcpinstall.Env, ffc FFCInfo, sites []string) Assistant {
	a := Assistant{ID: id, Name: clientNames[id], Status: StatusNotConnected}
	a.ConfigPath, _ = mcpinstall.ConfigPath(id, env)
	a.Detected = s.detected(id, env, a.ConfigPath)

	exe := ffc.Path
	if !ffc.Found {
		// Any absolute command tells whether an entry exists.
		exe = filepath.Join(env.Home, "ffc")
	}
	candidates := []struct {
		site     string
		readOnly bool
	}{{"", false}, {"", true}}
	for _, n := range sites {
		candidates = append(candidates, struct {
			site     string
			readOnly bool
		}{n, false}, struct {
			site     string
			readOnly bool
		}{n, true})
	}
	for i, c := range candidates {
		ch, err := mcpinstall.Plan(id, server(exe, c.site, c.readOnly), env)
		if err != nil {
			a.Status, a.Error = StatusError, text.Sanitize(err.Error())
			return a
		}
		if i == 0 {
			a.Hint = ch.Hint
			if !ch.Replaces {
				return a // no entry at all
			}
			a.Status = StatusDifferent
			if !ffc.Found {
				return a
			}
		}
		if !ch.Changed() {
			a.Status, a.Site, a.ReadOnly = StatusConnected, c.site, c.readOnly
			return a
		}
	}
	return a
}

func (s *AssistantsService) detected(id string, env mcpinstall.Env, configPath string) bool {
	if id == mcpinstall.ClaudeCode {
		if env.LookPath == nil {
			return false
		}
		_, err := env.LookPath("claude")
		return err == nil
	}
	return configPath != "" && s.dirExists(filepath.Dir(configPath))
}

// plan validates req and plans the entry.
func (s *AssistantsService) plan(req ConnectRequest) (*mcpinstall.Change, mcpinstall.Server, error) {
	if _, ok := clientNames[req.Client]; !ok {
		return nil, mcpinstall.Server{}, invalid("client", "Unknown assistant.")
	}
	ffc := s.ffc.Info()
	if !ffc.Found {
		return nil, mcpinstall.Server{}, &Error{Code: CodeFFCMissing, Message: "Install ffc first: assistants reach your sites through it."}
	}
	if req.Site != "" {
		cfg, err := config.Read(s.configPath)
		if err != nil {
			return nil, mcpinstall.Server{}, newError(CodeFailed, "The ffc config file could not be read.", err)
		}
		if _, ok := cfg.Sites[req.Site]; !ok {
			return nil, mcpinstall.Server{}, invalid("site", fmt.Sprintf("There is no site called %q any more.", req.Site))
		}
	}
	env, err := s.env()
	if err != nil {
		return nil, mcpinstall.Server{}, newError(CodeFailed, "Your user folders could not be found.", err)
	}
	srv := server(ffc.Path, req.Site, req.ReadOnly)
	ch, err := mcpinstall.Plan(req.Client, srv, env)
	if err != nil {
		return nil, srv, newError(CodeFailed, "The assistant's settings file could not be read, so it was left as it is.", err)
	}
	return ch, srv, nil
}

func preview(ch *mcpinstall.Change, srv mcpinstall.Server) Preview {
	p := Preview{
		Client:      ch.Client,
		EntryName:   ch.Name,
		Path:        ch.Path,
		Changed:     ch.Changed(),
		Replaces:    ch.Replaces,
		CreatesFile: ch.Client != mcpinstall.ClaudeCode && ch.Old == nil,
		Diff:        text.Sanitize(ch.Diff()),
		Commands:    []string{},
		Hint:        ch.Hint,
		CanApply:    true,
	}
	if srv.Command != "" {
		p.Server = append([]string{srv.Command}, srv.Args...)
	}
	for _, l := range ch.CommandLines() {
		p.Commands = append(p.Commands, text.Sanitize(l))
	}
	if p.Changed {
		if err := ch.Check(); err != nil {
			p.CanApply, p.Problem = false, problemText(err)
		}
	}
	return p
}

func problemText(err error) string {
	switch {
	case errors.Is(err, mcpinstall.ErrClaudeNotFound):
		return "The claude command was not found. Install Claude Code first, or run the commands below yourself."
	case errors.Is(err, mcpinstall.ErrClaudeBatch):
		return "The claude command is a Windows script the app does not run. Run the commands below yourself."
	case errors.Is(err, mcpinstall.ErrReadOnly):
		return "The assistant's settings file is read-only."
	}
	return text.Sanitize(err.Error())
}

// Preview shows what Connect would change, without changing anything.
func (s *AssistantsService) Preview(req ConnectRequest) (Preview, error) {
	ch, srv, err := s.plan(req)
	if err != nil {
		return Preview{}, err
	}
	return preview(ch, srv), nil
}

// Connect adds or updates the entry. The old config file is kept as
// <file>.ffc-<stamp>.bak.
func (s *AssistantsService) Connect(req ConnectRequest) (ApplyResult, error) {
	ch, _, err := s.plan(req)
	if err != nil {
		return ApplyResult{}, err
	}
	return apply(ch)
}

func apply(ch *mcpinstall.Change) (ApplyResult, error) {
	res := ApplyResult{Changed: ch.Changed(), Hint: ch.Hint}
	if !res.Changed {
		return res, nil
	}
	if err := ch.Check(); err != nil {
		return ApplyResult{}, &Error{Code: CodeUnavailable, Message: problemText(err)}
	}
	backup, err := ch.Apply()
	if err != nil {
		return ApplyResult{}, newError(CodeFailed, "The assistant's settings could not be changed.", err)
	}
	res.Backup = backup
	return res, nil
}

// PreviewDisconnect shows what Disconnect would change.
func (s *AssistantsService) PreviewDisconnect(client string) (Preview, error) {
	ch, err := s.removal(client)
	if err != nil {
		return Preview{}, err
	}
	return preview(ch, mcpinstall.Server{}), nil
}

// Disconnect removes the entry from the client's config.
func (s *AssistantsService) Disconnect(client string) (ApplyResult, error) {
	ch, err := s.removal(client)
	if err != nil {
		return ApplyResult{}, err
	}
	return apply(ch)
}

func (s *AssistantsService) removal(client string) (*mcpinstall.Change, error) {
	if _, ok := clientNames[client]; !ok {
		return nil, invalid("client", "Unknown assistant.")
	}
	env, err := s.env()
	if err != nil {
		return nil, newError(CodeFailed, "Your user folders could not be found.", err)
	}
	ch, err := s.planRemove(client, mcpinstall.DefaultName, env)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return nil, e
		}
		return nil, newError(CodeFailed, "The assistant's settings file could not be read, so it was left as it is.", err)
	}
	return ch, nil
}
