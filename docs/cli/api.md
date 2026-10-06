# ffc api

Send an authenticated request to any path of the site and print the response body: endpoints ffc has no command for, desk methods that return more than `message`, and file downloads.

```bash
ffc api /api/method/frappe.desk.form.load.getdoc -f doctype=ToDo -f name=TD-0001
ffc api /api/resource/Currency --paginate -f 'fields=["name","enabled"]'
ffc api POST /api/method/frappe.client.set_value -f doctype=ToDo -f name=TD-0001 -f fieldname=status -f value=Closed
ffc api /private/files/contract.pdf --output-file contract.pdf
echo '{"description":"x"}' | ffc api POST /api/resource/ToDo --input -
ffc api DELETE /api/resource/ToDo/TD-0001
```

Usage: `ffc api [METHOD] PATH [flags]`

| Flag | Description |
| --- | --- |
| `-f, --raw-field key=value` | Add a string field (repeatable). |
| `-F, --field key=value` | Add a typed field: `true`, `false`, `null`, a number, a JSON object or array, or `@FILE` / `@-` for file or stdin content (repeatable). |
| `-H, --header "Name: value"` | Add a request header (repeatable). |
| `--input FILE` | Send FILE (`-` for stdin) as the request body. |
| `--paginate` | Fetch every page of a `/api/resource/<DocType>` or `/api/v2/document/<DocType>` list. |
| `--output-file FILE` | Save the body to a file (for binary downloads). |
| `-i, --include` | Print the status line and headers to stderr (cookie values hidden). |
| `--silent` | Print no body. |
| `--dry-run` | Show the request; send nothing. |

## Rules

- **Paths only.** `PATH` is relative to the site URL (`/api/resource/...`, `/api/method/...`, `/api/v2/...`, `/private/files/...`). Full and protocol-relative URLs are refused, so the credentials never go to another host.
- **Protected headers.** `-H` cannot set `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name` or `X-Forwarded-Host`; the last three would pick another site on a multi-tenant bench.
- **Method.** The default is GET, or POST with `--input`. Unlike `gh api`, fields alone do not switch to POST: on `/api/resource` that would create a document. Name the method to write.
- **Fields.** For GET and HEAD, fields go in the query string; for other methods, in a JSON body. With `--input`, the body is the file and the fields go in the query string.

## Output

- A pipe or file gets the body bytes unchanged.
- A terminal gets indented JSON or cleaned text. A binary body is refused on a terminal: use `--output-file` or redirect stdout.
- `--jq` and an explicit `--output` render a JSON body (read into memory first). `--json` or `FFC_OUTPUT` alone leave the body unchanged.
- A status of 400 or more prints the body (with `--json`, only the error JSON on stderr) and exits with the matching [exit code](exit-codes.md).

## Pagination

`--paginate` requests pages of 500 rows by offset and prints `{"data": [...all rows]}`. Pass an `order_by` field when the list may change during the run.

## Limits

The request is never retried, and `--timeout` bounds the whole download.

## See also

- [Reports and methods](server-calls.md)
- [Files and PDF](files-and-pdf.md)
- [Output formats](output-formats.md)
