package mcpinstall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

var runtimeGOOS = runtime.GOOS

// Claude Code keeps user-scope servers in ~/.claude.json (in
// $CLAUDE_CONFIG_DIR when that is set), a large state file it rewrites
// itself. ffc reads it only to see whether the entry exists and is up to
// date, and runs the claude CLI to change it:
//
//	claude mcp add-json --scope user <name> '<json>'
//
// add-json refuses a name that exists in that scope ("MCP server <name>
// already exists in user config", claude 2.1), so a replacement first runs
// claude mcp remove --scope user <name>. "claude mcp get" is not used to
// check: it has no --scope and starts the server to health-check it.

// maxClaudeStateSize bounds the read of Claude Code's state file, which
// holds project history and can grow to several MiB.
const maxClaudeStateSize = 64 << 20

func claudeStatePath(env Env) string {
	if d := env.getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(env.Home, ".claude.json")
}

func planClaudeCode(srv Server, env Env) (*Change, error) {
	entry := jsonEntry{Type: "stdio", Command: srv.Command, Args: srv.Args}
	if entry.Args == nil {
		entry.Args = []string{}
	}
	js, err := marshalIndent(entry, "", "")
	if err != nil {
		return nil, err
	}
	c := &Change{Client: ClaudeCode, Name: srv.Name, Path: claudeStatePath(env), env: env, Hint: hints[ClaudeCode]}

	existing, known := claudeUserEntry(c.Path, srv.Name)
	switch {
	case known && existing != nil && sameClaudeEntry(existing, entry):
		c.Replaces = true
		return c, nil // up to date: no commands
	case known && existing != nil:
		c.Replaces = true
		c.Commands = append(c.Commands, []string{"claude", "mcp", "remove", "--scope", "user", srv.Name})
	default:
		// Not there, or the state file could not be read: add-json says if
		// the name exists after all, and Apply then replaces it.
		c.retryOnExists = true
	}
	c.Commands = append(c.Commands, []string{"claude", "mcp", "add-json", "--scope", "user", srv.Name, string(js)})

	if env.LookPath != nil {
		if p, err := env.LookPath("claude"); err == nil {
			c.tool = p
		}
	}
	return c, nil
}

// ToolFound reports whether the claude CLI was found (claude-code only).
func (c *Change) ToolFound() bool { return c.tool != "" }

// claudeUserEntry returns the user-scope entry name from Claude Code's
// state file: (entry, true) when it exists, (nil, true) when it does not,
// (nil, false) when the file cannot be read or parsed.
func claudeUserEntry(path, name string) (json.RawMessage, bool) {
	data, exists, _, err := readLimited(path, maxClaudeStateSize)
	if err != nil {
		return nil, false
	}
	if !exists {
		return nil, true
	}
	var state struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false
	}
	return state.MCPServers[name], true
}

// sameClaudeEntry reports whether a stored entry runs the same command: type
// stdio or absent, the same command and args, and nothing else but an empty
// env (claude mcp add writes one).
func sameClaudeEntry(raw json.RawMessage, want jsonEntry) bool {
	var have map[string]interface{}
	if err := json.Unmarshal(raw, &have); err != nil {
		return false
	}
	if t, ok := have["type"]; ok && t != "stdio" {
		return false
	}
	if have["command"] != want.Command {
		return false
	}
	args, _ := have["args"].([]interface{})
	wantArgs := make([]interface{}, len(want.Args))
	for i, a := range want.Args {
		wantArgs[i] = a
	}
	if len(args) != len(wantArgs) || (len(args) > 0 && !reflect.DeepEqual(args, wantArgs)) {
		return false
	}
	for k, v := range have {
		switch k {
		case "type", "command", "args":
		case "env":
			if m, ok := v.(map[string]interface{}); !ok || len(m) > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (c *Change) applyClaudeCode() error {
	if c.tool == "" {
		return ErrClaudeNotFound
	}
	if c.env.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(c.tool)) {
		case ".cmd", ".bat":
			// cmd.exe would re-parse the JSON argument (quotes, %, ^, &).
			return fmt.Errorf("the claude CLI is a batch file (%s), which cannot be given a JSON argument safely; run the command shown above yourself", c.tool)
		}
	}
	if c.env.Run == nil {
		return fmt.Errorf("internal error: no command runner")
	}
	run := func(argv []string) ([]byte, error) {
		out, err := c.env.Run(c.tool, argv[1:]...)
		if err != nil {
			return out, fmt.Errorf("%s: %w%s", strings.Join(argv[:3], " "), err, outputSuffix(out))
		}
		return out, nil
	}
	removed := false
	for _, argv := range c.Commands {
		out, err := run(argv)
		if err == nil {
			removed = removed || argv[2] == "remove"
			continue
		}
		if c.retryOnExists && argv[2] == "add-json" && bytes.Contains(out, []byte("already exists")) {
			// The entry exists although the state file did not show it.
			if _, rerr := run([]string{"claude", "mcp", "remove", "--scope", "user", c.Name}); rerr != nil {
				return rerr
			}
			removed = true
			if _, err = run(argv); err == nil {
				c.Replaces = true
				continue
			}
		}
		if removed {
			return fmt.Errorf("%w (the old %q entry was removed: run the add-json command above to add it)", err, c.Name)
		}
		return err
	}
	return nil
}

func outputSuffix(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	if len(s) > 2000 {
		s = s[:2000] + "…"
	}
	return ": " + s
}

// shellJoin quotes argv for PowerShell on Windows and POSIX sh elsewhere.
func shellJoin(goos string, argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(goos, a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(goos, s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@") == "" {
		if goos != "windows" || !strings.ContainsAny(s, "@") {
			return s
		}
	}
	if goos == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
