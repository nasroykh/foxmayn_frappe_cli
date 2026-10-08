# Exit codes

What each ffc exit code means, so scripts can react to the kind of failure.

| Code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | Other error: config file problem, unexpected response, a declined confirmation, a failed `ffc doctor` check. |
| 2 | Usage: unknown command or flag, wrong arguments, invalid flag value, or input needed but no terminal (for example a missing `--yes`). |
| 3 | Authentication: rejected credentials (401), failed login, or `ping`/`whoami` finding that the site treats the credentials as Guest. |
| 4 | Not found (404), including a DocType that does not exist. |
| 5 | Permission denied (403), or `ffc can` found the permission is not held. |
| 6 | Validation or conflict: Frappe validation errors (417, such as `ValidationError` or `LinkExistsError`), duplicates (409), 400, 422, 413, a document changed since you read it (`TimestampMismatchError`), or a document in the wrong state for the command. |
| 7 | Network or server: no connection, timeout, 429, 5xx; also a background job still running when `--wait` ran out (`run-report --prepared`, `import --server`). |
| 8 | Partial failure: some items of a bulk command (or `workflow bulk-apply`, documents of `import`, or parts of `cache warm`) did not succeed. |
| 130 | Interrupted (Ctrl+C). |

Before v1.7.0 every failure exited with 1. Scripts that test "non-zero" are unaffected; scripts that test `-eq 1` should test `-ne 0` instead.

```bash
ffc get-doc -d Customer -n "Acme" --json > acme.json
case $? in
  0) echo found ;;
  4) echo "no such customer" ;;
  3) echo "check your credentials: ffc doctor" ;;
  *) echo "failed" ;;
esac
```

## JSON errors

With `--json` (or `--output json`/`ndjson`), an error is printed on stderr as one JSON object, and stdout carries no data:

```json
{"error":{"code":"not_found","exit_code":4,"status":404,"exc_type":"DoesNotExistError","message":"ToDo \"x\" not found (404) — ToDo x not found (HTTP 404)"}}
```

`code` is one of `usage`, `auth`, `not_found`, `permission`, `validation`, `network`, `server`, `partial`, `interrupted`, `error`. `status` and `exc_type` are present when the site answered.

## See also

- [Output formats](output-formats.md)
- [Troubleshooting](../troubleshooting.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
