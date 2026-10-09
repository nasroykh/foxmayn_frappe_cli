# ERPNext helpers

Commands for sites that run ERPNext. They call the whitelisted methods ERPNext itself uses (the desk's "Create" buttons), so its validations, permissions and hooks apply.

```bash
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice"            # print the draft
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --create   # save it
ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --submit   # save and submit it
ffc erp payment --against "Sales Invoice:ACC-SINV-2026-00001"                       # print the Payment Entry draft
ffc erp payment --against "Sales Invoice:ACC-SINV-2026-00001" --submit              # save and submit it
ffc erp item ITEM-001 --company "Acme Inc" --customer CUST-0001                     # price, warehouse, accounts for a row
ffc erp stock ITEM-001 --warehouse "Stores - ACME"                                  # balance in one warehouse
ffc erp stock ITEM-001                                                              # stock in every warehouse
ffc erp party --customer CUST-0001 --company "Acme Inc"                             # defaults a document takes from a customer
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
| `--bank-account` | The bank or cash Account to pay from or into. Default: the Company's default bank account, else its default cash account. A Mode of Payment set on the document takes precedence (see below). |
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

**A Mode of Payment on the document overrides `--bank-account`.** When the invoice or order has a `mode_of_payment`, ERPNext uses that mode's account for the Company and ignores `bank_account`. After the draft comes back, `erp payment` compares the account on the bank side (`paid_to` for Receive, `paid_from` for Pay) with the one you gave and prints a warning on stderr naming both when they differ; the draft is still printed and `--create` still works.

**An invoice with nothing outstanding.** ERPNext checks only orders while it builds the draft, so an invoice that is already paid still comes back as a draft; whether `--create` is then refused is up to the server's own validation of the Payment Entry, whose error is reported as it comes.

### Permissions

As for `erp map`: the draft is built as your user, ffc never sends `ignore_permissions`, and you need the right to create Payment Entries and to read the document paid.

## Lookups: `erp item`, `erp stock`, `erp party`

Read-only lookups for what the desk fills in as you type. Each is a GET that writes nothing, runs as your user (ffc never sends `ignore_permissions`) and uses the ERPNext check above. Data output follows [`--output`/`--json`/`--jq`](output-formats.md) like the other commands; `--keys` keeps only the given keys. Numbers keep the literal Frappe sends: a Float arrives as `2.0`, and stays `2.0`.

### Item: `erp item ITEM`

What ERPNext fills into a document row when an item is picked: the rate (`price_list_rate`, `rate`, pricing rules), `uom`, `warehouse`, the income and expense accounts, the cost center, item taxes and, for stock items, the stock levels of the warehouse.

| Flag | Description |
| --- | --- |
| `--company` | The Company the row is for (required). |
| `--doctype` | The document the row is for. Default: Sales Invoice, or Purchase Invoice with `--supplier`. Sales: Quotation, Sales Order, Delivery Note, Sales Invoice. Purchase: Supplier Quotation, Purchase Order, Purchase Receipt, Purchase Invoice, Material Request. Any other is a usage error. |
| `--customer` | The Customer, for customer-specific prices. Needs a sales DocType. |
| `--supplier` | The Supplier. Needs a purchase DocType. Not together with `--customer`. |
| `--price-list` | The Price List to take the rate from. |
| `--qty` | The quantity (default 1): it matters for quantity-based prices and packing units. A positive number. |
| `--warehouse` | The warehouse to use instead of the Item's own default (sent as `set_warehouse`: the Item's, Item Group's and Brand's defaults outrank a plain `warehouse`). |
| `--date` | The transaction date, `YYYY-MM-DD` (default: today). |
| `--keys` | Keys to keep in the output, e.g. `item_code,rate`. |

The method is `erpnext.stock.get_item_details.get_item_details` on both majors, but its first parameter has a different name: `args` on ERPNext 15, `ctx` on 16 (there is no alias). ffc sends the dict under the right one. A plain call sends `company`, `doctype`, `item_code`, `qty` and both conversion rates as 1; everything else only when you give it.

**Currency.** ffc sends no currency and both conversion rates as 1 (ERPNext refuses a missing conversion rate with "Exchange Rate is mandatory"; with a rate of 1 and no currency it keeps 1 and makes no exchange-rate lookup). A price from a price list in another currency than the Company's is therefore not converted. For that, call the method yourself with [`ffc call-method`](server-calls.md).

### Stock: `erp stock ITEM`

One command, two ERPNext methods.

| Flag | Description |
| --- | --- |
| `--warehouse` | Show the balance in this Warehouse. |
| `--date` | The balance at the end of this day, `YYYY-MM-DD` (default: now). Needs `--warehouse`. |
| `--valuation` | Add the valuation rate. Needs `--warehouse`. |
| `--keys` | Keys to keep in the output, e.g. `warehouse,actual_qty`. |

**With `--warehouse`** it calls `erpnext.stock.utils.get_stock_balance` and prints one object: `item_code`, `warehouse`, `actual_qty`, plus `valuation_rate` with `--valuation` and `posting_date` with `--date`. A date means the end of that day (ffc sends the time 23:59:59), so everything posted on it counts. ERPNext checks only read access on Item for this call, not on the warehouse.

**Without `--warehouse`** it lists the item's stock per warehouse with `erpnext.stock.dashboard.item_dashboard.get_data`, the method behind the stock dashboard: one row per Bin that has any quantity or any reservation, order or plan for the item, ordered by warehouse, with `warehouse`, `actual_qty`, `reserved_qty`, `reserved_stock`, `projected_qty`, `valuation_rate` and `stock_uom`. The method answers 21 rows per call; ffc reads the pages for you, up to 24 pages (504 warehouses), and warns on stderr when it stopped with more rows left (no warning when there are exactly 504), in which case ask for the warehouse you want with `--warehouse`. The rows show the current state, so `--date` and `--valuation` need `--warehouse` (the rows carry `valuation_rate` anyway). ERPNext escapes the names in these rows as HTML (`R&D` arrives as `R&amp;D`); ffc undoes that, so a name can be passed back to `--warehouse`.

**Permissions differ by version.** ERPNext 16's dashboard answers an empty list unless you may read Bin. ERPNext 15's does not check Bin read permission at all: it lists with `get_all`, limited only by Warehouse user permissions. So on 15 a user who cannot open Bin in the desk can still see these quantities, and the result is not proof that they could. When the user has Warehouse user permissions, both versions restrict the rows to `frappe.get_list("Warehouse")`, which returns at most 20 warehouses (its default page length, upstream behaviour): on such a user the list can miss warehouses even though they are allowed.

### Party: `erp party`

What ERPNext fills into a document when a customer or supplier is picked (`erpnext.accounts.party.get_party_details`): the default address and contact, price list, currency, tax template and payment terms template, the sales team of a customer and, for an invoice, the receivable or payable account (`debit_to`, `credit_to`) and the `due_date`.

| Flag | Description |
| --- | --- |
| `--customer` | The Customer to look up. One of `--customer` and `--supplier` is required. |
| `--supplier` | The Supplier to look up. Not together with `--customer`. |
| `--company` | The Company the document is for. Optional, but accounts, taxes and payment terms depend on it. |
| `--date` | The posting date, `YYYY-MM-DD`: it sets the due date (default: today). |
| `--doctype` | The document the party is for. Sales Invoice and Purchase Invoice add the account and the due date. |
| `--keys` | Keys to keep in the output, e.g. `customer_name,currency`. |

The parameters are the same on ERPNext 15 and 16. Version 16 also accepts an `ignore_permissions` argument; ffc never sends it, so you need read access on the party.

### Errors

An unknown party, and an unknown item for `erp item`, are a 404 from ERPNext (exit 4). `get_stock_balance` and the stock dashboard do not fail for a name that does not exist: they answer 0 and an empty list. So `erp stock` first reads the Item, and the Warehouse when `--warehouse` is given, and a typo is a 404 (exit 4) instead of a quiet zero; a user who may not read Warehouses (403) skips that second check. Missing `--company` for `erp item`, `--customer` together with `--supplier`, a `--doctype` that does not fit, a bad `--date` or `--qty`, and `--date` or `--valuation` without `--warehouse` are usage errors (exit 2) answered before any request.

## See also

- [Lifecycle and workflow](lifecycle-and-workflow.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
- [Exit codes](exit-codes.md)
