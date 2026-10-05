//go:build contract

// Contract tests run ffc against a real Frappe site and pin the server
// behaviour ffc relies on. They create a custom submittable DocType and its
// documents, and remove them again, so point them only at a disposable site:
//
//	FFC_CONTRACT_SITE=<site in your ffc config> go test -tags contract -run Contract ./internal/cmd/
//
// or, without a config file, FFC_CONTRACT_URL with FFC_CONTRACT_API_KEY and
// FFC_CONTRACT_API_SECRET (or FFC_CONTRACT_USER and FFC_CONTRACT_PASSWORD).
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	contractDT    = "FFC Contract Test"
	contractChild = "FFC Contract Test Item"
	// contractMarker is the description of both fixture DocTypes; teardown
	// refuses to touch a DocType of the same name without it.
	contractMarker = "Created by the ffc contract tests; safe to delete."
	// contractWF is the fixture Workflow; its states and action carry the
	// same prefix.
	contractWF = "FFC Contract Workflow"
)

// contractSite resolves the site under test, or skips.
func contractSite(t *testing.T) *config.SiteConfig {
	t.Helper()
	if name := os.Getenv("FFC_CONTRACT_SITE"); name != "" {
		sc, err := config.Load(name, os.Getenv("FFC_CONTRACT_CONFIG"))
		if err != nil {
			t.Fatalf("FFC_CONTRACT_SITE: %v", err)
		}
		return sc
	}
	url := os.Getenv("FFC_CONTRACT_URL")
	if url == "" {
		t.Skip("set FFC_CONTRACT_SITE or FFC_CONTRACT_URL to run contract tests")
	}
	return &config.SiteConfig{
		URL:       url,
		APIKey:    os.Getenv("FFC_CONTRACT_API_KEY"),
		APISecret: os.Getenv("FFC_CONTRACT_API_SECRET"),
		Username:  os.Getenv("FFC_CONTRACT_USER"),
		Password:  os.Getenv("FFC_CONTRACT_PASSWORD"),
	}
}

func contractCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// TestContract runs every contract check against one fixture.
func TestContract(t *testing.T) {
	sc := contractSite(t)
	ctx := contractCtx(t)
	c, err := client.New(ctx, sc)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(c.CloseQuietly)

	teardownContract(t, c) // leftovers of an interrupted run
	setupContract(t, c)
	t.Cleanup(func() { teardownContract(t, c) })

	t.Run("numbers keep the server literal", func(t *testing.T) { contractNumbers(t, c, sc) })
	t.Run("list default fields are name only", func(t *testing.T) { contractListDefault(t, c) })
	t.Run("child table PUT replaces rows", func(t *testing.T) { contractChildPut(t, c) })
	t.Run("lifecycle submit cancel amend", func(t *testing.T) { contractLifecycle(t, c) })
	t.Run("lifecycle commands", func(t *testing.T) { contractLifecycleCLI(t, c, sc) })
	t.Run("schema merges custom field and property setter", func(t *testing.T) { contractSchema(t, c) })
	t.Run("cache and completion", func(t *testing.T) { contractCache(t, sc) })
	t.Run("api passthrough", func(t *testing.T) { contractAPI(t, c, sc) })
	t.Run("search and global search", func(t *testing.T) { contractSearch(t, c, sc) })
	t.Run("errors match the fake", func(t *testing.T) { contractErrors(t, c, sc.URL) })
	t.Run("password session", func(t *testing.T) { contractSession(t, sc) })
	t.Run("identity and permissions", func(t *testing.T) { contractIdentity(t, c, sc) })
	t.Run("document context", func(t *testing.T) { contractDocInfo(t, c, sc) })
	// Last: an active workflow changes how the DocType submits.
	t.Run("workflow", func(t *testing.T) { contractWorkflow(t, c, sc) })
}

