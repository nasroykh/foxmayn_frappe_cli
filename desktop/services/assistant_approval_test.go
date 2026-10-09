package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// waitApproval waits for the n-th chat:approval event and returns it.
func (g *loopRig) waitApproval(t *testing.T, n int) ChatApproval {
	t.Helper()
	waitFor(t, func() bool { return len(g.h.named(EventChatApproval)) >= n })
	return g.h.named(EventChatApproval)[n-1].(ChatApproval)
}

func (g *loopRig) closed(t *testing.T) []ChatApprovalClosed {
	t.Helper()
	var out []ChatApprovalClosed
	for _, e := range g.h.named(EventChatApprovalClosed) {
		out = append(out, e.(ChatApprovalClosed))
	}
	return out
}

// toolResults returns the results of the first tool-result message.
func (g *loopRig) toolResults(t *testing.T, convID string) []llm.ToolResult {
	t.Helper()
	var out []llm.ToolResult
	for _, m := range g.messages(t, convID) {
		for _, p := range m.Parts {
			if tr, ok := p.(llm.ToolResult); ok {
				out = append(out, tr)
			}
		}
	}
	return out
}

func (g *loopRig) writes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, rq := range g.fake.Requests() {
		if rq.Method != http.MethodGet && !strings.Contains(rq.Path, "login") {
			out = append(out, rq.Method+" "+rq.Path)
		}
	}
	return out
}

func TestApprovalCreateDocNeedsACard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		approve bool
	}{{"approve", true}, {"decline", false}} {
		t.Run(tc.name, func(t *testing.T) {
			g := newLoopRig(t,
				toolTurn(call("c1", "create_doc", `{"doctype":"ToDo","data":{"description":"new"}}`)),
				textTurn("done"),
			)
			cid := g.conv(t, "ask")
			runID, err := g.r.start(cid, "add a todo")
			if err != nil {
				t.Fatal(err)
			}
			card := g.waitApproval(t, 1)
			if card.Kind != ApprovalApp || card.Tool != "create_doc" || card.RunID != runID || card.Site != "prod" || len(card.Diff) != 0 {
				t.Errorf("card = %+v", card)
			}
			var args map[string]any
			if err := json.Unmarshal(card.Args, &args); err != nil || args["doctype"] != "ToDo" {
				t.Errorf("card args = %s, %v", card.Args, err)
			}
			if n := g.fake.Count("ToDo"); n != 3 {
				t.Errorf("created before approval: %d ToDos", n)
			}
			if p := g.r.pendingApprovals(cid); len(p) != 1 || p[0].ApprovalID != card.ApprovalID {
				t.Errorf("pending = %+v", p)
			}
			if p := g.r.pendingApprovals("other"); len(p) != 0 {
				t.Errorf("pending of another conversation = %+v", p)
			}
			if err := g.r.answer(cid, card.ApprovalID, tc.approve); err != nil {
				t.Fatal(err)
			}
			if p := g.r.pendingApprovals(cid); len(p) != 0 {
				t.Errorf("pending after answer = %+v", p)
			}
			if err := g.r.answer(cid, card.ApprovalID, tc.approve); err == nil {
				t.Error("second answer accepted")
			} else if se, ok := err.(*Error); !ok || se.Code != CodeNotFound {
				t.Errorf("second answer = %v", err)
			}
			if d := g.waitDone(t, 1); d.Status != RunDone {
				t.Fatalf("done = %+v", d)
			}
			res := g.toolResults(t, cid)
			calls, _ := g.st.ListToolCalls(runID)
			if len(res) != 1 || len(calls) != 1 {
				t.Fatalf("results %+v, calls %+v", res, calls)
			}
			if tc.approve {
				if res[0].IsError || g.fake.Count("ToDo") != 4 || calls[0].Approval != ApprovalApproved || calls[0].Status != ToolOK {
					t.Errorf("approved: %+v, %d ToDos, call %+v", res[0], g.fake.Count("ToDo"), calls[0])
				}
			} else {
				if !res[0].IsError || !strings.Contains(res[0].Text, "declined") || g.fake.Count("ToDo") != 3 || calls[0].Approval != ApprovalDeclined {
					t.Errorf("declined: %+v, %d ToDos, call %+v", res[0], g.fake.Count("ToDo"), calls[0])
				}
				if w := g.writes(t); len(w) != 0 {
					t.Errorf("site got %v", w)
				}
			}
			want := ApprovalDeclined
			if tc.approve {
				want = ApprovalApproved
			}
			if c := g.closed(t); len(c) != 1 || c[0].Outcome != want || c[0].ApprovalID != card.ApprovalID {
				t.Errorf("closed = %+v", c)
			}
			if err := g.r.answer(cid, "nope", true); err == nil {
				t.Error("unknown id accepted")
			}
		})
	}
}

