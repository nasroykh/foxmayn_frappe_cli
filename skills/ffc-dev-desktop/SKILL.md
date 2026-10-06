---
name: ffc-dev-desktop
description: Change Foxmayn Frappe Desktop, the Wails v3 + React 19 + shadcn/ui (Base UI) app in desktop/ of the foxmayn_frappe_cli repository - the nested Go module and its services (AppService, SitesService, AssistantsService), events, the typed frontend backend layer and generated bindings, the browser mock mode, CI and desktop-v* releases. Use it whenever you touch anything under desktop/, the desktop workflows, or internal/ packages the app imports (sitesetup, mcpinstall, config, release, sitecache). Load ffc-dev first.
---

# Foxmayn Frappe Desktop (desktop/)

A desktop app that manages ffc sites and connects AI assistants to them through the same Go code as the CLI. Windows and macOS only. desktop/README.md is the source of truth for features; read it first.

## Module boundary

- `desktop/` is its own Go module, `github.com/nasroykh/foxmayn_frappe_cli/desktop`. Its path under the ffc module path is what lets it import `internal/` packages. Never rename it to an unrelated path.
- Wails, Node and CGO stay in `desktop/go.mod`, never in the root `go.mod` (the CLI release is CGO-free).
- Root `go test ./...` and `go vet ./...` skip it; root `gofmt -l .` does check its Go files. desktop.yml runs its own vet and tests.
- The root `.gitignore` entry `/foxmayn_frappe_cli` is anchored on purpose: unanchored it ignored the bindings directory `frontend/bindings/github.com/nasroykh/foxmayn_frappe_cli/`.
- The app reuses prompt-free, cobra-free packages: `internal/sitesetup` (setup, OAuth, revoke), `internal/mcpinstall` (assistant entries), `internal/config` (only `config.Edit`/`config.Overwrite`), `internal/release` (installing ffc), `internal/sitecache`. Keep those packages free of prompts and cobra; a change there affects both programs.

## Services (desktop/services)

| Service | Owns |
| --- | --- |
| `AppService` | environment, ffc detection and installer (install.go, through `internal/release`, never by running install.sh/install.ps1), update check, opening links and folders |
| `SitesService` | sites, OAuth sign-in, config watch |
| `AssistantsService` | MCP entries through `internal/mcpinstall` with a preview first |

- Errors reach the UI as `services.Error` (`code`, `message`, `detail`, ...).
- Events: `config:changed`, `signin:progress`, `installer:log` (constants in host.go).
- Stored tokens and secrets never go back to the web view: services return sites without credentials; a typed secret travels only from the form to Go.
- The services' `TestMain` points `sitecache.UserCacheDir` at a temp dir; keep tests off the real config and cache.

## Frontend (desktop/frontend)

- `src/lib/backend.ts` is the typed layer over the generated bindings in `frontend/bindings`. Screens call backend.ts, never the bindings directly. After changing a service signature, regenerate: `wails3 generate bindings -clean=true -ts -i`, and commit the result. CI fails when the bindings or `package-lock.json` differ after a build.
- UI is shadcn/ui on Base UI: style `base-nova`, Tabler icons, components in `src/components/ui`. Base UI composes with the `render` prop (`<TooltipTrigger render={<SidebarTrigger />} />`), not Radix's `asChild`.
- Mock mode: `npm run dev:mock` in `desktop/frontend` serves the UI at http://127.0.0.1:9245 against `src/mock/backend.ts`. Query parameters pick a scenario (`?sites=none`, `?update=available`, `?update=fail`, `?ffc=missing`, `?wsl=1`, `?os=darwin`, `?signin=noreg`, `?slow=1`, `?fail=sites`; full list at the top of the mock). Only the Vite `mock` mode aliases it, so production builds never contain it. Add a scenario when you add a state the UI must show.
- The update check runs at most once a day (`localStorage` key `ffd-update-checked`).

## Build and verify

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28   # match desktop/go.mod
cd desktop/frontend && npm ci && npm run build   # go vet/test need frontend/dist
cd .. && go vet ./... && go test ./...
wails3 dev     # hot reload
wails3 build   # binary in desktop/bin/
```

desktop.yml runs on windows-latest and macos-latest when desktop/, internal/ or go.mod change: npm ci, tsc + vite build, `go mod tidy -diff`, vet, tests, `wails3 build`, bindings and lockfile unchanged, govulncheck (macOS).

## Releases

- Tag `desktop-v<semver>` (never `v*`, which is the CLI release) after writing `desktop/release-notes/<version>.md`. `.github/workflows/desktop-release.yml` builds a per-user NSIS installer (Windows amd64) and a universal macOS dmg, signs `checksums.txt` with relsign, attests provenance, and publishes with `--latest=false` (prerelease for 0.x or a `-` suffix). A PR touching the workflow or `desktop/build/**`, and a manual run, are dry runs.
- Never make a desktop release GitHub's "latest": install.sh, install.ps1 and ffc up to v1.11.0 read `releases/latest`. `release.Latest` skips non-`v<digit>` tags for newer ffc.
- The version comes from ldflags `-X github.com/nasroykh/foxmayn_frappe_cli/desktop/services.AppVersion=<semver>` (`APP_VERSION` in the Windows/darwin Taskfiles); a local build is `0.0.0-dev` and never gets an update offer.
- Builds are unsigned until the Apple and Windows signing secrets exist; those signing steps have never run (UNVERIFIED in the workflow).
