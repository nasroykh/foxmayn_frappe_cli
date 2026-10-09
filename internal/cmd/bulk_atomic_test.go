package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const atomicPath = "/api/method/frappe.client.insert_many"

// atomicTSent counts the requests that wrote: insert_many and /api/resource.
func atomicTSent(s *frappetest.Site) (many, one int) {
	for _, r := range s.Requests() {
		switch {
		case r.Method == http.MethodPost && r.Path == atomicPath:
			many++
		case r.Method == http.MethodPost && strings.HasPrefix(r.Path, "/api/resource/"):
			one++
		}
	}
	return many, one
}

func TestCmdBulkCreateAtomic(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "bulk-create", "-d", "ToDo", "--atomic",
		"--data", `[{"description":"a"},{"name":"X","description":"b"},{"description":"c"}]`))
	out := cmdTBulkJSON(t, r)
	if fmt.Sprint(out["created"]) != "3" || fmt.Sprint(out["failed"]) != "0" || fmt.Sprint(out["skipped"]) != "0" {
		t.Fatalf("out %v", out)
	}
	if got := strings.Join(cmdTResultStatuses(out), ","); got != "created,created,created" {
		t.Fatalf("statuses %s", got)
	}
	var names []string
	for i, x := range out["results"].([]interface{}) {
		res := x.(map[string]interface{})
		if fmt.Sprint(res["index"]) != fmt.Sprint(i+1) {
			t.Errorf("result %d index %v", i, res["index"])
		}
		names = append(names, fmt.Sprint(res["name"]))
	}
	if names[1] != "X" || names[0] == "" || names[2] == "" || names[0] == names[2] {
		t.Fatalf("names %v are not the three created documents in order", names)
	}
	for _, n := range names {
		if _, ok := s.Doc("ToDo", n); !ok {
			t.Errorf("%s was not created", n)
		}
	}
	if many, one := atomicTSent(s); many != 1 || one != 0 {
		t.Errorf("sent %d insert_many and %d single inserts, want 1 and 0", many, one)
	}
	// The doctype is set on every item.
	var body struct {
		Docs []map[string]interface{} `json:"docs"`
	}
	if err := json.Unmarshal([]byte(s.RequestsTo(http.MethodPost, atomicPath)[0].Body), &body); err != nil || len(body.Docs) != 3 || body.Docs[0]["doctype"] != "ToDo" {
		t.Errorf("body %+v, %v", body, err)
	}

	// The table view says what was created.
	r = cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"T1"}]`))
	cmdTHas(t, r.Stderr, "All 1 ToDo documents created")
	cmdTHas(t, r.Stdout, "created", "T1")

	// A same-doctype item is accepted.
	cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"doctype":"ToDo","name":"T2"}]`))
	if _, ok := s.Doc("ToDo", "T2"); !ok {
		t.Error("T2 missing")
	}
}

