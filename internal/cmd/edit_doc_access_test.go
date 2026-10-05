package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// edTClerk makes the fake's user a Clerk who writes level 0 and 2 and only
// reads level 1, with phone at level 1, the rows' rate at level 2 and notes
// masked (v16).
func edTClerk(s *frappetest.Site) {
	s.Permlevel("Sales Order", "phone", 1)
	s.Permlevel("Sales Order Item", "rate", 2)
	s.FieldProp("Sales Order", "notes", "mask", 1)
	s.SetUser("clerk@example.com", "Clerk")
	s.DocPerm("Sales Order", map[string]interface{}{"role": "Clerk", "permlevel": 0, "read": 1, "write": 1})
	s.DocPerm("Sales Order", map[string]interface{}{"role": "Clerk", "permlevel": 1, "read": 1})
	s.DocPerm("Sales Order", map[string]interface{}{"role": "Clerk", "permlevel": 2, "read": 1, "write": 1})
}

// Fields whose changes Frappe drops silently (a level the user does not
// write, a masked field) are not offered.
func TestEditDocPermissionLevels(t *testing.T) {
	s := edTSite(t)
	edTClerk(s)
	seen := edTEditor(t, replace("rate: 10.0", "rate: 11.0"))
	r := cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	file := edTFile(t, seen, 0)
	if strings.Contains(file, "phone:") || strings.Contains(file, "notes:") || !strings.Contains(file, "customer: C1") ||
		!strings.Contains(file, "rate: 10.0") {
		t.Errorf("file:\n%s", file)
	}
	if strings.Contains(r.Stderr, "did not keep") {
		t.Errorf("stderr: %s", r.Stderr)
	}

	// Roles unknown: only level-0 fields that are not masked.
	s = edTSite(t)
	edTClerk(s)
	s.Handle("GET /api/method/frappe.client.get_list", frappetest.ErrorHandler(frappetest.Permission("no")))
	seen = edTEditor(t, same)
	r = cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1"))
	file = edTFile(t, seen, 0)
	if !strings.Contains(r.Stderr, "permission levels are unknown") || strings.Contains(file, "rate:") ||
		strings.Contains(file, "notes:") || !strings.Contains(file, "item_code: A") {
		t.Errorf("stderr %s\nfile:\n%s", r.Stderr, file)
	}
}

// The fake drops changes the way Frappe does: what edit-doc would have lost
// without the check above.
func TestFakeDropsChangesWithoutWriteAccess(t *testing.T) {
	s := edTSite(t)
	edTClerk(s)
	cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "Sales Order", "-n", "SO-1", "--data", `{"phone":"1","notes":"n","customer":"C5"}`))
	d, _ := s.Doc("Sales Order", "SO-1")
	if d["phone"] != "0123" || d["notes"] != "line one\nline two" || d["customer"] != "C5" {
		t.Errorf("stored = %v", d)
	}
}

// A change the site does not keep is reported, not hidden behind "Updated".
func TestEditDocWarnsAboutChangesNotKept(t *testing.T) {
	s := edTSite(t)
	s.Handle("PUT /api/resource/Sales Order/SO-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"name":"SO-1","customer":"C1","items":[{"name":"r1","qty":1.0},{"name":"r2","qty":2.0}]}}`))
	}))
	edTEditor(t, func(f string) string {
		f = replace("customer: C1", "customer: C9")(f)
		return replace("qty: 2.0", "qty: 7")(f)
	})
	r := cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	for _, want := range []string{`customer: sent "C9", saved "C1"`, "items row 2 qty: sent 7, saved 2.0", "did not keep 2 change(s)"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	if strings.Contains(r.Stderr, "✓ Updated") {
		t.Errorf("success printed: %s", r.Stderr)
	}
}
