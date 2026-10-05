package cmd

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// Destructive MCP calls (delete_doc, bulk_delete, cancel_doc, a merging
// rename_doc and the call_method equivalents) ask the user through MCP
// elicitation before anything is sent to the site (T1.4c). The question is
// a multi round-trip input request: the client asks the user and calls the
// tool again with the answer and the request state ffc issued, which binds
// the answer to this site, tool and arguments. For clients older than the
// 2026-07-28 protocol, mcp-go sends elicitation/create and calls the
// handler again itself. Either way the handler runs twice, so the audit log
// has a confirm_pending line and then the outcome.
//
// Confirmation trusts the MCP client to have shown the question to a
// person; ffc cannot tell a person's answer from the client's.

const (
	confirmID  = "confirm"        // the key of ffc's one input request
	confirmTTL = 10 * time.Minute // how long a question stays answerable
)

// destructiveMethods delete, cancel or discard documents, or run workflow
// actions (which may submit or cancel); call_method confirms them like
// delete_doc and cancel_doc. Best effort, like the DocType rules: a custom
// method can delete without being listed.
var destructiveMethods = []string{
	"frappe.client.delete", "frappe.client.cancel",
	"frappe.desk.reportview.delete_items",
	"frappe.desk.form.save.cancel", "frappe.desk.form.save.discard",
	"frappe.desk.form.linked_with.cancel_all_linked_docs",
	"frappe.model.workflow.apply_workflow", "frappe.model.workflow.bulk_workflow_approval",
}

// argMethods are destructive only for some arguments: the method checks
// whether the named argument holds one of the values (any case).
var argMethods = []struct {
	method, arg string
	values      []string
}{
	// Whitelisted controller methods: Document.cancel, discard, rename.
	{"frappe.handler.run_doc_method", "method", []string{"cancel", "discard", "rename"}},
	{"frappe.desk.form.save.savedocs", "action", []string{"Cancel"}},
	{"frappe.desk.doctype.bulk_update.bulk_update.submit_cancel_or_update_docs", "action", []string{"cancel"}},
}

// widenMethods give users access to a document: frappe.share.add shares
// it, and an assignment shares it read-only with an assignee who cannot
// read it (assign_to.py, _add). frappe.share.set_permission widens only
// with a true value (see widensAccess).
var widenMethods = []string{
	"frappe.share.add",
	"frappe.desk.form.assign_to.add", "frappe.desk.form.assign_to.add_multiple",
}

// widensAccess reports whether a call_method call may give users access to
// a document.
func widensAccess(method string, args map[string]interface{}) bool {
	names := methodNames(method)
	if anyMatch(widenMethods, names) {
		return true
	}
	if anyMatch([]string{"frappe.share.set_permission"}, names) {
		v, ok := args["value"]
		return !ok || cintTrue(v) // Frappe's default value is 1
	}
	return false
}

// cintTrue reads a flag as Frappe's cint does: a string is a number ("0"
// and text that is not a number are 0), a bool or number is itself.
func cintTrue(v interface{}) bool {
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return err == nil && int64(f) != 0
	}
	return truthy(v)
}

// mergeMethods rename a document and, with merge set, merge it into another
// one, which then no longer exists.
var mergeMethods = []string{"frappe.client.rename_doc", "frappe.model.rename_doc.update_document_title"}

// needsConfirm reports whether a call destroys, cancels or merges
// documents, runs a workflow action, or may widen who can see a document
// (a share, or an assignment, which shares the document with an assignee
// who cannot read it).
func needsConfirm(req mcp.CallToolRequest, method string) bool {
	switch req.Params.Name {
	case "delete_doc", "bulk_delete", "cancel_doc", "apply_workflow", "share_doc", "assign_to":
		return true
	case "rename_doc":
		return req.GetBool("merge", false) // as the tool reads it
	case "call_method":
		names := methodNames(method)
		args := methodArgs(req.GetArguments()["args"])
		if anyMatch(destructiveMethods, names) || widensAccess(method, args) {
			return true
		}
		if anyMatch(mergeMethods, names) {
			return truthy(args["merge"])
		}
		for _, m := range argMethods {
			if anyMatch([]string{m.method}, names) {
				v, _ := args[m.arg].(string)
				for _, x := range m.values {
					if strings.EqualFold(strings.TrimSpace(v), x) {
						return true
					}
				}
			}
		}
	}
	return false
}

// methodArgs decodes call_method arguments given as an object or JSON text.
func methodArgs(args interface{}) map[string]interface{} {
	if s, ok := args.(string); ok {
		var m map[string]interface{}
		_ = json.Unmarshal([]byte(s), &m)
		return m
	}
	m, _ := args.(map[string]interface{})
	return m
}

