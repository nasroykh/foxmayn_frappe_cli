package services

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// engineSite is a fake site with three ToDos and a config that holds it as
// "prod". The audit log is next to the config, so it is in the temp dir.
func engineSite(t *testing.T) (*Engine, *frappetest.Site, string) {
	t.Helper()
	fake := frappetest.New(t)
	fake.Add("ToDo",
		map[string]any{"name": "TD-1", "description": "alpha", "status": "Open"},
		map[string]any{"name": "TD-2", "description": "beta", "status": "Closed"},
		map[string]any{"name": "TD-3", "description": "gamma", "status": "Open"},
	)
	s, _, path := newSites(t)
	if _, err := s.AddWithAPIKey(context.Background(), APIKeyRequest{Name: "prod", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(path)
	t.Cleanup(e.Close)
	return e, fake, path
}

func engineAudit(t *testing.T, configPath string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(filepath.Dir(configPath), "mcp-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func toolNames(t *testing.T, s *EngineSession) map[string]bool {
	t.Helper()
	tools, err := s.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	return names
}

func TestEngineAuditsClientAndRunID(t *testing.T) {
	e, _, path := engineSite(t)
	s, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Instructions() == "" {
		t.Error("no instructions")
	}
	res, err := s.Call(t.Context(), "run-42", "get_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"})
	if err != nil || res.IsError {
		t.Fatalf("get_doc: %v %v", err, res)
	}
	lines := engineAudit(t, path)
	if len(lines) != 1 || lines[0]["client"] != "foxmayn-desktop" || lines[0]["run_id"] != "run-42" || lines[0]["tool"] != "get_doc" {
		t.Errorf("audit = %v", lines)
	}
}

func TestEngineAskElicits(t *testing.T) {
	del := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	for _, tc := range []struct {
		action  mcp.ElicitationResponseAction
		confirm bool
		deleted bool
	}{
		{mcp.ElicitationResponseActionDecline, false, false},
		{mcp.ElicitationResponseActionAccept, true, true},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			e, fake, _ := engineSite(t)
			var asked atomic.Int32
			var msg string
			s, err := e.Open(t.Context(), "prod", EngineAsk, func(_ context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
				asked.Add(1)
				msg = req.Params.Message
				return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
					Action: tc.action, Content: map[string]any{"confirm": tc.confirm},
				}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			cls, err := s.Classify("delete_doc", del)
			if err != nil || !cls.WillAsk {
				t.Fatalf("classify = %+v, %v", cls, err)
			}
			if _, err := s.Call(t.Context(), "r1", "delete_doc", del); err != nil {
				t.Fatal(err)
			}
			if asked.Load() != 1 || !strings.Contains(msg, "TD-1") {
				t.Errorf("asked %d times: %q", asked.Load(), msg)
			}
			if _, ok := fake.Doc("ToDo", "TD-1"); ok == tc.deleted {
				t.Errorf("TD-1 exists = %v, want %v", ok, !tc.deleted)
			}
		})
	}
}

func TestEngineNilElicitorDeclines(t *testing.T) {
	e, fake, path := engineSite(t)
	s, err := e.Open(t.Context(), "prod", EngineAsk, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Call(t.Context(), "r1", "delete_doc", map[string]any{"doctype": "ToDo", "name": "TD-2"})
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(res); !res.IsError || !strings.Contains(text, "nothing was changed") {
		t.Errorf("result = %v %q", res.IsError, text)
	}
	if _, ok := fake.Doc("ToDo", "TD-2"); !ok {
		t.Error("TD-2 was deleted without an answer")
	}
	if got := auditStatuses(t, path); got != "confirm_pending,declined" {
		t.Errorf("audit statuses = %s", got)
	}
}

func auditStatuses(t *testing.T, path string) string {
	t.Helper()
	var out []string
	for _, l := range engineAudit(t, path) {
		out = append(out, l["status"].(string))
	}
	return strings.Join(out, ",")
}

// Every way an Elicitor can fail to say yes keeps the document.
func TestEngineElicitorFailuresDecline(t *testing.T) {
	accept := &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
		Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"confirm": true},
	}}
	for name, tc := range map[string]struct {
		elicit Elicitor
		cancel bool // cancel the call's context while it is asked
	}{
		"error": {elicit: func(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
			return nil, errors.New("boom")
		}},
		"nil result": {elicit: func(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
			return nil, nil
		}},
		"cancelled": {cancel: true, elicit: func(ctx context.Context, _ mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
			<-ctx.Done()
			return accept, nil // a late yes
		}},
	} {
		t.Run(name, func(t *testing.T) {
			e, fake, path := engineSite(t)
			asked := make(chan struct{}, 1)
			el := tc.elicit
			s, err := e.Open(t.Context(), "prod", EngineAsk, func(ctx context.Context, r mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
				asked <- struct{}{}
				return el(ctx, r)
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				go func() { <-asked; cancel() }()
			}
			res, err := s.Call(ctx, "r1", "delete_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"})
			if _, ok := fake.Doc("ToDo", "TD-1"); !ok {
				t.Error("TD-1 was deleted")
			}
			if tc.cancel {
				return
			}
			select {
			case <-asked:
			default:
				t.Error("the Elicitor was not asked")
			}
			if err != nil || !strings.Contains(resultText(res), "nothing was changed") {
				t.Errorf("result = %v, %v", res, err)
			}
			if got := auditStatuses(t, path); got != "confirm_pending,declined" {
				t.Errorf("audit statuses = %s", got)
			}
		})
	}
}

