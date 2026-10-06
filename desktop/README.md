# Foxmayn Frappe Desktop

A desktop app that connects Frappe and ERPNext sites to the AI assistants on a computer (Claude Desktop, Claude Code, Cursor, VS Code, Codex), built with Wails v3 around the same Go code as the ffc CLI. Scope and order of work are in the vault notes "ADR-004 Desktop app scope, CLI groundwork first" and "FFC Desktop MVP Plan".

**Windows and macOS only** for now. Linux comes later; the Wails template's Linux build files are kept but not maintained or tested.

Not an official Frappe product.

This directory is its own Go module (`github.com/nasroykh/foxmayn_frappe_cli/desktop`). Because its path sits under the ffc module path, it may import ffc's `internal/` packages (`internal/sitesetup`, `internal/mcpinstall`, `internal/config`, `internal/client`), while Wails, Node and CGO stay out of the CLI's `go.mod` and release build.

## What it does

- **Sites:** list, add (browser sign-in, API key, or username and password, in the CLI wizard's order), check, make default, rename, change address, remove (an OAuth sign-in is revoked first, as `ffc site remove` does). It edits the same `~/.config/ffc/config.yaml` as the CLI, only through `config.Edit`/`config.Overwrite`, and watches it, so CLI changes show up live.
- **Assistants:** connect, update or disconnect the "frappe" MCP entry of each assistant through `internal/mcpinstall` (the code behind `ffc mcp install` and `ffc mcp uninstall`), with a preview of the change first.
- **ffc helper:** the entries run an installed `ffc`, found on PATH or where the official installers put it. When it is missing, the app offers to run the official signed installer (after asking) and shows the command to run by hand. It never bundles its own ffc.
- On Windows it notices ffc settings inside a running WSL distribution and explains that they are separate.

Stored tokens and secrets never go back to the web view: the Go services return sites without credentials, and a secret typed into a sign-in form only travels from the form to Go.

## Layout

- `main.go`: the window and the three services.
- `services/`: `AppService` (environment, ffc detection and installer, opening links and folders), `SitesService` (sites and the OAuth sign-in, events `config:changed` and `signin:progress`), `AssistantsService` (MCP entries). Errors reach the UI as `services.Error` (`code`, `message`, `detail`, ...).
- `frontend/src/lib/backend.ts`: the typed layer over the generated bindings (`frontend/bindings`, regenerate with `wails3 generate bindings -clean=true -ts -i`).
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

`go vet ./...` and `go test ./...` need `frontend/dist`, so build the frontend once first (`wails3 build` or `npm run build` in `frontend/`).

### Browser preview (mock mode)

```bash
cd desktop/frontend
npm run dev:mock   # http://127.0.0.1:9245
```

The UI runs in a normal browser against a fake backend (`src/mock/backend.ts`) with sample data. Query parameters pick a scenario, for example `?sites=none` (first run), `?ffc=missing`, `?wsl=1`, `?os=darwin`, `?signin=noreg` (a Frappe v15 site), `?slow=1`, `?fail=sites`; the full list is at the top of the mock. Only the Vite `mock` mode resolves it, so production builds never contain it.

## Notes

- The root CI does not build this module yet: `go test ./...` and `go vet ./...` at the repository root skip nested modules (root `gofmt -l .` does check its Go files).
- Before the first desktop release: `ffc update` reads GitHub `releases/latest`, so publish desktop releases with `make_latest: false` or make `update.go` accept only `v*` tags.
