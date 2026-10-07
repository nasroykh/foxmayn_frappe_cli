# MCP clients: where ffc mcp install writes

`ffc mcp install --client C [--site NAME] [--read-only] [--name frappe] [--print] [--yes]` and `ffc mcp uninstall --client C [--name frappe] [--print] [--yes]`.

| Client | What changes |
| --- | --- |
| `claude-code` | runs `claude mcp add-json --scope user <name> '<json>'` (remove: `claude mcp remove --scope user <name>`). ffc never edits `~/.claude.json`; the `claude` CLI must be on PATH |
| `claude-desktop` | `<config dir>/Claude/claude_desktop_config.json` (`mcpServers`). On Windows the Microsoft Store (MSIX) build's own copy under `%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\Claude\` is used when it exists |
| `cursor` | `~/.cursor/mcp.json` (`mcpServers`) |
| `vscode` | `<config dir>/Code/User/mcp.json`, default profile (`servers`) |
| `codex` | `~/.codex/config.toml`, or `$CODEX_HOME/config.toml` (`[mcp_servers.<name>]`) |

`<config dir>` is `%APPDATA%` on Windows, `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux.

Checked on a real Windows install: Cursor's and VS Code's Windows paths. UNVERIFIED (per the project's own notes): the VS Code paths on macOS and Linux are inferred; `CODEX_HOME` was checked against one codex-cli release only.

## Behaviour

- `--print` prints the diff (or the claude command) on stdout and writes nothing.
- Without a terminal, `--yes` is required.
- `--json` result: `{client, name, path, backup, changed, applied, server, command}` for install; uninstall the same without `server`.
- Bad `--client`, `--name` (may not start with `-`) or `--site`: exit 2.
- Codex: `mcp_servers` written as an inline table, dotted keys, or keys directly under `[mcp_servers]` are refused, file untouched.
- Uninstall removes only that entry (and comments on its own lines); an emptied `mcpServers`/`servers` stays `{}`. No entry or no file: "nothing to remove", exit 0.
- ChatGPT is not supported: it takes only remote MCP servers.
- `go run` builds are refused (the entry needs a stable binary path).

## Manual entry

If a client is not supported, add a stdio server by hand (use the absolute path of `ffc` when the client does not inherit your PATH):

```json
{
  "mcpServers": {
    "frappe": {
      "command": "ffc",
      "args": ["mcp", "--site", "prod", "--read-only"]
    }
  }
}
```

For HTTP clients: `ffc mcp --detach --site prod`, then point the client at `http://127.0.0.1:8765/mcp` with header `Authorization: Bearer <token from ffc mcp status>`.

The desktop app (ffc-setup skill, references/desktop-app.md) does the same install and uninstall with buttons.
