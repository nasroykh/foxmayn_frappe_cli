package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
)

const testKey = "sk-ant-TESTKEY-0123456789"

// captured is what the fake server saw on the last request.
type captured struct {
	mu     sync.Mutex
	header http.Header
	body   []byte
	path   string
	url    string
}

func (c *captured) set(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.header, c.body, c.path = r.Header.Clone(), b, r.URL.Path
}

func (c *captured) get() (http.Header, []byte, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header, c.body, c.path
}

func newProvider(t *testing.T, h http.Handler) (*Provider, *captured) {
	t.Helper()
	// The adapter must ignore the caller's ANTHROPIC_* environment.
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token-must-not-be-sent")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("ANTHROPIC_API_KEY", "env-key-must-not-be-sent")
	t.Setenv("ANTHROPIC_PROFILE", "env-profile-that-does-not-exist")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Api-Key: env-leak\nX-Env-Leak: secret")
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.set(r)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cap.url = srv.URL
	return New(testKey, WithBaseURL(srv.URL), WithMaxRetries(0)), cap
}

func serveFile(t *testing.T, name string) http.Handler {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	})
}

func collect(t *testing.T, s llm.Stream) []llm.Event {
	t.Helper()
	defer s.Close()
	var out []llm.Event
	for {
		ev, err := s.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v (events so far %#v)", err, out)
		}
		out = append(out, ev)
	}
}

