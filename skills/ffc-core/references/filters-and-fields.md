# Filters, fields, ordering and paging

Used by `list-docs`, `count-docs`, `aggregate`, `bulk-update --filters`, `bulk-delete --filters` (and the MCP `filters` arguments). ffc checks only that `--filters` is a JSON object or array; the operators and their meaning are Frappe's own.

## Filter forms

```bash
# Object: every key is an equality test, all must match
--filters '{"status":"Open","docstatus":1}'

# Array of [field, operator, value]: all must match
--filters '[["grand_total",">",1000],["status","=","Unpaid"]]'

# Four elements [DocType, field, operator, value]: filter on a child table
--filters '[["Sales Invoice Item","item_code","=","SKU-001"]]'

# From a file or stdin
--filters @filters.json
cat filters.json | ffc list-docs -d ToDo --filters @- --json
```

Common Frappe operators: `=`, `!=`, `>`, `<`, `>=`, `<=`, `like`, `not like`, `in`, `not in`, `between`, `is` (value `set` or `not set`). Examples:

```bash
--filters '[["customer_name","like","%acme%"]]'
--filters '[["status","in",["Open","Overdue"]]]'
--filters '[["posting_date","between",["2026-01-01","2026-03-31"]]]'
--filters '[["email","is","set"]]'
```

Operator list: Frappe behaviour, not checked by ffc. Test an unfamiliar operator with `count-docs` first.

- Empty or invalid JSON is a usage error (exit 2) before any request. An empty `@file` or `@-` is an error too, so a broken pipe never turns into "no filter".
- Bulk commands refuse `{}` and `[]`, because they match every document. To really act on all of them, say so: `'[["name","is","set"]]'`.
- Booleans and Check fields are 0/1 in Frappe: `{"enabled":1}`.
- `docstatus`: 0 draft, 1 submitted, 2 cancelled.

## Fields

- `-f/--fields` takes CSV (`name,status`) or a JSON array (`'["name","status"]'`). Use the JSON form when an expression contains a comma.
- `list-docs` returns only `name` without `--fields`. Ask for the fields you need; `'["*"]'` returns every column of the parent table (no child rows).
- `get-doc -f` narrows the output (and JSON) to those fields; `--keys` wins for JSON.
- Field names come from `ffc get-schema -d DT --json --keys fields` (`fieldname`, not the label).
- No SQL aggregates in `--fields` on Frappe v16 (`count(name) as n` fails with exit 6). Use `ffc aggregate` (ffc-reports-api skill), which works on v15 and v16.

## Ordering and paging

- `-o/--order-by "modified desc"` (list-docs only; `-o` is not an output flag).
- `-l/--limit N`: default 20 for list-docs; `0` = no limit. Negative values are refused.
- `--start N`: offset for manual paging.
- `--all`: fetch every page (`--page-size`, default 500) and stream JSON, NDJSON, CSV or TSV as it arrives. Add an `--order-by` when the data may change during the run, since pages are offset-based.
