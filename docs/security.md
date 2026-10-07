# Security

How ffc and the desktop app protect your credentials and your sites: release signing, credential storage, network safeguards and MCP limits.

## Release signing

Every CLI release publishes `checksums.txt` (SHA-256 of each archive), `checksums.txt.sig` (an Ed25519 signature of `checksums.txt`) and a GitHub build-provenance attestation.

| Installer | Checksum | Signature |
| --- | --- | --- |
| `ffc update` (1.6.1 and later) | Yes | Yes, against keys built into ffc. Refuses anything unsigned or mismatched. |
| Desktop app's ffc install | Yes | Yes, same code as `ffc update`. |
| `install.sh` | Yes | Yes when OpenSSL 3 is present; otherwise a warning and checksum only. |
| `install.ps1` | Yes | No. |
| `install-desktop.sh` (desktop app, macOS) | Yes | Yes when OpenSSL 3 is present (macOS ships LibreSSL, so usually Homebrew's `openssl@3`); otherwise a note and checksum only. It also refuses an app whose code signature does not verify. |
| `install-desktop.ps1` (desktop app, Windows) | Yes | No. |

Desktop releases sign `checksums.txt` with the same keys. The desktop app itself is not yet signed by Apple or with a Windows certificate; the install scripts download with `curl` or PowerShell, which do not mark the file as downloaded from the internet, so macOS Gatekeeper and Windows SmartScreen do not stop it. The checks above replace theirs.

The signature is made over the text `ffc release checksums v1` followed by the contents of `checksums.txt`. Someone who can replace release files cannot also forge the signature. Two keys are trusted: the CI signing key and an offline backup key, so a leaked CI key can be replaced without breaking updates.

Verify a manual download:

```bash
gh attestation verify ffc_1.11.0_linux_amd64.tar.gz --repo nasroykh/foxmayn_frappe_cli
# or, from a checkout of the repository:
go run ./tools/relsign verify checksums.txt checksums.txt.sig
sha256sum -c --ignore-missing checksums.txt
```

Desktop releases (`desktop-v*` tags) publish the same `checksums.txt` and `checksums.txt.sig`. Beta desktop builds are not code-signed by Apple or Microsoft; see [Desktop install](desktop/install.md).

## Where credentials live

- All credentials are in `~/.config/ffc/config.yaml`: API secrets, OAuth tokens and, for username/password sites, the password in clear text. The file is written with mode 0600 and its directory 0700. ffc does not use the OS keychain. `ffc doctor` fails when the file is readable by others.
- On Windows, file modes are not enforced the same way; the file is protected by your user profile's permissions.
- Prefer OAuth or a scoped API key over a password. A password grants everything your account can do and cannot be revoked separately.
- Secrets are never accepted as flag values (they would land in shell history); use stdin or environment variables. See [Authentication](getting-started/authentication.md#secrets-are-never-flag-values).
- `ffc site remove` revokes an OAuth site's current token on the server.
- The local cache holds no secrets: only app versions, DocType and report names, and schemas, per site and login.
- The detached MCP server's bearer token is stored in `~/.config/ffc/mcp.json` (0600) and printed only to a terminal or by `ffc mcp status`.
- The desktop app uses the same file and does not send stored secrets back to its window.

## Network safeguards

- **Plain HTTP warning.** A site URL using `http://` to anything but `localhost`, `127.0.0.1`, `::1` or `*.localhost` prints a warning: credentials travel unencrypted. Use `https://`.
- **Credentials stay on the site's host.** `ffc api` and `ffc download` accept only paths on the site (or the site's own URL); requests carrying credentials are never redirected to another host or from https to http. Write requests never follow redirects.
- **Protected headers.** `ffc api -H` cannot set `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name` or `X-Forwarded-Host`.
- **No retries of writes.** Only reads are retried (on 429, 502, 503, 504 and connection errors).
- **Redacted traces.** `--debug` and `--dry-run` output never shows API keys, tokens, passwords, cookies or OAuth codes.
- **Sanitised output.** Terminal control characters, bidi overrides and zero-width characters in site data are stripped before printing, so a crafted field value cannot rewrite your terminal.
- **OAuth.** PKCE, a callback server bound to `127.0.0.1`, and a single-use callback.

## MCP safety

An AI assistant connected through `ffc mcp` acts with your Frappe user's permissions. ffc adds:

- `--read-only` and per-site `mcp` policies (allowed tools, DocTypes, methods);
- built-in write protection for sensitive DocTypes (users, permissions, credentials, scripts, files) and denied methods (console code execution, key generation, app installs);
- confirmations before deletes, cancels, merges and shares;
- an audit log of every call, with secrets redacted;
- a loopback-only HTTP server with a bearer token.

Details and limits: [MCP safety](mcp/safety.md). A custom server method can bypass DocType rules; for a hard limit use read-only mode or `allow_doctypes` with `allow_methods`.

## Reporting a vulnerability

Open a [GitHub security advisory](https://github.com/nasroykh/foxmayn_frappe_cli/security/advisories/new) or contact the maintainer privately rather than filing a public issue. (Unverified: the repository has no `SECURITY.md` describing a process.)

## See also

- [MCP safety](mcp/safety.md)
- [Installation](getting-started/installation.md)
- [Configuration](getting-started/configuration.md)
