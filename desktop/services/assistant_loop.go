package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"golang.org/x/sync/errgroup"
)

const (
	// loopStepBudget is how many tool calls a run makes before it pauses.
	loopStepBudget = 25
	// loopParallelReads caps the read calls of one group that run at once.
	loopParallelReads = 4
	// loopResultLimit caps the characters of a tool result sent to the model.
	loopResultLimit = 40000
	// loopDeltaEvery is how often buffered text goes out as a chat:delta.
	loopDeltaEvery = 40 * time.Millisecond
	// loopMaxTokens is the output budget of one model turn.
	loopMaxTokens = 8192
	// loopSummaryLimit caps the summary text of a chat:tool event.
	loopSummaryLimit = 120
)

const loopBaseRules = `You are the Foxmayn Frappe assistant, working on the Frappe site %q for the person using this app.
Use the tools to look things up; never invent data, documents, names or numbers. When a tool fails or returns nothing, say so plainly. Keep answers short and base them on tool results.`

const loopChangesUnavailable = "Changes are not available in this conversation yet. Tell the user you cannot make this change."

// providerFunc finds the provider and model a conversation uses.
type providerFunc func(conv store.Conversation) (llm.Provider, string, error)

// runner runs assistant turns: one goroutine per run, each with its own
// context under the runner's, so cancel(runID) and shutdown() end them.
type runner struct {
	store    *store.Store
	engine   *Engine
	provider providerFunc
	emit     func(name string, payload any)
	backoff  []time.Duration // waits before the retries of a failed turn

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	active map[string]*activeRun // by run id
}

// activeRun is one run in flight.
type activeRun struct {
	r      *runner
	runID  string
	conv   store.Conversation
	cancel context.CancelFunc

	session *EngineSession
	tools   map[string]bool
	steps   int // tool calls made, over the whole run
	turns   int // model turns, over the whole run
}

func newRunner(s *store.Store, e *Engine, p providerFunc, emit func(string, any)) *runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &runner{
		store: s, engine: e, provider: p, emit: emit,
		backoff: []time.Duration{time.Second, 3 * time.Second},
		ctx:     ctx, cancel: cancel,
		active: map[string]*activeRun{},
	}
}

// start appends the user's message to the conversation, creates a run and
// starts it. At most one run is active per conversation.
func (r *runner) start(convID, userText string) (string, error) {
	conv, err := r.store.GetConversation(convID)
	if err != nil {
		return "", wrapStoreErr(err)
	}
	if strings.TrimSpace(userText) == "" {
		return "", invalid("text", "Write a message first.")
	}
	parts, err := llm.MarshalParts([]llm.Part{llm.Text{Text: userText}})
	if err != nil {
		return "", newError(CodeFailed, "Could not save the message.", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.admit(convID); err != nil {
		return "", err
	}
	if _, err := r.store.AppendMessage(convID, string(llm.RoleUser), parts); err != nil {
		return "", wrapStoreErr(err)
	}
	run, err := r.store.CreateRun(convID)
	if err != nil {
		return "", wrapStoreErr(err)
	}
	r.launch(run.ID, conv, 0)
	return run.ID, nil
}

// continueRun resumes a paused run with a fresh step budget.
func (r *runner) continueRun(runID string) error {
	run, err := r.store.GetRun(runID)
	if err != nil {
		return wrapStoreErr(err)
	}
	if run.Status != RunPaused {
		return &Error{Code: CodeInvalid, Message: "This run is not paused."}
	}
	conv, err := r.store.GetConversation(run.ConvID)
	if err != nil {
		return wrapStoreErr(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.admit(run.ConvID); err != nil {
		return err
	}
	r.launch(runID, conv, run.Steps)
	return nil
}

// admit checks that a run may start on convID. r.mu is held.
func (r *runner) admit(convID string) error {
	if r.closed {
		return &Error{Code: CodeUnavailable, Message: "The assistant is shutting down."}
	}
	for _, a := range r.active {
		if a.conv.ID == convID {
			return &Error{Code: CodeInvalid, Message: "The assistant is still answering in this conversation."}
		}
	}
	return nil
}

// launch registers and starts a run goroutine. r.mu is held.
func (r *runner) launch(runID string, conv store.Conversation, steps int) {
	ctx, cancel := context.WithCancel(r.ctx)
	a := &activeRun{r: r, runID: runID, conv: conv, cancel: cancel, steps: steps}
	r.active[runID] = a
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancel()
		a.run(ctx)
	}()
}

// cancel ends a run; it reports whether the run was active.
func (r *runner) cancelRun(runID string) bool {
	r.mu.Lock()
	a := r.active[runID]
	r.mu.Unlock()
	if a == nil {
		return false
	}
	a.cancel()
	return true
}

// shutdown cancels every run and waits for them to finish. Later starts fail.
func (r *runner) shutdown() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	r.wg.Wait()
}

func wrapStoreErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: CodeNotFound, Message: "That conversation or run no longer exists."}
	}
	return newError(CodeFailed, "Could not read or save the conversation.", err)
}

