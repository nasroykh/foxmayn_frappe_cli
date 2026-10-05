package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// The MCP view keeps at most this many comments (newest) and timeline
// entries (newest); the rest are counted in omitted.
const (
	mcpContextComments = 50
	mcpContextTimeline = 100
)

func registerGetDocContext(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("get_doc_context",
		mcp.WithDescription("Everything around one document in one call, as the desk's form sidebar shows it: who changed which fields (the last 10 versions, newest first; each version lists changed fields with from/to, child-row changes as \"items[2].qty\"), comments, emails, attachments (names and URLs, not contents), assignments, shares, tags, the workflow log and the user's permissions on it. With links (default true), the number of linked documents per DocType as the Connections panel counts them (DocTypes from the DocType's dashboard, each count stops at 100: capped means at least; internal entries list the documents this one points to). With timeline, the activity timeline line by line with field labels (Frappe releases from 2026 on; notes says when the site lacks it). Versions exist only when the DocType tracks changes. Nothing is written. Parts read from a DocType this server may not read are left out and named in hidden_by_policy."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype", mcp.Required(), mcp.Description("The Frappe DocType, e.g. 'Sales Invoice'")),
		mcp.WithString("name", mcp.Description("The document name, e.g. 'ACC-SINV-2026-00001'. Omit for Single DocTypes.")),
		mcp.WithBoolean("links", mcp.Description("Count linked documents per DocType. Default true.")),
		mcp.WithBoolean("timeline", mcp.Description("Also return the activity timeline. Default false.")),
	)
	s.AddTool(tool, toolHandler(env, parseDocContext))
}

// parseDocContext reads get_doc_context's arguments.
func parseDocContext(req mcp.CallToolRequest) (toolCall, error) {
	doctype, err := req.RequireString("doctype")
	if err != nil {
		return nil, err
	}
	name, err := nameArg(req, "name", false)
	if err != nil {
		return nil, err
	}
	name = docNameOrSingle(name, doctype)
	opts := docContextOpts{Links: req.GetBool("links", true), Timeline: req.GetBool("timeline", false)}
	return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
		// The policy is needed before anything is read: without it
		// the parts from other DocTypes cannot be filtered.
		policy, ok := policyFrom(ctx)
		if !ok {
			return nil, fmt.Errorf("policy: no policy to filter the document context with")
		}
		raw, err := fetchDocContext(ctx, c, doctype, name, opts)
		if err != nil {
			return nil, err
		}
		d := compactDocContext(raw)
		policy.filterDocContext(d)
		trimDocContext(d)
		return d, nil
	}, nil
}

// contextSections are the parts of a document context and the DocType each
// is read from. The document's own DocType is checked before the call.
var contextSections = []struct {
	name, doctype string
}{
	{"versions", "Version"}, {"comments", "Comment"}, {"workflow_log", "Comment"},
	{"communications", "Communication"}, {"attachments", "File"}, {"assignments", "ToDo"},
	{"shares", "DocShare"}, {"tags", "Tag Link"},
}

// filterDocContext empties the sections, linked DocTypes and timeline
// entries whose DocType the DocType rules do not allow reading, and records
// what it took out. A timeline entry of an unknown kind counts as an
// unknown DocType: an allow list drops it.
func (p mcpPolicy) filterDocContext(d *docContext) {
	h := &ctxHidden{Sections: []string{}}
	allowed := func(dt string) bool { return p.doctypeAllowed(dt, false) == nil }
	for _, s := range contextSections {
		if allowed(s.doctype) {
			continue
		}
		h.Sections = append(h.Sections, s.name)
		switch s.name {
		case "versions":
			d.Versions = []ctxVersion{}
		case "comments":
			d.Comments = []ctxComment{}
		case "workflow_log":
			d.WorkflowLog = []ctxLog{}
		case "communications":
			d.Communications = []ctxCommunication{}
		case "attachments":
			d.Attachments = []ctxAttachment{}
		case "assignments":
			d.Assignments = []ctxAssignment{}
		case "shares":
			d.Shares = []ctxShare{}
		case "tags":
			d.Tags = []string{}
		}
	}
	if d.Links != nil {
		kept := []ctxLink{}
		for _, l := range d.Links {
			if dt := strings.TrimSpace(l.Doctype); dt != "" && allowed(dt) {
				kept = append(kept, l)
			}
		}
		h.LinkedDoctypes = len(d.Links) - len(kept)
		d.Links = kept
	}
	if d.Timeline != nil {
		kept := []ctxActivity{}
		for _, a := range d.Timeline {
			if a.source == "" || allowed(a.source) {
				kept = append(kept, a)
			}
		}
		h.TimelineEntries = len(d.Timeline) - len(kept)
		d.Timeline = kept
	}
	d.HiddenByPolicy = h
}

// trimDocContext keeps the newest comments and timeline entries, so a long
// history fits a tool result.
func trimDocContext(d *docContext) {
	omit := func(key string, n int) {
		if d.Omitted == nil {
			d.Omitted = map[string]int{}
		}
		d.Omitted[key] = n
	}
	if n := len(d.Comments) - mcpContextComments; n > 0 {
		d.Comments = d.Comments[:mcpContextComments] // newest first
		omit("comments", n)
	}
	if n := len(d.Timeline) - mcpContextTimeline; n > 0 {
		d.Timeline = d.Timeline[n:] // oldest first
		omit("timeline", n)
	}
}