func TestApprovalFFCConfirmationIsTheOnlyCard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		approve bool
	}{{"approve", true}, {"decline", false}} {
		t.Run(tc.name, func(t *testing.T) {
			g := newLoopRig(t,
				toolTurn(call("c1", "delete_doc", `{"doctype":"ToDo","name":"TD-1"}`)),
				textTurn("done"),
			)
			cid := g.conv(t, "ask")
			runID, err := g.r.start(cid, "delete TD-1")
			if err != nil {
				t.Fatal(err)
			}
			card := g.waitApproval(t, 1)
			if card.Kind != ApprovalFFC || card.Tool != "delete_doc" || !strings.Contains(card.Message, "TD-1") ||
				len(card.Names) != 1 || card.Names[0] != "TD-1" {
				t.Errorf("card = %+v", card)
			}
			if err := g.r.answer(cid, card.ApprovalID, tc.approve); err != nil {
				t.Fatal(err)
			}
			if d := g.waitDone(t, 1); d.Status != RunDone {
				t.Fatalf("done = %+v", d)
			}
			if n := len(g.h.named(EventChatApproval)); n != 1 {
				t.Errorf("%d cards, want 1", n)
			}
			_, kept := g.fake.Doc("ToDo", "TD-1")
			calls, _ := g.st.ListToolCalls(runID)
			statuses := auditStatuses(t, g.path)
			if tc.approve {
				if kept || statuses != "confirm_pending,ok" || calls[0].Approval != ApprovalFFCApproved {
					t.Errorf("kept %v, audit %s, call %+v", kept, statuses, calls[0])
				}
			} else {
				res := g.toolResults(t, cid)
				if !kept || statuses != "confirm_pending,declined" || calls[0].Approval != ApprovalFFCDeclined || !res[0].IsError {
					t.Errorf("kept %v, audit %s, call %+v, result %+v", kept, statuses, calls[0], res)
				}
			}
		})
	}
}

