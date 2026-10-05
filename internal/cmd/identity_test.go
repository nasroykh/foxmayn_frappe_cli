package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// ─── whoami ──────────────────────────────────────────────────────────────────

func TestWhoamiJSON(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	s.SetUser("jane@example.com", "Sales User", "Accounts User")
	s.Add("User", map[string]interface{}{"name": "jane@example.com", "full_name": "Jane Doe"})
	s.SetApps(map[string]frappetest.App{
		"frappe":  {Title: "Frappe Framework", Version: "15.50.0", Branch: "version-15"},
		"erpnext": {Title: "ERPNext", Version: "15.40.1", Branch: "version-15"},
	})
	r := cmdTOK(t, cmdTRun(t, s, "--json", "whoami"))
	out := cmdTObj(t, r)
	if out["user"] != "jane@example.com" || out["full_name"] != "Jane Doe" || out["roles_source"] != "has_role" ||
		out["site"] != "t" || out["url"] != s.URL || out["auth"] != "api_key" {
		t.Fatalf("out = %v", out)
	}
	if roles, _ := out["roles"].([]interface{}); len(roles) != 2 || roles[0] != "Accounts User" {
		t.Errorf("roles = %v", out["roles"])
	}
	srv := out["server"].(map[string]interface{})
	if srv["frappe"] != "15.50.0" || srv["major"] != float64(15) || srv["cached"] != false {
		t.Errorf("server = %v", srv)
	}
	if apps := srv["apps"].(map[string]interface{}); apps["erpnext"].(map[string]interface{})["version"] != "15.40.1" {
		t.Errorf("apps = %v", apps)
	}
	if strings.Contains(r.Stdout+r.Stderr, frappetest.APISecret) {
		t.Error("the secret was printed")
	}

	// The second run is served from the cache; --refresh asks again.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "whoami"))
	if cmdTObj(t, r)["server"].(map[string]interface{})["cached"] != true || versionCalls(s) != 1 {
		t.Errorf("not cached: %s (%d calls)", r.Stdout, versionCalls(s))
	}
	cmdTOK(t, cmdTRun(t, s, "--json", "whoami", "--refresh"))
	if versionCalls(s) != 2 {
		t.Errorf("--refresh: %d calls, want 2", versionCalls(s))
	}
}

func TestWhoamiTable(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	s.SetUser("jane@example.com", "Sales User")
	r := cmdTOK(t, cmdTRun(t, s, "whoami"))
	cmdTHas(t, r.Stdout, "jane@example.com", "Sales User", "frappe", "16.36.1")
}

// A user who cannot list Has Role rows falls back to the User document, and
// to a note when that has no roles either.
func TestWhoamiRolesFallback(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	s.SetUser("jane@example.com")
	s.HandleMethod("frappe.client.get_list", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Permission("no")
	})

	s.Add("User", map[string]interface{}{"name": "jane@example.com", "roles": []interface{}{map[string]interface{}{"role": "Blogger"}}})
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "whoami")))
	if out["roles_source"] != "user_doc" || out["roles"].([]interface{})[0] != "Blogger" {
		t.Errorf("User document fallback: %v", out)
	}

	// Frappe sends the User document without its roles (permission level 1).
	s.Add("User", map[string]interface{}{"name": "jane@example.com", "roles": []interface{}{}})
	out = cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "whoami")))
	notes, _ := out["notes"].([]interface{})
	if out["roles_source"] != "unavailable" || len(out["roles"].([]interface{})) != 0 || len(notes) == 0 ||
		!strings.Contains(notes[0].(string), "roles unavailable") {
		t.Errorf("unavailable roles must be said so: %v", out)
	}
}

func TestWhoamiUserNotReadableIsANote(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	s.SetUser("jane@example.com", "Sales User") // no User document: GET /api/resource/User/jane is a 404
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "whoami")))
	if out["roles_source"] != "has_role" || out["full_name"] != nil {
		t.Errorf("out = %v", out)
	}
	if notes, _ := out["notes"].([]interface{}); len(notes) != 1 || !strings.Contains(notes[0].(string), "not readable") {
		t.Errorf("notes = %v", out["notes"])
	}
}

