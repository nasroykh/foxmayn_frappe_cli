# Foxmayn Frappe Desktop

A desktop app for Windows and macOS that manages your Frappe and ERPNext sites and connects them to the AI apps on your computer, without the command line.

**Not an official Frappe product.** Foxmayn makes this app independently; it is not affiliated with or endorsed by Frappe Technologies.

## What it does

- **Sites.** Add a site with browser sign-in (OAuth), an API key, or a username and password. Check the connection, make a site the default, rename it, change its address, remove it.
- **Connect apps.** Connect Claude Desktop, Claude Code, Cursor, VS Code or Codex to a site, optionally read-only, with a preview of the change first. Disconnect again with one click.
- **ffc helper.** Apps reach your sites through the `ffc` command line. If it is missing, the app downloads the latest ffc release, verifies its signature and checksum, and installs it for you.
- **Update notice.** Tells you when a newer version of the app is available, and updates ffc for you when a newer ffc release is out.

The app and the CLI share one config file, `~/.config/ffc/config.yaml`. Sites you add with `ffc` show up in the app, and the other way round, live.

## Privacy

- No telemetry or analytics.
- The app talks to your own Frappe sites (sign-in, connection checks, token revocation) and to GitHub (the list of releases, for the update notice and the ffc download). Nothing else.
- Credentials are stored in `~/.config/ffc/config.yaml` with mode 0600, the same file the CLI uses. There is no OS keychain. Stored tokens and secrets are not sent back to the app's window; a secret you type travels from the form straight to the app's backend.

## Pages

| Page | What it covers |
| --- | --- |
| [Install](install.md) | Windows installer, macOS disk image, unsigned-build warnings, verifying downloads, uninstalling. |
| [Using the app](using.md) | First run, sites, sign-in methods, the Assistant, connecting apps, the ffc helper, settings, updates. |
| [Troubleshooting](troubleshooting.md) | Common messages and what to do. |

## See also

- [MCP server](../mcp/README.md) (what connected apps get)
- [Security](../security.md)
- [Development: desktop app](../development/desktop.md)