// outcome is how a run ended.
type outcome struct {
	status string
	err    error
	stop   string // StopReason for the done event
	cat    string
}

func (a *activeRun) run(ctx context.Context) {
	out := a.loop(ctx)
	if out.status == RunError && ctx.Err() != nil {
		out.status, out.err = RunCancelled, nil
	}
	errText := ""
	var pub *Error
	if out.err != nil {
		pub = toServiceError(out.err)
		errText = pub.Message
		if pub.Detail != "" {
			errText += ": " + pub.Detail
		}
	}
	if err := a.r.store.FinishRun(a.runID, out.status, errText, a.steps); err != nil && out.status != RunError {
		// The run is over either way; the store failing is the news.
		out.status, pub = RunError, newError(CodeFailed, "Could not save the conversation.", err)
	}
	a.r.mu.Lock()
	delete(a.r.active, a.runID)
	a.r.mu.Unlock()
	if out.status == RunError && pub != nil {
		a.r.emit(EventChatError, ChatError{RunID: a.runID, Error: pub})
	}
	a.r.emit(EventChatDone, ChatDone{RunID: a.runID, Status: out.status, StopReason: out.stop, Category: out.cat})
}

// toServiceError turns a run failure into the *Error the UI shows. Provider
// error messages never hold the key (llm.APIError's contract).
func toServiceError(err error) *Error {
	var se *Error
	if errors.As(err, &se) {
		return se
	}
	if llm.IsAuth(err) {
		return newError(CodeAuth, "The provider did not accept the API key.", err)
	}
	var ae *llm.APIError
	if errors.As(err, &ae) {
		return newError(CodeFailed, "The AI provider returned an error.", err)
	}
	return newError(CodeFailed, "The assistant stopped because of an error.", err)
}

