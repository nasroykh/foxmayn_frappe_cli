# Dry runs and debugging

Preview what a write would send with `--dry-run`, and trace every HTTP request with `--debug`.

## `--dry-run`

```bash
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --dry-run
ffc bulk-delete -d Note --file names.json --dry-run --json
```

Every command that writes takes `--dry-run`: `create-doc`, `update-doc`, `edit-doc`, `delete-doc`, the bulk commands, `import`, the lifecycle commands, `erp map` (with `--create` or `--submit`), `workflow apply` and `bulk-apply`, the collaboration commands, `upload`, `call-method` and `api`.

- It prints the request it would send, with secrets redacted, sends nothing that writes, and exits 0. No confirmation is asked.
- Reads still run, so a dry run fails where the real run would (a missing document, a draft that cannot be amended).
- `update-doc --dry-run` shows which fields would change; `delete-doc --dry-run` checks that the document exists; `upload --dry-run` shows the file name and size, never the content.
- `call-method` and `api` send nothing at all, since any request to a method can write.
- A username/password site still logs in and out, and an expired OAuth token is still refreshed, so the reads can run.
- With `--json` the output is `{"dry_run": true, "requests": [{method, url, body}, ...]}`.

The MCP server never dry-runs.

## `--debug`

```bash
ffc get-doc -d ToDo -n nope --debug
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --debug=body
FFC_DEBUG=basic ffc list-docs -d ToDo
```

`--debug` writes one line per HTTP exchange to stderr: method, URL, status, Frappe exception type, time and sizes.

```text
debug #2 GET https://erp.example.com/api/resource/ToDo/nope → 404 DoesNotExistError (91ms, sent 0 B, received 316 B)
```

`--debug=body` adds headers and bodies (the first 64 KiB of each). Write `--debug=body`, not `--debug body`.

Credentials never appear in the trace: `Authorization`, cookies and session IDs, API keys, and any password, secret, token, OAuth code or verifier in a URL, header or body are shown as `***`, also inside JSON passed as a string. The trace goes to stderr only, so it is safe with `--json` and with `ffc mcp`; a detached MCP server writes it to `mcp.log`.

The spinner is off while tracing. `get-schema` says when an answer came from the local cache.

## See also

- [Exit codes](exit-codes.md)
- [Troubleshooting](../troubleshooting.md)
- [Security](../security.md)
