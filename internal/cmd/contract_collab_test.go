//go:build contract

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// contractTag is the fixture tag; teardownContractTag removes it.
const contractTag = "ffc-contract-tag"

// teardownContractCollab removes what the collaboration tests leave once
// the fixture documents and user are gone: the Tag record, and the
// notifications Frappe sent the fixture user (written by a background job,
// so possibly after the user was deleted). It is idempotent.
func teardownContractCollab(c *client.FrappeClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if rows, err := c.GetList(ctx, "Tag Link", client.ListOptions{Filters: `{"tag":"` + contractTag + `"}`, Limit: -1}); err == nil {
		for _, r := range rows {
			if name, ok := r["name"].(string); ok {
				_ = c.DeleteDoc(ctx, "Tag Link", name)
			}
		}
	}
	_ = c.DeleteDoc(ctx, "Tag", contractTag)
	if rows, err := c.GetList(ctx, "Notification Log", client.ListOptions{Filters: `[["for_user","like","` + contractUserLike + `"]]`, Limit: -1}); err == nil {
		for _, r := range rows {
			if name, ok := r["name"].(string); ok {
				_ = c.DeleteDoc(ctx, "Notification Log", name)
			}
		}
	}
}

// contractCollab pins what T2.5 relies on: the argument names and results
// of add_comment, assign_to.add/remove, add_tag/remove_tag and
// share.add/set_permission/get_users, Frappe's HTML sanitising of comments,
// the duplicate-assignment and missing-share no-ops, the read-only share an
// assignee without permission gets, and that the fake fails like the site.
func contractCollab(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	teardownContractCollab(c)
	t.Cleanup(func() {
		// After the fixture user is gone (cleanups run last-in first-out),
		// and after the notification jobs had a moment to run.
		time.Sleep(2 * time.Second)
		teardownContractCollab(c)
	})
	uc := contractNonAdmin(t, c, sc)
	name := createContractDoc(t, c, map[string]interface{}{"title": "collab"})

	// Comments: plain text is escaped by ffc, HTML is sanitised by Frappe.
	res, err := addComment(ctx, c, contractDT, name, "a < b\n<script>x</script>", false)
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if res["content"] != "a &lt; b<br>&lt;script&gt;x&lt;/script&gt;" {
		t.Errorf("plain comment stored as %q", res["content"])
	}
	res, err = addComment(ctx, c, contractDT, name, `<b>ok</b><script>alert(1)</script><img src=x onerror=alert(1)>`, true)
	if err != nil {
		t.Fatalf("html comment: %v", err)
	}
	if s, _ := res["content"].(string); !strings.Contains(s, "<b>ok</b>") || strings.Contains(s, "<script") || strings.Contains(s, "onerror") {
		t.Errorf("HTML comment not sanitised: %q", s)
	}
	if cm, err := c.GetDoc(ctx, "Comment", res["comment"].(string)); err != nil || cm["comment_type"] != "Comment" || cm["reference_name"] != name {
		t.Errorf("Comment = %v, %v", cm, err)
	}

	// Assignment: a ToDo for the user, who cannot read the document and so
	// gets it shared read-only. A second add is a no-op, not an error.
	opts := client.AssignOptions{Priority: "High", Date: "2030-01-01", Description: "ffc contract"}
	res, err = assignUsers(ctx, c, contractDT, name, []string{contractUser}, opts)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !contains(res["assignees"].([]string), contractUser, false) {
		t.Errorf("assign = %v", res)
	}
	if rows, err := c.Assign(ctx, contractDT, name, []string{contractUser}, opts); err != nil || len(rows) != 1 {
		t.Errorf("duplicate assignment = %v, %v; want the one open assignment and no error", rows, err)
	}
	todos, err := c.GetList(ctx, "ToDo", client.ListOptions{Fields: []string{"name", "priority", "date", "status"},
		Filters: `{"reference_type":"` + contractDT + `","reference_name":"` + name + `"}`, Limit: -1})
	if err != nil || len(todos) != 1 || todos[0]["priority"] != "High" || todos[0]["date"] != "2030-01-01" || todos[0]["status"] != "Open" {
		t.Errorf("ToDos = %v, %v", todos, err)
	}
	if rows, _ := c.Shares(ctx, contractDT, name); len(rows) != 1 || rows[0]["user"] != contractUser || !truthy(rows[0]["read"]) || truthy(rows[0]["write"]) {
		t.Errorf("assignment share = %v", rows)
	}
	res, err = unassignUsers(ctx, c, contractDT, name, []string{contractUser})
	if err != nil || len(res["assignees"].([]string)) != 0 {
		t.Errorf("unassign = %v, %v", res, err)
	}
	if err := c.Unassign(ctx, contractDT, name, contractUser); err != nil {
		t.Errorf("unassigning a user who is not assigned: %v", err)
	}

	// Tags: exact match on add, any case on remove.
	if res, err = tagDoc(ctx, c, contractDT, name, []string{contractTag}); err != nil || !contains(res["tags"].([]string), contractTag, false) {
		t.Fatalf("tag = %v, %v", res, err)
	}
	if err := c.AddTag(ctx, contractDT, name, contractTag); err != nil {
		t.Errorf("adding a tag twice: %v", err)
	}
	if tags, _ := c.Tags(ctx, contractDT, name); len(tags) != 1 {
		t.Errorf("tags after a second add = %v", tags)
	}
	if res, err = untagDoc(ctx, c, contractDT, name, []string{strings.ToUpper(contractTag)}); err != nil || len(res["tags"].([]string)) != 0 {
		t.Errorf("untag = %v, %v", res, err)
	}
	if err := c.RemoveTag(ctx, contractDT, name, contractTag); err != nil {
		t.Errorf("removing a missing tag: %v", err)
	}

	// Shares: a second add replaces the rights; set_permission read=0
	// removes the share; removing a missing share is a no-op.
	if res, err = shareDoc(ctx, c, contractDT, name, client.ShareOptions{User: contractUser, Write: true, Submit: true}); err != nil || res["write"] != true || res["submit"] != true {
		t.Fatalf("share = %v, %v", res, err)
	}
	if res, err = shareDoc(ctx, c, contractDT, name, client.ShareOptions{User: contractUser}); err != nil || res["write"] != false || res["read"] != true {
		t.Errorf("re-share = %v, %v", res, err)
	}
	if _, err := uc.GetDoc(ctx, contractDT, name); err != nil {
		t.Errorf("the user cannot read a shared document: %v", err)
	}
	if res, err = unshareDoc(ctx, c, contractDT, name, contractUser, false); err != nil || res["removed"] != true {
		t.Errorf("unshare = %v, %v", res, err)
	}
	if err := c.Unshare(ctx, contractDT, name, contractUser, false); err != nil {
		t.Errorf("removing a missing share: %v", err)
	}
	if res, err = shareDoc(ctx, c, contractDT, name, client.ShareOptions{Everyone: true}); err != nil || res["everyone"] != true {
		t.Errorf("share with everyone = %v, %v", res, err)
	}
	// An everyone-share stores no user. share.add without a user shares
	// with the session user, and removing that share leaves the
	// everyone-share alone: get_share_name with everyone=0 filters on the
	// user only, and DocShare.validate_user clears an everyone-share's user.
	self, err := c.LoggedUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	shareKeys := func() map[string]bool {
		rows, err := c.Shares(ctx, contractDT, name)
		if err != nil {
			t.Fatalf("shares: %v", err)
		}
		keys := map[string]bool{}
		for _, r := range rows {
			keys[fmt.Sprintf("%v/%v/w%v", r["user"], truthy(r["everyone"]), truthy(r["write"]))] = true
		}
		return keys
	}
	if _, err := c.CallMethod(ctx, "frappe.share.add", map[string]interface{}{"doctype": contractDT, "name": name}, false); err != nil {
		t.Errorf("share.add without a user: %v", err)
	}
	if keys := shareKeys(); len(keys) != 2 || !keys["<nil>/true/wfalse"] || !keys[self+"/false/wfalse"] {
		t.Errorf("shares = %v; want an everyone-share without a user and one for %s", keys, self)
	}
	if res, err = unshareDoc(ctx, c, contractDT, name, self, false); err != nil || res["removed"] != true {
		t.Errorf("unshare self = %v, %v", res, err)
	}
	if keys := shareKeys(); len(keys) != 1 || !keys["<nil>/true/wfalse"] {
		t.Errorf("shares after unsharing self = %v; want the everyone-share", keys)
	}
	// set_permission with a true value and no share creates one with read.
	if _, err := c.CallMethod(ctx, "frappe.share.set_permission", map[string]interface{}{
		"doctype": contractDT, "name": name, "user": contractUser, "permission_to": "write", "value": 1}, false); err != nil {
		t.Errorf("set_permission write=1: %v", err)
	}
	if keys := shareKeys(); len(keys) != 2 || !keys[contractUser+"/false/wtrue"] {
		t.Errorf("shares after set_permission = %v", keys)
	}
	if res, err = unshareDoc(ctx, c, contractDT, name, contractUser, false); err != nil || res["removed"] != true {
		t.Errorf("unshare = %v, %v", res, err)
	}
	if res, err = unshareDoc(ctx, c, contractDT, name, "", true); err != nil || res["removed"] != true {
		t.Errorf("unshare everyone = %v, %v", res, err)
	}
	if rows, _ := c.Shares(ctx, contractDT, name); len(rows) != 0 {
		t.Errorf("shares left: %v", rows)
	}

	t.Run("errors match the fake", func(t *testing.T) { contractCollabErrors(t, c, uc) })
}

