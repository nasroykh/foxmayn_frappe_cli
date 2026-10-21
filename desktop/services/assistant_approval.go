package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

const (
	approvalDeclinedResult  = "The user declined this change. Nothing was changed."
	approvalCancelledResult = "Stopped by the user before this change ran."
	approvalUnaskedWarning  = "Warning: this change was applied without the confirmation the app expects."
	approvalConflictHint    = "\nThe document changed after the user was shown this change, so nothing was saved. Read it again with get_doc and tell the user what changed before trying anything else; do not retry on your own."
	// A bulk_update reports its failed items in a successful result.
	approvalBulkConflictHint = "\nSome documents changed after the user was shown this change, so their items were not saved (the results say which; the others were saved). Read those again with get_doc and tell the user what changed before trying anything else; do not retry on your own."
)

// approvalBroker holds the cards waiting for the user. A card is answered by
// answer or ends with the run's context, which counts as a decline.
type approvalBroker struct {
	emit func(string, any)

	mu      sync.Mutex
	seq     int
	pending map[string]*pendingApproval
}

type pendingApproval struct {
	seq    int
	convID string
	card   ChatApproval
	ch     chan bool // buffered: answer never blocks
}

func newApprovalBroker(emit func(string, any)) *approvalBroker {
	return &approvalBroker{emit: emit, pending: map[string]*pendingApproval{}}
}

// open registers a card and tells the UI.
func (b *approvalBroker) open(convID string, card ChatApproval) *pendingApproval {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	card.ConvID = convID
	card.ApprovalID = hex.EncodeToString(raw[:])
	p := &pendingApproval{convID: convID, card: card, ch: make(chan bool, 1)}
	b.mu.Lock()
	b.seq++
	p.seq = b.seq
	b.pending[card.ApprovalID] = p
	b.mu.Unlock()
	b.emit(EventChatApproval, card)
	return p
}

// wait blocks until the card is answered or ctx ends, and reports the outcome:
// ApprovalApproved, ApprovalDeclined or ApprovalCancelled.
func (b *approvalBroker) wait(ctx context.Context, p *pendingApproval) string {
	outcome := ApprovalCancelled
	select {
	case ok := <-p.ch:
		outcome = ApprovalDeclined
		// An approve that raced with a cancel must not run the call.
		if ok && ctx.Err() != nil {
			outcome = ApprovalCancelled
		} else if ok {
			outcome = ApprovalApproved
		}
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, p.card.ApprovalID)
		b.mu.Unlock()
	}
	b.emit(EventChatApprovalClosed, ChatApprovalClosed{ConvID: p.convID, RunID: p.card.RunID, ApprovalID: p.card.ApprovalID, Outcome: outcome})
	return outcome
}

// answer settles an open card of conversation convID. An unknown id, one
// answered already and one of another conversation are errors.
func (b *approvalBroker) answer(convID, id string, approve bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[id]
	if !ok || p.convID != convID {
		return keyed(CodeNotFound, "chat.approvalGone", nil)
	}
	delete(b.pending, id)
	p.ch <- approve
	return nil
}

// list returns the open cards of a conversation, oldest first.
func (b *approvalBroker) list(convID string) []ChatApproval {
	b.mu.Lock()
	var ps []*pendingApproval
	for _, p := range b.pending {
		if p.convID == convID {
			ps = append(ps, p)
		}
	}
	b.mu.Unlock()
	sort.Slice(ps, func(i, j int) bool { return ps[i].seq < ps[j].seq })
	out := make([]ChatApproval, len(ps))
	for i, p := range ps {
		out[i] = p.card
	}
	return out
}

// answer settles the approval card approvalID of conversation convID.
func (r *runner) answer(convID, approvalID string, approve bool) error {
	return r.approvals.answer(convID, approvalID, approve)
}

// pendingApprovals lists the open cards of a conversation, so the UI can show
// them again after a reload.
func (r *runner) pendingApprovals(convID string) []ChatApproval {
	return r.approvals.list(convID)
}

// card builds the payload for a call.
func (a *activeRun) card(e *callEntry, kind string) ChatApproval {
	args, _ := json.Marshal(e.args)
	return ChatApproval{
		RunID: a.runID, Kind: kind, Tool: e.call.Name, Site: a.conv.Site,
		Doctypes: e.cls.Doctypes, Names: e.cls.Names, Args: args,
	}
}

