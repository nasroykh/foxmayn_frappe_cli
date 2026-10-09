package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const restoreMethod = "frappe.core.doctype.deleted_document.deleted_document.restore"

// mcpTRestoreSite seeds deletions of a ToDo, a Server Script, a User and a
// Webhook and records the calls that reach the restore method.
func mcpTRestoreSite(t *testing.T, site *frappetest.Site) *[]string {
	t.Helper()
	del := func(id, dt, name string) map[string]interface{} {
		data, _ := json.Marshal(map[string]interface{}{"doctype": dt, "name": name})
		return map[string]interface{}{"name": id, "deleted_doctype": dt, "deleted_name": name, "restored": json.Number("0"), "data": string(data)}
	}
	// Records whose data does not match what they claim.
	forged := del("del-forged", "ToDo", "TD-F")
	forged["data"] = `{"doctype": "Server Script", "name": "TD-F"}`
	lower := del("del-lower", "ToDo", "TD-L")
	lower["data"] = `{"doctype": "todo", "name": "TD-L"}`
	notJSON := del("del-notjson", "ToDo", "TD-N")
	notJSON["data"] = "not json"
	noDoctype := del("del-nodoctype", "ToDo", "TD-D")
	noDoctype["data"] = `{"name": "TD-D"}`
	site.Add("Deleted Document", forged, lower, notJSON, noDoctype)
	site.Add("Deleted Document", del("del-todo", "ToDo", "TD-9"), del("del-ss", "Server Script", "SS-1"),
		del("del-user", "User", "x@example.com"), del("del-hook", "Webhook", "WH-1"), del("del-blank", "", "?"))
	var restored []string
	site.HandleMethod(restoreMethod, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		restored = append(restored, fmt.Sprint(args["name"]))
		return nil, nil
	})
	return &restored
}

func TestMCPRestoreDoc(t *testing.T) {
	s, site, _, path := mcpTPolicy(t, nil, config.MCPPolicy{})
	restored := mcpTRestoreSite(t, site)

	got := mcpTObj(t, mcpTOK(t, s, "restore_doc", map[string]interface{}{"deleted_document": "del-todo"}))
	if got["restored"] != true || got["deleted_document"] != "del-todo" || got["name"] == nil {
		t.Errorf("result = %v", got)
	}
	got = mcpTObj(t, mcpTOK(t, s, "restore_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-9"}))
	if got["restored"] != true || got["deleted_document"] != "del-todo" {
		t.Errorf("result = %v", got)
	}
	if strings.Join(*restored, ",") != "del-todo,del-todo" {
		t.Errorf("restored = %v", *restored)
	}
	recs := mcpTAudit(t, path)
	if len(recs) != 2 || recs[0].Status != auditOK || recs[0].Tool != "restore_doc" ||
		strings.Join(recs[0].Doctypes, ",") != "Deleted Document,ToDo" || strings.Join(recs[0].Names, ",") != "del-todo,TD-9" ||
		strings.Join(recs[1].Doctypes, ",") != "ToDo,Deleted Document" || strings.Join(recs[1].Names, ",") != "TD-9,del-todo" {
		t.Errorf("audit = %+v", recs)
	}
}

