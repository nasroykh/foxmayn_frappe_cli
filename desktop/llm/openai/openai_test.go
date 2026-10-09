package openai

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

const testKey = "sk-proj-TESTKEY-0123456789"

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

// setEnv fills every OPENAI_* variable the SDK reads; the adapter must ignore
// them all.
func setEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "env-key-must-not-be-sent")
	t.Setenv("OPENAI_ADMIN_KEY", "env-admin-must-not-be-sent")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1/v1")
	t.Setenv("OPENAI_ORG_ID", "env-org")
	t.Setenv("OPENAI_PROJECT_ID", "env-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Env-Leak: secret\nX-Other: 1")
}

func newProvider(t *testing.T, h http.Handler) (*Provider, *captured) {
	t.Helper()
	setEnv(t)
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.set(r)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(testKey, WithBaseURL(srv.URL+"/v1"), WithMaxRetries(0)), cap
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
		Model:    "gpt-test",
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "hi"}}}},
	}
}

func run(t *testing.T, file string, req llm.Request) ([]llm.Event, *captured) {
	t.Helper()
	p, cap := newProvider(t, serveFile(t, file))
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, s), cap
}

// sentInput runs req and returns the request body's "input" items.
func sentInput(t *testing.T, req llm.Request) []map[string]any {
	t.Helper()
	_, cap := run(t, "text.sse", req)
	_, raw, _ := cap.get()
	var body struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Input
}

func asst(parts ...llm.Part) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Parts: parts}
}

