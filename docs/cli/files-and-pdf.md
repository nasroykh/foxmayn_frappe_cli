# Files and PDF

Attach files to documents, list and download attachments, and save a document's print as PDF: `upload`, `attachments`, `download`, `pdf`.

```bash
ffc upload contract.pdf -d Customer -n "ACME Corp"                 # private, in Home/Attachments
ffc upload logo.png -d Item -n ITEM-001 --field image --public     # also sets Item.image
ffc attachments -d Customer -n "ACME Corp"
ffc download /private/files/contract.pdf                           # saved as ./contract.pdf
ffc pdf -d "Sales Invoice" -n SINV-0001 --format "Detailed Invoice" --no-letterhead -o inv.pdf
```

## Upload: `upload FILE`

Uploads a file and attaches it to a document, as the desk's Attach button does.

| Flag | Default | Description |
| --- | --- | --- |
| `-d, --doctype`, `-n, --name` | | The document to attach to (required; it must exist). |
| `--public` | private | Store under `/files`, readable by anyone with the URL. |
| `--folder` | `Home/Attachments` | File folder. |
| `--field` | | An Attach field of the document to set to the file's URL (a second request that updates the document). |
| `--filename` | FILE's name | File name on the site. Required when FILE is `-` (stdin). |
| `--dry-run` | | Show the request with the file's name and size, never its content. |

- Files are **private** unless `--public` (Frappe's own default is public). A private file is served from `/private/files`, only to users who may read the document.
- A file over the site's limit (System Settings > Max File Size, 25 MiB by default) is refused before anything is sent (exit 6). The site's allowed file extensions still apply.
- A file is streamed from disk. Stdin is read into memory first, up to 128 MiB.
- Uploads are never retried.

```bash
tar cz notes | ffc upload - --filename notes.tgz -d ToDo -n TD-0001
```

## List attachments: `attachments`

Lists the File documents attached to a document, oldest first: `name`, `file_name`, `file_url`, `is_private`, `file_size`, `attached_to_field`, `folder`, `creation`.

```bash
ffc attachments -d ToDo -n TD-0001
ffc attachments -d "Sales Invoice" -n SINV-0001 --jq '.[].file_url'
```

`-l/--limit` (default 100, `0` = no limit), `--all` and `--page-size` work as in `list-docs`.

## Download: `download FILE_URL`

```bash
ffc download /private/files/contract.pdf
ffc download /files/logo.png -o logo.png --force
ffc download "$(ffc attachments -d ToDo -n TD-0001 --jq '.[0].file_url')" -o - | sha256sum
```

| Flag | Description |
| --- | --- |
| `-o, --output-file` | Where to save. Default: the file's name in the current directory. `-` writes to stdout (binary is refused on a terminal). |
| `--force` | Replace an existing file. |

- FILE_URL is a File's `file_url` (`/files/...` or `/private/files/...`), or a full URL of the site itself (same scheme, host and port). Other hosts are refused, so the credentials never leave the site.
- The file is written to a temporary file and renamed into place, so an interrupted download leaves nothing behind.
- A missing private file and one you may not read both get Frappe's 403 (exit 5).
- Private files are saved with mode 0600.
- `--timeout` bounds the whole download, retries included.

## Save as PDF: `pdf`

Renders a document with a print format, like the desk's PDF button. Your user needs read or print access.

| Flag | Default | Description |
| --- | --- | --- |
| `-d, --doctype`, `-n, --name` | | The document (required). |
| `--format` | the DocType's default | Print Format. |
| `--letterhead` | the default Letter Head | Letter Head to use. |
| `--no-letterhead` | | Print without a letter head. |
| `--lang` | your user's language | Language code, e.g. `fr`. |
| `-o, --output-file` | `<name>.pdf` | Where to save. `-` writes to stdout, except on a terminal. |
| `--force` | | Replace an existing file. |

```bash
ffc pdf -d Quotation -n QTN-0001 --lang fr -o - | lpr
```

- A response that is not a PDF (an error page) is never saved.
- Frappe v16 renders only a few PDFs at a time and answers 503 when no slot frees within 10 seconds; ffc retries twice within `--timeout`. Raise `--timeout` for long documents.
- A site in Docker whose PDF renderer (wkhtmltopdf) cannot reach the site's own URL fails with a 500 `OSError`. That is a site setup problem, not an ffc one.

## See also

- [ffc api](api.md)
- [Collaboration](collaboration.md)
- [MCP tools](../mcp/tools.md)
