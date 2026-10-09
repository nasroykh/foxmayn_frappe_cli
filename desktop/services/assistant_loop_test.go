package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

type loopRig struct {
	r     *runner
	h     *fakeHost
	st    *store.Store
	fake  *frappetest.Site
	path  string
	prov  *llmtest.Provider
	convs map[string]store.Conversation
}

// newLoopRig is a runner over a fake site "prod" with the scripted turns.
func newLoopRig(t *testing.T, turns ...llmtest.Turn) *loopRig {
	t.Helper()
	e, fake, path := engineSite(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &fakeHost{}
	prov := llmtest.New(turns...)
	r := newRunner(st, e, func(store.Conversation) (llm.Provider, string, error) { return prov, "test-model", nil }, h.Emit)
	r.backoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(r.shutdown)
	return &loopRig{r: r, h: h, st: st, fake: fake, path: path, prov: prov}
}

func (g *loopRig) conv(t *testing.T, mode string) string {
	t.Helper()
	c, err := g.st.CreateConversation("t", "prod", mode, "p1", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// waitDone waits for the n-th chat:done event and returns it.
func (g *loopRig) waitDone(t *testing.T, n int) ChatDone {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if d := g.h.named(EventChatDone); len(d) >= n {
			return d[n-1].(ChatDone)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no chat:done #%d; events: %v", n, g.h.named(EventChatError))
	return ChatDone{}
}

func (g *loopRig) messages(t *testing.T, convID string) []llm.Message {
	t.Helper()
	rows, err := g.st.ListMessages(convID)
	if err != nil {
		t.Fatal(err)
	}
	var out []llm.Message
	for _, m := range rows {
		parts, err := llm.UnmarshalParts(m.PartsJSON)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, llm.Message{Role: llm.Role(m.Role), Parts: parts})
	}
	return out
}

func call(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

func toolTurn(calls ...llm.ToolCall) llmtest.Turn {
	var ev []llm.Event
	for _, c := range calls {
		ev = append(ev, c)
	}
	return llmtest.Turn{Events: append(ev, llm.Stop{Reason: llm.StopToolUse})}
}

func textTurn(s string) llmtest.Turn {
	return llmtest.Turn{Events: []llm.Event{llm.TextDelta{Text: s}, llm.Usage{In: 10, Out: 5}, llm.Stop{Reason: llm.StopEndTurn}}}
}

func TestLoopAnswersFromGetDoc(t *testing.T) {
	g := newLoopRig(t,
		llmtest.Turn{Events: []llm.Event{
			llm.TextDelta{Text: "Let me look. "},
			call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`),
			llm.Usage{In: 100, Out: 20, Cached: 7},
			llm.Stop{Reason: llm.StopToolUse},
		}},
		textTurn("TD-1 is alpha."),
	)
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "What is TD-1?")
	if err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone || d.RunID != runID {
		t.Fatalf("done = %+v", d)
	}
	msgs := g.messages(t, cid)
	if len(msgs) != 4 {
		t.Fatalf("%d messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != llm.RoleUser || msgs[1].Role != llm.RoleAssistant || msgs[2].Role != llm.RoleUser || msgs[3].Role != llm.RoleAssistant {
		t.Errorf("roles: %v %v %v %v", msgs[0].Role, msgs[1].Role, msgs[2].Role, msgs[3].Role)
	}
	if tu, ok := msgs[1].Parts[1].(llm.ToolUse); !ok || tu.Name != "get_doc" || tu.ID != "c1" {
		t.Errorf("assistant parts = %+v", msgs[1].Parts)
	}
	tr, ok := msgs[2].Parts[0].(llm.ToolResult)
	if !ok || tr.ID != "c1" || tr.IsError || !strings.Contains(tr.Text, "alpha") {
		t.Errorf("tool result = %+v", msgs[2].Parts[0])
	}
	if txt, _ := msgs[3].Parts[0].(llm.Text); txt.Text != "TD-1 is alpha." {
		t.Errorf("final = %+v", msgs[3].Parts)
	}

	reqs := g.prov.Requests()
	if len(reqs) != 2 || reqs[0].Model != "test-model" || len(reqs[0].Messages) != 1 {
		t.Fatalf("requests = %+v", reqs)
	}
	if !strings.Contains(reqs[0].System, "prod") || !strings.Contains(reqs[0].System, "never invent") {
		t.Errorf("system = %q", reqs[0].System)
	}
	names := map[string]bool{}
	for _, tl := range reqs[0].Tools {
		names[tl.Name] = true
		if !json.Valid(tl.InputSchema) {
			t.Errorf("tool %s schema = %s", tl.Name, tl.InputSchema)
		}
	}
	if !names["get_doc"] || names["call_method"] || names["create_doc"] {
		t.Errorf("tools = %v", names)
	}

	calls, err := g.st.ListToolCalls(runID)
	if err != nil || len(calls) != 1 || calls[0].Status != ToolOK || !strings.Contains(calls[0].ResultText, "alpha") || calls[0].Site != "prod" {
		t.Errorf("tool calls = %+v, %v", calls, err)
	}
	var deltas strings.Builder
	for _, d := range g.h.named(EventChatDelta) {
		deltas.WriteString(d.(ChatDelta).Text)
	}
	if deltas.String() != "Let me look. TD-1 is alpha." {
		t.Errorf("deltas = %q", deltas.String())
	}
	var statuses []string
	for _, d := range g.h.named(EventChatTool) {
		ev := d.(ChatTool)
		if ev.RunID != runID || ev.Tool != "get_doc" || ev.Site != "prod" || ev.CallID != calls[0].ID {
			t.Errorf("tool event = %+v", ev)
		}
		statuses = append(statuses, ev.Status)
	}
	if strings.Join(statuses, ",") != "running,ok" {
		t.Errorf("tool statuses = %v", statuses)
	}
	u, _ := g.st.ListUsage(runID)
	if len(u) != 2 || u[0].Input != 100 || u[0].Cached != 7 || u[1].Turn != 2 || len(g.h.named(EventChatUsage)) != 2 {
		t.Errorf("usage = %+v", u)
	}
	run, _ := g.st.GetRun(runID)
	if run.Status != RunDone || run.Steps != 1 || run.Ended.IsZero() {
		t.Errorf("run = %+v", run)
	}
	for i, l := range engineAudit(t, g.path) {
		if l["run_id"] != runID || l["client"] != "foxmayn-desktop" {
			t.Errorf("audit line %d = %v", i, l)
		}
	}
}

func TestLoopParallelReads(t *testing.T) {
	g := newLoopRig(t)
	var inflight, peak atomic.Int32
	barrier := make(chan struct{})
	var once atomic.Bool
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("TD-%d", i)
		g.fake.Handle("GET /api/resource/ToDo/"+name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := inflight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			if n == 4 && once.CompareAndSwap(false, true) {
				close(barrier)
			}
			select {
			case <-barrier:
			case <-time.After(5 * time.Second):
			}
			inflight.Add(-1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"doctype":"ToDo","name":%q,"description":"doc %s"}}`, name, name)
		}))
	}
	// Model order is deliberately not name order.
	g.prov = llmtest.New(
		toolTurn(
			call("a", "get_doc", `{"doctype":"ToDo","name":"TD-3"}`),
			call("b", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`),
			call("c", "get_doc", `{"doctype":"ToDo","name":"TD-4"}`),
			call("d", "get_doc", `{"doctype":"ToDo","name":"TD-2"}`),
		),
		textTurn("done"),
	)
	g.r.provider = func(store.Conversation) (llm.Provider, string, error) { return g.prov, "m", nil }
	cid := g.conv(t, "read")
	if _, err := g.r.start(cid, "read four"); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	if peak.Load() != 4 {
		t.Errorf("peak concurrency = %d, want 4", peak.Load())
	}
	res := g.messages(t, cid)[2].Parts
	want := []string{"a:doc TD-3", "b:doc TD-1", "c:doc TD-4", "d:doc TD-2"}
	if len(res) != 4 {
		t.Fatalf("results = %+v", res)
	}
	for i, p := range res {
		tr := p.(llm.ToolResult)
		id, frag, _ := strings.Cut(want[i], ":")
		if tr.ID != id || tr.IsError || !strings.Contains(tr.Text, frag) {
			t.Errorf("result %d = %+v, want %s", i, tr, want[i])
		}
	}
}

func TestLoopUnknownToolAndBadArgs(t *testing.T) {
	g := newLoopRig(t,
		toolTurn(
			call("u", "no_such_tool", `{}`),
			llm.ToolCall{ID: "bad", Name: "get_doc", Args: json.RawMessage(`{}`), ArgsError: "unexpected end of JSON input"},
			call("m", "call_method", `{"method":"frappe.ping"}`),
		),
		textTurn("ok"),
	)
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "go")
	if err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	res := g.messages(t, cid)[2].Parts
	if len(res) != 3 {
		t.Fatalf("results = %+v", res)
	}
	for i, frag := range []string{"Unknown tool", "not valid", "Unknown tool"} {
		tr := res[i].(llm.ToolResult)
		if !tr.IsError || !strings.Contains(tr.Text, frag) {
			t.Errorf("result %d = %+v, want error with %q", i, tr, frag)
		}
	}
	// None of the three reaches the site or the audit log.
	if n := len(g.fake.RequestsTo(http.MethodGet, "/api/resource/ToDo")); n != 0 {
		t.Errorf("%d list requests", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(g.path), "mcp-audit.jsonl")); err == nil {
		t.Error("an audit log exists")
	}
	calls, _ := g.st.ListToolCalls(runID)
	if len(calls) != 3 || calls[0].Status != ToolError || calls[1].Status != ToolError {
		t.Errorf("stored calls = %+v", calls)
	}
}

func TestLoopPausesAndContinues(t *testing.T) {
	var turns []llmtest.Turn
	for i := 0; i < loopStepBudget; i++ {
		turns = append(turns, toolTurn(call(fmt.Sprint("c", i), "get_doc", `{"doctype":"ToDo","name":"TD-1"}`)))
	}
	turns = append(turns, textTurn("finished"))
	g := newLoopRig(t, turns...)
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "loop")
	if err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunPaused {
		t.Fatalf("done = %+v", d)
	}
	if run, _ := g.st.GetRun(runID); run.Status != RunPaused || run.Steps != loopStepBudget {
		t.Errorf("run = %+v", run)
	}
	if n := len(g.prov.Requests()); n != loopStepBudget {
		t.Errorf("%d requests before the pause", n)
	}
	if err := g.r.continueRun(runID); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 2); d.Status != RunDone || d.RunID != runID {
		t.Fatalf("done = %+v", d)
	}
	msgs := g.messages(t, cid)
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleAssistant || last.Parts[0].(llm.Text).Text != "finished" {
		t.Errorf("last = %+v", last)
	}
	if err := g.r.continueRun(runID); err == nil {
		t.Error("a finished run continued")
	}
}

func TestLoopCancelDuringStream(t *testing.T) {
	g := newLoopRig(t, llmtest.Turn{Events: []llm.Event{llm.TextDelta{Text: "thinking out loud"}}, Hang: true})
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "hello")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(g.h.named(EventChatDelta)) > 0 })
	t0 := time.Now()
	if !g.r.cancelRun(runID) {
		t.Fatal("run not active")
	}
	d := g.waitDone(t, 1)
	if el := time.Since(t0); el > time.Second || d.Status != RunCancelled {
		t.Errorf("cancel took %v, done = %+v", el, d)
	}
	if run, _ := g.st.GetRun(runID); run.Status != RunCancelled {
		t.Errorf("run = %+v", run)
	}
	msgs := g.messages(t, cid)
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleAssistant || last.Parts[0].(llm.Text).Text != "thinking out loud" {
		t.Errorf("partial text not kept: %+v", msgs)
	}
	if g.r.cancelRun(runID) {
		t.Error("finished run still active")
	}
}

func TestLoopCancelDuringSlowTool(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`)), textTurn("never"))
	entered := make(chan struct{})
	var once atomic.Bool
	g.fake.Handle("GET /api/resource/ToDo/TD-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if once.CompareAndSwap(false, true) {
			close(entered)
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "slow")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("tool never called")
	}
	t0 := time.Now()
	g.r.cancelRun(runID)
	d := g.waitDone(t, 1)
	if el := time.Since(t0); el > time.Second || d.Status != RunCancelled {
		t.Errorf("cancel took %v, done = %+v", el, d)
	}
	calls, _ := g.st.ListToolCalls(runID)
	if len(calls) != 1 || calls[0].Status != ToolStopped {
		t.Errorf("tool calls = %+v", calls)
	}
	// The history stays valid: the tool_use has its result.
	msgs := g.messages(t, cid)
	if len(msgs) != 3 || !msgs[2].Parts[0].(llm.ToolResult).IsError {
		t.Errorf("messages = %+v", msgs)
	}
	if n := len(g.prov.Requests()); n != 1 {
		t.Errorf("%d model turns", n)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestLoopCutsLongResults(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "get_doc", `{"doctype":"ToDo","name":"BIG"}`)), textTurn("ok"))
	big := strings.Repeat("é", 50000)
	g.fake.Add("ToDo", map[string]any{"name": "BIG", "description": big})
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "big")
	if err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	seen := g.prov.Requests()[1].Messages[2].Parts[0].(llm.ToolResult)
	if n := utf8.RuneCountInString(seen.Text); n < loopResultLimit || n > loopResultLimit+300 || !strings.Contains(seen.Text, "cut:") {
		t.Errorf("model saw %d characters: ...%q", n, seen.Text[len(seen.Text)-120:])
	}
	if !utf8.ValidString(seen.Text) {
		t.Error("cut inside a character")
	}
	calls, _ := g.st.ListToolCalls(runID)
	if len(calls) != 1 || strings.Count(calls[0].ResultText, "é") != 50000 {
		t.Errorf("store holds %d é", strings.Count(calls[0].ResultText, "é"))
	}
	if stored := g.messages(t, cid)[2].Parts[0].(llm.ToolResult).Text; stored != seen.Text {
		t.Error("history differs from what the model saw")
	}
}