func (a *activeRun) setApproval(e *callEntry, v string) {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	e.approval = v
}

func (a *activeRun) approvalOf(e *callEntry) string {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	return e.approval
}

// elicit is the run's Elicitor: ffc's confirmation becomes a card of kind
// "ffc". Only an approve answers yes; ffc asks while a WillAsk call runs, which
// is alone, so the question belongs to a.asking.
func (a *activeRun) elicit(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	decline := &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}}
	a.askMu.Lock()
	e := a.asking
	a.askMu.Unlock()
	if e == nil {
		return decline, nil
	}
	card := a.card(e, ApprovalFFC)
	card.Message = req.Params.Message
	switch a.r.approvals.wait(ctx, a.r.approvals.open(a.conv.ID, card)) {
	case ApprovalApproved:
		a.setApproval(e, ApprovalFFCApproved)
		return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
			Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"confirm": true},
		}}, nil
	case ApprovalDeclined:
		a.setApproval(e, ApprovalFFCDeclined)
	default:
		a.setApproval(e, ApprovalCancelled)
	}
	return decline, nil
}

// runWrite runs a call that changes something: ffc asks by itself when it is
// certain to, else the app shows its own card first.
func (a *activeRun) runWrite(ctx context.Context, e *callEntry) {
	fail := func(msg string) {
		e.result, e.isErr, e.status = msg, true, ToolError
	}
	if ctx.Err() != nil {
		e.result, e.isErr, e.status = approvalCancelledResult, true, ToolStopped
		a.setApproval(e, ApprovalCancelled)
		return
	}
	if !e.cls.WillAsk {
		var (
			diff     []DiffField
			docs     []DocChanges
			msg      string
			prepared = true
		)
		switch e.call.Name {
		case "update_doc":
			diff, msg = a.prepareUpdate(ctx, e)
		case "bulk_update":
			docs, msg = a.prepareBulkUpdate(ctx, e)
		case "copy_doc", "amend_doc":
			diff, msg = a.prepareCopy(ctx, e)
		default:
			prepared = false
		}
		if prepared {
			if msg != "" {
				if ctx.Err() != nil {
					e.result, e.isErr, e.status = approvalCancelledResult, true, ToolStopped
					a.setApproval(e, ApprovalCancelled)
					return
				}
				fail(msg)
				return
			}
			cls, err := a.session.Classify(e.call.Name, e.args)
			if err != nil {
				fail("This call was refused: " + err.Error())
				return
			}
			if cls.Denied != "" {
				fail(cls.Denied)
				return
			}
			e.cls = cls
		}
		if !e.cls.WillAsk {
			card := a.card(e, ApprovalApp)
			if e.call.Name == "restore_doc" {
				a.restoreTarget(ctx, e, &card)
			}
			card.Diff, card.Docs = diff, docs
			card.NoChanges = prepared && e.call.Name != "bulk_update" && len(diff) == 0
			switch a.r.approvals.wait(ctx, a.r.approvals.open(a.conv.ID, card)) {
			case ApprovalDeclined:
				a.setApproval(e, ApprovalDeclined)
				fail(approvalDeclinedResult)
				return
			case ApprovalCancelled:
				a.setApproval(e, ApprovalCancelled)
				e.result, e.isErr, e.status = approvalCancelledResult, true, ToolStopped
				return
			}
			a.setApproval(e, ApprovalApproved)
		}
	}
	a.askMu.Lock()
	a.asking = e
	a.askMu.Unlock()
	a.call(ctx, e)
	a.askMu.Lock()
	a.asking = nil
	a.askMu.Unlock()
	if ctx.Err() != nil && a.approvalOf(e) == "" {
		a.setApproval(e, ApprovalCancelled)
	}
	// Defense in depth: a call that had to be asked about must not succeed
	// unless ffc asked and the user said yes.
	if a.r.afterCall != nil {
		a.r.afterCall(e)
	}
	if e.cls.WillAsk && !e.isErr && a.approvalOf(e) != ApprovalFFCApproved {
		// The change is done; say so, keep the real result, and end the run.
		log.Printf("assistant: %s on %s ran without ffc asking for confirmation (run %s)", e.call.Name, a.conv.Site, a.runID)
		e.result = approvalUnaskedWarning + "\n" + e.result
		e.isErr, e.status = true, ToolError
		a.errMu.Lock()
		a.violation = "A change to " + a.conv.Site + " (" + e.call.Name + ") was applied without the confirmation the app expects. The assistant was stopped. Check the document before going on."
		a.errMu.Unlock()
	}
	switch {
	case e.isErr && isConflict(e.result):
		e.result += approvalConflictHint
	case e.call.Name == "bulk_update" && isConflict(e.result):
		e.result += approvalBulkConflictHint
	}
}