// Sessions on one server each ask only their own Elicitor.
func TestEngineSessionsAskTheirOwnElicitor(t *testing.T) {
	e, fake, _ := engineSite(t)
	answer := func(n *atomic.Int32, yes bool) Elicitor {
		return func(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
			n.Add(1)
			act := mcp.ElicitationResponseActionDecline
			if yes {
				act = mcp.ElicitationResponseActionAccept
			}
			return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: act, Content: map[string]any{"confirm": yes}}}, nil
		}
	}
	var na, nb atomic.Int32
	a, err := e.Open(t.Context(), "prod", EngineAsk, answer(&na, false))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := e.Open(t.Context(), "prod", EngineAsk, answer(&nb, true))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.srv != b.srv {
		t.Fatal("the sessions have two servers")
	}
	a.Call(t.Context(), "ra", "delete_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"})
	b.Call(t.Context(), "rb", "delete_doc", map[string]any{"doctype": "ToDo", "name": "TD-2"})
	if na.Load() != 1 || nb.Load() != 1 {
		t.Errorf("asked a=%d b=%d", na.Load(), nb.Load())
	}
	if _, ok := fake.Doc("ToDo", "TD-1"); !ok {
		t.Error("a declined, TD-1 gone")
	}
	if _, ok := fake.Doc("ToDo", "TD-2"); ok {
		t.Error("b accepted, TD-2 still there")
	}
}

// Closing a session or the engine while a question is open ends the call
// before Close returns, and a yes given afterwards deletes nothing.
func TestEngineCloseDuringElicitation(t *testing.T) {
	for _, viaEngine := range []bool{false, true} {
		name := map[bool]string{false: "session", true: "engine"}[viaEngine]
		t.Run(name, func(t *testing.T) {
			e, fake, path := engineSite(t)
			asked, release := make(chan struct{}), make(chan struct{})
			s, err := e.Open(t.Context(), "prod", EngineAsk, func(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
				close(asked)
				<-release // ignores its context, then says yes
				return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
					Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"confirm": true},
				}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			callDone := make(chan struct{})
			go func() {
				defer close(callDone)
				s.Call(t.Context(), "r1", "delete_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"})
			}()
			<-asked
			closeDone := make(chan struct{})
			go func() {
				defer close(closeDone)
				if viaEngine {
					e.Close()
				} else {
					s.Close()
				}
			}()
			select {
			case <-closeDone:
			case <-time.After(10 * time.Second):
				t.Fatal("Close did not return while the Elicitor was blocked")
			}
			select {
			case <-callDone:
			case <-time.After(10 * time.Second):
				t.Error("the call did not end")
			}
			close(release)
			time.Sleep(50 * time.Millisecond)
			if _, ok := fake.Doc("ToDo", "TD-1"); !ok {
				t.Error("TD-1 was deleted after Close")
			}
			if got := auditStatuses(t, path); strings.Contains(got, "ok") {
				t.Errorf("audit statuses = %s", got)
			}
		})
	}
}