func TestLoopPauseTurnContinues(t *testing.T) {
	g := newLoopRig(t,
		llmtest.Turn{Events: []llm.Event{llm.TextDelta{Text: "part one, "}, llm.Stop{Reason: llm.StopPauseTurn}}},
		textTurn("part two"),
	)
	cid := g.conv(t, "read")
	if _, err := g.r.start(cid, "long"); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	reqs := g.prov.Requests()
	if len(reqs) != 2 || len(reqs[1].Messages) != 2 || reqs[1].Messages[1].Role != llm.RoleAssistant {
		t.Fatalf("requests = %+v", reqs)
	}
	if run := g.h.named(EventChatTool); len(run) != 0 {
		t.Errorf("tool events: %v", run)
	}
}

func TestLoopRetriesRetryableErrors(t *testing.T) {
	g := newLoopRig(t,
		llmtest.Turn{StreamErr: &llm.APIError{Status: 529, Message: "overloaded"}},
		llmtest.Turn{Err: &llm.APIError{Status: 429, Message: "slow down"}},
		textTurn("fine"),
	)
	cid := g.conv(t, "read")
	if _, err := g.r.start(cid, "hi"); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	if n := len(g.prov.Requests()); n != 3 {
		t.Errorf("%d attempts", n)
	}
}

func TestLoopErrorsEndTheRun(t *testing.T) {
	const key = "sk-ant-secret-key-1234"
	cases := []struct {
		name  string
		turns []llmtest.Turn
		code  string
		reqs  int
	}{
		{"auth", []llmtest.Turn{{StreamErr: &llm.APIError{Status: 401, Message: "invalid x-api-key"}}}, CodeAuth, 1},
		{"retries used up", []llmtest.Turn{
			{StreamErr: &llm.APIError{Status: 500, Message: "boom"}},
			{StreamErr: &llm.APIError{Status: 500, Message: "boom"}},
			{StreamErr: &llm.APIError{Status: 500, Message: "boom"}},
		}, CodeFailed, 3},
		{"after output", []llmtest.Turn{{
			Events: []llm.Event{llm.TextDelta{Text: "x"}}, Err: &llm.APIError{Status: 500, Message: "cut"},
		}}, CodeFailed, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newLoopRig(t, tc.turns...)
			cid := g.conv(t, "read")
			runID, err := g.r.start(cid, "hi")
			if err != nil {
				t.Fatal(err)
			}
			if d := g.waitDone(t, 1); d.Status != RunError {
				t.Fatalf("done = %+v", d)
			}
			errs := g.h.named(EventChatError)
			if len(errs) != 1 {
				t.Fatalf("errors = %v", errs)
			}
			ce := errs[0].(ChatError)
			if ce.RunID != runID || ce.Error.Code != tc.code {
				t.Errorf("error = %+v", ce.Error)
			}
			if n := len(g.prov.Requests()); n != tc.reqs {
				t.Errorf("%d attempts, want %d", n, tc.reqs)
			}
			if run, _ := g.st.GetRun(runID); run.Status != RunError || run.Error == "" || strings.Contains(run.Error, key) {
				t.Errorf("run = %+v", run)
			}
			b, _ := json.Marshal(g.h.events)
			if strings.Contains(string(b), key) {
				t.Error("key in events")
			}
		})
	}
}

