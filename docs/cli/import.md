# Import

`ffc import` creates or updates documents, with the rows of their child tables, from a file in the layout of Frappe's Data Import: the layout [`ffc export`](export.md) writes. ffc reads, converts and checks the whole file on your machine, then writes each document through the REST API.

```bash
ffc export -d Customer -o customers.csv          # edit the file, then:
ffc import -d Customer customers.csv --mode update
ffc import -d "Sales Invoice" invoices.csv --mode insert --submit
ffc import -d Item items.json --mode update --dry-run
cat todo.ndjson | ffc import -d ToDo - --format ndjson --mode insert --json
```

| Flag | Meaning |
| --- | --- |
| `-d, --doctype` | The DocType to import into (required). |
| `FILE` | The file to read; `-` reads stdin. |
| `--mode` | `insert` (create every document of the file) or `update` (change the documents the `name` column names). Required. |
| `--format` | `csv`, `json` or `ndjson`. Default: from the extension (`.json`, `.ndjson`, `.jsonl`), else CSV. |
| `--submit` | Submit each document after writing it. |
| `--concurrency` | Documents written at the same time, 1 to 10 (default 1). |
| `--fail-fast` | Stop starting new documents after the first failure. |
| `--dry-run` | Check the file and show the writes, without sending them. |

There is no confirmation prompt, as for `bulk-create` and `bulk-update`: use `--dry-run` first.

## Formats

