# MCP safety

Limit what an AI assistant can do on each site: read-only mode, per-site policy, built-in protections, confirmations and the audit log.

ffc is not an official Frappe project and is not affiliated with Frappe Technologies. The assistant acts with your Frappe user's permissions; these controls only ever narrow them.

## Read-only mode

```bash
ffc mcp --read-only --site prod
ffc mcp install --client claude-desktop --site prod --read-only
```

Only read tools are exposed: no create, update, delete, bulk, lifecycle, workflow, collaboration or `call_method`. You can also set it per site in the config (`mcp.read_only: true`), which applies however the server is started.

## Per-site policy

Each site can carry an `mcp` block in `config.yaml`:

```yaml
sites:
  prod:
    url: https://erp.example.com
    mcp:
      read_only: false
      allow_tools: [get_doc, list_docs, update_doc]   # only these tools are exposed
      allow_doctypes: [Sales Order, Customer]         # only these DocTypes
      deny_doctypes: [Salary Slip]
      allow_methods: [erpnext.selling.*]              # call_method; a trailing * is a prefix
      deny_methods: [frappe.client.delete]
      confirm: always                                 # always | if-supported (default) | never
```

| Key | Effect |
| --- | --- |
| `read_only` | Only read tools. |
| `allow_tools` | Only these tools are exposed. |
| `allow_doctypes` | Only these DocTypes may be read or written. Also lets MCP write a listed sensitive DocType (below). Requires `allow_methods` if `call_method` is to be usable, since a method can reach any DocType. |
| `deny_doctypes` | These DocTypes are refused. |
| `allow_methods` | `call_method` may call only these. Also unlocks a built-in denied method when listed. |
| `deny_methods` | `call_method` refuses these. |
| `confirm` | When to ask the user before destructive calls (see [Confirmations](#confirmations)). |

Rules:

- The policy is read again on every call, so a change that narrows it applies at once. Widening the tool list needs a server restart.
- A refused call sends nothing to the site and returns an error starting with `policy:` that names the setting to change.
- A misspelt key under `mcp:` is an error, and an `allow_` list that is present but empty is an error, so a policy is never silently ignored or read as "no limit".
- The command-line flags (`--allow-tools`, `--allow-doctypes`, `--deny-doctypes`, `--allow-methods`, `--deny-methods`, `--confirm`) only narrow the config. An MCP client's config cannot widen what the site's owner allowed.

How DocType rules reach other tools:

- `run_report` is checked through the report's `ref_doctype`.
- `search` with a DocType is checked like a read of it; a global search's hits are filtered to the allowed DocTypes and the answer reports how many were hidden (`hidden_by_policy`).
- `get_doc_context` empties the parts that come from DocTypes you do not allow (comments, versions, files, assignments, shares, linked DocTypes, timeline entries) and names them in `hidden_by_policy`.
- With any `allow_doctypes` or `deny_doctypes` rule, queries in `list_docs`, `count_docs`, `aggregate` and `call_method` may not reach another table: filter fields must be plain field names, and fields or `order_by` may not contain `.` or a backtick.
- The collaboration tools are also checked against the DocType they write: Comment, ToDo, Tag Link/Tag, DocShare.

## Built-in protections

These apply whatever the config says, unless the config explicitly lists the item:

- **Sensitive DocTypes** may be read but not written unless the site's `allow_doctypes` lists them:
  - Users and permissions: User, Role, Has Role, Role Profile, Module Profile, User Type, User Group, DocType, DocPerm, Custom DocPerm, User Permission, DocShare, Custom Field, Property Setter, Customize Form.
  - Settings and credentials: System Settings, OAuth Client, OAuth Provider Settings, OAuth Bearer Token, OAuth Authorization Code, Connected App, Token Cache, Social Login Key, LDAP Settings, Email Account.
  - Code and automation: Server Script, Client Script, Report, Print Format, Website Script, Web Page, Web Form, Custom HTML Block, Webhook, Notification, Auto Email Report, Assignment Rule, Energy Point Rule, Scheduled Job Type, System Console.
  - Bulk paths and files: Data Import, File.
- **Denied methods** in `call_method` unless `allow_methods` lists them: the System Console's `execute_code`, `generate_keys` (rotates a user's API keys), and the Frappe Cloud app installer (`frappe.integrations.frappe_providers.*`).
- `call_method` refuses method names containing `/` or spaces, and a `frappe.client` or form-save call that names no DocType.
- A comment posted through `call_method` must be authored by the signed-in user.

### Limits of these checks

- A custom server method can change any DocType without naming it in its arguments, so `deny_doctypes` and the sensitive list cannot bind it, and confirmation cannot catch it. For a hard limit, set `allow_doctypes` (with `allow_methods`), leave `call_method` out of `allow_tools`, or use `read_only`.
- A Query or Script Report can read tables other than its `ref_doctype`.
- `list_doctypes` and `list_reports` list names regardless of the DocType rules.

## Confirmations

Before these calls, ffc asks the user through the MCP client (elicitation), showing what will happen. Nothing is sent to the site unless the user confirms:

- `delete_doc`, `bulk_delete`, `cancel_doc`, `bulk_submit`, `bulk_cancel`, `rename_doc` with `merge`;
- `apply_workflow` (an action may submit or cancel);
- `share_doc` and `assign_to` (they can give users access to a document);
- the `call_method` equivalents (`frappe.client.delete` and `cancel`, `frappe.desk.reportview.delete_items`, the form cancel and discard methods, workflow apply methods, renames with merge, `frappe.share.add`, `frappe.share.set_permission` granting access, `frappe.desk.form.assign_to.add`).

A "no" returns `cancelled by the user; nothing was changed`.

| `confirm` | Behaviour |
| --- | --- |
| `if-supported` (default) | Ask when the client supports it; go ahead without asking when it does not. |
| `always` | Refuse the call when the client cannot ask, and name the `ffc` command to run in a terminal instead. |
| `never` | Never ask. |

`--confirm always` or `--confirm if-supported` can only tighten the site's setting. An answer is bound to the exact call (site, tool and arguments), expires after 10 minutes and works once. ffc trusts the client to show the question to a person; it cannot tell a person's answer from the client's.

## Audit log

Every tool call and resource read, allowed or refused, appends one JSON line to `mcp-audit.jsonl` next to the config file (`~/.config/ffc/mcp-audit.jsonl` by default, mode 0600). It records the time, site, the client's self-reported name, tool, DocTypes, document names (up to 20), status (`ok`, `error`, `denied`, `invalid`, `confirm_pending`, `declined`), the confirmation outcome, error and duration. Arguments are logged with secrets redacted, and document data and method arguments reduced to their keys and size. The file is rotated to `mcp-audit.jsonl.1` at 10 MiB.

```bash
tail -n 20 ~/.config/ffc/mcp-audit.jsonl | jq -c '{time, site, tool, status}'
```

## Checklist

- Start with `--read-only`, and add write tools only when needed.
- Use a dedicated Frappe user (API key) with only the roles the assistant needs.
- One site per connection; mark production `read_only` if you serve several.
- Keep `confirm` at `if-supported` or `always`.
- Review the audit log.

## See also

- [Tools](tools.md)
- [Running the server](running.md)
- [Security](../security.md)