func TestLoopEndsOnMaxTokensAndRefusal(t *testing.T) {
	for _, reason := range []string{llm.StopMaxTokens, llm.StopRefusal} {
		g := newLoopRig(t, llmtest.Turn{Events: []llm.Event{llm.TextDelta{Text: "partial"}, llm.Stop{Reason: reason, Category: "cyber"}}})
		cid := g.conv(t, "read")
		if _, err := g.r.start(cid, "hi"); err != nil {
			t.Fatal(err)
		}
		d := g.waitDone(t, 1)
		if d.Status != RunDone || d.StopReason != reason {
			t.Errorf("%s: done = %+v", reason, d)
		}
	}
}

func TestLoopWriteToolNotAvailableYet(t *testing.T) {
	g := newLoopRig(t,
		toolTurn(
			call("w", "create_doc", `{"doctype":"ToDo","data":{"description":"new"}}`),
			call("x", "delete_doc", `{"doctype":"ToDo","name":"TD-1"}`),
		),
		textTurn("I could not change anything."),
	)
	cid := g.conv(t, "ask")
	runID, err := g.r.start(cid, "add a todo and delete TD-1")
	if err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	for i, p := range g.messages(t, cid)[2].Parts {
		tr := p.(llm.ToolResult)
		if !tr.IsError || !strings.Contains(tr.Text, "not available") {
			t.Errorf("result %d = %+v", i, tr)
		}
	}
	if n := g.fake.Count("ToDo"); n != 3 {
		t.Errorf("%d ToDos, want 3", n)
	}
	for _, rq := range g.fake.Requests() {
		if rq.Method != http.MethodGet && !strings.Contains(rq.Path, "login") {
			t.Errorf("site got %s %s", rq.Method, rq.Path)
		}
	}
	calls, _ := g.st.ListToolCalls(runID)
	if len(calls) != 2 || calls[0].Status != ToolError {
		t.Errorf("stored calls = %+v", calls)
	}
}

func TestLoopOneRunPerConversation(t *testing.T) {
	g := newLoopRig(t, llmtest.Turn{Hang: true})
	cid := g.conv(t, "read")
	runID, err := g.r.start(cid, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.r.start(cid, "second"); err == nil {
		t.Error("second run accepted")
	}
	if _, err := g.r.start("nope", "x"); err == nil {
		t.Error("unknown conversation accepted")
	}
	g.r.shutdown()
	if d := g.waitDone(t, 1); d.Status != RunCancelled || d.RunID != runID {
		t.Errorf("done = %+v", d)
	}
	if _, err := g.r.start(cid, "late"); err == nil {
		t.Error("start after shutdown accepted")
	}
}
