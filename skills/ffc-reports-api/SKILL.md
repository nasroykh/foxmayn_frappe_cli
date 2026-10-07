---
name: ffc-reports-api
description: Answer questions about Frappe/ERPNext data with ffc without fetching every row - run query reports (General Ledger, Accounts Receivable...), totals and counts per group (ffc aggregate, count-docs --group-by), text search and title-to-name lookup (ffc search), whitelisted server methods (ffc call-method) and raw authenticated requests to any endpoint (ffc api). Use it whenever the user asks "how many", "total per customer", "top 10", "find the invoice for Acme", wants a report, or needs an endpoint ffc has no command for. Read ffc-core first for flags and safety rules.
---

# Reports, aggregates, search, methods and raw API

## Pick the right tool

| Question | Command |
| --- | --- |
| Total / average / min / max, optionally per group | `ffc aggregate` |
| Quick count per value of one field (list sidebar) | `ffc count-docs --group-by` |
| A saved Frappe report | `ffc run-report` |
| "Which document is Acme?" | `ffc search TEXT -d DocType` |
| Text across many DocTypes | `ffc search TEXT` |
| A whitelisted Python method | `ffc call-method` |
| Any other path (desk methods, v2 API, files, paging) | `ffc api` |

## aggregate (computed by the site)

```bash
ffc aggregate -d ToDo --group-by status --json                                   # count per status
ffc aggregate -d "Sales Invoice" --group-by customer --sum grand_total --count --limit 10 --json
ffc aggregate -d "Sales Invoice" --sum grand_total --min posting_date --max posting_date --filters '{"docstatus":1}' --json
ffc aggregate -d "Sales Invoice" --group-by status,currency --sum grand_total --order-by "sum_grand_total desc" --json
```

- Columns: `count`, `sum_F`, `avg_F`, `min_F`, `max_F`. No aggregate flag means `--count`. No `--group-by` means one row over all matches.
- `--group-by` takes up to 5 fields (repeat or comma-separate); `--sum/--avg/--min/--max` repeat or take CSV.
- `--order-by` names a group field or an aggregate column with `asc`/`desc`; default: first aggregate, largest first. `--limit` default 100, `0` = all; a warning on stderr says when groups were cut.
- Fields must be plain fieldnames of the DocType. `customer.territory` or `items.qty` is a usage error (exit 2) before any request.
- It works on Frappe v15 and v16 (it picks the syntax from the cached version and retries once with the other). A field above the user's permission level is exit 5.
- Never put `count(name) as n` in `list-docs --fields`: Frappe v16 rejects it (exit 6).

## count-docs --group-by

```bash
ffc count-docs -d ToDo --group-by status --json             # [{"status":"Open","count":4}, ...]
ffc count-docs -d "Sales Invoice" --group-by assigned_to --filters '{"docstatus":0}' --json
```

At most 50 groups, most frequent first (use `aggregate` for more). `owner` puts your own group first. `assigned_to` is not a field: it counts open and closed (not cancelled) ToDos per System User whose `reference_name` matches; Frappe does not compare `reference_type`. An unknown field is exit 6.

## Reports

```bash
ffc list-reports --module Accounts --json
ffc run-report -n "General Ledger" --filters '{"company":"Acme","from_date":"2026-01-01","to_date":"2026-03-31"}' --timeout 2m --json
ffc run-report -n "Accounts Receivable" --filters @filters.json --limit 100 --keys columns,result --json
```

- `run-report`: `-n/--name` (required), `--filters` (JSON object; most reports need some, e.g. `company`), `-l/--limit` (rows, table and JSON; `0` = all), `--keys`.
- Heavy report: `--prepared` runs it as a Frappe prepared report on the site's `long` queue worker. It reuses your finished result for the same filters (stderr names it and its finish time; `--fresh` makes a new one), else your queued job, else starts one, and waits up to `--wait` (default 5m). A wait that runs out exits 7 with a resume command (`--prepared-name NAME` with the same `--filters`). Each new job leaves a Prepared Report document (deleted after 30 days).
- `--json` returns the full response (`columns`, `result`, and Frappe's other keys); trim with `--keys columns,result`.
- Heavy reports outrun the 30 s default: raise `--timeout`.
- `list-reports`: `-m/--module`, `-l` (default 50), `--all`, `--page-size`.
- Filter names per report are not exposed by ffc. When unsure, read the report's filter definitions on the site or ask the user. (UNVERIFIED: no ffc command lists a query report's filters.)

## search

```bash
ffc search acme -d Customer --json          # [{"value":"CUST-0042","description":...,"label"?}]
ffc search "" -d Item --limit 5 --json      # first 5 items
ffc search overdue invoice --json           # global search, ranked
```

- With `-d`: the Link-field search (search fields, title, link query, user permissions). Use it to turn a title into a document name before `get-doc`. Frappe marks the answer cacheable for 60 s, so a proxy may hide a document created a moment ago.
- Without `-d`: global search. It covers only DocTypes in Global Search Settings and fields flagged "In Global Search", so no hit proves nothing; fall back to `list-docs --filters '[["field","like","%x%"]]'`. `a & b` searches each phrase (at most 5) and combines the hits.
- `-l/--limit` default 20, at least 1. TEXT is positional; several words are joined; put `--` before a text starting with `-`.

## call-method

```bash
ffc call-method --method frappe.client.get_count --args '{"doctype":"ToDo","filters":{"status":"Open"}}'
ffc call-method --method frappe.auth.get_logged_user --get
ffc call-method --method frappe.desk.form.load.getdoctype --args '{"doctype":"ToDo"}' --raw
ffc call-method --method erpnext.some.method --args @args.json --dry-run
```

- POST to `/api/method/<method>` (`--get` for GET-only methods). Prints the `message` value as data: JSON even without `--json`, or the `--output`/`--jq` you ask for. `--raw` prints the whole response (desk methods answer in `docs`, `docinfo`).
- `--dry-run` holds back every request, reads included.
- A method can do anything the user may do: treat it as a write unless you know it only reads, and confirm first.

## ffc api (raw, authenticated)

```bash
ffc api /api/method/frappe.desk.form.load.getdoc -f doctype=ToDo -f name=TD-0001
ffc api /api/resource/Currency --paginate -f 'fields=["name","enabled"]'
ffc api POST /api/method/frappe.client.set_value -f doctype=ToDo -f name=TD-0001 -f fieldname=status -f value=Closed --dry-run
ffc api /private/files/contract.pdf --output-file contract.pdf
ffc api DELETE /api/resource/ToDo/TD-0001 --dry-run
```

- `ffc api [METHOD] PATH`. PATH is site-relative; full URLs are refused, so credentials never leave the site. Headers `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name`, `X-Forwarded-Host` are refused.
- **Defaults to GET even with fields** (unlike gh). Name `POST`/`PUT`/`DELETE` to write; only `--input FILE` implies POST.
- `-f key=value` string field; `-F key=value` typed (`true`, `false`, `null`, number, JSON, `@FILE`, `@-`). GET/HEAD send fields as query parameters, other methods as a JSON body.
- `-H "Name: value"`, `-i/--include` (status and headers on stderr), `--silent`, `--output-file PATH` (binary), `--paginate` (every page of `/api/resource/<DT>` or `/api/v2/document/<DT>` as `{"data":[...]}`; pass an `order_by`), `--dry-run` (nothing sent).
- The body streams to stdout; `--jq`/`--output` parse a JSON body. A status of 400 or more prints the body and exits with the mapped code. Never retried.