func TestText(t *testing.T) {
	req := hi()
	req.System = "be brief"
	req.MaxTokens = 500
	got, cap := run(t, "text.sse", req)
	want := []llm.Event{
		llm.TextDelta{Text: "Hello"},
		llm.TextDelta{Text: ", world"},
		llm.Usage{In: 40, Out: 7, Cached: 60},
		llm.Stop{Reason: llm.StopEndTurn, Message: asst(llm.Text{Text: "Hello, world"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
	h, raw, path := cap.get()
	if path != "/v1/responses" {
		t.Fatalf("path %q (OPENAI_BASE_URL used?)", path)
	}
	if h.Get("Authorization") != "Bearer "+testKey {
		t.Fatalf("Authorization %q", h.Get("Authorization"))
	}
	for k, v := range h {
		for _, s := range v {
			if strings.Contains(s, "env-") || strings.Contains(s, "secret") {
				t.Fatalf("env value sent in %s: %q", k, s)
			}
		}
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["store"] != false || body["stream"] != true || body["model"] != "gpt-test" || body["instructions"] != "be brief" || body["max_output_tokens"] != float64(500) {
		t.Fatalf("body %s", raw)
	}
	if inc, _ := body["include"].([]any); len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Fatalf("include %v", body["include"])
	}
	in, _ := body["input"].([]any)
	if len(in) != 1 || in[0].(map[string]any)["role"] != "user" || in[0].(map[string]any)["content"] != "hi" {
		t.Fatalf("input %v", body["input"])
	}
}

func TestRequestNeedsModel(t *testing.T) {
	p, cap := newProvider(t, serveFile(t, "text.sse"))
	req := hi()
	req.Model = ""
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Fatal("no model accepted")
	}
	if _, raw, _ := cap.get(); raw != nil {
		t.Fatal("request sent")
	}
}

func TestParallelToolCallsSplitArgs(t *testing.T) {
	got, _ := run(t, "reasoning_tools.sse", hi())
	th := llm.Thinking{Provider: llm.ProviderOpenAI, Signature: "rs_abc", Data: "gAAAAABo-ENC+/=="}
	a := llm.ToolCall{ID: "call_A", Name: "get_doc", Args: json.RawMessage(`{"doctype":"Sales Invoice","name":"SINV-1"}`)}
	b := llm.ToolCall{ID: "call_B", Name: "list_docs", Args: json.RawMessage(`{"doctype":"Customer"}`)}
	want := []llm.Event{
		th, a, b,
		llm.Usage{In: 904, Out: 300, Cached: 4096, CacheWrite: 512},
		llm.Stop{Reason: llm.StopToolUse, Message: asst(th,
			llm.ToolUse{ID: a.ID, Name: a.Name, Args: a.Args},
			llm.ToolUse{ID: b.ID, Name: b.Name, Args: b.Args})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

// The reasoning item comes back on the next request with its encrypted
// content unchanged (and no id: store is false), right before the calls it
// produced, followed by one function_call_output per call.
func TestReasoningReplay(t *testing.T) {
	got, _ := run(t, "reasoning_tools.sse", hi())
	stop := got[len(got)-1].(llm.Stop)
	req := hi()
	req.Messages = append(req.Messages, stop.Message, llm.Message{Role: llm.RoleUser, Parts: []llm.Part{
		llm.ToolResult{ID: "call_B", Text: "[]"},
		llm.ToolResult{ID: "call_A", Text: "not found", IsError: true},
		llm.ToolResult{ID: "call_unknown", Text: "dropped"},
		llm.Text{Text: "and then?"},
	}})
	in := sentInput(t, req)
	if len(in) != 7 {
		t.Fatalf("input %v", in)
	}
	r := in[1]
	if r["type"] != "reasoning" || r["encrypted_content"] != "gAAAAABo-ENC+/==" || r["id"] != nil {
		t.Fatalf("reasoning item %v", r)
	}
	if s, ok := r["summary"].([]any); !ok || len(s) != 0 {
		t.Fatalf("summary %v", r["summary"])
	}
	if in[2]["type"] != "function_call" || in[2]["call_id"] != "call_A" || in[2]["name"] != "get_doc" || in[2]["arguments"] != `{"doctype":"Sales Invoice","name":"SINV-1"}` || in[2]["id"] != nil {
		t.Fatalf("call A %v", in[2])
	}
	if in[3]["type"] != "function_call" || in[3]["call_id"] != "call_B" {
		t.Fatalf("call B %v", in[3])
	}
	if in[4]["type"] != "function_call_output" || in[4]["call_id"] != "call_A" || in[4]["output"] != "Error: not found" {
		t.Fatalf("output A %v", in[4])
	}
	if in[5]["type"] != "function_call_output" || in[5]["call_id"] != "call_B" || in[5]["output"] != "[]" {
		t.Fatalf("output B %v", in[5])
	}
	if in[6]["role"] != "user" || in[6]["content"] != "and then?" {
		t.Fatalf("user text %v", in[6])
	}
}

func TestHistorySkipsForeignThinking(t *testing.T) {
	req := hi()
	req.Messages = append(req.Messages,
		asst(
			llm.Thinking{Text: "0.2.0 anthropic", Signature: "S"},
			llm.Thinking{Provider: llm.ProviderAnthropic, Text: "a", Signature: "S2"},
			llm.Thinking{Provider: llm.ProviderGemini, Signature: "c2ln"},
			llm.Text{Text: "answer"},
		),
		llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "next"}}},
	)
	in := sentInput(t, req)
	if len(in) != 3 {
		t.Fatalf("input %v", in)
	}
	if in[1]["role"] != "assistant" || in[1]["content"] != "answer" {
		t.Fatalf("assistant %v", in[1])
	}
}

func TestMissingResultGetsError(t *testing.T) {
	req := hi()
	req.Messages = append(req.Messages, asst(llm.ToolUse{ID: "c1", Name: "whoami"}))
	in := sentInput(t, req)
	if len(in) != 3 || in[2]["type"] != "function_call_output" || in[2]["call_id"] != "c1" || in[2]["output"] != "Error: no result" {
		t.Fatalf("input %v", in)
	}
	if in[1]["arguments"] != "{}" {
		t.Fatalf("args %v", in[1])
	}
}

func TestImageEncoding(t *testing.T) {
	gif := []byte("GIF89a\x01\x00")
	req := hi()
	req.Images = llmtest.Images{"g": gif}
	req.Messages = []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "look"}, llm.Image{AttachmentID: "g", MediaType: "image/gif"}}}}
	in := sentInput(t, req)
	c, _ := in[0]["content"].([]any)
	if len(c) != 2 {
		t.Fatalf("content %v", in[0])
	}
	txt, img := c[0].(map[string]any), c[1].(map[string]any)
	if txt["type"] != "input_text" || txt["text"] != "look" {
		t.Fatalf("text %v", txt)
	}
	if img["type"] != "input_image" || img["detail"] != "auto" || img["image_url"] != "data:image/gif;base64,"+base64.StdEncoding.EncodeToString(gif) {
		t.Fatalf("image %v", img)
	}
	p, _ := newProvider(t, serveFile(t, "text.sse"))
	req.Images = nil
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Fatal("image without resolver accepted")
	}
}

