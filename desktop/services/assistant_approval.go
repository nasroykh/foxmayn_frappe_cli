package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	approvalDeclinedResult  = "The user declined this change. Nothing was changed."
	approvalCancelledResult = "Stopped by the user before this change ran."
	approvalUnaskedWarning  = "Warning: this change was applied without the confirmation the app expects."
	approvalConflictHint    = "\nThe document changed after the user was shown this change, so nothing was saved. Read it again with get_doc and tell the user what changed before trying anything else; do not retry on your own."
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
		return &Error{Code: CodeNotFound, Message: "This request was already answered or has ended."}
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
		var diff []DiffField
		if e.call.Name == "update_doc" {
			var msg string
			if diff, msg = a.prepareUpdate(ctx, e); msg != "" {
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
			card.Diff = diff
			card.NoChanges = e.call.Name == "update_doc" && len(diff) == 0
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
	if e.isErr && isConflict(e.result) {
		e.result += approvalConflictHint
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
	data := map[string]any{}
	switch d := e.args["data"].(type) {
	case map[string]any:
		data = d
	case string:
		if err := json.Unmarshal([]byte(d), &data); err != nil || data == nil {
			return nil, "The data argument of update_doc was not a JSON object. Call it again with one."
		}
	default:
		return nil, "The data argument of update_doc was not a JSON object. Call it again with one."
	}
	read := map[string]any{}
	for _, k := range []string{"doctype", "name"} {
		if v, ok := e.args[k]; ok {
			read[k] = v
		}
	}
	res, err := a.session.Call(ctx, a.runID, "get_doc", read)
	if err != nil {
		return nil, "Could not read the document before the change: " + toServiceError(err).Error()
	}
	text := callResultText(res)
	if res.IsError {
		return nil, "Could not read the document before the change: " + text
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil || doc == nil {
		return nil, "Could not read the document before the change."
	}
	if m, ok := doc["modified"]; ok && m != nil && fmt.Sprint(m) != "" {
		e.args["if_unmodified"] = fmt.Sprint(m)
	} else {
		delete(e.args, "if_unmodified")
	}
	return diffFields(doc, data), ""
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
		out = append(out, DiffField{Field: k, Old: ov, New: nv})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// sameValue is DeepEqual after the differences Frappe hides: a flag as true or
// 1, a number as 5 or "5".
func sameValue(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	// Two strings are never compared as numbers ("0123" is not "123"): at
	// least one side must be a bool or a JSON number.
	if !isFlagOrNumber(a) && !isFlagOrNumber(b) {
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