// truthy reports whether an argument is set. Anything but an absent,
// false, zero or empty value counts: Python treats the string "0" as true.
func truthy(v interface{}) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case json.Number:
		f, err := v.Float64()
		return err != nil || f != 0
	case string:
		return v != ""
	}
	return true
}

// What the audit line records about the confirmation of a call that needed
// one (auditRecord.Confirm).
const (
	confirmYes         = "confirmed"   // the user said yes
	confirmUnsupported = "unsupported" // if-supported, and the client cannot ask
	confirmOff         = "never"       // confirm: never
)

// confirmRank orders the modes from loosest to strictest.
var confirmRank = map[string]int{config.ConfirmNever: 1, config.ConfirmIfSupported: 2, config.ConfirmAlways: 3}

// confirmMode is the stricter of the site's mode (if-supported when unset)
// and --confirm, which can only tighten it.
func (p mcpPolicy) confirmMode() string {
	mode := p.cfg.Confirm
	if mode == "" {
		mode = config.ConfirmIfSupported
	}
	if confirmRank[p.flag.Confirm] > confirmRank[mode] {
		mode = p.flag.Confirm
	}
	return mode
}

// canElicit reports whether the client said it can ask its user a form
// question: per request on the 2026-07-28 protocol, at initialize before.
func canElicit(ctx context.Context) bool {
	var caps *mcp.ClientCapabilities
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern {
		caps = info.ClientCapabilities
	} else if s, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		if _, ok := s.(server.SessionWithElicitation); ok {
			c := s.GetClientCapabilities()
			caps = &c
		}
	}
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	// "elicitation": {} means form mode only.
	return caps.Elicitation.Form != nil || caps.Elicitation.URL == nil
}

// confirmer issues and checks request states. The key is random per
// process, so a state is only good for the server that issued it.
type confirmer struct {
	key  []byte
	mu   sync.Mutex
	used map[string]time.Time // spent states, kept until they expire
}

var mcpConfirm = newConfirmer()

func newConfirmer() *confirmer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return &confirmer{key: key, used: map[string]time.Time{}}
}

// newState issues a state for a question about req, good until exp. The
// nonce keeps two identical calls in the same second apart.
func (c *confirmer) newState(site string, req mcp.CallToolRequest, exp time.Time) string {
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce)
	return c.state(site, req, strconv.FormatInt(exp.Unix(), 10)+"."+base64.RawURLEncoding.EncodeToString(nonce))
}

// state signs head (expiry and nonce) together with the site, the tool and
// its arguments.
func (c *confirmer) state(site string, req mcp.CallToolRequest, head string) string {
	args, _ := json.Marshal(req.GetArguments()) // map keys are sorted
	mac := hmac.New(sha256.New, c.key)
	fmt.Fprintf(mac, "%q %q %q ", site, req.Params.Name, head)
	mac.Write(args)
	return head + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// spend reports whether the call carries a state this process issued for
// exactly this call, unexpired and not used before, and marks it used, so
// one "yes" never runs a call twice.
func (c *confirmer) spend(site string, req mcp.CallToolRequest) bool {
	st := req.Params.RequestState
	i := strings.LastIndexByte(st, '.')
	if i < 0 {
		return false
	}
	head := st[:i]
	sec, _, _ := strings.Cut(head, ".")
	unix, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return false
	}
	now, exp := time.Now(), time.Unix(unix, 0)
	if !now.Before(exp) || !hmac.Equal([]byte(st), []byte(c.state(site, req, head))) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.used {
		if !now.Before(e) {
			delete(c.used, k)
		}
	}
	if _, spent := c.used[st]; spent {
		return false
	}
	c.used[st] = exp
	return true
}

// confirmSchema is the one-checkbox form the client shows.
var confirmSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"confirm": map[string]interface{}{
			"type": "boolean", "title": "Confirm", "default": false,
			"description": "Check to go ahead.",
		},
	},
	"required": []string{"confirm"},
}

