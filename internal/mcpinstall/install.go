// Package mcpinstall adds, replaces or removes the stdio MCP server entry
// that runs ffc in an AI client's user-level configuration.
//
// It never prompts and does not depend on cobra, so the CLI (ffc mcp install,
// ffc mcp uninstall) and other front ends share it. Plan (or PlanRemove)
// reads the client's config file and computes the new content; the caller
// shows the diff, asks, and calls Apply, which backs the old file up and
// writes the new one atomically.
//
// Config files are edited as text: JSON and JSONC through a syntax tree that
// keeps comments, key order and formatting (hujson), Codex's TOML line by
// line. A file that does not parse, or uses a form that cannot be edited
// safely, is refused and left untouched. Claude Code's user config is never
// edited: its own CLI is run instead.
package mcpinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// Supported clients.
const (
	ClaudeCode    = "claude-code"
	ClaudeDesktop = "claude-desktop"
	Cursor        = "cursor"
	VSCode        = "vscode"
	Codex         = "codex"
)

// Clients lists the supported clients in display order.
var Clients = []string{ClaudeCode, ClaudeDesktop, Cursor, VSCode, Codex}

// DefaultName is the entry name the old Node installer used, so installing
// over it replaces its entry instead of adding a second one.
const DefaultName = "frappe"

// maxConfigSize bounds a config file Plan reads (Claude Code's own state
// file gets maxClaudeStateSize). Client configs are a few KiB.
const maxConfigSize = 8 << 20

// ErrInvalid marks an error in the caller's input (client, name, server),
// as opposed to a problem with the config file: errors.Is(err, ErrInvalid).
var ErrInvalid = errors.New("invalid input")

type invalidError struct{ msg string }

func (e *invalidError) Error() string        { return e.msg }
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

func invalidf(format string, a ...interface{}) error {
	return &invalidError{fmt.Sprintf(format, a...)}
}

// ErrClaudeNotFound is returned by Apply for claude-code when the claude
// CLI is not on PATH. Change.Commands still holds what to run by hand.
var ErrClaudeNotFound = errors.New("the claude CLI is not on PATH")

// ErrClaudeBatch is returned for claude-code when claude is a Windows
// .cmd/.bat shim, which ffc does not run (cmd.exe re-parses arguments).
var ErrClaudeBatch = errors.New("cannot run the claude CLI")

// ErrReadOnly is returned when the config file is read-only.
var ErrReadOnly = errors.New("the config file is read-only")

// Server is the MCP server entry to install.
type Server struct {
	Name    string   // entry name, see ValidName
	Command string   // absolute path of the ffc binary
	Args    []string // e.g. ["mcp", "--site", "prod"]
}

// Env is the environment Plan works in. Tests build one over a temp dir to
// lay out any OS.
type Env struct {
	Home      string // the user's home directory
	ConfigDir string // the OS config dir: %AppData%, ~/Library/Application Support, $XDG_CONFIG_HOME or ~/.config
	GOOS      string
	Getenv    func(string) string                               // CODEX_HOME, CLAUDE_CONFIG_DIR; nil = none set
	LookPath  func(string) (string, error)                      // finds the claude CLI
	Run       func(name string, args ...string) ([]byte, error) // runs it; returns combined output
}

// DefaultEnv is the environment of the running process.
func DefaultEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, fmt.Errorf("finding the home directory: %w", err)
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return Env{}, fmt.Errorf("finding the user config directory: %w", err)
	}
	return Env{
		Home:      home,
		ConfigDir: cfgDir,
		GOOS:      runtimeGOOS,
		Getenv:    os.Getenv,
		LookPath:  exec.LookPath,
		Run:       runCommand,
	}, nil
}

// runTimeout bounds one claude CLI invocation.
const runTimeout = 2 * time.Minute

func runCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// Change is a planned edit of one client's config.
type Change struct {
	Client string
	Name   string
	// Path is the config file. For claude-code it is the state file the
	// claude CLI edits, which ffc only reads.
	Path string
	// Old is the current content (nil: the file does not exist), New the
	// content to write. Both nil for claude-code.
	Old, New []byte
	// Commands are the claude CLI invocations for claude-code, argv[0] =
	// "claude"; nil when the entry is already up to date.
	Commands [][]string
	// Replaces is true when an entry with this name exists (as far as ffc
	// could tell for claude-code). For PlanRemove it means the entry to
	// remove is present.
	Replaces bool
	// Hint tells the user how to make the client pick the change up.
	Hint string
	// Absent is set by Apply of a claude-code PlanRemove whose state file
	// could not be read, when claude answered that there was no such
	// entry: the command ran and nothing changed.
	Absent bool

	env    Env
	mode   fs.FileMode // of the existing file
	remove bool        // planned by PlanRemove
	// claude-code
	tool          string // resolved claude CLI, "" when not found
	retryOnExists bool   // add-json may meet an entry ffc could not see
	absentOK      bool   // remove: claude saying there is no such server is success
}

// Changed reports whether Apply would write or run anything.
func (c *Change) Changed() bool {
	if c.Client == ClaudeCode {
		return len(c.Commands) > 0
	}
	if c.Old == nil {
		return !c.remove // a missing file has no entry to remove
	}
	return !bytes.Equal(c.Old, c.New)
}

