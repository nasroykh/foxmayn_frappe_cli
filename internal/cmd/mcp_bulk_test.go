package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// mcpTBulkNames is n document names, TD-1 to TD-n.
func mcpTBulkNames(n int) []interface{} {
	out := make([]interface{}, n)
	for i := range out {
		out[i] = fmt.Sprintf("TD-%d", i+1)
	}
	return out
}

// mcpTSlowDeletes answers every DELETE of names itself. An answer waits until
// four requests are in flight (or a second has passed), so a pool that
// allows fewer shows up as a lower peak, then holds a little longer, longest
// for the first names, so items finish out of input order. It returns the
// highest number of requests in flight.
func mcpTSlowDeletes(site *frappetest.Site, names []interface{}) *atomic.Int32 {
	var inflight, peak atomic.Int32
	full := make(chan struct{})
	var once sync.Once
	for i, n := range names {
		delay := time.Duration(len(names)-i) * 3 * time.Millisecond
		site.Handle("DELETE /api/resource/ToDo/"+n.(string), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			cur := inflight.Add(1)
			defer inflight.Add(-1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			if cur >= maxMCPBulkWorkers {
				once.Do(func() { close(full) })
			}
			select {
			case <-full:
			case <-time.After(time.Second):
			}
			time.Sleep(delay)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		}))
	}
	return &peak
}

func TestMCPBulkParallel(t *testing.T) {
	s, site := newMCPFake(t, false)
	names := mcpTBulkNames(12)
	peak := mcpTSlowDeletes(site, names)
	m := mcpTObj(t, mcpTOK(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": names}))
	if got := peak.Load(); got != maxMCPBulkWorkers {
		t.Errorf("peak requests in flight = %d, want %d", got, maxMCPBulkWorkers)
	}
	if m["deleted"] != 12.0 || m["failed"] != 0.0 || m["skipped"] != 0.0 {
		t.Fatalf("report = %v", m)
	}
	// Results stay in input order although the items finished out of it.
	for i, r := range m["results"].([]interface{}) {
		got := r.(map[string]interface{})
		if got["index"] != float64(i+1) || got["name"] != names[i] || got["status"] != "deleted" {
			t.Errorf("result %d = %v", i+1, got)
		}
	}
}

func TestMCPBulkCreateUpdateParallel(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("ToDo")
	items := make([]interface{}, 20)
	for i := range items {
		items[i] = map[string]interface{}{"description": "task " + strconv.Itoa(i)}
	}
	m := mcpTObj(t, mcpTOK(t, s, "bulk_create", map[string]interface{}{"doctype": "ToDo", "data": items}))
	if m["created"] != 20.0 || site.Count("ToDo") != 20 {
		t.Fatalf("created %v, count %d", m["created"], site.Count("ToDo"))
	}
	res := m["results"].([]interface{})
	var upd []interface{}
	for i, r := range res {
		got := r.(map[string]interface{})
		if got["index"] != float64(i+1) || got["status"] != "created" || got["name"] == "" {
			t.Fatalf("result %d = %v", i+1, got)
		}
		// The created document is the item's: the order of results is the input's.
		if d, _ := site.Doc("ToDo", got["name"].(string)); d["description"] != "task "+strconv.Itoa(i) {
			t.Errorf("result %d is %v, description %v", i+1, got["name"], d["description"])
		}
		upd = append(upd, map[string]interface{}{"name": got["name"], "status": "Closed"})
	}
	m = mcpTObj(t, mcpTOK(t, s, "bulk_update", map[string]interface{}{"doctype": "ToDo", "data": upd}))
	if m["updated"] != 20.0 || m["failed"] != 0.0 {
		t.Errorf("update report = %v", m)
	}
}

func TestMCPBulkProgressParallel(t *testing.T) {
	s, site := newMCPFake(t, false)
	names := mcpTBulkNames(12)
	mcpTSlowDeletes(site, names)
	c := mcpTClient(t, s, nil, false)
	var (
		mu  sync.Mutex
		got []int
	)
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method != string(mcp.MethodNotificationProgress) {
			return
		}
		p, _ := strconv.Atoi(fmt.Sprint(n.Params.AdditionalFields["progress"]))
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = "bulk_delete"
	req.Params.Arguments = map[string]interface{}{"doctype": "ToDo", "names": names}
	req.Params.Meta = &mcp.Meta{ProgressToken: "p"}
	if res, err := c.CallTool(t.Context(), req); err != nil || res.IsError {
		t.Fatalf("bulk_delete: %v %+v", err, res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 12 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	want := make([]int, 12)
	for i := range want {
		want[i] = i + 1
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("progress = %v, want %v", got, want)
	}
}

func TestMCPBulkDuplicateNames(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	mcpTErr(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"TD-1", "TD-2", "TD-1"}}, `items 1 and 3 name the same document, "TD-1"`)
	// Frappe names are case-insensitive, and a number is its string.
	mcpTErr(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"td-1", "TD-1"}}, "name the same document")
	mcpTErr(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"7", 7.0}}, "name the same document")
	mcpTErr(t, s, "bulk_update", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{
		map[string]interface{}{"name": "TD-1", "status": "Open"},
		map[string]interface{}{"name": "TD-2", "status": "Open"},
		map[string]interface{}{"name": "TD-1", "status": "Closed"},
	}}, `items 1 and 3 name the same document, "TD-1"`)
	if n := len(site.Requests()); n != 0 {
		t.Errorf("%d requests reached the site", n)
	}
	if site.Count("ToDo") != 3 {
		t.Errorf("count = %d", site.Count("ToDo"))
	}
}