// confirm runs before a destructive call. It returns nil to let the call
// go ahead, or the result to send instead: a question for the user, a
// refusal, or the user's "no".
func (p mcpPolicy) confirm(ctx context.Context, req mcp.CallToolRequest, sc toolScope, rec *auditRecord) *mcp.CallToolResult {
	if !sc.Confirm {
		return nil
	}
	mode := p.confirmMode()
	if mode == config.ConfirmNever {
		rec.Confirm = confirmOff
		return nil
	}
	// A retry carries the answer. A missing answer or a state that is not
	// ours, has expired or was used asks again rather than going ahead.
	if answer := server.ElicitationResponse(req.Params.InputResponses, confirmID); answer != nil && mcpConfirm.spend(p.site, req) {
		if answer.Action == mcp.ElicitationResponseActionAccept && confirmed(answer.Content) {
			rec.Confirm = confirmYes
			return nil
		}
		rec.Status = auditDeclined
		return mcp.NewToolResultError("cancelled by the user; nothing was changed")
	}
	if !canElicit(ctx) {
		if mode != config.ConfirmAlways {
			rec.Confirm = confirmUnsupported
			return nil
		}
		key := "--confirm always"
		if p.cfg.Confirm == config.ConfirmAlways {
			key = p.key("confirm") + ": always"
		}
		err := fmt.Errorf("policy: %s needs the user's confirmation (%s) and this MCP client cannot ask for it; nothing was changed. Run it in a terminal instead: %s",
			req.Params.Name, key, cliEquivalent(p.site, req, sc))
		rec.Status, rec.Error = auditDenied, err.Error()
		return mcp.NewToolResultError(err.Error())
	}
	rec.Status = auditPending
	state := mcpConfirm.newState(p.site, req, time.Now().Add(confirmTTL))
	return server.NewInputRequestBuilder(state).Elicit(confirmID, mcp.ElicitationParams{
		Message:         confirmMessage(p.site, req, sc),
		RequestedSchema: confirmSchema,
	}).ToolResult()
}

// confirmed reports whether the form came back with confirm checked.
func confirmed(content interface{}) bool {
	m, _ := content.(map[string]interface{})
	yes, _ := m["confirm"].(bool)
	return yes
}

// quoted shows a value the model chose: cut to max runes, then quoted with
// every control, format and invisible character escaped (\u200b), so it
// cannot pass for ffc's own words or hide what it names. It is not
// sanitised first: that would show another document's name.
func quoted(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return strconv.Quote(s)
	}
	return strconv.Quote(string(r[:max])) + "…"
}

// keyMethodArgs are shown first and in full (up to a cut per value) in a
// call_method question, so padding arguments cannot push them out of view.
var keyMethodArgs = []string{
	"doctype", "dt", "name", "dn", "docname", "docnames", "names", "items", "docs", "doc",
	"method", "action", "merge", "new_name", "old_name",
}

// methodSummary describes call_method arguments: the key ones with their
// values, then only the names of the rest.
func methodSummary(args interface{}) string {
	m := methodArgs(client.RedactArgs(args))
	if len(m) == 0 {
		return "no arguments"
	}
	var shown, rest []string
	seen := map[string]bool{}
	for _, k := range keyMethodArgs {
		if v, ok := m[k]; ok {
			b, _ := json.Marshal(v)
			shown = append(shown, k+"="+quoted(string(b), 300))
			seen[k] = true
		}
	}
	for k := range m {
		if !seen[k] {
			rest = append(rest, quoted(k, 40))
		}
	}
	sort.Strings(rest)
	out := strings.Join(shown, ", ")
	if len(rest) > 0 {
		if out != "" {
			out += "; "
		}
		out += fmt.Sprintf("%d other arguments: %s", len(rest), strings.Join(rest, ", "))
	}
	return out
}

// confirmMessage says what the call will do.
func confirmMessage(site string, req mcp.CallToolRequest, sc toolScope) string {
	args := req.GetArguments()
	dt := ""
	if len(sc.Doctypes) > 0 {
		dt = quoted(sc.Doctypes[0], 140)
	}
	name := func(i int) string { return quoted(sc.Names[i], 140) }
	var what string
	switch req.Params.Name {
	case "delete_doc":
		what = fmt.Sprintf("Delete %s %s.", dt, name(0))
	case "cancel_doc":
		what = fmt.Sprintf("Cancel %s %s.", dt, name(0))
	case "bulk_delete":
		const show = 10
		list := make([]string, 0, show)
		for i := range sc.Names {
			if i == show {
				break
			}
			list = append(list, name(i))
		}
		what = fmt.Sprintf("Delete %d %s documents: %s", len(sc.Names), dt, strings.Join(list, ", "))
		if more := len(sc.Names) - show; more > 0 {
			what += fmt.Sprintf(" and %d more", more)
		}
		what += "."
	case "rename_doc":
		to, _ := docName(args["new_name"])
		what = fmt.Sprintf("Merge %s %s into %s. %s will no longer exist.", dt, name(0), quoted(to, 140), name(0))
	case "apply_workflow":
		action, _ := args["action"].(string)
		what = fmt.Sprintf("Apply the workflow action %s to %s %s. It may submit or cancel the document.", quoted(action, 100), dt, name(0))
	case "call_method":
		what = fmt.Sprintf("Call %s with %s.", quoted(sc.Method, 200), methodSummary(args["args"]))
	case "share_doc":
		user, everyone, _ := shareTargetArgs(req)
		who := "every user"
		if !everyone {
			who = "user " + quoted(user, 140)
		}
		return fmt.Sprintf("An AI agent asks to share %s %s on site %s with %s (%s). They will be able to open it whatever their roles allow.",
			dt, name(0), confirmSite(site), who, shareRights(shareArgs(req)))
	case "assign_to":
		users, _ := listArg(req, "users", "user", true)
		list := make([]string, 0, len(users))
		for _, u := range users {
			list = append(list, quoted(u, 140))
		}
		return fmt.Sprintf("An AI agent asks to assign %s %s on site %s to %s. Each gets a ToDo and a notification; one who cannot read the document gets read access to it through a share.",
			dt, name(0), confirmSite(site), strings.Join(list, ", "))
	}
	if req.Params.Name == "call_method" && widensAccess(sc.Method, methodArgs(args["args"])) {
		return fmt.Sprintf("An AI agent asks to change site %s. %s It may give users access to documents beyond what their roles allow.", confirmSite(site), what)
	}
	return fmt.Sprintf("An AI agent asks to change site %s. %s This cannot be undone.", confirmSite(site), what)
}

