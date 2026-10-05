//go:build contract

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractDocInfo pins T2.1: get_docinfo answers in "docinfo" (not
// "message") with the versions of a DocType that tracks changes (a Version's
// data is {"changed": [[field, old, new]], "row_changed": [[table, index,
// row, [[field, old, new]]]]}), HTML comments, tags as a comma string;
// get_open_count answers {count: {external_links_found, internal_links_found}};
// getdoc returns the document with __onload and the docinfo; the activity
// timeline either exists or is missing with Frappe's "Failed to get method"
// (417). (Deleting a document removes its versions and comments in a
// background job, so teardown leaves them to Frappe.)
func contractDocInfo(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	name := createContractDoc(t, c, map[string]interface{}{
		"title": "docinfo", "n_int": 1,
		"items": []interface{}{map[string]interface{}{"item": "a", "qty": 1}},
	})
	doc, err := c.GetDoc(ctx, contractDT, name)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := doc["items"].([]interface{})
	items[0].(map[string]interface{})["qty"] = 5
	if _, err := c.UpdateDoc(ctx, contractDT, name, map[string]interface{}{"n_int": 2, "items": items}); err != nil {
		t.Fatal(err)
	}
	user, err := c.LoggedUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallMethod(ctx, "frappe.desk.form.utils.add_comment", map[string]interface{}{
		"reference_doctype": contractDT, "reference_name": name, "content": "<p>hello <b>contract</b></p>",
		"comment_email": user, "comment_by": user,
	}, false); err != nil {
		t.Fatalf("add_comment: %v", err)
	}
	// add_tag creates the Tag master, which outlives the document: untag,
	// then delete it.
	t.Cleanup(func() {
		ctx := contractCtx(t)
		_, _ = c.CallMethod(ctx, "frappe.desk.doctype.tag.tag.remove_tag", map[string]interface{}{"tag": "ffc-contract-tag", "dt": contractDT, "dn": name}, false)
		if err := c.DeleteDoc(ctx, "Tag", "ffc-contract-tag"); err != nil {
			t.Logf("cleanup: delete Tag: %v", err)
		}
	})
	if _, err := c.CallMethod(ctx, "frappe.desk.doctype.tag.tag.add_tag", map[string]interface{}{"tag": "ffc-contract-tag", "dt": contractDT, "dn": name}, false); err != nil {
		t.Fatalf("add_tag: %v", err)
	}

	// The raw docinfo shape the compact view reads.
	info, err := c.DocInfo(ctx, contractDT, name)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"versions", "comments", "attachments", "assignments", "shared", "tags", "workflow_logs", "communications", "permissions"} {
		if _, ok := info[k]; !ok {
			t.Errorf("docinfo lacks %q: %v", k, info)
		}
	}

	cfg := contractConfig(t, sc)
	r := runFFC(t, cfg, "", "--json", "doc-info", "-d", contractDT, "-n", name, "--links", "--timeline")
	if r.Err != nil {
		t.Fatalf("doc-info: %v\n%s", r.Err, r.Stderr)
	}
	var d docContext
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		t.Fatalf("doc-info output: %v\n%s", err, r.Stdout)
	}
	if by, ch, ok := changeBy(d, "n_int"); !ok || by != user || fmt.Sprint(ch.From) != "1" || fmt.Sprint(ch.To) != "2" {
		t.Errorf("n_int change = %q %+v %v; versions %+v", by, ch, ok, d.Versions)
	}
	if _, ch, ok := changeBy(d, "items[1].qty"); !ok || fmt.Sprint(ch.To) != "5" {
		t.Errorf("row change = %+v %v; versions %+v", ch, ok, d.Versions)
	}
	if len(d.Comments) != 1 || d.Comments[0].Text != "hello contract" || d.Comments[0].By != user {
		t.Errorf("comments = %+v", d.Comments)
	}
	// Frappe caches per DocType whether any document has tags
	// (doctype_has_tags in tag_link.py) and does not always invalidate a
	// cached "no": on a fresh DocType the tag may then be missing from
	// docinfo although its Tag Link exists.
	if strings.Join(d.Tags, ",") != "ffc-contract-tag" {
		if n, err := c.GetCount(ctx, "Tag Link", `{"document_type":"`+contractDT+`","document_name":"`+name+`"}`); err != nil || n != 1 || len(d.Tags) != 0 {
			t.Errorf("tags = %v; Tag Link rows %d, %v", d.Tags, n, err)
		}
		t.Log("docinfo tags empty although the Tag Link exists: Frappe's stale has_tags cache")
	}
	if !strings.Contains(strings.Join(d.Permissions, ","), "read") {
		t.Errorf("permissions = %v", d.Permissions)
	}
	// A custom DocType has no dashboard: no linked DocTypes, but a list.
	if d.Links == nil {
		t.Errorf("links missing: %s", r.Stdout)
	}
	switch {
	case len(d.Notes) > 0:
		// A release without the timeline: the method is reported missing.
		if !strings.Contains(d.Notes[0], "no activity timeline") {
			t.Errorf("notes = %v", d.Notes)
		}
		_, err := c.ActivityTimeline(ctx, contractDT, name)
		var api *client.APIError
		if !errors.Is(err, client.ErrNoTimeline) || !errors.As(err, &api) || api.Status != 417 {
			t.Errorf("timeline error = %v, want ErrNoTimeline over a 417", err)
		}
		t.Log("this site has no activity timeline")
	default:
		found := false
		for _, a := range d.Timeline {
			found = found || a.Type == "version" && a.Field == "n_int"
		}
		if !found {
			t.Errorf("timeline has no n_int change: %+v", d.Timeline)
		}
	}

	// getdoc gives the document's __onload (empty for a custom DocType).
	r = runFFC(t, cfg, "", "--json", "doc-info", "-d", contractDT, "-n", name, "--onload")
	d = docContext{}
	if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &d) != nil || d.Onload == nil || len(d.Versions) == 0 {
		t.Errorf("doc-info --onload: %v\n%s", r.Err, r.Stdout)
	}
	if r = runFFC(t, cfg, "", "doc-info", "-d", contractDT, "-n", "ffc-contract-missing"); r.Code != 4 {
		t.Errorf("missing document exit = %d, want 4 (%v)", r.Code, r.Err)
	}
}
