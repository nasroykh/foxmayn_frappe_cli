//go:build contract

package cmd

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	contractUser     = "ffc-contract-user@example.com"
	contractUserPwd  = "Zx9!qwErty-ffc-2026"
	contractUserRole = "Desk User"
)

// teardownContractUser removes the fixture user. It is idempotent.
func teardownContractUser(c *client.FrappeClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 30e9)
	defer cancel()
	_ = c.DeleteDoc(ctx, "User", contractUser)
	// Creating a user also creates a Contact, which deleting the user leaves
	// behind (and which global search would then find).
	if rows, err := c.GetList(ctx, "Contact", client.ListOptions{Filters: `{"email_id":"` + contractUser + `"}`, Limit: -1}); err == nil {
		for _, r := range rows {
			if name, ok := r["name"].(string); ok {
				_ = c.DeleteDoc(ctx, "Contact", name)
			}
		}
	}
}

// contractIdentity pins what T2.4 relies on: the get_versions shape, the
// has_permission / get_doc_permissions shapes and their errors, and what a
// user who is not a System Manager can read about themselves.
func contractIdentity(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)

	// get_versions: every installed app with a version, Frappe first of all.
	info, err := c.ServerVersions(ctx)
	if err != nil {
		t.Fatalf("get_versions: %v", err)
	}
	fv := info.Apps["frappe"]
	if fv.Version == "" || fv.Title == "" || info.FrappeMajor() < 14 {
		t.Errorf("frappe = %+v (major %d)", fv, info.FrappeMajor())
	}
	t.Logf("server: frappe %s, %d apps", fv.Version, len(info.Apps))

	// The calls the fake models answer with the same shapes.
	user, err := c.LoggedUser(ctx)
	if err != nil || user == "" || user == "Guest" {
		t.Fatalf("LoggedUser = %q, %v", user, err)
	}
	roles, err := c.UserRoles(ctx, user)
	if err != nil || len(roles) == 0 {
		t.Errorf("UserRoles(%s) = %v, %v", user, roles, err)
	}
	name := createContractDoc(t, c, map[string]interface{}{"title": "identity"})
	if ok, err := c.HasPermission(ctx, contractDT, name, "read"); err != nil || !ok {
		t.Errorf("HasPermission(read) = %v, %v", ok, err)
	}
	perms, err := c.DocPermissions(ctx, contractDT, name)
	if err != nil || perms["read"] != 1 || perms["write"] != 1 || perms["submit"] != 1 || len(perms) < 10 {
		t.Errorf("DocPermissions = %v, %v", perms, err)
	}
	// An unknown permission type is false, not an error.
	if ok, err := c.HasPermission(ctx, contractDT, name, "frobnicate"); err != nil || (user != "Administrator" && ok) {
		t.Errorf("HasPermission(frobnicate) = %v, %v", ok, err)
	}
	// docname is required: the method cannot judge a DocType alone.
	_, err = c.CallMethod(ctx, "frappe.client.has_permission", map[string]interface{}{"doctype": contractDT}, true)
	var api *client.APIError
	if !errors.As(err, &api) || api.Status < 400 {
		t.Errorf("has_permission without docname: %v", err)
	}

	// whoami as built for the CLI.
	cfg := *sc
	cfg.Name = "contract"
	w, err := buildWhoami(ctx, c, &cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	if w.User != user || w.RolesSource != "has_role" || len(w.Roles) == 0 || w.Server == nil || w.Server.Frappe != fv.Version || w.FullName == "" {
		t.Errorf("whoami = %+v", w)
	}
	invalidateServerCache(&cfg)

	t.Run("a user without System Manager", func(t *testing.T) { contractIdentityUser(t, c, sc) })
	t.Run("denials match the fake", func(t *testing.T) { contractPermissionErrors(t, c, sc) })
}

// contractNonAdmin returns a client logged in as a fresh user whose only
// role is Desk User, and a cleanup for it.
func contractNonAdmin(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) *client.FrappeClient {
	t.Helper()
	ctx := contractCtx(t)
	teardownContractUser(c)
	if _, err := c.CreateDoc(ctx, "User", map[string]interface{}{
		"email": contractUser, "first_name": "FFC Contract", "send_welcome_email": 0,
		"new_password": contractUserPwd, "roles": []interface{}{map[string]interface{}{"role": contractUserRole}},
	}); err != nil {
		t.Fatalf("creating the fixture user: %v", err)
	}
	t.Cleanup(func() { teardownContractUser(c) })
	uc, err := client.New(ctx, &config.SiteConfig{URL: sc.URL, Username: contractUser, Password: contractUserPwd})
	if err != nil {
		t.Fatalf("login as the fixture user: %v", err)
	}
	t.Cleanup(uc.CloseQuietly)
	return uc
}

