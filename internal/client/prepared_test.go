package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const preparedMake = "/api/method/frappe.core.doctype.prepared_report.prepared_report.make_prepared_report"

func preparedSite(t *testing.T) (*frappetest.Site, *FrappeClient) {
	t.Helper()
	old := PreparedPollStart
	PreparedPollStart = time.Millisecond
	t.Cleanup(func() { PreparedPollStart = old })
	site := frappetest.New(t)
	site.AddReport("Stock", map[string]interface{}{"columns": []interface{}{"Item"}, "result": []interface{}{[]interface{}{"bolt"}}})
	site.PrepareReport("Stock", 1, "")
	return site, fakeClient(t, site)
}

func TestRunPreparedReportReuseOnly(t *testing.T) {
	site, c := preparedSite(t)
	ctx := context.Background()
	acme := map[string]interface{}{"company": "Acme"}
	reuse := PreparedOptions{ReuseOnly: true, Wait: time.Second}

	if _, err := c.RunPreparedReport(ctx, "Stock", acme, reuse); !errors.Is(err, ErrNoPreparedReport) {
		t.Fatalf("nothing to reuse: %v", err)
	}
	fresh := reuse
	fresh.Fresh = true
	if _, err := c.RunPreparedReport(ctx, "Stock", acme, fresh); !errors.Is(err, ErrNoPreparedReport) {
		t.Fatalf("fresh, nothing to reuse: %v", err)
	}
	if n := len(site.RequestsTo("POST", preparedMake)); n != 0 {
		t.Fatalf("ReuseOnly started %d jobs", n)
	}

	first, err := c.RunPreparedReport(ctx, "Stock", acme, PreparedOptions{Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	name := first["doc"].(map[string]interface{})["name"]
	// Fresh with ReuseOnly still returns the finished one, and so does Name.
	for _, opt := range []PreparedOptions{fresh, {ReuseOnly: true, Name: name.(string), Wait: time.Second}} {
		res, err := c.RunPreparedReport(ctx, "Stock", acme, opt)
		if err != nil || res["doc"].(map[string]interface{})["name"] != name {
			t.Fatalf("%+v: %v, %v", opt, err, res["doc"])
		}
	}
	if n := len(site.RequestsTo("POST", preparedMake)); n != 1 {
		t.Fatalf("%d jobs, want 1", n)
	}
}

func TestRunPreparedReportNameChecked(t *testing.T) {
	site, c := preparedSite(t)
	ctx := context.Background()
	first, err := c.RunPreparedReport(ctx, "Stock", map[string]interface{}{"company": "Acme"}, PreparedOptions{Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	name := first["doc"].(map[string]interface{})["name"].(string)
	other := map[string]interface{}{"company": "Other"}
	var se *StateError

	// The document cannot be read: the final answer is checked instead.
	site.Handle("GET /api/resource/Prepared Report/"+name, frappetest.ErrorHandler(frappetest.Permission("No permission for Prepared Report")))
	if _, err := c.RunPreparedReport(ctx, "Stock", other, PreparedOptions{Name: name, Wait: time.Second}); !errors.As(err, &se) {
		t.Fatalf("other filters, unreadable document: %v", err)
	}
	// Any other failure to read it is returned, not skipped.
	site.Handle("GET /api/resource/Prepared Report/"+name, frappetest.ErrorHandler(frappetest.NotFound("Prepared Report "+name+" not found")))
	var api *APIError
	if _, err := c.RunPreparedReport(ctx, "Stock", other, PreparedOptions{Name: name, Wait: time.Second}); !errors.As(err, &api) || api.Status != 404 {
		t.Fatalf("unreadable for another reason: %v", err)
	}
}