func (a *activeRun) loop(ctx context.Context) outcome {
	prov, model, err := a.r.provider(a.conv)
	if err != nil {
		return outcome{status: RunError, err: err}
	}
	mode := EngineRead
	if a.conv.Mode == "ask" {
		mode = EngineAsk
	}
	// The elicitor is nil: ffc's confirmations are declined until approval
	// cards exist.
	sess, err := a.r.engine.Open(ctx, a.conv.Site, mode, nil)
	if err != nil {
		return outcome{status: RunError, err: err}
	}
	defer sess.Close()
	a.session = sess
	tools, offered, err := a.offerTools(ctx)
	if err != nil {
		return outcome{status: RunError, err: err}
	}
	system := fmt.Sprintf(loopBaseRules, a.conv.Site)
	if in := sess.Instructions(); in != "" {
		system += "\n\n" + in
	}
	usage, err := a.r.store.ListUsage(a.runID)
	if err != nil {
		return outcome{status: RunError, err: wrapStoreErr(err)}
	}
	a.turns = len(usage)
	budget := loopStepBudget

	for {
		if ctx.Err() != nil {
			return outcome{status: RunCancelled}
		}
		history, err := a.history()
		if err != nil {
			return outcome{status: RunError, err: err}
		}
		req := llm.Request{Model: model, System: system, Messages: history, Tools: tools, MaxTokens: loopMaxTokens}
		t, err := a.turn(ctx, prov, req)
		if t.text != "" && (err != nil || t.stop == nil) && ctx.Err() != nil {
			// Keep what the user saw of a turn they stopped.
			a.appendMessage(llm.RoleAssistant, []llm.Part{llm.Text{Text: t.text}})
		}
		if err != nil {
			if ctx.Err() != nil {
				return outcome{status: RunCancelled}
			}
			return outcome{status: RunError, err: err}
		}
		stop := *t.stop
		a.turns++
		if t.usage != nil {
			u := store.Usage{RunID: a.runID, Turn: a.turns, Input: t.usage.In, Output: t.usage.Out, Cached: t.usage.Cached}
			if err := a.r.store.AddUsage(u); err != nil {
				return outcome{status: RunError, err: wrapStoreErr(err)}
			}
			a.r.emit(EventChatUsage, ChatUsage{RunID: a.runID, Turn: u.Turn, Input: u.Input, Output: u.Output, Cached: u.Cached})
		}
		var msgID string
		if len(stop.Message.Parts) > 0 {
			m, err := a.appendMessage(llm.RoleAssistant, stop.Message.Parts)
			if err != nil {
				return outcome{status: RunError, err: err}
			}
			msgID = m.ID
		}
		switch stop.Reason {
		case llm.StopPauseTurn:
			continue
		case llm.StopToolUse:
			if len(t.calls) == 0 {
				return outcome{status: RunDone}
			}
			results := a.execute(ctx, msgID, t.calls, offered)
			budget -= len(t.calls)
			a.steps += len(t.calls)
			if _, err := a.appendMessage(llm.RoleUser, results); err != nil {
				return outcome{status: RunError, err: err}
			}
			if ctx.Err() != nil {
				return outcome{status: RunCancelled}
			}
			if budget <= 0 {
				return outcome{status: RunPaused}
			}
		case llm.StopMaxTokens, llm.StopRefusal:
			return outcome{status: RunDone, stop: stop.Reason, cat: stop.Category}
		default:
			return outcome{status: RunDone}
		}
	}
}

