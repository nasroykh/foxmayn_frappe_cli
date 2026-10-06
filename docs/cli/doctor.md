# doctor

Check the whole setup (config, network, login, server, local state) and get a hint for each problem.

```bash
ffc doctor
ffc doctor --site prod --json
```

Each check reports pass, warn or fail. Any fail makes the command exit 1; warnings do not. Checks that need the site are skipped when it cannot be reached or redirects.

| Check | What it looks at |
| --- | --- |
| `config.file`, `config.dir` | The config file is mode 0600 (fail otherwise: it holds credentials) and its directory 0700 (warn). |
| `config.parse` | The file parses and the site resolves. Values are never printed in parse errors. |
| `config.lock` | No stale lock left by a crashed ffc (warn; the next write breaks it). |
| `net.reachable` | The URL answers like a Frappe site, without redirecting. A redirect fails; the hint names the final URL to configure. |
| `net.tls` | The certificate verifies and does not expire within 14 days. Plain `http://` to a non-local host is a warning. |
| `net.clock` | The local clock agrees with the server's: over 1 minute off warns, over 10 minutes fails. |
| `auth.valid` | The credentials log in; names the user. |
| `auth.oauth_token` | An OAuth token's expiry. An expired token is a warning (the next command refreshes it), or a fail when there is no refresh token. |
| `server.versions` | The installed apps and versions, read live (warns for Frappe older than v15). |
| `server.api_v2` | Whether `/api/v2` exists (warns only on Frappe v16+, which should have it). |
| `mcp.daemon` | The health of the detached MCP server, if one runs. |
| `mcp.state_file` | The MCP state file (it holds a token) is not readable by others. |
| `update.check` | Whether a newer ffc was seen. |

With `--json` the output is an array of `{"check", "status", "message", "hint"}` (`hint` is empty when there is nothing to do). The check names are stable.

`doctor` changes nothing: it does not renew an expired OAuth token, does not touch the version cache (use `ffc whoami --refresh`), and a username/password site signs in and out again. The network probe is one GET of `frappe.ping` through your proxy settings. Secrets are never printed.

## See also

- [Troubleshooting](../troubleshooting.md)
- [Sites and settings](sites-and-settings.md) (`ffc ping`)
- [Security](../security.md)
