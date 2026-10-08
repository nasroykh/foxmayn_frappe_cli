# Import

`ffc import` creates or updates documents, with the rows of their child tables, from a file in the layout of Frappe's Data Import: the layout [`ffc export`](export.md) writes. ffc reads, converts and checks the whole file on your machine, then writes each document through the REST API. With [`--server`](#--server-frappes-data-import), Frappe's own Data Import reads the file (CSV or Excel) and writes the documents in a background job on the site.

```bash
ffc export -d Customer -o customers.csv          # edit the file, then:
ffc import -d Customer customers.csv --mode update
ffc import -d "Sales Invoice" invoices.csv --mode insert --submit
ffc import -d Item items.json --mode update --dry-run
cat todo.ndjson | ffc import -d ToDo - --format ndjson --mode insert --json
ffc import -d Item items.xlsx --mode insert --server
```

| Flag | Meaning |
| --- | --- |
| `-d, --doctype` | The DocType to import into (required, except with `--resume`). |
| `FILE` | The file to read; `-` reads stdin. |
| `--mode` | `insert` (create every document of the file) or `update` (change the documents the `name` column names). Required, except with `--resume`. |
| `--format` | `csv`, `json` or `ndjson`; with `--server` `csv` or `xlsx`. Default: from the extension (`.json`, `.ndjson`, `.jsonl`, `.xlsx`), else CSV. |
| `--submit` | Submit each document after writing it. |
| `--concurrency` | Documents written at the same time, 1 to 10 (default 1). |
| `--fail-fast` | Stop starting new documents after the first failure. |
| `--dry-run` | Check the file and show the writes, without sending them. Not with `--server`. |
| `--server` | Import with Frappe's Data Import on the site, see below. |
| `--preview` | With `--server`: upload the file and show Frappe's warnings, without starting the import. |
| `--mute-emails` | With `--server`: Frappe sends no emails during the import. |
| `--wait` | With `--server`: how long to wait for the import job (default 10m, at least 1s). |
| `--resume NAME` | Start the Data Import `NAME` if it was not started yet, then wait for it; implies `--server`. |

There is no confirmation prompt, as for `bulk-create` and `bulk-update`: use `--dry-run` first.

## Formats

- **CSV**: the Data Import layout. The first non-empty row is the header. A document is its first row plus the rows below it whose document columns are all blank: those rows only add table rows. A row with a value in any document column starts the next document; `owner` and `docstatus` count as document columns here, as in Frappe, although their values are not set. Empty rows are skipped; a row whose values are all in ignored or untitled columns is a problem. Cells are trimmed, and a leading `'` before `=`, `+`, `-`, `@`, a tab or a carriage return is removed (Frappe's exporter adds it so a spreadsheet does not run a formula). The file must be UTF-8 (a BOM is fine).
- **JSON / NDJSON**: an array of documents, or one per line, as `ffc export --output json` (or `ndjson`) writes them: fieldnames as keys, each table an array of row objects. `null` and `""` mean "not set". Numbers and `true`/`false` are taken where they fit. A UTF-8 BOM is skipped.
- **TSV** is refused: export escapes backslashes, tabs and line breaks in TSV cells, so the values would not come back as they were.
- **Excel** (`.xlsx`) is read only with `--server`. Without it, save the sheet as CSV UTF-8.

## Columns

Headers are matched like Frappe's importer (untranslated):

| Header | Sets |
| --- | --- |
| `name`, `ID` | the document's name |
| `customer`, `Customer`, `Customer (customer)` | a field: its fieldname, its label (the first field with that label), or `Label (fieldname)` |
| `items.qty`, `Qty (Items)` | a table's field: `table.fieldname`, or `Label (Table label)` |
| `items.name`, `ID (Items)` | a table row's name |
| `owner`, `docstatus`, `creation`, `modified`, `modified_by`, `idx`, and a table's `parent`, `parenttype`, `parentfield`, `idx` | nothing: these are skipped with a warning (use `--submit` to submit) |

An unknown header, a table named without a field (`items`), two columns for one field, or a Password field (in any header form, or as a JSON key) is a usage error (exit 2), and every one is listed. ffc import never sets passwords: the site answers them masked, so they could not be compared, and their values would show in warnings and plans. A column with no header is skipped (with a warning when it holds values).

## Values

A blank cell means "not set": it never clears a field. Values are converted as Frappe's importer does, and stricter where Frappe would silently change a value:

| Field type | Accepted |
| --- | --- |
| Check | `1`/`0`, `yes`/`no`, `y`/`n`, `true`/`false`, `t`/`f` (any case). Other numbers are refused. |
| Int | a whole number (`12`, `+12`, `12.0`). `2.5` is refused (Frappe would store 2). |
| Float, Currency, Percent | a plain number: `1234.56`, `.5`, `1e3`. `1,234.56` is refused. |
| Date, Datetime | one format per column, guessed from its values like Frappe's `guess_date_format`: `2026-01-31`, `31-01-2026`, `01-31-2026`, `31/01/2026`, `31.01.26`, `31 Jan 2026`, `31 January 2026`, with a time `10:05`, `10:05:00`, `10:05:00.123456` or `10:05 PM`. The format most values match wins (day-first before month-first), and a value it cannot read is a problem; seconds with or without a fraction count as one format. When every value of a column also reads with day and month swapped, and some value then means another date (`01-02-2026`), a warning says how the column was read. Sent as `YYYY-MM-DD` and `YYYY-MM-DD HH:MM:SS`, with `.ffffff` added when the fraction of a second is not zero. |
| Time | `H:MM`, `HH:MM`, `HH:MM:SS` or `HH:MM:SS.ffffff` (24-hour); sent as `HH:MM:SS`, plus `.ffffff` when the fraction is not zero. |
| Duration | `1d 2h 3m 4s` (any of the parts, in that order, one space apart), as export writes it; a leading `-` for a negative one. JSON: seconds. |
| Select | one of the field's options (any value when it has none) |
| Link | the name of an existing document (checked, see below) |
| everything else (Data, Text, Dynamic Link, ...) | the text as it is |

Link values are checked before anything is written: one list request per target DocType (split so that each request URL stays under about 4 KiB), compared without case, as MariaDB does. The check lists documents as your user, so User Permissions apply, while Frappe's importer checks with `get_all`, which ignores them: a user restricted by User Permissions can be told that a document that exists is not there (the problem says so). When your user may not read the target DocType, a warning says the site will check those values on save. Dynamic Links are not checked (Frappe's importer does not either).