func contractIdentityUser(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	uc := contractNonAdmin(t, c, sc)
	ctx := contractCtx(t)

	if u, err := uc.LoggedUser(ctx); err != nil || u != contractUser {
		t.Fatalf("LoggedUser = %q, %v", u, err)
	}
	// Any logged-in user may call get_versions.
	if info, err := uc.ServerVersions(ctx); err != nil || info.FrappeVersion() == "" {
		t.Errorf("get_versions as a plain user: %v", err)
	}
	// The user reads their own document, but not its roles table
	// (permission level 1): that is why whoami lists Has Role rows.
	doc, err := uc.GetDoc(ctx, "User", contractUser)
	if err != nil {
		t.Fatalf("a user must read their own User document: %v", err)
	}
	if rows, _ := doc["roles"].([]interface{}); len(rows) != 0 {
		t.Errorf("the roles table is readable by a plain user: %v", rows)
	}
	if doc["full_name"] == nil || doc["full_name"] == "" {
		t.Errorf("full_name missing: %v", doc["full_name"])
	}
	roles, err := uc.UserRoles(ctx, contractUser)
	if err != nil || !reflect.DeepEqual(roles, []string{contractUserRole}) {
		t.Errorf("UserRoles = %v, %v", roles, err)
	}

	w, err := buildWhoami(ctx, uc, &config.SiteConfig{Name: "contract-user", URL: sc.URL, Username: contractUser, Password: contractUserPwd}, true)
	if err != nil {
		t.Fatal(err)
	}
	invalidateServerCache(&config.SiteConfig{Name: "contract-user", URL: sc.URL})
	if w.User != contractUser || w.RolesSource != "has_role" || !reflect.DeepEqual(w.Roles, []string{contractUserRole}) || w.FullName == "" {
		t.Errorf("whoami = %+v", w)
	}

	// The fixture DocType only grants System Manager: this user is refused,
	// whichever way it is asked.
	name := createContractDoc(t, c, map[string]interface{}{"title": "hidden"})
	if ok, err := uc.HasPermission(ctx, contractDT, name, "read"); err != nil || ok {
		t.Errorf("HasPermission(read) = %v, %v; want false", ok, err)
	}
	if perms, err := uc.DocPermissions(ctx, contractDT, name); err != nil || perms["read"] != 0 || perms["write"] != 0 {
		t.Errorf("DocPermissions = %v, %v", perms, err)
	}
	if p, err := uc.DocTypePermission(ctx, contractDT, "create"); err != nil || p.Allowed {
		t.Errorf("DocTypePermission(create) = %+v, %v", p, err)
	}
	if p, err := c.DocTypePermission(ctx, contractDT, "create"); err != nil || !p.Allowed {
		t.Errorf("Administrator DocTypePermission(create) = %+v, %v", p, err)
	}
	// ToDo gives role All read and write: the role rows say so for this user.
	if p, err := uc.DocTypePermission(ctx, "ToDo", "write"); err != nil || !p.Allowed {
		t.Errorf("ToDo write via the role All = %+v, %v", p, err)
	}
	if p, err := uc.DocTypePermission(ctx, "ToDo", "submit"); err != nil || p.Allowed {
		t.Errorf("ToDo submit = %+v, %v", p, err)
	}

	// Administrator is allowed everything, a missing document included.
	if ok, err := c.HasPermission(ctx, contractDT, "ffc-contract-missing", "write"); err != nil || !ok {
		t.Errorf("Administrator on a missing document = %v, %v", ok, err)
	}

	contractIfOwner(t, c, uc)

	// A child table is the parent's business.
	var child *client.ChildTableError
	if _, err := uc.DocTypePermission(ctx, contractChild, "read"); !errors.As(err, &child) {
		t.Errorf("DocTypePermission on a child table: %v, want a ChildTableError", err)
	}
	// A DocType that is not submittable cannot grant submit, whatever its rows say.
	if p, err := uc.DocTypePermission(ctx, "Note", "submit"); err != nil || p.Allowed {
		t.Errorf("Note submit = %+v, %v", p, err)
	}
}

