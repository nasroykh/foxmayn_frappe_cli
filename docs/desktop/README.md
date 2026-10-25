# Foxmayn Frappe Desktop

A desktop app for Windows and macOS that manages your Frappe and ERPNext sites and connects them to the AI apps on your computer, without the command line.

**Not an official Frappe product.** Foxmayn makes this app independently; it is not affiliated with or endorsed by Frappe Technologies.

## What it does

- **Sites.** Add a site with browser sign-in (OAuth), an API key, or a username and password. Check the connection, make a site the default, rename it, change its address, remove it.
- **Assistant.** Ask a site questions in your own words and get answers from its data. It uses your own Anthropic, OpenAI, Gemini or OpenRouter key, or a model on this computer (Ollama, LM Studio). A conversation is read only until you turn on "Ask before changes"; then every change waits on a card that shows what will change, and runs only when you approve it. Profiles, chat history, cost per conversation, and attached files (CSV, Excel, PDF, Word, images).
- **Safety rules.** Per site: read only, ask before risky changes, or your own lists of allowed and refused document types, tools and server methods. They apply to the Assistant and to the connected apps alike.
- **Activity.** Every call the Assistant and the connected apps made through ffc, from ffc's audit log, with filters.
- **Connect apps.** Connect Claude Desktop, Claude Code, Cursor, VS Code or Codex to a site, optionally read-only, with a preview of the change first. Disconnect again with one click.
- **ffc helper.** Apps reach your sites through the `ffc` command line. If it is missing, the app downloads the latest ffc release, verifies its signature and checksum, and installs it for you.
- **English, French and Arabic.** The whole app, right to left in Arabic, following your system's language until you choose one. Keyboard shortcuts, and the chat works with the keyboard alone.
- **Updates.** Tells you when a newer version of the app is available and installs it for you (it checks the release signature and checksum, installs and restarts the app), and updates ffc when a newer ffc release is out.

The app and the CLI share one config file, `~/.config/ffc/config.yaml`. Sites you add with `ffc` show up in the app, and the other way round, live.

## Privacy

- No telemetry or analytics.
- The app talks to your own Frappe sites (sign-in, connection checks, token revocation, the Assistant's tools), to GitHub (the list of releases, for the update notice and the ffc download) and, when you use the Assistant, to the model provider you chose. Nothing else. The provider sees your messages and the site data the Assistant's tools return; with a model on this computer, that data stays here.
- Site credentials are stored in `~/.config/ffc/config.yaml` with mode 0600, the same file the CLI uses. Model provider keys are stored in the operating system's keychain (Windows Credential Manager, macOS Keychain). Stored tokens, secrets and keys are not sent back to the app's window; a secret you type travels from the form straight to the app's backend.

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
