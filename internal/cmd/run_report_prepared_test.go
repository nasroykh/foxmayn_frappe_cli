package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	rpRun    = "/api/method/frappe.desk.query_report.run"
	rpMake   = "/api/method/frappe.core.doctype.prepared_report.prepared_report.make_prepared_report"
	rpQueued = "/api/method/frappe.core.doctype.prepared_report.prepared_report.get_reports_in_queued_state"
)

// rpSite has a prepared report "Stock" whose jobs finish on the second
// check, and a plain report "Plain".
func rpSite(t *testing.T) *frappetest.Site {
	t.Helper()
	old := client.PreparedPollStart
	client.PreparedPollStart = time.Millisecond
	t.Cleanup(func() { client.PreparedPollStart = old })
	s := frappetest.New(t)
	result := map[string]interface{}{
		"columns": []interface{}{map[string]interface{}{"fieldname": "item", "label": "Item"}},
		"result":  []interface{}{map[string]interface{}{"item": "bolt"}},
	}
	s.AddReport("Stock", result)
	s.AddReport("Plain", result)
	s.PrepareReport("Stock", 2, "")
	return s
}

// rpCount counts the POSTs to run and make_prepared_report and the queued
// checks so far.
func rpCount(s *frappetest.Site) (runs, makes, checks int) {
	return len(s.RequestsTo("POST", rpRun)), len(s.RequestsTo("POST", rpMake)), len(s.RequestsTo("GET", rpQueued))
}

func TestCmdRunReportPrepared(t *testing.T) {
	s := rpSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "run-report", "-n", "Stock", "--filters", `{"warehouse":"A","company":"Acme"}`, "--prepared"))
	out := cmdTObj(t, r)
	doc, _ := out["doc"].(map[string]interface{})
	if out["prepared_report"] != true || doc["status"] != "Completed" || !strings.Contains(fmt.Sprint(out["result"]), "bolt") {
		t.Fatalf("out %v", out)
	}
	if !strings.Contains(r.Stderr, fmt.Sprintf("From prepared report %v, finished", doc["name"])) {
		t.Errorf("stderr %q", r.Stderr)
	}
	runs, makes, checks := rpCount(s)
	if runs != 2 || makes != 1 || checks < 2 {
		t.Fatalf("%d runs, %d makes, %d checks; want 2, 1, >= 2", runs, makes, checks)
	}
	reqs := s.RequestsTo("POST", rpRun)
	first, last := mcpTBody(t, reqs[0]), mcpTBody(t, reqs[1])
	if fmt.Sprint(first["ignore_prepared_report"]) != "0" || first["filters"] != `{"company":"Acme","warehouse":"A"}` {
		t.Errorf("first run %v", first)
	}
	if want := fmt.Sprintf(`{"company":"Acme","prepared_report_name":"%v","warehouse":"A"}`, doc["name"]); last["filters"] != want {
		t.Errorf("last run filters %v, want %s", last["filters"], want)
	}
	if b := mcpTBody(t, s.RequestsTo("POST", rpMake)[0]); b["filters"] != `{"company":"Acme","warehouse":"A"}` || b["report_name"] != "Stock" {
		t.Errorf("make %v", b)
	}

	// The finished result is reused: one run, nothing queued.
	cmdTOK(t, cmdTRun(t, s, "--json", "run-report", "-n", "Stock", "--filters", `{"company":"Acme","warehouse":"A"}`, "--prepared"))
	if r2, m2, _ := rpCount(s); r2 != runs+1 || m2 != makes {
		t.Fatalf("reuse: %d runs, %d makes", r2-runs, m2-makes)
	}
	// --fresh and other filters prepare a new one.
	cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Stock", "--filters", `{"company":"Acme","warehouse":"A"}`, "--prepared", "--fresh"))
	cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Stock", "--prepared"))
	if _, m3, _ := rpCount(s); m3 != makes+2 {
		t.Fatalf("--fresh and no filters: %d makes, want 2", m3-makes)
	}

	// A report that is not prepared runs in the request, once.
	before, _, _ := rpCount(s)
	r = cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Plain", "--prepared"))
	if r4, _, _ := rpCount(s); r4 != before+1 || strings.Contains(r.Stderr, "From prepared report") {
		t.Fatalf("plain report: %d runs, stderr %q", r4-before, r.Stderr)
	}
}

func TestCmdRunReportPreparedPending(t *testing.T) {
	s := rpSite(t)
	s.PrepareReport("Stock", -1, "") // no worker
	start := time.Now()
	r := cmdTRun(t, s, "run-report", "-n", "Stock", "--filters", `{"company":"Acme"}`, "--prepared", "--wait", "1s")
	if r.Code != exitNetwork || !strings.Contains(r.Err.Error(), "still queued or running after 1s") || !strings.Contains(r.Err.Error(), `"long" queue`) {
		t.Fatalf("exit %d: %v", r.Code, r.Err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("waited %s for --wait 1s", d)
	}
	name := regexp.MustCompile(`--prepared-name (\S+)`).FindStringSubmatch(r.Err.Error())
	if name == nil || !strings.Contains(r.Err.Error(), `--filters '{"company":"Acme"}'`) {
		t.Fatalf("no resume command in %q", r.Err)
	}

	// A second --prepared run waits for the same job instead of a new one.
	_, makes, _ := rpCount(s)
	cmdTRun(t, s, "run-report", "-n", "Stock", "--filters", `{"company":"Acme"}`, "--prepared", "--wait", "1s")
	if _, m, _ := rpCount(s); m != makes {
		t.Fatalf("queued job not reused: %d makes", m-makes)
	}

	// The worker comes back: --prepared-name picks the job up.
	s.PrepareReport("Stock", 1, "")
	r = cmdTOK(t, cmdTRun(t, s, "--json", "run-report", "-n", "Stock", "--filters", `{"company":"Acme"}`, "--prepared-name", name[1]))
	if doc, _ := cmdTObj(t, r)["doc"].(map[string]interface{}); doc["name"] != name[1] {
		t.Fatalf("resumed doc %v", doc)
	}
	if _, m, _ := rpCount(s); m != makes {
		t.Fatalf("--prepared-name made a new job")
	}
}

func TestCmdRunReportPreparedFails(t *testing.T) {
	s := rpSite(t)
	s.PrepareReport("Stock", 1, "division by zero")
	r := cmdTRun(t, s, "run-report", "-n", "Stock", "--prepared")
	if r.Code != exitNetwork || !strings.Contains(r.Err.Error(), "has no result (status Error): division by zero") {
		t.Fatalf("exit %d: %v", r.Code, r.Err)
	}

	before := len(s.Requests())
	for args, want := range map[string]string{
		"--fresh":                              "--fresh and --wait need --prepared",
		"--wait 2m":                            "--fresh and --wait need --prepared",
		"--prepared --wait 0s":                 "--wait must be at least 1s",
		"--prepared-name x --fresh":            "cannot be combined",
		"--prepared --prepared-name x --fresh": "cannot be combined",
	} {
		r := cmdTRun(t, s, append([]string{"run-report", "-n", "Stock"}, strings.Fields(args)...)...)
		if cmdTFail(t, r, want); r.Code != exitUsage {
			t.Errorf("%s: exit %d", args, r.Code)
		}
	}
	if n := len(s.Requests()); n != before {
		t.Errorf("usage errors sent %d requests", n-before)
	}
}
