# Foxmayn Frappe Desktop (for users)

A desktop app (Windows and macOS only) that connects Frappe and ERPNext sites to the AI assistants on a computer: Claude Desktop, Claude Code, Cursor, VS Code and Codex. It is built on the same Go code as ffc and edits the same `~/.config/ffc/config.yaml`.

## What it does

- **Sites:** list, add (browser sign-in, API key, or username and password), check, make default, rename, change the address, remove (an OAuth sign-in is revoked first). Changes made with the CLI show up live.
- **Assistants:** connect, update or disconnect the `frappe` MCP entry of each assistant, with a preview of the change first. It does what `ffc mcp install` / `ffc mcp uninstall` do.
- **ffc helper:** the assistant entries run an installed `ffc`. When ffc is missing, the app offers to download the latest release, verify it as `ffc update` does (signed checksums, then SHA-256), and install it for the user. It also shows the install command to run by hand.
- **Update notice:** at most once a day it checks for a newer desktop release and offers a download link. It does not update itself.
- On Windows it notices ffc settings inside a running WSL distribution and explains that they are separate.

Stored secrets never reach the app's web view.

## When to point a user to it

- They want an AI assistant to use their Frappe data but do not use a terminal.
- They need to sign in to a site or connect an assistant and the CLI steps feel too technical.
- They are on Linux: not supported yet; use the CLI (ffc-setup, ffc-mcp skills).

## Download

Desktop releases are published on the project's GitHub releases page (github.com/nasroykh/foxmayn_frappe_cli/releases) under tags `desktop-v<version>`, as prereleases for 0.x, never marked "Latest" (the "Latest" release is always the CLI). Files: `foxmayn-frappe-desktop-<version>-windows-amd64-setup.exe` (per-user installer, no administrator rights) and `foxmayn-frappe-desktop-<version>-macos-universal.dmg`, plus `checksums.txt` and its signature `checksums.txt.sig`.

Builds are unsigned for now: Windows SmartScreen needs "More info" then "Run anyway"; macOS needs "Open Anyway" in System Settings > Privacy & Security after the first blocked launch. The release notes of each version give the exact steps.

UNVERIFIED: no desktop release had been published when this was written (2026-10-06); the workflow exists, so check the releases page before sending a link.
