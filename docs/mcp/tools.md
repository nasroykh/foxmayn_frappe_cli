# MCP tools

The 38 tools the ffc MCP server offers, how tool sets select them, their limits, and the resources, prompts and completion that come with them.

## Tool sets

```bash
ffc mcp --toolsets core                    # documents, schema, reports, search, bulk, call_method, ...
ffc mcp --toolsets core,lifecycle,collab   # + comments, assignments, tags
ffc mcp --toolsets core,admin              # + sharing
ffc mcp --toolsets core,lifecycle,files    # + attachments and print HTML
```

The default is `core,lifecycle`. The `collab`, `admin` and `files` sets are exposed only when named. `list_sites` is always there. Like `--allow-tools`, `--toolsets` only narrows what the policy allows; an unknown set name is a usage error.

## Tools

R = read tool (kept by `--read-only`), W = writes, M = `call_method` (treated as a write).

### `core`

| Tool | Kind | What it does | CLI equivalent |
| --- | --- | --- | --- |
| `list_sites` | R | The served sites (name, URL, sign-in method, read-only). | `ffc site list` |
| `ping` | R | Check the connection. | `ffc ping` |
| `whoami` | R | Signed-in user, roles, installed apps. | `ffc whoami` |
| `check_permission` | R | May the user do X on a DocType or document? | `ffc can` |
| `get_doc` | R | One document. | `ffc get-doc` |
| `get_doc_context` | R | Versions, comments, attachments, assignments, shares, tags, links, timeline. | `ffc doc-info` (without `--onload`) |
| `list_docs` | R | List documents. | `ffc list-docs` |
| `count_docs` | R | Count documents. | `ffc count-docs` |
| `aggregate` | R | Counts and totals per group. | `ffc aggregate` |
| `get_schema` | R | Compact DocType schema (always fetched live, never from the cache). | `ffc get-schema` |
| `list_doctypes` | R | DocType names. | `ffc list-doctypes` |
| `list_reports` | R | Report names. | `ffc list-reports` |
| `run_report` | R | Run a report (at most 500 rows unless `limit` is given). | `ffc run-report` |
| `search` | R | Link-field search in one DocType, or global search. | `ffc search` |
| `create_doc` | W | Create a document. | `ffc create-doc` |
| `update_doc` | W | Update fields; `if_unmodified` fails if the document was saved since it was read. | `ffc update-doc` |
| `delete_doc` | W | Delete a document (asks for confirmation). | `ffc delete-doc` |
| `bulk_create`, `bulk_update`, `bulk_delete` | W | Up to 200 items per call; progress notifications; `bulk_delete` asks for confirmation. | `ffc bulk-*` |
| `call_method` | M | Call a whitelisted method; `full_response: true` returns the whole response object. | `ffc call-method` |

### `lifecycle`

| Tool | Kind | What it does |
| --- | --- | --- |
| `submit_doc` | W | Submit a draft. |
| `cancel_doc` | W | Cancel a submitted document (asks for confirmation). |
| `amend_doc` | W | Amend a cancelled document. |
| `copy_doc` | W | Duplicate a document. |
| `rename_doc` | W | Rename, or merge into another document (a merge asks for confirmation). |
| `apply_workflow` | W | Apply a workflow action (asks for confirmation: an action may submit or cancel). |
| `get_transitions` | R | Workflow actions available now. |

### `collab`

| Tool | Kind | What it does |
| --- | --- | --- |
| `add_comment` | W | Comment on a document. |
| `assign_to` | W | Assign users (asks for confirmation: Frappe may share the document with them). |
| `remove_assignment` | W | Remove assignments. |
| `add_tag`, `remove_tag` | W | Add or remove tags. |

### `admin`

| Tool | Kind | What it does |
| --- | --- | --- |
| `share_doc` | W | Share a document (asks for confirmation). Refused unless the site's `allow_doctypes` lists `DocShare`. |
| `unshare_doc` | W | Remove a share. Same `DocShare` rule. |

