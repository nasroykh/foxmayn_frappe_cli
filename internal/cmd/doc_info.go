package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// doc-info flags
var (
	diDoctype  string
	diName     string
	diTimeline bool
	diLinks    bool
	diOnload   bool
	diFull     bool
)

// Clipping of the compact view: comment and log text, and each version
// value (a Long Text field can hold a whole page).
const (
	ctxTextMax  = 500
	ctxValueMax = 200
)

var docInfoCmd = &cobra.Command{
	Use:   "doc-info",
	Short: "Show a document's context: versions, comments, attachments, links",
	Long: `Show what the desk's form sidebar and timeline show for one document:
who changed which fields (the last 10 versions), comments, emails,
attachments, assignments, shares, tags and the workflow log.

It reads frappe.desk.form.load.get_docinfo, which changes nothing. Versions
exist only for DocTypes with "Track Changes" on.

  --links     also count the documents linked to it, as the form's
              Connections panel does (frappe.desk.notifications.get_open_count):
              the DocTypes come from the DocType's dashboard, each count stops
              at 100, and the counts ignore your permissions. DocTypes with no
              linked document are left out.
  --timeline  also read the activity timeline (Frappe from 2026 on): every
              change field by field with labels, emails, comments and logs,
              oldest first. Fields you may not read are left out by Frappe.
  --onload    load the document as the desk form does (getdoc) and show its
              __onload values, e.g. the party dashboard of a Customer or
              Supplier (billing this year, total unpaid). This WRITES: it adds
              a View Log entry and marks the document seen by you (on
              DocTypes that track views or seen) and runs the controller's
              onload.
  --full      with --json, print the raw responses instead of the compact view.

JSON (compact view):
  {doctype, name, permissions: [right...],
   versions: [{name, by, at, changed: [{field, from, to}], rows_added: {table: n},
               rows_removed: {table: n}, impersonated_by}],   newest first
   comments: [{name, by, at, text}], communications: [{name, subject, sender, at, medium}],
   attachments: [{name, file_name, file_url, is_private, size}],
   assignments: [{user, status, description}],
   shares: [{user, everyone, read, write, share, submit}], tags: [tag...],
   workflow_log: [{by, at, text}],
   links: [{doctype, count, open_count, capped, timed_out, internal, names}],   --links
   timeline: [{at, by, type, field, text}],   --timeline (oldest first)
   onload: {...},   --onload
   notes: [...]}   parts that could not be read
A change inside a child table row is field "items[2].qty" (rows count from 1).
Text is stripped of HTML and cut to 500 characters, version values to 200.
In links, capped means "at least count", and internal marks the documents
this one's own fields point to (their names are listed).

For Single DocTypes, --name can be omitted.

Examples:
  ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001
  ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --links --json
  ffc doc-info -d Customer -n "Acme" --onload
  ffc doc-info -d ToDo -n TD-0001 --timeline
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := docNameOrSingle(diName, diDoctype)
		opts := docContextOpts{Timeline: diTimeline, Links: diLinks, Onload: diOnload}
		res, err := callSite(cmd, fmt.Sprintf("Reading %s %s…", diDoctype, name), func(ctx context.Context, c *client.FrappeClient) (*docContextRaw, error) {
			return fetchDocContext(ctx, c, diDoctype, name, opts)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			if diFull {
				return printResult(res.full())
			}
			return printResult(compactDocContext(res))
		}
		printDocContext(compactDocContext(res))
		return nil
	},
}

func init() {
	docInfoCmd.Flags().StringVarP(&diDoctype, "doctype", "d", "", "Frappe DocType (required)")
	docInfoCmd.Flags().StringVarP(&diName, "name", "n", "", "Name of the document (defaults to the DocType name for Single DocTypes)")
	docInfoCmd.Flags().BoolVar(&diTimeline, "timeline", false, "Also read the activity timeline (Frappe from 2026 on)")
	docInfoCmd.Flags().BoolVar(&diLinks, "links", false, "Also count linked documents per DocType (the Connections panel)")
	docInfoCmd.Flags().BoolVar(&diOnload, "onload", false, "Load the document as the desk does and show __onload (writes a view log and marks it seen)")
	docInfoCmd.Flags().BoolVar(&diFull, "full", false, "Print the raw Frappe responses (JSON mode only)")
	_ = docInfoCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(docInfoCmd)
}

// docContextOpts selects the optional parts of a document context.
type docContextOpts struct {
	Timeline, Links, Onload bool
}

// docContextRaw holds the site's answers. An optional part that failed
// with an API error is a note, not a failure: the docinfo is still worth
// returning.
type docContextRaw struct {
	Doctype, Name string
	Docinfo       map[string]interface{}
	Timeline      map[string]interface{}
	Links         map[string]interface{}
	Onload        map[string]interface{}
	onloadRead    bool
	Notes         []string
	// access says which fields the user may read; nil when it could not be
	// read, and the versions are then left out (fail closed).
	access *client.FieldAccess
}

// full is the --full view: the raw answers, keyed by part.
func (r *docContextRaw) full() map[string]interface{} {
	out := map[string]interface{}{"docinfo": r.Docinfo}
	if r.Timeline != nil {
		out["timeline"] = r.Timeline
	}
	if r.Links != nil {
		out["links"] = r.Links
	}
	if r.onloadRead {
		out["onload"] = r.Onload
	}
	if len(r.Notes) > 0 {
		out["notes"] = r.Notes
	}
	return out
}

// fetchDocContext reads the docinfo (through getdoc when onload is wanted:
// it returns both) and the optional parts, one request each.
func fetchDocContext(ctx context.Context, c *client.FrappeClient, doctype, name string, o docContextOpts) (*docContextRaw, error) {
	r := &docContextRaw{Doctype: doctype, Name: name}
	var err error
	if o.Onload {
		var doc map[string]interface{}
		if doc, r.Docinfo, err = c.FormLoad(ctx, doctype, name); err != nil {
			return nil, err
		}
		r.Onload, _ = doc["__onload"].(map[string]interface{})
		if r.Onload == nil {
			r.Onload = map[string]interface{}{}
		}
		r.onloadRead = true
	} else if r.Docinfo, err = c.DocInfo(ctx, doctype, name); err != nil {
		return nil, err
	}
	// get_versions is a plain get_all on Version: its data holds every
	// changed field, whatever the user may read. Filter it like the timeline
	// does, which needs the meta and the user's roles.
	if len(ctxList(r.Docinfo["versions"])) > 0 {
		if r.access, err = c.ReadableFields(ctx, doctype); err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			r.Notes = append(r.Notes, fmt.Sprintf("versions left out: the fields you may read are unknown (%v)", err))
		}
	}
	optional := func(part string, fn func() (map[string]interface{}, error)) (map[string]interface{}, error) {
		v, err := fn()
		var api *client.APIError
		switch {
		case err == nil:
			return v, nil
		case errors.Is(err, client.ErrNoTimeline):
			r.Notes = append(r.Notes, client.ErrNoTimeline.Error())
			return nil, nil
		case errors.As(err, &api):
			r.Notes = append(r.Notes, fmt.Sprintf("%s unavailable: %v", part, err))
			return nil, nil
		}
		return nil, err
	}
	if o.Links {
		if r.Links, err = optional("links", func() (map[string]interface{}, error) { return c.LinkCounts(ctx, doctype, name) }); err != nil {
			return nil, err
		}
	}
	if o.Timeline {
		if r.Timeline, err = optional("timeline", func() (map[string]interface{}, error) { return c.ActivityTimeline(ctx, doctype, name) }); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// ─── compact view ───────────────────────────────────────────────────────────

type ctxChange struct {
	Field string      `json:"field"`
	From  interface{} `json:"from"`
	To    interface{} `json:"to"`
}

type ctxVersion struct {
	Name           string         `json:"name"`
	By             string         `json:"by"`
	At             string         `json:"at"`
	Changed        []ctxChange    `json:"changed"`
	RowsAdded      map[string]int `json:"rows_added,omitempty"`
	RowsRemoved    map[string]int `json:"rows_removed,omitempty"`
	ImpersonatedBy string         `json:"impersonated_by,omitempty"`
	// HiddenFields counts the changes left out: fields the user may not
	// read, Password fields, fields no longer in the meta.
	HiddenFields int `json:"hidden_fields,omitempty"`
}

type ctxComment struct {
	Name string `json:"name"`
	By   string `json:"by"`
	At   string `json:"at"`
	Text string `json:"text"`
}

type ctxCommunication struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Sender  string `json:"sender"`
	At      string `json:"at"`
	Medium  string `json:"medium,omitempty"`
}

type ctxAttachment struct {
	Name     string      `json:"name"`
	FileName string      `json:"file_name"`
	FileURL  string      `json:"file_url"`
	Private  bool        `json:"is_private"`
	Size     interface{} `json:"size,omitempty"`
}

type ctxAssignment struct {
	User        string `json:"user"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
}

