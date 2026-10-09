//go:build contract

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

const setupCompleteMethod = "frappe.desk.page.setup_wizard.setup_wizard.setup_complete"

// contractERPSetup makes sure the site has a Company, running ERPNext's setup
// wizard once as the contract user when it has none. frappe_docker's pwd.yml
// installs ERPNext but never runs the wizard, so a new site has no Company,
// Fiscal Year, chart of accounts or warehouses. The wizard is idempotent
// (setup_complete returns "ok" once the setup is done), and the data stays on
// the site for the next run. It returns the Company's name.
//
// The keys are what the two wizards read (frappe setup_wizard.py
// update_global_settings and update_system_settings; erpnext
// install_fixtures.install_company and install_defaults): language is the
// language's name ("English" skips the language switch), no email so that no
// user is created, and the fiscal year spans the current year.
func contractERPSetup(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) string {
	t.Helper()
	company := func() string {
		rows, err := c.GetList(contractCtx(t), "Company", client.ListOptions{Fields: []string{"name"}, Limit: 1})
		if err != nil {
			t.Fatalf("listing Company: %v", err)
		}
		if len(rows) == 0 {
			return ""
		}
		return fmt.Sprint(rows[0]["name"])
	}
	if name := company(); name != "" {
		return name
	}

	// The wizard rewrites System Settings and creates a Company, so it runs
	// only on a site that is plainly a throwaway one.
	if u, err := url.Parse(sc.URL); os.Getenv("FFC_CONTRACT_ERP_SETUP") != "1" && (err != nil || !loopbackHost(u.Hostname())) {
		t.Skipf("%s has no Company and is not a local site: the ERPNext setup wizard would change it. Set FFC_CONTRACT_ERP_SETUP=1 to run it anyway", sc.URL)
	}

	// The wizard installs fixtures and the chart of accounts in one request,
	// which outlasts the usual 30 s timeout.
	old := client.Timeout
	client.Timeout = 5 * time.Minute
	wc, err := client.New(contractCtx(t), sc)
	client.Timeout = old
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer wc.CloseQuietly()
	year := time.Now().Year()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	res, err := wc.CallMethod(ctx, setupCompleteMethod, map[string]interface{}{"args": map[string]interface{}{
		"language": "English", "country": "United States", "timezone": "America/New_York", "currency": "USD",
		"full_name": "FFC Contract", "company_name": "FFC Contract Co", "company_abbr": "FCC",
		"chart_of_accounts": "Standard", "fy_start_date": fmt.Sprintf("%d-01-01", year), "fy_end_date": fmt.Sprintf("%d-12-31", year),
	}}, false)
	var api *client.APIError
	var tr *client.TransportError
	switch {
	case errors.As(err, &tr) || errors.As(err, &api) && api.Status >= 500:
		// A proxy in front of the site (pwd.yml: 120 s) can give up on the
		// request while the wizard goes on: wait for its last step instead.
		t.Logf("%s: %v; waiting for the setup to finish", setupCompleteMethod, err)
	case err != nil:
		t.Fatalf("%s: %v", setupCompleteMethod, err)
	default:
		// process_setup_stages swallows an exception into a None reply.
		if reply, _ := res.(map[string]interface{}); reply["status"] != "ok" {
			t.Fatalf("%s answered %v, want {\"status\": \"ok\"}", setupCompleteMethod, res)
		}
	}
	// The last step of the wizard sets the default Company (Global Defaults).
	for deadline := time.Now().Add(6 * time.Minute); ; time.Sleep(5 * time.Second) {
		if gd, err := c.GetDoc(contractCtx(t), "Global Defaults", "Global Defaults"); err == nil {
			if name, ok := docName(gd["default_company"]); ok {
				return name
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no default Company after %s (answered %v, error %v)", setupCompleteMethod, res, err)
		}
	}
}

// loopbackHost reports whether a host name is this machine.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// contractERPFirst returns the name of one document of a DocType that is not a
// group (Customer Group, Territory and Item Group trees need a leaf).
func contractERPFirst(t *testing.T, c *client.FrappeClient, doctype string) string {
	t.Helper()
	rows, err := c.GetList(contractCtx(t), doctype, client.ListOptions{Fields: []string{"name"}, Filters: `{"is_group":0}`, Limit: 1})
	if err != nil || len(rows) == 0 {
		t.Fatalf("no %s that is not a group: %v", doctype, err)
	}
	return fmt.Sprint(rows[0]["name"])
}

// contractERPDocs are the documents the ERP subtests start from.
type contractERPDocs struct {
	company, customer, item, order string
	// create makes a document and removes it again when the test ends
	// (cancelled first when it was submitted).
	create func(dt string, data map[string]interface{}) string
}

// contractERPOrder skips unless ERPNext 15 or 16 is installed, makes sure the
// site has a Company, then creates a Customer, a non-stock Item and a
// submitted Sales Order, all with unique names.
func contractERPOrder(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) contractERPDocs {
	t.Helper()
	info, err := c.ServerVersions(contractCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if !client.SupportedERPNext(info.Major(client.ERPNextApp)) {
		t.Skipf("ERPNext %q is not installed or not supported", info.Version(client.ERPNextApp))
	}
	company := contractERPSetup(t, c, sc)
	// After the setup, which can take minutes.
	ctx := contractCtx(t)

	// Unique names, so a run never meets the data of an interrupted one.
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	today := time.Now().Format("2006-01-02")
	due := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	create := func(dt string, data map[string]interface{}) string {
		t.Helper()
		d, err := c.CreateDoc(contractCtx(t), dt, data)
		if err != nil {
			t.Fatalf("create %s: %v", dt, err)
		}
		name := fmt.Sprint(d["name"])
		t.Cleanup(func() {
			// Best effort: cancel what was submitted, then delete.
			ctx := contractCtx(t)
			_, _ = c.CancelDoc(ctx, dt, name)
			_ = c.DeleteDoc(ctx, dt, name)
		})
		return name
	}
	customer := create("Customer", map[string]interface{}{
		"customer_name": "FFC ERP " + id, "customer_type": "Company",
		"customer_group": contractERPFirst(t, c, "Customer Group"), "territory": contractERPFirst(t, c, "Territory"),
	})
	item := create("Item", map[string]interface{}{
		"item_code": "FFC-ERP-" + id, "item_name": "FFC ERP " + id, "item_group": contractERPFirst(t, c, "Item Group"),
		"stock_uom": "Nos", "is_stock_item": 0,
	})
	order := create("Sales Order", map[string]interface{}{
		"customer": customer, "company": company, "transaction_date": today, "delivery_date": due,
		"currency": "USD", "selling_price_list": "Standard Selling", "price_list_currency": "USD",
		"conversion_rate": 1, "plc_conversion_rate": 1,
		"items": []interface{}{map[string]interface{}{"item_code": item, "qty": 2, "rate": 100, "delivery_date": due}},
	})
	if _, err := c.SubmitDoc(ctx, "Sales Order", order); err != nil {
		t.Fatalf("submit %s: %v", order, err)
	}
	return contractERPDocs{company: company, customer: customer, item: item, order: order, create: create}
}

// contractERPMap pins T3.4's map: the mapper methods answer an unsaved draft
// over GET, the draft inserts once cleaned, and a Sales Invoice made from a
// Sales Order submits and links back to it.
func contractERPMap(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	docs := contractERPOrder(t, c, sc)
	customer, order := docs.customer, docs.order
	ctx := contractCtx(t)
	invoicesOf := func() int {
		rows, err := c.GetList(contractCtx(t), "Sales Invoice", client.ListOptions{Fields: []string{"name"}, Filters: fmt.Sprintf(`{"customer":%q}`, customer), Limit: -1})
		if err != nil {
			t.Fatalf("listing Sales Invoice: %v", err)
		}
		return len(rows)
	}
	cfg := contractConfig(t, sc)
	from := "Sales Order:" + order

	// The draft is an unsaved document with the order's items, and a GET
	// writes nothing.
	r := runFFC(t, cfg, "", "--json", "--timeout", "2m", "erp", "map", "--from", from, "--to", "Sales Invoice")
	if r.Err != nil {
		t.Fatalf("erp map: %v\n%s", r.Err, r.Stderr)
	}
	var draft struct {
		DocType string                   `json:"doctype"`
		Items   []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &draft); err != nil || draft.DocType != "Sales Invoice" || len(draft.Items) != 1 || draft.Items[0]["sales_order"] != order {
		t.Fatalf("draft = %s (%v)", r.Stdout, err)
	}
	if n := invoicesOf(); n != 0 {
		t.Fatalf("%d Sales Invoice after a plain map", n)
	}

	// --create --submit: inserted and submitted, linked to the order.
	r = runFFC(t, cfg, "", "--json", "--timeout", "2m", "erp", "map", "--from", from, "--to", "Sales Invoice", "--create", "--submit", "--keys", "name,docstatus")
	if r.Err != nil {
		t.Fatalf("erp map --create --submit: %v\n%s", r.Err, r.Stderr)
	}
	var made struct {
		Name      string      `json:"name"`
		DocStatus json.Number `json:"docstatus"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &made); err != nil || made.Name == "" || made.DocStatus != "1" {
		t.Fatalf("created = %s (%v)", r.Stdout, err)
	}
	t.Cleanup(func() {
		ctx := contractCtx(t)
		_, _ = c.CancelDoc(ctx, "Sales Invoice", made.Name)
		_ = c.DeleteDoc(ctx, "Sales Invoice", made.Name)
	})
	inv, err := c.GetDoc(ctx, "Sales Invoice", made.Name)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := inv["items"].([]interface{})
	row, _ := items[0].(map[string]interface{})
	// Frappe sends a Float as 2.0, and ffc keeps the literal: compare the value.
	qty, _ := strconv.ParseFloat(fmt.Sprint(row["qty"]), 64)
	if fmt.Sprint(inv["docstatus"]) != "1" || inv["customer"] != customer || len(items) != 1 || row["sales_order"] != order || qty != 2 {
		t.Errorf("invoice = docstatus %v customer %v items %v", inv["docstatus"], inv["customer"], items)
	}
	if n := invoicesOf(); n != 1 {
		t.Errorf("%d Sales Invoice, want 1", n)
	}

	// A pair ffc does not map is refused before any request to the mapper.
	r = runFFC(t, cfg, "", "erp", "map", "--from", from, "--to", "Purchase Order")
	if r.Err == nil || r.Code != exitUsage || !strings.Contains(r.Err.Error(), "supported:") {
		t.Errorf("unsupported pair: exit %d, %v", r.Code, r.Err)
	}
}

// contractERPPayment pins T3.4's payment: get_payment_entry answers an
// unsaved Payment Entry over GET, and --create --submit saves one that is
// submitted, references the invoice and brings its outstanding amount to 0.
func contractERPPayment(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	docs := contractERPOrder(t, c, sc)
	customer, company := docs.customer, docs.company
	ctx := contractCtx(t)
	cfg := contractConfig(t, sc)

	// A submitted Sales Invoice, made the way a user would: from the order.
	r := runFFC(t, cfg, "", "--json", "--timeout", "2m", "erp", "map", "--from", "Sales Order:"+docs.order, "--to", "Sales Invoice", "--submit", "--keys", "name,docstatus")
	if r.Err != nil {
		t.Fatalf("erp map --submit: %v\n%s", r.Err, r.Stderr)
	}
	var raw struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &raw); err != nil || raw.Name == "" {
		t.Fatalf("invoice = %s (%v)", r.Stdout, err)
	}
	invoice := raw.Name
	t.Cleanup(func() {
		ctx := contractCtx(t)
		_, _ = c.CancelDoc(ctx, "Sales Invoice", invoice)
		_ = c.DeleteDoc(ctx, "Sales Invoice", invoice)
	})
	number := func(v interface{}) float64 {
		// Frappe sends a Float as 2.0, and ffc keeps the literal: compare the value.
		f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f
	}
	outstanding := func() float64 {
		d, err := c.GetDoc(contractCtx(t), "Sales Invoice", invoice)
		if err != nil {
			t.Fatalf("get %s: %v", invoice, err)
		}
		return number(d["outstanding_amount"])
	}
	before := outstanding()
	if before <= 0 {
		t.Fatalf("outstanding_amount of %s = %v, want more than 0", invoice, before)
	}

	// get_payment_entry needs an account to pay into. The setup wizard sets
	// the Company's default cash account from the chart's "Cash" account; if
	// it did not, use the one cash account the chart has. It is also passed
	// as --bank-account below, so a default bank account (which would ask for
	// a reference number) never gets in the way.
	co, err := c.GetDoc(ctx, "Company", company)
	if err != nil {
		t.Fatal(err)
	}
	cash, _ := docName(co["default_cash_account"])
	if cash == "" {
		rows, err := c.GetList(ctx, "Account", client.ListOptions{Fields: []string{"name"}, Filters: fmt.Sprintf(`{"company":%q,"account_type":"Cash","is_group":0}`, company), Limit: 1})
		if err != nil || len(rows) == 0 {
			t.Fatalf("no cash account in %s: %v", company, err)
		}
		cash = fmt.Sprint(rows[0]["name"])
		t.Logf("Company %s has no default cash account: using %s", company, cash)
		if _, err := c.UpdateDoc(ctx, "Company", company, map[string]interface{}{"default_cash_account": cash}); err != nil {
			t.Fatalf("setting default_cash_account: %v", err)
		}
		// The site is left as it was found. This runs after the cleanup of
		// the Payment Entry made below, which needs the account.
		t.Cleanup(func() {
			if _, err := c.UpdateDoc(contractCtx(t), "Company", company, map[string]interface{}{"default_cash_account": nil}); err != nil {
				t.Logf("restoring default_cash_account of %s: %v", company, err)
			}
		})
	}
	paymentsOf := func() int {
		rows, err := c.GetList(contractCtx(t), "Payment Entry", client.ListOptions{Fields: []string{"name"}, Filters: fmt.Sprintf(`{"party":%q}`, customer), Limit: -1})
		if err != nil {
			t.Fatalf("listing Payment Entry: %v", err)
		}
		return len(rows)
	}
	against := "Sales Invoice:" + invoice
	today := time.Now().Format("2006-01-02")

	// The draft is an unsaved Payment Entry for the whole outstanding amount,
	// and a GET writes nothing.
	r = runFFC(t, cfg, "", "--json", "--timeout", "2m", "erp", "payment", "--against", against, "--bank-account", cash, "--reference-date", today)
	if r.Err != nil {
		t.Fatalf("erp payment: %v\n%s", r.Err, r.Stderr)
	}
	var draft struct {
		DocType     string                   `json:"doctype"`
		PaymentType string                   `json:"payment_type"`
		Party       string                   `json:"party"`
		PaidTo      string                   `json:"paid_to"`
		PaidAmount  json.Number              `json:"paid_amount"`
		References  []map[string]interface{} `json:"references"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &draft); err != nil || draft.DocType != "Payment Entry" || draft.PaymentType != "Receive" ||
		draft.Party != customer || draft.PaidTo != cash || number(draft.PaidAmount) != before ||
		len(draft.References) != 1 || draft.References[0]["reference_name"] != invoice || draft.References[0]["reference_doctype"] != "Sales Invoice" {
		t.Fatalf("draft = %s (%v), want a Receive of %v into %s for %s", r.Stdout, err, before, cash, invoice)
	}
	if n := paymentsOf(); n != 0 {
		t.Fatalf("%d Payment Entry after a plain payment", n)
	}
	if got := outstanding(); got != before {
		t.Fatalf("outstanding_amount = %v after a plain payment, want %v", got, before)
	}

	// --create --submit: saved, submitted, linked to the invoice.
	r = runFFC(t, cfg, "", "--json", "--timeout", "2m", "erp", "payment", "--against", against, "--bank-account", cash, "--reference-date", today, "--submit", "--keys", "name,docstatus")
	var made struct {
		Name      string      `json:"name"`
		DocStatus json.Number `json:"docstatus"`
	}
	// A submit that fails after the insert still prints the created entry:
	// remove it whatever happened next.
	parseErr := json.Unmarshal([]byte(r.Stdout), &made)
	if made.Name != "" {
		t.Cleanup(func() {
			ctx := contractCtx(t)
			_, _ = c.CancelDoc(ctx, "Payment Entry", made.Name)
			_ = c.DeleteDoc(ctx, "Payment Entry", made.Name)
		})
	}
	if r.Err != nil {
		t.Fatalf("erp payment --submit: %v\n%s\n%s", r.Err, r.Stderr, r.Stdout)
	}
	if parseErr != nil || made.Name == "" || made.DocStatus != "1" {
		t.Fatalf("created = %s (%v)", r.Stdout, parseErr)
	}
	pe, err := c.GetDoc(ctx, "Payment Entry", made.Name)
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := pe["references"].([]interface{})
	var ref map[string]interface{}
	if len(refs) == 1 {
		ref, _ = refs[0].(map[string]interface{})
	}
	if fmt.Sprint(pe["docstatus"]) != "1" || pe["party"] != customer || pe["paid_to"] != cash || number(pe["paid_amount"]) != before ||
		ref["reference_doctype"] != "Sales Invoice" || ref["reference_name"] != invoice || number(ref["allocated_amount"]) != before {
		t.Errorf("payment entry = docstatus %v party %v paid_to %v paid_amount %v references %v", pe["docstatus"], pe["party"], pe["paid_to"], pe["paid_amount"], refs)
	}
	if n := paymentsOf(); n != 1 {
		t.Errorf("%d Payment Entry, want 1", n)
	}
	if got := outstanding(); got != 0 {
		t.Errorf("outstanding_amount of %s = %v after the payment, want 0", invoice, got)
	}

	// A DocType ffc does not pay is refused before any request.
	r = runFFC(t, cfg, "", "erp", "payment", "--against", "Quotation:X")
	if r.Err == nil || r.Code != exitUsage || !strings.Contains(r.Err.Error(), "supported:") {
		t.Errorf("unsupported DocType: exit %d, %v", r.Code, r.Err)
	}
}
