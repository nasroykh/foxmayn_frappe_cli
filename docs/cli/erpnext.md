# ERPNext helpers

Commands for sites that run ERPNext. They call the whitelisted methods ERPNext itself uses (the desk's "Create" buttons), so its validations, permissions and hooks apply.

```bash
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice"            # print the draft
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --create   # save it
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --submit   # save and submit it
ffc erp payment --against "Sales Invoice:ACC-SINV-2026-00001"                       # print the Payment Entry draft
ffc erp payment --against "Sales Invoice:ACC-SINV-2026-00001" --submit              # save and submit it
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

- **Default: print the draft, write nothing.** The mapping is a GET. The result is the unsaved document as ERPNext builds it: it has no name yet and carries `__islocal`; use `--json` to pipe it.
- **`--create`** removes the keys the form adds (`__islocal`, `__temporary_name`, ... on the document and on its child rows), then inserts it as a draft; the site names it. The output is the saved document, and the table view names it.
- **`--submit`** does the same, then submits the new document like [`submit-doc`](lifecycle-and-workflow.md#submit-submit-doc). A DocType with an active Workflow is refused (exit 6) before anything is written; use `--create`, then [`ffc workflow apply`](lifecycle-and-workflow.md#workflows-ffc-workflow). If the insert worked but the submit failed, the created document is printed as with `--create` (so a script can pick up its name), and the error, which names it too, sets the exit code.
- **`--dry-run`** still runs the mapping (it is a read) and shows the insert, and with `--submit` the submit, as plans. Nothing is sent.

### Quotations for a Lead

For a Quotation made out to a Lead or Prospect, ERPNext reuses the Customer that points back at it (`lead_name`, `prospect_name`) and otherwise creates one while it maps. Over a read that creation would be undone, and a Sales Order needs a real Customer, so `erp map` refuses a Quotation whose lead or prospect has no Customer yet (exit 6): convert the lead to a customer first, then map. When your user may not read Customers, it refuses too.

### Permissions

The mapping runs as your user. ffc never sends `ignore_permissions`, which some of these methods accept: you need read access on the source and create access on the target, and the insert and submit are checked like any other.

## Payment: `erp payment`

Makes a Payment Entry against an invoice or an order, as the desk's Create > Payment button does: ERPNext picks the party account, the bank or cash account, the payment type (Receive for a sale, Pay for a purchase) and the amount still outstanding, and fills the reference row that links the entry to the document.

| Flag | Description |
| --- | --- |
| `--against` | The document to pay, as `"DocType:name"` (required). |
| `--amount` | Pay this much instead of the outstanding amount. A positive number. |
| `--bank-account` | The bank or cash Account to pay from or into. Default: the Company's default bank account, else its default cash account. |
| `--reference-date` | The reference date of the payment, `YYYY-MM-DD`. Default: today. |
| `--create` | Save the draft as a new Payment Entry. |
| `--submit` | Save and submit it. Implies `--create`. |
| `--keys` | Keys to keep in the output, e.g. `name,docstatus`. |
| `--dry-run` | Show the writes instead of sending them. |

`--against` accepts a Sales Invoice, Sales Order, Purchase Invoice, Purchase Order or Dunning (the same on ERPNext 15 and 16). Any other DocType is a usage error (exit 2) that lists these. Only the options you give are sent to ERPNext, which uses its own defaults for the rest.

It works like [`erp map`](#what-it-does): by default the draft is printed and nothing is written (the call is a GET); `--create` removes the keys the form adds and inserts it as a draft; `--submit` also submits it, after refusing a Payment Entry Workflow (exit 6) before anything is written; a submit that fails after the insert prints the created entry and keeps the exit code of the error; `--dry-run` runs the read and shows the insert, and with `--submit` the submit, as plans.

### Errors

ERPNext's refusals reach the usual [exit codes](exit-codes.md):

| Situation | Exit |
| --- | --- |
| A Sales Order or Purchase Order that is already fully billed ("Can only make payment against unbilled ..."), a blocked Supplier (validation, HTTP 417) | 6 |
| No right to create Payment Entries or to read the document | 5 |
| The document does not exist, or ERPNext is not installed | 4 |
| No bank or cash account for the Company (below) | 6 |

**No bank or cash account.** When the Company has no default bank or cash account and its chart of accounts has not exactly one account of either type, ERPNext does not throw: it answers a draft whose `paid_from` or `paid_to` is empty. `erp payment` prints that draft with a warning on stderr; with `--create` or `--submit` it refuses (exit 6) before writing anything. Set the Company's default bank or cash account, or pass `--bank-account`.

**An invoice with nothing outstanding.** ERPNext checks only orders while it builds the draft, so an invoice that is already paid still comes back as a draft; whether `--create` is then refused is up to the server's own validation of the Payment Entry, whose error is reported as it comes.

### Permissions

As for `erp map`: the draft is built as your user, ffc never sends `ignore_permissions`, and you need the right to create Payment Entries and to read the document paid.

## See also

- [Lifecycle and workflow](lifecycle-and-workflow.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
- [Exit codes](exit-codes.md)