type ctxShare struct {
	User     string `json:"user,omitempty"`
	Everyone bool   `json:"everyone,omitempty"`
	Read     bool   `json:"read"`
	Write    bool   `json:"write"`
	Share    bool   `json:"share"`
	Submit   bool   `json:"submit"`
}

type ctxLog struct {
	By   string `json:"by"`
	At   string `json:"at"`
	Text string `json:"text"`
}

type ctxLink struct {
	Doctype   string   `json:"doctype"`
	Count     int      `json:"count"`
	OpenCount int      `json:"open_count,omitempty"`
	Capped    bool     `json:"capped,omitempty"`
	TimedOut  bool     `json:"timed_out,omitempty"`
	Internal  bool     `json:"internal,omitempty"`
	Names     []string `json:"names,omitempty"`
}

type ctxActivity struct {
	At    string `json:"at"`
	By    string `json:"by"`
	Type  string `json:"type"`
	Field string `json:"field,omitempty"`
	Text  string `json:"text"`
	// sources are the DocTypes the entry was read from (none for the
	// document itself, "?" when unknown), for the MCP policy filter.
	sources []string
}

// ctxHidden is what the MCP policy took out of a context.
type ctxHidden struct {
	Sections        []string `json:"sections"`
	LinkedDoctypes  int      `json:"linked_doctypes"`
	TimelineEntries int      `json:"timeline_entries"`
}