func setupContract(t *testing.T, c *client.FrappeClient) {
	t.Helper()
	ctx := contractCtx(t)
	mustCreate := func(dt string, data map[string]interface{}) {
		t.Helper()
		if _, err := c.CreateDoc(ctx, dt, data); err != nil {
			t.Fatalf("setup %s: %v", dt, err)
		}
	}
	mustCreate("DocType", map[string]interface{}{
		"name": contractChild, "module": "Custom", "custom": 1, "istable": 1, "description": contractMarker,
		"fields": []interface{}{
			map[string]interface{}{"fieldname": "item", "label": "Item", "fieldtype": "Data"},
			map[string]interface{}{"fieldname": "qty", "label": "Qty", "fieldtype": "Int"},
		},
	})
	mustCreate("DocType", map[string]interface{}{
		"name": contractDT, "module": "Custom", "custom": 1, "is_submittable": 1, "autoname": "hash", "description": contractMarker,
		"allow_rename": 1, "track_changes": 1,
		"fields": []interface{}{
			map[string]interface{}{"fieldname": "title", "label": "Title", "fieldtype": "Data"},
			map[string]interface{}{"fieldname": "ref_no", "label": "Ref No", "fieldtype": "Data", "no_copy": 1},
			map[string]interface{}{"fieldname": "status", "label": "Status", "fieldtype": "Select", "options": "Open\nClosed"},
			map[string]interface{}{"fieldname": "n_int", "label": "N Int", "fieldtype": "Int"},
			map[string]interface{}{"fieldname": "n_float", "label": "N Float", "fieldtype": "Float"},
			map[string]interface{}{"fieldname": "n_cur", "label": "N Cur", "fieldtype": "Currency"},
			map[string]interface{}{"fieldname": "n_check", "label": "N Check", "fieldtype": "Check"},
			map[string]interface{}{"fieldname": "items", "label": "Items", "fieldtype": "Table", "options": contractChild},
		},
		"permissions": []interface{}{map[string]interface{}{
			"role": "System Manager", "read": 1, "write": 1, "create": 1, "delete": 1,
			"submit": 1, "cancel": 1, "amend": 1,
		}},
	})
	mustCreate("Custom Field", map[string]interface{}{
		"dt": contractDT, "fieldname": "custom_note", "label": "Note", "fieldtype": "Data", "insert_after": "title",
	})
	mustCreate("Property Setter", map[string]interface{}{
		"doctype_or_field": "DocField", "doc_type": contractDT, "field_name": "title",
		"property": "label", "property_type": "Data", "value": "Heading",
	})
}

// teardownContract removes the fixture. It is idempotent and best-effort:
// amendments go before their originals, submitted documents are cancelled
// first, then the customisations and the DocTypes.
func teardownContract(t *testing.T, c *client.FrappeClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, dt := range []string{contractDT, contractChild} {
		d, err := c.GetDoc(ctx, "DocType", dt)
		if err != nil {
			continue // absent (or unreadable): nothing of ours to remove
		}
		if d["description"] != contractMarker {
			t.Fatalf("DocType %q exists but was not created by these tests; refusing to delete it", dt)
		}
	}
	teardownWorkflow(ctx, t, c)
	rows, err := c.GetList(ctx, contractDT, client.ListOptions{Fields: []string{"name", "docstatus"}, Limit: -1})
	if err == nil {
		// Longer names first: "x-1-1" before "x-1" before "x".
		sort.Slice(rows, func(i, j int) bool {
			return len(fmt.Sprint(rows[i]["name"])) > len(fmt.Sprint(rows[j]["name"]))
		})
		for _, r := range rows {
			name := fmt.Sprint(r["name"])
			if fmt.Sprint(r["docstatus"]) == "1" {
				_, _ = c.CallMethod(ctx, "frappe.client.cancel", map[string]interface{}{"doctype": contractDT, "name": name}, false)
			}
			if err := c.DeleteDoc(ctx, contractDT, name); err != nil {
				t.Logf("teardown: delete %s: %v", name, err)
			}
		}
	}
	for _, dt := range []string{"Custom Field", "Property Setter"} {
		filter := `{"dt":"` + contractDT + `"}`
		if dt == "Property Setter" {
			filter = `{"doc_type":"` + contractDT + `"}`
		}
		if rows, err := c.GetList(ctx, dt, client.ListOptions{Filters: filter, Limit: -1}); err == nil {
			for _, r := range rows {
				_ = c.DeleteDoc(ctx, dt, fmt.Sprint(r["name"]))
			}
		}
	}
	for _, dt := range []string{contractDT, contractChild} {
		if err := c.DeleteDoc(ctx, "DocType", dt); err != nil && !strings.Contains(err.Error(), "404") {
			t.Logf("teardown: delete DocType %s: %v", dt, err)
		}
	}
	teardownWorkflow(ctx, t, c) // the states, once no document links to them
}