func run(t *testing.T, file string) ([]llm.Event, *captured) {
	t.Helper()
	p, cap := newProvider(t, serveFile(t, file))
	s, err := p.Stream(context.Background(), llm.Request{
		Model:    DefaultModel,
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, s), cap
}

func TestStreamText(t *testing.T) {
	got, cap := run(t, "text.sse")
	want := []llm.Event{
		llm.TextDelta{Text: "Hello"},
		llm.TextDelta{Text: ", world"},
		llm.Usage{In: 25, Out: 6},
		llm.Stop{Reason: "end_turn", Message: asst(llm.Text{Text: "Hello, world"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
	h, _, path := cap.get()
	if path != "/v1/messages" {
		t.Fatalf("path %q", path)
	}
	if h.Get("x-api-key") != testKey {
		t.Fatalf("x-api-key %q", h.Get("x-api-key"))
	}
}

func TestStreamToolUseSplitJSON(t *testing.T) {
	got, _ := run(t, "tool_split.sse")
	if len(got) != 4 {
		t.Fatalf("events %#v", got)
	}
	if got[0] != (llm.TextDelta{Text: "Looking it up."}) {
		t.Fatalf("first %#v", got[0])
	}
	tc, ok := got[1].(llm.ToolCall)
	if !ok || tc.ID != "toolu_A" || tc.Name != "get_doc" {
		t.Fatalf("tool call %#v", got[1])
	}
	var args map[string]string
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("args %s: %v", tc.Args, err)
	}
	if args["doctype"] != "Sales Invoice" || args["name"] != "SINV-0001" {
		t.Fatalf("args %v", args)
	}
	if got[2] != (llm.Usage{In: 100, Out: 42}) || !reflect.DeepEqual(got[3], llm.Stop{Reason: "tool_use", Message: asst(
		llm.Text{Text: "Looking it up."},
		llm.ToolUse{ID: "toolu_A", Name: "get_doc", Args: tc.Args},
	)}) {
		t.Fatalf("tail %#v", got[2:])
	}
}

func TestStreamTwoToolUses(t *testing.T) {
	got, _ := run(t, "two_tools.sse")
	var calls []llm.ToolCall
	for _, ev := range got {
		if tc, ok := ev.(llm.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("events %#v", got)
	}
	if calls[0].ID != "toolu_1" || calls[0].Name != "list_docs" || string(calls[0].Args) != `{"doctype":"Item"}` {
		t.Fatalf("call 0 %#v", calls[0])
	}
	// No input_json_delta at all: the assembled args are "{}".
	if calls[1].ID != "toolu_2" || calls[1].Name != "whoami" || string(calls[1].Args) != "{}" {
		t.Fatalf("call 1 %#v (%s)", calls[1], calls[1].Args)
	}
}

func TestStreamUsageWithCache(t *testing.T) {
	got, _ := run(t, "usage_cache.sse")
	want := []llm.Event{
		llm.TextDelta{Text: "ok"},
		llm.Usage{In: 312, Out: 77, Cached: 4096, CacheWrite: 300},
		llm.Stop{Reason: "max_tokens", Message: asst(llm.Text{Text: "ok"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func TestStreamErrorStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		auth   bool
		rate   bool
	}{
		{401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, true, false},
		// A server that echoes the key back must not leak it through the error.
		{401, `{"type":"error","error":{"type":"authentication_error","message":"bad key ` + testKey + `"}}`, true, false},
		{429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, false, true},
		{500, `not json at all ` + testKey, false, false},
	}
	for _, c := range cases {
		p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, err := p.Stream(context.Background(), llm.Request{Model: DefaultModel})
		var ae *llm.APIError
		if !errors.As(err, &ae) || ae.Status != c.status {
			t.Fatalf("status %d: got %v", c.status, err)
		}
		if llm.IsAuth(err) != c.auth || llm.IsRateLimit(err) != c.rate {
			t.Fatalf("status %d: auth=%v rate=%v", c.status, llm.IsAuth(err), llm.IsRateLimit(err))
		}
		if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "env-") {
			t.Fatalf("key leaked in %q", err.Error())
		}
		if ae.Message == "" {
			t.Fatalf("empty message for %d", c.status)
		}
	}
}

func TestStreamCancelMidStream(t *testing.T) {
	started := make(chan struct{})
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := p.Stream(ctx, llm.Request{Model: DefaultModel})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	<-started
	done := make(chan error, 1)
	go func() {
		_, err := s.Next()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	t0 := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if d := time.Since(t0); d > time.Second {
			t.Fatalf("took %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not return within 1s of cancel")
	}
}

func TestStreamBadToolArgs(t *testing.T) {
	got, _ := run(t, "args_bad.sse")
	var calls []llm.ToolCall
	for _, ev := range got {
		if tc, ok := ev.(llm.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	if len(calls) != 4 {
		t.Fatalf("events %#v", got)
	}
	// The good call first, then the held ones in stream order, then the open block.
	if calls[0].ID != "toolu_ok" || calls[0].ArgsError != "" || string(calls[0].Args) != "{}" {
		t.Fatalf("good call %#v", calls[0])
	}
	bad, arr, open := calls[1], calls[2], calls[3]
	if bad.ID != "toolu_bad" || bad.ArgsError == "" || string(bad.Args) != "{}" || !strings.Contains(bad.ArgsError, `"doctype": "Item"`) {
		t.Fatalf("bad call %#v", bad)
	}
	if arr.ID != "toolu_arr" || arr.ArgsError == "" || string(arr.Args) != "[1, 2]" {
		t.Fatalf("array call %#v", arr)
	}
	if open.ID != "toolu_open" || open.ArgsError == "" || string(open.Args) != "{}" {
		t.Fatalf("open call %#v", open)
	}
	for _, c := range calls {
		if !json.Valid(c.Args) {
			t.Fatalf("Args must always be valid JSON: %#v", c)
		}
	}
	if last := stopOf(t, got); last.Reason != llm.StopToolUse || len(last.Message.Parts) != 4 {
		t.Fatalf("last %#v", last)
	}
}

func TestStreamTruncatedToolArgsAfterMaxTokens(t *testing.T) {
	got, _ := run(t, "args_truncated.sse")
	want := []llm.Event{
		llm.TextDelta{Text: "Creating it."},
		llm.Usage{In: 5, Out: 100},
		llm.Stop{Reason: llm.StopMaxTokens, Message: asst(llm.Text{Text: "Creating it."})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func TestStreamThinkingAndToolUse(t *testing.T) {
	got, _ := run(t, "thinking_tool.sse")
	want := []llm.Event{
		llm.Thinking{Provider: llm.ProviderAnthropic, Text: "I should read the invoice.", Signature: "EqQBCgIYAhIM1234"},
		llm.TextDelta{Text: "Checking."},
		llm.ToolCall{ID: "toolu_T", Name: "get_doc", Args: json.RawMessage(`{"doctype":"Item"}`)},
		llm.Usage{In: 50, Out: 60},
		llm.Stop{Reason: llm.StopToolUse, Message: asst(
			llm.Thinking{Provider: llm.ProviderAnthropic, Text: "I should read the invoice.", Signature: "EqQBCgIYAhIM1234"},
			llm.Text{Text: "Checking."},
			llm.ToolUse{ID: "toolu_T", Name: "get_doc", Args: json.RawMessage(`{"doctype":"Item"}`)},
		)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func TestStreamRedactedThinking(t *testing.T) {
	got, _ := run(t, "redacted.sse")
	want := []llm.Event{
		llm.Thinking{Provider: llm.ProviderAnthropic, Redacted: true, Data: "EmwKAhgBEgy3va3pzix/LafPsn4"},
		llm.TextDelta{Text: "Done."},
		llm.Usage{In: 5, Out: 9},
		llm.Stop{Reason: llm.StopEndTurn, Message: asst(llm.Thinking{Provider: llm.ProviderAnthropic, Redacted: true, Data: "EmwKAhgBEgy3va3pzix/LafPsn4"}, llm.Text{Text: "Done."})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func TestStreamRefusalCategory(t *testing.T) {
	got, _ := run(t, "refusal.sse")
	if last := stopOf(t, got); last.Reason != llm.StopRefusal || last.Category != "cyber" || len(last.Message.Parts) != 0 {
		t.Fatalf("events %#v", got)
	}
}

func TestStreamMidStreamErrorEvent(t *testing.T) {
	p, _ := newProvider(t, serveFile(t, "error_overloaded.sse"))
	s, err := p.Stream(context.Background(), llm.Request{Model: DefaultModel})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ev, err := s.Next(); err != nil || ev != (llm.TextDelta{Text: "partial"}) {
		t.Fatalf("first: %#v %v", ev, err)
	}
	_, err = s.Next()
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Status != 529 || ae.Message != "Overloaded" {
		t.Fatalf("err = %v", err)
	}
	if !llm.IsRetryable(err) || llm.IsRateLimit(err) || llm.IsAuth(err) {
		t.Fatalf("classification of %v", err)
	}
}

func TestStatusFromType(t *testing.T) {
	for typ, want := range map[string]int{
		"overloaded_error": 529, "rate_limit_error": 429, "api_error": 500, "invalid_request_error": 0, "": 0,
	} {
		if got := statusFromType(typ); got != want {
			t.Errorf("%q: %d, want %d", typ, got, want)
		}
	}
}

func TestEnvCustomHeadersNotSent(t *testing.T) {
	_, cap := newProvider(t, serveFile(t, "text.sse"))
	// The SDK reads these only when no env credential is set.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_PROFILE", "")
	// An X-Api-Key line once made the adapter delete its own key header.
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Env-Leak: secret\nX-Other: 1\nX-Api-Key: env-leak")
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	p2 := New(testKey, WithBaseURL(cap.url), WithMaxRetries(0))
	s, err := p2.Stream(context.Background(), llm.Request{Model: DefaultModel})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	h, _, _ := cap.get()
	if h.Get("X-Env-Leak") != "" || h.Get("X-Other") != "" {
		t.Fatalf("env headers sent: %v", h)
	}
	if h.Get("x-api-key") != testKey {
		t.Fatalf("x-api-key %q", h.Get("x-api-key"))
	}
}

func TestStreamEndsWithoutStop(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
	}))
	s, err := p.Stream(context.Background(), llm.Request{Model: DefaultModel})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Next(); err == nil || err == io.EOF {
		t.Fatalf("truncated stream must be an error, got %v", err)
	}
}

func TestRequestBody(t *testing.T) {
	p, cap := newProvider(t, serveFile(t, "text.sse"))
	req := llm.Request{
		Model:     "claude-opus-5-5",
		System:    "You are helpful.",
		MaxTokens: 1234,
		Tools: []llm.Tool{
			{Name: "get_doc", Description: "Read one document.", InputSchema: json.RawMessage(
				`{"type":"object","properties":{"doctype":{"type":"string"},"name":{"type":"string"}},"required":["doctype"],"additionalProperties":false}`)},
			{Name: "whoami", Description: "Who am I.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "show SINV-1"}}},
			{Role: llm.RoleAssistant, Parts: []llm.Part{
				llm.Text{Text: "Reading."},
				llm.ToolUse{ID: "toolu_a", Name: "get_doc", Args: json.RawMessage(`{"doctype":"Sales Invoice","name":"SINV-1"}`)},
				llm.ToolUse{ID: "toolu_b", Name: "whoami"},
			}},
			// Text before the results in Parts; the API wants results first.
			{Role: llm.RoleUser, Parts: []llm.Part{
				llm.Text{Text: "continue"},
				llm.ToolResult{ID: "toolu_a", Text: "{\"grand_total\": 10}"},
				llm.ToolResult{ID: "toolu_b", Text: "denied", IsError: true},
			}},
			{Role: llm.RoleAssistant}, // empty: dropped
		},
	}
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)

	h, raw, _ := cap.get()
	if h.Get("x-api-key") != testKey {
		t.Fatalf("x-api-key = %q", h.Get("x-api-key"))
	}
	if h.Get("Authorization") != "" {
		t.Fatalf("Authorization sent: %q", h.Get("Authorization"))
	}
	for k, v := range h {
		if k != "X-Api-Key" && strings.Contains(strings.Join(v, ","), testKey) {
			t.Fatalf("key in header %s", k)
		}
	}
	if strings.Contains(string(raw), testKey) {
		t.Fatal("key in request body")
	}

	var body struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
		// Automatic caching of the conversation tail.
		CacheControl map[string]any `json:"cache_control"`
		System       []struct {
			Type         string         `json:"type"`
			Text         string         `json:"text"`
			CacheControl map[string]any `json:"cache_control"`
		} `json:"system"`
		Tools []struct {
			Name         string         `json:"name"`
			Description  string         `json:"description"`
			InputSchema  map[string]any `json:"input_schema"`
			CacheControl map[string]any `json:"cache_control"`
		} `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
				IsError   bool            `json:"is_error"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if body.Model != "claude-opus-5-5" || body.MaxTokens != 1234 || !body.Stream {
		t.Fatalf("top level %+v", body)
	}
	if body.CacheControl["type"] != "ephemeral" {
		t.Fatalf("top-level cache_control %v", body.CacheControl)
	}
	// system + last tool + tail = 3 of the 4 breakpoints the API allows.
	if n := strings.Count(string(raw), `"cache_control"`); n != 3 {
		t.Fatalf("%d cache_control breakpoints in %s", n, raw)
	}
	if len(body.System) != 1 || body.System[0].Text != "You are helpful." || body.System[0].CacheControl["type"] != "ephemeral" {
		t.Fatalf("system %+v", body.System)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("tools %+v", body.Tools)
	}
	if body.Tools[0].CacheControl != nil {
		t.Fatal("only the last tool carries cache_control")
	}
	if body.Tools[1].CacheControl["type"] != "ephemeral" {
		t.Fatalf("last tool cache_control %v", body.Tools[1].CacheControl)
	}
	sch := body.Tools[0].InputSchema
	if sch["type"] != "object" || sch["additionalProperties"] != false {
		t.Fatalf("schema %v", sch)
	}
	if props, _ := sch["properties"].(map[string]any); len(props) != 2 {
		t.Fatalf("properties %v", sch["properties"])
	}
	if req, _ := sch["required"].([]any); len(req) != 1 || req[0] != "doctype" {
		t.Fatalf("required %v", sch["required"])
	}
	if body.Tools[1].InputSchema["type"] != "object" {
		t.Fatalf("empty schema %v", body.Tools[1].InputSchema)
	}

	if len(body.Messages) != 3 {
		t.Fatalf("messages %d: %s", len(body.Messages), raw)
	}
	asst := body.Messages[1]
	if asst.Role != "assistant" || len(asst.Content) != 3 {
		t.Fatalf("assistant %+v", asst)
	}
	if asst.Content[0].Type != "text" || asst.Content[1].Type != "tool_use" || asst.Content[1].ID != "toolu_a" ||
		asst.Content[1].Name != "get_doc" || !strings.Contains(string(asst.Content[1].Input), `"SINV-1"`) {
		t.Fatalf("assistant content %+v", asst.Content)
	}
	if string(asst.Content[2].Input) != "{}" {
		t.Fatalf("empty args must serialize as {}, got %s", asst.Content[2].Input)
	}
	user := body.Messages[2]
	if user.Role != "user" || len(user.Content) != 3 {
		t.Fatalf("user %+v", user)
	}
	r0, r1, tx := user.Content[0], user.Content[1], user.Content[2]
	if r0.Type != "tool_result" || r0.ToolUseID != "toolu_a" || r0.IsError || len(r0.Content) != 1 || r0.Content[0].Text != "{\"grand_total\": 10}" {
		t.Fatalf("result a %+v", r0)
	}
	if r1.Type != "tool_result" || r1.ToolUseID != "toolu_b" || !r1.IsError {
		t.Fatalf("result b %+v", r1)
	}
	if tx.Type != "text" || tx.Text != "continue" {
		t.Fatalf("text after results %+v", tx)
	}
}

// sentBlocks sends req and returns the sent messages as role plus raw blocks.
func sentMessages(t *testing.T, req llm.Request) []struct {
	Role    string
	Content []map[string]any
} {
	t.Helper()
	p, cap := newProvider(t, serveFile(t, "text.sse"))
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	_, raw, _ := cap.get()
	var body struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	out := make([]struct {
		Role    string
		Content []map[string]any
	}, len(body.Messages))
	for i, m := range body.Messages {
		out[i].Role, out[i].Content = m.Role, m.Content
	}
	return out
}

func TestRequestReplaysThinkingVerbatim(t *testing.T) {
	msgs := sentMessages(t, llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "go"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{
			llm.Thinking{Text: "think one", Signature: "SIG1"},
			llm.Text{Text: "reading"},
			llm.Thinking{Redacted: true, Data: "REDACTED2"},
			llm.ToolUse{ID: "toolu_x", Name: "get_doc", Args: json.RawMessage(`{"a":1}`)},
		}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{ID: "toolu_x", Text: "ok"}}},
	}})
	if len(msgs) != 3 {
		t.Fatalf("messages %+v", msgs)
	}
	c := msgs[1].Content
	if len(c) != 4 {
		t.Fatalf("assistant content %+v", c)
	}
	if c[0]["type"] != "thinking" || c[0]["thinking"] != "think one" || c[0]["signature"] != "SIG1" {
		t.Fatalf("thinking block %v", c[0])
	}
	if c[1]["type"] != "text" || c[1]["text"] != "reading" {
		t.Fatalf("text block %v", c[1])
	}
	if c[2]["type"] != "redacted_thinking" || c[2]["data"] != "REDACTED2" {
		t.Fatalf("redacted block %v", c[2])
	}
	if c[3]["type"] != "tool_use" || c[3]["id"] != "toolu_x" {
		t.Fatalf("tool_use block %v", c[3])
	}
}

func TestRequestMergesSameRoleMessages(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "go"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{llm.ToolUse{ID: "toolu_1", Name: "a"}, llm.ToolUse{ID: "toolu_2", Name: "b"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{ID: "toolu_1", Text: "r1"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "and also this"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{ID: "toolu_2", Text: "r2"}}},
	}
	msgs := sentMessages(t, llm.Request{Messages: in})
	if len(msgs) != 3 {
		t.Fatalf("want 3 alternating messages, got %+v", msgs)
	}
	c := msgs[2].Content
	if msgs[2].Role != "user" || len(c) != 3 {
		t.Fatalf("merged user message %+v", msgs[2])
	}
	if c[0]["type"] != "tool_result" || c[0]["tool_use_id"] != "toolu_1" ||
		c[1]["type"] != "tool_result" || c[1]["tool_use_id"] != "toolu_2" ||
		c[2]["type"] != "text" || c[2]["text"] != "and also this" {
		t.Fatalf("order %v", c)
	}
	if len(in) != 5 || len(in[2].Parts) != 1 {
		t.Fatal("input messages were modified")
	}
}

func TestRequestDefaults(t *testing.T) {
	p, cap := newProvider(t, serveFile(t, "text.sse"))
	s, err := p.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	_, raw, _ := cap.get()
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if defaultMaxTokens != 32000 {
		t.Fatalf("defaultMaxTokens = %d", defaultMaxTokens)
	}
	if body["model"] != DefaultModel || body["max_tokens"] != float64(32000) {
		t.Fatalf("defaults %v", body)
	}
	if _, ok := body["system"]; ok {
		t.Fatal("empty system must be omitted")
	}
	if _, ok := body["tools"]; ok {
		t.Fatal("no tools must be omitted")
	}
}

func TestBadToolSchema(t *testing.T) {
	p, _ := newProvider(t, serveFile(t, "text.sse"))
	_, err := p.Stream(context.Background(), llm.Request{Tools: []llm.Tool{{Name: "x", InputSchema: json.RawMessage(`[1]`)}}})
	if err == nil {
		t.Fatal("expected schema error")
	}
}

func TestModels(t *testing.T) {
	p, cap := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[
{"id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","created_at":"2026-09-01T00:00:00Z","type":"model"},
{"id":"embed-1","display_name":"Not a chat model","created_at":"2026-09-02T00:00:00Z","type":"model"},
{"id":"claude-haiku-5-5","display_name":"","created_at":"2026-09-02T00:00:00Z","type":"model"}],
"has_more":false,"first_id":"claude-sonnet-5-5","last_id":"claude-haiku-5-5"}`)
	}))
	got, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Model{
		{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5"},
		{ID: "claude-haiku-5-5", Label: "claude-haiku-5-5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models %#v", got)
	}
	h, _, path := cap.get()
	if path != "/v1/models" || h.Get("x-api-key") != testKey {
		t.Fatalf("path %q key %q", path, h.Get("x-api-key"))
	}
}

func TestModelsAuthError(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	_, err := p.Models(context.Background())
	if !llm.IsAuth(err) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

func asst(parts ...llm.Part) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Parts: parts}
}

func stopOf(t *testing.T, evs []llm.Event) llm.Stop {
	t.Helper()
	st, ok := evs[len(evs)-1].(llm.Stop)
	if !ok {
		t.Fatalf("last event is not Stop: %#v", evs[len(evs)-1])
	}
	return st
}

// A turn with thinking, text and two tool calls, the first with bad arguments:
// ToolCall events and Stop.Message keep block order, and replaying
// Stop.Message sends the blocks in that same order.
func TestStreamBlockOrderAndReplay(t *testing.T) {
	got, _ := run(t, "order_mixed.sse")
	var calls []llm.ToolCall
	for _, ev := range got {
		if tc, ok := ev.(llm.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	if len(calls) != 2 || calls[0].ID != "toolu_bad" || calls[0].ArgsError == "" || calls[1].ID != "toolu_good" || calls[1].ArgsError != "" {
		t.Fatalf("calls %#v", calls)
	}
	// ToolCalls come right before Usage and Stop.
	n := len(got)
	if _, ok := got[n-4].(llm.ToolCall); !ok {
		t.Fatalf("events %#v", got)
	}
	stop := stopOf(t, got)
	want := asst(
		llm.Thinking{Provider: llm.ProviderAnthropic, Text: "plan it", Signature: "SIGX"},
		llm.Text{Text: "Let me look."},
		llm.ToolUse{ID: "toolu_bad", Name: "get_doc", Args: json.RawMessage(`{}`)},
		llm.ToolUse{ID: "toolu_good", Name: "whoami", Args: json.RawMessage(`{}`)},
	)
	if !reflect.DeepEqual(stop.Message, want) {
		t.Fatalf("Stop.Message\n got %#v\nwant %#v", stop.Message, want)
	}

	msgs := sentMessages(t, llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "go"}}},
		stop.Message,
		{Role: llm.RoleUser, Parts: []llm.Part{
			llm.ToolResult{ID: "toolu_bad", Text: "bad arguments", IsError: true},
			llm.ToolResult{ID: "toolu_good", Text: "ok"},
		}},
	}})
	c := msgs[1].Content
	if len(c) != 4 || c[0]["type"] != "thinking" || c[1]["type"] != "text" ||
		c[2]["type"] != "tool_use" || c[2]["id"] != "toolu_bad" ||
		c[3]["type"] != "tool_use" || c[3]["id"] != "toolu_good" {
		t.Fatalf("replayed order %v", c)
	}
}

func TestRequestSkipsForeignThinking(t *testing.T) {
	msgs := sentMessages(t, llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "go"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{
			llm.Thinking{Provider: llm.ProviderOpenAI, Signature: "rs_1", Data: "gAAAA"},
			llm.Thinking{Provider: llm.ProviderGemini, Signature: "c2ln"},
			llm.Text{Text: "from another provider"},
		}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "again"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{
			llm.Thinking{Text: "stored by 0.2.0", Signature: "OLD"},
			llm.Thinking{Provider: llm.ProviderAnthropic, Text: "mine", Signature: "NEW"},
			llm.Text{Text: "ok"},
		}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "next"}}},
	}})
	if len(msgs) != 5 {
		t.Fatalf("messages %+v", msgs)
	}
	if c := msgs[1].Content; len(c) != 1 || c[0]["type"] != "text" {
		t.Fatalf("foreign thinking sent: %+v", c)
	}
	c := msgs[3].Content
	if len(c) != 3 || c[0]["signature"] != "OLD" || c[1]["signature"] != "NEW" || c[2]["type"] != "text" {
		t.Fatalf("own thinking not replayed: %+v", c)
	}
}

func TestRequestEncodesImages(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake")
	msgs := sentMessages(t, llm.Request{
		Images: llmtest.Images{"att-1": png},
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{
			llm.Text{Text: "what is this?"},
			llm.Image{AttachmentID: "att-1", MediaType: "image/png"},
		}}},
	})
	c := msgs[0].Content
	if len(c) != 2 || c[1]["type"] != "image" {
		t.Fatalf("content %+v", c)
	}
	src, _ := c[1]["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("image source %+v", src)
	}
	// No resolver, or an unknown attachment: the request is refused before
	// anything is sent.
	p, cap := newProvider(t, serveFile(t, "text.sse"))
	for _, imgs := range []llm.ImageResolver{nil, llmtest.Images{}} {
		_, err := p.Stream(context.Background(), llm.Request{Images: imgs, Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Image{AttachmentID: "att-1", MediaType: "image/png"}}}}})
		if err == nil {
			t.Fatalf("image without bytes accepted (%v)", imgs)
		}
	}
	if _, raw, _ := cap.get(); raw != nil {
		t.Fatalf("request sent: %s", raw)
	}
}

// A redirect is never followed: the key header would go to the new host.
func TestRedirectNotFollowed(t *testing.T) {
	var mu sync.Mutex
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hit = true
		mu.Unlock()
	}))
	t.Cleanup(other.Close)
	p, cap := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	if s, err := p.Stream(context.Background(), llm.Request{Model: DefaultModel}); err == nil {
		for {
			if _, err = s.Next(); err != nil {
				break
			}
		}
		s.Close()
	}
	if _, err := p.Models(context.Background()); err == nil {
		t.Fatal("redirected model list accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if hit {
		t.Fatal("redirect followed")
	}
	// The env base URL (127.0.0.1:1) was not used: the request reached the
	// test server, with the configured key.
	if h, _, _ := cap.get(); h.Get("x-api-key") != testKey || h.Get("Authorization") != "" {
		t.Fatalf("headers %v", h)
	}
}

func TestRequestEncodesPDF(t *testing.T) {
	pdf := []byte("%PDF-1.7 fake")
	msgs := sentMessages(t, llm.Request{
		Images: llmtest.Images{"d": pdf},
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{
			llm.Document{AttachmentID: "d", MediaType: "application/pdf", Name: "f.pdf"},
			llm.Text{Text: "sum it"},
		}}},
	})
	c := msgs[0].Content
	if len(c) != 2 || c[0]["type"] != "document" {
		t.Fatalf("content %+v", c)
	}
	src, _ := c[0]["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "application/pdf" || src["data"] != base64.StdEncoding.EncodeToString(pdf) {
		t.Fatalf("document source %+v", src)
	}
}