// docContext is the compact view shared by doc-info --json and the
// get_doc_context MCP tool. Its shape is part of the CLI contract.
type docContext struct {
	Doctype        string                 `json:"doctype"`
	Name           string                 `json:"name"`
	Permissions    []string               `json:"permissions"`
	Versions       []ctxVersion           `json:"versions"`
	Comments       []ctxComment           `json:"comments"`
	Communications []ctxCommunication     `json:"communications"`
	Attachments    []ctxAttachment        `json:"attachments"`
	Assignments    []ctxAssignment        `json:"assignments"`
	Shares         []ctxShare             `json:"shares"`
	Tags           []string               `json:"tags"`
	WorkflowLog    []ctxLog               `json:"workflow_log"`
	Links          []ctxLink              `json:"links,omitzero"`
	Timeline       []ctxActivity          `json:"timeline,omitzero"`
	Omitted        map[string]int         `json:"omitted,omitempty"`
	Onload         map[string]interface{} `json:"onload,omitzero"`
	Notes          []string               `json:"notes,omitempty"`
	HiddenByPolicy *ctxHidden             `json:"hidden_by_policy,omitempty"`
}

// compactDocContext turns the site's answers into the compact view.
func compactDocContext(r *docContextRaw) *docContext {
	d := r.Docinfo
	out := &docContext{
		Doctype: r.Doctype, Name: r.Name,
		Permissions: []string{}, Versions: []ctxVersion{}, Comments: []ctxComment{},
		Communications: []ctxCommunication{}, Attachments: []ctxAttachment{},
		Assignments: []ctxAssignment{}, Shares: []ctxShare{}, Tags: []string{}, WorkflowLog: []ctxLog{},
		Notes: r.Notes,
	}
	if s := ctxStr(d["name"]); s != "" {
		out.Name = s // the site's spelling
	}
	if p, ok := d["permissions"].(map[string]interface{}); ok {
		for k, v := range p {
			if ctxTruthy(v) {
				out.Permissions = append(out.Permissions, k)
			}
		}
		sort.Strings(out.Permissions)
	}
	if r.access != nil {
		for _, v := range ctxRows(d["versions"]) {
			out.Versions = append(out.Versions, compactVersion(v, r.access))
		}
	}
	for _, c := range ctxRows(d["comments"]) {
		out.Comments = append(out.Comments, ctxComment{Name: ctxStr(c["name"]), By: ctxStr(c["owner"]), At: ctxStr(c["creation"]), Text: ctxPlain(ctxStr(c["content"]), ctxTextMax)})
	}
	sort.SliceStable(out.Comments, func(i, j int) bool { return out.Comments[i].At > out.Comments[j].At })
	for _, c := range ctxRows(d["communications"]) {
		at := ctxStr(c["communication_date"])
		if at == "" {
			at = ctxStr(c["creation"])
		}
		out.Communications = append(out.Communications, ctxCommunication{Name: ctxStr(c["name"]), Subject: ctxPlain(ctxStr(c["subject"]), ctxTextMax), Sender: ctxStr(c["sender"]), At: at, Medium: ctxStr(c["communication_medium"])})
	}
	for _, a := range ctxRows(d["attachments"]) {
		out.Attachments = append(out.Attachments, ctxAttachment{Name: ctxStr(a["name"]), FileName: ctxStr(a["file_name"]), FileURL: ctxStr(a["file_url"]), Private: ctxTruthy(a["is_private"]), Size: a["file_size"]})
	}
	for _, a := range ctxRows(d["assignments"]) {
		// get_assignments selects allocated_to as owner.
		out.Assignments = append(out.Assignments, ctxAssignment{User: ctxStr(a["owner"]), Status: ctxStr(a["status"]), Description: ctxPlain(ctxStr(a["description"]), ctxTextMax)})
	}
	for _, s := range ctxRows(d["shared"]) {
		out.Shares = append(out.Shares, ctxShare{User: ctxStr(s["user"]), Everyone: ctxTruthy(s["everyone"]), Read: ctxTruthy(s["read"]), Write: ctxTruthy(s["write"]), Share: ctxTruthy(s["share"]), Submit: ctxTruthy(s["submit"])})
	}
	for _, t := range strings.Split(ctxStr(d["tags"]), ",") {
		if t = strings.TrimSpace(t); t != "" {
			out.Tags = append(out.Tags, t)
		}
	}
	for _, l := range ctxRows(d["workflow_logs"]) {
		out.WorkflowLog = append(out.WorkflowLog, ctxLog{By: ctxStr(l["owner"]), At: ctxStr(l["creation"]), Text: ctxPlain(ctxStr(l["content"]), ctxTextMax)})
	}
	if r.Links != nil {
		if links, ok := compactLinks(r.Links); ok {
			out.Links = links
		} else {
			// get_open_count answers {"count": []} when a count query
			// ran over its one-second limit.
			out.Notes = append(out.Notes, "link counts timed out")
		}
	}
	if r.Timeline != nil {
		out.Timeline = compactTimeline(r.Timeline)
	}
	if r.onloadRead {
		out.Onload = r.Onload
	}
	return out
}

