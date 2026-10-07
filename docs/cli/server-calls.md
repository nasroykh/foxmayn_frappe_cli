# Reports and methods

List and run Frappe reports, and call whitelisted server methods: `list-reports`, `run-report`, `call-method`.

## List reports: `list-reports`

```bash
ffc list-reports
ffc list-reports --module Accounts --limit 20
ffc list-reports --all --json
```

| Flag | Default | Description |
| --- | --- | --- |
| `-m, --module` | | Only reports of this module. |
| `-l, --limit` | `50` | Maximum reports; `0` for no limit. |
| `--all` | off | Fetch every row, page by page. |
| `--page-size` | `500` | Rows per request with `--all`. |

A complete, unfiltered list also fills the [local cache](schema-and-cache.md#the-local-cache) used by shell completion.

## Run a report: `run-report`

```bash
ffc run-report -n "General Ledger" --filters '{"company":"My Company","from_date":"2026-01-01"}' -l 10
ffc run-report -n "Accounts Receivable" --json --keys columns,result
```

| Flag | Description |
| --- | --- |
| `-n, --name` | Report name (required). |
| `--filters` | Report filters as a JSON object (also `@FILE`, `@-`). |
| `-l, --limit` | Maximum result rows, for the table and JSON (`0` = all). |
| `--keys` | Top-level keys to keep in JSON output, e.g. `columns,result`. |
| `--prepared` | Use Frappe's background job (prepared report) instead of running the report in the request. |
| `--fresh` | With `--prepared`: prepare a new result even if a finished one exists. |
| `--wait` | With `--prepared`: how long to wait for the job (default `5m`). |
| `--prepared-name` | Wait for this Prepared Report and return its result (from an earlier run with the same `--filters`). |

The table shows the report's columns. `--json` prints the full response. Heavy reports may need a longer `--timeout`, for example `--timeout 2m`.

### Heavy reports: `--prepared`

ffc normally runs a report in the request, even one marked "prepared" in Frappe, so a heavy report can outlast the server's request timeout. `--prepared` uses Frappe's prepared reports instead: a worker on the site's `long` queue runs the report and keeps the result.

```bash
ffc run-report -n "Stock Balance" --filters '{"company":"Acme"}' --prepared --wait 10m
```

- If you already have a finished result for the same filters, ffc returns it at once. It may be old: stderr names the Prepared Report and when it finished. `--fresh` prepares a new one.
- Otherwise ffc reuses your queued job for these filters, or starts one, and checks it until it finishes or `--wait` runs out. The per-request `--timeout` still applies to each check.
- When the wait runs out, ffc exits with code 7 and prints the command to continue, with `--prepared-name`. A job that never starts usually means the site has no worker on the `long` queue.
- A job that fails also exits 7. The error message appears when your user may read Prepared Report documents (System Manager or Prepared Report User).
- Each new job saves a Prepared Report document on the site; Frappe deletes them after 30 days. With `--json`, the response's `doc` is that document.
- On a report that is not marked "prepared", `--prepared` runs it normally.

## Call a server method: `call-method`

```bash
ffc call-method --method frappe.ping
ffc call-method --method frappe.client.get_count --args '{"doctype":"ToDo","filters":{"status":"Open"}}'
ffc call-method --method frappe.desk.form.load.getdoc --args '{"doctype":"ToDo","name":"TD-0001"}' --raw
```

| Flag | Description |
| --- | --- |
| `--method` | Dotted method path, e.g. `frappe.ping` (required). |
| `--args` | JSON object of method arguments (also `@FILE`, `@-`). |
| `--get` | Send a GET request, for methods whitelisted for GET only. The default is POST. |
| `--raw` | Print the whole response object, not only `message` (desk methods also return `docs`, `docinfo`, `_server_messages`). |
| `--dry-run` | Show the request; send nothing. |

`call-method` prints the response's `message`. For binary responses (PDFs, files) use [`ffc api ... --output-file`](api.md). Any method can write, so `--dry-run` holds back every request, reads included.

## See also

- [ffc api](api.md)
- [Output formats](output-formats.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
