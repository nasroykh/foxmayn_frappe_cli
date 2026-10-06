---
name: ffc-dev-command
description: Add or change a CLI command, subcommand or flag in the ffc Go codebase (internal/cmd) the way the repo expects - cobra file layout and flag-variable prefixes, callSite for site access, render/printResult for --output and --jq, JSON flags with @FILE, lists with --all, writes with --dry-run and --yes, prompts through runForm, exit-code errors, shell completion, doctor checks, help text and the docs to update. Use it whenever you create a new file in internal/cmd, add a flag, change command output, or wire a new Frappe call into a command. Load ffc-dev first for build and test commands.
---

# Add or change an ffc command

Ground truth is the code next to yours: copy the closest existing command (`get_doc.go` for a read, `create_doc.go` for a write, `delete_doc.go` for a confirmed write, `list_docs.go` for a list). CLAUDE.md "Conventions" has the full rules.

## Checklist

1. **File:** `internal/cmd/<snake_name>.go`, package `cmd`; `Use` is kebab-case. Flag variables are package-level with a unique two-letter prefix (`gd` get-doc, `ld` list-docs, `cd` create-doc...); grep before picking one.
2. **Command:** `Args: cobra.NoArgs` for flag-only data commands (positional args only where the UX needs them, e.g. `search TEXT...`, `upload FILE`). `RunE`, never `Run`. `Long` ends with an `Examples:` block; that text is the user documentation agents read.
3. **Validate first:** parse and check every flag before any network call. Bad values return `usageErrorf(...)` (exit 2); cobra's own flag errors are already usage errors.
4. **Site access:** `callSite(cmd, "Doing X…", func(ctx, c) (T, error) {...})`, or `callSiteCfg` when you need the site config, or `newClient`/`newClientCfg` for several calls (then `defer c.CloseQuietly()`, which logs a password session out). Never `client.New` in a command.
5. **New Frappe call:** add a method on `client.FrappeClient` that goes through `c.do(...)` with a hints map (see ffc-dev-client).
6. **Output:** `if machineOutput() { return printResult(v, fields...) }`, then the table (`output.PrintTable`, `output.PrintDocTable`, `output.PrintSuccess`), or `render(v, fields, tableFn)`. Never call `output.PrintJSON` directly, or `--output`/`--jq` break. Lists use `listDocs(cmd, pages, title, doctype, opts, fields, tableFn)` with a `pageFlags` registered via `pages.register(cmd, "limit", "start")`, which adds `--all`/`--page-size`.
7. **JSON flags:** `parseObject("--data", raw)` for objects, `filtersFlag(raw)` for filters, `jsonFlag` for anything else, so `@FILE` and `@-` work and an empty file is an error. Field lists: `parseFields`. Limits: `listLimit` (0 = no limit, negatives refused).
8. **Writes:** `addDryRun(cmd, false)` in `init()` (`true` only when every request must be held back, as in call-method and api). A confirmed write: `--yes` flag, then `if !yes && !dryRunOn(cmd) { if err := confirm("…"); err != nil { return err } }`. Under a dry run, still do the reads that prove the write would work.
9. **Single DocTypes:** a command taking `-d`/`-n` for one document defaults the name with `docNameOrSingle(name, doctype)` (not for delete).
10. **Register:** `rootCmd.AddCommand(cmd)` (or `parentCmd.AddCommand`) in `init()`; `_ = cmd.MarkFlagRequired(...)`.
11. **Tests** against the fake site through `runFFC` (ffc-dev-testing): success, machine output, a usage error, the server error classes that matter, and `--dry-run` sending no write.
12. **Docs:** README.md, CLAUDE.md (architecture line, new pitfall) and the matching user skill under `skills/`.

Template and the read/list variants: [references/templates.md](references/templates.md).

## Prompts and confirmation

- No prompt without a terminal: forms go through `runForm(groups...)` (auth_wizard.go), which returns `errNoInput` (usage error) under `--no-input` or a non-TTY stdin and maps Esc/Ctrl+C to `errAborted`. Simple yes/no: `confirm(prompt)` (helpers.go), which turns `errNoInput` into "pass --yes".
- huh v1.0.0 binds only Ctrl+C to quit. A hand-built form must use `.WithKeyMap(escQuitKeyMap())` (config_cmd.go) so Esc aborts.
- A declined prompt is `errAborted` (non-zero exit). Only the config TUI's explicit "Cancel" exits 0.

## Errors and exit codes

Wrap with `fmt.Errorf("context: %w", err)`; never log and return. `classify` (exit.go) maps: `usageError` 2, `client.AuthError` / 401 3, 404 or missing DocType 4, 403 or `deniedError` 5, `client.StateError` / 400/409/417/422/413 / `TimestampMismatchError` 6, `client.TransportError` / 5xx / 429 7, `partialError` 8 (bulk: `bulkReport.err()`), `codeError{code}` for a fixed code (doctor uses 1). Exit codes are a documented contract: never repurpose one.

## Things that come for free, or must not be broken

- **Completion:** registered by flag name in `registerCompletions` (completion.go): any `doctype` flag and a `fields` flag next to it complete automatically. A new enum flag adds one `reg(cmd, "flag", fixedValues(...))` line. Completion reads only config and cache: never a request.
- **Env vars** `FFC_SITE`, `FFC_CONFIG`, `FFC_TIMEOUT`, `FFC_OUTPUT`, `FFC_DEBUG` are applied in `applyEnv`; do not read them yourself.
- **Pre-run:** `rootCmd.PersistentPreRunE` is owned by update_check.go. The update check skips `update`, `mcp`, `completion`, `__complete`, `help`; a command whose stdout is a protocol must be added to that skip list.
- **`-o`** stays local (`--order-by`, `--output-file`). The output flag is `--output` with no shorthand.
- **Caches:** site versions through `serverInfo(ctx, c, cfg, refresh)` (24 h; version-dependent code uses `.FrappeMajor()`); DocType/report lists and schemas through `meta_cache.go` (fill only from complete, unfiltered results; never cache documents). Cache paths come from `internal/sitecache` (`sitecache.Dir`, `sitecache.DirName`), never from a raw site name.
- **doctor checks:** a method on `doctor` calling `d.add(id, checkPass|checkWarn|checkFail, message, hint)`. Ids are a JSON contract (never rename); a check changes nothing (no refresh, no cache, no cleanup) and prints no secret.
- **Sanitising:** the `output` package sanitises server strings; sanitise yourself (`text.Sanitize`) anything you print another way.