// The lifecycle fixture: SO-1 and SO-4 are drafts, SO-2 is submitted, SO-3
// cancelled.
func mcpTLifecycleSite(site *frappetest.Site) {
	site.Add("Sales Order",
		map[string]interface{}{"name": "SO-1", "customer": "C1"},
		map[string]interface{}{"name": "SO-2", "customer": "C2", "docstatus": json.Number("1")},
		map[string]interface{}{"name": "SO-3", "customer": "C3", "docstatus": json.Number("2")},
		map[string]interface{}{"name": "SO-4", "customer": "C4"},
	)
}

func mcpTDocstatus(site *frappetest.Site, name string) string {
	d, _ := site.Doc("Sales Order", name)
	return fmt.Sprint(d["docstatus"])
}

func TestMCPBulkSubmit(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTLifecycleSite(site)
	m := mcpTObj(t, mcpTOK(t, s, "bulk_submit", map[string]interface{}{"doctype": "Sales Order", "names": []interface{}{"SO-1", "SO-2", "SO-3", "SO-4", "missing"}}))
	if m["submitted"] != 2.0 || m["failed"] != 3.0 || m["skipped"] != 0.0 {
		t.Fatalf("report = %v", m)
	}
	want := []struct{ name, status, err string }{
		{"SO-1", "submitted", ""},
		{"SO-2", "error", "SO-2 is already submitted"},
		{"SO-3", "error", "SO-3 is cancelled and cannot be submitted again"},
		{"SO-4", "submitted", ""},
		{"missing", "error", "not found"},
	}
	for i, r := range m["results"].([]interface{}) {
		got := r.(map[string]interface{})
		errMsg, _ := got["error"].(string)
		if got["index"] != float64(i+1) || got["name"] != want[i].name || got["status"] != want[i].status || !strings.Contains(errMsg, want[i].err) {
			t.Errorf("result %d = %v, want %+v", i+1, got, want[i])
		}
	}
	for name, ds := range map[string]string{"SO-1": "1", "SO-2": "1", "SO-3": "2", "SO-4": "1"} {
		if got := mcpTDocstatus(site, name); got != ds {
			t.Errorf("%s docstatus = %s, want %s", name, got, ds)
		}
	}
	// One at a time, each document read before it is acted on (SubmitDoc
	// reads it again to send it back as read).
	var seq []string
	for _, r := range site.Requests() {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.Path, "/api/resource/Sales Order/"):
			seq = append(seq, "GET "+strings.TrimPrefix(r.Path, "/api/resource/Sales Order/"))
		case r.Method == http.MethodPost && strings.HasSuffix(r.Path, "frappe.client.submit"):
			seq = append(seq, "submit")
		}
	}
	if wantSeq := []string{"GET SO-1", "GET SO-1", "submit", "GET SO-2", "GET SO-3", "GET SO-4", "GET SO-4", "submit", "GET missing"}; !reflect.DeepEqual(seq, wantSeq) {
		t.Errorf("requests = %v, want %v", seq, wantSeq)
	}
}