func TestWhoamiGuestAndErrors(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	s.SetUser("Guest")
	// Guest: the result is printed, then the command fails like ping (exit 3).
	r := cmdTRun(t, s, "--json", "whoami")
	if r.Code != exitAuth {
		t.Errorf("Guest exits %d, want %d (stderr %s)", r.Code, exitAuth, r.Stderr)
	}
	out := cmdTObj(t, r)
	if out["user"] != "Guest" || out["notes"] == nil {
		t.Errorf("Guest: %v", out)
	}

	s.HandleMethod("frappe.auth.get_logged_user", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, &frappetest.Error{Status: 401, ExcType: "AuthenticationError", Message: "bad"}
	})
	if r := cmdTRun(t, s, "whoami"); r.Code != exitAuth {
		t.Errorf("a rejected login exits %d, want %d", r.Code, exitAuth)
	}
}

// Versions that cannot be read are a note: whoami still names the user.
func TestWhoamiVersionsUnavailable(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	s.HandleMethod("frappe.utils.change_log.get_versions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Permission("no")
	})
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "whoami")))
	if out["user"] != "Administrator" || out["server"] != nil || out["notes"] == nil {
		t.Errorf("out = %v", out)
	}
}

// ─── ping ────────────────────────────────────────────────────────────────────

func TestPingNamesTheUser(t *testing.T) {
	s := frappetest.New(t)
	s.SetUser("jane@example.com")
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "ping")))
	// The fields ping had are still there.
	if out["user"] != "jane@example.com" || out["response"] != "pong" || out["url"] != s.URL || out["latency"] == "" {
		t.Errorf("out = %v", out)
	}
	cmdTHas(t, cmdTOK(t, cmdTRun(t, s, "ping")).Stderr, "pong", "jane@example.com")
}

// frappe.ping needs no login, so a pong with rejected credentials must fail.
func TestPingFailsOnBadCredentials(t *testing.T) {
	s := frappetest.New(t)
	s.HandleMethod("frappe.auth.get_logged_user", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, &frappetest.Error{Status: 401, ExcType: "AuthenticationError", Message: "bad"}
	})
	if r := cmdTRun(t, s, "ping"); r.Code != exitAuth {
		t.Errorf("exit %d, want %d (stderr %q)", r.Code, exitAuth, r.Stderr)
	}
	s.HandleMethod("frappe.auth.get_logged_user", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return "Guest", nil
	})
	if r := cmdTRun(t, s, "ping"); r.Code != exitAuth || !strings.Contains(r.Err.Error(), "Guest") {
		t.Errorf("Guest: exit %d, %v", r.Code, r.Err)
	}
}

// ─── can ─────────────────────────────────────────────────────────────────────

func canTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	s.AddDocType("Customer")
	s.DocPerm("Customer", map[string]interface{}{"role": "Sales User", "permlevel": 0, "read": 1, "create": 1})
	s.SetUser("jane@example.com", "Sales User")
	return s
}

func TestCanAllowedDocument(t *testing.T) {
	s := canTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "can", "-d", "ToDo", "-n", "TD-1", "--perm", "write"))
	out := cmdTObj(t, r)
	if out["allowed"] != true || out["basis"] != "document" || out["perm"] != "write" || out["doctype"] != "ToDo" || out["name"] != "TD-1" {
		t.Errorf("out = %v", out)
	}
	if _, has := out["permissions"]; has {
		t.Error("permissions are listed only with --all")
	}
	cmdTHas(t, cmdTOK(t, cmdTRun(t, s, "can", "-d", "ToDo", "-n", "TD-1")).Stderr, "allowed", "read", "ToDo TD-1")
}

