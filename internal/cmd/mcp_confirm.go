package cmd

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// destructiveMethods delete or cancel documents; call_method confirms them
// like delete_doc and cancel_doc. Best effort, like the DocType rules: a
// custom method can delete without being listed.
var destructiveMethods = []string{
	"frappe.client.delete", "frappe.client.cancel",
	"frappe.desk.reportview.delete_items", "frappe.desk.form.save.cancel",
}

// mergeMethods rename a document and, with merge set, merge it into another
// one, which then no longer exists.
var mergeMethods = []string{"frappe.client.rename_doc", "frappe.model.rename_doc.update_document_title"}

// needsConfirm reports whether a call destroys or merges documents.
func needsConfirm(req mcp.CallToolRequest, method string) bool {
	switch req.Params.Name {
	case "delete_doc", "bulk_delete", "cancel_doc":
		return true
	case "rename_doc":
		return req.GetBool("merge", false) // as the tool reads it
	case "call_method":
		names := methodNames(method)
		return anyMatch(destructiveMethods, names) ||
			anyMatch(mergeMethods, names) && mergeSet(req.GetArguments()["args"])
	}
	return false
}

// mergeSet reports whether method arguments (an object or JSON text) set
// merge. Anything but an absent, false, zero or empty value counts: Python
// treats the string "0" as true.
func mergeSet(args interface{}) bool {
	if s, ok := args.(string); ok {
		var m map[string]interface{}
		if json.Unmarshal([]byte(s), &m) != nil {
			return false
		}
		args = m
	}
	m, _ := args.(map[string]interface{})
	switch v := m["merge"].(type) {
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
			"description": "Check to go ahead. This cannot be undone.",
		},
	},
	"required": []string{"confirm"},
}

// confirm runs before a destructive call. It returns nil to let the call
// go ahead, or the result to send instead: a question for the user, a
// refusal, or the user's "no".
func (p mcpPolicy) confirm(ctx context.Context, req mcp.CallToolRequest, sc toolScope, rec *auditRecord) *mcp.CallToolResult {
	mode := p.confirmMode()
	if !sc.Confirm || mode == config.ConfirmNever {
		return nil
	}
	// A retry carries the answer. A missing answer or a state that is not
	// ours, has expired or was used asks again rather than going ahead.
	if answer := server.ElicitationResponse(req.Params.InputResponses, confirmID); answer != nil && mcpConfirm.spend(p.site, req) {
		if answer.Action == mcp.ElicitationResponseActionAccept && confirmed(answer.Content) {
			return nil
		}
		rec.Status = auditDeclined
		return mcp.NewToolResultError("cancelled by the user; nothing was changed")
	}
	if !canElicit(ctx) {
		if mode != config.ConfirmAlways {
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

// confirmMessage says what the call will do. Every value the model chose is
// sanitised and quoted, so it cannot pass for ffc's own words.
func confirmMessage(site string, req mcp.CallToolRequest, sc toolScope) string {
	args := req.GetArguments()
	dt := ""
	if len(sc.Doctypes) > 0 {
		dt = clip(sc.Doctypes[0], 140)
	}
	name := func(i int) string { return strconv.Quote(clip(sc.Names[i], 140)) }
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
		what = fmt.Sprintf("Merge %s %s into %s. %s will no longer exist.", dt, name(0), strconv.Quote(clip(to, 140)), name(0))
	case "call_method":
		b, _ := json.Marshal(client.RedactArgs(args["args"]))
		what = fmt.Sprintf("Call %s with %s.", strconv.Quote(clip(sc.Method, 200)), clip(string(b), 500))
	}
	if site == "" {
		site = "(from the environment)"
	}
	return fmt.Sprintf("An AI agent asks to change site %s. %s This cannot be undone.", strconv.Quote(clip(site, 100)), what)
}

// cliEquivalent is the ffc command that does what the call asked, for a
// person to run (and confirm) in a terminal. A value with control or
// invisible characters cannot be shown as is, and changing it would name
// another document, so then only the command's name is given.
func cliEquivalent(site string, req mcp.CallToolRequest, sc toolScope) string {
	args := req.GetArguments()
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
		parts = append(parts, "bulk-delete", "--doctype", q(dt), "--names", q(strings.Join(sc.Names, ",")))
	case "rename_doc":
		to, _ := docName(args["new_name"])
		parts = append(parts, "rename-doc", "--doctype", q(dt), "--name", q(sc.Names[0]), "--to", q(to), "--merge")
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
	return "ffc " + strings.ReplaceAll(req.Params.Name, "_", "-")
}
