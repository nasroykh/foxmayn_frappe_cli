package frappetest_test

import (
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestIdentityEndpoints(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	get := func(path string) resp { return fkDo(t, s, "GET", path, "", fkKey) }

	// get_versions has Frappe's per-app shape.
	apps := get("/api/method/frappe.utils.change_log.get_versions").Body["message"].(map[string]interface{})
	fr := apps["frappe"].(map[string]interface{})
	if fr["version"] != "16.36.1" || fr["title"] != "Frappe Framework" || fr["branch"] != "version-16" {
		t.Errorf("frappe = %v", fr)
	}

	// As Administrator (the default) has_permission is true before anything
	// is looked up, and get_doc_permissions lists every right.
	if r := get("/api/method/frappe.client.has_permission?doctype=Nope&docname=x"); r.Status != 200 || r.Body["message"].(map[string]interface{})["has_permission"] != true {
		t.Errorf("Administrator: %d %s", r.Status, r.Raw)
	}
	perms := get("/api/method/frappe.client.get_doc_permissions?doctype=ToDo&docname=TD-1").Body["message"].(map[string]interface{})["permissions"].(map[string]interface{})
	if perms["read"] != float64(1) || perms["impersonate"] != float64(1) || perms["has_if_owner_enabled"] != false {
		t.Errorf("permissions = %v", perms)
	}

	// Any other user is checked, with Frappe's errors.
	s.SetUser("jane@example.com", "Sales User")
	if r := get("/api/method/frappe.client.has_permission?doctype=ToDo&docname=nope"); r.Status != 404 {
		t.Errorf("missing document: %d", r.Status)
	}
	if r := get("/api/method/frappe.client.has_permission?doctype=Nope&docname=x"); r.Status != 403 {
		t.Errorf("unknown DocType: %d", r.Status)
	}
	if r := get("/api/method/frappe.client.has_permission?doctype=ToDo"); r.Status != 500 || !strings.Contains(r.Raw, "missing 1 required positional argument") {
		t.Errorf("no docname: %d %s", r.Status, r.Raw)
	}
	if r := get("/api/method/frappe.client.has_permission?doctype=ToDo&docname=TD-1&perm_type=bogus"); r.Body["message"].(map[string]interface{})["has_permission"] != false {
		t.Errorf("an unknown permission type is false: %s", r.Raw)
	}
	if got := get("/api/method/frappe.auth.get_logged_user").Body["message"]; got != "jane@example.com" {
		t.Errorf("user = %v", got)
	}
	roles := get(`/api/method/frappe.client.get_list?doctype=Has+Role&parent=User&fields=["role"]`).Body["message"].([]interface{})
	if len(roles) != 1 || roles[0].(map[string]interface{})["role"] != "Sales User" {
		t.Errorf("roles = %v", roles)
	}

	// v2 answers until disabled.
	if r := fkDo(t, s, "GET", "/api/v2/method/ping", "", nil); r.Status != 200 || r.Body["data"] != "pong" {
		t.Errorf("v2 ping: %d %s", r.Status, r.Raw)
	}
	s.DisableV2()
	if r := fkDo(t, s, "GET", "/api/v2/method/ping", "", nil); r.Status != 404 {
		t.Errorf("disabled v2: %d", r.Status)
	}
}