// isConflict reports whether a result is ffc's timestamp conflict.
func isConflict(text string) bool {
	return strings.Contains(text, "TimestampMismatchError") || strings.Contains(text, "was changed on the server since")
}

// prepareUpdate reads the document an update_doc targets, pins the call to the
// version read (if_unmodified) and returns the changed fields. A non-empty
// message is the call's error result.
func (a *activeRun) prepareUpdate(ctx context.Context, e *callEntry) ([]DiffField, string) {
	data, ok := objectOf(e.args["data"])
	if !ok {
		return nil, "The data argument of update_doc was not a JSON object. Call it again with one."
	}
	doc, msg := a.readTarget(ctx, e)
	if msg != "" {
		return nil, msg
	}
	pin(e.args, "if_unmodified", doc)
	return diffFields(doc, data), ""
}

// prepareCopy reads the source of a copy_doc or amend_doc, pins the call to
// the version read (if_unmodified: ffc creates nothing from a source saved
// since) and returns every field data sets, next to the source's value.
func (a *activeRun) prepareCopy(ctx context.Context, e *callEntry) ([]DiffField, string) {
	data := map[string]any{}
	if v := e.args["data"]; v != nil {
		var ok bool
		if data, ok = objectOf(v); !ok {
			return nil, "The data argument of " + e.call.Name + " was not a JSON object. Call it again with one."
		}
	}
	doc, msg := a.readTarget(ctx, e)
	if msg != "" {
		return nil, msg
	}
	pin(e.args, "if_unmodified", doc)
	if e.call.Name == "amend_doc" {
		// ffc links the amendment to its source whatever data says.
		data = maps.Clone(data)
		data["amended_from"] = e.args["name"]
	}
	return overrideFields(doc, data), ""
}

// bulkPrereadWorkers is how many documents of a bulk_update are read at once,
// as many as ffc's own bulk tools have in flight.
const bulkPrereadWorkers = 4

// prepareBulkUpdate reads every document a bulk_update names, pins each item
// to the version read (its "modified": Frappe then refuses that item if the
// document was saved since) and returns each document's changed fields, in
// the order of the call. A document named twice is refused, since its second
// item would always conflict with the first. Nothing is written when any
// read fails.
func (a *activeRun) prepareBulkUpdate(ctx context.Context, e *callEntry) ([]DocChanges, string) {
	const invalid = "The data argument of bulk_update was not a JSON array of objects that each have a name. Call it again with one."
	raw := e.args["data"]
	if s, ok := raw.(string); ok {
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return nil, invalid
		}
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, invalid
	}
	if len(list) > cmd.MaxMCPBulkItems {
		return nil, fmt.Sprintf("bulk_update may change at most %d documents in one call. Split it into smaller calls.", cmd.MaxMCPBulkItems)
	}
	items := make([]map[string]any, len(list))
	names := make([]string, len(list))
	seen := map[string]bool{}
	for i, v := range list {
		item, ok := v.(map[string]any)
		if !ok {
			return nil, invalid
		}
		name, ok := nameString(item["name"])
		if !ok {
			return nil, invalid
		}
		// Frappe compares names without regard to case or trailing spaces
		// (the key of ffc's refuseDuplicateNames).
		key := strings.ToLower(strings.TrimSpace(name))
		if seen[key] {
			return nil, "bulk_update names " + strconv.Quote(name) + " more than once. Put all changes to one document in one item and call it again."
		}
		seen[key] = true
		items[i], names[i] = item, name
	}

	reads := make([]docRead, len(items))
	next := make(chan int)
	var (
		wg     sync.WaitGroup
		failed atomic.Bool // one failed read fails the call: read no more
	)
	for range min(bulkPrereadWorkers, len(items)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				reads[i] = a.readDoc(ctx, map[string]any{"doctype": e.args["doctype"], "name": items[i]["name"]})
				if reads[i].failed != "" {
					failed.Store(true)
				}
			}
		}()
	}
	for i := range items {
		if ctx.Err() != nil || failed.Load() {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, approvalCancelledResult
	}
	// The first failure in the call's order is the result. Items after a
	// failure may not have been read: their zero docRead is never used.
	docs := make([]map[string]any, len(items))
	for i, r := range reads {
		if r.doc == nil && r.failed == "" {
			continue
		}
		var msg string
		if docs[i], msg = a.readResult(e, r); msg != "" {
			return nil, msg
		}
	}

	out := make([]DocChanges, len(items))
	for i, item := range items {
		pin(item, "modified", docs[i])
		diff := diffFields(docs[i], item)
		out[i] = DocChanges{Name: revealString(names[i]), Diff: diff, NoChanges: len(diff) == 0}
	}
	e.args["data"] = list
	return out, ""
}

