package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// EngineMode is what a session may do to a site.
type EngineMode int

const (
	// EngineRead serves read tools only (Policy.ReadOnly).
	EngineRead EngineMode = iota
	// EngineAsk also serves write tools. ffc asks the session's Elicitor
	// only before the calls it counts as needing confirmation (deletes,
	// cancels, sharing and the like); a plain create_doc or update_doc runs
	// unasked. The engine alone is not a write gate: the caller must show
	// its own card for every call whose Classify has Action other than
	// "read" and WillAsk false.
	EngineAsk
)

// engineClientName is the client name in the MCP audit log.
const engineClientName = "foxmayn-desktop"

// engineBuildTimeout bounds building a server, which signs in to the site
// (password and OAuth sites) and so could otherwise wait on a dead host.
const engineBuildTimeout = 60 * time.Second

// engineIdleAfter is how long a cached server nobody uses is kept.
const engineIdleAfter = 10 * time.Minute

// EngineSpec is what a server serves on a site. Policy is ffc's options
// layer: its lists can only narrow the site's own policy. The engine sets
// Policy.Confirm to "always" and Policy.ReadOnly from Mode whatever the spec
// holds, so neither can be loosened through a spec.
type EngineSpec struct {
	Mode   EngineMode
	Policy config.MCPPolicy
	// Toolsets are ffc's tool sets; nil is core and lifecycle.
	Toolsets []string
}