func TestApprovalCancelDeclinesOpenCards(t *testing.T) {
	for name, c := range map[string]struct {
		call     string
		approval string
	}{
		"ffc": {`{"doctype":"ToDo","name":"TD-1"}`, ""},
		"app": {`{"doctype":"ToDo","data":{"description":"x"}}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			tool := "delete_doc"
			if name == "app" {
				tool = "create_doc"
			}
			g := newLoopRig(t, toolTurn(call("c1", tool, c.call)), textTurn("never"))
			cid := g.conv(t, "ask")
			runID, err := g.r.start(cid, "go")
			if err != nil {
				t.Fatal(err)
			}
			card := g.waitApproval(t, 1)
			t0 := time.Now()
			g.r.cancelRun(runID)
			d := g.waitDone(t, 1)
			if el := time.Since(t0); el > time.Second || d.Status != RunCancelled {
				t.Errorf("cancel took %v, done = %+v", el, d)
			}
			if _, ok := g.fake.Doc("ToDo", "TD-1"); !ok || g.fake.Count("ToDo") > 3 {
				t.Error("the site changed")
			}
			if p := g.r.pendingApprovals(cid); len(p) != 0 {
				t.Errorf("pending = %+v", p)
			}
			if cl := g.closed(t); len(cl) != 1 || cl[0].Outcome != ApprovalCancelled || cl[0].ApprovalID != card.ApprovalID {
				t.Errorf("closed = %+v", cl)
			}
			calls, _ := g.st.ListToolCalls(runID)
			if len(calls) != 1 || calls[0].Approval != ApprovalCancelled {
				t.Errorf("calls = %+v", calls)
			}
			if err := g.r.answer(cid, card.ApprovalID, true); err == nil {
				t.Error("answer after cancel accepted")
			}
			if n := len(g.prov.Requests()); n != 1 {
				t.Errorf("%d model turns", n)
			}
		})
	}
}

func TestApprovalUpdateDocShowsDiffAndPinsModified(t *testing.T) {
	g := newLoopRig(t,
		toolTurn(call("c1", "update_doc", `{"doctype":"ToDo","name":"TD-1","data":{"description":"changed","status":"Open"},"if_unmodified":"1999-01-01 00:00:00"}`)),
		textTurn("done"),
	)
	doc, _ := g.fake.Doc("ToDo", "TD-1")
	modified := fmt.Sprint(doc["modified"])
	cid := g.conv(t, "ask")
	runID, err := g.r.start(cid, "update")
	if err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	if len(card.Diff) != 1 || card.Diff[0].Field != "description" || card.Diff[0].Old != "alpha" || card.Diff[0].New != "changed" {
		t.Errorf("diff = %+v", card.Diff)
	}
	var args map[string]any
	if err := json.Unmarshal(card.Args, &args); err != nil || args["if_unmodified"] != modified {
		t.Errorf("args = %s (modified %s), %v", card.Args, modified, err)
	}
	if err := g.r.answer(cid, card.ApprovalID, true); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	if got, _ := g.fake.Doc("ToDo", "TD-1"); got["description"] != "changed" {
		t.Errorf("doc = %v", got)
	}
	// The read and the write are both audited under the run.
	n := 0
	for _, l := range engineAudit(t, g.path) {
		if l["run_id"] != runID {
			t.Errorf("audit line = %v", l)
		}
		n++
	}
	if n != 2 {
		t.Errorf("%d audit lines, want get_doc and update_doc", n)
	}
}

func TestApprovalUpdateDocConflict(t *testing.T) {
	g := newLoopRig(t,
		toolTurn(call("c1", "update_doc", `{"doctype":"ToDo","name":"TD-1","data":{"description":"mine"}}`)),
		textTurn("done"),
	)
	cid := g.conv(t, "ask")
	if _, err := g.r.start(cid, "update"); err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	// Someone else saves the document while the card is open.
	other, err := g.r.engine.Open(t.Context(), "prod", EngineAsk, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	res, err := other.Call(t.Context(), "someone", "update_doc", map[string]any{"doctype": "ToDo", "name": "TD-1", "data": map[string]any{"description": "theirs"}})
	if err != nil || res.IsError {
		t.Fatalf("other update: %v %v", err, res)
	}
	if err := g.r.answer(cid, card.ApprovalID, true); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	tr := g.toolResults(t, cid)
	if len(tr) != 1 || !tr[0].IsError || !strings.Contains(tr[0].Text, "get_doc") || !strings.Contains(tr[0].Text, "changed") {
		t.Errorf("result = %+v", tr)
	}
	if got, _ := g.fake.Doc("ToDo", "TD-1"); got["description"] != "theirs" {
		t.Errorf("doc overwritten: %v", got)
	}
	if n := len(g.h.named(EventChatApproval)); n != 1 {
		t.Errorf("%d cards: the conflict must not be retried", n)
	}
}

func TestApprovalReadModeOffersNoWriteTool(t *testing.T) {
	g := newLoopRig(t, textTurn("hi"))
	cid := g.conv(t, "read")
	if _, err := g.r.start(cid, "hello"); err != nil {
		t.Fatal(err)
	}
	g.waitDone(t, 1)
	tools := g.prov.Requests()[0].Tools
	if len(tools) == 0 {
		t.Fatal("no tools offered")
	}
	for _, tl := range tools {
		switch tl.Name {
		case "create_doc", "update_doc", "delete_doc", "call_method":
			t.Errorf("read mode offers %s", tl.Name)
		}
	}
	if n := len(g.h.named(EventChatApproval)); n != 0 {
		t.Errorf("%d cards", n)
	}
}

func TestApprovalKeepsModelOrder(t *testing.T) {
	g := newLoopRig(t,
		toolTurn(
			call("r1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`),
			call("w", "create_doc", `{"doctype":"ToDo","data":{"description":"new"}}`),
			call("r2", "get_doc", `{"doctype":"ToDo","name":"TD-2"}`),
		),
		textTurn("done"),
	)
	cid := g.conv(t, "ask")
	if _, err := g.r.start(cid, "mix"); err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	// The read before the card has run; the one after has not.
	if rq := g.fake.RequestsTo(http.MethodGet, "/api/resource/ToDo/TD-1"); len(rq) != 1 {
		t.Errorf("TD-1 reads before the card: %d", len(rq))
	}
	if rq := g.fake.RequestsTo(http.MethodGet, "/api/resource/ToDo/TD-2"); len(rq) != 0 {
		t.Errorf("TD-2 read before approval: %d", len(rq))
	}
	if err := g.r.answer(cid, card.ApprovalID, true); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	res := g.toolResults(t, cid)
	if len(res) != 3 || res[0].ID != "r1" || res[1].ID != "w" || res[2].ID != "r2" {
		t.Fatalf("results = %+v", res)
	}
	for _, r := range res {
		if r.IsError {
			t.Errorf("result %+v", r)
		}
	}
	if g.fake.Count("ToDo") != 4 {
		t.Errorf("%d ToDos", g.fake.Count("ToDo"))
	}
}

