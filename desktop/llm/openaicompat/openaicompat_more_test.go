package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

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

func TestSameIndexDistinctIDs(t *testing.T) {
	got := run(t, "same_index.sse")
	var calls []llm.ToolCall
	for _, ev := range got {
		switch e := ev.(type) {
		case llm.ToolCall:
			calls = append(calls, e)
		case llm.TextDelta:
			t.Fatalf("a second choice leaked: %#v", e)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("calls %#v", calls)
	}
	if calls[0].ID != "call_one" || calls[0].Name != "get_doc" || string(calls[0].Args) != `{"doctype":"ToDo"}` || calls[0].ArgsError != "" {
		t.Fatalf("call 0 %#v", calls[0])
	}
	if calls[1].ID != "call_two" || calls[1].Name != "count_docs" || string(calls[1].Args) != `{"doctype":"User"}` || calls[1].ArgsError != "" {
		t.Fatalf("call 1 %#v", calls[1])
	}
	if stop := got[len(got)-1].(llm.Stop); stop.Reason != llm.StopToolUse || len(stop.Message.Parts) != 2 {
		t.Fatalf("stop %#v", stop)
	}
}

func TestRetryableErrors(t *testing.T) {
	// A stream that ends without finish_reason.
	p, _ := newProvider(t, serveFile(t, "no_finish.sse"))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	if err := nextErrAfterText(t, s); !llm.IsRetryable(err) {
		t.Fatalf("no finish_reason not retryable: %v", err)
	}

	// A connection cut after the headers.
	p, _ = newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	s, err = p.Stream(context.Background(), hi())
	if err != nil {
		if !llm.IsRetryable(err) {
			t.Fatalf("transport error not retryable: %v", err)
		}
	} else if err := nextErr(t, s); !llm.IsRetryable(err) {
		t.Fatalf("cut connection not retryable: %v", err)
	}

	// An in-stream error with a string code and a type.
	p, _ = newProvider(t, serveFile(t, "midstream_rate_limit.sse"))
	s, err = p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	err = nextErr(t, s)
	if !llm.IsRateLimit(err) || !llm.IsRetryable(err) {
		t.Fatalf("rate_limit_exceeded: %v", err)
	}

	// A cancelled context stays a cancel, never a retryable APIError.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, _ = newProvider(t, serveFile(t, "no_finish.sse"))
	if _, err := p.Stream(ctx, hi()); !errors.Is(err, context.Canceled) || llm.IsRetryable(err) {
		t.Fatalf("cancel: %v", err)
	}
}

func nextErrAfterText(t *testing.T, s llm.Stream) error {
	t.Helper()
	return nextErr(t, s)
}

// sentMessages runs a request and returns the "messages" of its body.
func sentMessages(t *testing.T, msgs []llm.Message) []map[string]any {
	t.Helper()
	p, cap := newProvider(t, serveFile(t, "ollama_text.sse"))
	req := hi()
	req.Messages = msgs
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	_, raw, _ := cap.get()
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Messages
}

func roles(msgs []map[string]any) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m["role"].(string))
	}
	return out
}

func TestHistoryToolMessageOrderAndGaps(t *testing.T) {
	msgs := sentMessages(t, []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "a"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "b"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{
			llm.ToolUse{ID: "c1", Name: "x", Args: json.RawMessage(`{}`)},
			llm.ToolUse{ID: "c2", Name: "x", Args: json.RawMessage(`{}`)},
			llm.ToolUse{ID: "c3", Name: "x", Args: json.RawMessage(`{}`)},
		}},
		{Role: llm.RoleUser, Parts: []llm.Part{
			llm.ToolResult{ID: "c3", Text: "three"},
			llm.ToolResult{ID: "zzz", Text: "stray"},
			llm.ToolResult{ID: "c1", Text: "one"},
			llm.Text{Text: "go on"},
		}},
	})
	// user (a+b merged), assistant, tool c1, c2, c3, user.
	if want := []string{"user", "assistant", "tool", "tool", "tool", "user"}; !reflect.DeepEqual(roles(msgs), want) {
		t.Fatalf("roles %v\n%v", roles(msgs), msgs)
	}
	if msgs[0]["content"] != "ab" {
		t.Fatalf("merged user %v", msgs[0])
	}
	if _, has := msgs[1]["content"]; has && msgs[1]["content"] != nil {
		t.Fatalf("content on a tool-call-only turn: %v", msgs[1]["content"])
	}
	want := [][2]string{{"c1", "one"}, {"c2", "Error: no result"}, {"c3", "three"}}
	for i, w := range want {
		m := msgs[2+i]
		if m["tool_call_id"] != w[0] || m["content"] != w[1] {
			t.Fatalf("tool %d: %v want %v", i, m, w)
		}
	}
	if msgs[5]["content"] != "go on" {
		t.Fatalf("trailing %v", msgs[5])
	}
}

func TestHistoryCallsWithoutFollowingUser(t *testing.T) {
	msgs := sentMessages(t, []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "a"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{llm.Text{Text: "t"}, llm.ToolUse{ID: "c1", Name: "x"}}},
	})
	if want := []string{"user", "assistant", "tool"}; !reflect.DeepEqual(roles(msgs), want) {
		t.Fatalf("roles %v", roles(msgs))
	}
	if msgs[2]["tool_call_id"] != "c1" || msgs[2]["content"] != "Error: no result" {
		t.Fatalf("tool %v", msgs[2])
	}
}

func TestHistoryEmptyToolUseID(t *testing.T) {
	msgs := sentMessages(t, []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "a"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{llm.ToolUse{Name: "x", Args: json.RawMessage(`{}`)}}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{Text: "answer"}}},
	})
	calls := msgs[1]["tool_calls"].([]any)
	id := calls[0].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("empty tool call id: %v", msgs[1])
	}
	if msgs[2]["tool_call_id"] != id || msgs[2]["content"] != "answer" {
		t.Fatalf("tool %v (call id %q)", msgs[2], id)
	}
}
