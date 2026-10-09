package openaicompat

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

const testKey = "sk-or-TESTKEY-0123456789"

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

// setEnv fills every OPENAI_* variable the SDK reads; the adapter must ignore
// them all.
func setEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "env-key-must-not-be-sent")
	t.Setenv("OPENAI_ADMIN_KEY", "env-admin-must-not-be-sent")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1/v1")
	t.Setenv("OPENAI_ORG_ID", "env-org")
	t.Setenv("OPENAI_PROJECT_ID", "env-project")
	// An Authorization line once made the adapter delete its own key header.
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Env-Leak: secret\nX-Other: 1\nAuthorization: Bearer env-leak")
	t.Setenv("OPENAI_WEBHOOK_SECRET", "env-webhook")
}

func newServer(t *testing.T, h http.Handler) (*httptest.Server, *captured) {
	t.Helper()
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.set(r)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cap.url = srv.URL + "/v1"
	return srv, cap
}

// newProvider returns an OpenRouter-shaped provider (key, attribution headers)
// whose base URL is the fake server.
func newProvider(t *testing.T, h http.Handler) (*Provider, *captured) {
	t.Helper()
	setEnv(t)
	_, cap := newServer(t, h)
	pr := OpenRouter
	pr.BaseURL = cap.url
	return New(pr, testKey, WithMaxRetries(0)), cap
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

func hi() llm.Request {
	return llm.Request{
		Model:    "some/model",
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "hi"}}}},
	}
}

func runWith(t *testing.T, pr Preset, key, file string) ([]llm.Event, *captured) {
	t.Helper()
	setEnv(t)
	_, cap := newServer(t, serveFile(t, file))
	pr.BaseURL = cap.url
	s, err := New(pr, key, WithMaxRetries(0)).Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, s), cap
}

func run(t *testing.T, file string) []llm.Event {
	t.Helper()
	evs, _ := runWith(t, OpenRouter, testKey, file)
	return evs
}

func asst(parts ...llm.Part) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Parts: parts}
}

func f64(v float64) *float64 { return &v }

