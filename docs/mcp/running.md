# Running the MCP server

Run `ffc mcp` over stdio, over HTTP in the foreground, or as a detached background server, for one site or several.

## Three ways to run it

| Mode | Command | Use it for |
| --- | --- | --- |
| stdio (default) | `ffc mcp --site prod` | What AI clients start themselves. `ffc mcp install` writes this. |
| HTTP, foreground | `ffc mcp --port 8765 --site prod` | Testing, for example with the MCP Inspector. Ctrl+C stops it. |
| HTTP, detached | `ffc mcp --detach [--port 8765] [--site prod]` | A background server that clients connect to by URL. |

The HTTP endpoint is `http://127.0.0.1:<port>/mcp` (Streamable HTTP transport). It listens on the loopback interface only and requires a bearer token. In the foreground the token is printed when stderr is a terminal; for a detached server, `ffc mcp status` shows it.

## Detached server

```bash
ffc mcp --detach --site prod     # default port 8765
ffc mcp status                   # PID, URL, bearer token, site(s), start time, log path
ffc mcp stop                     # stop it and clean up
ffc mcp stop --force             # stop the recorded PID even if it does not answer
```

The state file `~/.config/ffc/mcp.json` (mode 0600, it holds the token) and the log `~/.config/ffc/mcp.log` live next to the default config. Only one detached server runs at a time. `ffc doctor` checks its health and the state file's permissions.

Configure a client that supports HTTP servers with the URL and an `Authorization: Bearer <token>` header. The token changes each time the server starts.

## Server flags

| Flag | Description |
| --- | --- |
| `--site` (global) | The site to serve (default: `default_site`). |
| `--read-only` | Expose only read tools: no create, update, delete, bulk, lifecycle, workflow or `call_method`. |
| `--toolsets` | Expose only these tool sets: `core`, `lifecycle`, `collab`, `admin`, `files`, `erp`. Default `core,lifecycle`. See [Tools](tools.md#tool-sets). |
| `--allow-tools` | Expose only these tools (narrows the site's `allow_tools`). |
| `--allow-doctypes` | Allow only these DocTypes (narrows the config; never unlocks a sensitive DocType). |
| `--deny-doctypes` | Refuse these DocTypes, in addition to the config. |
| `--allow-methods` | `call_method` may call only these methods; a trailing `*` is a prefix. |
| `--deny-methods` | Refuse these methods in `call_method`, in addition to the config. |
| `--confirm` | `always` or `if-supported`: ask before deleting, cancelling or merging. Only tightens the config; `never` is refused. |
| `--sites` | Serve these sites (comma-separated). |
| `--all-sites` | Serve every site in the config. |
| `-p, --port` | HTTP mode on this port (default 8765 with `--detach`). |
| `-d, --detach` | Run as a background HTTP server. |

The flags only narrow what the site's config allows, so a client's config cannot widen what the site's owner allowed. How they combine with the config: [Safety](safety.md).

The server uses the same config, credentials and OAuth refresh as every other ffc command. It checks the credentials at start.

## Several sites in one server

```bash
ffc mcp --sites prod,staging     # these sites; the default is --site, else default_site, else the first
ffc mcp --all-sites              # every site in the config
```

Every tool then takes a required `site` argument (one of the served sites), so a call never lands on a site by default. The `list_sites` tool lists them: name, URL, how ffc signs in, and whether it is read-only (never secrets).

- Each site's own `mcp:` policy applies to calls on that site. Write tools are exposed when any served site allows them; a write to a read-only site is refused.
- The policy flags apply to every site.
- The set of sites is fixed at start. A served site later removed or renamed in the config is refused, never matched to another site.
- Only the default site signs in at start; the others sign in on their first call, so one that is down does not stop the server.
- `FFC_API_KEY`/`FFC_API_SECRET` cannot be combined with several sites.

> **Warning:** one connection that spans several sites lets one mistake by the assistant reach all of them, including other clients' data. Serve only the sites a task needs, make the others `read_only`, and keep confirmations on.

## Debugging

`ffc mcp --debug` traces every request to the site on stderr with secrets redacted (clients usually log stderr; a detached server writes it to `mcp.log`). Stdout is the protocol channel and carries nothing else.

## See also

- [Setup](setup.md)
- [Tools](tools.md)
- [Safety](safety.md)