// TestMCPRestoreDocRefusals: no refusal sends anything to the restore method.
func TestMCPRestoreDocRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   *config.MCPPolicy
		flags config.MCPPolicy
		args  map[string]interface{}
		want  string
	}{
		{"server script", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-ss"}, `DocType "Server Script" is sensitive`},
		{"user by name", nil, config.MCPPolicy{}, map[string]interface{}{"doctype": "User", "name": "x@example.com"}, `DocType "User" is sensitive`},
		{"webhook", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-hook"}, `DocType "Webhook" is sensitive`},
		{"allow list misses it", &config.MCPPolicy{AllowDoctypes: []string{"Deleted Document", "ToDo"}}, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-ss"}, "allow_doctypes"},
		{"allow flag misses it", nil, config.MCPPolicy{AllowDoctypes: []string{"Deleted Document", "ToDo"}}, map[string]interface{}{"deleted_document": "del-ss"}, "is not in --allow-doctypes"},
		{"deny on the original", &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-todo"}, `DocType "ToDo" is denied`},
		{"deny flag on the original", nil, config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, map[string]interface{}{"doctype": "ToDo", "name": "TD-9"}, `DocType "ToDo" is denied`},
		{"deny Deleted Document", &config.MCPPolicy{DenyDoctypes: []string{"Deleted Document"}}, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-todo"}, `DocType "Deleted Document" is denied`},
		{"data doctype differs", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-forged"}, `its data restores a Server Script`},
		{"data doctype differs by case", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-lower"}, `its data restores a todo`},
		{"data not JSON", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-notjson"}, "no readable data"},
		{"data without doctype", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-nodoctype"}, "its data restores a <nil>"},
		{"call_method restore", nil, config.MCPPolicy{}, map[string]interface{}{"method": restoreMethod, "args": map[string]interface{}{"name": "del-todo"}}, "runs code, changes apps"},
		{"call_method bulk_restore", nil, config.MCPPolicy{}, map[string]interface{}{"method": "frappe.core.doctype.deleted_document.deleted_document.bulk_restore", "args": map[string]interface{}{"deleted_documents": `["del-todo"]`}}, "runs code, changes apps"},
		{"no deleted_doctype", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-blank"}, "has no deleted_doctype"},
		{"both forms", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-todo", "doctype": "ToDo", "name": "TD-9"}, "not both"},
		{"deleted_document and doctype", nil, config.MCPPolicy{}, map[string]interface{}{"deleted_document": "del-todo", "doctype": "ToDo"}, "not both"},
		{"neither", nil, config.MCPPolicy{}, map[string]interface{}{}, "not both"},
		{"doctype alone", nil, config.MCPPolicy{}, map[string]interface{}{"doctype": "ToDo"}, "not both"},
		{"nothing deleted", nil, config.MCPPolicy{}, map[string]interface{}{"doctype": "ToDo", "name": "TD-0"}, "no unrestored deletion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, site, _, _ := mcpTPolicy(t, tc.cfg, tc.flags)
			restored := mcpTRestoreSite(t, site)
			tool := "restore_doc"
			if _, ok := tc.args["method"]; ok {
				tool = "call_method"
			}
			mcpTErr(t, s, tool, tc.args, tc.want)
			if len(*restored) != 0 || len(site.RequestsTo("POST", "/api/method/"+restoreMethod)) != 0 {
				t.Errorf("the restore method was called: %v", *restored)
			}
		})
	}
}

// The audit line of a refusal shows both DocTypes and both names.
func TestMCPRestoreDocRefusedAudit(t *testing.T) {
	s, site, _, path := mcpTPolicy(t, nil, config.MCPPolicy{})
	restored := mcpTRestoreSite(t, site)
	mcpTErr(t, s, "restore_doc", map[string]interface{}{"deleted_document": "del-ss"}, `DocType "Server Script" is sensitive`)
	recs := mcpTAudit(t, path)
	if len(recs) != 1 || recs[0].Status != auditDenied || strings.Join(recs[0].Doctypes, ",") != "Deleted Document,Server Script" ||
		strings.Join(recs[0].Names, ",") != "del-ss,SS-1" {
		t.Errorf("audit = %+v", recs)
	}
	if len(*restored) != 0 {
		t.Errorf("restored = %v", *restored)
	}
}

func TestMCPRestoreDocAllowed(t *testing.T) {
	cfg := &config.MCPPolicy{AllowDoctypes: []string{"Deleted Document", "Server Script"}}
	s, site, _, path := mcpTPolicy(t, cfg, config.MCPPolicy{})
	restored := mcpTRestoreSite(t, site)
	mcpTOK(t, s, "restore_doc", map[string]interface{}{"deleted_document": "del-ss"})
	if strings.Join(*restored, ",") != "del-ss" {
		t.Errorf("restored = %v", *restored)
	}
	// The allow list still binds the other DocTypes.
	mcpTErr(t, s, "restore_doc", map[string]interface{}{"deleted_document": "del-todo"}, "allow_doctypes")
	recs := mcpTAudit(t, path)
	if len(recs) != 2 || recs[0].Status != auditOK || strings.Join(recs[0].Doctypes, ",") != "Deleted Document,Server Script" || recs[1].Status != auditDenied {
		t.Errorf("audit = %+v", recs)
	}
}

func TestMCPRestoreDocReadOnly(t *testing.T) {
	s, _, _, _ := mcpTPolicy(t, &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{})
	if _, ok := s.ListTools()["restore_doc"]; ok {
		t.Error("restore_doc is exposed to a read-only site")
	}
	p := newMCPPolicy(&config.SiteConfig{Name: "prod", MCP: &config.MCPPolicy{ReadOnly: true}}, config.MCPPolicy{})
	if err := p.toolAllowed("restore_doc"); err == nil {
		t.Error("toolAllowed accepts restore_doc on a read-only site")
	}
}

// checkRestore refuses a Deleted Document of another DocType than the one
// named, as the record's own deleted_doctype is the truth.
func TestMCPCheckRestoreMismatch(t *testing.T) {
	site := frappetest.New(t)
	mcpTRestoreSite(t, site)
	c, err := client.New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	p := newMCPPolicy(&config.SiteConfig{Name: "prod"}, config.MCPPolicy{})
	sc := toolScope{Restore: &restoreScope{Deleted: "del-todo", Doctype: "Note"}}
	id, dt, _, err := p.checkRestore(context.Background(), c, sc)
	if err == nil || !strings.Contains(err.Error(), "is a deleted ToDo, not a Note") || dt != "ToDo" {
		t.Errorf("id=%q dt=%q err=%v", id, dt, err)
	}
	sc.Restore.Doctype = "todo"
	if id, _, _, err = p.checkRestore(context.Background(), c, sc); err != nil || id != "del-todo" {
		t.Errorf("id=%q err=%v", id, err)
	}
}

// Lookup failures are audited as errors, only policy refusals as denied.
func TestMCPRestoreDocLookupIsError(t *testing.T) {
	s, site, _, path := mcpTPolicy(t, nil, config.MCPPolicy{})
	mcpTRestoreSite(t, site)
	mcpTErr(t, s, "restore_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-0"}, "no unrestored deletion")
	mcpTErr(t, s, "restore_doc", map[string]interface{}{"deleted_document": "nope"}, "nope")
	mcpTErr(t, s, "restore_doc", map[string]interface{}{"deleted_document": "del-forged"}, "policy:")
	recs := mcpTAudit(t, path)
	if len(recs) != 3 || recs[0].Status != auditError || recs[1].Status != auditError || recs[2].Status != auditDenied ||
		strings.HasPrefix(recs[0].Error, "policy:") || strings.HasPrefix(recs[1].Error, "policy:") {
		t.Fatalf("audit = %+v", recs)
	}
	if strings.Join(recs[2].Names, ",") != "del-forged,TD-F" || strings.Join(recs[2].Doctypes, ",") != "Deleted Document,ToDo" {
		t.Errorf("denied audit = %+v", recs[2])
	}
}

// Deleted Document is sensitive: Frappe re-creates what its data names, so
// writing one is writing any DocType. Reads and restore_doc itself stay.
func TestMCPDeletedDocumentIsSensitive(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	mcpTRestoreSite(t, site)
	const want = `DocType "Deleted Document" is sensitive`
	row := map[string]interface{}{"deleted_doctype": "ToDo", "deleted_name": "x", "data": "{}"}
	mcpTErr(t, s, "create_doc", map[string]interface{}{"doctype": "Deleted Document", "data": row}, want)
	mcpTErr(t, s, "update_doc", map[string]interface{}{"doctype": "Deleted Document", "name": "del-todo", "data": map[string]interface{}{"data": "{}"}}, want)
	mcpTErr(t, s, "delete_doc", map[string]interface{}{"doctype": "Deleted Document", "name": "del-todo"}, want)
	mcpTErr(t, s, "bulk_create", map[string]interface{}{"doctype": "Deleted Document", "data": []interface{}{row}}, want)
	mcpTErr(t, s, "bulk_update", map[string]interface{}{"doctype": "Deleted Document", "data": []interface{}{map[string]interface{}{"name": "del-todo", "data": "{}"}}}, want)
	for _, r := range site.Requests() {
		if r.Method != "GET" {
			t.Errorf("a write reached the site: %s %s", r.Method, r.Path)
		}
	}
	mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "Deleted Document", "name": "del-todo"})
	mcpTOK(t, s, "restore_doc", map[string]interface{}{"deleted_document": "del-todo"})

	cfg := &config.MCPPolicy{AllowDoctypes: []string{"Deleted Document"}}
	s, site, _, _ = mcpTPolicy(t, cfg, config.MCPPolicy{})
	mcpTRestoreSite(t, site)
	mcpTOK(t, s, "create_doc", map[string]interface{}{"doctype": "Deleted Document", "data": row})
}