A Link to the imported DocType may name a document that the same file inserts, when the insert keeps its name (`Prompt` or `UUID` naming, or the `field:<x>` naming field) and the document comes earlier in the file; a later document, or the document itself, is a problem. When the file has such Links, the documents are written one at a time in file order (`--concurrency` is ignored, with a warning). Otherwise, with `--concurrency` above 1 the order of the writes is not guaranteed.

## Checking first

The whole file is read, converted and checked before the first write. When anything is wrong, every problem is listed with its row (the CSV line, the JSON item, the NDJSON line), column, value and reason, nothing is written, and the command exits 6:

```text
line 2, status "Pending": not one of the options: Open, Closed
line 3, items.item "I-Z": no Item named "I-Z" (or your User Permissions hide it from you)
2 problems in tickets.csv; nothing was written
```

With `--json` the problems go to stdout as `{"problems": [{row, column, value, reason}, ...]}`.

Header and invocation problems (unknown columns, `--mode update` without a name column, a refused format) are usage errors, exit 2.

## Insert

Each document is created with `POST /api/resource/<DocType>`, with its table rows. A table row that holds only a row name is left out.

- **Names.** Through the REST API Frappe keeps a given name only when the DocType's naming rule is `Prompt` or `UUID` (`set_new_name`); Frappe's own Data Import keeps it in more cases because it runs with an import flag. So ffc sends the name column only for those rules. With a `field:<x>` rule and no `<x>` column, the name goes into `<x>`. Otherwise the name column is ignored, with a warning, and Frappe names the documents. Table row names are never sent on insert.
- With names sent, two documents of the file with the same name (compared without case) are a problem.

## Update

Each document is read (`GET`), compared with the file, and written with `PUT` only when something differs; otherwise its status is `unchanged` (Frappe's importer would fail with "No changes to update").

- Every document needs a name; a name that appears twice is a problem.
- Only the fields whose value differs are sent. Numbers compare by value, Link names without case.
- A table that has rows for the document in the file replaces the document's rows. A row whose `items.name` is one of the document's rows keeps it: its name and the values the file leaves blank stay, and the file's values go on top. Other rows are new. Rows the file leaves out are removed, and the file's order becomes the order. A table with no rows for the document in the file is left alone, so a file cannot empty a table.
- When none of a document's rows in a table has a row name (no `items.name` column, or JSON rows without `"name"`) and the document has rows in that table on the site, that is a problem (nothing is written): the file's rows would replace the site's rows and every column the file does not have would go back to its default. Export the documents with `ffc export`, which writes `items.name`, and edit that file. Rows without a name are fine next to named rows (they are added), and for a document whose table is empty.
- The update carries the `modified` value that was read: when someone saved the document in between, it fails with "changed on the server" (exit 6 for that document) and nothing of it is saved.
- When the site saves a value other than the one sent (a field above your permission level is dropped silently by Frappe, a controller rewrites a value), the result carries a warning. Table rows are compared by position on the file's columns, for inserts too.

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

## --server: Frappe's Data Import

`--server` hands the file to Frappe's own importer (the desk's Data Import) instead of reading it on your machine. Use it for Excel files, for files whose values ffc's stricter conversion refuses, or for large files you do not want to send document by document. Frappe parses, converts and validates the file itself, with its own rules (for example, it truncates `2.5` in an Int column where ffc refuses it), and imports it in a background job.

