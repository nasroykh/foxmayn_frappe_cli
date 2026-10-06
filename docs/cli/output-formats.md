# Output formats

Choose how results are printed (tables for people, JSON, NDJSON, CSV, TSV or YAML for scripts) and filter them with jq.

```bash
ffc list-docs -d "Sales Invoice" --all --output csv --fields name,customer,grand_total > invoices.csv
ffc list-docs -d ToDo --filters '{"status":"Open"}' --jq '.[].name'
export FFC_OUTPUT=ndjson    # machine output by default in scripts
```

## `--output`

| Format | What you get |
| --- | --- |
| `table` | The default human-readable view. Numbers and dates use your [display settings](sites-and-settings.md#display-settings-ffc-config). |
| `json` | Indented JSON. Same as `--json` / `-j`. |
| `ndjson` | One compact JSON value per line; one line per row for lists. |
| `csv`, `tsv` | A header row, then the data. |
| `yaml` | YAML. |

CSV and TSV details:

- Columns follow `--fields`; otherwise they are the sorted union of the keys.
- Nested values are written as JSON. Numbers are printed as the server sent them (`1500.0`), with no locale formatting.
- In TSV, tabs, newlines and backslashes inside values are escaped as `\t`, `\n` and `\\`.
- Cells are written as they are. A value starting with `=`, `+`, `-` or `@` runs as a formula when the file is opened in a spreadsheet, so treat data from untrusted users with care.

The default format comes from `FFC_OUTPUT` when `--output` is not given.

## `--jq`

`--jq EXPR` filters the JSON result before printing (jq syntax, built in; no `jq` binary needed).

- With the default format, each result prints as it comes, like `jq -r`: strings raw, other values as JSON.
- With `--output`, one result is rendered in that format. Several results print one after another for `json` and `yaml`, and as one list for `ndjson`, `csv` and `tsv`.
- `halt_error` exits with its code. Ctrl+C stops an endless expression.

```bash
ffc get-doc -d "Sales Invoice" -n SINV-0001 --jq .grand_total
ffc list-docs -d ToDo --fields name,status --jq '[.[] | select(.status=="Open")]' --output csv
```

## Long lists

`--all` on `list-docs`, `list-doctypes`, `list-reports`, `attachments` and `workflow pending` fetches every page. `json`, `ndjson`, `csv` and `tsv` are written as each page arrives; `table`, `yaml` and `--jq` wait for the whole list. See [Documents](documents.md#list-documents-list-docs).

## Selecting keys

Commands that return one object take `--keys a,b,c` to keep only those top-level keys, for example `ffc get-doc ... --json --keys name,status`.

## Errors

With `json` or `ndjson`, errors are printed on stderr as one JSON object and stdout carries no data. See [Exit codes](exit-codes.md#json-errors).

## `ffc api`

`ffc api` prints the response body unchanged with `--json` or `FFC_OUTPUT`. Only `--jq` or an explicit `--output` render it. See [ffc api](api.md#output).

## See also

- [Exit codes](exit-codes.md)
- [CLI reference](README.md)
- [Documents](documents.md)