func TestEngineReadListsNoWriteTools(t *testing.T) {
	e, _, _ := engineSite(t)
	r, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, err := e.Open(t.Context(), "prod", EngineAsk, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	read, ask := toolNames(t, r), toolNames(t, a)
	if !read["get_doc"] || !read["list_docs"] {
		t.Errorf("read tools = %v", read)
	}
	for _, w := range []string{"create_doc", "update_doc", "delete_doc", "call_method"} {
		if read[w] {
			t.Errorf("read mode lists %s", w)
		}
		if !ask[w] {
			t.Errorf("ask mode lacks %s", w)
		}
	}
}

func TestEngineJQ(t *testing.T) {
	e, _, _ := engineSite(t)
	s, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Call(t.Context(), "r1", "list_docs", map[string]any{
		"doctype": "ToDo", "fields": []string{"name"}, "order_by": "name asc", "jq": "map(.name)",
	})
	if err != nil || res.IsError {
		t.Fatalf("list_docs: %v %v", err, res)
	}
	var names []string
	if err := json.Unmarshal([]byte(resultText(res)), &names); err != nil || len(names) != 3 || names[0] != "TD-1" {
		t.Errorf("jq result = %q (%v)", resultText(res), err)
	}
}

func TestEngineClosedSessionAndEngine(t *testing.T) {
	e, _, _ := engineSite(t)
	s, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s.Close()
	get := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	if _, err := s.Call(t.Context(), "r", "get_doc", get); code(err) != CodeUnavailable {
		t.Errorf("Call after Close: %v", err)
	}
	if _, err := s.Tools(t.Context()); code(err) != CodeUnavailable {
		t.Errorf("Tools after Close: %v", err)
	}
	if _, err := s.Classify("get_doc", get); code(err) != CodeUnavailable {
		t.Errorf("Classify after Close: %v", err)
	}

	e.Close()
	e.Close()
	if _, err := e.Open(t.Context(), "prod", EngineRead, nil); code(err) != CodeUnavailable {
		t.Errorf("Open after Engine.Close: %v", err)
	}
}

func TestEngineEngineCloseEndsOpenSession(t *testing.T) {
	e, _, _ := engineSite(t)
	s, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, err := s.Call(t.Context(), "r", "get_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"}); code(err) != CodeUnavailable {
		t.Errorf("call after Engine.Close: %v", err)
	}
	s.Close()
}

func TestEngineOpenErrors(t *testing.T) {
	e, _, _ := engineSite(t)
	if _, err := e.Open(t.Context(), "", EngineRead, nil); code(err) != CodeInvalid {
		t.Errorf("empty site: %v", err)
	}
	if _, err := e.Open(t.Context(), "prod", EngineMode(7), nil); code(err) != CodeInvalid {
		t.Errorf("unknown mode: %v", err)
	}
	if _, err := e.Open(t.Context(), "nope", EngineRead, nil); err == nil {
		t.Error("unknown site opened")
	}
	// A failed build is not cached.
	if _, err := e.Open(t.Context(), "nope", EngineRead, nil); err == nil {
		t.Error("unknown site opened the second time")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Open(ctx, "prod", EngineRead, nil); err == nil {
		t.Error("opened with a cancelled context")
	}
}

func TestEngineInvalidate(t *testing.T) {
	e, _, _ := engineSite(t)
	get := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	old, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	if old.srv != same.srv {
		t.Error("two Opens built two servers")
	}
	e.Invalidate()
	fresh, err := e.Open(t.Context(), "prod", EngineRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if fresh.srv == old.srv {
		t.Error("Open after Invalidate reused the old server")
	}
	// The old server serves its sessions until the last one closes.
	if res, err := old.Call(t.Context(), "r", "get_doc", get); err != nil || res.IsError {
		t.Fatalf("old session after Invalidate: %v %v", err, res)
	}
	old.Close()
	if res, err := same.Call(t.Context(), "r", "get_doc", get); err != nil || res.IsError {
		t.Fatalf("second old session: %v %v", err, res)
	}
	same.Close()
	if res, err := fresh.Call(t.Context(), "r", "get_doc", get); err != nil || res.IsError {
		t.Fatalf("new session: %v %v", err, res)
	}
}

// Opens, Invalidates, cancels and Engine.Close race; every server built is
// closed exactly once.
func TestEngineConcurrent(t *testing.T) {
	e, _, _ := engineSite(t)
	var mu sync.Mutex
	closes := map[*cmd.MCPServer]int{}
	e.closeSrv = func(s *cmd.MCPServer) {
		mu.Lock()
		closes[s]++
		mu.Unlock()
		s.Close()
	}
	seen := map[*cmd.MCPServer]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch i % 6 {
			case 3:
				e.Invalidate()
			case 4:
				cancel() // ends the wait for a build
			case 5:
				if i > 18 {
					e.Close()
				}
			}
			s, err := e.Open(ctx, "prod", EngineMode(i%2), nil)
			if err != nil {
				return
			}
			mu.Lock()
			seen[s.srv] = true
			mu.Unlock()
			s.Tools(ctx)
			s.Close()
		}()
	}
	wg.Wait()
	e.Close()
	e.Invalidate()
	mu.Lock()
	defer mu.Unlock()
	for srv := range seen {
		if closes[srv] != 1 {
			t.Errorf("a server was closed %d times", closes[srv])
		}
	}
	for srv, n := range closes {
		if n != 1 {
			t.Errorf("server %p closed %d times", srv, n)
		}
	}
}

