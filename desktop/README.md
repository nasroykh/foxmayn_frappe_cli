# Foxmayn Frappe Desktop

A desktop app that connects Frappe and ERPNext sites to the AI assistants on a computer (Claude Desktop, Claude Code, Cursor, VS Code, Codex), built with Wails v3 around the same Go code as the ffc CLI. Scope and order of work are in the vault notes "ADR-004 Desktop app scope, CLI groundwork first" and "FFC Desktop MVP Plan".

**Windows and macOS only** for now. Linux comes later; the Wails template's Linux build files are kept but not maintained or tested.

Not an official Frappe product.

This directory is its own Go module (`github.com/nasroykh/foxmayn_frappe_cli/desktop`). Because its path sits under the ffc module path, it may import ffc's `internal/` packages (`internal/sitesetup`, `internal/mcpinstall`, `internal/config`, `internal/client`), while Wails, Node and CGO stay out of the CLI's `go.mod` and release build.

## What it does

- **Sites:** list, add (browser sign-in, API key, or username and password, in the CLI wizard's order), check, make default, rename, change address, remove (an OAuth sign-in is revoked first, as `ffc site remove` does). It edits the same `~/.config/ffc/config.yaml` as the CLI, only through `config.Edit`/`config.Overwrite`, and watches it, so CLI changes show up live.
- **Assistant:** a chat screen that answers questions about a site, and with "Ask before changes" proposes edits that wait for the user's approval. It runs ffc's MCP server inside the app (`internal/cmd.NewMCPServer`, through the `replace` directive in `go.mod`), so it needs no installed ffc; the site policy, sensitive DocTypes and audit log apply as for any connected app (audit lines carry client `foxmayn-desktop` and a run id). Providers: Anthropic and OpenRouter (API key), Ollama and LM Studio (local), or a custom OpenAI-compatible address. Keys live only in the OS keychain (`services/keys.go`); the web view sees the last four characters. History is in `assistant.db` under `os.UserConfigDir()/Foxmayn Frappe Desktop/` (SQLite, pure Go). User guide: `docs/desktop/using.md`.
- **Connect apps:** connect, update or disconnect the "frappe" MCP entry of each assistant through `internal/mcpinstall` (the code behind `ffc mcp install` and `ffc mcp uninstall`), with a preview of the change first.
- **ffc helper:** the entries run an installed `ffc`, found on PATH or where the install scripts put it. When it is missing, the app offers (after asking) to download the latest ffc release from GitHub, verify it exactly as `ffc update` does (signed `checksums.txt`, then SHA-256, through `internal/release`; it refuses anything unverified), and install it for the user: `%LOCALAPPDATA%\Programs\ffc\ffc.exe` plus the user PATH on Windows, `~/.local/bin/ffc` on macOS. When the ffc found is updatable (`FFCInfo.Updatable`: it answers `ffc version <release>`, and it is `ffc.exe` on Windows, never a `.cmd` shim, or resolves through symlinks to a file named `ffc` on macOS), the install replaces that binary in place (`AppService.updateTarget`), so an update never leaves the old one first on PATH; anything else gets a fresh copy in the app's folder; a folder the app cannot write gets an error pointing to `ffc update`. An ffc a package manager installed (`release.ManagedBy` on the path found, resolved outside Windows; the Scoop shim counts) sets `FFCInfo.Manager`/`UpgradeCommand` and is never updatable: `InstallFFC` refuses (CodeUnavailable, naming the command), Settings hides "Install again" and shows the command, and the install dialog shows it instead of an Install button. It also shows the install script command, pinned to a release tag, to run by hand. It never bundles its own ffc.
- **Update notice:** at start, at most once a day (`localStorage` key `ffd-update-checked`), the app asks GitHub for the newest `desktop-v<semver>` release (not a draft; prereleases count only while the running version is 0.x or itself a prerelease) through `AppService.CheckForUpdate` and, when it is newer than this build, shows a toast and a dot on Settings. For a release without a `-` suffix the answer carries `InstallCommand` (`desktopInstallCommand`: `install-desktop.sh` or `install-desktop.ps1` from the release's tag), and the toast copies it and Settings > About shows it: a download by curl or PowerShell is not quarantined, so the unsigned app opens without Gatekeeper or SmartScreen screens. Otherwise the toast has a Download button (opens the release page in the browser). Settings > About has a "Check for updates" button. A version that does not parse, such as the `0.0.0-dev` of a build without a release version, never gets an offer. No tokens, no self-update: the user installs the download. The same answer carries `ffc` (`FFCUpdate`): the installed ffc, only when updatable (anything else is left to ffc's own update notice), against the release `ffc update` and the installer take (the first final `v<digit>` entry of the list, `release.IsCLITag`, as `release.Latest`), so the offer always names what gets installed; when newer, a toast with Update (the install dialog), a sidebar entry and Settings > ffc helper "Update to X". An ffc reporting `dev` is never compared. Release builds must set the version with `-ldflags "-X github.com/nasroykh/foxmayn_frappe_cli/desktop/services.AppVersion=<semver>"`.
- On Windows it notices ffc settings inside a running WSL distribution and explains that they are separate.

Stored tokens and secrets never go back to the web view: the Go services return sites without credentials, and a secret typed into a sign-in form only travels from the form to Go.

## Layout

- `main.go`: the window and the services (it calls `cmd.RunJQChildIfRequested()` first).
- `services/`: `AppService` (environment, ffc detection and installer, update check, opening links and folders), `SitesService` (sites and the OAuth sign-in, events `config:changed` and `signin:progress`), `AssistantsService` (MCP entries), `AssistantService` (the Assistant: `assistant.go` bindings, `assistant_loop.go` and `assistant_approval.go` the run loop and approval cards, `assistant_events.go` the `chat:*` events, `engine.go` the in-process ffc MCP servers, `keys.go` the keychain). Errors reach the UI as `services.Error` (`code`, `message`, `detail`, ...).
- `frontend/src/lib/backend.ts`: the typed layer over the generated bindings (`frontend/bindings`, regenerate with `wails3 generate bindings -clean=true -ts -i`, and commit the result: CI fails on a diff).
- `llm/`: the provider interface and message types; `llm/anthropic`, `llm/openaicompat` (OpenRouter, Ollama, LM Studio, custom URL) and `llm/llmtest` (a scripted fake provider for tests).
- `store/`: the SQLite store behind the Assistant (conversations, messages, runs, tool calls, usage, providers; no secrets).
- `frontend/src/screens/assistant/` and `src/components/chat/`: the chat screen, onboarding, provider settings, approval card, Markdown (no raw HTML or images).
- `frontend/src/screens/`: sites, add-site sheet, assistants, settings, onboarding. UI components are shadcn/ui on Base UI (`frontend/src/components/ui`).

## Requirements

- Go (version from `go.mod`), Node and npm
- The Wails CLI matching `go.mod`: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28`

## Develop and build

```bash
cd desktop
wails3 dev      # hot reload
wails3 build    # binary in desktop/bin/
```

Go tests run with `-race` on macOS in CI only (the race detector needs cgo). `go vet ./...` and `go test ./...` need `frontend/dist`, so build the frontend once first (`wails3 build` or `npm run build` in `frontend/`).

### Browser preview (mock mode)

```bash
cd desktop/frontend
npm run dev:mock   # http://127.0.0.1:9245
```

The UI runs in a normal browser against a fake backend (`src/mock/backend.ts`) with sample data. Query parameters pick a scenario, for example `?sites=none` (first run), `?update=available` (update toast and About tab), `?update=fail`, `?ffc=missing`, `?wsl=1`, `?os=darwin`, `?signin=noreg` (a Frappe v15 site), `?slow=1`, `?fail=sites`, `?providers=none|nokey`, `?local=ollama`; the full list is at the top of the mock. In the Assistant, what you type picks a scripted model: anything reads a document and answers; "update"/"change" gives an approval card with a diff; "create"/"add" a card without one; "delete" ffc's own card; "error" a failed run; "many"/"pause" a pause at the step limit. Frontend unit tests: `npm test` (Vitest). Only the Vite `mock` mode resolves it, so production builds never contain it.

## Notes

- `go test ./...` and `go vet ./...` at the repository root skip nested modules; `.github/workflows/desktop.yml` vets, tests and builds this module on Windows and macOS (root `gofmt -l .` does check its Go files).
- Releases: push a `desktop-v<version>` tag (`.github/workflows/desktop-release.yml`; notes in `release-notes/<version>.md`). They are prereleases on 0.x and never GitHub's latest, because the install scripts and ffc up to v1.11.0 read `releases/latest`.
