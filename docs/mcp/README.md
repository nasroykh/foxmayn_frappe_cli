# MCP server

Let AI assistants (Claude Desktop, Claude Code, Cursor, VS Code, Codex) read and change your Frappe sites through ffc, with per-site limits, confirmations and an audit log.

ffc is not an official Frappe project and is not affiliated with Frappe Technologies.

## What it is

`ffc mcp` starts a [Model Context Protocol](https://modelcontextprotocol.io) server. An AI client starts it (or connects to it) and gets a set of tools such as `list_docs`, `get_doc`, `update_doc` and `run_report`. Every tool call goes to your site with the credentials ffc already has for it, so the assistant can do what your user can do, no more, and less when you limit it.

The server runs on your computer. It speaks over stdio to the client that started it, or over HTTP on `127.0.0.1` only. Nothing is sent anywhere except to your own sites.

## Set it up in one command

```bash
ffc mcp install --client claude-desktop               # follows your default site
ffc mcp install --client cursor --site prod --read-only
```

Then restart the client. Clients: `claude-code`, `claude-desktop`, `cursor`, `vscode`, `codex`. Details per client: [Setup](setup.md).

Prefer buttons? [Foxmayn Frappe Desktop](../desktop/README.md) connects the same clients.

## Start safely

1. **Read-only first.** `--read-only` exposes only read tools: the assistant cannot create, change or delete anything.
2. **One site per connection.** Serve only the site a task needs.
3. **Keep confirmations on.** Deletes, cancels, merges and shares ask you first in clients that support it.
4. **Narrow with a policy.** Limit DocTypes, tools and methods per site in `config.yaml`.

All of this is in [Safety](safety.md).

## Pages

| Page | What it covers |
| --- | --- |
| [Setup](setup.md) | `ffc mcp install` / `uninstall` for each client, config file locations, manual config. |
| [Running the server](running.md) | stdio, HTTP and the detached background server; several sites in one server. |
| [Tools](tools.md) | Every tool, tool sets, limits, `jq`, resources, prompts and completion. |
| [Safety](safety.md) | Read-only mode, per-site policy, sensitive DocTypes, confirmations, audit log. |

## See also

- [Desktop app](../desktop/README.md)
- [Security](../security.md)
- [Configuration](../getting-started/configuration.md)