func TestOpenRouterText(t *testing.T) {
	got, cap := runWith(t, OpenRouter, testKey, "openrouter_text.sse")
	want := []llm.Event{
		llm.TextDelta{Text: "Hello"},
		llm.TextDelta{Text: ", world"},
		llm.Usage{In: 40, Out: 7, Cached: 60, Cost: f64(0.00123)},
		llm.Stop{Reason: llm.StopEndTurn, Message: asst(llm.Text{Text: "Hello, world"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
	h, _, path := cap.get()
	if path != "/v1/chat/completions" {
		t.Fatalf("path %q", path)
	}
	if h.Get("Authorization") != "Bearer "+testKey {
		t.Fatalf("Authorization %q", h.Get("Authorization"))
	}
	if h.Get("HTTP-Referer") != "https://github.com/nasroykh/foxmayn_frappe_cli" || h.Get("X-Title") != "Foxmayn Frappe Desktop" {
		t.Fatalf("attribution headers %v", h)
	}
}

func TestOpenRouterTwoToolCallsSplitByIndex(t *testing.T) {
	got := run(t, "openrouter_two_tools.sse")
	if len(got) != 5 {
		t.Fatalf("events %#v", got)
	}
	if got[0] != (llm.TextDelta{Text: "Checking."}) {
		t.Fatalf("first %#v", got[0])
	}
	a, b := got[1].(llm.ToolCall), got[2].(llm.ToolCall)
	if a.ID != "call_A" || a.Name != "get_doc" || b.ID != "call_B" || b.Name != "count_docs" {
		t.Fatalf("calls %#v %#v", a, b)
	}
	if a.ArgsError != "" || b.ArgsError != "" {
		t.Fatalf("args errors %q %q", a.ArgsError, b.ArgsError)
	}
	var args map[string]string
	if err := json.Unmarshal(a.Args, &args); err != nil || args["doctype"] != "Sales Invoice" || args["name"] != "SINV-0001" {
		t.Fatalf("args A %s %v", a.Args, err)
	}
	if err := json.Unmarshal(b.Args, &args); err != nil || args["doctype"] != "Customer" {
		t.Fatalf("args B %s %v", b.Args, err)
	}
	if u := got[3].(llm.Usage); u.In != 50 || u.Out != 20 || u.Cost == nil || *u.Cost != 0.5 {
		t.Fatalf("usage %#v", u)
	}
	stop := got[4].(llm.Stop)
	if stop.Reason != llm.StopToolUse {
		t.Fatalf("reason %q", stop.Reason)
	}
	want := asst(llm.Text{Text: "Checking."},
		llm.ToolUse{ID: "call_A", Name: "get_doc", Args: a.Args},
		llm.ToolUse{ID: "call_B", Name: "count_docs", Args: b.Args})
	if !reflect.DeepEqual(stop.Message, want) {
		t.Fatalf("message\n got %#v\nwant %#v", stop.Message, want)
	}
}

func TestOllamaToolCallInOneChunk(t *testing.T) {
	got, cap := runWith(t, Ollama, "", "ollama_tool.sse")
	if len(got) != 2 {
		t.Fatalf("events %#v (no usage was sent, none expected)", got)
	}
	tc := got[0].(llm.ToolCall)
	if tc.ID != "call_zx1" || tc.Name != "get_doc" || tc.ArgsError != "" {
		t.Fatalf("call %#v", tc)
	}
	// Ollama answers "stop" even when it called a tool.
	if stop := got[1].(llm.Stop); stop.Reason != llm.StopToolUse || len(stop.Message.Parts) != 1 {
		t.Fatalf("stop %#v", got[1])
	}
	h, _, _ := cap.get()
	for _, k := range []string{"Authorization", "Api-Key", "X-Api-Key", "HTTP-Referer", "X-Title", "OpenAI-Organization", "OpenAI-Project", "X-Env-Leak", "X-Other"} {
		if h.Get(k) != "" {
			t.Fatalf("header %s sent: %q", k, h.Get(k))
		}
	}
}

func TestOllamaUsageAtEnd(t *testing.T) {
	got, _ := runWith(t, Ollama, "", "ollama_usage.sse")
	want := []llm.Event{
		llm.TextDelta{Text: "Hi"},
		llm.TextDelta{Text: "!"},
		llm.Usage{In: 12, Out: 2},
		llm.Stop{Reason: llm.StopEndTurn, Message: asst(llm.Text{Text: "Hi!"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func TestOllamaNoUsage(t *testing.T) {
	got, _ := runWith(t, Ollama, "", "ollama_text.sse")
	for _, ev := range got {
		if _, ok := ev.(llm.Usage); ok {
			t.Fatalf("usage emitted without data: %#v", got)
		}
	}
	if stop := got[len(got)-1].(llm.Stop); stop.Reason != llm.StopEndTurn {
		t.Fatalf("stop %#v", stop)
	}
}

func TestLMStudioToolCallsSplitArguments(t *testing.T) {
	got, _ := runWith(t, LMStudio, "", "lmstudio_tool_split.sse")
	want := []llm.Event{
		llm.ToolCall{ID: "426118737", Name: "get_doc", Args: json.RawMessage(`{"doctype": "ToDo"}`)},
		llm.Usage{In: 30, Out: 9},
		llm.Stop{Reason: llm.StopToolUse, Message: asst(llm.ToolUse{ID: "426118737", Name: "get_doc", Args: json.RawMessage(`{"doctype": "ToDo"}`)})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
	for _, ev := range got {
		if td, ok := ev.(llm.TextDelta); ok {
			t.Fatalf("reasoning leaked as text: %#v", td)
		}
	}
}

func TestReasoningNotEmitted(t *testing.T) {
	for _, ev := range run(t, "openrouter_text.sse") {
		if td, ok := ev.(llm.TextDelta); ok && strings.Contains(td.Text, "thinking") {
			t.Fatalf("reasoning emitted: %#v", td)
		}
		if _, ok := ev.(llm.Thinking); ok {
			t.Fatalf("thinking event emitted: %#v", ev)
		}
	}
}

func TestLengthTruncatedArgsDropped(t *testing.T) {
	got := run(t, "length_args.sse")
	var calls []llm.ToolCall
	for _, ev := range got {
		if tc, ok := ev.(llm.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	if len(calls) != 1 || calls[0].ID != "call_A" {
		t.Fatalf("calls %#v", calls)
	}
	stop := got[len(got)-1].(llm.Stop)
	if stop.Reason != llm.StopMaxTokens {
		t.Fatalf("reason %q", stop.Reason)
	}
	want := asst(llm.Text{Text: "Starting."}, llm.ToolUse{ID: "call_A", Name: "get_doc", Args: calls[0].Args})
	if !reflect.DeepEqual(stop.Message, want) {
		t.Fatalf("message\n got %#v\nwant %#v", stop.Message, want)
	}
}

func TestBadToolArgs(t *testing.T) {
	got := run(t, "args_bad.sse")
	var calls []llm.ToolCall
	for _, ev := range got {
		if tc, ok := ev.(llm.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("calls %#v", calls)
	}
	// Not JSON: ArgsError, Args "{}".
	if calls[0].ArgsError == "" || string(calls[0].Args) != "{}" {
		t.Fatalf("call 0 %#v", calls[0])
	}
	// Valid JSON but not an object: ArgsError, raw kept.
	if calls[1].ArgsError == "" || string(calls[1].Args) != "[1,2]" {
		t.Fatalf("call 1 %#v", calls[1])
	}
	// No arguments at all: an empty object, and a synthetic id.
	if calls[2].ArgsError != "" || string(calls[2].Args) != "{}" || calls[2].ID != "call_2" || calls[2].Name != "ping" {
		t.Fatalf("call 2 %#v", calls[2])
	}
	if stop := got[len(got)-1].(llm.Stop); stop.Reason != llm.StopToolUse || len(stop.Message.Parts) != 3 {
		t.Fatalf("stop %#v", stop)
	}
}

func TestRefusal(t *testing.T) {
	got := run(t, "refusal.sse")
	if stop := got[len(got)-1].(llm.Stop); stop.Reason != llm.StopRefusal {
		t.Fatalf("stop %#v", stop)
	}
}

func TestStreamEndsWithoutFinishReason(t *testing.T) {
	p, _ := newProvider(t, serveFile(t, "no_finish.sse"))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var last error
	for {
		if _, last = s.Next(); last != nil {
			break
		}
	}
	var ae *llm.APIError
	if !errors.As(last, &ae) {
		t.Fatalf("err = %v", last)
	}
}

func TestMidStreamError(t *testing.T) {
	p, _ := newProvider(t, serveFile(t, "midstream_error.sse"))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev, err := s.Next()
	if err != nil || ev != (llm.TextDelta{Text: "Partial"}) {
		t.Fatalf("first: %#v %v", ev, err)
	}
	_, err = s.Next()
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Status != 529 || !llm.IsRetryable(err) {
		t.Fatalf("err = %#v", err)
	}
	if strings.Contains(err.Error(), testKey) || ae.Message == "" {
		t.Fatalf("message %q", err.Error())
	}
}

func TestStreamErrorStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		auth   bool
		rate   bool
	}{
		{401, `{"error":{"message":"No auth credentials found","code":401}}`, true, false},
		{401, `{"error":{"message":"Incorrect API key provided: ` + testKey + `","type":"invalid_request_error","code":"invalid_api_key"}}`, true, false},
		{429, `{"error":{"message":"slow down ` + testKey + `","type":"rate_limit_error","code":429}}`, false, true},
		{500, `not json at all ` + testKey, false, false},
	}
	for _, c := range cases {
		p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, err := p.Stream(context.Background(), hi())
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
		_, _ = io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"x"},"finish_reason":null}]}`+"\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := p.Stream(ctx, hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	<-started
	if ev, err := s.Next(); err != nil || ev != (llm.TextDelta{Text: "x"}) {
		t.Fatalf("first: %#v %v", ev, err)
	}
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

func TestEnvCredentialsNotSent(t *testing.T) {
	// A keyless provider must not pick up OPENAI_API_KEY either.
	_, cap := runWith(t, Custom("x"), "", "ollama_text.sse")
	h, _, path := cap.get()
	if path != "/v1/chat/completions" {
		t.Fatalf("path %q (OPENAI_BASE_URL used?)", path)
	}
	for k, v := range h {
		for _, s := range v {
			if strings.Contains(s, "env-") || strings.Contains(s, "secret") {
				t.Fatalf("env value sent in %s: %q", k, s)
			}
		}
	}
	if h.Get("Authorization") != "" {
		t.Fatalf("Authorization %q", h.Get("Authorization"))
	}
	// A custom server with a key gets exactly that key.
	_, cap = runWith(t, Custom("x"), "custom-key", "ollama_text.sse")
	h, _, _ = cap.get()
	if h.Get("Authorization") != "Bearer custom-key" {
		t.Fatalf("Authorization %q", h.Get("Authorization"))
	}
	if h.Get("HTTP-Referer") != "" || h.Get("X-Title") != "" {
		t.Fatalf("attribution sent to a custom server: %v", h)
	}
}

func TestRequestBody(t *testing.T) {
	p, cap := newProvider(t, serveFile(t, "ollama_text.sse"))
	args := json.RawMessage(`{"doctype":"ToDo","name":"T-1"}`)
	req := llm.Request{
		Model:     "some/model",
		System:    "be brief",
		MaxTokens: 512,
		Tools: []llm.Tool{
			{Name: "get_doc", Description: "read one", InputSchema: json.RawMessage(`{"type":"object","properties":{"doctype":{"type":"string"}},"required":["doctype"]}`)},
			{Name: "ping"},
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "look"}}},
			{Role: llm.RoleAssistant, Parts: []llm.Part{
				llm.Thinking{Text: "secret reasoning", Signature: "sig"},
				llm.Thinking{Redacted: true, Data: "blob"},
				llm.Text{Text: "ok"},
				llm.ToolUse{ID: "call_1", Name: "get_doc", Args: args},
				llm.ToolUse{ID: "call_2", Name: "ping", Args: json.RawMessage(`not json`)},
			}},
			{Role: llm.RoleUser, Parts: []llm.Part{
				llm.ToolResult{ID: "call_1", Text: "the doc"},
				llm.ToolResult{ID: "call_2", Text: "boom", IsError: true},
				llm.Text{Text: "and continue"},
			}},
			{Role: llm.RoleAssistant, Parts: []llm.Part{llm.Thinking{Text: "only thinking"}}},
		},
	}
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	h, raw, _ := cap.get()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "some/model" || body["stream"] != true || body["max_tokens"] != float64(512) {
		t.Fatalf("body %s", raw)
	}
	if so, _ := body["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatalf("stream_options %v", body["stream_options"])
	}
	if strings.Contains(string(raw), "secret reasoning") || strings.Contains(string(raw), "blob") || strings.Contains(string(raw), "sig") {
		t.Fatalf("thinking sent: %s", raw)
	}
	// The key lives in the Authorization header only.
	if strings.Contains(string(raw), testKey) {
		t.Fatalf("key in the body: %s", raw)
	}
	for k, v := range h {
		if k != "Authorization" && strings.Contains(strings.Join(v, ","), testKey) {
			t.Fatalf("key in header %s", k)
		}
	}

	msgs, _ := body["messages"].([]any)
	type msg = map[string]any
	at := func(i int) msg { return msgs[i].(msg) }
	if len(msgs) != 6 { // the thinking-only assistant turn sends nothing
		t.Fatalf("messages (%d): %s", len(msgs), raw)
	}
	if at(0)["role"] != "system" || at(0)["content"] != "be brief" {
		t.Fatalf("system %v", at(0))
	}
	if at(1)["role"] != "user" || at(1)["content"] != "look" {
		t.Fatalf("user %v", at(1))
	}
	a := at(2)
	if a["role"] != "assistant" || a["content"] != "ok" {
		t.Fatalf("assistant %v", a)
	}
	calls := a["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("tool_calls %v", calls)
	}
	c0 := calls[0].(msg)
	if c0["id"] != "call_1" || c0["type"] != "function" || c0["function"].(msg)["name"] != "get_doc" || c0["function"].(msg)["arguments"] != string(args) {
		t.Fatalf("call 0 %v", c0)
	}
	if calls[1].(msg)["function"].(msg)["arguments"] != "{}" {
		t.Fatalf("call 1 %v", calls[1])
	}
	t1, t2 := at(3), at(4)
	if t1["role"] != "tool" || t1["tool_call_id"] != "call_1" || t1["content"] != "the doc" ||
		t2["role"] != "tool" || t2["tool_call_id"] != "call_2" || t2["content"] != "Error: boom" {
		t.Fatalf("tool messages %v %v", t1, t2)
	}
	if at(5)["role"] != "user" || at(5)["content"] != "and continue" {
		t.Fatalf("trailing user %v", at(5))
	}
}

func TestRequestTools(t *testing.T) {
	p, cap := newProvider(t, serveFile(t, "ollama_text.sse"))
	req := hi()
	req.Tools = []llm.Tool{
		{Name: "get_doc", Description: "read one", InputSchema: json.RawMessage(`{"type":"object","properties":{"doctype":{"type":"string"}},"required":["doctype"]}`)},
		{Name: "ping"},
	}
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	_, raw, _ := cap.get()
	var body struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		MaxTokens *int `json:"max_tokens"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 2 || body.Tools[0].Type != "function" || body.Tools[0].Function.Name != "get_doc" ||
		body.Tools[0].Function.Description != "read one" {
		t.Fatalf("tools %s", raw)
	}
	if body.Tools[0].Function.Parameters["required"] == nil {
		t.Fatalf("schema lost: %s", raw)
	}
	if body.Tools[1].Function.Parameters["type"] != "object" {
		t.Fatalf("empty schema: %v", body.Tools[1].Function.Parameters)
	}
	if body.MaxTokens != nil {
		t.Fatalf("max_tokens sent without a limit: %s", raw)
	}
}

func TestRequestNeedsModel(t *testing.T) {
	p, _ := newProvider(t, serveFile(t, "ollama_text.sse"))
	if _, err := p.Stream(context.Background(), llm.Request{}); err == nil {
		t.Fatal("no error for an empty model")
	}
	req := hi()
	req.Tools = []llm.Tool{{Name: "x", InputSchema: json.RawMessage(`[`)}}
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Fatal("no error for a bad schema")
	}
}

func TestModelsOpenRouterFiltersTools(t *testing.T) {
	const list = `{"data":[
		{"id":"a/tools","name":"A Tools","created":1,"supported_parameters":["temperature","tools","max_tokens"]},
		{"id":"b/notools","name":"B No Tools","created":1,"supported_parameters":["temperature"]},
		{"id":"c/unknown","created":1}
	]}`
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, list)
	})
	p, cap := newProvider(t, h)
	got, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Model{{ID: "a/tools", Label: "A Tools"}, {ID: "c/unknown", Label: "c/unknown"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if _, _, path := cap.get(); path != "/v1/models" {
		t.Fatalf("path %q", path)
	}
	// Other servers get every model.
	_, cap2 := newServer(t, h)
	pr := Ollama
	pr.BaseURL = cap2.url
	all, err := New(pr, "", WithMaxRetries(0)).Models(context.Background())
	if err != nil || len(all) != 3 {
		t.Fatalf("all %#v %v", all, err)
	}
}

func TestModelsAuthError(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key `+testKey+`","code":401}}`)
	}))
	_, err := p.Models(context.Background())
	if !llm.IsAuth(err) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectLocal(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer up.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hang.Close()

	ollama, lm := Ollama, LMStudio
	ollama.BaseURL = up.URL + "/v1"
	lm.BaseURL = downURL + "/v1"
	hung := Custom(hang.URL + "/v1")

	t0 := time.Now()
	got := detect(context.Background(), []Preset{ollama, lm, hung}, 300*time.Millisecond)
	if len(got) != 1 || got[0].ID != "ollama" {
		t.Fatalf("got %#v", got)
	}
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("probes ran too long: %v", d)
	}
	// The real presets are the ones DetectLocal probes.
	if probeTimeout > 1500*time.Millisecond {
		t.Fatalf("probe timeout %v", probeTimeout)
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
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	if s, err := p.Stream(context.Background(), hi()); err == nil {
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
}