// canonical returns the spec as ffc gets it: Confirm and ReadOnly forced,
// lists trimmed, deduplicated and sorted, and empty lists nil (ffc refuses
// an empty one).
func (sp EngineSpec) canonical() EngineSpec {
	clean := func(in []string) []string {
		var out []string
		for _, v := range in {
			if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		slices.Sort(out)
		return out
	}
	// The whole policy is copied, so a field ffc adds later is carried, not
	// dropped (TestEngineSpecCanonicalCarriesEveryField).
	p := sp.Policy
	p.Confirm = config.ConfirmAlways
	p.ReadOnly = sp.Mode != EngineAsk
	p.AllowTools = clean(p.AllowTools)
	p.AllowDoctypes = clean(p.AllowDoctypes)
	p.DenyDoctypes = clean(p.DenyDoctypes)
	p.AllowMethods = clean(p.AllowMethods)
	p.DenyMethods = clean(p.DenyMethods)
	return EngineSpec{Mode: sp.Mode, Policy: p, Toolsets: clean(sp.Toolsets)}
}

// engineDefaultToolsets are the tool sets ffc serves for nil Toolsets
// (defaultToolsets in internal/cmd/mcp_policy.go).
var engineDefaultToolsets = []string{"core", "lifecycle"}

// hash is the sha256 of the canonical policy and tool sets: two specs that
// serve the same share a server. For the hash only, nil tool sets count as
// the defaults and DocType names as lower case (ffc compares them without
// case), so equal specs written differently share a server.
func (sp EngineSpec) hash() string {
	c := sp.canonical()
	if c.Toolsets == nil {
		c.Toolsets = engineDefaultToolsets
	}
	lower := func(in []string) []string {
		if in == nil {
			return nil
		}
		out := make([]string, len(in))
		for i, v := range in {
			out[i] = strings.ToLower(v)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	c.Policy.AllowDoctypes = lower(c.Policy.AllowDoctypes)
	c.Policy.DenyDoctypes = lower(c.Policy.DenyDoctypes)
	b, _ := json.Marshal(struct {
		Policy   config.MCPPolicy `json:"policy"`
		Toolsets []string         `json:"toolsets"`
	}{c.Policy, c.Toolsets})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Elicitor answers an ffc confirmation question. A nil Elicitor declines
// every question. So does an error, a nil result, and a session that is
// closed or cancelled before the Elicitor answers: it is run on its own
// goroutine and its late answer is dropped. The Elicitor must return when
// its ctx ends, or its goroutine lingers.
type Elicitor func(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error)

// Engine hosts the ffc MCP server in this process: one server per (site,
// spec), built on first use and shared by the sessions that open it, and
// closed after engineIdleAfter without a session. It is safe for concurrent
// use.
type Engine struct {
	configPath string
	ctx        context.Context // ends with Close; builds follow it
	cancel     context.CancelFunc
	closeSrv   func(*cmd.MCPServer) // a seam for tests
	idleAfter  time.Duration        // a seam for tests

	startMu  sync.Mutex // see startClient
	mu       sync.Mutex
	closed   bool
	done     chan struct{} // closed when Close has finished
	servers  map[engineKey]*engineServer
	sessions map[*EngineSession]struct{}
}

type engineKey struct {
	site string
	spec string // EngineSpec.hash
}

// engineServer is a cached server. It is built once (ready closes when srv
// or err is set) and closed once, when it is retired and its last
// reference is gone.
type engineServer struct {
	key       engineKey
	spec      EngineSpec // canonical
	ready     chan struct{}
	srv       *cmd.MCPServer
	err       error
	refs      int
	retired   bool
	srvClosed bool
	idleGen   int // bumped each time refs drops to 0; see idle
}

// NewEngine returns an engine over the ffc config file at configPath.
func NewEngine(configPath string) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		configPath: configPath,
		ctx:        ctx,
		cancel:     cancel,
		closeSrv:   (*cmd.MCPServer).Close,
		idleAfter:  engineIdleAfter,
		done:       make(chan struct{}),
		servers:    map[engineKey]*engineServer{},
		sessions:   map[*EngineSession]struct{}{},
	}
}

// Open starts a session on site with the site's own policy in mode. The
// session's own in-process MCP client sends elicitation requests to elicit.
func (e *Engine) Open(ctx context.Context, site string, mode EngineMode, elicit Elicitor) (*EngineSession, error) {
	return e.OpenSpec(ctx, site, EngineSpec{Mode: mode}, elicit)
}

// OpenSpec starts a session on site with spec narrowing the site's policy.
func (e *Engine) OpenSpec(ctx context.Context, site string, spec EngineSpec, elicit Elicitor) (*EngineSession, error) {
	if site == "" {
		return nil, invalid("site", "Choose a site.")
	}
	if spec.Mode != EngineRead && spec.Mode != EngineAsk {
		return nil, invalid("mode", "Unknown mode.")
	}
	spec = spec.canonical()
	es, err := e.acquire(ctx, engineKey{site, spec.hash()}, spec)
	if err != nil {
		return nil, err
	}
	sess, err := e.newSession(ctx, es, elicit)
	if err != nil {
		e.release(es)
		return nil, err
	}
	return sess, nil
}

// acquire returns the server for key with a reference taken, building it if
// needed.
func (e *Engine) acquire(ctx context.Context, key engineKey, spec EngineSpec) (*engineServer, error) {
	if err := ctx.Err(); err != nil {
		return nil, newError(CodeCancelled, "Cancelled.", err)
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, errEngineClosed()
	}
	es := e.servers[key]
	build := es == nil
	if build {
		es = &engineServer{key: key, spec: spec, ready: make(chan struct{})}
		e.servers[key] = es
	}
	es.refs++
	e.mu.Unlock()

	if build {
		// The build may log in to the site, so it runs on its own goroutine
		// (this caller stops waiting when its ctx ends, like the others) and
		// follows the engine, not this caller. It holds a reference of its
		// own, so a server finished after every waiter left is still closed.
		e.mu.Lock()
		es.refs++
		e.mu.Unlock()
		go e.build(es)
	}

	select {
	case <-es.ready:
	case <-ctx.Done():
		e.release(es)
		return nil, newError(CodeCancelled, "Cancelled.", ctx.Err())
	}
	if es.err != nil {
		e.release(es)
		return nil, es.err
	}
	return es, nil
}

// build builds es's server and publishes the result to its waiters.
func (e *Engine) build(es *engineServer) {
	defer e.release(es)
	key := es.key
	bctx, cancel := context.WithTimeout(e.ctx, engineBuildTimeout)
	defer cancel()
	srv, err := cmd.NewMCPServer(bctx, cmd.MCPOptions{
		ConfigPath: e.configPath,
		Site:       key.site,
		Sites:      []string{key.site},
		Policy:     es.spec.Policy,
		Toolsets:   es.spec.Toolsets,
	})
	e.mu.Lock()
	var late *cmd.MCPServer
	switch {
	case e.closed:
		es.err = errEngineClosed()
		if err == nil {
			es.srvClosed, late = true, srv
		}
	case err != nil:
		es.err = siteError("Starting the assistant engine", err)
	default:
		es.srv = srv
	}
	if es.err != nil && e.servers[key] == es {
		delete(e.servers, key)
	}
	close(es.ready)
	e.mu.Unlock()
	if late != nil {
		e.closeSrv(late)
	}
}

// takeClose returns es's server if it must be closed now, once. e.mu is held.
func (e *Engine) takeClose(es *engineServer) *cmd.MCPServer {
	if es.srv == nil || es.srvClosed || !es.retired || es.refs > 0 {
		return nil
	}
	es.srvClosed = true
	return es.srv
}

// release drops one reference and closes a retired server nobody uses. A
// cached server nobody uses any more is closed after idleAfter, unless it
// is used again meanwhile.
func (e *Engine) release(es *engineServer) {
	e.mu.Lock()
	es.refs--
	srv := e.takeClose(es)
	if es.refs == 0 && !es.retired && !e.closed && es.srv != nil {
		es.idleGen++
		gen := es.idleGen
		time.AfterFunc(e.idleAfter, func() { e.idle(es, gen) })
	}
	e.mu.Unlock()
	if srv != nil {
		e.closeSrv(srv)
	}
}

// idle retires es if nobody used it since its refs dropped to 0 the gen-th
// time. A server with a session (an active run holds one) has refs > 0 and
// is never closed here.
func (e *Engine) idle(es *engineServer, gen int) {
	e.mu.Lock()
	if e.closed || es.retired || es.refs > 0 || es.idleGen != gen || e.servers[es.key] != es {
		e.mu.Unlock()
		return
	}
	delete(e.servers, es.key)
	es.retired = true
	srv := e.takeClose(es)
	e.mu.Unlock()
	if srv != nil {
		e.closeSrv(srv)
	}
}

// retireAll removes the cached servers and returns those nobody uses.
func (e *Engine) retireAll() []*cmd.MCPServer {
	e.mu.Lock()
	defer e.mu.Unlock()
	var idle []*cmd.MCPServer
	for k, es := range e.servers {
		delete(e.servers, k)
		es.retired = true
		if srv := e.takeClose(es); srv != nil {
			idle = append(idle, srv)
		}
	}
	return idle
}

// Invalidate retires every cached server: the config changed. Sessions that
// are open keep their server until they close; new Opens build a new one.
func (e *Engine) Invalidate() {
	for _, s := range e.retireAll() {
		e.closeSrv(s)
	}
}

// Close ends every session (cancelling and waiting for its calls), closes
// every server and refuses later Opens. A second Close waits for the first.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		<-e.done
		return
	}
	e.closed = true
	sessions := make([]*EngineSession, 0, len(e.sessions))
	for s := range e.sessions {
		sessions = append(sessions, s)
	}
	e.mu.Unlock()
	// No session takes a new call or an answer from here on: all are marked
	// closed and cancelled before any wait.
	for _, s := range sessions {
		s.begin()
	}
	e.cancel() // the sessions' contexts and builds under way end
	for _, s := range e.retireAll() {
		e.closeSrv(s)
	}
	for _, s := range sessions {
		s.Close()
	}
	close(e.done)
}

