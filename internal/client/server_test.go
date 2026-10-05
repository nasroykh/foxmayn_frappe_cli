package client

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func serverTClient(t *testing.T, s *frappetest.Site) *FrappeClient {
	t.Helper()
	c, err := New(context.Background(), &config.SiteConfig{URL: s.URL + "/", APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseMajor(t *testing.T) {
	for in, want := range map[string]int{
		"16.36.1": 16, "v15.2.0-dev": 15, " 14.0.0 ": 14, "0.0.1": 0, "": 0, "dev": 0, "x16": 0, "9999999.1": 0,
	} {
		if got := ParseMajor(in); got != want {
			t.Errorf("ParseMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestServerVersions(t *testing.T) {
	s := frappetest.New(t)
	s.SetApps(map[string]frappetest.App{
		"frappe":  {Title: "Frappe Framework", Version: "15.50.0", Branch: "version-15"},
		"erpnext": {Title: "ERPNext", Version: "15.40.1", Branch: "version-15"},
	})
	c := serverTClient(t, s)
	info, err := c.ServerVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.FrappeVersion() != "15.50.0" || info.FrappeMajor() != 15 || info.Version("erpnext") != "15.40.1" || info.Version("hrms") != "" {
		t.Errorf("info = %+v", info)
	}
	if info.URL != s.URL {
		t.Errorf("URL = %q, want %q (no trailing slash)", info.URL, s.URL)
	}
	if got := info.AppNames(); !reflect.DeepEqual(got, []string{"erpnext", "frappe"}) {
		t.Errorf("AppNames = %v", got)
	}
	var none *ServerInfo
	if none.FrappeMajor() != 0 || none.AppNames() != nil {
		t.Error("a nil ServerInfo must answer 0 / nil")
	}

	// An empty or malformed reply is an error, not an empty server.
	s.HandleMethod("frappe.utils.change_log.get_versions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	if _, err := c.ServerVersions(context.Background()); err == nil {
		t.Error("an empty app list must be an error")
	}
}

func TestIdentityCalls(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	s.SetUser("jane@example.com", "Sales User", "Accounts User")
	c := serverTClient(t, s)
	ctx := context.Background()

	if u, err := c.LoggedUser(ctx); err != nil || u != "jane@example.com" {
		t.Fatalf("LoggedUser = %q, %v", u, err)
	}
	roles, err := c.UserRoles(ctx, "jane@example.com")
	if err != nil || !reflect.DeepEqual(roles, []string{"Accounts User", "Sales User"}) {
		t.Fatalf("UserRoles = %v, %v", roles, err)
	}
	req := s.RequestsTo("GET", "/api/method/frappe.client.get_list")
	if len(req) != 1 || req[0].Query.Get("parent") != "User" || req[0].Query.Get("doctype") != "Has Role" {
		t.Errorf("the roles must be listed as Has Role rows with parent=User: %+v", req)
	}

	if ok, err := c.HasPermission(ctx, "ToDo", "TD-1", "write"); err != nil || !ok {
		t.Errorf("HasPermission = %v, %v", ok, err)
	}
	s.Deny("ToDo", "write")
	if ok, err := c.HasPermission(ctx, "ToDo", "TD-1", "write"); err != nil || ok {
		t.Errorf("a denied permission must be false, got %v, %v", ok, err)
	}
	perms, err := c.DocPermissions(ctx, "ToDo", "TD-1")
	if err != nil || perms["read"] != 1 || perms["write"] != 0 {
		t.Errorf("DocPermissions = %v, %v", perms, err)
	}
	if _, ok := perms["if_owner"]; ok {
		t.Error("if_owner is not a right")
	}

	// A missing document is a 404, an unknown DocType a 403.
	var api *APIError
	if _, err := c.HasPermission(ctx, "ToDo", "nope", "read"); !errors.As(err, &api) || api.Status != 404 {
		t.Errorf("missing document: %v", err)
	}
	if _, err := c.HasPermission(ctx, "Nope", "x", "read"); !errors.As(err, &api) || api.Status != 403 {
		t.Errorf("unknown DocType: %v", err)
	}
}

// A document the controller refuses comes back from Frappe as {"null": 0}
// (get_doc_permissions returns {ptype: 0} with ptype None).
func TestDocPermissionsControllerRefusal(t *testing.T) {
	s := frappetest.New(t)
	s.HandleMethod("frappe.client.get_doc_permissions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"permissions": map[string]interface{}{"null": 0}}, nil
	})
	perms, err := serverTClient(t, s).DocPermissions(context.Background(), "ToDo", "x")
	if err != nil || len(perms) != 0 {
		t.Errorf("perms = %v, %v; want an empty set", perms, err)
	}
}

func TestEvalDocTypePermission(t *testing.T) {
	meta := &DocTypeMeta{Name: "X", IsSubmittable: true, AllowImport: true, Permissions: []map[string]interface{}{
		{"role": "Sales User", "permlevel": 0, "read": 1, "write": 1, "create": 1},
		{"role": "Sales Manager", "permlevel": 0, "read": 1, "delete": 1},
		{"role": "Sales User", "permlevel": 1, "delete": 1},                             // field level: grants nothing
		{"role": "Accounts User", "permlevel": 0, "read": 1, "write": 1, "if_owner": 1}, // own documents only
		{"role": "All", "permlevel": 0, "select": 1},
		{"role": "Desk User", "permlevel": 0, "print": 1},
	}}
	roles := []string{"Sales User", "Accounts User", "All", "Guest"}
	for _, c := range []struct {
		perm            string
		allowed, ownerO bool
	}{
		{"read", true, false},
		{"create", true, false},
		{"delete", false, false}, // Sales Manager is not held; the permlevel 1 row does not count
		{"select", true, false},  // automatic role All
		{"print", false, false},  // Desk User is not among the roles
		{"submit", false, false},
	} {
		got := EvalDocTypePermission(meta, roles, c.perm)
		if got.Allowed != c.allowed || got.OwnerOnly != c.ownerO {
			t.Errorf("%s: %+v, want allowed=%v ownerOnly=%v", c.perm, got, c.allowed, c.ownerO)
		}
	}
	if got := EvalDocTypePermission(meta, append(roles, "Desk User"), "print"); !got.Allowed {
		t.Errorf("print with Desk User: %+v", got)
	}
}

// if_owner follows permissions.py get_role_permissions: it narrows a right
// only when no other applicable row grants it, and never create.
func TestEvalDocTypePermissionIfOwner(t *testing.T) {
	ownerOnly := &DocTypeMeta{Name: "X", IsSubmittable: true, Permissions: []map[string]interface{}{
		{"role": "Clerk", "permlevel": 0, "select": 1, "read": 1, "write": 1, "create": 1, "delete": 1, "submit": 1, "if_owner": 1},
	}}
	mixed := &DocTypeMeta{Name: "X", IsSubmittable: true, Permissions: []map[string]interface{}{
		{"role": "Clerk", "permlevel": 0, "read": 1, "write": 1, "create": 1, "if_owner": 1},
		{"role": "Clerk", "permlevel": 0, "read": 1},                              // plain read beside the owner row
		{"role": "Other", "permlevel": 0, "write": 1, "delete": 1, "if_owner": 0}, // not held
	}}
	for _, c := range []struct {
		name    string
		meta    *DocTypeMeta
		perm    string
		allowed bool
		owner   bool
	}{
		{"create is never owner-only", ownerOnly, "create", true, false},
		{"read stays allowed, narrowed", ownerOnly, "read", true, true},
		{"select stays allowed, narrowed", ownerOnly, "select", true, true},
		{"write is owner-only", ownerOnly, "write", false, true},
		{"delete is owner-only", ownerOnly, "delete", false, true},
		{"submit is owner-only", ownerOnly, "submit", false, true},
		{"a right nobody grants is plain denied", ownerOnly, "share", false, false},
		{"a plain row beside if_owner wins", mixed, "read", true, false},
		{"owner row alone narrows write", mixed, "write", false, true},
		{"create beside if_owner rows", mixed, "create", true, false},
		{"select is implied by read", mixed, "select", true, false},
	} {
		got := EvalDocTypePermission(c.meta, []string{"Clerk"}, c.perm)
		if got.Allowed != c.allowed || got.OwnerOnly != c.owner {
			t.Errorf("%s: %+v, want allowed=%v ownerOnly=%v", c.name, got, c.allowed, c.owner)
		}
	}
}

func TestEvalDocTypePermissionMetaFlags(t *testing.T) {
	rows := []map[string]interface{}{
		{"role": "Clerk", "permlevel": 0, "submit": 1, "cancel": 1, "amend": 1, "import": 1, "read": 1},
	}
	roles := []string{"Clerk"}
	plain := &DocTypeMeta{Name: "X", Permissions: rows}
	for _, p := range []string{"submit", "cancel", "amend", "import"} {
		if got := EvalDocTypePermission(plain, roles, p); got.Allowed {
			t.Errorf("%s allowed on a DocType that is neither submittable nor importable", p)
		}
	}
	full := &DocTypeMeta{Name: "X", IsSubmittable: true, AllowImport: true, Permissions: rows}
	for _, p := range []string{"submit", "cancel", "amend", "import", "read"} {
		if got := EvalDocTypePermission(full, roles, p); !got.Allowed {
			t.Errorf("%s denied on a submittable, importable DocType", p)
		}
	}
	if got := EvalDocTypePermission(plain, roles, "read"); !got.Allowed {
		t.Error("read must not depend on the flags")
	}
}

func TestDocTypePermissionThroughTheSite(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Customer")
	s.DocPerm("Customer", map[string]interface{}{"role": "Sales User", "permlevel": 0, "read": 1, "create": 1})
	s.SetUser("jane@example.com", "Sales User")
	c := serverTClient(t, s)
	ctx := context.Background()
	if p, err := c.DocTypePermission(ctx, "Customer", "create"); err != nil || !p.Allowed {
		t.Errorf("create: %+v, %v", p, err)
	}
	if p, err := c.DocTypePermission(ctx, "Customer", "delete"); err != nil || p.Allowed {
		t.Errorf("delete: %+v, %v", p, err)
	}
	// Administrator may do everything and costs one request.
	s.SetUser("Administrator")
	n := len(s.Requests())
	if p, err := c.DocTypePermission(ctx, "Customer", "delete"); err != nil || !p.Allowed {
		t.Errorf("Administrator: %+v, %v", p, err)
	}
	if got := len(s.Requests()) - n; got != 1 {
		t.Errorf("Administrator took %d requests, want 1", got)
	}
}

// Desk User counts only for a user known to be a System User. When the user
// type cannot be read the role is left out and the note says what it would
// have changed.
func TestDocTypePermissionDeskUser(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Customer")
	s.AddDocType("User")
	s.DocPerm("Customer", map[string]interface{}{"role": "Desk User", "permlevel": 0, "read": 1})
	s.SetUser("jane@example.com", "Sales User")
	c := serverTClient(t, s)
	ctx := context.Background()

	// Unreadable user type: not counted, and the note says why.
	p, err := c.DocTypePermission(ctx, "Customer", "read")
	if err != nil || p.Allowed {
		t.Fatalf("unknown user type: %+v, %v", p, err)
	}
	if !strings.Contains(p.Note, "Desk User") || !strings.Contains(p.Note, "allowed") {
		t.Errorf("note = %q", p.Note)
	}
	// No note when Desk User would change nothing.
	if p, err := c.DocTypePermission(ctx, "Customer", "delete"); err != nil || p.Note != "" {
		t.Errorf("delete: %+v, %v", p, err)
	}

	// A System User gets the role.
	s.Add("User", map[string]interface{}{"name": "jane@example.com", "user_type": "System User"})
	if p, err := c.DocTypePermission(ctx, "Customer", "read"); err != nil || !p.Allowed || p.Note != "" {
		t.Errorf("System User: %+v, %v", p, err)
	}
	// A Website User does not.
	s.Add("User", map[string]interface{}{"name": "jane@example.com", "user_type": "Website User"})
	if p, err := c.DocTypePermission(ctx, "Customer", "read"); err != nil || p.Allowed || p.Note != "" {
		t.Errorf("Website User: %+v, %v", p, err)
	}
}

func TestDocTypePermissionChildTable(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Invoice Item")
	s.DocTypeFlags("Invoice Item", map[string]interface{}{"istable": 1})
	s.SetUser("jane@example.com", "Sales User")
	c := serverTClient(t, s)
	_, err := c.DocTypePermission(context.Background(), "Invoice Item", "read")
	var ct *ChildTableError
	if !errors.As(err, &ct) || ct.DocType != "Invoice Item" || !strings.Contains(err.Error(), "parent") {
		t.Errorf("err = %v, want a ChildTableError naming the parent", err)
	}
}