func TestCanDeniedExitsPermission(t *testing.T) {
	s := canTSite(t)
	s.Deny("ToDo", "write")
	r := cmdTRun(t, s, "--json", "can", "-d", "ToDo", "-n", "TD-1", "--perm", "write")
	if r.Code != exitPermission {
		t.Fatalf("exit %d, want %d", r.Code, exitPermission)
	}
	// The answer is still data on stdout.
	if out := cmdTObj(t, r); out["allowed"] != false {
		t.Errorf("out = %v", out)
	}
	if !strings.Contains(r.Stderr, `"code":"permission"`) || !strings.Contains(r.Stderr, "denied: write on ToDo TD-1") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	r = cmdTRun(t, s, "can", "-d", "ToDo", "-n", "TD-1", "--perm", "write")
	if r.Code != exitPermission || !strings.Contains(r.Stderr, "denied: write on ToDo TD-1") {
		t.Errorf("human: exit %d stderr %q", r.Code, r.Stderr)
	}
}

func TestCanAll(t *testing.T) {
	s := canTSite(t)
	s.Deny("ToDo", "delete")
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "can", "-d", "ToDo", "-n", "TD-1", "--all")))
	perms := out["permissions"].(map[string]interface{})
	if perms["read"] != float64(1) || perms["delete"] != float64(0) {
		t.Errorf("permissions = %v", perms)
	}
	cmdTOK(t, cmdTRun(t, s, "can", "-d", "ToDo", "-n", "TD-1", "--all")) // the table view
}

func TestCanDocTypeLevel(t *testing.T) {
	s := canTSite(t)
	out := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "can", "-d", "Customer", "--perm", "create")))
	if out["allowed"] != true || out["basis"] != "doctype" || out["name"] != nil {
		t.Errorf("out = %v", out)
	}
	r := cmdTRun(t, s, "--json", "can", "-d", "Customer", "--perm", "delete")
	if r.Code != exitPermission || cmdTObj(t, r)["allowed"] != false {
		t.Errorf("delete: exit %d, %s", r.Code, r.Stdout)
	}
	// Document-level calls were not made for a DocType-level question.
	if n := len(s.RequestsTo("GET", "/api/method/frappe.client.has_permission")); n != 0 {
		t.Errorf("has_permission called %d times", n)
	}
}

func TestCanErrors(t *testing.T) {
	s := canTSite(t)
	if r := cmdTRun(t, s, "can", "-d", "ToDo", "-n", "nope"); r.Code != exitNotFound {
		t.Errorf("a missing document exits %d, want %d (%v)", r.Code, exitNotFound, r.Err)
	}
	if r := cmdTRun(t, s, "can", "-d", "Nope", "-n", "x"); r.Code != exitPermission {
		t.Errorf("an unknown DocType is a 403 from the site: exit %d", r.Code)
	}
	if r := cmdTRun(t, s, "can", "-d", "ToDo", "--perm", "frobnicate"); r.Code != exitUsage || !strings.Contains(r.Err.Error(), "--perm") {
		t.Errorf("bad --perm: exit %d, %v", r.Code, r.Err)
	}
	if r := cmdTRun(t, s, "can", "-d", "ToDo", "--all"); r.Code != exitUsage {
		t.Errorf("--all without --name: exit %d", r.Code)
	}
	if r := cmdTRun(t, s, "can"); r.Code != exitUsage {
		t.Errorf("no --doctype: exit %d", r.Code)
	}
	// An upper-case permission is accepted.
	cmdTOK(t, cmdTRun(t, s, "can", "-d", "ToDo", "-n", "TD-1", "--perm", "WRITE"))
}

// ─── MCP ─────────────────────────────────────────────────────────────────────