// contractCollabErrors compares the site's errors with the fake's. uc is the
// fixture user, who has no role on the fixture DocType.
func contractCollabErrors(t *testing.T, c, uc *client.FrappeClient) {
	ctx := contractCtx(t)
	priv := createContractDoc(t, c, map[string]interface{}{"title": "private"})
	note, err := c.CreateDoc(ctx, "Note", map[string]interface{}{"title": "ffc contract collab"})
	if err != nil {
		t.Fatal(err)
	}
	noteName := note["name"].(string)
	t.Cleanup(func() { _ = c.DeleteDoc(context.Background(), "Note", noteName) })

	fake := frappetest.New(t)
	fake.Add(contractDT, map[string]interface{}{"name": priv})
	fake.Add("Note", map[string]interface{}{"name": noteName})
	fc, err := client.New(ctx, &config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	denied := frappetest.New(t)
	denied.Add(contractDT, map[string]interface{}{"name": priv})
	denied.Deny(contractDT, "read", "write", "share")
	dc, err := client.New(ctx, &config.SiteConfig{URL: denied.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	const missing = "ffc-contract-missing"
	type call func(c *client.FrappeClient) error
	cases := []struct {
		name       string
		real, fake *client.FrappeClient
		call       call
	}{
		{"comment on a missing document", c, fc, func(c *client.FrappeClient) error {
			_, err := c.AddComment(ctx, contractDT, missing, "x", "Administrator", "Administrator")
			return err
		}},
		{"assign a missing document", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Assign(ctx, contractDT, missing, []string{"Administrator"}, client.AssignOptions{})
			return err
		}},
		{"unassign a missing document", c, fc, func(c *client.FrappeClient) error { return c.Unassign(ctx, contractDT, missing, "Administrator") }},
		{"tag a missing document", c, fc, func(c *client.FrappeClient) error { return c.AddTag(ctx, contractDT, missing, contractTag) }},
		{"untag a missing document", c, fc, func(c *client.FrappeClient) error { return c.RemoveTag(ctx, contractDT, missing, contractTag) }},
		{"share a missing document", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Share(ctx, contractDT, missing, client.ShareOptions{User: "Administrator"})
			return err
		}},
		{"shares of a missing document", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Shares(ctx, contractDT, missing)
			return err
		}},
		{"read the assignees of a missing document", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Assignees(ctx, contractDT, missing)
			return err
		}},
		{"assign an unknown user", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Assign(ctx, contractDT, priv, []string{"ffc-contract-nobody@example.com"}, client.AssignOptions{})
			return err
		}},
		{"share with an unknown user", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Share(ctx, contractDT, priv, client.ShareOptions{User: "ffc-contract-nobody@example.com"})
			return err
		}},
		{"share submit on a DocType that is not submittable", c, fc, func(c *client.FrappeClient) error {
			_, err := c.Share(ctx, "Note", noteName, client.ShareOptions{User: "Administrator", Submit: true})
			return err
		}},
		{"comment without read permission", uc, dc, func(c *client.FrappeClient) error {
			_, err := c.AddComment(ctx, contractDT, priv, "x", contractUser, contractUser)
			return err
		}},
		{"assign without read permission", uc, dc, func(c *client.FrappeClient) error {
			_, err := c.Assign(ctx, contractDT, priv, []string{contractUser}, client.AssignOptions{})
			return err
		}},
		{"tag without write permission", uc, dc, func(c *client.FrappeClient) error { return c.AddTag(ctx, contractDT, priv, contractTag) }},
		{"share without share permission", uc, dc, func(c *client.FrappeClient) error {
			_, err := c.Share(ctx, contractDT, priv, client.ShareOptions{User: contractUser})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			realErr, fakeErr := tc.call(tc.real), tc.call(tc.fake)
			if realErr == nil || fakeErr == nil {
				t.Fatalf("real = %v, fake = %v; want both to fail", realErr, fakeErr)
			}
			var ra, fa *client.APIError
			if !errors.As(realErr, &ra) || !errors.As(fakeErr, &fa) {
				t.Fatalf("real = %v, fake = %v", realErr, fakeErr)
			}
			if ra.Status != fa.Status || ra.ExcType != fa.ExcType {
				t.Errorf("real %d %s (%v), fake %d %s (%v)", ra.Status, ra.ExcType, realErr, fa.Status, fa.ExcType, fakeErr)
			}
			if rc, _ := classify(realErr); rc != func() int { c, _ := classify(fakeErr); return c }() {
				t.Errorf("exit code differs: real %v, fake %v", realErr, fakeErr)
			}
		})
	}
	// Nothing of the denied calls stuck.
	if tags, err := c.Tags(ctx, contractDT, priv); err != nil || len(tags) != 0 {
		t.Errorf("tags on the private document = %v, %v", tags, err)
	}
}
