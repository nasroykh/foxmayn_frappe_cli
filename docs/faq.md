# FAQ

Short answers to common questions about ffc, its MCP server and the desktop app.

**Is this an official Frappe tool?**
No. ffc and Foxmayn Frappe Desktop are independent projects by Foxmayn, not affiliated with or endorsed by Frappe Technologies.

**Which Frappe versions work?**
ffc is tested against Frappe/ERPNext v15 and v16 (nightly contract tests against real sites). Some features need v16 or newer releases: `discard-doc`, OAuth without setup (dynamic client registration), and `doc-info --timeline` (releases from August 2026). `ffc doctor` warns for Frappe older than v15.

**Does it work with Frappe Cloud or self-hosted sites?**
Yes, any site reachable over HTTP(S) with the REST API. ffc talks only to the URL you configure.

**Which sign-in method should I use?**
OAuth for yourself, an API key for scripts and CI, a password only for local testing. See [Authentication](getting-started/authentication.md).

**Where are my credentials stored? Is there a keychain?**
In `~/.config/ffc/config.yaml`, mode 0600. There is no keychain integration. See [Security](security.md#where-credentials-live).

**Can I use ffc in CI without a config file?**
Yes: set `FFC_URL`, `FFC_API_KEY` and `FFC_API_SECRET`. See [Configuration](getting-started/configuration.md#environment-variables).

**How do I get machine-readable output?**
`--json`, `--output ndjson|csv|tsv|yaml`, or `--jq`. Set `FFC_OUTPUT=json` for a default. See [Output formats](cli/output-formats.md).

**Why does `-o` not set the output format?**
`-o` is `--order-by` on `list-docs` and `--output-file` on `download` and `pdf`. The format flag is `--output`.

**How do I call an endpoint ffc has no command for?**
`ffc call-method` for whitelisted methods, `ffc api` for any path. See [ffc api](cli/api.md).

**Can I preview a write?**
Yes, `--dry-run` on every command that writes. See [Dry runs and debugging](cli/dry-run-and-debugging.md).

**Can an AI assistant break my data?**
It can do what your Frappe user can do, unless you limit it. Use `--read-only`, a dedicated user with few roles, per-site policies, and keep confirmations on. See [MCP safety](mcp/safety.md).

**Which AI clients are supported?**
`ffc mcp install` supports Claude Desktop, Claude Code, Cursor, VS Code and Codex. Any MCP client that starts stdio servers can run `ffc mcp`. ChatGPT is not supported: it connects only to remote servers.

**Do I need the desktop app?**
No. The app is a graphical front end for site setup and assistant connections; everything it does is available in the CLI.

**Does the desktop app run on Linux?**
Not yet: Windows and macOS only. The CLI runs on Linux.

**Does ffc send telemetry?**
No. The CLI contacts your sites and, once a day, GitHub to check for updates (`FFC_NO_UPDATE_CHECK` turns that off). The desktop app contacts your sites and GitHub.

**How do I update?**
`ffc update` for the CLI. For the desktop app, download the new installer when the app shows the update notice.

**Does ffc cache my documents?**
No. It caches only app versions, DocType and report names, and schemas. See [the local cache](cli/schema-and-cache.md#the-local-cache).

## See also

- [Troubleshooting](troubleshooting.md)
- [Documentation index](README.md)