// readTarget reads the document a call names by its doctype and name.
func (a *activeRun) readTarget(ctx context.Context, e *callEntry) (map[string]any, string) {
	read := map[string]any{}
	for _, k := range []string{"doctype", "name"} {
		if v, ok := e.args[k]; ok {
			read[k] = v
		}
	}
	return a.readResult(e, a.readDoc(ctx, read))
}

// docRead is the outcome of one get_doc read for a card.
type docRead struct {
	doc      map[string]any
	failed   string // the failure, when the read failed
	fromSite bool   // failed is the site's (or the connection's) text
}

// readDoc reads one document for a card through get_doc, so the site's rules
// and the audit log apply. It touches no call state, so the reads of a
// bulk_update can run at once.
func (a *activeRun) readDoc(ctx context.Context, read map[string]any) docRead {
	res, err := a.session.Call(ctx, a.runID, "get_doc", read)
	if err != nil {
		return docRead{failed: toServiceError(err).Error(), fromSite: true}
	}
	text := callResultText(res)
	if res.IsError {
		return docRead{failed: text, fromSite: true}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil || doc == nil {
		return docRead{failed: "Could not read the document before the change."}
	}
	return docRead{doc: doc}
}

// readResult returns the document read, or the call's error result. Failure
// text from the site is marked as the site's answer: modelResult cuts it and
// wraps it as data once, after the cut, so the closing tag always survives.
func (a *activeRun) readResult(e *callEntry, r docRead) (map[string]any, string) {
	switch {
	case r.fromSite:
		return nil, a.prereadFailed(e, r.failed)
	case r.failed != "":
		return nil, r.failed
	}
	return r.doc, ""
}

// pin sets args[key] to the "modified" of the document read, or removes it
// when the document has none, so the call runs against that version only.
func pin(args map[string]any, key string, doc map[string]any) {
	if m, ok := doc["modified"]; ok && m != nil && fmt.Sprint(m) != "" {
		args[key] = fmt.Sprint(m)
	} else {
		delete(args, key)
	}
}

// objectOf reads a JSON object argument given as an object or as JSON text.
func objectOf(v any) (map[string]any, bool) {
	switch d := v.(type) {
	case map[string]any:
		return d, true
	case string:
		var m map[string]any
		if err := json.Unmarshal([]byte(d), &m); err != nil || m == nil {
			return nil, false
		}
		return m, true
	}
	return nil, false
}

// nameString reads a document name the way ffc's bulk tools take it: a
// non-blank string, or a number (integer-named DocTypes).
func nameString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, strings.TrimSpace(t) != ""
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case json.Number:
		return t.String(), true
	}
	return "", false
}

