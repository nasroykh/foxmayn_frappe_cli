---
name: ffc-mcp
description: Run and configure ffc's MCP server so AI clients (Claude Code, Claude Desktop, Cursor, VS Code, Codex) can use a Frappe or ERPNext site - ffc mcp install/uninstall per client, stdio, HTTP and detached modes, read-only servers, tool sets, the per-site mcp policy (allowed DocTypes and methods, sensitive DocTypes, confirmations, audit log), multi-site servers, and how to use the 42 tools well. Use it whenever the user wants an assistant or agent connected to Frappe through MCP, asks why an MCP tool call was refused with "policy:", or wants to limit what an AI may do on their site.
---

# ffc MCP server

`ffc mcp` exposes Frappe operations as MCP tools, using the same config, sites and credentials as the CLI. Set up the site first (ffc-setup skill); an API key suits long-running servers best.

## Connect a client (recommended)

```bash
ffc mcp install --client claude-code --site prod --read-only --print   # preview only
ffc mcp install --client claude-code --site prod --read-only --yes
ffc mcp install --client cursor --name frappe-prod --site prod
ffc mcp uninstall --client cursor --name frappe-prod --yes
```

- Clients: `claude-code`, `claude-desktop`, `cursor`, `vscode`, `codex`. The entry runs this ffc binary by absolute path with `mcp`, plus `--site` (pinned to the exact config name) and `--read-only` when given; a non-default `--config` is added as an absolute path.
- Shows a diff (claude-code: the `claude mcp add-json` command it runs) and asks unless `--yes` (required without a terminal). Backs up the old file as `<file>.ffc-<YYYYMMDD-HHMMSS>.bak`, keeps comments and formatting, refuses a file that does not parse.
- `--name` defaults to `frappe`. Same entry already there: "already up to date", exit 0. Restart or reload the client afterwards.
- Without `--site` the server follows `ffc site use`. Pin `--site` so a changed default never retargets the assistant.
- File locations and the manual JSON: [references/clients.md](references/clients.md).

## Run it yourself

```bash
ffc mcp --site prod                         # stdio (what client configs run)
ffc mcp --site prod --port 8765             # HTTP in the foreground: http://127.0.0.1:8765/mcp
ffc mcp --detach --site prod                # HTTP in the background
ffc mcp status                              # PID, URL, bearer token, site, log path
ffc mcp stop                                # --force if it does not answer health checks
```

HTTP binds 127.0.0.1 only and requires `Authorization: Bearer <token>` (printed to the terminal in the foreground, shown by `ffc mcp status` when detached). Treat the token as a secret.

## Limit what the AI may do

| Flag | Effect |
| --- | --- |
| `--read-only` | only read tools (no create, update, delete, bulk, lifecycle, workflow, `call_method`) |
| `--toolsets LIST` | `core`, `lifecycle`, `collab`, `admin`, `files`, `erp`; default `core,lifecycle` |
| `--allow-tools`, `--allow-doctypes`, `--deny-doctypes`, `--allow-methods`, `--deny-methods` | narrow the site's policy (never widen it) |
| `--confirm always\|if-supported` | ask the user before destructive calls; `always` refuses when the client cannot ask |
| `--sites A,B` / `--all-sites` | serve several sites; every call then needs a `site` argument |
| `-p, --port`, `-d, --detach` | HTTP transport; background |

Tool sets: `core` (documents, reports, search, aggregate, bulk, `call_method`, whoami, check_permission), `lifecycle` (submit, cancel, bulk_submit, bulk_cancel, amend, copy, rename, workflow), `collab` (comments, assignments, tags), `admin` (share, unshare, site_health, list_jobs, list_errors, scheduler_status), `files` (list_attachments, attach_file, get_print_html), `erp` (erp_map, erp_payment, erp_item, erp_stock, erp_party: ERPNext drafts and lookups, all reads). `list_sites` is always there.

The durable way is a `mcp:` block under the site in `config.yaml`, read on every call:

```yaml
sites:
  prod:
    url: https://erp.example.com
    api_key: "..."
    api_secret: "..."
    mcp:
      read_only: false
      allow_doctypes: [Customer, Sales Invoice, ToDo]
      allow_methods: [frappe.client.get_count]
      confirm: always
```

Built-in rules: sensitive DocTypes (User, Role, DocType, System Settings, Server Script, File, DocShare...) are readable but not writable unless `allow_doctypes` lists them; code-running methods are refused unless `allow_methods` lists them. A refused call returns an error starting with `policy:` that names the setting, and sends nothing. Every call is appended to `~/.config/ffc/mcp-audit.jsonl` (secrets redacted). Full rules: [references/policy.md](references/policy.md).

## Using the tools (for the agent on the other side)

- Call `get_schema` before writing, pass `fields` and a `limit` to `list_docs` (it returns only `name` otherwise), page with `start`/`next_start`.
- Big read tools take `jq` to shrink results on the server, e.g. `"[.[] | {name, status}]"`. Results over 512 KiB are refused or cut (`truncated`, `next_start`, `hint`).
- JSON arguments (`filters`, `fields`, `data`, `args`) may be native JSON or a JSON string.
- A declined confirmation returns "cancelled by the user; nothing was changed": do not retry it another way.
- MCP never dry-runs. For a preview, run the CLI command with `--dry-run`.
- Tool list with arguments, resources and prompts: [references/tools.md](references/tools.md).