func TestApprovalWrongConversationCannotAnswer(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "create_doc", `{"doctype":"ToDo","data":{"description":"new"}}`)), textTurn("done"))
	cid := g.conv(t, "ask")
	if _, err := g.r.start(cid, "add"); err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	err := g.r.answer("another-conversation", card.ApprovalID, true)
	if se, ok := err.(*Error); !ok || se.Code != CodeNotFound {
		t.Errorf("answer from another conversation = %v", err)
	}
	if p := g.r.pendingApprovals(cid); len(p) != 1 {
		t.Fatalf("the card was consumed: %+v", p)
	}
	if err := g.r.answer(cid, card.ApprovalID, false); err != nil {
		t.Fatal(err)
	}
	g.waitDone(t, 1)
}

// engine.build forces Confirm "always", so a site that says never still gets
// ffc's own card.
func TestApprovalSiteConfirmNeverStillAsks(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "delete_doc", `{"doctype":"ToDo","name":"TD-1"}`)), textTurn("done"))
	raw, err := os.ReadFile(g.path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(cfg, "\n  prod:\n") {
		t.Fatalf("unexpected config:\n%s", cfg)
	}
	cfg = strings.Replace(cfg, "\n  prod:\n", "\n  prod:\n    mcp:\n      confirm: never\n", 1)
	if err := os.WriteFile(g.path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cid := g.conv(t, "ask")
	if _, err := g.r.start(cid, "delete TD-1"); err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	if card.Kind != ApprovalFFC {
		t.Fatalf("card = %+v", card)
	}
	if _, ok := g.fake.Doc("ToDo", "TD-1"); !ok {
		t.Fatal("deleted before the answer")
	}
	if err := g.r.answer(cid, card.ApprovalID, false); err != nil {
		t.Fatal(err)
	}
	g.waitDone(t, 1)
	if _, ok := g.fake.Doc("ToDo", "TD-1"); !ok {
		t.Error("declined delete went through")
	}
}

func bareRun(t *testing.T) (*activeRun, *fakeHost) {
	t.Helper()
	h := &fakeHost{}
	r := newRunner(nil, nil, nil, h.Emit)
	t.Cleanup(r.shutdown)
	return &activeRun{r: r, runID: "run1", conv: store.Conversation{ID: "conv1", Site: "prod"}}, h
}

func elicitReq(msg string) mcp.ElicitationRequest {
	var req mcp.ElicitationRequest
	req.Params.Message = msg
	return req
}

func TestApprovalElicitationOutsideAWriteIsDeclined(t *testing.T) {
	a, h := bareRun(t)
	// No write is running (reads, or prepareUpdate's get_doc): decline at once.
	res, err := a.elicit(t.Context(), elicitReq("Delete everything?"))
	if err != nil || res.Action != mcp.ElicitationResponseActionDecline {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if n := len(h.named(EventChatApproval)); n != 0 {
		t.Errorf("%d cards", n)
	}
}

// A call the app already approved (WillAsk false) that ffc asks about anyway
// needs the second card too.
func TestApprovalFFCAsksAfterAppCard(t *testing.T) {
	a, h := bareRun(t)
	e := &callEntry{call: llm.ToolCall{Name: "create_doc"}, args: map[string]any{}}
	a.setApproval(e, ApprovalApproved)
	a.askMu.Lock()
	a.asking = e
	a.askMu.Unlock()
	done := make(chan *mcp.ElicitationResult, 1)
	go func() {
		res, _ := a.elicit(t.Context(), elicitReq("Really?"))
		done <- res
	}()
	waitFor(t, func() bool { return len(h.named(EventChatApproval)) == 1 })
	card := h.named(EventChatApproval)[0].(ChatApproval)
	if card.Kind != ApprovalFFC || card.Message != "Really?" {
		t.Fatalf("card = %+v", card)
	}
	if err := a.r.answer("conv1", card.ApprovalID, true); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Action != mcp.ElicitationResponseActionAccept {
		t.Errorf("res = %+v", res)
	}
	if got := a.approvalOf(e); got != ApprovalFFCApproved {
		t.Errorf("approval = %q", got)
	}
}

func TestApprovalApproveRacingCancelDoesNotRun(t *testing.T) {
	for i := 0; i < 200; i++ {
		h := &fakeHost{}
		b := newApprovalBroker(h.Emit)
		p := b.open("conv1", ChatApproval{RunID: "r"})
		ctx, cancel := context.WithCancel(t.Context())
		if err := b.answer("conv1", p.card.ApprovalID, true); err != nil {
			t.Fatal(err)
		}
		cancel()
		if got := b.wait(ctx, p); got != ApprovalCancelled {
			t.Fatalf("round %d: outcome %q after a cancel", i, got)
		}
	}
}

func TestApprovalCancelDuringWriteSetsCancelled(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "update_doc", `{"doctype":"ToDo","name":"TD-1","data":{"description":"x"}}`)), textTurn("never"))
	entered := make(chan struct{})
	var once sync.Once
	g.fake.Handle("GET /api/resource/ToDo/TD-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	cid := g.conv(t, "ask")
	runID, err := g.r.start(cid, "go")
	if err != nil {
		t.Fatal(err)
	}
	// Cancel while prepareUpdate's get_doc is in flight.
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("get_doc never called")
	}
	g.r.cancelRun(runID)
	if d := g.waitDone(t, 1); d.Status != RunCancelled {
		t.Fatalf("done = %+v", d)
	}
	calls, _ := g.st.ListToolCalls(runID)
	if len(calls) != 1 || calls[0].Approval != ApprovalCancelled {
		t.Errorf("calls = %+v", calls)
	}
}

func TestApprovalNoChangesAndDiffNormalisation(t *testing.T) {
	doc := map[string]any{"enabled": float64(1), "qty": float64(5), "name": "X", "title": "a", "n": nil}
	data := map[string]any{"enabled": true, "qty": "5", "title": "a", "name": "Y"}
	if d := diffFields(doc, data); len(d) != 0 {
		t.Errorf("diff = %+v", d)
	}
	data = map[string]any{"enabled": false, "qty": "6", "title": "b"}
	if d := diffFields(doc, data); len(d) != 3 {
		t.Errorf("diff = %+v", d)
	}
	if sameValue("abc", float64(0)) || sameValue("", float64(0)) {
		t.Error("non-numeric strings equal a number")
	}

	g := newLoopRig(t, toolTurn(call("c1", "update_doc", `{"doctype":"ToDo","name":"TD-1","data":{"description":"alpha"}}`)), textTurn("done"))
	cid := g.conv(t, "ask")
	if _, err := g.r.start(cid, "update"); err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	if !card.NoChanges || len(card.Diff) != 0 {
		t.Errorf("card = %+v", card)
	}
	if err := g.r.answer(cid, card.ApprovalID, false); err != nil {
		t.Fatal(err)
	}
	g.waitDone(t, 1)
}
