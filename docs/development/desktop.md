# Desktop app development

Build, run and test Foxmayn Frappe Desktop (Wails v3, React 19, shadcn/ui on Base UI) from `desktop/`.

## Requirements

- Go (version from `desktop/go.mod`).
- Node.js and npm (CI uses Node 24).
- The Wails CLI matching `desktop/go.mod`:

  ```bash
  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28
  ```

- Windows or macOS. Linux build files from the Wails template exist but are not maintained or tested.

## Run and build

```bash
cd desktop
wails3 dev      # hot reload (Vite on port 9245)
wails3 build    # binary in desktop/bin/
```

A local build reports version `0.0.0-dev` and never shows the update notice. Release builds set the version with `-ldflags "-X github.com/nasroykh/foxmayn_frappe_cli/desktop/services.AppVersion=<semver>"` (the release workflow does this).

The app uses your real `~/.config/ffc/config.yaml`. Use a test user account if you do not want development runs to change your sites.

## Browser preview without a backend

```bash
cd desktop/frontend
npm install
npm run dev:mock     # http://127.0.0.1:9245
```

The UI runs in a normal browser against a fake backend (`src/mock/backend.ts`) with sample data. Query parameters pick a scenario, for example `?sites=none` (first run), `?update=available`, `?update=fail`, `?ffc=missing`, `?wsl=1`, `?os=darwin`, `?signin=noreg` (a Frappe v15 site), `?slow=1`, `?fail=sites`. The full list is at the top of the mock. Only Vite's `mock` mode includes it, so production builds never contain it.

## Tests

```bash
cd desktop/frontend && npm run build   # go vet and go test need frontend/dist
cd .. && go vet ./... && go test ./...
```

There are no frontend unit tests; `npm run build` type-checks the frontend (`tsc`). The root module's `go test ./...` skips `desktop/`; `.github/workflows/desktop.yml` runs it on Windows and macOS: `npm ci`, the frontend build, `go mod tidy -diff`, vet, tests, `wails3 build`, a check that the committed bindings and lockfile are unchanged, and govulncheck.

After changing a service's Go API, regenerate the TypeScript bindings and commit them:

```bash
wails3 generate bindings -clean=true -ts -i
```

## Layout

- `main.go`: the window and the three services.
- `services/`: `AppService` (environment, ffc detection and installer, update check, links and folders), `SitesService` (sites and the OAuth sign-in, events `config:changed` and `signin:progress`), `AssistantsService` (MCP entries). Errors reach the UI as `services.Error` with a `code` and a `message`.
- `frontend/src/lib/backend.ts`: the typed layer over the generated bindings in `frontend/bindings`.
- `frontend/src/screens/`: sites, the add-site sheet, assistants, settings, onboarding.

## Module boundaries

`desktop/` is its own Go module (`github.com/nasroykh/foxmayn_frappe_cli/desktop`). Because its path sits under the ffc module path, it can import ffc's `internal/` packages (`sitesetup`, `mcpinstall`, `config`, `client`, `release`, `sitecache`). Wails, Node and CGO stay in `desktop/go.mod`, never in the root `go.mod`: the CLI release is CGO-free. The app never runs the install scripts and never bundles its own ffc.

## See also

- [Releasing](releasing.md#desktop-release)
- [Development](README.md)
- [Desktop app](../desktop/README.md)