- **CSV**: the Data Import layout. The first non-empty row is the header. A document is its first row plus the rows below it whose document columns are all blank: those rows only add table rows. A row with a value in any document column starts the next document. Empty rows are skipped. Cells are trimmed, and a leading `'` before `=`, `+`, `-`, `@`, a tab or a carriage return is removed (Frappe's exporter adds it so a spreadsheet does not run a formula). The file must be UTF-8 (a BOM is fine).
- **JSON / NDJSON**: an array of documents, or one per line, as `ffc export --output json` (or `ndjson`) writes them: fieldnames as keys, each table an array of row objects. `null` and `""` mean "not set". Numbers and `true`/`false` are taken where they fit.
- **TSV** is refused: export escapes backslashes, tabs and line breaks in TSV cells, so the values would not come back as they were.
- **Excel** is not read. Save the sheet as CSV UTF-8, or use the desk's Data Import. A later `--server` mode will hand such files to Frappe.

## Columns

Headers are matched like Frappe's importer (untranslated):

| Header | Sets |
| --- | --- |
| `name`, `ID` | the document's name |
| `customer`, `Customer`, `Customer (customer)` | a field: its fieldname, its label (the first field with that label), or `Label (fieldname)` |
| `items.qty`, `Qty (Items)` | a table's field: `table.fieldname`, or `Label (Table label)` |
| `items.name`, `ID (Items)` | a table row's name |
| `owner`, `docstatus`, `creation`, `modified`, `modified_by`, `idx`, and a table's `parent`, `parenttype`, `parentfield`, `idx` | nothing: these are skipped with a warning (use `--submit` to submit) |

An unknown header, a table named without a field (`items`), or two columns for one field is a usage error (exit 2), and every one is listed. A column with no header is skipped (with a warning when it holds values).

## Values

A blank cell means "not set": it never clears a field. Values are converted as Frappe's importer does, and stricter where Frappe would silently change a value:

| Field type | Accepted |
| --- | --- |
| Check | `1`/`0`, `yes`/`no`, `y`/`n`, `true`/`false`, `t`/`f` (any case) |
| Int | a whole number (`12`, `+12`, `12.0`). `2.5` is refused (Frappe would store 2). |
| Float, Currency, Percent | a plain number: `1234.56`, `.5`, `1e3`. `1,234.56` is refused. |
| Date, Datetime | one format per column, guessed from its values like Frappe's `guess_date_format`: `2026-01-31`, `31-01-2026`, `01-31-2026`, `31/01/2026`, `31.01.26`, `31 Jan 2026`, `31 January 2026`, with a time `10:05`, `10:05:00`, `10:05:00.123456` or `10:05 PM`. The format most values match wins (day-first before month-first), and a value it cannot read is a problem; seconds with or without a fraction count as one format. Sent as `YYYY-MM-DD` and `YYYY-MM-DD HH:MM:SS`. |
| Duration | `1d 2h 3m 4s` (any of the parts, in that order, one space apart), as export writes it; a leading `-` for a negative one. JSON: seconds. |
| Select | one of the field's options (any value when it has none) |
| Link | the name of an existing document (checked, see below) |
| everything else (Data, Text, Time, Dynamic Link, ...) | the text as it is |

Link values are checked before anything is written: one list request per target DocType (in chunks of 50 names), compared without case, as MariaDB does. A Link to the imported DocType may name a document of the same file. When your user may not read the target DocType, a warning says the site will check those values on save. Dynamic Links are not checked (Frappe's importer does not either).

## Checking first

The whole file is read, converted and checked before the first write. When anything is wrong, every problem is listed with its row (the CSV line, the JSON item, the NDJSON line), column, value and reason, nothing is written, and the command exits 6:

```text
line 2, status "Pending": not one of the options: Open, Closed
line 3, items.item "I-Z": no Item named "I-Z"
2 problems in tickets.csv; nothing was written
```

With `--json` the problems go to stdout as `{"problems": [{row, column, value, reason}, ...]}`.

Header and invocation problems (unknown columns, `--mode update` without a name column, a refused format) are usage errors, exit 2.

## Insert

Each document is created with `POST /api/resource/<DocType>`, with its table rows.

- **Names.** Through the REST API Frappe keeps a given name only when the DocType's naming rule is `Prompt` or `UUID` (`set_new_name`); Frappe's own Data Import keeps it in more cases because it runs with an import flag. So ffc sends the name column only for those rules. With a `field:<x>` rule and no `<x>` column, the name goes into `<x>`. Otherwise the name column is ignored, with a warning, and Frappe names the documents. Table row names are never sent on insert.
- With names sent, two documents of the file with the same name (compared without case) are a problem.

## Update

Each document is read (`GET`), compared with the file, and written with `PUT` only when something differs; otherwise its status is `unchanged` (Frappe's importer would fail with "No changes to update").

- Every document needs a name; a name that appears twice is a problem.
- Only the fields whose value differs are sent. Numbers compare by value, Link names without case.
- A table that has rows for the document in the file replaces the document's rows. A row whose `items.name` is one of the document's rows keeps it: its name and the values the file leaves blank stay, and the file's values go on top. Other rows are new. Rows the file leaves out are removed, and the file's order becomes the order. A table with no rows for the document in the file is left alone, so a file cannot empty a table.
- The update carries the `modified` value that was read: when someone saved the document in between, it fails with "changed on the server" (exit 6 for that document) and nothing of it is saved.
- When the site saves a value other than the one sent (a field above your permission level is dropped silently by Frappe, a controller rewrites a value), the result carries a warning.

## --submit

The DocType must be submittable (otherwise exit 2) and must not have an active Workflow (exit 6, as `submit-doc`: apply workflow actions instead). After each write, a draft is submitted with `frappe.client.submit`. In update mode an unchanged draft is submitted too; a document already submitted is left alone. When the write succeeds and the submit fails, the document's status is `failed` and the error says it was created or updated.

## Results and exit codes

One result per document: `row` (where it starts in the file), `name`, `status` (`created`, `updated`, `unchanged`, `failed`, `interrupted`, `skipped`), `submitted`, and `warning` or `error`. On a terminal they print as a table; `--json` gives

```json
{"created": 1, "updated": 0, "unchanged": 2, "submitted": 0, "failed": 0, "skipped": 0,
 "results": [{"row": 2, "name": "CUST-0001", "status": "created"}, ...]}
```

The exit code is 0 when every document succeeded and 8 when any failed, was interrupted (Ctrl+C while it was being written: check it) or was skipped (`--fail-fast`). See [Exit codes](exit-codes.md).

## --dry-run

`--dry-run` does everything but the writes: it reads the meta, checks the file and the Link values, and in update mode reads each document to tell `updated` from `unchanged`. It prints the results and the requests it would send (an update lists the changed fields). With `--json`: the results plus `"dry_run": true` and `"requests"`. Each document stops at its first write, so the submits of created or updated documents are not shown; the submit of an unchanged draft is. A document that would fail (one that does not exist, for an update) makes the dry run exit 8.