// restoreTarget adds to a restore_doc card the DocType and name the Deleted
// Document will re-create, when the call names only the Deleted Document:
// otherwise the card shows "Deleted Document" and not what comes back. The
// read is best effort (ffc's checkRestore still applies the write rules to
// that DocType); a failure leaves the card as it was.
func (a *activeRun) restoreTarget(ctx context.Context, e *callEntry, card *ChatApproval) {
	deleted, _ := e.args["deleted_document"].(string)
	if dt, _ := e.args["doctype"].(string); strings.TrimSpace(deleted) == "" || strings.TrimSpace(dt) != "" {
		return
	}
	res, err := a.session.Call(ctx, a.runID, "get_doc", map[string]any{
		"doctype": "Deleted Document", "name": strings.TrimSpace(deleted), "fields": []any{"deleted_doctype", "deleted_name"},
	})
	if err != nil || res.IsError {
		return
	}
	var doc map[string]any
	if json.Unmarshal([]byte(callResultText(res)), &doc) != nil {
		return
	}
	if dt, _ := doc["deleted_doctype"].(string); dt != "" {
		card.Doctypes = append(slices.Clone(card.Doctypes), revealString(text.Sanitize(dt)))
		if n, _ := doc["deleted_name"].(string); n != "" {
			card.Names = append(slices.Clone(card.Names), revealString(text.Sanitize(n)))
		}
	}
}

// diffFields lists the fields of data whose value differs from doc, by name.
// A child table is one field whose whole value changes.
func diffFields(doc, data map[string]any) []DiffField {
	var out []DiffField
	for k, nv := range data {
		if k == "modified" || k == "name" {
			continue
		}
		ov := doc[k]
		if sameValue(ov, nv) {
			continue
		}
		// The card shows reveal()ed copies; the call's args stay as sent.
		out = append(out, DiffField{Field: revealString(k), Old: reveal(ov), New: reveal(nv)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// overrideFields lists every field of data, by name, next to the source's
// value: a copy drops the identity and "no copy" fields before data is
// applied, so a value equal to the source's may still be a change (docstatus
// on a submitted source, name), and only the full list shows what is set.
func overrideFields(doc, data map[string]any) []DiffField {
	out := make([]DiffField, 0, len(data))
	for k, nv := range data {
		out = append(out, DiffField{Field: revealString(k), Old: reveal(doc[k]), New: reveal(nv)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// reveal returns v for display with every control, format and invisible
// character of its strings escaped (\u202e), so a model-chosen value cannot
// show differently from what is written. v itself is not changed.
func reveal(v any) any {
	switch t := v.(type) {
	case string:
		return revealString(t)
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[revealString(k)] = reveal(x)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = reveal(x)
		}
		return l
	}
	return v
}

// revealString escapes the runes that can hide or reorder text: controls
// (except newline and tab), format characters (bidi, zero-width), line and
// paragraph separators and every space but the plain one.
func revealString(s string) string {
	hidden := func(r rune) bool {
		switch {
		case r == '\n' || r == '\t' || r == ' ':
			return false
		case r == utf8.RuneError:
			return true
		}
		return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Zs)
	}
	if !strings.ContainsFunc(s, hidden) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if r == utf8.RuneError {
			if _, n := utf8.DecodeRuneInString(s[i:]); n == 1 {
				fmt.Fprintf(&b, "\\x%02x", s[i])
				continue
			}
		}
		switch {
		case !hidden(r):
			b.WriteRune(r)
		case r > 0xffff:
			fmt.Fprintf(&b, "\\U%08x", r)
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}

// sameValue is DeepEqual after the differences Frappe hides: a flag as true or
// 1, a number as 5 or "5". a is the stored value and b the new one. Frappe
// returns Int, Float, Currency and Check as numbers and Data as strings, so
// the loose comparison applies only when the stored value is a bool or a
// number: a Data "0123" changed to 123 is a change (Frappe stores "123").
func sameValue(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	if !isFlagOrNumber(a) {
		return false
	}
	x, okx := asNumber(a)
	y, oky := asNumber(b)
	return okx && oky && math.Abs(x-y) < 1e-9
}

func isFlagOrNumber(v any) bool {
	switch v.(type) {
	case bool, float64, int, int64, json.Number:
		return true
	}
	return false
}

// plainNumber is a string that is only a number: no spaces, no exponent.
var plainNumber = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// asNumber reads a bool, number or numeric string as a number.
func asNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		if !plainNumber.MatchString(t) {
			return 0, false
		}
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func (a *activeRun) violationText() string {
	a.errMu.Lock()
	defer a.errMu.Unlock()
	return a.violation
}

// prereadFailed is the error result of a write whose document could not be
// read first. text may come from the site, so it is recorded as the site's
// answer (see modelResult).
func (a *activeRun) prereadFailed(e *callEntry, text string) string {
	e.site, e.siteText, e.siteTool = true, text, "get_doc"
	return "Could not read the document before the change:\n" + text
}
