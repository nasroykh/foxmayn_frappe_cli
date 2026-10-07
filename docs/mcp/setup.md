# MCP setup

Add ffc to an AI client's MCP configuration with `ffc mcp install`, and remove it with `ffc mcp uninstall`.

ffc is not an official Frappe project and is not affiliated with Frappe Technologies.

## Install

```bash
ffc mcp install --client claude-desktop                  # follows 'ffc site use'
ffc mcp install --client cursor --site prod --read-only
ffc mcp install --client codex --print                   # show the change, write nothing
ffc mcp install --client vscode --name frappe-prod --site prod --yes
```

| Flag | Default | Description |
| --- | --- | --- |
| `--client` | | Required: `claude-code`, `claude-desktop`, `cursor`, `vscode` or `codex`. |
| `--site` | | Pin the entry to this site. Without it, the server uses your default site, so `ffc site use` switches it. `FFC_SITE` is not pinned. |
| `--read-only` | off | Add `--read-only` to the entry (only read tools). |
| `--name` | `frappe` | Entry name: 1-64 letters, digits, `_` or `-`, not starting with `-`. |
| `--print` | off | Print the diff (or the `claude` command) on stdout and change nothing. |
| `-y, --yes` | off | Write without asking. Needed without a terminal. |

The entry runs **this ffc binary by its absolute path** with `mcp` plus the flags above, so it does not depend on the client's `PATH` and keeps working after `ffc update`. A non-default config file (`--config` or `FFC_CONFIG`) is added as `--config <absolute path>`. A temporary `go run` build is refused: install ffc first.

The default name `frappe` is the one the older Node-based installer used, so an entry from it is replaced, not duplicated.

After installing, restart the client:

| Client | What to do |
| --- | --- |
| Claude Desktop | Fully quit it (also from the tray or menu bar) and start it again. |
| Claude Code | Start a new session, or run `/mcp`. |
| Cursor | Restart Cursor. |
| VS Code | Reload the window, or run **MCP: List Servers** from the command palette. |
| Codex | Start a new Codex session. |

## Where each client's entry goes

`<config dir>` is `%APPDATA%` on Windows, `~/Library/Application Support` on macOS, and `$XDG_CONFIG_HOME` or `~/.config` on Linux.

| Client | File | Entry |
| --- | --- | --- |
| `claude-code` | No file is edited. ffc runs `claude mcp add-json --scope user <name> '<json>'` (and `claude mcp remove` first to replace an entry). Claude Code keeps user servers in `~/.claude.json` (`$CLAUDE_CONFIG_DIR/.claude.json` when set); ffc only reads it. | Needs `claude` on `PATH`. |
| `claude-desktop` | `<config dir>/Claude/claude_desktop_config.json`. Windows Store (MSIX) install: `%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\Claude\claude_desktop_config.json`, used when that file exists or the package is installed and there is no `%APPDATA%` file. | `mcpServers.<name>` = `{command, args}` |
| `cursor` | `~/.cursor/mcp.json` | `mcpServers.<name>` = `{type: "stdio", command, args}` |
| `vscode` | `<config dir>/Code/User/mcp.json` (default profile; `settings.json` is never touched) | `servers.<name>` = `{type: "stdio", command, args}` |
| `codex` | `~/.codex/config.toml`, or `$CODEX_HOME/config.toml` when set | `[mcp_servers.<name>]` with `command` and `args` |

Claude Desktop on Linux is unofficial; the path above is what ffc uses there.

On Windows, the Cursor path (`%USERPROFILE%\.cursor\mcp.json`) and the VS Code path (`%APPDATA%\Code\User\mcp.json`) were checked on a real install: both editors keep their MCP servers there. **Not verified:** the VS Code paths on macOS and Linux are inferred from its documentation, not quoted from it. `CODEX_HOME` was checked against the Codex CLI but is not in its documentation.

## How the file is changed

- **Preview and confirmation.** The target path and a unified diff are shown (for `claude-code`, the commands), then ffc asks. Nothing changes when the entry is already up to date.
- **Backup.** An existing file is copied to `<file>.ffc-<YYYYMMDD-HHMMSS>.bak` first, then written atomically, keeping its file mode (0600 for a new file).
- **Only the entry changes.** JSON and JSONC files keep their comments, key order and formatting. Codex's TOML is edited as text, so comments stay. The whole entry is replaced, so keys you added to it by hand (such as `env`) are dropped; they show in the diff.
- **Refusals.** A file that does not parse is refused and left untouched (an empty file counts as `{}`), and so is a read-only file. In Codex's file, an `mcp_servers` written as an inline table, with dotted keys, as keys under `[mcp_servers]` or as `[[mcp_servers...]]` is refused: move the entry to a `[mcp_servers.<name>]` table or edit it by hand.
- With `--json` the result is `{client, name, path, backup, changed, applied, server, command}`.

### Claude Code notes

- When `claude` is not on `PATH`, ffc prints the command to run yourself (exit 2).
- On Windows, ffc does not run `claude` when it is a `.cmd`/`.bat` shim (an npm install), because `cmd.exe` would mangle the JSON argument. Run the printed command yourself. It is quoted for PowerShell 7; Windows PowerShell 5.1 drops the inner double quotes.

## Uninstall

```bash
ffc mcp uninstall --client claude-desktop
ffc mcp uninstall --client codex --print
ffc mcp uninstall --client vscode --name frappe-prod --yes
```

Takes `--client`, `--name` (default `frappe`), `--print` and `-y/--yes`. It uses the same files, preview, confirmation, backup and refusals as `install`. Only the named entry goes: other servers, comments outside the entry and formatting stay. For `claude-code` it runs `claude mcp remove --scope user <name>` (a `.cmd` shim is fine here). When there is no such entry, nothing changes ("nothing to remove", exit 0).

## Manual configuration

Any MCP client that can start a stdio server can run ffc. The command is the ffc binary, the arguments are `mcp` plus any [server flags](running.md#server-flags):

```json
{
  "mcpServers": {
    "frappe": {
      "command": "/usr/local/bin/ffc",
      "args": ["mcp", "--site", "prod", "--read-only"]
    }
  }
}
```

Use the absolute path of ffc (`command -v ffc`, or `where ffc` on Windows).

**ChatGPT** is not supported: it connects only to remote MCP servers, and ffc is a local server.

## See also

- [Running the server](running.md)
- [Safety](safety.md)
- [Desktop app: assistants](../desktop/using.md#assistants)
