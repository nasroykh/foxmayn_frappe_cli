package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// dryTPlan decodes the --json plan of a dry run.
func dryTPlan(t *testing.T, r cliResult) []map[string]interface{} {
	t.Helper()
	cmdTOK(t, r)
	if r.Code != exitOK {
		t.Fatalf("dry run exit %d", r.Code)
	}
	var out struct {
		DryRun   bool                     `json:"dry_run"`
		Requests []map[string]interface{} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || !out.DryRun {
		t.Fatalf("not a plan: %v\n%s", err, r.Stdout)
	}
	return out.Requests
}

// dryTWrites counts the requests that could have changed something.
func dryTWrites(s *frappetest.Site) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method != http.MethodGet && r.Path != "/api/method/login" && r.Path != "/api/method/logout" {
			n++
		}
	}
	return n
}

func TestDryRunCRUD(t *testing.T) {
	s := cmdTSite(t)

	reqs := dryTPlan(t, cmdTRun(t, s, "create-doc", "-d", "ToDo", "--dry-run", "--json",
		"--data", `{"description":"new","new_password":"hidden"}`))
	if len(reqs) != 1 || reqs[0]["method"] != "POST" || !strings.HasSuffix(fmt.Sprint(reqs[0]["url"]), "/api/resource/ToDo") {
		t.Errorf("create plan = %v", reqs)
	}
	body, _ := reqs[0]["body"].(map[string]interface{})
	if body["description"] != "new" || body["new_password"] != "***" {
		t.Errorf("create body = %v (secrets must be redacted)", body)
	}

	reqs = dryTPlan(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--dry-run", "--json",
		"--data", `{"status":"Closed","description":"alpha"}`))
	changes, _ := reqs[0]["changes"].(map[string]interface{})
	if reqs[0]["method"] != "PUT" || len(changes) != 1 || changes["status"] == nil {
		t.Errorf("update plan = %v", reqs)
	}

	// No prompt without --yes: nothing would be deleted.
	reqs = dryTPlan(t, cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "TD-2", "--dry-run", "--json"))
	if reqs[0]["method"] != "DELETE" {
		t.Errorf("delete plan = %v", reqs)
	}
	// A dry run of a missing document fails like the real run.
	lcTCode(t, cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "nope", "--dry-run"), exitNotFound)

	if n := dryTWrites(s); n != 0 {
		t.Fatalf("%d writes reached the site", n)
	}
	if d, _ := s.Doc("ToDo", "TD-1"); d["status"] != "Open" || s.Count("ToDo") != 3 {
		t.Errorf("site changed: %v, %d docs", d, s.Count("ToDo"))
	}

	// The table view lists the request and the changes.
	r := cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--dry-run", "--data", `{"status":"Closed"}`))
	for _, want := range []string{"PUT ", "/api/resource/ToDo/TD-1", `"status": "Closed"`, "Open"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("table plan lacks %q:\n%s", want, r.Stdout)
		}
	}
	if !strings.Contains(r.Stderr, "Dry run: nothing was sent.") {
		t.Errorf("stderr = %q", r.Stderr)
	}
}

func TestDryRunBulk(t *testing.T) {
	s := cmdTSite(t)
	reqs := dryTPlan(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--dry-run", "--json", "--concurrency", "4",
		"--data", `[{"description":"a"},{"description":"b"},{"description":"c"}]`))
	if len(reqs) != 3 {
		t.Errorf("bulk-create plan = %v", reqs)
	}
	reqs = dryTPlan(t, cmdTRun(t, s, "bulk-delete", "-d", "ToDo", "--names", "TD-1,TD-2", "--dry-run", "--json"))
	if len(reqs) != 2 || reqs[1]["method"] != "DELETE" {
		t.Errorf("bulk-delete plan = %v", reqs)
	}
	if n := dryTWrites(s); n != 0 || s.Count("ToDo") != 3 {
		t.Fatalf("%d writes, %d docs", n, s.Count("ToDo"))
	}
}

