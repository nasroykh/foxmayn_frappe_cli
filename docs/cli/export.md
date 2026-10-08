# Export and import templates

`ffc export` writes the documents of a DocType with the rows of their child tables, in the layout of Frappe's Data Import. `ffc import-template` writes the header of such a file, ready to fill. Both read only; neither changes the site. [`ffc import`](import.md) reads such a file back (CSV, JSON or NDJSON).

## export

```bash
ffc export -d "Sales Invoice" -o invoices.csv
ffc export -d "Sales Invoice" --filters '{"docstatus":1}' --fields customer,posting_date,items.item_code,items.qty
ffc export -d Customer --no-tables --output ndjson | jq .customer_name
ffc export -d Item --xlsx -o items.xlsx
```

| Flag | Meaning |
| --- | --- |
| `-d, --doctype` | The DocType to export (required). |
| `-f, --fields` | Columns, comma-separated: fields of the DocType, `table.field` for a table's field, or a table's name for all its fields. |
| `--tables` | Child tables to include, comma-separated table fieldnames (default all). |
| `--no-tables` | Leave out every child table. |
| `--filters` | JSON filters, as for `list-docs` (`@FILE` and `@-` work). |
| `--order-by` | Order of the documents (default `creation asc, name asc`). |
| `-l, --limit` | At most this many documents (default 0: all). |
| `--page-size` | Documents per request (default 500). |
| `-o, --output-file` | File to write; `-` or none writes to stdout. |
| `--force` | Replace an existing output file. |
| `--xlsx` | Ask the site for an Excel workbook instead (see below). |

### Formats

The format is an explicit `--output` (or `--json`): `csv` (the default), `tsv`, `json` or `ndjson`. `table`, `yaml` and `--jq` do not apply. `FFC_OUTPUT` is ignored: export writes CSV unless the command line asks for another format.

CSV and TSV are flat, in the layout Frappe's Data Import uses. Frappe imports CSV and Excel only. A TSV cell escapes backslashes, tabs and line breaks (`\\`, `\t`, `\n`), so TSV is for reading, not for importing back:

- the header holds fieldnames: `name`, the DocType's fields, then `items.name`, `items.<field>` for each table. Frappe's importer matches these names.
- each document takes one row with its own values and the first row of each table;
- each further table row takes one more row, with the document's columns blank. That is how the importer knows the row belongs to the same document.

```text
name,customer,items.name,items.item_code,items.qty,taxes.name,taxes.rate
SINV-0001,ACME,r1,ITEM-A,2.0,t1,5.0
,,r2,ITEM-B,1.0,,
SINV-0002,Bob,r3,ITEM-C,4.0,,
```

The cells follow Frappe's exporter:

- a Duration is written as Frappe shows it, for example `1d 2h 3m 4s` (hours only when the field hides days);
- a text that starts with `=`, `+`, `-`, `@`, a tab or a carriage return gets a leading `'`, so a spreadsheet does not run it as a formula. The importer removes it again (v15 and v16);
- numbers are written as the site sends them, null as an empty cell.

As with Frappe's own export, a text that itself starts with `'` loses that quote when the file is imported.

JSON and NDJSON write each document with its tables nested as arrays. The rows keep `name`; their order is their `idx`. The values are as the site sends them, with no Duration text and no `'`.

### Columns

By default the columns are `name` and every field you may read that holds data, in form order. Layout fields, tables, Password fields and virtual fields are left out, and so are a tree's `lft` and `rgt`. Every table you may read comes with `name` and its readable fields.

`--fields` names the columns instead. When it names a table (`items` or `items.qty`), only the tables it names are exported. When it names none, all tables are, or those of `--tables`. An unknown field is a usage error (exit 2); a field above your permission level is a permission error (exit 5). When your roles cannot be read, only level-0 fields are offered, with a warning.

### Paging and files

The documents are read page by page, in creation order unless `--order-by` says otherwise; `name` is added to an order that does not sort by it, so pages never repeat or skip documents with equal sort values. Documents created or deleted during the export can still shift the pages. The rows of each page come with one request per table (`frappe.client.get_list` on the child DocType, which checks your read access on the parent). Each page is written as it arrives: memory holds one page of documents (`--page-size`, 500 by default) and all their table rows, never the whole export.

CSV, TSV, JSON and NDJSON need only read access, like `list-docs`. Only `--xlsx` checks the Export permission.

With `--output-file` the file is written to a temporary file next to it and renamed into place when complete: a failure or Ctrl+C leaves nothing behind. An existing file is replaced only with `--force` (checked before anything is sent). An export file is created readable by you only (`import-template` files, which hold no data, get the usual mode). On stdout, a failed page leaves the rows written so far, and the exit code says it failed.

### --xlsx

`--xlsx` asks the site for an Excel workbook, through the same method as the desk's Data Import export (`download_template`), with the columns chosen above and `--filters`. Things differ from the CSV:

- Frappe builds the whole file in memory, in one request; raise `--timeout` for a big export.
- The order is your list view's sort setting in the desk; `--limit`, `--order-by` and `--page-size` do not apply.
- It needs the Export permission on the DocType (System Manager always has it). Without it, the error is exit 5.
- On v16, Frappe turns Text Editor HTML into plain text. The CSV keeps the HTML.
- A workbook is never printed to a terminal: pass `--output-file` or redirect stdout. `--output`, `--json` and `--jq` are refused with `--xlsx`.

## import-template

```bash
ffc import-template -d "Sales Invoice" -o invoices.csv
ffc import-template -d Customer --fields customer_name,customer_group --no-tables
ffc import-template -d Item --xlsx -o items.xlsx
```

It writes the header row of a Data Import CSV, with the same columns as `ffc export`, chosen with the same `--fields`, `--tables` and `--no-tables`. Fill one row per document, and one more row per extra table row with the document's columns blank.

Like the desk's template, it includes read-only and hidden fields; Frappe ignores what it cannot set. Password fields are left out: add the column by hand if you import passwords.

`--xlsx` asks the site for Frappe's blank Excel template with these columns. It needs read access only. `-o`, `--force` and the terminal rule work as for `export`.