```bash
ffc import -d Item items.xlsx --mode insert --server
ffc import -d Customer customers.csv --mode update --server --preview   # check only
ffc import --server --resume "Item Import on 2026-10-08 10:15:02.123456"  # start, or keep waiting
```

What happens:

1. ffc creates a **Data Import** document, named `<DocType> Import on <timestamp>` by Frappe (DocType, `Insert New Records` or `Update Existing Records`, `--submit` as "Submit After Import", `--mute-emails`). Frappe refuses a DocType without "Allow Import" and core DocTypes (exit 6).
2. It uploads the file as a private File attached to the Data Import (the site's upload limit applies, as for [`ffc upload`](files.md)) and saves it as the Data Import's file. Frappe parses it then: a file it cannot read as a template fails here (exit 6).
3. It reads Frappe's **preview**. Warnings of type "info" (a skipped column, for example) are printed as notes. Any other warning (an unknown Select value, a missing Link, a bad date) stops the import before it starts, as in the desk: every warning is printed with its row and column, and the command exits 6. With `--json` they go to stdout as `{"data_import", "doctype", "status": "Blocked", "warnings": [{row, col, type, message}]}`.
4. It **starts** the import (`form_start_import`), which Frappe queues on the site's `default` queue, and waits up to `--wait`, checking the status every second at first and then every 5 seconds. A spinner shows while it waits; without a terminal, each status change is a line on stderr.
5. It prints Frappe's **log** for each document: the file rows it came from, the document name, `created` / `updated` (or `failed`) and, for a failure, Frappe's message (or the last line of the traceback). `--json` gives `{"data_import", "doctype", "status", "success", "failed", "total", "rows": [{"rows": [2, 3], "name", "status", "message"}]}`.

Anything that fails before the import starts (the file, the preview's warnings) deletes the Data Import again; Frappe deletes its file with it. Once started, the Data Import stays on the site, as one made in the desk does.

**`--preview`** stops after step 3: it prints the number of file rows and documents Frappe found and the warnings, and leaves the Data Import in place (also when it has warnings, exit 6 then; `--json` reports its real status, `Pending`) with the command that starts it: `ffc import --server --resume <name>`, or, with warnings, the command that deletes it. **`--resume NAME`** reads that Data Import (`-d` may be given; it must match), and:

- when it was not started (status Pending), starts it, then waits. Frappe never queues the same Data Import twice, so resuming one that is already queued or running only waits for it;
- when it is running, waits for it;
- when it has finished, prints its log again; nothing is restarted (retry the failed rows from the desk);
- when Frappe stopped it on warnings found by the job, prints them (exit 6). ffc does not start it again: when the cause was data on the site (a missing Link target, say) and that is fixed now, start it from the desk, which clears the warnings and checks again.

`--resume` does not check who created the Data Import: any System Manager may start or read anyone's, as in the desk.

Ctrl+C stops the waiting, not the import: the job keeps running on the site, and ffc prints the `--resume` command.

`--resume` takes no file and none of `--mode`, `--submit`, `--mute-emails`, `--preview`, `--format`: the Data Import keeps what it was created with. `--dry-run` is refused with `--server`, because creating the Data Import is already a write; `--preview` is the server-side check. `--concurrency` and `--fail-fast` apply only without `--server`. ffc does not check the header or the values itself in this mode: Frappe's preview reports them. Frappe ignores `--submit` for a DocType that is not submittable.

Exit codes:

| Code | When |
| --- | --- |
| 0 | every document was imported |
| 8 | the run finished and some documents failed (also when all of them failed) |
| 6 | the file or its warnings stopped the import; the job ended `Error` or `Timed Out` before logging every document (Frappe records "Data import failed" in the Error Log: `ffc errors`); or the site's scheduler is inactive |
| 7 | `--wait` ran out: the job is still queued or running on the site. The message names the Data Import and the command to pick it up: `ffc import --server --resume <name>` |
| 5 | permission denied: Data Import needs the **System Manager** role, plus import permission on the DocType |

Requirements and caveats:

- **Scheduler and worker.** Frappe refuses to start a Data Import while the site's scheduler is inactive (disabled in System Settings, paused, or in maintenance mode), except in developer mode, where it runs the import inside the request (a long import may then need a larger `--timeout`). ffc then exits 6, keeps the Data Import, and says how to enable the scheduler (`bench --site <site> enable-scheduler`) and resume. A site with no worker on the `default` queue never runs the job: the wait runs out (exit 7).
- Before v16.51 a running import reads `Pending`, then `Partial Success` after its first document, until the end; ffc takes the run as over when every document has a log, or the status is `Success`, `Error` or `Timed Out`. From v16.51 a running import reads `In Progress`, and the import type `Insert or Update Records` exists (ffc does not create it; `--resume` reports such a Data Import).
- Frappe ends a run `Pending` when some documents got no log and either none failed or none succeeded (importer.py). Such a run never changes again, and ffc cannot tell it from one still running: the wait runs out (exit 7) with a message saying so.
- Frappe returns at most 5000 logs (1000 from v16.51); beyond that the rows are cut and the counts are the site's, with a warning.
