# ffc desktop

The ffc desktop app: a Wails v3 shell around the same Go code as the CLI. Scope and order of work are in the vault note "ADR-004 Desktop app scope, CLI groundwork first".

This directory is its own Go module (`github.com/nasroykh/foxmayn_frappe_cli/desktop`). Because its path sits under the ffc module path, it may import ffc's `internal/` packages (`internal/sitesetup`, `internal/mcpinstall`, `internal/config`), while Wails, Node and CGO stay out of the CLI's `go.mod` and release build.

Status: scaffold only (Wails template, React 19, Tailwind v4, shadcn/ui preset `bcivVtCa` with every component added). No app code yet.

## Requirements

- Go (version from `go.mod`), Node and npm
- The Wails CLI matching `go.mod`: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28`

## Develop and build

```bash
cd desktop
wails3 dev      # hot reload
wails3 build    # binary in desktop/bin/
```

`go vet ./...` needs `frontend/dist`, so build the frontend once first (`wails3 build` or `npm run build` in `frontend/`).

## Notes

- The root CI does not build this module yet: `go test ./...` and `go vet ./...` at the repository root skip nested modules.
- Product name, identifier and company in `build/config.yml` are still the template's placeholders; the name waits for decision D13 (the "Frappe" trademark).
- Before the first desktop release: `ffc update` reads GitHub `releases/latest`, so publish desktop releases with `make_latest: false` or make `update.go` accept only `v*` tags.
