// Package llmtest provides a scripted llm.Provider for loop tests.
package llmtest

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// Turn scripts one Stream call: its events, an optional delay before each
// event, and an optional error returned after the events (or from Stream
// itself when StreamErr is set).
type Turn struct {
	Events    []llm.Event
	Delay     time.Duration
	Err       error
	StreamErr error
	// Hang makes Next block after the events (and before Err) until the ctx
	// is cancelled, which returns ctx.Err(), or the stream is closed, which
	// returns io.EOF. It models a slow or stalled provider.
	Hang bool
}

// Provider replays Turns in order, one per Stream call, and records the
// requests it received. It is safe for concurrent use.
type Provider struct {
	mu       sync.Mutex
	turns    []Turn
	requests []llm.Request
	models   []llm.Model
}

// New returns a Provider that serves the given turns.
func New(turns ...Turn) *Provider { return &Provider{turns: turns} }

// SetModels sets the list Models returns.
func (p *Provider) SetModels(m []llm.Model) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = m
}

// Requests returns a deep copy of the recorded requests (messages, parts and
// tools), so a caller that keeps building its history cannot change them.
func (p *Provider) Requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.Request, len(p.requests))
	for i, r := range p.requests {
		out[i] = cloneRequest(r)
	}
	return out
}

func cloneRequest(r llm.Request) llm.Request {
	c := r
	if r.Messages != nil {
		c.Messages = make([]llm.Message, len(r.Messages))
		for i, m := range r.Messages {
			c.Messages[i] = llm.Message{Role: m.Role}
			if m.Parts != nil {
				c.Messages[i].Parts = make([]llm.Part, len(m.Parts))
				for j, part := range m.Parts {
					switch v := part.(type) {
					case llm.ToolUse:
						v.Args = append(json.RawMessage(nil), v.Args...)
						part = v
					}
					c.Messages[i].Parts[j] = part
				}
			}
		}
	}
	if r.Tools != nil {
		c.Tools = make([]llm.Tool, len(r.Tools))
		for i, t := range r.Tools {
			t.InputSchema = append(json.RawMessage(nil), t.InputSchema...)
			c.Tools[i] = t
		}
	}
	return c
}

// Models implements llm.Provider.
func (p *Provider) Models(context.Context) ([]llm.Model, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Model(nil), p.models...), nil
}

// Stream implements llm.Provider. A call past the end of the script fails.
func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	if len(p.requests) > len(p.turns) {
		p.mu.Unlock()
		return nil, &llm.APIError{Message: "llmtest: no scripted turn left"}
	}
	t := p.turns[len(p.requests)-1]
	p.mu.Unlock()
	if t.StreamErr != nil {
		return nil, t.StreamErr
	}
	return &stream{ctx: ctx, turn: t, done: make(chan struct{})}, nil
}

type stream struct {
	ctx  context.Context
	turn Turn
	i    int
	done chan struct{}
	once sync.Once
}

func (s *stream) Next() (llm.Event, error) {
	if s.turn.Delay > 0 && (s.i < len(s.turn.Events) || s.turn.Err != nil) {
		select {
		case <-time.After(s.turn.Delay):
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-s.done:
			return nil, io.EOF
		}
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-s.done:
		return nil, io.EOF
	default:
	}
	if s.i < len(s.turn.Events) {
		ev := s.turn.Events[s.i]
		s.i++
		return ev, nil
	}
	if s.turn.Hang {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-s.done:
			return nil, io.EOF
		}
	}
	if s.turn.Err != nil {
		return nil, s.turn.Err
	}
	return nil, io.EOF
}

func (s *stream) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}
