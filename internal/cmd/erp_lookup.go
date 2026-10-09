package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// erp item, stock and party flags
var (
	eiCompany, eiDoctype, eiCustomer, eiSupplier string
	eiPriceList, eiQty, eiWarehouse, eiDate      string
	eiKeys                                       string

	esWarehouse, esDate, esKeys string
	esValuation                 bool

	eyCustomer, eySupplier, eyCompany string
	eyDate, eyDoctype, eyKeys         string
)

// erpLookupMethods returns the lookup methods of the site's ERPNext, or the
// error that ends an 'ffc erp' command (exit 4 without ERPNext, exit 1 on a
// major ffc has not checked) or fails an MCP erp tool.
func erpLookupMethods(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, in erpInput) (client.LookupMethods, error) {
	info, _, err := serverInfo(ctx, c, cfg, false)
	if err != nil {
		return client.LookupMethods{}, err
	}
	major, err := erpNextMajor(info, cfg, in)
	if err != nil {
		return client.LookupMethods{}, err
	}
	m, ok := client.LookupFor(major)
	if !ok {
		return client.LookupMethods{}, fmt.Errorf("ffc erp has no lookups for ERPNext %d", major)
	}
	return m, nil
}

// lookupItemArg checks the ITEM argument.
func lookupItemArg(args []string) (string, error) {
	return checkItemCode(args[0])
}

// runERPItem looks an item up the way the desk fills a document row.
func runERPItem(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, in erpInput, o client.ItemOptions) (map[string]interface{}, error) {
	m, err := erpLookupMethods(ctx, c, cfg, in)
	if err != nil {
		return nil, err
	}
	return c.ItemDetails(ctx, m, o)
}

// stockResult is the outcome of a stock lookup: doc, the balance in one
// warehouse, or rows, one per warehouse (truncated: more could be read).
type stockResult struct {
	doc       map[string]interface{}
	rows      []map[string]interface{}
	truncated bool
}

// runERPStock reads an item's stock in one warehouse or in every warehouse.
// The Item, and the Warehouse when given, are read first so a typo is a
// not-found error, not a zero.
func runERPStock(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, in erpInput, q stockQuery) (*stockResult, error) {
	m, err := erpLookupMethods(ctx, c, cfg, in)
	if err != nil {
		return nil, err
	}
	if err := stockTargetsExist(ctx, c, q.item, q.warehouse); err != nil {
		return nil, err
	}
	if q.warehouse != "" {
		doc, err := c.StockBalance(ctx, m, q.item, q.warehouse, q.date, q.valuation)
		return &stockResult{doc: doc}, err
	}
	rows, truncated, err := c.StockByWarehouse(ctx, m, q.item)
	return &stockResult{rows: rows, truncated: truncated}, err
}

// stockTruncatedNote says that the per-warehouse rows stop short.
func stockTruncatedNote(in erpInput) string {
	return fmt.Sprintf("stopped after %d warehouses; there may be more: ask for one with %s", client.StockPageSize*client.StockMaxPages, in.name("warehouse"))
}

// runERPParty looks up the defaults a customer or supplier gives a document.
func runERPParty(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, in erpInput, o client.PartyOptions) (map[string]interface{}, error) {
	m, err := erpLookupMethods(ctx, c, cfg, in)
	if err != nil {
		return nil, err
	}
	return c.PartyDetails(ctx, m, o)
}

// printLookup prints one document as data, or as a field table.
func printLookup(doc map[string]interface{}, keys string) error {
	if machineOutput() {
		return printResult(selectKeys(doc, keys))
	}
	output.PrintDocTable(doc, nil)
	return nil
}

