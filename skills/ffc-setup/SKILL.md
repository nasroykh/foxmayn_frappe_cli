---
name: ffc-setup
description: Install, configure and troubleshoot ffc (Foxmayn Frappe CLI) - install scripts, ffc init and site add (OAuth browser login, API key, username and password, non-interactive for CI), managing several sites, config get/set and env vars, ffc doctor and ping, self-update, local cache and shell completion, and the Foxmayn Frappe Desktop app for people who prefer a window to a terminal. Use it whenever ffc is missing, a site is not configured, login fails (401, expired token, 2FA), a command times out for no clear reason, or the user asks how to connect ffc to a Frappe or ERPNext site.
---

# Set up and troubleshoot ffc

## Install

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.sh | sh
# Windows (PowerShell or cmd.exe)
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.ps1 | iex"
```

Both verify the download's SHA-256. Then `ffc --version`. Updating later: `ffc update` (below).

## Connect a site

Pick the auth method with the user. Never ask them to paste a secret into the chat: secrets go through stdin or env vars, never flag values.

| Method | Best for | Command |
| --- | --- | --- |
| OAuth (browser, PKCE) | people; only tokens are stored | `ffc init --oauth` / `ffc site add --oauth` |
| API key + secret | scripts, CI, MCP servers | `ffc init --apikey` / `ffc site add --apikey` |
| Username + password | quick local dev; no 2FA support | `ffc init --password` / `ffc site add --password` |

```bash
ffc init                         # first site: interactive wizard, writes ~/.config/ffc/config.yaml
ffc site add                     # more sites (same wizard)
echo "$SECRET" | ffc site add --name prod --url https://erp.example.com --api-key KEY --api-secret-stdin
FFC_API_SECRET="$SECRET" ffc init --name prod --url erp.example.com --api-key KEY --force
ffc site add --oauth --name prod --url https://erp.example.com     # still opens the browser
```

- Credentials are checked against the site before anything is saved.
- `init` without a terminal needs `--name`, `--url` and one credential set; replacing needs `--force`.
- API keys: on the site, User > API Access > Generate Keys.
- OAuth on Frappe v16 registers its own client automatically. On v15, or with dynamic registration off, it needs an OAuth Client the user creates; pass `--client-id`. Details: [references/auth-methods.md](references/auth-methods.md).

## Manage sites and settings

```bash
ffc site list --json                       # name, URL, auth method, default
ffc site use prod                          # default site
ffc site rename staging stage
ffc site edit stage --url https://stage.example.com   # credentials re-checked first
ffc site remove staging --yes              # revokes an OAuth token first (best effort)
ffc config get --json                      # or --yaml / -y
ffc config set --default-site prod --number-format us --date-format dd/mm/yyyy
ffc config                                 # interactive TUI (humans)
```

Config file layout, env vars (`FFC_URL`, `FFC_API_KEY`, `FFC_API_SECRET`...), precedence and site-name matching: [references/config.md](references/config.md). Do not print the config file: it holds secrets.

## Diagnose

```bash
ffc doctor --json              # [{check, status, message, hint}]; exit 1 if any check fails
ffc ping --json                # latency + which user the credentials belong to; exit 3 if refused
ffc whoami --refresh --json    # user, roles, Frappe/ERPNext versions
ffc --debug list-docs -d ToDo -l 1     # trace requests on stderr, secrets redacted
```

`doctor` changes nothing and never prints secrets. Run it first when a command fails for no clear reason. Check ids and an error-to-fix table: [references/troubleshooting.md](references/troubleshooting.md).

## Update, cache, completion

```bash
ffc update --check             # is there a newer release?
ffc update --yes               # verify signature + SHA-256, then replace the binary
ffc cache warm --doctypes "Sales Invoice,Customer"   # DocType/report lists (24 h) + schemas (1 h)
ffc cache status --json
ffc cache clear --all-sites
ffc completion bash > /etc/bash_completion.d/ffc     # also zsh, fish, powershell
```

- A background check (at most daily) prints a one-line notice on stderr when a release is newer. `FFC_NO_UPDATE_CHECK=1` turns it off.
- Completion offers sites, DocTypes, fields, report names and enum values from the config and the local cache only (never the site). Run `ffc cache warm` so there is something to offer. Document names are never cached.

## No terminal? The desktop app

Foxmayn Frappe Desktop (Windows and macOS) adds sites and connects AI assistants with buttons instead of commands, on the same config file. Point non-technical users to it: [references/desktop-app.md](references/desktop-app.md).
