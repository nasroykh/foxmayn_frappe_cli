# Output formats, --jq and errors

## Formats

`--output table|json|ndjson|csv|tsv|yaml` (no shorthand), or `FFC_OUTPUT`. `-j/--json` is `--output json`; `--json` together with another `--output` is a usage error.

| Format | Use |
| --- | --- |
| `table` | default, for humans only |
| `json` | indented JSON; byte-identical to the old `--json` |
| `ndjson` | one JSON value per line; good for streaming lists |
| `csv`, `tsv` | spreadsheets; columns follow `--fields` |
| `yaml` | YAML |

Data goes to stdout. Spinner, warnings and errors go to stderr. The spinner is off with `-q`, `NO_COLOR`, `CI`, or when stderr is not a terminal.

## --jq

`--jq EXPR` runs a jq expression on the JSON result (gojq, built in; no `jq` binary needed).

```bash
ffc list-docs -d Customer -f name,customer_name --jq '.[].customer_name'
ffc whoami --jq .server.major
ffc attachments -d ToDo -n TD-0001 --jq '.[0].file_url'
```

- Without `--output`, each result prints as it comes, like `jq -r`: strings raw, other values as JSON. Zero results print nothing.
- With `--output`, a single result is rendered in that format; several are a list for ndjson/csv/tsv.
- A bad expression is a usage error (exit 2).

## Trimming results

- `--keys a,b` keeps top-level keys of a JSON document (get-doc, create-doc, update-doc, edit-doc, submit-doc, cancel-doc, amend-doc, copy-doc, workflow apply, get-schema, run-report).
- `--fields` picks columns for lists and orders CSV/TSV columns.
- `--all` with `--page-size N` streams every row for list-docs, list-doctypes, list-reports, attachments and workflow pending.

## Numbers and text

- Numbers keep the server's literal: big integers stay exact and a Currency `0.0` stays `0.0` in JSON. Do not expect `20` where the site sent `20.0`.
- Table output formats numbers and dates with the config's `number_format` / `date_format`. Machine output never does.
- Table output strips control and invisible characters from server text (terminal escape injection).

## Errors with machine output

With `--json`, `--output json` or `--output ndjson`, an error prints one JSON object on stderr and the process exits with the matching code:

```json
{"error":{"code":"not_found","exit_code":4,"status":404,"exc_type":"DoesNotExistError","message":"..."}}
```

`code` is one of `usage`, `auth`, `not_found`, `permission`, `validation`, `server`, `network`, `partial`, `interrupted`, `error`. `status` and `exc_type` appear when the site answered. See [exit-codes.md](exit-codes.md).

## Dry-run output

`--dry-run` with machine output prints `{"dry_run":true,"requests":[{"method","url","headers"?,"body"?,"changes"?}]}` on stdout and exits 0. `update-doc --dry-run` adds `changes` (field: `{from, to}`). Without machine output the plan goes to stdout as text, with "Dry run: nothing was sent." on stderr.
