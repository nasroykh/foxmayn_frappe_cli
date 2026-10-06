# Documentation

User and developer documentation for **ffc** (Foxmayn Frappe CLI), its MCP server, and **Foxmayn Frappe Desktop**.

> ffc and Foxmayn Frappe Desktop are independent projects. They are not official Frappe products and are not affiliated with or endorsed by Frappe Technologies.

- **ffc** is a command line for Frappe and ERPNext sites over the REST API: documents, reports, workflows, files, permissions, scripting-friendly output.
- **The MCP server** (`ffc mcp`) lets AI assistants such as Claude, Cursor, VS Code and Codex work with your sites, with per-site limits, confirmations and an audit log.
- **Foxmayn Frappe Desktop** is a Windows and macOS app that sets up sites and connects assistants without the command line.

New here? Start with [Installation](getting-started/installation.md), then the [Quickstart](getting-started/quickstart.md).

## Getting started

| Page | What it covers |
| --- | --- |
| [Installation](getting-started/installation.md) | Install scripts, signed releases, manual download, from source, updating, uninstalling. |
| [Quickstart](getting-started/quickstart.md) | Add a site and run your first commands. |
| [Authentication](getting-started/authentication.md) | OAuth (with or without client registration), API keys, username/password, 2FA. |
| [Configuration](getting-started/configuration.md) | `config.yaml`, several sites, environment variables, precedence. |

## CLI

| Page | Commands |
| --- | --- |
| [CLI reference](cli/README.md) | Command index, global flags, shared conventions. |
| [Sites and settings](cli/sites-and-settings.md) | `init`, `site`, `config`, `ping` |
| [Documents](cli/documents.md) | `get-doc`, `list-docs`, `count-docs`, `create-doc`, `update-doc`, `delete-doc` |
| [edit-doc](cli/edit-doc.md) | `edit-doc` |
| [Bulk operations](cli/bulk.md) | `bulk-create`, `bulk-update`, `bulk-delete` |
| [Lifecycle and workflow](cli/lifecycle-and-workflow.md) | `submit-doc`, `cancel-doc`, `amend-doc`, `copy-doc`, `rename-doc`, `restore-doc`, `discard-doc`, `workflow` |
| [Search and aggregate](cli/search-and-aggregate.md) | `search`, `aggregate`, `count-docs --group-by` |
| [Reports and methods](cli/server-calls.md) | `list-reports`, `run-report`, `call-method` |
| [ffc api](cli/api.md) | `api` |
| [Files and PDF](cli/files-and-pdf.md) | `upload`, `attachments`, `download`, `pdf` |
| [Collaboration](cli/collaboration.md) | `comment`, `assign`, `unassign`, `tag`, `untag`, `share`, `unshare` |
| [Schema and cache](cli/schema-and-cache.md) | `list-doctypes`, `get-schema`, `cache` |
| [Identity and permissions](cli/identity-and-permissions.md) | `whoami`, `can`, `doc-info` |
| [doctor](cli/doctor.md) | `doctor` |
| [update](cli/update.md) | `update`, the daily update check |
| [Completion](cli/completion.md) | `completion` |
| [Output formats](cli/output-formats.md) | `--output`, `--json`, `--jq`, CSV, NDJSON |
| [Exit codes](cli/exit-codes.md) | Exit codes and JSON errors |
| [Dry runs and debugging](cli/dry-run-and-debugging.md) | `--dry-run`, `--debug` |

## MCP server

| Page | What it covers |
| --- | --- |
| [Overview](mcp/README.md) | What the server is and how to start safely. |
| [Setup](mcp/setup.md) | `ffc mcp install` / `uninstall` for Claude Desktop, Claude Code, Cursor, VS Code, Codex. |
| [Running the server](mcp/running.md) | stdio, HTTP, detached daemon, several sites. |
| [Tools](mcp/tools.md) | All tools, tool sets, limits, resources, prompts, completion. |
| [Safety](mcp/safety.md) | Read-only mode, per-site policy, confirmations, audit log. |

## Desktop app

| Page | What it covers |
| --- | --- |
| [Overview](desktop/README.md) | What the app does; privacy. |
| [Install](desktop/install.md) | Windows installer, macOS dmg, unsigned beta warnings, uninstalling. |
| [Using the app](desktop/using.md) | Sites, sign-in, assistants, the ffc helper, settings, updates. |
| [Troubleshooting](desktop/troubleshooting.md) | Messages and fixes. |

## Reference

| Page | What it covers |
| --- | --- |
| [Security](security.md) | Release signing, credential storage, network safeguards, MCP safety. |
| [Troubleshooting](troubleshooting.md) | Common problems and fixes. |
| [FAQ](faq.md) | Short answers. |

## Development

| Page | What it covers |
| --- | --- |
| [Development](development/README.md) | Building from source, project layout, conventions, contributing. |
| [Testing](development/testing.md) | Unit tests with the fake Frappe site, contract tests, CI. |
| [Releasing](development/releasing.md) | CLI `v*` and desktop `desktop-v*` releases, signing keys. |
| [Desktop app development](development/desktop.md) | Wails dev loop, mock mode, tests, bindings. |

## See also

- [Repository](https://github.com/nasroykh/foxmayn_frappe_cli)
- [Releases](https://github.com/nasroykh/foxmayn_frappe_cli/releases)
- [Issues](https://github.com/nasroykh/foxmayn_frappe_cli/issues)
