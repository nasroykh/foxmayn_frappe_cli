# Configuration

Where ffc keeps its settings, how to work with several sites, and which environment variables override what.

## The config file

Default path: `~/.config/ffc/config.yaml` on every OS (on Windows, `%USERPROFILE%\.config\ffc\config.yaml`). Change it with `--config PATH` or `FFC_CONFIG`.

You rarely need to edit it by hand: `ffc init`, `ffc site` and `ffc config` write it for you, keeping comments and key order. ffc writes it atomically with mode 0600 (directory 0700) under a lock file (`config.yaml.lock`), so concurrent ffc processes do not corrupt it.

```yaml
default_site: dev
number_format: french        # french | us | german | plain
date_format: yyyy-mm-dd      # yyyy-mm-dd | dd-mm-yyyy | dd/mm/yyyy | mm/dd/yyyy

sites:
  dev:                       # API key
    url: "http://mysite.localhost:8000"
    api_key: "your_api_key"
    api_secret: "your_api_secret"

  prod:                      # OAuth (written by ffc init --oauth / site add --oauth)
    url: "https://erp.example.com"
    oauth_client_id: "..."
    oauth_client_secret: "..."   # only for a confidential client
    access_token: "..."
    refresh_token: "..."
    token_expiry: 1234567890
    mcp:                         # optional MCP policy for this site
      read_only: true

  local:                     # username and password (stored in clear text)
    url: "http://localhost:8000"
    username: "admin"
    password: "..."
```

| Key | Description |
| --- | --- |
| `default_site` | Site used when no `--site` is given. |
| `number_format`, `date_format` | Display formats for tables. See [display settings](../cli/sites-and-settings.md#display-settings-ffc-config). |
| `sites.<name>.url` | Site URL. |
| `sites.<name>.api_key`, `api_secret` | API key sign-in. |
| `sites.<name>.oauth_client_id`, `oauth_client_secret`, `access_token`, `refresh_token`, `token_expiry` | OAuth sign-in (managed by ffc). |
| `sites.<name>.username`, `password` | Username/password sign-in. |
| `sites.<name>.mcp` | MCP policy for the site. See [MCP safety](../mcp/safety.md#per-site-policy). |

When a site has more than one credential set, ffc uses the OAuth token first, then the API key, then the username and password.

A template with comments is in [`config.example.yaml`](../../config.example.yaml).

## Several sites

```bash
ffc site list
ffc site use prod                 # change the default
ffc --site staging list-docs -d ToDo
FFC_SITE=staging ffc list-docs -d ToDo
```

Site names keep their case and may contain dots (`Prod`, `erp.example.com`). `--site` matches the exact name first, then a unique case-insensitive match.

## Environment variables

| Variable | Effect |
| --- | --- |
| `FFC_SITE` | Like `--site`. |
| `FFC_CONFIG` | Like `--config`. |
| `FFC_OUTPUT` | Like `--output` (default output format). |
| `FFC_TIMEOUT` | Like `--timeout`, e.g. `2m`. |
| `FFC_DEBUG` | Like `--debug`: `basic` (or `1`) or `body`. |
| `FFC_API_KEY` + `FFC_API_SECRET` | Set together, they replace every stored credential of the selected site. One alone is ignored with a warning. |
| `FFC_URL` | Site URL. Applied only together with `FFC_API_KEY` and `FFC_API_SECRET`, so stored credentials are never sent to another host. Set alone to a URL that differs from the site's, it is an error. |
| `FFC_API_SECRET`, `FFC_PASSWORD` | Secrets for non-interactive `init` / `site add`. |
| `FFC_OAUTH_CLIENT_SECRET` | Secret of the OAuth Client given with `--client-id` during setup. Ignored, with a warning, without `--client-id`. |
| `FFC_NO_UPDATE_CHECK` | Any value disables the daily update check. |
| `NO_COLOR`, `CI` | Any value turns off the progress spinner. |
| `VISUAL`, `EDITOR` | Editor for `ffc edit-doc`. |

**No config file at all.** When there is no file at the default path, ffc builds the site from `FFC_URL`, `FFC_API_KEY` and `FFC_API_SECRET` alone. This is the simplest setup for CI:

```bash
export FFC_URL=https://erp.example.com FFC_API_KEY=... FFC_API_SECRET=...
ffc list-docs -d ToDo --output ndjson
```

An explicit `--config` (or `FFC_CONFIG`) pointing at a missing file is an error; it does not fall back to the environment.

## Precedence

Flags win over environment variables, which win over the config file, which wins over built-in defaults. For example `--site` beats `FFC_SITE`, which beats `default_site`.

## Other files ffc keeps

| Path | What |
| --- | --- |
| `~/.config/ffc/config.yaml.lock` | Lock taken while writing the config. |
| `~/.config/ffc/.update_check.json` | When the update check last ran (0600). |
| `~/.config/ffc/mcp.json`, `mcp.log` | State and log of a detached MCP server (the state file holds its bearer token; 0600). |
| `~/.config/ffc/mcp-audit.jsonl` | MCP audit log, next to the config file (0600). |
| `<user cache dir>/ffc/` | Version, DocType, report and schema cache. See [the local cache](../cli/schema-and-cache.md#the-local-cache). |

## See also

- [Authentication](authentication.md)
- [Sites and settings](../cli/sites-and-settings.md)
- [Security](../security.md)