// compactVersion flattens a Version's data: changed fields, then the
// fields changed inside child rows as "table[row].field". Changes to fields
// the user may not read (access) are left out and counted in hidden_fields,
// rows added or removed in such a table too.
func compactVersion(v map[string]interface{}, access *client.FieldAccess) ctxVersion {
	cv := ctxVersion{Name: ctxStr(v["name"]), By: ctxStr(v["owner"]), At: ctxStr(v["creation"]), Changed: []ctxChange{}}
	var data struct {
		Changed        [][]interface{} `json:"changed"`
		Added          [][]interface{} `json:"added"`
		Removed        [][]interface{} `json:"removed"`
		RowChanged     [][]interface{} `json:"row_changed"`
		ImpersonatedBy string          `json:"impersonated_by"`
	}
	dec := json.NewDecoder(strings.NewReader(ctxStr(v["data"])))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return cv
	}
	cv.ImpersonatedBy = data.ImpersonatedBy
	for _, c := range data.Changed {
		if len(c) < 3 {
			continue
		}
		if !access.Readable(ctxStr(c[0])) {
			cv.HiddenFields++
			continue
		}
		cv.Changed = append(cv.Changed, ctxChange{Field: ctxStr(c[0]), From: ctxClipValue(c[1]), To: ctxClipValue(c[2])})
	}
	// row_changed entries are [table, row index, row name, [[field, old, new]...]].
	for _, rc := range data.RowChanged {
		if len(rc) < 4 {
			continue
		}
		row := fmt.Sprint(rc[1])
		if n, ok := rc[1].(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				row = fmt.Sprint(i + 1)
			}
		}
		changes, _ := rc[3].([]interface{})
		for _, ch := range changes {
			c, ok := ch.([]interface{})
			if !ok || len(c) < 3 {
				continue
			}
			if !access.ReadableRow(ctxStr(rc[0]), ctxStr(c[0])) {
				cv.HiddenFields++
				continue
			}
			cv.Changed = append(cv.Changed, ctxChange{Field: fmt.Sprintf("%s[%s].%s", ctxStr(rc[0]), row, ctxStr(c[0])), From: ctxClipValue(c[1]), To: ctxClipValue(c[2])})
		}
	}
	count := func(entries [][]interface{}) map[string]int {
		if len(entries) == 0 {
			return nil
		}
		m := map[string]int{}
		for _, e := range entries {
			if len(e) == 0 {
				continue
			}
			if !access.Readable(ctxStr(e[0])) {
				cv.HiddenFields++
				continue
			}
			m[ctxStr(e[0])]++
		}
		if len(m) == 0 {
			return nil
		}
		return m
	}
	cv.RowsAdded, cv.RowsRemoved = count(data.Added), count(data.Removed)
	return cv
}