// Diff is a unified diff from the current file to the new one ("" for
// claude-code or when nothing changes).
func (c *Change) Diff() string {
	if c.Client == ClaudeCode || !c.Changed() {
		return ""
	}
	oldLabel := c.Path
	if c.Old == nil {
		oldLabel = "/dev/null"
	}
	return UnifiedDiff(oldLabel, c.Path, c.Old, c.New)
}

// CommandLines are Commands quoted for the user's shell (PowerShell on
// Windows, POSIX sh elsewhere), for printing.
func (c *Change) CommandLines() []string {
	out := make([]string, 0, len(c.Commands))
	for _, argv := range c.Commands {
		out = append(out, shellJoin(c.env.GOOS, argv))
	}
	return out
}

// Check reports whether Apply can make the change, so a caller can stop
// before asking the user: for claude-code that the claude CLI can be run
// (ErrClaudeNotFound, ErrClaudeBatch; the message holds the commands to run
// by hand), for a config file that it is not read-only (ErrReadOnly).
func (c *Change) Check() error {
	if !c.Changed() {
		return nil
	}
	if c.Client == ClaudeCode {
		return c.checkTool()
	}
	return checkWritable(c.Path)
}

// checkWritable refuses an existing file without the owner write bit
// (on Windows, the read-only attribute), whose rename would fail.
func checkWritable(path string) error {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	if fi.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("%w: %s; make it writable and run the command again", ErrReadOnly, path)
	}
	return nil
}

// Apply makes the change. For a config file it re-reads the file and
// refuses if it changed since Plan, copies it to
// <path>.ffc-<YYYYMMDD-HHMMSS>.bak (never over an existing file), creates
// missing parent directories (0700) and writes the new content atomically,
// keeping the file's mode (0600 for a new file). It returns the backup path
// ("" when there was no file). Nothing is written when nothing changed.
func (c *Change) Apply() (backup string, err error) {
	if !c.Changed() {
		return "", nil
	}
	if c.Client == ClaudeCode {
		return "", c.applyClaudeCode()
	}
	cur, exists, _, err := readFile(c.Path)
	if err != nil {
		return "", err
	}
	if exists != (c.Old != nil) || !bytes.Equal(cur, c.Old) {
		return "", fmt.Errorf("%s changed since it was read; run the command again", c.Path)
	}
	if err := checkWritable(c.Path); err != nil {
		return "", err // before the backup: nothing is left behind
	}
	mode := fs.FileMode(0o600)
	if c.Old != nil {
		mode = c.mode
		if backup, err = writeBackup(c.Path, c.Old, now()); err != nil {
			return "", err
		}
	}
	if err := config.WriteFileAtomic(c.Path, c.New, mode); err != nil {
		return backup, fmt.Errorf("writing %s: %w", c.Path, err)
	}
	return backup, nil
}

// now is the clock for backup names; tests replace it.
var now = time.Now