func TestCmdBulkCreateAtomicRollsBack(t *testing.T) {
	s := cmdTSite(t)
	// The third item clashes with TD-1: the two before it must not stay.
	r := cmdTRun(t, s, "--json", "bulk-create", "-d", "ToDo", "--atomic",
		"--data", `[{"name":"keep1"},{"name":"keep2"},{"name":"TD-1"}]`)
	lcTCode(t, r, exitValidation, "nothing was created", "already exists")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Errorf("a failed batch printed a report: %q", r.Stdout)
	}
	if s.Count("ToDo") != 3 {
		t.Errorf("count = %d, want 3", s.Count("ToDo"))
	}
	for _, n := range []string{"keep1", "keep2"} {
		if _, ok := s.Doc("ToDo", n); ok {
			t.Errorf("%s survived the failed batch", n)
		}
	}
	if many, one := atomicTSent(s); many != 1 || one != 0 {
		t.Errorf("sent %d insert_many and %d single inserts", many, one)
	}

	// The error keeps its class: a permission error is exit 5, and says
	// nothing was created.
	s.HandleMethod("frappe.client.insert_many", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Permission("No permission for ToDo")
	})
	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"p"}]`), exitPermission, "nothing was created")
}

func TestCmdBulkCreateAtomicOutcomeUnknown(t *testing.T) {
	s := cmdTSite(t)
	s.HandleMethod("frappe.client.insert_many", func(*http.Request, map[string]interface{}) (interface{}, error) {
		time.Sleep(600 * time.Millisecond)
		return []interface{}{"late"}, nil
	})
	r := cmdTRun(t, s, "--timeout", "150ms", "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"late"}]`)
	lcTCode(t, r, exitNetwork, "may or may not have been created", "--timeout", "check the site")
	if strings.Contains(r.Err.Error(), "nothing was created") {
		t.Errorf("a timeout must not claim nothing was created: %v", r.Err)
	}

	// A proxy's 504 is no answer from Frappe either.
	s2 := cmdTSite(t)
	s2.Handle("POST "+atomicPath, frappetest.ErrorHandler(&frappetest.Error{Status: http.StatusGatewayTimeout, ExcType: "Exception", Message: "gateway timeout"}))
	lcTCode(t, cmdTRun(t, s2, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"late"}]`), exitNetwork, "may or may not have been created")

	// A CDN's origin timeout (524) and a plain 500 page are not Frappe's
	// answers; a 500 with Frappe's exc_type is.
	for _, tc := range []struct {
		err  *frappetest.Error
		want string
	}{
		{&frappetest.Error{Status: 524, Message: "origin timeout"}, "may or may not have been created"},
		{&frappetest.Error{Status: http.StatusRequestTimeout, Message: "request timeout"}, "may or may not have been created"},
		{&frappetest.Error{Status: http.StatusInternalServerError, ExcType: "LinkValidationError", Message: "bad link"}, "nothing was created"},
	} {
		s3 := cmdTSite(t)
		s3.Handle("POST "+atomicPath, frappetest.ErrorHandler(tc.err))
		r := cmdTRun(t, s3, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"x"}]`)
		if r.Err == nil || !strings.Contains(r.Err.Error(), tc.want) {
			t.Errorf("status %d: err %v, want %q", tc.err.Status, r.Err, tc.want)
		}
	}

	// A success answer that does not hold the names means the batch was
	// probably created.
	s4 := cmdTSite(t)
	s4.HandleMethod("frappe.client.insert_many", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return []interface{}{}, nil
	})
	r = cmdTRun(t, s4, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"x"}]`)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "probably created") {
		t.Errorf("unusable reply: err %v", r.Err)
	}
}

func TestCmdBulkCreateAtomicRefusedBeforeSending(t *testing.T) {
	s := cmdTSite(t)

	over := make([]string, 201)
	for i := range over {
		over[i] = fmt.Sprintf(`{"name":"o%d"}`, i)
	}
	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", "["+strings.Join(over, ",")+"]"),
		exitUsage, "201 documents exceed the 200")

	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--concurrency", "2", "--data", `[{"name":"c"}]`),
		exitUsage, "--concurrency", "--fail-fast")
	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--fail-fast", "--data", `[{"name":"c"}]`),
		exitUsage, "--concurrency", "--fail-fast")

	// An item for another DocType is refused, not rewritten.
	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"name":"a"},{"doctype":"Note","name":"b"}]`),
		exitUsage, "item 2", `doctype Note, not "ToDo"`)

	// So is a row for an existing parent, which Frappe would save instead of create.
	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"parent":"TD-1","parenttype":"ToDo","parentfield":"x"}]`),
		exitUsage, "item 1", "parent")

	// The input checks of bulk-create still come first.
	lcTCode(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", `[{"a":1},3]`), exitUsage, "item 2")

	if n := len(s.Requests()); n != 0 {
		t.Fatalf("refused input still sent %d requests", n)
	}
	if s.Count("ToDo") != 3 {
		t.Errorf("count = %d", s.Count("ToDo"))
	}

	// Exactly 200 is fine.
	cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--data", "["+strings.Join(over[:200], ",")+"]"))
	if s.Count("ToDo") != 203 {
		t.Errorf("count = %d, want 203", s.Count("ToDo"))
	}
}

func TestDryRunBulkCreateAtomic(t *testing.T) {
	s := cmdTSite(t)
	reqs := dryTPlan(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--atomic", "--dry-run", "--json",
		"--data", `[{"description":"a"},{"description":"b"},{"description":"c"}]`))
	if len(reqs) != 1 || reqs[0]["method"] != "POST" || !strings.HasSuffix(fmt.Sprint(reqs[0]["url"]), atomicPath) {
		t.Fatalf("plan = %v, want the one insert_many request", reqs)
	}
	body, _ := reqs[0]["body"].(map[string]interface{})
	docs, _ := body["docs"].([]interface{})
	if len(docs) != 3 || docs[2].(map[string]interface{})["doctype"] != "ToDo" {
		t.Errorf("planned body = %v", body)
	}
	if n := dryTWrites(s); n != 0 || s.Count("ToDo") != 3 {
		t.Fatalf("%d writes, %d docs", n, s.Count("ToDo"))
	}
}
