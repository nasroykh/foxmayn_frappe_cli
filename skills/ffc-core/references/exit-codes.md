# Exit codes

Part of ffc's CLI contract (same table in `ffc --help`). With `--json`, `--output json` or `--output ndjson`, stderr carries `{"error":{"code","exit_code","status","exc_type","message"}}`.

| Exit | `code` | Meaning | Typical cause and next step |
| --- | --- | --- | --- |
| 0 | `ok` | Success | Also: a `--dry-run` that printed its plan |
| 1 | `error` | Anything not classified below | A failed `ffc doctor` check; a declined or aborted prompt ("aborted"). Read the message |
| 2 | `usage` | Bad invocation | Unknown flag, bad flag value, invalid `--filters` JSON, a prompt needed without a terminal ("pass --yes"), a child table given to `ffc can`. Fix the command; nothing was sent |
| 3 | `auth` | Credentials refused | 401, expired session, `ping` or `whoami` seeing Guest. Re-run setup (ffc-setup skill) |
| 4 | `not_found` | Document or DocType missing | 404, or a DocType that does not exist. Check the name with `search` or `list-doctypes` |
| 5 | `permission` | Not allowed | 403; `ffc can` answering "denied" (the result is still printed); an aggregate field above the user's permission level |
| 6 | `validation` | Rejected by validation or state | 400/409/417/422/413, `TimestampMismatchError` (someone saved first), wrong document state (submitting a submitted document, workflow DocType), a file over the site's limit, `discard-doc` on Frappe before v16 |
| 7 | `server` / `network` | Server error or no response | 5xx or 429 (`server`), timeout, DNS, TLS, refused connection (`network`). Retry later or raise `--timeout` |
| 8 | `partial` | Bulk run partly failed | Some items `error`, `interrupted` or `skipped`. Read the per-item results |
| 130 | `interrupted` | Ctrl+C / SIGTERM | Writes in flight may or may not have been applied: check before re-running |

Notes:

- GET requests are retried on 429, 502, 503, 504 and transport errors; writes are never retried.
- A 401 on an OAuth site triggers one token refresh and one repeat of the request.
- Scripts: `ffc can -d Customer --perm delete && ffc delete-doc -d Customer -n CUST-1 --yes` runs the delete only when allowed.