// writeBackup copies data next to path. An existing backup of the same
// second is never overwritten: a counter is added instead.
func writeBackup(path string, data []byte, t time.Time) (string, error) {
	base := path + ".ffc-" + t.Format("20060102-150405")
	for i := 0; i < 100; i++ {
		name := base + ".bak"
		if i > 0 {
			name = fmt.Sprintf("%s-%d.bak", base, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("backing up %s: %w", path, err)
		}
		_, werr := f.Write(data)
		if werr == nil {
			werr = f.Sync()
		}
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(name)
			return "", fmt.Errorf("backing up %s: %w", path, werr)
		}
		return name, nil
	}
	return "", fmt.Errorf("backing up %s: too many backups named %s*.bak", path, base)
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,63}$`)

// ValidName checks an entry name: 1 to 64 ASCII letters, digits, '_' or
// '-', not starting with '-' (it is a positional argument of the claude
// CLI, which would read it as an option). Every client accepts it as a key
// and Codex as a bare TOML key.
func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return invalidf("server name %q: use 1 to 64 letters, digits, '_' or '-', not starting with '-'", name)
	}
	return nil
}

func validServer(srv Server) error {
	if err := ValidName(srv.Name); err != nil {
		return err
	}
	if srv.Command == "" {
		return invalidf("empty server command")
	}
	for _, s := range append([]string{srv.Command}, srv.Args...) {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return invalidf("server argument %q is not valid UTF-8 text", s)
		}
	}
	return nil
}

// Plan computes the change that installs srv in client's user config.
func Plan(client string, srv Server, env Env) (*Change, error) {
	if err := validServer(srv); err != nil {
		return nil, err
	}
	if client == ClaudeCode {
		return planClaudeCode(srv, env)
	}
	path, err := ConfigPath(client, env)
	if err != nil {
		return nil, err
	}
	path, err = resolveLink(path)
	if err != nil {
		return nil, err
	}
	old, exists, mode, err := readFile(path)
	if err != nil {
		return nil, err
	}
	c := &Change{Client: client, Name: srv.Name, Path: path, env: env, mode: mode, Hint: hints[client]}
	if exists {
		c.Old = old
		if c.Old == nil {
			c.Old = []byte{}
		}
	}
	var replaced bool
	switch client {
	case ClaudeDesktop:
		// Claude Desktop's documented entries have no "type".
		c.New, replaced, err = editJSON(c.Old, "mcpServers", srv.Name, jsonEntry{Command: srv.Command, Args: srv.Args})
	case Cursor:
		c.New, replaced, err = editJSON(c.Old, "mcpServers", srv.Name, jsonEntry{Type: "stdio", Command: srv.Command, Args: srv.Args})
	case VSCode:
		c.New, replaced, err = editJSON(c.Old, "servers", srv.Name, jsonEntry{Type: "stdio", Command: srv.Command, Args: srv.Args})
	case Codex:
		c.New, replaced, err = editTOML(c.Old, srv)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; the file was left untouched", path, err)
	}
	c.Replaces = replaced
	return c, nil
}

// PlanRemove computes the change that removes the entry name from client's
// user config, the reverse of Plan. Without such an entry nothing changes
// (Changed is false, New equals Old); Replaces tells whether it is present.
// For claude-code the remove command is planned when the state file shows
// the entry or cannot be read; in the second case claude answering that
// there is no such server counts as success.
func PlanRemove(client, name string, env Env) (*Change, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	if client == ClaudeCode {
		return planClaudeCodeRemove(name, env), nil
	}
	path, err := ConfigPath(client, env)
	if err != nil {
		return nil, err
	}
	path, err = resolveLink(path)
	if err != nil {
		return nil, err
	}
	old, exists, mode, err := readFile(path)
	if err != nil {
		return nil, err
	}
	c := &Change{Client: client, Name: name, Path: path, env: env, mode: mode, Hint: removeHints[client], remove: true}
	if !exists {
		return c, nil
	}
	c.Old = old
	if c.Old == nil {
		c.Old = []byte{}
	}
	var found bool
	switch client {
	case ClaudeDesktop, Cursor:
		c.New, found, err = removeJSON(c.Old, "mcpServers", name)
	case VSCode:
		c.New, found, err = removeJSON(c.Old, "servers", name)
	case Codex:
		c.New, found, err = removeTOML(c.Old, name)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; the file was left untouched", path, err)
	}
	c.Replaces = found
	return c, nil
}

var removeHints = map[string]string{
	ClaudeCode:    "Start a new Claude Code session (or run /mcp) for the change to take effect.",
	ClaudeDesktop: "Fully quit Claude Desktop (also from the tray or menu bar) and start it again.",
	Cursor:        "Restart Cursor to drop the server.",
	VSCode:        "Reload VS Code to stop the server.",
	Codex:         "Start a new Codex session for the change to take effect.",
}

var hints = map[string]string{
	ClaudeCode:    "Start a new Claude Code session (or run /mcp) to use it.",
	ClaudeDesktop: "Fully quit Claude Desktop (also from the tray or menu bar) and start it again.",
	Cursor:        "Restart Cursor to load it.",
	VSCode:        "Reload VS Code, or run 'MCP: List Servers' from the command palette, to start it.",
	Codex:         "Start a new Codex session to use it.",
}

// ConfigPath is the user-level config file of a file-based client.
//
//   - claude-desktop: <ConfigDir>/Claude/claude_desktop_config.json
//   - cursor: ~/.cursor/mcp.json
//   - vscode: <ConfigDir>/Code/User/mcp.json (default profile)
//   - codex: $CODEX_HOME/config.toml, else ~/.codex/config.toml
func ConfigPath(client string, env Env) (string, error) {
	switch client {
	case ClaudeDesktop:
		return filepath.Join(env.ConfigDir, "Claude", "claude_desktop_config.json"), nil
	case Cursor:
		return filepath.Join(env.Home, ".cursor", "mcp.json"), nil
	case VSCode:
		return filepath.Join(env.ConfigDir, "Code", "User", "mcp.json"), nil
	case Codex:
		if h := env.getenv("CODEX_HOME"); h != "" {
			return filepath.Join(h, "config.toml"), nil
		}
		return filepath.Join(env.Home, ".codex", "config.toml"), nil
	case ClaudeCode:
		return claudeStatePath(env), nil
	}
	return "", invalidf("unknown client %q (want one of %s)", client, strings.Join(Clients, ", "))
}

// resolveLink follows a symlinked config file (dotfiles) so the atomic
// rename replaces its target, not the link. Only a link is resolved: on
// Windows EvalSymlinks would also expand 8.3 names.
func resolveLink(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return real, nil
}

// readFile reads a config file of at most maxConfigSize bytes. A missing
// file is not an error.
func readFile(path string) (data []byte, exists bool, mode fs.FileMode, err error) {
	return readLimited(path, maxConfigSize)
}

func readLimited(path string, limit int64) (data []byte, exists bool, mode fs.FileMode, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("%s is not a regular file", path)
	}
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, false, 0, fmt.Errorf("%s is larger than %d MiB; refusing to edit it", path, limit>>20)
	}
	return data, true, fi.Mode().Perm(), nil
}