// contractIfOwner pins the if_owner semantics EvalDocTypePermission mirrors,
// on the stock Note DocType: role Desk User has a plain read row and one
// if_owner row for create, write, delete, share. The role-level answer and
// the site's own answer for a real document must agree: create is never
// owner-only, write and delete hold only on the user's own notes, read and
// select hold for everyone's.
func contractIfOwner(t *testing.T, admin, uc *client.FrappeClient) {
	ctx := contractCtx(t)
	for _, c := range []struct {
		perm           string
		allowed, owner bool
	}{
		{"read", true, false},
		{"select", true, false},
		{"create", true, false},
		{"write", false, true},
		{"delete", false, true},
	} {
		p, err := uc.DocTypePermission(ctx, "Note", c.perm)
		if err != nil || p.Allowed != c.allowed || p.OwnerOnly != c.owner {
			t.Errorf("Note %s = %+v, %v; want allowed=%v owner_only=%v", c.perm, p, err, c.allowed, c.owner)
		}
	}

	others, err := admin.CreateDoc(ctx, "Note", map[string]interface{}{"title": "ffc contract others", "public": 1})
	if err != nil {
		t.Fatalf("creating a note as Administrator: %v", err)
	}
	t.Cleanup(func() { _ = admin.DeleteDoc(ctx, "Note", others["name"].(string)) })
	mine, err := uc.CreateDoc(ctx, "Note", map[string]interface{}{"title": "ffc contract mine"})
	if err != nil {
		t.Fatalf("creating a note as the fixture user (create is allowed): %v", err)
	}
	t.Cleanup(func() { _ = admin.DeleteDoc(ctx, "Note", mine["name"].(string)) })

	for doc, want := range map[string]map[string]int{
		others["name"].(string): {"read": 1, "write": 0, "delete": 0},
		mine["name"].(string):   {"read": 1, "write": 1, "delete": 1},
	} {
		perms, err := uc.DocPermissions(ctx, "Note", doc)
		if err != nil {
			t.Fatalf("DocPermissions(%s): %v", doc, err)
		}
		for ptype, v := range want {
			if perms[ptype] != v {
				t.Errorf("Note %s: %s = %d, want %d (%v)", doc, ptype, perms[ptype], v, perms)
			}
		}
	}
}

// contractPermissionErrors checks the statuses has_permission answers a user
// who is not Administrator, against the fake.
func contractPermissionErrors(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	uc := contractNonAdmin(t, c, sc)

	fake := frappetest.New(t)
	fake.Add("ToDo", map[string]interface{}{"name": "x"})
	fake.SetUser(contractUser, contractUserRole)
	fc, err := client.New(contractCtx(t), &config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(c *client.FrappeClient) error{
		"missing document": func(c *client.FrappeClient) error {
			_, err := c.HasPermission(contractCtx(t), "ToDo", "ffc-contract-missing", "read")
			return err
		},
		"unknown DocType": func(c *client.FrappeClient) error {
			_, err := c.HasPermission(contractCtx(t), "FFC Contract No Such DocType", "x", "read")
			return err
		},
		"doc permissions of a missing document": func(c *client.FrappeClient) error {
			_, err := c.DocPermissions(contractCtx(t), "ToDo", "ffc-contract-missing")
			return err
		},
		"no docname": func(c *client.FrappeClient) error {
			_, err := c.CallMethod(contractCtx(t), "frappe.client.has_permission", map[string]interface{}{"doctype": "ToDo"}, true)
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			realErr, fakeErr := call(uc), call(fc)
			if realErr == nil || fakeErr == nil {
				t.Fatalf("real = %v, fake = %v; want both to fail", realErr, fakeErr)
			}
			var ra, fa *client.APIError
			if !errors.As(realErr, &ra) || !errors.As(fakeErr, &fa) {
				t.Fatalf("real = %v, fake = %v", realErr, fakeErr)
			}
			// The no-docname TypeError is a 500 on Frappe's current
			// versions; what matters is that both fail the same way.
			if ra.Status != fa.Status {
				t.Errorf("status: real %d (%v), fake %d (%v)", ra.Status, realErr, fa.Status, fakeErr)
			}
		})
	}
}
