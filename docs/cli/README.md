# CLI reference

Every `ffc` command, grouped by task, plus the flags and conventions they all share.

```text
ffc [global flags] <command> [flags]
```

Run `ffc <command> --help` for the full text of any command. This reference follows that help output.

## Commands by task

| Task | Commands | Page |
| --- | --- | --- |
| Set up sites and settings | `init`, `site`, `config`, `ping` | [Sites and settings](sites-and-settings.md) |
| Read and write documents | `get-doc`, `list-docs`, `count-docs`, `create-doc`, `update-doc`, `delete-doc` | [Documents](documents.md) |
| Edit a document in your editor | `edit-doc` | [edit-doc](edit-doc.md) |
| Change many documents at once | `bulk-create`, `bulk-update`, `bulk-delete` | [Bulk operations](bulk.md) |
| Submit, cancel, amend, workflows | `submit-doc`, `cancel-doc`, `amend-doc`, `copy-doc`, `rename-doc`, `restore-doc`, `discard-doc`, `workflow` | [Lifecycle and workflow](lifecycle-and-workflow.md) |
| Find documents, compute totals | `search`, `aggregate`, `count-docs --group-by` | [Search and aggregate](search-and-aggregate.md) |
| Reports and server methods | `list-reports`, `run-report`, `call-method` | [Reports and methods](server-calls.md) |
| Any other endpoint | `api` | [ffc api](api.md) |
| Files and PDFs | `upload`, `download`, `attachments`, `pdf` | [Files and PDF](files-and-pdf.md) |
| Comments, assignments, tags, shares | `comment`, `assign`, `unassign`, `tag`, `untag`, `share`, `unshare` | [Collaboration](collaboration.md) |
| DocTypes, schemas, local cache | `list-doctypes`, `get-schema`, `cache` | [Schema and cache](schema-and-cache.md) |
| Who am I, what may I do, document history | `whoami`, `can`, `doc-info` | [Identity and permissions](identity-and-permissions.md) |
| Check the setup | `doctor` | [doctor](doctor.md) |
| Update ffc | `update` | [update](update.md) |
| Shell completion | `completion` | [Completion](completion.md) |
| MCP server for AI agents | `mcp` | [MCP server](../mcp/README.md) |

Cross-cutting topics:

- [Output formats](output-formats.md): `--output`, `--json`, `--jq`, CSV, NDJSON, streaming lists.
- [Exit codes](exit-codes.md): what each code means and the JSON error shape.
- [Dry runs and debugging](dry-run-and-debugging.md): `--dry-run` and `--debug`.

## Global flags

These work with every command.

| Flag | Short | Default | Description |
| --- | --- | --- | --- |
| `--site` | `-s` | `default_site` | Site name from the config. |
| `--config` | `-c` | `~/.config/ffc/config.yaml` | Path to the config file. |
| `--json` | `-j` | off | Print raw JSON instead of a table (same as `--output json`). |
| `--output` | | `table` | `table`, `json`, `ndjson`, `csv`, `tsv` or `yaml`. No short form. |
| `--jq` | | | Filter the JSON result with a jq expression; strings print raw. |
| `--quiet` | `-q` | off | No progress spinner. The spinner is also off when stderr is not a terminal, or when `NO_COLOR` or `CI` is set. |
| `--timeout` | | `30s` | HTTP timeout per request to the site, for example `2m`. Raise it for heavy reports. |
| `--no-input` | | off | Never prompt; fail instead. Also on when stdin is not a terminal. Pass `--yes` to confirm deletions. |
| `--debug` | | off | Trace every HTTP request on stderr with secrets redacted. `--debug=body` adds headers and bodies. |
| `--version` | `-v` | | Print the version. |
| `--help` | `-h` | | Help for any command. |

`FFC_SITE`, `FFC_CONFIG`, `FFC_TIMEOUT`, `FFC_OUTPUT` and `FFC_DEBUG` set the matching flag when the flag is not given. See [Configuration](../getting-started/configuration.md#environment-variables).

> `-o` is never the output format. On `list-docs` it is `--order-by`; on `download` and `pdf` it is `--output-file`. Use `--output` for the format.

## Conventions shared by all commands

**Selecting a document.** Most commands take `-d/--doctype` and `-n/--name`. For a Single DocType (such as `System Settings`), `get-doc`, `update-doc`, `edit-doc` and `doc-info` default `--name` to the DocType name. `delete-doc` always needs `--name`.

**JSON input from files.** JSON-valued flags (`--data`, `--args`, `--filters`, `--set`) also take `@FILE`, or `@-` for stdin:

```bash
ffc create-doc -d ToDo --data @todo.json
jq -n '{status:"Open"}' | ffc count-docs -d ToDo --filters @-
```

**Filters** are a JSON object (`'{"status":"Open"}'`) or a list of conditions (`'[["status","=","Open"],["modified",">","2026-01-01"]]'`), as in Frappe's REST API.

**No prompts without a terminal.** In pipes, cron jobs, CI and agents, ffc never waits for input. A command that would prompt fails with exit code 2 and a hint, for example "pass --yes" for a deletion.

**Data on stdout, messages on stderr.** Results go to stdout; spinners, warnings and errors go to stderr, so piping stays clean.

**Server text is sanitised.** Terminal control characters, bidi overrides and zero-width characters in data from the site are stripped before printing.

## See also

- [Quickstart](../getting-started/quickstart.md)
- [Configuration](../getting-started/configuration.md)
- [Output formats](output-formats.md)
- [Exit codes](exit-codes.md)
