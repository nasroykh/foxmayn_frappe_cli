package cmd

import (
	"context"
	"fmt"
	"html"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// Collaboration writes (T2.5): comments, assignments, tags and shares. The
// logic here is shared by the CLI commands (collab_cmds.go) and the MCP
// tools (mcp_collab_tools.go). Each change reads the current state first,
// so the result says what changed and what already was, and a dry run
// still proves the document exists.

// commentHTML turns plain text into the HTML a comment holds: escaped, so
// "<b>" shows as typed, with line breaks kept. With asHTML the text is sent
// as is; Frappe sanitises it either way.
func commentHTML(text string, asHTML bool) string {
	if asHTML {
		return text
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
}

// readCommentText is the comment of `ffc comment`: the arguments joined by
// spaces, or stdin for a single "-".
func readCommentText(args []string) (string, error) {
	text := strings.Join(args, " ")
	if len(args) == 1 && args[0] == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		text = strings.TrimRight(string(b), "\r\n")
	}
	if strings.TrimSpace(text) == "" {
		return "", usageErrorf("the comment is empty")
	}
	return text, nil
}

// addComment comments on a document as the signed-in user: Frappe records
// whatever author it is sent, so ffc sends the user it signs in as, with
// the user's full name when it may read it.
func addComment(ctx context.Context, c *client.FrappeClient, doctype, name, text string, asHTML bool) (map[string]interface{}, error) {
	user, err := c.LoggedUser(ctx)
	if err != nil {
		return nil, err
	}
	by := user
	// Over MCP the name is read only when the policy lets the call read User.
	if p, ok := policyFrom(ctx); !ok || p.doctypeAllowed("User", false) == nil {
		if doc, err := c.GetDoc(ctx, "User", user); err == nil {
			if full, _ := doc["full_name"].(string); strings.TrimSpace(full) != "" {
				by = full
			}
		}
	}
	doc, err := c.AddComment(ctx, doctype, name, commentHTML(text, asHTML), user, by)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"doctype": doctype, "name": name, "comment": doc["name"],
		"comment_by": user, "content": doc["content"],
	}, nil
}

// assignOptions checks the optional ToDo fields and normalises the priority.
func assignOptions(description, date, priority string) (client.AssignOptions, error) {
	o := client.AssignOptions{Description: description, Date: strings.TrimSpace(date)}
	if o.Date != "" {
		if _, err := time.Parse(time.DateOnly, o.Date); err != nil {
			return o, usageErrorf("date must be YYYY-MM-DD, not %q", date)
		}
	}
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "":
	case "low":
		o.Priority = "Low"
	case "medium":
		o.Priority = "Medium"
	case "high":
		o.Priority = "High"
	default:
		return o, usageErrorf("priority must be Low, Medium or High, not %q", priority)
	}
	return o, nil
}

// cleanList trims the values, drops blanks and duplicates (any case when
// fold), and refuses an empty result.
func cleanList(what string, values []string, fold bool) ([]string, error) {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" && !contains(out, v, fold) {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, usageErrorf("no %s given", what)
	}
	return out, nil
}

// cleanTags checks tag names: a document keeps its tags as one
// comma-separated text, so a tag cannot contain a comma.
func cleanTags(tags []string) ([]string, error) {
	out, err := cleanList("tag", tags, false)
	if err != nil {
		return nil, err
	}
	for _, t := range out {
		if strings.Contains(t, ",") {
			return nil, usageErrorf("tag %q contains a comma: Frappe stores a document's tags comma-separated", t)
		}
	}
	return out, nil
}