// compactLinks keeps the linked DocTypes that have documents, the ones
// this document points to (internal) first. ok is false when the answer
// holds no counts.
func compactLinks(raw map[string]interface{}) (links []ctxLink, ok bool) {
	out := []ctxLink{}
	counts, ok := raw["count"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	for _, l := range ctxRows(counts["internal_links_found"]) {
		link := ctxLink{Doctype: ctxStr(l["doctype"]), Count: ctxInt(l["count"]), Internal: true}
		for _, n := range ctxList(l["names"]) {
			link.Names = append(link.Names, fmt.Sprint(n))
		}
		if link.Count > 0 {
			out = append(out, link)
		}
	}
	for _, l := range ctxRows(counts["external_links_found"]) {
		link := ctxLink{Doctype: ctxStr(l["doctype"]), Count: ctxInt(l["count"]), OpenCount: ctxInt(l["open_count"])}
		if s, ok := l["count"].(string); ok && s == "?" {
			link.TimedOut = true
		}
		link.Capped = link.Count >= 100 // get_doc_count reads at most 100 rows
		if link.Count > 0 || link.TimedOut {
			out = append(out, link)
		}
	}
	return out, true
}

// compactTimeline turns activities into one line each, oldest first as
// Frappe sorts them.
func compactTimeline(raw map[string]interface{}) []ctxActivity {
	out := []ctxActivity{}
	for _, a := range ctxRows(raw["activities"]) {
		data, _ := a["data"].(map[string]interface{})
		author, _ := a["author"].(map[string]interface{})
		by := ctxStr(author["fullname"])
		if by == "" {
			by = ctxStr(author["email"])
		}
		e := ctxActivity{At: ctxStr(a["timestamp"]), By: by, Type: ctxStr(a["type"]), sources: []string{"?"}}
		switch e.Type {
		case "version":
			e.sources, e.Field = []string{"Version"}, ctxStr(data["fieldname"])
			if ctxStr(data["type"]) == "diff" {
				e.Text = ctxStr(data["prefix"])
				if from := ctxStr(data["from"]); from != "" {
					e.Text += ": " + ctxClip(from, ctxValueMax) + " → "
				} else {
					e.Text += " "
				}
				e.Text += ctxClip(ctxStr(data["to"]), ctxValueMax)
			} else {
				e.Text = ctxStr(data["text"])
			}
		case "comment":
			e.sources, e.Text = []string{"Comment"}, ctxPlain(ctxStr(data["content"]), ctxTextMax)
		case "email":
			e.sources, e.Text = []string{"Communication"}, ctxPlain(ctxStr(data["subject"]), ctxTextMax)
		case "attachment_log":
			// A Comment of type Attachment that names a File.
			e.sources, e.Text = []string{"Comment", "File"}, strings.TrimSpace(ctxStr(data["action"])+" "+ctxStr(data["fileName"]))
		case "log":
			e.sources = logSources(ctxStr(data["subtype"]))
			e.Text = ctxPlain(ctxStr(data["text"]), ctxTextMax)
		default:
			e.Text = ctxPlain(ctxStr(data["text"]), ctxTextMax)
		}
		out = append(out, e)
	}
	return out
}

// logSources maps a timeline log entry to the DocTypes it shows, by its
// subtype (frappe/desk/form/activity.py): assignment logs name the assignee
// and the ToDo's description, share logs the users, so each needs Comment
// and the DocType it reports on. An unknown subtype is an unknown DocType.
func logSources(subtype string) []string {
	switch subtype {
	case "created", "edited":
		return nil // the document's own creation and last edit
	case "view":
		return []string{"View Log"}
	case "milestone":
		return []string{"Milestone"}
	case "assigned", "assignment_completed":
		return []string{"Comment", "ToDo"}
	case "shared":
		return []string{"Comment", "DocShare"}
	case "like", "workflow", "info":
		return []string{"Comment"}
	}
	return []string{"Comment", "?"}
}

// ─── human view ─────────────────────────────────────────────────────────────

// maxVersionLines caps the changes printed per version.
const maxVersionLines = 10

func printDocContext(d *docContext) {
	output.PrintTitle(d.Doctype + " " + d.Name)
	var lines []string
	for _, v := range d.Versions {
		head := ctxTime(v.At) + "  " + v.By
		if v.ImpersonatedBy != "" {
			head += " (impersonated by " + v.ImpersonatedBy + ")"
		}
		lines = append(lines, head)
		for i, c := range v.Changed {
			if i == maxVersionLines {
				lines = append(lines, fmt.Sprintf("  … %d more", len(v.Changed)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("  %s: %s → %s", c.Field, ctxValueText(c.From), ctxValueText(c.To)))
		}
		for _, k := range ctxKeys(v.RowsAdded) {
			lines = append(lines, fmt.Sprintf("  %s: %d row(s) added", k, v.RowsAdded[k]))
		}
		for _, k := range ctxKeys(v.RowsRemoved) {
			lines = append(lines, fmt.Sprintf("  %s: %d row(s) removed", k, v.RowsRemoved[k]))
		}
	}
	output.PrintSection("Versions", lines)

	lines = nil
	for _, c := range d.Comments {
		lines = append(lines, fmt.Sprintf("%s  %s: %s", ctxTime(c.At), c.By, oneLine(c.Text, 100)))
	}
	output.PrintSection("Comments", lines)

	if len(d.Communications) > 0 {
		lines = nil
		for _, c := range d.Communications {
			lines = append(lines, fmt.Sprintf("%s  %s: %s", ctxTime(c.At), c.Sender, oneLine(c.Subject, 100)))
		}
		output.PrintSection("Communications", lines)
	}

	lines = nil
	for _, a := range d.Attachments {
		l := a.FileName
		if a.Private {
			l += " (private)"
		}
		lines = append(lines, l)
	}
	output.PrintSection("Attachments", lines)

	lines = nil
	for _, a := range d.Assignments {
		l := a.User + "  " + a.Status
		if a.Description != "" {
			l += "  " + oneLine(a.Description, 80)
		}
		lines = append(lines, l)
	}
	output.PrintSection("Assignments", lines)

	lines = nil
	for _, s := range d.Shares {
		who := s.User
		if s.Everyone {
			who = "everyone"
		}
		var rights []string
		for _, r := range []struct {
			on   bool
			name string
		}{{s.Read, "read"}, {s.Write, "write"}, {s.Share, "share"}, {s.Submit, "submit"}} {
			if r.on {
				rights = append(rights, r.name)
			}
		}
		lines = append(lines, who+": "+strings.Join(rights, ", "))
	}
	output.PrintSection("Shared with", lines)

	lines = nil
	if len(d.Tags) > 0 {
		lines = []string{strings.Join(d.Tags, ", ")}
	}
	output.PrintSection("Tags", lines)

	lines = nil
	for _, l := range d.WorkflowLog {
		lines = append(lines, fmt.Sprintf("%s  %s: %s", ctxTime(l.At), l.By, oneLine(l.Text, 100)))
	}
	output.PrintSection("Workflow log", lines)

	if d.Links != nil {
		lines = nil
		for _, l := range d.Links {
			switch {
			case l.Internal:
				lines = append(lines, fmt.Sprintf("%s: %s (linked from this document)", l.Doctype, strings.Join(l.Names, ", ")))
			case l.TimedOut:
				lines = append(lines, l.Doctype+": ? (count timed out)")
			default:
				n := fmt.Sprint(l.Count)
				if l.Capped {
					n += "+"
				}
				if l.OpenCount > 0 {
					n += fmt.Sprintf(" (%d open)", l.OpenCount)
				}
				lines = append(lines, l.Doctype+": "+n)
			}
		}
		output.PrintSection("Links", lines)
	}
	if d.Timeline != nil {
		lines = nil
		for _, a := range d.Timeline {
			lines = append(lines, fmt.Sprintf("%s  %s: %s", ctxTime(a.At), a.By, oneLine(a.Text, 120)))
		}
		output.PrintSection("Timeline", lines)
	}
	if d.Onload != nil {
		output.PrintSection("Onload", onloadLines(d.Onload))
	}
	for _, n := range d.Notes {
		output.PrintWarning("note: " + n)
	}
}

// onloadLines shows the party dashboard of a Customer or Supplier as one
// line per company, and any other __onload value as JSON.
func onloadLines(onload map[string]interface{}) []string {
	var lines []string
	for _, k := range ctxKeys(onload) {
		v := onload[k]
		if k == "dashboard_info" {
			for _, r := range ctxRows(v) {
				lines = append(lines, fmt.Sprintf("%s: billing this year %s, total unpaid %s %s",
					ctxStr(r["company"]), ctxValueText(r["billing_this_year"]), ctxValueText(r["total_unpaid"]), ctxStr(r["currency"])))
			}
			if len(ctxRows(v)) == 0 {
				lines = append(lines, "dashboard_info: no submitted invoices")
			}
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		lines = append(lines, k+": "+oneLine(string(b), 160))
	}
	return lines
}

// ─── helpers ────────────────────────────────────────────────────────────────

func ctxStr(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	case json.Number:
		return s.String()
	}
	return fmt.Sprint(v)
}

func ctxTruthy(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		return x.String() != "0" && x.String() != "0.0"
	case float64:
		return x != 0
	case string:
		return x != "" && x != "0"
	}
	return false
}

func ctxInt(v interface{}) int {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
		if f, err := x.Float64(); err == nil {
			return int(f)
		}
	case float64:
		return int(x)
	}
	return 0
}

func ctxList(v interface{}) []interface{} {
	l, _ := v.([]interface{})
	return l
}

func ctxRows(v interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	for _, x := range ctxList(v) {
		if m, ok := x.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func ctxKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var ctxTagRE = regexp.MustCompile(`<[^>]*>`)

// plainText strips HTML tags and entities and cuts the text to max runes.
func ctxPlain(s string, max int) string {
	s = text.Sanitize(html.UnescapeString(ctxTagRE.ReplaceAllString(s, " ")))
	return ctxClip(strings.Join(strings.Fields(s), " "), max)
}

func ctxClip(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// clipValue cuts a long string value; other values pass unchanged.
func ctxClipValue(v interface{}) interface{} {
	if s, ok := v.(string); ok {
		return ctxClip(s, ctxValueMax)
	}
	return v
}

func ctxValueText(v interface{}) string {
	if v == nil || v == "" {
		return "∅"
	}
	return oneLine(ctxStr(v), 60)
}

// shortTime drops the microseconds of a Frappe timestamp.
func ctxTime(ts string) string {
	if len(ts) > 19 {
		return ts[:19]
	}
	return ts
}
