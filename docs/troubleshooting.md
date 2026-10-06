# Troubleshooting

Fixes for common ffc problems. Start with `ffc doctor`: it checks the config, network, login, server and local state, and gives a hint for each failure.

```bash
ffc doctor
ffc get-doc -d ToDo -n TD-0001 --debug      # see every HTTP request, secrets redacted
```

## Setup and config

| Symptom | Cause and fix |
| --- | --- |
| `no config file found and FFC_URL is not set` | Run `ffc init`, or set `FFC_URL`, `FFC_API_KEY` and `FFC_API_SECRET`. |
| `site "x" not found in config` | Check `ffc site list`. Names match exactly first, then case-insensitively when unique. |
| `no site selected` | Set a default with `ffc site use NAME`, or pass `--site`. |
| `FFC_URL (...) differs from the site URL` | `FFC_URL` only applies together with `FFC_API_KEY` and `FFC_API_SECRET`. Set all three, or unset `FFC_URL`. |
| `FFC_API_KEY and FFC_API_SECRET must be set together` | Set both, or neither. |
| `config.file` fails in `ffc doctor` | The config is readable by others: `chmod 600 ~/.config/ffc/config.yaml`. |
| A stale `config.yaml.lock` | Left by a crashed ffc; the next write breaks it. `ffc doctor` reports it. |

## Signing in

| Symptom | Cause and fix |
| --- | --- |
| Exit 3 on `ping` or any command | The site rejects the credentials. Regenerate the API key, or re-run `ffc site add --oauth --force` for an OAuth site. |
| `two-factor authentication is enabled for this account` | Password sign-in does not support 2FA. Use OAuth or an API key. |
| Logged out of the browser after running ffc | The site has **Deny Multiple Sessions** on and you use password sign-in. Switch to OAuth or an API key. |
| OAuth: "no registration" or asked for a client ID | Frappe v15 or dynamic client registration is off. Create an OAuth Client (see [Authentication](getting-started/authentication.md#frappe-v15-or-registration-turned-off)) or use an API key. |
| OAuth: 429 during setup | Too many client registrations from your IP (5 per 10 minutes on newer Frappe). Wait and retry, or pass `--client-id`. |
| OAuth: redirect URI mismatch | The OAuth Client's redirect URI must be the one ffc prints (`http://127.0.0.1:<port>/callback`), not `localhost`. |
| OAuth: refresh failed | The refresh token was revoked or the user disabled. Run `ffc site add --oauth --force` with the same name. |
| `site "x" uses OAuth` on `site edit` | An OAuth site cannot move to another URL. Run `ffc site add --oauth --force` with the new URL. |

## Network

| Symptom | Cause and fix |
| --- | --- |
| Exit 7, timeout | Raise `--timeout` (default 30s), e.g. `--timeout 2m` for heavy reports. |
| `site redirected` on a write, or `net.reachable` fails | The configured URL redirects (often `http://` to `https://`, or to `www.`). Set the final URL: `ffc site edit NAME --url https://...` (`ffc doctor` names it). |
| Warning: `uses plain HTTP` | Credentials travel unencrypted. Use `https://` if the site supports it. |
| Certificate errors | `ffc doctor` (`net.tls`) shows the problem. A self-signed certificate must be trusted by your OS. |
| `net.clock` warns or fails | Your computer's clock is off; OAuth tokens and TLS depend on it. Sync the clock. |

## Commands

| Symptom | Cause and fix |
| --- | --- |
| Exit 2 with "pass --yes" | No terminal, so ffc cannot ask. Add `--yes` after checking with `--dry-run`. |
| Exit 4 on a DocType you know exists | Check spelling and case (`Sales Invoice`, not `sales invoice`), and that its app is installed (`ffc whoami`). |
| Exit 6 `TimestampMismatchError` | Someone saved the document after you read it. Read it again and retry. |
| `list-docs` returns only `name` | Frappe's default. Pass `--fields`. |
| `SQL functions are not allowed as strings` | Frappe v16 refuses `count(...)` in `--fields`. Use `ffc aggregate`. |
| `get-schema` shows an old schema | It is cached for an hour. Use `--refresh`. |
| Tab completion offers nothing | Fill the cache: `ffc cache warm`. |
| `ffc search` without `-d` finds nothing | Global search covers only DocTypes in Global Search Settings. Use `-d DocType` or `list-docs --filters`. |
| `pdf` fails with `OSError` (500) | The site's PDF renderer cannot reach the site's own URL (common in Docker). Fix the site's setup. |
| `edit-doc` refuses to run | It needs a terminal. In scripts, use `update-doc`. |

## MCP

| Symptom | Cause and fix |
| --- | --- |
| The assistant does not see ffc | Restart the client fully after `ffc mcp install`. Check the entry with `ffc mcp install --client X --print`. |
| `claude` not found / Windows script | Install Claude Code, or run the printed `claude mcp add-json` command yourself in PowerShell 7. |
| A tool error starting with `policy:` | The site's `mcp` policy or a server flag refused it. The message names the setting. See [MCP safety](mcp/safety.md). |
| `cancelled by the user; nothing was changed` | The user declined the confirmation. |
| `MCP server already running` | Stop it with `ffc mcp stop` (`--force` if it does not answer). |
| Result "too large" | Narrow with `limit`, `fields`, `filters`, `keys` or `jq`. |

## Updating

| Symptom | Cause and fix |
| --- | --- |
| `no write permission to ...` | ffc lives in a system directory: `sudo ffc update`. |
| `install.sh`: "OpenSSL 3 not found" | Informational: the signature was not checked, only the checksum. Install OpenSSL 3 to check it, or rely on `ffc update`, which always checks. |

Still stuck? Run the failing command with `--debug` and open an [issue](https://github.com/nasroykh/foxmayn_frappe_cli/issues) with the output (it contains no secrets, but check it for data you do not want to share).

## See also

- [doctor](cli/doctor.md)
- [Exit codes](cli/exit-codes.md)
- [Desktop troubleshooting](desktop/troubleshooting.md)
- [FAQ](faq.md)
