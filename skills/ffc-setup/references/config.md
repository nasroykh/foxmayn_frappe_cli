# Config file, env vars and precedence

## File

Default `~/.config/ffc/config.yaml` (directory 0700, file 0600; `-c/--config` or `FFC_CONFIG` for another path). ffc edits it in place, keeping comments and key order. Edit it through ffc commands, not by hand, while ffc may be running (writes take a lock file `config.yaml.lock`).

```yaml
default_site: dev
number_format: french     # french | us | german | plain   (tables only)
date_format: yyyy-mm-dd   # yyyy-mm-dd | dd-mm-yyyy | dd/mm/yyyy | mm/dd/yyyy
sites:
  dev:
    url: "http://mysite.localhost:8000"
    api_key: "..."
    api_secret: "..."
  prod:                    # OAuth: written by ffc
    url: "https://erp.example.com"
    oauth_client_id: "..."
    access_token: "..."
    refresh_token: "..."
    token_expiry: 1767225600
  local:                   # username + password (clear text, 0600)
    url: "http://localhost:8000"
    username: "admin"
    password: "..."
    mcp:                   # optional MCP policy, see the ffc-mcp skill
      read_only: true
```

Number formats: french `1 000 000,00`, us `1,000,000.00`, german `1.000.000,00`, plain `1000000.00`. They affect table output only.

## Commands

- `ffc config get` (table), `--json`, `-y/--yaml`.
- `ffc config set --default-site NAME --number-format FMT --date-format FMT` (any subset; values are validated).
- `ffc config` opens a TUI for humans.

## Env vars

| Variable | Effect |
| --- | --- |
| `FFC_SITE`, `FFC_CONFIG`, `FFC_TIMEOUT`, `FFC_OUTPUT`, `FFC_DEBUG` | same as `--site`, `--config`, `--timeout`, `--output`, `--debug`; a flag wins |
| `FFC_API_KEY` + `FFC_API_SECRET` | only as a pair; replace every stored credential of the selected site for this run |
| `FFC_URL` | only with that pair (stored credentials never go to another host). Alone and different from the site's URL: error |
| (no config file) | `FFC_URL` + `FFC_API_KEY` + `FFC_API_SECRET` alone define the site (CI) |
| `FFC_API_SECRET`, `FFC_PASSWORD` | also the secret sources for `init`/`site add --api-key` / `--username` |
| `FFC_OAUTH_CLIENT_SECRET` | secret of the OAuth Client given with `--client-id` (setup only; ignored with a warning otherwise) |
| `FFC_NO_UPDATE_CHECK=1` | disable the daily update check |
| `NO_COLOR`, `CI` | turn the spinner off |

CI example without a config file:

```bash
export FFC_URL=https://erp.example.com FFC_API_KEY=... FFC_API_SECRET=...
ffc list-docs -d ToDo --json
```

## Precedence

Flags > env vars > config file > defaults. `--site`/`default_site` match the site name exactly first, then a unique case-insensitive match.

## Local state (for reference)

- `~/.config/ffc/.update_check.json`: last update check.
- `~/.config/ffc/mcp.json`, `mcp.log`, `mcp-audit.jsonl`: MCP server state, log and audit trail (ffc-mcp skill).
- User cache directory (`~/.cache/ffc/...` on Linux): server versions, DocType/report lists and schemas per site and login. `ffc cache clear` removes it.