func TestToolsSent(t *testing.T) {
	req := hi()
	req.Tools = []llm.Tool{{Name: "get_doc", Description: "Read one", InputSchema: json.RawMessage(`{"type":"object","properties":{"doctype":{"type":"string"}},"required":["doctype"]}`)}, {Name: "whoami"}}
	_, cap := run(t, "text.sse", req)
	_, raw, _ := cap.get()
	var body struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("tools %s", raw)
	}
	g := body.Tools[0]
	if g["type"] != "function" || g["name"] != "get_doc" || g["description"] != "Read one" || g["strict"] != false {
		t.Fatalf("tool %v", g)
	}
	if req, _ := g["parameters"].(map[string]any)["required"].([]any); len(req) != 1 {
		t.Fatalf("parameters %v", g["parameters"])
	}
	if w := body.Tools[1]["parameters"].(map[string]any); w["type"] != "object" {
		t.Fatalf("empty schema %v", w)
	}
}

func TestIncompleteDropsCutCalls(t *testing.T) {
	got, _ := run(t, "incomplete.sse", hi())
	want := []llm.Event{
		llm.TextDelta{Text: "Partial"},
		llm.Usage{In: 10, Out: 32},
		llm.Stop{Reason: llm.StopMaxTokens, Message: asst(llm.Text{Text: "Partial"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func TestRefusal(t *testing.T) {
	got, _ := run(t, "refusal.sse", hi())
	want := []llm.Event{
		llm.TextDelta{Text: "I cannot help with that."},
		llm.Usage{In: 9, Out: 6},
		llm.Stop{Reason: llm.StopRefusal, Message: asst(llm.Text{Text: "I cannot help with that."})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
}

func nextErr(t *testing.T, s llm.Stream) error {
	t.Helper()
	defer s.Close()
	for {
		if _, err := s.Next(); err != nil {
			if err == io.EOF {
				t.Fatal("stream ended without an error")
			}
			return err
		}
	}
}

func TestMidStreamErrors(t *testing.T) {
	cases := []struct {
		file      string
		status    int
		retryable bool
	}{
		{"failed.sse", 429, true},
		{"error_event.sse", 500, true},
	}
	for _, c := range cases {
		p, _ := newProvider(t, serveFile(t, c.file))
		s, err := p.Stream(context.Background(), hi())
		if err != nil {
			t.Fatal(err)
		}
		err = nextErr(t, s)
		var ae *llm.APIError
		if !errors.As(err, &ae) || ae.Status != c.status || llm.IsRetryable(err) != c.retryable {
			t.Fatalf("%s: %v", c.file, err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("%s: key leaked in %q", c.file, err)
		}
	}
}

func TestStreamEndsEarly(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"x\"}\n\n")
	}))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	err = nextErr(t, s)
	if !llm.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
}

func TestErrorStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		auth   bool
		rate   bool
	}{
		{401, `{"error":{"message":"Incorrect API key provided: ` + testKey + `","type":"invalid_request_error","code":"invalid_api_key"}}`, true, false},
		{403, `{"error":{"message":"Project does not have access","type":"invalid_request_error"}}`, true, false},
		{429, `{"error":{"message":"Rate limit reached ` + testKey + `","type":"requests","code":"rate_limit_exceeded"}}`, false, true},
		{500, `not json ` + testKey, false, false},
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
		if _, err := p.Models(context.Background()); !errors.As(err, &ae) || ae.Status != c.status || strings.Contains(err.Error(), testKey) {
			t.Fatalf("models %d: %v", c.status, err)
		}
	}
}

func TestCancelMidStream(t *testing.T) {
	started := make(chan struct{})
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"x\"}\n\n")
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

func TestModelsFilteredNewestFirst(t *testing.T) {
	p, cap := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[
			{"id":"gpt-old","object":"model","created":100,"owned_by":"openai"},
			{"id":"text-embedding-3-large","object":"model","created":900,"owned_by":"openai"},
			{"id":"gpt-new","object":"model","created":300,"owned_by":"openai"},
			{"id":"o9-mini","object":"model","created":200,"owned_by":"openai"},
			{"id":"gpt-new-realtime","object":"model","created":400,"owned_by":"openai"},
			{"id":"gpt-image-2","object":"model","created":500,"owned_by":"openai"},
			{"id":"whisper-1","object":"model","created":50,"owned_by":"openai"},
			{"id":"omni-moderation-latest","object":"model","created":60,"owned_by":"openai"},
			{"id":"gpt-3.5-turbo-instruct","object":"model","created":70,"owned_by":"openai"}
		]}`)
	}))
	list, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range list {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "gpt-new,o9-mini,gpt-old" {
		t.Fatalf("models %v", ids)
	}
	if _, _, path := cap.get(); path != "/v1/models" {
		t.Fatalf("path %q", path)
	}
}

// The real client talks only to api.openai.com.
func TestPinnedHost(t *testing.T) {
	if BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("BaseURL %q", BaseURL)
	}
}