// confirmSite shows the site a question is about.
func confirmSite(site string) string {
	if site = quoted(site, 100); site == `""` {
		return "(from the environment)"
	}
	return site
}

// shareArgs reads the rights share_doc grants, as the tool does.
func shareArgs(req mcp.CallToolRequest) client.ShareOptions {
	return client.ShareOptions{Write: req.GetBool("write", false), Submit: req.GetBool("submit", false),
		Share: req.GetBool("share", false), Notify: req.GetBool("notify", false)}
}

// cliEquivalent is the ffc command that does what the call asked, for a
// person to run (and confirm) in a terminal. When a value cannot be shown
// as is (control or invisible characters) or would not reach the command
// unchanged (a --names entry with a comma or edge spaces, which splitCSV
// would split or trim), only the command's name is given: anything else
// could name another document.
func cliEquivalent(site string, req mcp.CallToolRequest, sc toolScope) string {
	args := req.GetArguments()
	cmdName := "ffc " + strings.ReplaceAll(req.Params.Name, "_", "-")
	q := shellQuote
	parts := []string{"ffc"}
	if site != "" {
		parts = append(parts, "--site", q(site))
	}
	dt := ""
	if len(sc.Doctypes) > 0 {
		dt = sc.Doctypes[0]
	}
	switch req.Params.Name {
	case "delete_doc", "cancel_doc":
		parts = append(parts, strings.ReplaceAll(req.Params.Name, "_", "-"), "--doctype", q(dt), "--name", q(sc.Names[0]))
	case "bulk_delete":
		for _, n := range sc.Names {
			if strings.Contains(n, ",") || strings.TrimSpace(n) != n {
				return cmdName
			}
		}
		parts = append(parts, "bulk-delete", "--doctype", q(dt), "--names", q(strings.Join(sc.Names, ",")))
	case "rename_doc":
		to, _ := docName(args["new_name"])
		parts = append(parts, "rename-doc", "--doctype", q(dt), "--name", q(sc.Names[0]), "--to", q(to), "--merge")
	case "apply_workflow":
		action, _ := args["action"].(string)
		cmdName = "ffc workflow apply"
		parts = append(parts, "workflow", "apply", "--doctype", q(dt), "--name", q(sc.Names[0]), "--action", q(action))
	case "assign_to":
		users, _ := listArg(req, "users", "user", true)
		for _, u := range users {
			if strings.Contains(u, ",") {
				return cmdName
			}
		}
		parts = append(parts, "assign", "--doctype", q(dt), "--name", q(sc.Names[0]), "--to", q(strings.Join(users, ",")))
		for _, k := range []string{"description", "date", "priority"} {
			if v, _ := args[k].(string); v != "" {
				parts = append(parts, "--"+k, q(v))
			}
		}
	case "share_doc":
		user, everyone, _ := shareTargetArgs(req)
		parts = append(parts, "share", "--doctype", q(dt), "--name", q(sc.Names[0]))
		if everyone {
			parts = append(parts, "--everyone")
		} else {
			parts = append(parts, "--user", q(user))
		}
		o := shareArgs(req)
		for _, f := range []struct {
			on   bool
			flag string
		}{{o.Write, "--write"}, {o.Submit, "--submit"}, {o.Share, "--share"}, {o.Notify, "--notify"}} {
			if f.on {
				parts = append(parts, f.flag)
			}
		}
	case "call_method":
		parts = append(parts, "call-method", "--method", q(sc.Method))
		if a := args["args"]; a != nil {
			s, ok := a.(string)
			if !ok {
				b, _ := json.Marshal(a)
				s = string(b)
			}
			parts = append(parts, "--args", q(s))
		}
	}
	if cmd := strings.Join(parts, " "); text.Sanitize(cmd) == cmd {
		return cmd
	}
	return cmdName
}