func TestDryRunLifecycle(t *testing.T) {
	s := lcTSite(t)
	reqs := dryTPlan(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-1", "--dry-run", "--json"))
	if !strings.HasSuffix(fmt.Sprint(reqs[0]["url"]), "/api/method/frappe.client.submit") {
		t.Errorf("submit plan = %v", reqs)
	}
	reqs = dryTPlan(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-3", "--dry-run", "--json"))
	if b, _ := reqs[0]["body"].(map[string]interface{}); b["amended_from"] != "SO-3" {
		t.Errorf("amend plan = %v", reqs)
	}
	dryTPlan(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2", "--dry-run", "--json"))
	dryTPlan(t, cmdTRun(t, s, "rename-doc", "-d", "Sales Order", "-n", "SO-1", "--to", "X", "--merge", "--dry-run", "--json"))
	// A state error still fails: the dry run reads what the real run reads.
	lcTCode(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-1", "--dry-run"), exitValidation, "not cancelled")
	lcTCode(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2", "--check", "--dry-run"), exitUsage)
	if n := dryTWrites(s); n != 0 {
		t.Fatalf("%d writes reached the site", n)
	}
	for name, want := range map[string]string{"SO-1": "0", "SO-2": "1", "SO-3": "2"} {
		if ds := lcTDocstatus(t, s, "Sales Order", name); ds != want {
			t.Errorf("%s docstatus = %s, want %s", name, ds, want)
		}
	}
}

func TestDryRunSendsNothingForArbitraryRequests(t *testing.T) {
	s := cmdTSite(t)
	before := len(s.Requests())
	reqs := dryTPlan(t, cmdTRun(t, s, "call-method", "--method", "frappe.client.get_count", "--get", "--args", `{"doctype":"ToDo"}`, "--dry-run", "--json"))
	if reqs[0]["method"] != "GET" || !strings.Contains(fmt.Sprint(reqs[0]["url"]), "doctype=ToDo") {
		t.Errorf("call-method plan = %v", reqs)
	}
	reqs = dryTPlan(t, cmdTRun(t, s, "api", "/api/resource/ToDo", "-f", "limit=1", "--dry-run", "--json"))
	if reqs[0]["method"] != "GET" || !strings.Contains(fmt.Sprint(reqs[0]["url"]), "limit=1") {
		t.Errorf("api plan = %v", reqs)
	}
	reqs = dryTPlan(t, cmdTRunStdin(t, s, `{"usr":"a","pwd":"p4ss"}`, "api", "/api/method/login", "--input", "-", "--dry-run", "--json"))
	if b, _ := reqs[0]["body"].(map[string]interface{}); reqs[0]["method"] != "POST" || b["usr"] != "a" || b["pwd"] != "***" {
		t.Errorf("api --input plan = %v", reqs)
	}
	if n := len(s.Requests()) - before; n != 0 {
		t.Errorf("%d requests reached the site", n)
	}
}

func TestDryRunWorkflowBulk(t *testing.T) {
	s := lcTWorkflowSite(t)
	reqs := dryTPlan(t, cmdTRun(t, s, "workflow", "bulk-apply", "-d", "Leave Application", "--names", "LA-1,LA-2", "--action", "Approve", "--dry-run", "--json"))
	if len(reqs) != 2 {
		t.Errorf("plan = %v", reqs)
	}
	if d, _ := s.Doc("Leave Application", "LA-1"); d["workflow_state"] != "Open" || dryTWrites(s) != 0 {
		t.Errorf("site changed: %v", d)
	}
}

func TestDryRunSessionSite(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "password")
	r := runFFC(t, cfg, "", "create-doc", "-d", "ToDo", "--data", `{"description":"x"}`, "--dry-run")
	cmdTOK(t, r)
	if n := dryTWrites(s); n != 0 || s.Logouts() != s.Logins() {
		t.Errorf("%d writes, %d logins, %d logouts", n, s.Logins(), s.Logouts())
	}
	if strings.Contains(r.Stdout+r.Stderr, frappetest.Password) {
		t.Errorf("password printed")
	}
}

// TestDryRunEveryWriteCommand covers the write commands the other dry-run
// tests do not. Tests have no terminal, so a command that still asked for
// confirmation would fail with "pass --yes".
func TestDryRunEveryWriteCommand(t *testing.T) {
	s := lcTSite(t)
	s.Add("Deleted Document",
		map[string]interface{}{"name": "del-1", "deleted_doctype": "Sales Order", "deleted_name": "SO-9", "restored": json.Number("0")})
	s.Add("ToDo", map[string]interface{}{"name": "TD-1", "status": "Open", "amount": json.Number("1500.0"), "done": json.Number("0")})

	for _, args := range [][]string{
		{"discard-doc", "-d", "Sales Order", "-n", "SO-1"},
		{"restore-doc", "--deleted", "del-1"},
		{"copy-doc", "-d", "Sales Order", "-n", "SO-2"},
		{"bulk-update", "-d", "ToDo", "--data", `[{"name":"TD-1","status":"Closed"}]`},
	} {
		reqs := dryTPlan(t, cmdTRun(t, s, append(args, "--dry-run", "--json")...))
		if len(reqs) == 0 {
			t.Errorf("%s: empty plan", args[0])
		}
	}

	// Equal values written with another literal are not changes.
	reqs := dryTPlan(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--dry-run", "--json",
		"--data", `{"amount":1500,"done":false,"status":"Closed"}`))
	if changes, _ := reqs[0]["changes"].(map[string]interface{}); len(changes) != 1 || changes["status"] == nil {
		t.Errorf("changes = %v", reqs[0]["changes"])
	}

	// bulk-delete checks each document exists, like delete-doc.
	lcTCode(t, cmdTRun(t, s, "bulk-delete", "-d", "ToDo", "--names", "TD-1,nope", "--dry-run"), exitNotFound)

	// Headers set for the request appear in the plan, credentials do not.
	reqs = dryTPlan(t, cmdTRunStdin(t, s, "usr=a&pwd=hunter2", "api", "/api/method/login", "--input", "-",
		"-H", "Content-Type: application/x-www-form-urlencoded", "-H", "X-API-Key: k3y", "--dry-run", "--json"))
	h, _ := reqs[0]["headers"].(map[string]interface{})
	if reqs[0]["body"] != "usr=a&pwd=***" || h["Content-Type"] != "application/x-www-form-urlencoded" || h["X-Api-Key"] != "***" {
		t.Errorf("api plan = %v", reqs[0])
	}

	if n := dryTWrites(s); n != 0 {
		t.Fatalf("%d writes reached the site", n)
	}
}

func TestDryRunWorkflowApply(t *testing.T) {
	s := lcTWorkflowSite(t)
	reqs := dryTPlan(t, cmdTRun(t, s, "workflow", "apply", "-d", "Leave Application", "-n", "LA-1", "--action", "Approve", "--dry-run", "--json"))
	if !strings.HasSuffix(fmt.Sprint(reqs[0]["url"]), "frappe.model.workflow.apply_workflow") {
		t.Errorf("plan = %v", reqs)
	}
	if d, _ := s.Doc("Leave Application", "LA-1"); d["workflow_state"] != "Open" || dryTWrites(s) != 0 {
		t.Errorf("site changed: %v", d)
	}
}