func TestMCPWhoami(t *testing.T) {
	cacheTEnv(t)
	site := frappetest.New(t)
	cfg := &config.SiteConfig{Name: "test", URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret, MCP: &config.MCPPolicy{ReadOnly: true}}
	c, err := client.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("test", "0")
	mcpTRegister(s, c, cfg, nil)
	site.SetUser("jane@example.com", "Sales User")
	out := mcpTObj(t, mcpTOK(t, s, "whoami", nil))
	if out["user"] != "jane@example.com" || out["roles_source"] != "has_role" || out["server"].(map[string]interface{})["frappe"] != "16.36.1" {
		t.Errorf("out = %v", out)
	}
	n := versionCalls(site)
	mcpTOK(t, s, "whoami", nil)
	if versionCalls(site) != n {
		t.Error("the second call must use the cache")
	}
	mcpTOK(t, s, "whoami", map[string]interface{}{"refresh": true})
	if versionCalls(site) != n+1 {
		t.Error("refresh must read the versions again")
	}
}

// A policy that denies User blocks whoami before anything is sent to the site.
func TestMCPWhoamiDeniedByPolicy(t *testing.T) {
	cacheTEnv(t)
	site := frappetest.New(t)
	cfg := &config.SiteConfig{Name: "test", URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret,
		MCP: &config.MCPPolicy{DenyDoctypes: []string{"User"}}}
	c, err := client.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("test", "0")
	mcpTRegister(s, c, cfg, nil)
	n := len(site.Requests())
	mcpTErr(t, s, "whoami", nil, `DocType "User" is denied`)
	if got := len(site.Requests()) - n; got != 0 {
		t.Errorf("%d requests reached the site", got)
	}
	// check_permission on another DocType is not affected.
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
}

func TestMCPCheckPermission(t *testing.T) {
	s, site := newMCPFake(t, true)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	site.AddDocType("Customer")
	site.DocPerm("Customer", map[string]interface{}{"role": "Sales User", "permlevel": 0, "read": 1})
	site.SetUser("jane@example.com", "Sales User")

	out := mcpTObj(t, mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "perm_type": "write"}))
	if out["allowed"] != true || out["basis"] != "document" {
		t.Errorf("out = %v", out)
	}
	// A denial is a result, not an error.
	site.Deny("ToDo", "write")
	out = mcpTObj(t, mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "perm_type": "write", "all": true}))
	if out["allowed"] != false || out["permissions"].(map[string]interface{})["read"] != float64(1) {
		t.Errorf("out = %v", out)
	}
	out = mcpTObj(t, mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "Customer"}))
	if out["allowed"] != true || out["basis"] != "doctype" {
		t.Errorf("default perm_type is read: %v", out)
	}
	out = mcpTObj(t, mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "Customer", "perm_type": "create"}))
	if out["allowed"] != false {
		t.Errorf("create on Customer: %v", out)
	}

	mcpTErr(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "perm_type": "frobnicate"}, "perm_type")
	mcpTErr(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "all": true}, "needs name")
	mcpTErr(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "nope"}, "not found")
	mcpTErr(t, s, "check_permission", map[string]interface{}{}, "doctype")
}

// The policy sees the DocType and document a check_permission call names.
func TestMCPCheckPermissionScope(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Name = "check_permission"
	req.Params.Arguments = map[string]interface{}{"doctype": " ToDo ", "name": "TD-1"}
	sc, err := scopeOf(req)
	if err != nil || sc.Action != actRead || len(sc.Doctypes) != 1 || sc.Doctypes[0] != "ToDo" || len(sc.Names) != 1 || sc.Names[0] != "TD-1" || sc.Confirm {
		t.Errorf("scope = %+v, %v", sc, err)
	}
	req.Params.Arguments = map[string]interface{}{"doctype": "To\u200bDo"}
	if _, err := scopeOf(req); err == nil {
		t.Error("an invisible character in the DocType must be refused")
	}
	req.Params.Name = "whoami"
	req.Params.Arguments = map[string]interface{}{}
	if sc, err := scopeOf(req); err != nil || sc.Action != actRead || len(sc.Doctypes) != 1 || sc.Doctypes[0] != "User" {
		t.Errorf("whoami scope = %+v, %v (it reads User)", sc, err)
	}
}
