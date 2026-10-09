package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

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
// major ffc has not checked).
func erpLookupMethods(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (client.LookupMethods, error) {
	info, _, err := serverInfo(ctx, c, cfg, false)
	if err != nil {
		return client.LookupMethods{}, err
	}
	major, err := erpNextMajor(info, cfg)
	if err != nil {
		return client.LookupMethods{}, err
	}
	m, ok := client.LookupFor(major)
	if !ok {
		return client.LookupMethods{}, fmt.Errorf("ffc erp has no lookups for ERPNext %d", major)
	}
	return m, nil
}

// lookupDate checks an optional YYYY-MM-DD flag.
func lookupDate(cmd *cobra.Command, flag, value string) (string, error) {
	if !cmd.Flags().Changed(flag) {
		return "", nil
	}
	d := strings.TrimSpace(value)
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", usageErrorf("--%s: expected YYYY-MM-DD, got %q", flag, value)
	}
	return d, nil
}

// lookupText checks an optional text flag: given means not blank.
func lookupText(cmd *cobra.Command, flag, value string) (string, error) {
	if !cmd.Flags().Changed(flag) {
		return "", nil
	}
	v := strings.TrimSpace(value)
	if v == "" {
		return "", usageErrorf("--%s: provide a value", flag)
	}
	return v, nil
}

// lookupItemArg checks the ITEM argument.
func lookupItemArg(args []string) (string, error) {
	item := strings.TrimSpace(args[0])
	if item == "" {
		return "", usageErrorf("provide the Item code")
	}
	return item, nil
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
			m, err := erpLookupMethods(ctx, c, cfg)
			if err != nil {
				return nil, err
			}
			return c.ItemDetails(ctx, m, o)
		})
		if err != nil {
			return err
		}
		return printLookup(doc, eiKeys)
	},
}

// itemOptions reads and checks the flags of 'erp item'.
func itemOptions(cmd *cobra.Command, item string) (client.ItemOptions, error) {
	o := client.ItemOptions{ItemCode: item}
	var err error
	if o.Company = strings.TrimSpace(eiCompany); o.Company == "" {
		return o, usageErrorf("--company: provide the Company")
	}
	if o.Customer, err = lookupText(cmd, "customer", eiCustomer); err != nil {
		return o, err
	}
	if o.Supplier, err = lookupText(cmd, "supplier", eiSupplier); err != nil {
		return o, err
	}
	if o.Customer != "" && o.Supplier != "" {
		return o, usageErrorf("--customer and --supplier cannot be used together")
	}
	if o.Doctype, err = lookupText(cmd, "doctype", eiDoctype); err != nil {
		return o, err
	}
	sales, purchase := slices.Contains(client.ItemSalesDoctypes, o.Doctype), slices.Contains(client.ItemPurchaseDoctypes, o.Doctype)
	switch {
	case o.Doctype == "":
		o.Doctype = "Sales Invoice"
		if o.Supplier != "" {
			o.Doctype = "Purchase Invoice"
		}
	case !sales && !purchase:
		return o, usageErrorf("--doctype: cannot look up an item for %s; supported: %s, %s", o.Doctype,
			strings.Join(client.ItemSalesDoctypes, ", "), strings.Join(client.ItemPurchaseDoctypes, ", "))
	case o.Customer != "" && !sales:
		return o, usageErrorf("--customer: %s is a purchase DocType, use --supplier", o.Doctype)
	case o.Supplier != "" && !purchase:
		return o, usageErrorf("--supplier: %s is a sales DocType, use --customer", o.Doctype)
	}
	if o.PriceList, err = lookupText(cmd, "price-list", eiPriceList); err != nil {
		return o, err
	}
	if o.Warehouse, err = lookupText(cmd, "warehouse", eiWarehouse); err != nil {
		return o, err
	}
	if o.Date, err = lookupDate(cmd, "date", eiDate); err != nil {
		return o, err
	}
	if cmd.Flags().Changed("qty") {
		q := strings.TrimSpace(eiQty)
		if v, err := strconv.ParseFloat(q, 64); !plainAmount.MatchString(q) || err != nil || v <= 0 {
			return o, usageErrorf("--qty: expected a positive number such as 5 or 2.5, got %q", eiQty)
		}
		o.Qty = q
	}
	return o, nil
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
rows (a warning says when it stopped). --date and --valuation need
--warehouse; the rows show the current state and carry the valuation rate.
On ERPNext 15 this call does not check Bin read permission (it lists with
get_all, limited only by Warehouse user permissions); ERPNext 16 returns
nothing without it. Names in the rows are shown as they are, not as HTML.

Quantities keep the number Frappe sends (2.0 stays 2.0).

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
		warehouse, err := lookupText(cmd, "warehouse", esWarehouse)
		if err != nil {
			return err
		}
		date, err := lookupDate(cmd, "date", esDate)
		if err != nil {
			return err
		}
		if warehouse == "" && (date != "" || esValuation) {
			return usageErrorf("--date and --valuation need --warehouse: without one the rows show the current stock and their valuation rate")
		}
		if warehouse != "" {
			doc, err := callSiteCfg(cmd, fmt.Sprintf("Reading the stock of %s…", item), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (map[string]interface{}, error) {
				m, err := erpLookupMethods(ctx, c, cfg)
				if err != nil {
					return nil, err
				}
				return c.StockBalance(ctx, m, item, warehouse, date, esValuation)
			})
			if err != nil {
				return err
			}
			return printLookup(doc, esKeys)
		}
		type result struct {
			rows      []map[string]interface{}
			truncated bool
		}
		res, err := callSiteCfg(cmd, fmt.Sprintf("Reading the stock of %s…", item), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (result, error) {
			m, err := erpLookupMethods(ctx, c, cfg)
			if err != nil {
				return result{}, err
			}
			rows, truncated, err := c.StockByWarehouse(ctx, m, item)
			return result{rows, truncated}, err
		})
		if err != nil {
			return err
		}
		if res.truncated {
			fmt.Fprintf(os.Stderr, "warning: stopped after %d warehouses; there may be more: ask for one with --warehouse\n", client.StockPageSize*client.StockMaxPages)
		}
		return printStockRows(res.rows, esKeys)
	},
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
			m, err := erpLookupMethods(ctx, c, cfg)
			if err != nil {
				return nil, err
			}
			return c.PartyDetails(ctx, m, o)
		})
		if err != nil {
			return err
		}
		return printLookup(doc, eyKeys)
	},
}

// partyOptions reads and checks the flags of 'erp party'.
func partyOptions(cmd *cobra.Command) (client.PartyOptions, error) {
	var o client.PartyOptions
	customer, err := lookupText(cmd, "customer", eyCustomer)
	if err != nil {
		return o, err
	}
	supplier, err := lookupText(cmd, "supplier", eySupplier)
	if err != nil {
		return o, err
	}
	switch {
	case customer != "" && supplier != "":
		return o, usageErrorf("--customer and --supplier cannot be used together")
	case customer != "":
		o.PartyType, o.Party = "Customer", customer
	case supplier != "":
		o.PartyType, o.Party = "Supplier", supplier
	default:
		return o, usageErrorf("provide --customer or --supplier")
	}
	if o.Company, err = lookupText(cmd, "company", eyCompany); err != nil {
		return o, err
	}
	if o.Doctype, err = lookupText(cmd, "doctype", eyDoctype); err != nil {
		return o, err
	}
	if o.Date, err = lookupDate(cmd, "date", eyDate); err != nil {
		return o, err
	}
	return o, nil
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
