# Schema and cache

List DocTypes, inspect a DocType's fields, and manage the small local cache that speeds up schemas and shell completion: `list-doctypes`, `get-schema`, `cache`.

## List DocTypes: `list-doctypes`

```bash
ffc list-doctypes
ffc list-doctypes --module Accounts --limit 20
ffc list-doctypes --all --json
```

| Flag | Default | Description |
| --- | --- | --- |
| `-m, --module` | | Only DocTypes of this module. |
| `-l, --limit` | `50` | Maximum DocTypes; `0` for no limit. |
| `--all` | off | Fetch every row, page by page. |
| `--page-size` | `500` | Rows per request with `--all`. |

## Inspect a DocType: `get-schema`

```bash
ffc get-schema -d "Sales Invoice"                          # table of fields
ffc get-schema -d "Sales Invoice" --json                   # compact view
ffc get-schema -d "Sales Invoice" --json --keys fields     # only the field definitions
ffc get-schema -d "Sales Invoice" --json --full            # Frappe's complete answer
ffc get-schema -d "Sales Invoice" --refresh                # skip the local cache
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `--keys` | Top-level keys to keep, e.g. `name,module,fields` (JSON only). |
| `--full` | The complete, unfiltered response (JSON only). Always fetched from the site. |
| `--refresh` | Fetch the schema from the site instead of the cache. |

`--json` returns a compact view: meaningful DocType properties and field attributes, without zero values and internal metadata. Custom Fields and Property Setters (Customize Form changes) are merged in.

**Caching.** The compact schema is cached per site and login for one hour. A repeat call prints the same output without a request. A cache hit does not check your credentials or that the DocType still exists: a revoked key or a deleted DocType shows up only when the entry expires or with `--refresh`. Use `--refresh` right after a Customize Form change. `--debug` says when an answer came from the cache.

## The local cache

ffc keeps a small cache per site and per login in your user cache directory (`~/.cache/ffc` on Linux, `~/Library/Caches/ffc` on macOS, `%LOCALAPPDATA%\ffc` on Windows). Directories are 0700 and files 0600. Documents are never cached, and no secret is stored there.

| Entry | Kept | Filled by |
| --- | --- | --- |
| Installed apps and versions | 24 h | `whoami` |
| DocType list | 24 h | `list-doctypes` returning the whole list (no `--module`; `--all`, `--limit 0`, or fewer rows than the limit), `cache warm` |
| Report list | 24 h | `list-reports`, same rule, `cache warm` |
| DocType schemas (compact, at most 100, least recently used evicted) | 1 h | `get-schema`, `cache warm --doctypes` |

Shell completion and MCP completion read the cache but never fill it and never send a request. A changed site URL, or another login on the same site, never sees what was cached before. Removing, renaming or editing a site, or adding a site over an existing name, deletes that site's cache.

### `ffc cache`

```bash
ffc cache warm                                       # DocType and report lists (2 requests)
ffc cache warm --doctypes "Sales Invoice,Customer"   # plus those schemas (3 requests each)
ffc cache status                                     # entries, age, size, fresh or stale
ffc cache clear                                      # the selected site, every login
ffc cache clear --all-sites
```

A failed part of `cache warm` (the report list, a schema) is listed in `errors` and makes it exit 8; what did come back is cached. A failed DocType list fails the whole command.

## See also

- [Completion](completion.md)
- [Documents](documents.md)
- [Identity and permissions](identity-and-permissions.md)
