package llmtest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

func drain(s llm.Stream) ([]llm.Event, error) {
	var out []llm.Event
	for {
		ev, err := s.Next()
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
}

func TestScriptAndRecording(t *testing.T) {
	p := New(
		Turn{Events: []llm.Event{llm.TextDelta{Text: "hi"}, llm.Stop{Reason: "end_turn"}}},
		Turn{Events: []llm.Event{llm.Stop{Reason: "end_turn"}}, Err: errors.New("boom")},
	)
	s, err := p.Stream(context.Background(), llm.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	evs, err := drain(s)
	if err != io.EOF || len(evs) != 2 {
		t.Fatalf("turn1: %v %v", evs, err)
	}
	s, _ = p.Stream(context.Background(), llm.Request{Model: "m2"})
	evs, err = drain(s)
	if err == nil || err.Error() != "boom" || len(evs) != 1 {
		t.Fatalf("turn2: %v %v", evs, err)
	}
	if _, err := p.Stream(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error past the script")
	}
	reqs := p.Requests()
	if len(reqs) != 3 || reqs[0].Model != "m1" || reqs[1].Model != "m2" {
		t.Fatalf("requests: %+v", reqs)
	}
}

func TestStreamErr(t *testing.T) {
	p := New(Turn{StreamErr: &llm.APIError{Status: 401, Message: "bad"}})
	if _, err := p.Stream(context.Background(), llm.Request{}); !llm.IsAuth(err) {
		t.Fatalf("got %v", err)
	}
}

func TestCancelDuringDelay(t *testing.T) {
	p := New(Turn{Events: []llm.Event{llm.TextDelta{Text: "x"}}, Delay: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := p.Stream(ctx, llm.Request{})
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := s.Next()
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestCloseUnblocksNext(t *testing.T) {
	p := New(Turn{Events: []llm.Event{llm.TextDelta{Text: "x"}}, Delay: time.Hour})
	s, _ := p.Stream(context.Background(), llm.Request{})
	go func() { time.Sleep(20 * time.Millisecond); _ = s.Close() }()
	if _, err := s.Next(); err != io.EOF {
		t.Fatalf("err=%v", err)
	}
}

func TestHangUntilCancel(t *testing.T) {
	p := New(Turn{Events: []llm.Event{llm.TextDelta{Text: "x"}}, Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := p.Stream(ctx, llm.Request{})
	if ev, err := s.Next(); err != nil || ev != (llm.TextDelta{Text: "x"}) {
		t.Fatalf("first: %v %v", ev, err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Next(); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("Next returned before cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	t0 := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || time.Since(t0) > time.Second {
			t.Fatalf("err=%v after %v", err, time.Since(t0))
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not return after cancel")
	}
}

func TestHangUntilClose(t *testing.T) {
	p := New(Turn{Hang: true})
	s, _ := p.Stream(context.Background(), llm.Request{})
	go func() { time.Sleep(20 * time.Millisecond); _ = s.Close() }()
	if _, err := s.Next(); err != io.EOF {
		t.Fatalf("err=%v", err)
	}
}

func TestRequestsAreDeepCopies(t *testing.T) {
	p := New(Turn{}, Turn{})
	msgs := []llm.Message{{Role: llm.RoleAssistant, Parts: []llm.Part{
		llm.ToolUse{ID: "a", Name: "n", Args: json.RawMessage(`{"k":1}`)},
	}}}
	_, _ = p.Stream(context.Background(), llm.Request{Messages: msgs})
	// The loop keeps appending to and rewriting its history.
	msgs[0].Parts[0] = llm.Text{Text: "changed"}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser})
	got := p.Requests()
	if len(got[0].Messages) != 1 {
		t.Fatalf("messages %+v", got[0].Messages)
	}
	if tu, ok := got[0].Messages[0].Parts[0].(llm.ToolUse); !ok || tu.ID != "a" {
		t.Fatalf("recorded part changed: %#v", got[0].Messages[0].Parts[0])
	}
	// Mutating a returned copy does not reach the recorded one either.
	got[0].Messages[0].Parts[0] = llm.Text{Text: "x"}
	if _, ok := p.Requests()[0].Messages[0].Parts[0].(llm.ToolUse); !ok {
		t.Fatal("Requests() shares memory with the recording")
	}
}

func TestConcurrent(t *testing.T) {
	var turns []Turn
	for i := 0; i < 20; i++ {
		turns = append(turns, Turn{Events: []llm.Event{llm.Stop{Reason: "end_turn"}}})
	}
	p := New(turns...)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := p.Stream(context.Background(), llm.Request{})
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = drain(s)
			_ = p.Requests()
		}()
	}
	wg.Wait()
	if len(p.Requests()) != 20 {
		t.Fatal("lost requests")
	}
}