// A retired server is closed exactly once, on its last session's close.
func TestEngineRetiredServerClosedOnce(t *testing.T) {
	e, _, _ := engineSite(t)
	var n atomic.Int32
	e.closeSrv = func(s *cmd.MCPServer) { n.Add(1); s.Close() }
	a, _ := e.Open(t.Context(), "prod", EngineRead, nil)
	b, _ := e.Open(t.Context(), "prod", EngineRead, nil)
	e.Invalidate()
	e.Invalidate()
	a.Close()
	if n.Load() != 0 {
		t.Fatalf("closed with a session open: %d", n.Load())
	}
	b.Close()
	b.Close()
	e.Close()
	if n.Load() != 1 {
		t.Errorf("closed %d times", n.Load())
	}
}

// Engine.Close with two questions open: both sessions are shut at once (a
// new call is refused, a yes given late deletes nothing) and Close returns
// only after both calls ended.
func TestEngineCloseTwoSessions(t *testing.T) {
	e, fake, _ := engineSite(t)
	release := make(chan struct{})
	asked := make(chan struct{}, 2)
	elicit := func(context.Context, mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
		asked <- struct{}{}
		<-release
		return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
			Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"confirm": true},
		}}, nil
	}
	var sess [2]*EngineSession
	var callDone [2]chan struct{}
	for i := range sess {
		s, err := e.Open(t.Context(), "prod", EngineAsk, elicit)
		if err != nil {
			t.Fatal(err)
		}
		sess[i], callDone[i] = s, make(chan struct{})
		name := []string{"TD-1", "TD-2"}[i]
		go func() {
			defer close(callDone[i])
			s.Call(t.Context(), "r", "delete_doc", map[string]any{"doctype": "ToDo", "name": name})
		}()
	}
	<-asked
	<-asked
	closeDone := make(chan struct{})
	go func() { defer close(closeDone); e.Close() }()
	// Once the sessions are shut a new call is refused.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := sess[1].Call(t.Context(), "r2", "get_doc", map[string]any{"doctype": "ToDo", "name": "TD-3"})
		if code(err) == CodeUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a new call was accepted during Close: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	close(release) // late yes on both
	select {
	case <-closeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Engine.Close did not return")
	}
	for i := range callDone {
		select {
		case <-callDone[i]:
		case <-time.After(10 * time.Second):
			t.Errorf("call %d did not end", i)
		}
	}
	for _, n := range []string{"TD-1", "TD-2"} {
		if _, ok := fake.Doc("ToDo", n); !ok {
			t.Errorf("%s was deleted", n)
		}
	}
}

// Engine.Close waits for a session.Close that is already running, and a
// second Close of either waits for the first.
func TestEngineCloseWaitsForSessionClose(t *testing.T) {
	e, _, _ := engineSite(t)
	s, err := e.Open(t.Context(), "prod", EngineAsk, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the session's close open: a call that ignores cancellation is
	// simulated by registering one directly.
	_, done, err := s.enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sessionClosed, engineClosed := make(chan struct{}), make(chan struct{})
	go func() { defer close(sessionClosed); s.Close() }()
	for s.live() == nil { // Close has begun
		time.Sleep(time.Millisecond)
	}
	go func() { defer close(engineClosed); e.Close() }()
	select {
	case <-engineClosed:
		t.Fatal("Engine.Close returned while a session close was still waiting")
	case <-sessionClosed:
		t.Fatal("session Close returned with a call in flight")
	case <-time.After(200 * time.Millisecond):
	}
	second := make(chan struct{})
	go func() { defer close(second); e.Close() }()
	select {
	case <-second:
		t.Fatal("a second Engine.Close returned early")
	case <-time.After(100 * time.Millisecond):
	}
	done()
	for _, c := range []chan struct{}{sessionClosed, engineClosed, second} {
		select {
		case <-c:
		case <-time.After(10 * time.Second):
			t.Fatal("a Close did not return after the call ended")
		}
	}
}