func errEngineClosed() *Error {
	return &Error{Code: CodeUnavailable, Message: "The assistant engine is shut down."}
}

// EngineSession is one in-process MCP client on an engine server. It is safe
// for concurrent use.
type EngineSession struct {
	engine *Engine
	es     *engineServer
	srv    *cmd.MCPServer
	client *mcpclient.Client
	instr  string

	ctx    context.Context // ends when the session closes
	cancel context.CancelFunc
	calls  sync.WaitGroup // in-flight Tools and Call

	mu         sync.Mutex
	closed     bool
	done       chan struct{} // closed when Close has finished
	finishOnce sync.Once
}

func (e *Engine) newSession(ctx context.Context, es *engineServer, elicit Elicitor) (*EngineSession, error) {
	s := &EngineSession{engine: e, es: es, srv: es.srv, done: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(e.ctx)
	fail := func(err error) (*EngineSession, error) {
		s.cancel()
		if _, ok := err.(*Error); ok {
			return nil, err
		}
		return nil, newError(CodeFailed, "Could not start the assistant engine.", err)
	}
	c, err := e.startClient(ctx, es.srv, engineElicit{s, elicit})
	if err != nil {
		return fail(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ClientInfo = mcp.Implementation{Name: engineClientName, Version: AppVersion}
	res, err := c.Initialize(ctx, init)
	if err != nil {
		c.Close()
		return fail(err)
	}
	s.client, s.instr = c, res.Instructions
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		c.Close()
		return fail(errEngineClosed())
	}
	e.sessions[s] = struct{}{}
	e.mu.Unlock()
	return s, nil
}

// startClient connects a new in-process client. mcp-go names the client's
// session after the clock's nanoseconds, so two clients started within one
// clock tick (coarse on Windows) collide: starts are serialised and a
// collision is retried on a fresh id.
func (e *Engine) startClient(ctx context.Context, srv *cmd.MCPServer, h mcpclient.ElicitationHandler) (*mcpclient.Client, error) {
	e.startMu.Lock()
	defer e.startMu.Unlock()
	var err error
	for range 50 {
		var c *mcpclient.Client
		c, err = mcpclient.NewInProcessClientWithOptions(srv.MCPServer, mcpclient.WithElicitationHandler(h))
		if err != nil {
			return nil, err
		}
		if err = c.Start(ctx); err == nil {
			return c, nil
		}
		c.Close()
		if !errors.Is(err, mcpserver.ErrSessionExists) {
			return nil, err
		}
		time.Sleep(time.Millisecond)
	}
	return nil, err
}

// engineElicit is the session's elicitation handler: it passes the question
// to the session's Elicitor and declines in every case but a clear answer
// given while the session and the call are alive.
type engineElicit struct {
	s *EngineSession
	f Elicitor
}

func (h engineElicit) Elicit(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	decline := &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}}
	if h.f == nil {
		return decline, nil
	}
	ctx, stop := h.s.merge(ctx)
	defer stop()
	type answer struct {
		res *mcp.ElicitationResult
		err error
	}
	done := make(chan answer, 1)
	go func() {
		res, err := h.f(ctx, req)
		done <- answer{res, err}
	}()
	select {
	case <-ctx.Done():
		return decline, nil
	case a := <-done:
		if a.err != nil || a.res == nil || ctx.Err() != nil {
			return decline, nil
		}
		return a.res, nil
	}
}

