# Testing

Run the unit tests against the in-memory fake Frappe site, and the contract tests against a real one.

## Unit tests

```bash
make test               # go test -race ./...
go test ./internal/cmd/ -run TestBulk -v
make lint               # gofmt, vet, staticcheck
```

The root module's tests skip `desktop/` (a separate module); see [Desktop app development](desktop.md#tests).

Tests never touch your real config or cache: the cache directory and config paths are pointed at temporary directories.

### The fake Frappe site (`internal/frappetest`)

An in-memory site that behaves like Frappe where ffc depends on it:

- `/api/resource` CRUD with filters, fields, paging and ordering;
- the `/api/method` endpoints ffc calls (lifecycle, workflow, collaboration, search, aggregates, document info, identity, OAuth);
- files (`upload_file`, `attach_file`, `/files`, `/private/files`, PDF and print HTML);
- API key, Bearer token and session auth, with login and logout counters;
- Frappe's real error shapes (`exc_type`, `_server_messages`, `exception`), captured from a v16 site;
- v15 versus v16 differences, selected by the Frappe version the fake reports.

`HandleMethod` adds a whitelisted method, `Handle("METHOD /path", h)` overrides a route, and `Requests()` returns what was sent.

### CLI harness

In `internal/cmd/cli_harness_test.go`: `fakeConfig(t, site, "apikey"|"password"|"oauth")` writes a config for the fake, and `runFFC(t, cfg, stdin, args...)` runs the root command with stdout and stderr captured and resets every flag afterwards.

### MCP tests

`newMCPFake(t, readOnly)` registers the tools against a fake site; `callTool` goes through JSON-RPC so argument decoding matches a real client.

## Contract tests

The contract tests (`internal/cmd/contract_*_test.go`, build tag `contract`) run against a real, disposable Frappe site and pin the Frappe behaviour the fake imitates: number literals, list defaults, child-table updates, submit/cancel/amend, schema merging, sessions, OAuth expiry and revocation, aggregates per version, and more.

**They write data.** Each run creates a custom submittable DocType (`FFC Contract Test`, with a child table, a Custom Field and a Property Setter) and removes it again, including leftovers of an interrupted run. Never point them at a production site.

```bash
make contract SITE=<site name in your ffc config>
```

Or with environment variables:

```bash
FFC_CONTRACT_URL=http://localhost:8080 FFC_CONTRACT_API_KEY=... FFC_CONTRACT_API_SECRET=... \
  go test -tags contract -count=1 -run TestContract -v ./internal/cmd/
# or FFC_CONTRACT_USER / FFC_CONTRACT_PASSWORD instead of the key pair
```

CI (`.github/workflows/contract.yml`) runs them nightly, on demand, and on pull requests that touch them, against ERPNext v15 and v16 started from frappe_docker's `pwd.yml`.

## Continuous integration

| Workflow | Runs |
| --- | --- |
| `ci.yml` | gofmt, `go mod tidy -diff`, vet, race tests, staticcheck, govulncheck; vet and tests on Windows; a cross-build. On every push and pull request. |
| `contract.yml` | Contract tests against ERPNext v15 and v16. |
| `desktop.yml` | The desktop app on Windows and macOS when `desktop/`, `internal/` or `go.mod` change. |
| `release.yml` | Tidy check, vet and tests, then the release. On `v*` tags. |

## See also

- [Development](README.md)
- [Releasing](releasing.md)
- [Desktop app development](desktop.md)
