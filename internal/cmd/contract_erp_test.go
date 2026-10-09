//go:build contract

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
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
	if err != nil {
		t.Fatalf("%s: %v", setupCompleteMethod, err)
	}
	t.Logf("setup_complete answered %v", res)
	// "registered" means a site configured to run the wizard in a worker.
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(3 * time.Second) {
		if name := company(); name != "" {
			return name
		}
		if time.Now().After(deadline) {
			t.Fatalf("no Company after setup_complete (%v)", res)
		}
	}
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

// contractERPMap pins T3.4's map: the mapper methods answer an unsaved draft
// over GET, the draft inserts once cleaned, and a Sales Invoice made from a
// Sales Order submits and links back to it.
func contractERPMap(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	info, err := c.ServerVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !client.SupportedERPNext(info.Major(client.ERPNextApp)) {
		t.Skipf("ERPNext %q is not installed or not supported", info.Version(client.ERPNextApp))
	}
	company := contractERPSetup(t, c, sc)

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
	if fmt.Sprint(inv["docstatus"]) != "1" || inv["customer"] != customer || len(items) != 1 || row["sales_order"] != order || fmt.Sprint(row["qty"]) != "2" {
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