// offerTools lists the session's tools for the model, without call_method.
func (a *activeRun) offerTools(ctx context.Context) ([]llm.Tool, map[string]bool, error) {
	list, err := a.session.Tools(ctx)
	if err != nil {
		return nil, nil, err
	}
	var out []llm.Tool
	offered := map[string]bool{}
	for _, t := range list {
		if t.Name == "call_method" {
			continue
		}
		schema, err := toolSchema(t)
		if err != nil {
			return nil, nil, newError(CodeFailed, "Could not read the tool list.", err)
		}
		out = append(out, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
		offered[t.Name] = true
	}
	return out, offered, nil
}

// toolSchema returns a tool's JSON Schema whichever way the tool defines it.
func toolSchema(t mcp.Tool) (json.RawMessage, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	var v struct {
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if len(v.InputSchema) == 0 {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	return v.InputSchema, nil
}

// history reads the conversation as model messages.
func (a *activeRun) history() ([]llm.Message, error) {
	rows, err := a.r.store.ListMessages(a.conv.ID)
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	out := make([]llm.Message, 0, len(rows))
	for _, m := range rows {
		parts, err := llm.UnmarshalParts(m.PartsJSON)
		if err != nil {
			return nil, newError(CodeFailed, "A saved message could not be read.", err)
		}
		out = append(out, llm.Message{Role: llm.Role(m.Role), Parts: parts})
	}
	return out, nil
}

func (a *activeRun) appendMessage(role llm.Role, parts []llm.Part) (store.Message, error) {
	s, err := llm.MarshalParts(parts)
	if err != nil {
		return store.Message{}, newError(CodeFailed, "Could not save the message.", err)
	}
	m, err := a.r.store.AppendMessage(a.conv.ID, string(role), s)
	if err != nil {
		return store.Message{}, wrapStoreErr(err)
	}
	return m, nil
}

// turnResult is what one model turn produced.
type turnResult struct {
	text  string // the text shown, for a turn cut short
	calls []llm.ToolCall
	usage *llm.Usage
	stop  *llm.Stop
}

// turn streams one model turn, retrying a retryable failure that came before
// any output.
func (a *activeRun) turn(ctx context.Context, prov llm.Provider, req llm.Request) (turnResult, error) {
	for attempt := 0; ; attempt++ {
		t, got, err := a.streamOnce(ctx, prov, req)
		if err == nil || ctx.Err() != nil {
			return t, err
		}
		if got || !llm.IsRetryable(err) || attempt >= len(a.r.backoff) {
			return t, err
		}
		select {
		case <-time.After(a.r.backoff[attempt]):
		case <-ctx.Done():
			return t, ctx.Err()
		}
	}
}

// streamOnce runs one Stream call. got reports whether any event arrived.
func (a *activeRun) streamOnce(ctx context.Context, prov llm.Provider, req llm.Request) (t turnResult, got bool, err error) {
	st, err := prov.Stream(ctx, req)
	if err != nil {
		return t, false, err
	}
	defer st.Close()
	// A stream that ignores ctx must not hold the run past a cancel.
	defer context.AfterFunc(ctx, func() { st.Close() })()
	d := &deltaBatcher{emit: a.r.emit, runID: a.runID}
	defer func() {
		d.flush()
		t.text = d.all()
	}()
	for {
		ev, nerr := st.Next()
		if nerr != nil {
			if ctx.Err() != nil {
				return t, got, ctx.Err()
			}
			if errors.Is(nerr, io.EOF) {
				nerr = &llm.APIError{Message: "the stream ended before the model finished"}
			}
			return t, got, nerr
		}
		got = true
		switch e := ev.(type) {
		case llm.TextDelta:
			d.add(e.Text)
		case llm.ToolCall:
			t.calls = append(t.calls, e)
		case llm.Usage:
			u := e
			t.usage = &u
		case llm.Stop:
			s := e
			t.stop = &s
			return t, got, nil
		}
	}
}

// deltaBatcher joins text deltas into one chat:delta about every 40 ms.
type deltaBatcher struct {
	emit  func(string, any)
	runID string

	mu    sync.Mutex
	buf   strings.Builder
	shown strings.Builder
	timer *time.Timer
}

func (d *deltaBatcher) add(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.buf.WriteString(s)
	d.shown.WriteString(s)
	if d.timer == nil {
		d.timer = time.AfterFunc(loopDeltaEvery, d.flush)
	}
}

func (d *deltaBatcher) flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if d.buf.Len() == 0 {
		return
	}
	text := d.buf.String()
	d.buf.Reset()
	d.emit(EventChatDelta, ChatDelta{RunID: d.runID, Text: text})
}

func (d *deltaBatcher) all() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.shown.String()
}

// callEntry is one tool call of a turn on its way to a result.
type callEntry struct {
	call   llm.ToolCall
	args   map[string]any
	row    store.ToolCall
	result string
	isErr  bool
	status string
}

// execute answers every call of a turn and returns the ToolResult parts in
// the model's order. Consecutive plain reads run in parallel groups; any
// other call runs alone, in order.
func (a *activeRun) execute(ctx context.Context, msgID string, calls []llm.ToolCall, offered map[string]bool) []llm.Part {
	entries := make([]*callEntry, len(calls))
	for i, c := range calls {
		argsJSON := string(c.Args)
		if argsJSON == "" {
			argsJSON = "{}"
		}
		e := &callEntry{call: c}
		row, err := a.r.store.InsertToolCall(a.runID, msgID, c.Name, a.conv.Site, argsJSON, ToolRunning)
		if err == nil {
			e.row = row
		}
		entries[i] = e
		a.emitTool(e, ToolRunning, summarizeArgs(c.Args))
	}

	var group []*callEntry
	flush := func() {
		a.runReads(ctx, group)
		group = nil
	}
	for _, e := range entries {
		switch a.plan(ctx, e, offered) {
		case planRead:
			group = append(group, e)
		default:
			flush()
			a.finish(e)
		}
	}
	flush()

	out := make([]llm.Part, len(entries))
	for i, e := range entries {
		out[i] = llm.ToolResult{ID: e.call.ID, Text: cutForModel(e.result), IsError: e.isErr}
	}
	return out
}

type planKind int

const (
	planDone planKind = iota // the entry has its result already
	planRead                 // a read that can run in parallel
)

// plan decides what happens to a call. A call that cannot run gets its error
// result here.
func (a *activeRun) plan(ctx context.Context, e *callEntry, offered map[string]bool) planKind {
	fail := func(msg string) planKind {
		e.result, e.isErr, e.status = msg, true, ToolError
		return planDone
	}
	c := e.call
	switch {
	case ctx.Err() != nil:
		e.result, e.isErr, e.status = "Stopped by the user before this tool ran.", true, ToolStopped
		return planDone
	case c.ArgsError != "":
		return fail(fmt.Sprintf("The arguments for %s were not valid (%s). Call it again with a valid JSON object of arguments.", c.Name, c.ArgsError))
	case !offered[c.Name]:
		return fail(fmt.Sprintf("Unknown tool %q. Use only the tools you were given.", c.Name))
	}
	args := map[string]any{}
	if len(c.Args) > 0 {
		if err := json.Unmarshal(c.Args, &args); err != nil || args == nil {
			return fail(fmt.Sprintf("The arguments for %s were not a JSON object. Call it again with one.", c.Name))
		}
	}
	e.args = args
	cls, err := a.session.Classify(c.Name, args)
	if err != nil {
		return fail("This call was refused: " + err.Error())
	}
	if cls.Denied != "" {
		return fail(cls.Denied)
	}
	if cls.Action == "read" && !cls.Confirm {
		return planRead
	}
	// Changes are not offered yet; approval cards replace this branch.
	return fail(loopChangesUnavailable)
}

// runReads runs a group of reads, at most loopParallelReads at once.
func (a *activeRun) runReads(ctx context.Context, group []*callEntry) {
	var g errgroup.Group
	g.SetLimit(loopParallelReads)
	for _, e := range group {
		g.Go(func() error {
			a.call(ctx, e)
			a.finish(e)
			return nil
		})
	}
	_ = g.Wait()
}

// call runs one tool through the session.
func (a *activeRun) call(ctx context.Context, e *callEntry) {
	res, err := a.session.Call(ctx, a.runID, e.call.Name, e.args)
	switch {
	case ctx.Err() != nil && (err != nil || res.IsError):
		// ffc reports a request cut short by the cancel as a tool error.
		e.result, e.isErr, e.status = "Stopped by the user before the result was known.", true, ToolStopped
	case err != nil:
		e.result, e.isErr, e.status = "The tool call failed: "+toServiceError(err).Error(), true, ToolError
	default:
		e.result, e.isErr = callResultText(res), res.IsError
		e.status = ToolOK
		if e.isErr {
			e.status = ToolError
		}
	}
}

// finish stores a call's outcome and reports it.
func (a *activeRun) finish(e *callEntry) {
	if e.row.ID != "" {
		_ = a.r.store.FinishToolCall(e.row.ID, e.result, e.status, "")
	}
	summary := ""
	if e.status != ToolOK {
		summary = clip(firstLine(e.result), loopSummaryLimit)
	} else {
		summary = summarizeArgs(e.call.Args)
	}
	a.emitTool(e, e.status, summary)
}

func (a *activeRun) emitTool(e *callEntry, status, summary string) {
	a.r.emit(EventChatTool, ChatTool{
		RunID: a.runID, CallID: e.row.ID, Tool: e.call.Name, Site: a.conv.Site, Status: status, Summary: summary,
	})
}

// callResultText joins the text content of a tool result.
func callResultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// cutForModel keeps the first loopResultLimit characters of a result.
func cutForModel(s string) string {
	if utf8.RuneCountInString(s) <= loopResultLimit {
		return s
	}
	cut := 0
	for n := 0; n < loopResultLimit; n++ {
		_, w := utf8.DecodeRuneInString(s[cut:])
		cut += w
	}
	more := utf8.RuneCountInString(s[cut:])
	return fmt.Sprintf("%s\n[cut: %d more characters not shown. Narrow the request (fewer fields, a filter or a limit) to see them.]", s[:cut], more)
}

// summarizeArgs is a short line about what a call is for.
func summarizeArgs(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"doctype", "name", "report_name", "txt", "text"} {
		if v, ok := m[k].(string); ok && v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return clip(strings.Join(parts, " "), loopSummaryLimit)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}