func TestMCPBulkCancelDocs(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTLifecycleSite(site)
	site.Add("Sales Order", map[string]interface{}{"name": "SO-5", "customer": "C5", "docstatus": json.Number("1")})
	m := mcpTObj(t, mcpTOK(t, s, "bulk_cancel", map[string]interface{}{"doctype": "Sales Order", "names": []interface{}{"SO-5", "SO-1", "SO-2", "SO-3"}}))
	if m["cancelled"] != 2.0 || m["failed"] != 2.0 {
		t.Fatalf("report = %v", m)
	}
	res := m["results"].([]interface{})
	for i, w := range []struct{ status, err string }{
		{"cancelled", ""},
		{"error", "only a submitted document can be cancelled"},
		{"cancelled", ""},
		{"error", "SO-3 is already cancelled"},
	} {
		got := res[i].(map[string]interface{})
		errMsg, _ := got["error"].(string)
		if got["status"] != w.status || !strings.Contains(errMsg, w.err) {
			t.Errorf("result %d = %v, want %+v", i+1, got, w)
		}
	}
	for name, ds := range map[string]string{"SO-1": "0", "SO-2": "2", "SO-3": "2", "SO-5": "2"} {
		if got := mcpTDocstatus(site, name); got != ds {
			t.Errorf("%s docstatus = %s, want %s", name, got, ds)
		}
	}
}

func TestMCPBulkLifecycleRefusals(t *testing.T) {
	for _, tool := range []string{"bulk_submit", "bulk_cancel"} {
		t.Run(tool, func(t *testing.T) {
			s, site := newMCPFake(t, false)
			mcpTLifecycleSite(site)
			args := map[string]interface{}{"doctype": "Sales Order", "names": []interface{}{"SO-1", "SO-2"}}

			// Bad input never reaches the site.
			names := make([]interface{}, maxMCPBulkItems+1)
			for i := range names {
				names[i] = fmt.Sprintf("SO-%d", i)
			}
			mcpTErr(t, s, tool, map[string]interface{}{"doctype": "Sales Order", "names": names}, "too many items (201)")
			mcpTErr(t, s, tool, map[string]interface{}{"doctype": "Sales Order", "names": []interface{}{}}, "empty")
			mcpTErr(t, s, tool, map[string]interface{}{"doctype": "Sales Order"}, "names")
			if n := len(site.Requests()); n != 0 {
				t.Errorf("%d requests reached the site", n)
			}

			// An active Workflow is refused once, before any document is read.
			site.Add("Workflow", map[string]interface{}{"name": "SO Approval", "document_type": "Sales Order", "is_active": json.Number("1")})
			mcpTErr(t, s, tool, args, `workflow "SO Approval"`)
			for _, r := range site.Requests() {
				if r.Method != http.MethodGet || strings.HasPrefix(r.Path, "/api/resource/Sales Order") {
					t.Errorf("request after the workflow check: %s %s", r.Method, r.Path)
				}
			}
			for _, name := range []string{"SO-1", "SO-2", "SO-3", "SO-4"} {
				if got := mcpTDocstatus(site, name); got == "" {
					t.Errorf("%s vanished", name)
				}
			}
			if mcpTDocstatus(site, "SO-1") != "0" || mcpTDocstatus(site, "SO-2") != "1" {
				t.Errorf("a document changed: SO-1 %s, SO-2 %s", mcpTDocstatus(site, "SO-1"), mcpTDocstatus(site, "SO-2"))
			}
		})
	}
}

func TestMCPBulkLifecyclePolicy(t *testing.T) {
	for _, tool := range []string{"bulk_submit", "bulk_cancel"} {
		t.Run(tool, func(t *testing.T) {
			s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"Sales Order"}}, config.MCPPolicy{})
			mcpTLifecycleSite(site)
			mcpTErr(t, s, tool, map[string]interface{}{"doctype": "Sales Order", "names": []interface{}{"SO-1"}}, "policy:")
			if n := len(site.Requests()); n != 0 {
				t.Errorf("%d requests reached the site", n)
			}
			// Another DocType is fine.
			site.Add("Purchase Order", map[string]interface{}{"name": "PO-1"})
			if res := callTool(t, s, tool, map[string]interface{}{"doctype": "Purchase Order", "names": []interface{}{"PO-1"}}); res.IsError {
				t.Errorf("Purchase Order refused: %s", resultText(t, res))
			}
		})
	}
}

