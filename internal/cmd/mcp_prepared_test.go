package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// mcpTPrepared is an MCP server over a site with the prepared report "Stock"
// (jobs finish on the second check), quick polls and a short wait.
func mcpTPrepared(t *testing.T, readOnly bool) (*server.MCPServer, *frappetest.Site) {
	t.Helper()
	oldPoll, oldWait := client.PreparedPollStart, mcpPreparedWait
	client.PreparedPollStart, mcpPreparedWait = time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { client.PreparedPollStart, mcpPreparedWait = oldPoll, oldWait })
	s, site := newMCPFake(t, readOnly)
	site.AddReport("Stock", map[string]interface{}{
		"columns": []interface{}{map[string]interface{}{"fieldname": "item", "label": "Item"}},
		"result":  []interface{}{map[string]interface{}{"item": "bolt"}},
	})
	site.PrepareReport("Stock", 2, "")
	return s, site
}

func TestMCPRunReportPrepared(t *testing.T) {
	s, site := mcpTPrepared(t, false)
	args := map[string]interface{}{"report_name": "Stock", "filters": map[string]interface{}{"company": "Acme"}, "prepared": true}
	out := mcpTObj(t, mcpTOK(t, s, "run_report", args))
	pr, _ := out["prepared_report"].(map[string]interface{})
	if pr["name"] == nil || pr["finished"] == nil || !strings.Contains(fmt.Sprint(out["result"]), "bolt") || out["doc"] != nil {
		t.Fatalf("out %v", out)
	}
	if _, makes, _ := rpCount(site); makes != 1 {
		t.Fatalf("%d makes, want 1", makes)
	}
	// Reused, then a fresh one.
	again := mcpTObj(t, mcpTOK(t, s, "run_report", args))
	if p, _ := again["prepared_report"].(map[string]interface{}); p["name"] != pr["name"] {
		t.Errorf("not reused: %v", again["prepared_report"])
	}
	args["fresh"] = true
	mcpTOK(t, s, "run_report", args)
	if _, makes, _ := rpCount(site); makes != 2 {
		t.Fatalf("fresh: %d makes, want 2", makes)
	}

	before := len(site.Requests())
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock", "fresh": true}, "fresh needs prepared")
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock", "fresh": true, "prepared_report": "x"}, "cannot be combined")
	if n := len(site.Requests()); n != before {
		t.Errorf("invalid calls sent %d requests", n-before)
	}

	// No worker: answered as queued, then picked up by name.
	site.PrepareReport("Stock", -1, "")
	args = map[string]interface{}{"report_name": "Stock", "filters": map[string]interface{}{"company": "Other"}, "prepared": true}
	q := mcpTObj(t, mcpTOK(t, s, "run_report", args))
	name := fmt.Sprint(q["prepared_report"].(map[string]interface{})["name"])
	if q["status"] != "queued" || !strings.Contains(fmt.Sprint(q["hint"]), name) {
		t.Fatalf("queued answer %v", q)
	}
	site.PrepareReport("Stock", 1, "")
	delete(args, "prepared")
	args["prepared_report"] = name
	done := mcpTObj(t, mcpTOK(t, s, "run_report", args))
	if p, _ := done["prepared_report"].(map[string]interface{}); p["name"] != name || done["status"] != nil {
		t.Fatalf("resumed %v", done)
	}

	// A failed job is an error.
	site.PrepareReport("Stock", 1, "Traceback\nZeroDivisionError: division by zero")
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock", "prepared": true, "fresh": true}, "ZeroDivisionError")
}

func TestMCPRunReportPreparedReadOnly(t *testing.T) {
	s, site := mcpTPrepared(t, true)
	args := map[string]interface{}{"report_name": "Stock", "filters": map[string]interface{}{"company": "Acme"}, "prepared": true}
	mcpTErr(t, s, "run_report", args, "no finished or queued prepared report for these filters; MCP is read-only")
	runs, makes, _ := rpCount(site)
	if makes != 0 {
		t.Fatalf("read-only server started %d jobs", makes)
	}
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock", "prepared": true, "fresh": true}, "fresh starts a new prepared report")
	if r, _, _ := rpCount(site); r != runs {
		t.Errorf("fresh on a read-only server ran the report")
	}

	// A job someone else started (here: ffc) is waited for and reused.
	c, err := client.New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	site.PrepareReport("Stock", -1, "")
	if _, err := c.RunPreparedReport(t.Context(), "Stock", map[string]interface{}{"company": "Acme"}, client.PreparedOptions{Wait: time.Millisecond}); err == nil {
		t.Fatal("job finished without a worker")
	}
	site.PrepareReport("Stock", 1, "")
	out := mcpTObj(t, mcpTOK(t, s, "run_report", args))
	if out["prepared_report"] == nil || !strings.Contains(fmt.Sprint(out["result"]), "bolt") {
		t.Fatalf("out %v", out)
	}
	if _, makes, _ := rpCount(site); makes != 1 {
		t.Errorf("%d makes, want only ffc's", makes)
	}
}

func TestMCPRunReportPreparedProgress(t *testing.T) {
	s, site := mcpTPrepared(t, false)
	site.PrepareReport("Stock", 4, "")
	c := mcpTClient(t, s, nil, false)
	var (
		mu  sync.Mutex
		got []map[string]interface{}
	)
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method == string(mcp.MethodNotificationProgress) {
			mu.Lock()
			got = append(got, n.Params.AdditionalFields)
			mu.Unlock()
		}
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = "run_report"
	req.Params.Arguments = map[string]interface{}{"report_name": "Stock", "prepared": true}
	req.Params.Meta = &mcp.Meta{ProgressToken: "rep-1"}
	if res, err := c.CallTool(t.Context(), req); err != nil || res.IsError {
		t.Fatalf("run_report: %v %+v", err, res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 3 {
		t.Fatalf("progress %v", got)
	}
	for i, f := range got {
		if f["progressToken"] != "rep-1" || fmt.Sprint(f["progress"]) != fmt.Sprint(i+1) || f["total"] != nil || !strings.Contains(fmt.Sprint(f["message"]), "is queued or running") {
			t.Errorf("notification %d: %v", i, f)
		}
	}
}