// merge returns a context that ends with ctx or with the session.
func (s *EngineSession) merge(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// enter registers a call. The returned context ends with ctx or when the
// session closes; done must be called when the call ends.
func (s *EngineSession) enter(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, &Error{Code: CodeUnavailable, Message: "The session is closed."}
	}
	s.calls.Add(1)
	s.mu.Unlock()
	ctx, stop := s.merge(ctx)
	return ctx, func() { stop(); s.calls.Done() }, nil
}

// Instructions is the server's usage text from initialize.
func (s *EngineSession) Instructions() string { return s.instr }

// Warnings are the server's start-up warnings.
func (s *EngineSession) Warnings() []string { return append([]string(nil), s.srv.Warnings...) }

// Tools lists the tools the session's policy serves.
func (s *EngineSession) Tools(ctx context.Context) ([]mcp.Tool, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	var out []mcp.Tool
	req := mcp.ListToolsRequest{}
	for {
		res, err := s.client.ListTools(ctx, req)
		if err != nil {
			return nil, s.wrap(ctx, "Could not list the tools.", err)
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		req.Params.Cursor = res.NextCursor
	}
}

// Call runs a tool; its audit line carries runID. A tool's own failure is
// the result's IsError, not an error. Closing the session cancels the call,
// and a question ffc asks meanwhile is declined.
func (s *EngineSession) Call(ctx context.Context, runID, name string, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	req := mcp.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = name, args
	res, err := s.client.CallTool(cmd.WithRunID(ctx, runID), req)
	if err != nil {
		return nil, s.wrap(ctx, "The tool call failed.", err)
	}
	return res, nil
}

// Classify says what the server's rules make of a call before it is made.
func (s *EngineSession) Classify(name string, args map[string]any) (cmd.ToolClass, error) {
	if err := s.live(); err != nil {
		return cmd.ToolClass{}, err
	}
	cls, err := s.srv.Classify(name, args)
	if err != nil {
		return cmd.ToolClass{}, newError(CodeInvalid, "The tool call is not allowed.", err)
	}
	return cls, nil
}

// begin marks the session closed and cancels it: no new call is accepted
// and the open ones are told to stop. It reports whether this was the first
// time.
func (s *EngineSession) begin() bool {
	s.mu.Lock()
	first := !s.closed
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	return first
}

// Close ends the session: it cancels its calls, waits for them to end, and
// releases the server. It is safe to call more than once; a later call
// waits for the first to finish.
func (s *EngineSession) Close() {
	s.begin()
	s.finishOnce.Do(func() {
		s.calls.Wait()
		s.client.Close()
		s.engine.mu.Lock()
		delete(s.engine.sessions, s)
		s.engine.mu.Unlock()
		s.engine.release(s.es)
		close(s.done)
	})
	<-s.done
}

func (s *EngineSession) live() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return &Error{Code: CodeUnavailable, Message: "The session is closed."}
	}
	return nil
}

func (s *EngineSession) wrap(ctx context.Context, msg string, err error) *Error {
	if ctx.Err() != nil {
		return newError(CodeCancelled, "Cancelled.", ctx.Err())
	}
	return newError(CodeFailed, msg, err)
}