var erpItemCmd = &cobra.Command{
	Use:   "item ITEM",
	Short: "Look up an item's price, warehouse and accounts for a document",
	Long: `Look up what ERPNext fills into a document row when an item is picked, as the
desk does: the rate (price list rate, pricing rules), UOM, warehouse, accounts,
taxes and stock levels. Nothing is written (the call is a GET).

--company is required. --doctype is the document the row is for; it defaults to
Sales Invoice, or Purchase Invoice with --supplier. Sales DocTypes: ` + strings.Join(client.ItemSalesDoctypes, ", ") + `.
Purchase DocTypes: ` + strings.Join(client.ItemPurchaseDoctypes, ", ") + `.

--customer needs a sales DocType and --supplier a purchase one, and not both.
--qty (default 1) matters for quantity-based price lists and packing units.
--warehouse overrides the warehouse the Item's own defaults would give.

Conversion rates are sent as 1, so a price in another currency than the
company's is not converted.

Examples:
  ffc erp item ITEM-001 --company "Acme Inc"
  ffc erp item ITEM-001 --company "Acme Inc" --customer CUST-0001 --price-list "Standard Selling" --qty 5
  ffc erp item ITEM-001 --company "Acme Inc" --supplier SUP-0001 --keys item_code,rate,warehouse
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := lookupItemArg(args)
		if err != nil {
			return err
		}
		o, err := itemOptions(cmd, item)
		if err != nil {
			return err
		}
		doc, err := callSiteCfg(cmd, fmt.Sprintf("Looking up %s…", item), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (map[string]interface{}, error) {
			return runERPItem(ctx, c, cfg, erpInput{}, o)
		})
		if err != nil {
			return err
		}
		return printLookup(doc, eiKeys)
	},
}

// itemOptions reads and checks the flags of 'erp item'.
func itemOptions(cmd *cobra.Command, item string) (client.ItemOptions, error) {
	return buildItemOptions(erpInput{}, item, itemInput{
		company: eiCompany, doctype: flagOpt(cmd, "doctype", eiDoctype), customer: flagOpt(cmd, "customer", eiCustomer),
		supplier: flagOpt(cmd, "supplier", eiSupplier), priceList: flagOpt(cmd, "price-list", eiPriceList),
		qty: flagOpt(cmd, "qty", eiQty), warehouse: flagOpt(cmd, "warehouse", eiWarehouse), date: flagOpt(cmd, "date", eiDate),
	})
}

var erpStockCmd = &cobra.Command{
	Use:   "stock ITEM",
	Short: "Show an item's stock in one warehouse or in every warehouse",
	Long: `Show an item's stock.

With --warehouse: the balance in that warehouse (get_stock_balance), as of
now, or with --date as of the end of that day. --valuation adds the valuation
rate. ERPNext checks only read access on Item for this call, not on the
warehouse.

Without --warehouse: one row per warehouse that holds any quantity or has
any reservation, order or plan for the item (the stock dashboard's
item_dashboard.get_data), ordered by warehouse: actual, reserved, projected
quantity, valuation rate. ffc reads its pages of 21 rows for you, up to 504
rows (a warning says when it stopped with more to read). --date and --valuation need
--warehouse; the rows show the current state and carry the valuation rate.
On ERPNext 15 this call does not check Bin read permission (it lists with
get_all, limited only by Warehouse user permissions); ERPNext 16 returns
nothing without it. Names in the rows are shown as they are, not as HTML.

Quantities keep the number Frappe sends (2.0 stays 2.0). ERPNext answers 0
(or no rows) for an item or warehouse that does not exist, so ffc reads the
Item, and the Warehouse when given, first: a typo is a not-found error (exit
4), not a zero.

Examples:
  ffc erp stock ITEM-001
  ffc erp stock ITEM-001 --warehouse "Stores - ACME"
  ffc erp stock ITEM-001 --warehouse "Stores - ACME" --date 2026-09-30 --valuation
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := lookupItemArg(args)
		if err != nil {
			return err
		}
		q, err := buildStockQuery(erpInput{}, item, flagOpt(cmd, "warehouse", esWarehouse), flagOpt(cmd, "date", esDate), esValuation)
		if err != nil {
			return err
		}
		res, err := callSiteCfg(cmd, fmt.Sprintf("Reading the stock of %s…", item), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (*stockResult, error) {
			return runERPStock(ctx, c, cfg, erpInput{}, q)
		})
		if err != nil {
			return err
		}
		if res.doc != nil {
			return printLookup(res.doc, esKeys)
		}
		if res.truncated {
			fmt.Fprintln(os.Stderr, "warning: "+stockTruncatedNote(erpInput{}))
		}
		return printStockRows(res.rows, esKeys)
	},
}

// stockTargetsExist reads the Item, and the Warehouse when one is given, so a
// typo is a 404 (exit 4): get_stock_balance answers 0 and the item dashboard
// an empty list for a name that does not exist (utils.py:98-143). A user who
// may not read Warehouses (403) is not blocked: the balance call does not
// need that right, so the warehouse is left unchecked.
func stockTargetsExist(ctx context.Context, c *client.FrappeClient, item, warehouse string) error {
	if _, err := c.GetDoc(ctx, "Item", item); err != nil {
		return err
	}
	if warehouse == "" {
		return nil
	}
	_, err := c.GetDoc(ctx, "Warehouse", warehouse)
	var api *client.APIError
	if errors.As(err, &api) && api.Status == http.StatusForbidden {
		return nil
	}
	return err
}

// stockColumns are the table columns of the per-warehouse view.
var stockColumns = []string{"warehouse", "actual_qty", "reserved_qty", "reserved_stock", "projected_qty", "valuation_rate", "stock_uom"}