// assignUsers assigns a document to users. Users already assigned (an open
// ToDo) are reported, not assigned twice; Frappe would skip them too.
func assignUsers(ctx context.Context, c *client.FrappeClient, doctype, name string, users []string, o client.AssignOptions) (map[string]interface{}, error) {
	before, err := c.Assignees(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	added, already := []string{}, []string{}
	for _, u := range users {
		if contains(before, u, true) {
			already = append(already, u)
		} else {
			added = append(added, u)
		}
	}
	after := before
	if len(added) > 0 {
		if _, err := c.Assign(ctx, doctype, name, added, o); err != nil {
			return nil, err
		}
		if after, err = c.Assignees(ctx, doctype, name); err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{"doctype": doctype, "name": name, "assigned": stored(after, added), "already_assigned": stored(before, already), "assignees": after}, nil
}

// unassignUsers cancels the assignments of users; users who are not
// assigned are reported, not sent.
func unassignUsers(ctx context.Context, c *client.FrappeClient, doctype, name string, users []string) (map[string]interface{}, error) {
	before, err := c.Assignees(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	removed, missing := []string{}, []string{}
	for _, u := range users {
		if !contains(before, u, true) {
			missing = append(missing, u)
			continue
		}
		if err := c.Unassign(ctx, doctype, name, u); err != nil {
			return nil, err
		}
		removed = append(removed, u)
	}
	after := before
	if len(removed) > 0 {
		if after, err = c.Assignees(ctx, doctype, name); err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{"doctype": doctype, "name": name, "unassigned": stored(before, removed), "not_assigned": missing, "assignees": after}, nil
}

// stored replaces each user ID with the spelling the site stores in ids
// (User IDs compare case-insensitively), so results name the same users
// as assignees.
func stored(ids, users []string) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u
		for _, id := range ids {
			if strings.EqualFold(id, u) {
				out[i] = id
				break
			}
		}
	}
	return out
}

// tagDoc adds tags; one the document has (same case, as Frappe compares)
// is reported, not sent.
func tagDoc(ctx context.Context, c *client.FrappeClient, doctype, name string, tags []string) (map[string]interface{}, error) {
	before, err := c.Tags(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	added, already := []string{}, []string{}
	for _, t := range tags {
		if contains(before, t, false) {
			already = append(already, t)
			continue
		}
		if err := c.AddTag(ctx, doctype, name, t); err != nil {
			return nil, err
		}
		added = append(added, t)
	}
	after := before
	if len(added) > 0 {
		if after, err = c.Tags(ctx, doctype, name); err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{"doctype": doctype, "name": name, "added": added, "already_tagged": already, "tags": after}, nil
}

// untagDoc removes tags (any case, as Frappe does); one the document does
// not have is reported, not sent.
func untagDoc(ctx context.Context, c *client.FrappeClient, doctype, name string, tags []string) (map[string]interface{}, error) {
	before, err := c.Tags(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	removed, missing := []string{}, []string{}
	for _, t := range tags {
		if !contains(before, t, true) {
			missing = append(missing, t)
			continue
		}
		if err := c.RemoveTag(ctx, doctype, name, t); err != nil {
			return nil, err
		}
		removed = append(removed, t)
	}
	after := before
	if len(removed) > 0 {
		if after, err = c.Tags(ctx, doctype, name); err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{"doctype": doctype, "name": name, "removed": removed, "not_tagged": missing, "tags": after}, nil
}

// checkShareTarget requires exactly one of a user and everyone.
func checkShareTarget(user string, everyone bool) error {
	switch {
	case everyone && user != "":
		return usageErrorf("share with a user or with everyone, not both")
	case !everyone && user == "":
		return usageErrorf("name a user, or share with everyone")
	}
	return nil
}

// shareTarget names whom a share is for, for messages.
func shareTarget(user string, everyone bool) string {
	if everyone {
		return "everyone"
	}
	return user
}

// shareRights lists the rights a share grants, read first.
func shareRights(o client.ShareOptions) string {
	r := []string{"read"}
	for _, x := range []struct {
		on   bool
		name string
	}{{o.Write, "write"}, {o.Submit, "submit"}, {o.Share, "share"}} {
		if x.on {
			r = append(r, x.name)
		}
	}
	return strings.Join(r, ", ")
}

// shareDoc shares a document and returns the share's rights.
func shareDoc(ctx context.Context, c *client.FrappeClient, doctype, name string, o client.ShareOptions) (map[string]interface{}, error) {
	doc, err := c.Share(ctx, doctype, name, o)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{"doctype": doctype, "name": name, "docshare": doc["name"], "everyone": o.Everyone}
	if !o.Everyone {
		out["user"] = doc["user"]
	}
	for _, k := range []string{"read", "write", "submit", "share"} {
		out[k] = truthy(doc[k])
	}
	return out, nil
}

// unshareDoc removes a user's (or everyone's) share of a document, if there
// is one.
func unshareDoc(ctx context.Context, c *client.FrappeClient, doctype, name, user string, everyone bool) (map[string]interface{}, error) {
	shares, err := c.Shares(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	found := false
	for _, s := range shares {
		u, _ := s["user"].(string)
		if everyone && truthy(s["everyone"]) || !everyone && !truthy(s["everyone"]) && strings.EqualFold(u, user) {
			found = true
			break
		}
	}
	if found {
		if err := c.Unshare(ctx, doctype, name, user, everyone); err != nil {
			return nil, err
		}
	}
	out := map[string]interface{}{"doctype": doctype, "name": name, "everyone": everyone, "removed": found}
	if !everyone {
		out["user"] = user
	}
	return out, nil
}
