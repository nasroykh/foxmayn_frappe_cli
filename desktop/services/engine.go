package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// EngineMode is what a session may do to a site.
type EngineMode int

const (
	// EngineRead serves read tools only (Policy.ReadOnly).
	EngineRead EngineMode = iota
	// EngineAsk also serves write tools; ffc asks, through the session's
	// Elicitor, before every call that needs confirmation.
	EngineAsk
)

// engineClientName is the client name in the MCP audit log.
const engineClientName = "foxmayn-desktop"

// Elicitor answers an ffc confirmation question. A nil Elicitor declines
// every question.
type Elicitor func(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error)

// Engine hosts the ffc MCP server in this process: one server per (site,
// mode), built on first use and shared by the sessions that open it. It is
// safe for concurrent use.
type Engine struct {
	configPath string

	startMu sync.Mutex // see startClient
	mu      sync.Mutex
	closed  bool
	servers map[engineKey]*engineServer
}

type engineKey struct {
	site string
	mode EngineMode
}

// engineServer is a cached server. It is built once (ready closes when srv
// or err is set) and closed when it is retired and its last session closed.
type engineServer struct {
	ready   chan struct{}
	srv     *cmd.MCPServer
	err     error
	refs    int
	retired bool
}

// NewEngine returns an engine over the ffc config file at configPath.
func NewEngine(configPath string) *Engine {
	return &Engine{configPath: configPath, servers: map[engineKey]*engineServer{}}
}

// Open starts a session on site. The session's own in-process MCP client
// sends elicitation requests to elicit.
func (e *Engine) Open(ctx context.Context, site string, mode EngineMode, elicit Elicitor) (*EngineSession, error) {
	if site == "" {
		return nil, invalid("site", "Choose a site.")
	}
	es, err := e.acquire(ctx, engineKey{site, mode})
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
func (e *Engine) acquire(ctx context.Context, key engineKey) (*engineServer, error) {
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
		es = &engineServer{ready: make(chan struct{})}
		e.servers[key] = es
	}
	es.refs++
	e.mu.Unlock()

	if build {
		// The build may log in to the site, so it runs outside the lock. It
		// does not follow this caller's ctx: others wait for the same build.
		srv, err := cmd.NewMCPServer(context.WithoutCancel(ctx), cmd.MCPOptions{
			ConfigPath: e.configPath,
			Site:       key.site,
			Sites:      []string{key.site},
			Policy:     config.MCPPolicy{Confirm: "always", ReadOnly: key.mode == EngineRead},
		})
		e.mu.Lock()
		switch {
		case err != nil:
			es.err = err
			if e.servers[key] == es {
				delete(e.servers, key)
			}
		case e.closed:
			es.err = errEngineClosed()
			srv.Close()
		default:
			es.srv = srv
		}
		close(es.ready)
		e.mu.Unlock()
	}

	select {
	case <-es.ready:
	case <-ctx.Done():
		e.release(es)
		return nil, newError(CodeCancelled, "Cancelled.", ctx.Err())
	}
	if es.err != nil {
		e.release(es)
		var se *Error
		if errors.As(es.err, &se) {
			return nil, se
		}
		return nil, newError(CodeFailed, "Could not start the assistant engine for this site.", es.err)
	}
	return es, nil
}

// release drops one reference and closes a retired server nobody uses.
func (e *Engine) release(es *engineServer) {
	e.mu.Lock()
	es.refs--
	var srv *cmd.MCPServer
	if es.retired && es.refs <= 0 {
		srv = es.srv
	}
	e.mu.Unlock()
	srv.Close()
}

// Invalidate retires every cached server: the config changed. Sessions that
// are open keep their server until they close; new Opens build a new one.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	var idle []*cmd.MCPServer
	for k, es := range e.servers {
		delete(e.servers, k)
		es.retired = true
		if es.refs <= 0 && es.srv != nil {
			idle = append(idle, es.srv)
		}
	}
	e.mu.Unlock()
	for _, s := range idle {
		s.Close()
	}
}

// Close closes every server, open sessions included. Later Opens fail.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	var all []*cmd.MCPServer
	for k, es := range e.servers {
		delete(e.servers, k)
		es.retired = true
		if es.srv != nil {
			all = append(all, es.srv)
		}
	}
	e.mu.Unlock()
	for _, s := range all {
		s.Close()
	}
}

func errEngineClosed() *Error {
	return &Error{Code: CodeUnavailable, Message: "The assistant engine is shut down."}
}

// engineElicit adapts an Elicitor to the MCP client's handler.
type engineElicit struct{ f Elicitor }

func (h engineElicit) Elicit(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	if h.f == nil {
		return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}}, nil
	}
	return h.f(ctx, req)
}

// EngineSession is one in-process MCP client on an engine server. It is safe
// for concurrent use.
type EngineSession struct {
	engine *Engine
	es     *engineServer
	srv    *cmd.MCPServer
	client *mcpclient.Client
	instr  string

	mu     sync.Mutex
	closed bool
}

func (e *Engine) newSession(ctx context.Context, es *engineServer, elicit Elicitor) (*EngineSession, error) {
	fail := func(err error) (*EngineSession, error) {
		return nil, newError(CodeFailed, "Could not start the assistant engine.", err)
	}
	c, err := e.startClient(ctx, es.srv, elicit)
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
	return &EngineSession{engine: e, es: es, srv: es.srv, client: c, instr: res.Instructions}, nil
}

// startClient connects a new in-process client. mcp-go names the client's
// session after the clock's nanoseconds, so two clients started within one
// clock tick (coarse on Windows) collide: starts are serialised and a
// collision is retried on a fresh id.
func (e *Engine) startClient(ctx context.Context, srv *cmd.MCPServer, elicit Elicitor) (*mcpclient.Client, error) {
	e.startMu.Lock()
	defer e.startMu.Unlock()
	var err error
	for range 50 {
		var c *mcpclient.Client
		c, err = mcpclient.NewInProcessClientWithOptions(srv.MCPServer, mcpclient.WithElicitationHandler(engineElicit{elicit}))
		if err != nil {
			return nil, err
		}
		if err = c.Start(ctx); err == nil {
			return c, nil
		}
		c.Close()
		if !strings.Contains(err.Error(), "session already exists") {
			return nil, err
		}
		time.Sleep(time.Millisecond)
	}
	return nil, err
}

// Instructions is the server's usage text from initialize.
func (s *EngineSession) Instructions() string { return s.instr }

// Warnings are the server's start-up warnings.
func (s *EngineSession) Warnings() []string { return append([]string(nil), s.srv.Warnings...) }

// Tools lists the tools the session's policy serves.
func (s *EngineSession) Tools(ctx context.Context) ([]mcp.Tool, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
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
// the result's IsError, not an error.
func (s *EngineSession) Call(ctx context.Context, runID, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
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

// Close ends the session. It is safe to call more than once.
func (s *EngineSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.client.Close()
	s.engine.release(s.es)
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
	if e := s.live(); e != nil {
		return e.(*Error)
	}
	return newError(CodeFailed, msg, err)
}