// printStockRows prints the per-warehouse rows as data (--keys keeps only
// the given keys of each row) or as a table.
func printStockRows(rows []map[string]interface{}, keys string) error {
	if !machineOutput() {
		output.PrintTable(rows, stockColumns)
		return nil
	}
	if keys == "" {
		return printResult(rows)
	}
	want := splitCSV(keys)
	out := make([]map[string]interface{}, len(rows))
	seen := map[string]bool{}
	for i, r := range rows {
		var missing []string
		out[i], missing = filterKeys(r, want)
		for _, k := range want {
			if !slices.Contains(missing, k) {
				seen[k] = true
			}
		}
	}
	if len(rows) > 0 {
		var absent []string
		for _, k := range want {
			if !seen[k] {
				absent = append(absent, k)
			}
		}
		if len(absent) > 0 {
			fmt.Fprintf(os.Stderr, "warning: --keys: not present in the result: %s\n", strings.Join(absent, ", "))
		}
	}
	return printResult(out)
}

var erpPartyCmd = &cobra.Command{
	Use:   "party",
	Short: "Look up a customer's or supplier's defaults for a document",
	Long: `Look up what ERPNext fills into a document when a customer or supplier is
picked, as the desk does: address and contact, price list, currency, taxes
template, payment terms and, with an invoice --doctype (Sales Invoice,
Purchase Invoice), the receivable or payable account and the due date.
Nothing is written (the call is a GET).

Give --customer or --supplier, not both. --company is optional, but the
accounts, taxes and payment terms depend on it. --date is the posting date
(it sets the due date).

Examples:
  ffc erp party --customer CUST-0001 --company "Acme Inc"
  ffc erp party --supplier SUP-0001 --company "Acme Inc" --doctype "Purchase Invoice" --date 2026-10-09
  ffc erp party --customer CUST-0001 --company "Acme Inc" --keys customer_name,payment_terms_template
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := partyOptions(cmd)
		if err != nil {
			return err
		}
		doc, err := callSiteCfg(cmd, fmt.Sprintf("Looking up %s…", o.Party), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (map[string]interface{}, error) {
			return runERPParty(ctx, c, cfg, erpInput{}, o)
		})
		if err != nil {
			return err
		}
		return printLookup(doc, eyKeys)
	},
}

// partyOptions reads and checks the flags of 'erp party'.
func partyOptions(cmd *cobra.Command) (client.PartyOptions, error) {
	return buildPartyOptions(erpInput{}, flagOpt(cmd, "customer", eyCustomer), flagOpt(cmd, "supplier", eySupplier),
		flagOpt(cmd, "company", eyCompany), flagOpt(cmd, "doctype", eyDoctype), flagOpt(cmd, "date", eyDate))
}

func init() {
	f := erpItemCmd.Flags()
	f.StringVar(&eiCompany, "company", "", "Company the row is for (required)")
	f.StringVar(&eiDoctype, "doctype", "", "Document the row is for (default: Sales Invoice, or Purchase Invoice with --supplier)")
	f.StringVar(&eiCustomer, "customer", "", "Customer, for customer prices (a sales DocType)")
	f.StringVar(&eiSupplier, "supplier", "", "Supplier (a purchase DocType)")
	f.StringVar(&eiPriceList, "price-list", "", "Price List to take the rate from")
	f.StringVar(&eiQty, "qty", "", "Quantity (default 1)")
	f.StringVar(&eiWarehouse, "warehouse", "", "Warehouse to use instead of the Item's default")
	f.StringVar(&eiDate, "date", "", "Transaction date, YYYY-MM-DD (default: today)")
	f.StringVar(&eiKeys, "keys", "", "Comma-separated keys to include in data output, e.g. item_code,rate")
	_ = erpItemCmd.MarkFlagRequired("company")

	f = erpStockCmd.Flags()
	f.StringVar(&esWarehouse, "warehouse", "", "Show the balance in this Warehouse (default: every warehouse)")
	f.StringVar(&esDate, "date", "", "Balance as of the end of this day, YYYY-MM-DD (needs --warehouse)")
	f.BoolVar(&esValuation, "valuation", false, "Include the valuation rate (needs --warehouse)")
	f.StringVar(&esKeys, "keys", "", "Comma-separated keys to include in data output, e.g. warehouse,actual_qty")

	f = erpPartyCmd.Flags()
	f.StringVar(&eyCustomer, "customer", "", "Customer to look up")
	f.StringVar(&eySupplier, "supplier", "", "Supplier to look up")
	f.StringVar(&eyCompany, "company", "", "Company the document is for")
	f.StringVar(&eyDate, "date", "", "Posting date, YYYY-MM-DD (default: today)")
	f.StringVar(&eyDoctype, "doctype", "", "Document the party is for; an invoice adds the account and due date")
	f.StringVar(&eyKeys, "keys", "", "Comma-separated keys to include in data output, e.g. customer_name,currency")
}
