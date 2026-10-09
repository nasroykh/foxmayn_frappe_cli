# ERPNext helpers

Commands for sites that run ERPNext. They call the whitelisted methods ERPNext itself uses (the desk's "Create" buttons), so its validations, permissions and hooks apply.

```bash
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice"            # print the draft
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --create   # save it
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --submit   # save and submit it
```

They need ERPNext 15 or 16. On a site without ERPNext they exit 4 ("ERPNext is not installed on <site>"); on another major they exit 1 and point to [`ffc api`](api.md) and [`call-method`](server-calls.md), which reach any method.

## Map: `erp map`

Turns a document into a document of the next DocType, as the desk's Create menu does: items, taxes, addresses and links to the source are carried over by ERPNext.

| Flag | Description |
| --- | --- |
| `--from` | The source, as `"DocType:name"` (required). |
| `--to` | The DocType to map into (required). |
| `--create` | Save the draft as a new document. |
| `--submit` | Save and submit it. Implies `--create`. |
| `--keys` | Keys to keep in the output, e.g. `name,docstatus`. |
| `--dry-run` | Show the writes instead of sending them. |

Supported pairs (the same on ERPNext 15 and 16):

| From | To |
| --- | --- |
| Quotation | Sales Order |
| Sales Order | Sales Invoice, Delivery Note |
| Delivery Note | Sales Invoice |
| Purchase Order | Purchase Receipt, Purchase Invoice |
| Purchase Receipt | Purchase Invoice |
| Material Request | Purchase Order |

Any other pair is a usage error (exit 2) that lists these. Sales Order to Purchase Order is left out: ERPNext answers it with one document per supplier.

### What it does

- **Default: print the draft, write nothing.** The mapping is a GET. The result is the unsaved document as ERPNext builds it (it has `__islocal` and a placeholder name); use `--json` to pipe it.
- **`--create`** removes the keys the form adds (`__islocal`, `__unsaved`, ... on the document and on its child rows) and a name that is empty or local, then inserts it as a draft. The output is the saved document, and the table view names it.
- **`--submit`** does the same, then submits the new document like [`submit-doc`](lifecycle-and-workflow.md#submit-submit-doc). A DocType with an active Workflow is refused (exit 6) before anything is written; use `--create`, then [`ffc workflow apply`](lifecycle-and-workflow.md#workflows-ffc-workflow). If the insert worked but the submit failed, the error names the document that was created.
- **`--dry-run`** still runs the mapping (it is a read) and shows the insert, and with `--submit` the submit, as plans. Nothing is sent.

### Quotations for a Lead

ERPNext creates the Customer while it maps a Quotation made out to a Lead or Prospect. Over a read that would be undone, and a Sales Order needs a real Customer, so `erp map` refuses such a Quotation (exit 6): convert the lead to a customer first, then map.

### Permissions

The mapping runs as your user. ffc never sends `ignore_permissions`, which some of these methods accept: you need read access on the source and create access on the target, and the insert and submit are checked like any other.

## See also

- [Lifecycle and workflow](lifecycle-and-workflow.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
- [Exit codes](exit-codes.md)
