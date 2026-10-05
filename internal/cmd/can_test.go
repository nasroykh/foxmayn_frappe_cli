package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// --all prints the table when the answer is "denied" too, and every key the
// site returns, custom permission types included.
func TestCanAllPrintsTableWhenDenied(t *testing.T) {
	s := canTSite(t)
	s.Deny("ToDo", "write")
	s.HandleMethod("frappe.client.get_doc_permissions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"permissions": map[string]interface{}{
			"read": 1, "write": 0, "approve": 1, "if_owner": map[string]interface{}{}, "has_if_owner_enabled": false,
		}}, nil
	})
	r := cmdTRun(t, s, "can", "-d", "ToDo", "-n", "TD-1", "--perm", "write", "--all")
	if r.Code != exitPermission {
		t.Fatalf("exit %d, want %d", r.Code, exitPermission)
	}
	cmdTHas(t, r.Stdout, "read", "write", "approve")
	if strings.Contains(r.Stdout, "if_owner") || strings.Contains(r.Stdout, "has_if_owner_enabled") {
		t.Errorf("flags are not rights: %s", r.Stdout)
	}
}

// A custom permission type is the site's to judge: accepted with --name only.
func TestCanCustomPermissionType(t *testing.T) {
	s := canTSite(t)
	r := cmdTRun(t, s, "--json", "can", "-d", "ToDo", "-n", "TD-1", "--perm", "Approve_Leave")
	if r.Code != exitPermission || cmdTObj(t, r)["perm"] != "approve_leave" {
		t.Errorf("custom type with --name: exit %d, %s", r.Code, r.Stdout)
	}
	for _, bad := range []string{"1abc", "has space", "a-b", "x;y"} {
		if r := cmdTRun(t, s, "can", "-d", "ToDo", "-n", "TD-1", "--perm", bad); r.Code != exitUsage {
			t.Errorf("--perm %q: exit %d, want usage", bad, r.Code)
		}
	}
	if r := cmdTRun(t, s, "can", "-d", "Customer", "--perm", "approve"); r.Code != exitUsage || !strings.Contains(r.Err.Error(), "--name") {
		t.Errorf("custom type without --name: exit %d, %v", r.Code, r.Err)
	}
}

// The DocType-level answer follows Frappe: create is never owner-only, and an
// owner-only right keeps read and denies the rest.
func TestCanDocTypeIfOwner(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Note")
	s.DocTypeFlags("Note", map[string]interface{}{"is_submittable": 1})
	s.DocPerm("Note", map[string]interface{}{"role": "Clerk", "permlevel": 0, "read": 1, "write": 1, "create": 1, "submit": 1, "if_owner": 1})
	s.SetUser("jane@example.com", "Clerk")
	if out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "can", "-d", "Note", "--perm", "create"))); out["allowed"] != true || out["owner_only"] != nil {
		t.Errorf("create with an if_owner row: %v", out)
	}
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "can", "-d", "Note", "--perm", "read")))
	if out["allowed"] != true || out["owner_only"] != true {
		t.Errorf("read: %v", out)
	}
	cmdTOK(t, cmdTRun(t, s, "can", "-d", "Note", "--perm", "select"))
	r := cmdTRun(t, s, "--json", "can", "-d", "Note", "--perm", "write")
	if out := cmdTObj(t, r); r.Code != exitPermission || out["allowed"] != false || out["owner_only"] != true {
		t.Errorf("write: exit %d, %v", r.Code, out)
	}
	if r := cmdTRun(t, s, "can", "-d", "Note", "--perm", "write"); !strings.Contains(r.Stderr, "only for documents you own") {
		t.Errorf("stderr = %q", r.Stderr)
	}
}

func TestCanDocTypeMetaFlagsAndChildTable(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Plain")
	s.AddDocType("Row")
	s.DocTypeFlags("Row", map[string]interface{}{"istable": 1})
	for _, dt := range []string{"Plain", "Row"} {
		s.DocPerm(dt, map[string]interface{}{"role": "Clerk", "permlevel": 0, "read": 1, "submit": 1, "import": 1})
	}
	s.SetUser("jane@example.com", "Clerk")
	for _, p := range []string{"submit", "import"} {
		if r := cmdTRun(t, s, "can", "-d", "Plain", "--perm", p); r.Code != exitPermission {
			t.Errorf("%s on a DocType without the flag: exit %d", p, r.Code)
		}
	}
	r := cmdTRun(t, s, "can", "-d", "Row", "--perm", "read")
	if r.Code != exitUsage || !strings.Contains(r.Err.Error(), "parent") {
		t.Errorf("child table: exit %d, %v", r.Code, r.Err)
	}
}

// Desk User is not assumed: without a readable user type the answer says what
// it left out.
func TestCanDocTypeDeskUserNote(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Note")
	s.DocPerm("Note", map[string]interface{}{"role": "Desk User", "permlevel": 0, "read": 1})
	s.SetUser("jane@example.com", "Clerk")
	r := cmdTRun(t, s, "--json", "can", "-d", "Note", "--perm", "read")
	out := cmdTObj(t, r)
	if r.Code != exitPermission || out["allowed"] != false || !strings.Contains(out["note"].(string), "Desk User") {
		t.Errorf("exit %d, %v", r.Code, out)
	}
	r = cmdTRun(t, s, "can", "-d", "Note", "--perm", "read")
	cmdTHas(t, r.Stderr, "note:", "Desk User")
}