func createContractDoc(t *testing.T, c *client.FrappeClient, data map[string]interface{}) string {
	t.Helper()
	d, err := c.CreateDoc(contractCtx(t), contractDT, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return fmt.Sprint(d["name"])
}

// contractNumbers pins the T0.3 assumption: Int and Check arrive as integer
// literals, Float and Currency always with a fraction (a whole Currency
// value is "1234567.0"), and the table groups only the latter.
func contractNumbers(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	name := createContractDoc(t, c, map[string]interface{}{
		"title": "numbers", "n_int": 2025, "n_float": 1.5, "n_cur": 1234567, "n_check": 1,
	})
	d, err := c.GetDoc(contractCtx(t), contractDT, name)
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"n_int": "2025", "n_float": "1.5", "n_cur": "1234567.0", "n_check": "1", "docstatus": "0"} {
		n, ok := d[field].(json.Number)
		if !ok || n.String() != want {
			t.Errorf("%s = %#v, want json.Number %s", field, d[field], want)
		}
	}

	cfg := contractConfig(t, sc)
	r := runFFC(t, cfg, "", "get-doc", "-d", contractDT, "-n", name)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.Contains(r.Stdout, "2025") || strings.Contains(r.Stdout, "2,025") {
		t.Errorf("table should print the Int unformatted:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "1,234,567") {
		t.Errorf("table should group the Currency (us):\n%s", r.Stdout)
	}
}

// contractAPI pins T1.2: desk methods put their data next to "message", and
// both list APIs page the way --paginate expects (v1 by limit_start, v2 by
// start with has_next_page).
func contractAPI(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	var names []string
	for i := 0; i < 3; i++ {
		names = append(names, createContractDoc(t, c, map[string]interface{}{"title": fmt.Sprintf("api-%d", i)}))
	}
	cfg := contractConfig(t, sc)

	r := runFFC(t, cfg, "", "api", "/api/method/frappe.desk.form.load.getdoc", "-f", "doctype="+contractDT, "-f", "name="+names[0])
	if r.Err != nil {
		t.Fatalf("getdoc: %v\n%s", r.Err, r.Stdout)
	}
	var getdoc struct {
		Docs    []map[string]interface{} `json:"docs"`
		Docinfo map[string]interface{}   `json:"docinfo"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &getdoc); err != nil || len(getdoc.Docs) != 1 || getdoc.Docs[0]["name"] != names[0] || getdoc.Docinfo == nil {
		t.Errorf("getdoc = %s (%v)", r.Stdout, err)
	}

	// list-docs --all pages with the stable "creation asc, name asc" order.
	r = runFFC(t, cfg, "", "list-docs", "-d", contractDT, "--all", "--page-size", "2", "--output", "ndjson", "--filters", `[["title","like","api-%"]]`)
	if r.Err != nil || strings.Count(r.Stdout, "\n") != 3 {
		t.Errorf("list-docs --all: %v\n%s", r.Err, r.Stdout)
	}

	filters := `filters=[["title","like","api-%"]]`
	for _, path := range []string{"/api/resource/" + contractDT, "/api/v2/document/" + contractDT} {
		r := runFFC(t, cfg, "", "api", path, "--paginate", "-f", "limit=2", "-f", filters)
		var out struct{ Data []map[string]interface{} }
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || r.Err != nil || len(out.Data) != 3 {
			t.Errorf("%s: %d rows, err %v / %v\n%s", path, len(out.Data), r.Err, err, r.Stdout)
		}
	}
}

// contractConfig writes an ffc config for the site under test (us number
// format) so CLI commands can be run with runFFC.
func contractConfig(t *testing.T, sc *config.SiteConfig) string {
	t.Helper()
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var b strings.Builder
	fmt.Fprintf(&b, "default_site: contract\nnumber_format: us\nsites:\n  contract:\n    url: %s\n", q(sc.URL))
	for k, v := range map[string]string{"api_key": sc.APIKey, "api_secret": sc.APISecret, "username": sc.Username, "password": sc.Password, "access_token": sc.AccessToken} {
		if v != "" {
			fmt.Fprintf(&b, "    %s: %s\n", k, q(v))
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func contractListDefault(t *testing.T, c *client.FrappeClient) {
	createContractDoc(t, c, map[string]interface{}{"title": "list"})
	rows, err := c.GetList(contractCtx(t), contractDT, client.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if len(r) != 1 || r["name"] == nil {
			t.Errorf("row = %v, want only name", r)
		}
	}
}

// contractSearch pins T2.2: the response shapes of search_link and the global
// search (the fake in internal/frappetest copies them), the page length, an
// empty answer being [] and an unknown DocType being a 404 DoesNotExistError.
func contractSearch(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	var names []string
	for i := 0; i < 2; i++ {
		names = append(names, createContractDoc(t, c, map[string]interface{}{"title": "search"}))
	}

	// search_link finds by name; without a title field a row is
	// {value, description}, and v16 adds a label repeating the name.
	rows, err := c.SearchLink(ctx, contractDT, names[0], 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["value"] != names[0] {
		t.Fatalf("search_link by name = %v, want one row for %s", rows, names[0])
	}
	if l, ok := rows[0]["label"]; ok && l != names[0] {
		t.Errorf("label = %#v, want the name %s", l, names[0])
	}
	if _, ok := rows[0]["description"].(string); !ok {
		t.Errorf("description = %#v, want a string", rows[0]["description"])
	}
	// An empty text lists, page_length cuts.
	if rows, err = c.SearchLink(ctx, contractDT, "", 1); err != nil || len(rows) != 1 {
		t.Errorf("page_length 1: %d rows, %v", len(rows), err)
	}
	if rows, err = c.SearchLink(ctx, contractDT, "", 10); err != nil || len(rows) < 2 {
		t.Errorf("empty text: %d rows, %v", len(rows), err)
	}
	// No hit is [], never null: the client refuses a missing list.
	if rows, err = c.SearchLink(ctx, contractDT, "no-such-ffc-document", 10); err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("no hit = %#v, %v", rows, err)
	}
	var apiErr *client.APIError
	if _, err = c.SearchLink(ctx, "FFC No Such DocType", "x", 10); !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.ExcType != "DoesNotExistError" {
		t.Errorf("unknown DocType: %v, want 404 DoesNotExistError", err)
	}

	// Global search only knows DocTypes in Global Search Settings, so the
	// fixture is not found there; probe words an ERPNext site has hits for.
	rows, err = c.GlobalSearch(ctx, "ffc-contract-no-such-text", 5)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("global search without a hit = %#v, %v", rows, err)
	}
	var hits []map[string]interface{}
	for _, word := range []string{"admin", "account", "customer", "item", "user", "sales"} {
		if hits, err = c.GlobalSearch(ctx, word, 3); err != nil {
			t.Fatal(err)
		}
		if len(hits) > 0 {
			break
		}
	}
	if len(hits) == 0 {
		t.Log("global search: no hits for any probe word; hit shape not checked")
	}
	for _, h := range hits {
		for _, k := range []string{"doctype", "name", "content", "rank"} {
			if h[k] == nil {
				t.Errorf("hit %v lacks %s", h, k)
			}
		}
		if n, ok := h["rank"].(json.Number); !ok || n.String() == "" {
			t.Errorf("rank = %#v, want a json.Number", h["rank"])
		}
	}
	if len(hits) > 3 {
		t.Errorf("limit 3 gave %d hits", len(hits))
	}

	// The CLI end to end: JSON output and the exit code of an unknown DocType.
	cfg := contractConfig(t, sc)
	r := runFFC(t, cfg, "", "--json", "search", names[1], "-d", contractDT)
	var viaCLI []map[string]interface{}
	if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &viaCLI) != nil || len(viaCLI) != 1 || viaCLI[0]["value"] != names[1] {
		t.Errorf("ffc search -d: %v\n%s", r.Err, r.Stdout)
	}
	if r = runFFC(t, cfg, "", "search", "x", "-d", "FFC No Such DocType"); r.Code != 4 {
		t.Errorf("unknown DocType exit = %d, want 4 (%v)", r.Code, r.Err)
	}
}

// contractChildPut pins T2.9: a PUT with a child table replaces the table;
// rows not sent are removed.
func contractChildPut(t *testing.T, c *client.FrappeClient) {
	ctx := contractCtx(t)
	name := createContractDoc(t, c, map[string]interface{}{
		"title": "child",
		"items": []interface{}{
			map[string]interface{}{"item": "a", "qty": 1},
			map[string]interface{}{"item": "b", "qty": 2},
		},
	})
	d, err := c.UpdateDoc(ctx, contractDT, name, map[string]interface{}{
		"items": []interface{}{map[string]interface{}{"item": "c", "qty": 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := d["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["item"] != "c" {
		t.Errorf("items after PUT = %v, want only c", items)
	}
}

// contractLifecycle pins what T1.1 builds on: frappe.client.submit takes the
// document, frappe.client.cancel takes doctype and name, a submitted
// document refuses plain edits, and an amendment is named <original>-1.
func contractLifecycle(t *testing.T, c *client.FrappeClient) {
	ctx := contractCtx(t)
	name := createContractDoc(t, c, map[string]interface{}{"title": "life"})
	doc, err := c.GetDoc(ctx, contractDT, name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.CallMethod(ctx, "frappe.client.submit", map[string]interface{}{"doc": doc}, false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if m, _ := res.(map[string]interface{}); fmt.Sprint(m["docstatus"]) != "1" {
		t.Fatalf("submit returned docstatus %v", m["docstatus"])
	}

	_, err = c.UpdateDoc(ctx, contractDT, name, map[string]interface{}{"title": "changed"})
	if err == nil || !strings.Contains(err.Error(), "UpdateAfterSubmitError") || !strings.Contains(err.Error(), "417") {
		t.Errorf("edit after submit: err = %v, want UpdateAfterSubmitError (417)", err)
	}

	if _, err := c.CallMethod(ctx, "frappe.client.cancel", map[string]interface{}{"doctype": contractDT, "name": name}, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if d, _ := c.GetDoc(ctx, contractDT, name); fmt.Sprint(d["docstatus"]) != "2" {
		t.Fatalf("after cancel docstatus = %v", d["docstatus"])
	}

	amended, err := c.CreateDoc(ctx, contractDT, map[string]interface{}{"title": "life", "amended_from": name})
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if got := fmt.Sprint(amended["name"]); got != name+"-1" {
		t.Errorf("amendment name = %s, want %s-1", got, name)
	}

	err = c.DeleteDoc(ctx, contractDT, name)
	if err == nil || !strings.Contains(err.Error(), "LinkExistsError") {
		t.Errorf("delete original with an amendment: err = %v, want LinkExistsError", err)
	}
}

func contractSchema(t *testing.T, c *client.FrappeClient) {
	schema, warnings, err := fetchSchema(contractCtx(t), c, contractDT)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) > 0 {
		t.Errorf("warnings: %v", warnings)
	}
	fields, _ := schema["fields"].([]interface{})
	var order []string
	labels := map[string]string{}
	for _, f := range fields {
		m := f.(map[string]interface{})
		fn := fmt.Sprint(m["fieldname"])
		order = append(order, fn)
		labels[fn] = fmt.Sprint(m["label"])
	}
	if len(order) < 2 || order[0] != "title" || order[1] != "custom_note" {
		t.Errorf("field order = %v, want custom_note right after title", order)
	}
	if labels["title"] != "Heading" {
		t.Errorf("title label = %q, want the Property Setter value Heading", labels["title"])
	}
}

// contractErrors runs the same failing calls against the real site and the
// fake, and checks they fail the same way, so the fake stays honest.
func contractErrors(t *testing.T, real *client.FrappeClient, realURL string) {
	fake := frappetest.New(t)
	fake.Add(contractDT, map[string]interface{}{"name": "x", "title": "t"})
	fc, err := client.New(contractCtx(t), &config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(c *client.FrappeClient, url string) error{
		"missing document": func(c *client.FrappeClient, url string) error {
			_, err := c.GetDoc(contractCtx(t), contractDT, "ffc-contract-missing")
			return err
		},
		"missing doctype": func(c *client.FrappeClient, url string) error {
			_, err := c.GetList(contractCtx(t), "FFC Contract No Such DocType", client.ListOptions{})
			return err
		},
		// Reading or writing a document of an unknown DocType is a 500
		// ImportError, which ffc reports as not found.
		"missing doctype read": func(c *client.FrappeClient, url string) error {
			_, err := c.GetDoc(contractCtx(t), "FFC Contract No Such DocType", "x")
			return err
		},
		"missing doctype create": func(c *client.FrappeClient, url string) error {
			_, err := c.CreateDoc(contractCtx(t), "FFC Contract No Such DocType", map[string]interface{}{"title": "x"})
			return err
		},
		"missing doctype delete": func(c *client.FrappeClient, url string) error {
			return c.DeleteDoc(contractCtx(t), "FFC Contract No Such DocType", "x")
		},
		"unknown field": func(c *client.FrappeClient, url string) error {
			_, err := c.GetList(contractCtx(t), contractDT, client.ListOptions{Fields: []string{"ffc_no_such_field"}})
			return err
		},
		"unknown filter field": func(c *client.FrappeClient, url string) error {
			_, err := c.GetList(contractCtx(t), contractDT, client.ListOptions{Filters: `[["ffc_no_such_field","=","1"]]`})
			return err
		},
		"unknown method": func(c *client.FrappeClient, url string) error {
			_, err := c.CallMethod(contractCtx(t), "frappe.ffc_no_such.method", nil, false)
			return err
		},
		"wrong api key": func(c *client.FrappeClient, url string) error {
			bad, err := client.New(contractCtx(t), &config.SiteConfig{URL: url, APIKey: "ffc-contract-bad", APISecret: "bad"})
			if err != nil {
				return err
			}
			_, err = bad.GetList(contractCtx(t), contractDT, client.ListOptions{})
			return err
		},
		// The document context methods (doc-info, get_doc_context).
		"docinfo missing document": func(c *client.FrappeClient, url string) error {
			_, err := c.DocInfo(contractCtx(t), contractDT, "ffc-contract-missing")
			return err
		},
		"docinfo missing doctype": func(c *client.FrappeClient, url string) error {
			_, err := c.DocInfo(contractCtx(t), "FFC Contract No Such DocType", "x")
			return err
		},
		"getdoc missing document": func(c *client.FrappeClient, url string) error {
			_, _, err := c.FormLoad(contractCtx(t), contractDT, "ffc-contract-missing")
			return err
		},
		"link counts missing document": func(c *client.FrappeClient, url string) error {
			_, err := c.LinkCounts(contractCtx(t), contractDT, "ffc-contract-missing")
			return err
		},
		"delete missing": func(c *client.FrappeClient, url string) error {
			return c.DeleteDoc(contractCtx(t), contractDT, "ffc-contract-missing")
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			realErr, fakeErr := call(real, realURL), call(fc, fake.URL)
			if realErr == nil || fakeErr == nil {
				t.Fatalf("real = %v, fake = %v; want both to fail", realErr, fakeErr)
			}
			if rs, fs := httpStatus(realErr), httpStatus(fakeErr); rs != fs {
				t.Errorf("status: real %s (%v), fake %s (%v)", rs, realErr, fs, fakeErr)
			}
			if rc, _ := classify(realErr); rc != func() int { c, _ := classify(fakeErr); return c }() {
				t.Errorf("exit code: real %d (%v), fake differs (%v)", rc, realErr, fakeErr)
			}
		})
	}
}

// httpStatus extracts the "(HTTP nnn)" or "(nnn)" status from an ffc error.
func httpStatus(err error) string {
	s := err.Error()
	for _, code := range []string{"400", "401", "403", "404", "409", "417", "500"} {
		if strings.Contains(s, "HTTP "+code) || strings.Contains(s, "("+code+")") {
			return code
		}
	}
	return "?"
}

// contractSession pins T0.1: a logged-out sid is rejected, and a session
// can write without a CSRF token.
func contractSession(t *testing.T, sc *config.SiteConfig) {
	if sc.Username == "" || sc.Password == "" {
		t.Skip("site uses no username/password")
	}
	ctx := contractCtx(t)
	c, err := client.New(ctx, &config.SiteConfig{URL: sc.URL, Username: sc.Username, Password: sc.Password})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateDoc(ctx, contractDT, map[string]interface{}{"title": "session"}); err != nil {
		t.Errorf("session POST: %v", err)
	}
	c.CloseQuietly()

	sid, err := client.LoginPassword(ctx, sc.URL, sc.Username, sc.Password)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Logout(ctx, sc.URL, sid); err != nil {
		t.Fatalf("logout: %v", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sc.URL+"/api/method/frappe.auth.get_logged_user", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("logged-out sid: status %d, want 401/403", resp.StatusCode)
	}
}

// contractLifecycleCLI runs the T1.1 commands: submit, cancel (--check
// first), amend (keeps "no copy" fields), copy (drops them), rename, delete
// and restore, and discard (v16; v15 reports that it needs v16).
func contractLifecycleCLI(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	cfg := contractConfig(t, sc)
	ffc := func(args ...string) map[string]interface{} {
		t.Helper()
		r := runFFC(t, cfg, "", append(args, "--json")...)
		if r.Err != nil {
			t.Fatalf("%s: %v\n%s", args[0], r.Err, r.Stderr)
		}
		var m map[string]interface{}
		if strings.HasPrefix(strings.TrimSpace(r.Stdout), "{") {
			if err := json.Unmarshal([]byte(r.Stdout), &m); err != nil {
				t.Fatalf("%s: %v\n%s", args[0], err, r.Stdout)
			}
		}
		return m
	}
	name := createContractDoc(t, c, map[string]interface{}{
		"title": "cli", "ref_no": "R-1", "items": []interface{}{map[string]interface{}{"item": "a", "qty": 2}},
	})

	if d := ffc("submit-doc", "-d", contractDT, "-n", name); fmt.Sprint(d["docstatus"]) != "1" {
		t.Fatalf("submit-doc: docstatus %v", d["docstatus"])
	}
	r := runFFC(t, cfg, "", "cancel-doc", "-d", contractDT, "-n", name, "--check", "--json")
	if r.Err != nil || strings.TrimSpace(r.Stdout) != "[]" {
		t.Errorf("cancel-doc --check: %v %s", r.Err, r.Stdout)
	}
	if d := ffc("cancel-doc", "-d", contractDT, "-n", name, "--yes"); fmt.Sprint(d["docstatus"]) != "2" {
		t.Fatalf("cancel-doc: docstatus %v", d["docstatus"])
	}

	am := ffc("amend-doc", "-d", contractDT, "-n", name, "--data", `{"title":"cli amended"}`)
	if am["name"] != name+"-1" || am["amended_from"] != name || am["title"] != "cli amended" || am["ref_no"] != "R-1" || fmt.Sprint(am["docstatus"]) != "0" {
		t.Errorf("amend-doc = %v", am)
	}
	if items, _ := am["items"].([]interface{}); len(items) != 1 || items[0].(map[string]interface{})["parent"] != am["name"] {
		t.Errorf("amend-doc items = %v", am["items"])
	}
	r = runFFC(t, cfg, "", "amend-doc", "-d", contractDT, "-n", fmt.Sprint(am["name"]))
	if r.Code != exitValidation {
		t.Errorf("amend-doc of a draft: exit %d, want %d (%v)", r.Code, exitValidation, r.Err)
	}

	cp := ffc("copy-doc", "-d", contractDT, "-n", name)
	if cp["name"] == name || cp["amended_from"] != nil || cp["ref_no"] != nil || cp["title"] != "cli" || fmt.Sprint(cp["docstatus"]) != "0" {
		t.Errorf("copy-doc = %v", cp)
	}
	if items, _ := cp["items"].([]interface{}); len(items) != 1 {
		t.Errorf("copy-doc items = %v", cp["items"])
	}

	newName := fmt.Sprint(cp["name"]) + "-renamed"
	if rn := ffc("rename-doc", "-d", contractDT, "-n", fmt.Sprint(cp["name"]), "--to", newName); rn["name"] != newName {
		t.Errorf("rename-doc = %v", rn)
	}
	if _, err := c.GetDoc(contractCtx(t), contractDT, newName); err != nil {
		t.Errorf("renamed document: %v", err)
	}

	// A hash-named document comes back under a new name.
	ffc("delete-doc", "-d", contractDT, "-n", newName, "--yes")
	rs := ffc("restore-doc", "-d", contractDT, "-n", newName)
	if rs["restored"] != true || rs["name"] == "" {
		t.Errorf("restore-doc = %v", rs)
	}
	if d, err := c.GetDoc(contractCtx(t), contractDT, fmt.Sprint(rs["name"])); err != nil || d["title"] != "cli" {
		t.Errorf("restored document: %v %v", d, err)
	}

	draft := createContractDoc(t, c, map[string]interface{}{"title": "discard"})
	r = runFFC(t, cfg, "", "discard-doc", "-d", contractDT, "-n", draft, "--yes")
	switch {
	case r.Err == nil:
		if d, _ := c.GetDoc(contractCtx(t), contractDT, draft); fmt.Sprint(d["docstatus"]) != "2" {
			t.Errorf("discard-doc: docstatus %v, want 2", d["docstatus"])
		}
	case !strings.Contains(r.Err.Error(), "Frappe v16"):
		t.Errorf("discard-doc: %v", r.Err)
	}
}

// contractWorkflow pins the workflow methods' arguments (a doc with only
// doctype and name) on a two-state workflow, and that submit-doc refuses a
// DocType with an active workflow.
func contractWorkflow(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	draft, done, action := contractWF+" Draft", contractWF+" Done", contractWF+" Finish"
	for _, st := range []string{draft, done} {
		if _, err := c.CreateDoc(ctx, "Workflow State", map[string]interface{}{"workflow_state_name": st}); err != nil {
			t.Fatalf("Workflow State: %v", err)
		}
	}
	if _, err := c.CreateDoc(ctx, "Workflow Action Master", map[string]interface{}{"workflow_action_name": action}); err != nil {
		t.Fatalf("Workflow Action Master: %v", err)
	}
	_, err := c.CreateDoc(ctx, "Workflow", map[string]interface{}{
		"workflow_name": contractWF, "document_type": contractDT, "is_active": 1, "send_email_alert": 0,
		"states": []interface{}{
			map[string]interface{}{"state": draft, "doc_status": "0", "allow_edit": "System Manager"},
			map[string]interface{}{"state": done, "doc_status": "1", "allow_edit": "System Manager"},
		},
		"transitions": []interface{}{
			map[string]interface{}{"state": draft, "action": action, "next_state": done, "allowed": "System Manager", "allow_self_approval": 1},
		},
	})
	if err != nil {
		t.Fatalf("Workflow: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		teardownWorkflow(ctx, t, c)
	})

	cfg := contractConfig(t, sc)
	name := createContractDoc(t, c, map[string]interface{}{"title": "workflow"})
	r := runFFC(t, cfg, "", "submit-doc", "-d", contractDT, "-n", name)
	if r.Code != exitValidation || r.Err == nil || !strings.Contains(r.Err.Error(), contractWF) {
		t.Errorf("submit-doc with a workflow: exit %d, %v", r.Code, r.Err)
	}

	r = runFFC(t, cfg, "", "workflow", "transitions", "-d", contractDT, "-n", name, "--jq", ".[].action")
	if r.Err != nil || strings.TrimSpace(r.Stdout) != action {
		t.Fatalf("workflow transitions: %v %q", r.Err, r.Stdout)
	}
	r = runFFC(t, cfg, "", "workflow", "apply", "-d", contractDT, "-n", name, "--action", action, "--json")
	var doc map[string]interface{}
	if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &doc) != nil || doc["workflow_state"] != done || fmt.Sprint(doc["docstatus"]) != "1" {
		t.Errorf("workflow apply: %v %s", r.Err, r.Stdout)
	}
	r = runFFC(t, cfg, "", "workflow", "apply", "-d", contractDT, "-n", name, "--action", action)
	if r.Code != exitValidation {
		t.Errorf("workflow apply twice: exit %d, %v", r.Code, r.Err)
	}
}

// teardownWorkflow removes the fixture Workflow, its states and its action,
// and the Workflow Actions on contract documents. It touches only a Workflow
// on the contract DocType.
func teardownWorkflow(ctx context.Context, t *testing.T, c *client.FrappeClient) {
	t.Helper()
	// Open Workflow Actions link to the documents and block their delete.
	if rows, err := c.GetList(ctx, "Workflow Action", client.ListOptions{Filters: `{"reference_doctype":"` + contractDT + `"}`, Limit: -1}); err == nil {
		for _, r := range rows {
			_ = c.DeleteDoc(ctx, "Workflow Action", fmt.Sprint(r["name"]))
		}
	}
	if wf, err := c.GetDoc(ctx, "Workflow", contractWF); err == nil {
		if wf["document_type"] != contractDT {
			t.Fatalf("Workflow %q exists but is not on %s; refusing to delete it", contractWF, contractDT)
		}
		if err := c.DeleteDoc(ctx, "Workflow", contractWF); err != nil {
			t.Logf("teardown: delete Workflow: %v", err)
		}
	}
	// The states and the action are matched by name only (they have no field
	// to mark them), so the names carry the fixture prefix; Frappe refuses
	// the delete while any Workflow or document still links to them.
	for _, st := range []string{contractWF + " Draft", contractWF + " Done"} {
		_ = c.DeleteDoc(ctx, "Workflow State", st)
	}
	_ = c.DeleteDoc(ctx, "Workflow Action Master", contractWF+" Finish")
}