### `files`

| Tool | Kind | What it does |
| --- | --- | --- |
| `list_attachments` | R | Files attached to a document. |
| `get_print_html` | R | A document's print view as `{html, style}`, or `{text}` with `text_only`. Runs the DocType's `before_print` hook, as the desk's print view does, even on a read-only server. |
| `attach_file` | W | Attach a file: `data` in base64 (or `encoding: text`), at most 5 MiB decoded, private unless `is_private: false`. Refused unless the site's `allow_doctypes` lists `File`. |

No tool returns file contents or PDFs.

## Limits and result shapes

- **512 KiB per result.** A larger result is refused with a hint to narrow it (`limit`, `fields`, `filters`, `keys`), except rows:
  - `list_docs` returns the rows that fit as `{"data": [...], "truncated": true, "next_start": N, "hint": "..."}`; call again with `start: N` for the rest. A list that fits is still a plain array.
  - `run_report` drops rows from the end and adds `truncated`, `total_rows` and a `hint`.
  - `get_print_html` drops the style, then cuts the HTML (`truncated: true`).
- Tools whose results can be large tell the client the cap, so Claude Code does not cut the JSON.
- `get_doc_context` returns at most 50 comments, emails and workflow log entries, 100 attachments, assignments, shares and tags, 50 changes per version and 100 timeline entries; the rest is counted in `omitted`.
- `count_docs`, `whoami` and `list_sites` also return structured content with an output schema.
- `bulk_create`, `bulk_update` and `bulk_delete` send progress notifications when the call carries a progress token; cancelling the call stops new items.

## `jq` on read tools

Read tools whose results can be large (`get_doc`, `list_docs`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `search`, `get_doc_context`, `aggregate`, `list_attachments`, `get_print_html`) take a `jq` argument: a filter the server runs on the result before returning it, for example `"[.[] | {name, status}]"` or `".data | length"`.

The output is JSON (one output as is, none or several as an array) under the same 512 KiB cap. The filter runs in a separate process that sees only the result (no environment, no input, no modules) and is stopped after 5 seconds or 256 MiB.

`list_docs` and `run_report` also take `response_format`: `concise` (default) or `detailed` (every field for `list_docs` without `fields`; the report as Frappe returns it).

## Resources

Read-only JSON resources, served through the same tool handlers (so policy, audit and size cap apply):

| URI | Content |
| --- | --- |
| `ffc://sites` | The served sites (as `list_sites`). |
| `ffc://{site}/schema/{doctype}` | The compact schema (as `get_schema`). |
| `ffc://{site}/doc/{doctype}/{name}` | A document (as `get_doc`). Percent-encode each segment: `ffc://prod/doc/Sales%20Invoice/SINV%2F0001`. |

A template is offered only when its tool is. A failed read returns the tool's error message; read the message, not the JSON-RPC code.

## Prompts

Guidance only; they call nothing. Each lists which tools to call in which order and what to check, leaving out steps whose tools are not exposed.

| Prompt | Arguments |
| --- | --- |
| `inspect-doctype` | `doctype` |
| `safe-bulk-import` | `doctype`, optional `source` |
| `audit-doc-changes` | `doctype`, `name` |
| `explain-report` | `report_name` |

With several sites they take a required `site`. An argument longer than its limit (140 characters, 500 for `source`) is refused, never cut.

## Instructions and completion

On connecting, the client receives short instructions for the model: filter syntax, reading the schema before writing, `docstatus` and the lifecycle tools, `fields` and `limit` on lists, name versus title, which sites are read-only. They mention only the tools this server exposes.

The server answers completion requests for `{site}`, `{doctype}` and the prompts' arguments from the local cache the CLI fills (`ffc cache warm`). Without a fresh cache it offers nothing. It never offers what the site's policy would refuse, and never completes document names.

## See also

- [Safety](safety.md)
- [Running the server](running.md)
- [CLI reference](../cli/README.md)
