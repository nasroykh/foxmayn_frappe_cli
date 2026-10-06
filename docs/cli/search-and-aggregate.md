# Search and aggregate

Find documents by text, resolve a title to a document name, and compute counts and totals on the server: `search`, `aggregate`, `count-docs --group-by`.

## Find a document: `search`

```bash
ffc search acme -d Customer            # resolve "acme" to Customer names, like a Link field
ffc search "" -d Item --limit 5        # the first 5 items
ffc search "overdue invoice"           # global search across DocTypes
ffc search acme -d Customer --json
```

| Flag | Default | Description |
| --- | --- | --- |
| `-d, --doctype` | | Search one DocType like a Link field. Omit for global search. |
| `-l, --limit` | `20` | Maximum results; at least 1. |

The text is positional; several words are joined with spaces. Put `--` before a text that starts with a dash.

**With `--doctype`** ffc runs the search a Link field runs (`search_link`). It matches the DocType's search fields and title, applies its link query and your permissions, and returns `value` (the document name), `description` and, for some DocTypes, `label`. Use it to turn "Acme" into `CUST-0042`. An empty text lists the first documents. An unknown DocType exits 4.

Frappe marks this answer cacheable for 60 seconds. ffc keeps no cache, but a caching proxy in front of the site may serve an answer up to a minute old, so a document created a moment ago may not show up yet.

**Without `--doctype`** ffc runs Frappe's global search over every DocType you may read, ranked by relevance; results have `doctype`, `name`, `content` and `rank`. Only DocTypes listed in **Global Search Settings** are searched, and only fields flagged **In Global Search** are indexed, so an empty answer proves nothing: use `list-docs --filters` instead. `a & b` searches the phrases `a` and `b` separately (at most 5 phrases) and combines the hits, cut to `--limit`.

## Totals per group: `aggregate`

Groups documents by one or more fields and computes counts, sums, averages, minimums and maximums on the server, without fetching the rows.

```bash
ffc aggregate -d ToDo --group-by status
ffc aggregate -d "Sales Invoice" --group-by customer --sum grand_total --count --limit 10
ffc aggregate -d "Sales Invoice" --group-by status,currency --sum grand_total,outstanding_amount --filters '{"docstatus":1}'
ffc aggregate -d "Sales Invoice" --sum grand_total --min posting_date --max posting_date   # one row, no grouping
ffc aggregate -d ToDo --group-by owner --order-by "owner asc" --json
```

| Flag | Default | Description |
| --- | --- | --- |
| `-d, --doctype` | | DocType (required). |
| `--group-by` | | Field(s) to group by; repeat or comma-separate, at most 5. Omit for one row over all matching documents. |
| `--count` | | Count per group (column `count`). The default when no other aggregate is given. |
| `--sum`, `--avg`, `--min`, `--max` | | Field(s) to aggregate (columns `sum_F`, `avg_F`, `min_F`, `max_F`); repeat or comma-separate. |
| `--filters` | | JSON filters (also `@FILE`). |
| `--order-by` | first aggregate, descending | A group-by field or aggregate column with `asc`/`desc`, e.g. `"sum_grand_total desc"`; comma-separate several. |
| `-l, --limit` | `100` | Maximum groups; `0` for all. ffc says on stderr when there were more. |

Notes:

- Fields must be plain field names of the DocType (letters, digits, underscore). A field of a linked or child DocType (`customer.territory`, `items.qty`) is refused before any request: Frappe v15 cannot group by one and v16 cannot aggregate one.
- ffc writes the query the way your site's Frappe version expects (v16 and v15 differ). It learns the version from the cache `whoami` fills; if the site was upgraded, ffc retries once with the other form.
- A field above your permission level is refused with exit 5 before anything is sent.
- ffc sorts the rows itself, because some databases ignore the order when grouping. Numbers keep the site's literal (a Currency sum prints as `20.0`).

## Counts per value: `count-docs --group-by`

```bash
ffc count-docs -d ToDo --group-by status
ffc count-docs -d "Sales Invoice" --group-by assigned_to --filters '{"docstatus":0}'
```

This runs the list view's sidebar count: one row per value of the field with its count, most frequent first, at most 50 groups (ffc warns when it hits 50; `aggregate` has no such cap).

Two values are special:

- `owner` puts your own group first.
- `assigned_to` is not a field. It counts, per System User, the ToDo records allocated to them that are not Cancelled (Open and Closed) and whose `reference_name` is a matching document's name. Frappe does not compare `reference_type`, so a ToDo on a document of another DocType with the same name counts too.

A field the DocType lacks exits 6.

## See also

- [Documents](documents.md)
- [Output formats](output-formats.md)
- [Identity and permissions](identity-and-permissions.md)
