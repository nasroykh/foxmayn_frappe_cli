package anthropic

import (
	"context"
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
)

const testKey = "sk-ant-TESTKEY-0123456789"

// captured is what the fake server saw on the last request.
type captured struct {
	mu     sync.Mutex
	header http.Header
	body   []byte
	path   string
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
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.set(r)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
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
		llm.Stop{Reason: "end_turn"},
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
	if got[2] != (llm.Usage{In: 100, Out: 42}) || got[3] != (llm.Stop{Reason: "tool_use"}) {
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
		llm.Usage{In: 312, Out: 77, Cached: 4096},
		llm.Stop{Reason: "max_tokens"},
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

func TestStreamMalformedToolArgs(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"x","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"[1,2"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)
	}))
	s, err := p.Stream(context.Background(), llm.Request{Model: DefaultModel})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Next(); err == nil || !strings.Contains(err.Error(), "not a JSON object") {
		t.Fatalf("err = %v", err)
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
		System    []struct {
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
	if body["model"] != DefaultModel || body["max_tokens"] != float64(defaultMaxTokens) {
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