func TestMCPBulkLifecycleToolSets(t *testing.T) {
	for _, tool := range []string{"bulk_submit", "bulk_cancel"} {
		if toolActions[tool] != actWrite {
			t.Errorf("%s action = %v", tool, toolActions[tool])
		}
	}
	has := func(names []string, tool string) bool {
		for _, n := range names {
			if n == tool {
				return true
			}
		}
		return false
	}
	s, _ := newMCPFake(t, true)
	ro := mcpTToolNames(t, s)
	core := mcpTToolNames(t, mcpTToolsets(t, []string{"core"}))
	life := mcpTToolNames(t, mcpTToolsets(t, []string{"lifecycle"}))
	for _, tool := range []string{"bulk_submit", "bulk_cancel"} {
		if has(ro, tool) {
			t.Errorf("%s registered under --read-only", tool)
		}
		if has(core, tool) {
			t.Errorf("%s registered without the lifecycle tool set", tool)
		}
		if !has(life, tool) {
			t.Errorf("%s missing from the lifecycle tool set", tool)
		}
	}
	// The instructions follow.
	if text := mcpTInstructions(t, s); strings.Contains(text, "bulk_submit") {
		t.Errorf("read-only instructions name bulk_submit:\n%s", text)
	}
	s, _ = newMCPFake(t, false)
	if text := mcpTInstructions(t, s); !strings.Contains(text, "bulk_submit and bulk_cancel do the first two for up to 200 documents") {
		t.Errorf("instructions lack the bulk lifecycle line:\n%s", text)
	}
}

func TestMCPBulkLifecycleConfirm(t *testing.T) {
	for _, tc := range []struct {
		tool, verb, after, status string
	}{
		{"bulk_submit", "Submit", "A submitted document can only be cancelled, not edited.", "1"},
		{"bulk_cancel", "Cancel", "This cannot be undone.", "2"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			s, site, _, audit := mcpTPolicy(t, nil, config.MCPPolicy{})
			mcpTLifecycleSite(site)
			site.Add("Sales Order", map[string]interface{}{"name": "SO-5", "docstatus": json.Number("1")})
			names := []interface{}{"SO-1", "SO-5"}
			if tc.tool == "bulk_submit" {
				names = []interface{}{"SO-1", "SO-4"}
			}
			args := map[string]interface{}{"doctype": "Sales Order", "names": names}

			// Declined: asked once, nothing sent.
			a := &mcpTAsker{action: mcp.ElicitationResponseActionDecline}
			c := mcpTClient(t, s, a, false)
			out, isErr := mcpTCall(t, c, tc.tool, args)
			if !isErr || !strings.Contains(out, "cancelled by the user; nothing was changed") {
				t.Errorf("declined: %v %q", isErr, out)
			}
			wantMsg := fmt.Sprintf(`%s 2 "Sales Order" documents: %s, %s.`, tc.verb, quoted(names[0].(string), 140), quoted(names[1].(string), 140))
			if len(a.asked) != 1 || !strings.Contains(a.asked[0], wantMsg) || !strings.HasSuffix(a.asked[0], tc.after) {
				t.Errorf("asked %q, want %q ... %q", a.asked, wantMsg, tc.after)
			}
			if n := blcTWrites(site); n != 0 {
				t.Errorf("%d writes after a decline", n)
			}

			// Accepted: it runs.
			a.action, a.confirm, a.asked = mcp.ElicitationResponseActionAccept, true, nil
			out, isErr = mcpTCall(t, c, tc.tool, args)
			if isErr {
				t.Fatalf("accepted: %s", out)
			}
			if len(a.asked) != 1 {
				t.Errorf("asked %d times", len(a.asked))
			}
			if got := mcpTDocstatus(site, names[1].(string)); got != tc.status {
				t.Errorf("%s docstatus = %s, want %s", names[1], got, tc.status)
			}
			if got := strings.Join(mcpTStatuses(t, audit), ","); got != "confirm_pending,declined,confirm_pending,ok" {
				t.Errorf("audit statuses = %s", got)
			}
		})
	}
}

// A client that cannot ask is sent to the CLI, with the command to run.
func TestMCPBulkLifecycleConfirmAlways(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{Confirm: "always"}, config.MCPPolicy{})
	mcpTLifecycleSite(site)
	c := mcpTClient(t, s, nil, false)
	for tool, cmd := range map[string]string{
		"bulk_submit": "ffc --site prod bulk-submit --doctype 'Sales Order' --names 'SO-1,SO-4'",
		"bulk_cancel": "ffc --site prod bulk-cancel --doctype 'Sales Order' --names 'SO-1,SO-4'",
	} {
		out, isErr := mcpTCall(t, c, tool, map[string]interface{}{"doctype": "Sales Order", "names": []interface{}{"SO-1", "SO-4"}})
		if !isErr || !strings.Contains(out, "cannot ask for it; nothing was changed") || !strings.Contains(out, cmd) {
			t.Errorf("%s: %v %q", tool, isErr, out)
		}
	}
	if n := blcTWrites(site); n != 0 {
		t.Errorf("%d writes", n)
	}
}
