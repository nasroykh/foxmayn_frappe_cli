# Troubleshooting

## ffc doctor

`ffc doctor [-s SITE] [--json]` runs these checks (ids are stable in JSON):

| Check | Passes when |
| --- | --- |
| `config.file` / `config.dir` | config file is 0600, its directory 0700 |
| `config.parse` | the file parses and the site resolves |
| `config.lock` | no stale lock from a crashed ffc |
| `net.reachable` | the URL answers like a Frappe site, without redirecting |
| `net.tls` | the certificate verifies (plain http is a warning) |
| `net.clock` | local clock agrees with the server's `Date` header |
| `auth.valid` | the credentials log in (names the user) |
| `auth.oauth_token` | OAuth token expiry (reported, not renewed) |
| `server.versions` | installed apps and versions (read live) |
| `server.api_v2` | whether `/api/v2` exists |
| `mcp.daemon` | the detached MCP server answers |
| `mcp.state_file` | its state file (holds a token) is not readable by others |
| `update.check` | whether a newer ffc was seen |

`fail` exits 1, `warn` does not. Checks that need the site are skipped when it cannot be reached or redirects. doctor changes nothing: no token refresh, no cache read or write, no lock or state cleanup.

## Common errors

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| exit 3, `authentication failed (401)` | wrong or revoked key, expired session, OAuth refresh failed | regenerate the API key and re-add the site, or `ffc site add --oauth --name SAME --url URL --force` |
| `warning: refreshing the OAuth token ... failed` | refresh token revoked or expired | log in again with OAuth as above |
| 2FA error on a password site | Frappe two-factor auth is not supported | switch to OAuth or an API key |
| exit 5, `permission denied (403)` | the user's roles lack the right | `ffc can -d DT --perm read`; ask an administrator |
| exit 4, not found | typo in DocType or name, app not installed | `ffc list-doctypes`, `ffc search TEXT -d DT` |
| `no config file found` | no setup yet | `ffc init`, or set `FFC_URL` + `FFC_API_KEY` + `FFC_API_SECRET` |
| `site "X" not found in config` | wrong `--site` / `FFC_SITE` | `ffc site list` |
| `FFC_URL (...) differs from the site URL` | `FFC_URL` set without the key pair | set all three vars, or unset `FFC_URL` |
| exit 7, timeout / deadline exceeded | slow report or site | `--timeout 2m` |
| exit 2, `confirmation needed: pass --yes` | no terminal for the prompt | add `--yes` once the user approved the action |
| a redirected write fails with the target URL | site URL redirects (http to https, other host) | `ffc site edit NAME --url <final URL>` |
| empty shell completion | no fresh cache | `ffc cache warm` |
| schema missing a new field | 1 h schema cache | `ffc get-schema -d DT --refresh` |

## Tracing

`--debug` (or `FFC_DEBUG=1`) prints every HTTP request on stderr with secrets redacted; `--debug=body` adds headers and bodies. The spinner is off while tracing.
