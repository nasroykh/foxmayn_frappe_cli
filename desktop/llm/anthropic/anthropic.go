// Package anthropic adapts the Anthropic Messages API to llm.Provider.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// DefaultModel is the model the app picks first.
const DefaultModel = "claude-sonnet-5-5"

const (
	defaultBaseURL   = "https://api.anthropic.com/"
	defaultMaxTokens = 8192
	maxMessageLen    = 300
)

// Option customises New.
type Option func(*config)

type config struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
}

// WithBaseURL points the adapter at another server (tests).
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.httpClient = h } }

// WithMaxRetries sets the SDK retry count for connection errors, 408, 409,
// 429 and 5xx (default 2).
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetries = n } }

// Provider is the Anthropic llm.Provider.
type Provider struct {
	client sdk.Client
	key    string
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that authenticates with apiKey. The key goes only in
// the x-api-key header; ANTHROPIC_* environment credentials are overridden.
func New(apiKey string, opts ...Option) *Provider {
	cfg := config{baseURL: defaultBaseURL, maxRetries: 2}
	for _, o := range opts {
		o(&cfg)
	}
	ro := []option.RequestOption{
		option.WithBaseURL(cfg.baseURL),
		option.WithAPIKey(apiKey),
		option.WithHeaderDel("authorization"),
		option.WithMaxRetries(cfg.maxRetries),
	}
	if cfg.httpClient != nil {
		ro = append(ro, option.WithHTTPClient(cfg.httpClient))
	}
	return &Provider{client: sdk.NewClient(ro...), key: apiKey}
}

// Models lists the models the account can use.
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	pager := p.client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})
	var out []llm.Model
	for pager.Next() {
		m := pager.Current()
		label := m.DisplayName
		if label == "" {
			label = m.ID
		}
		out = append(out, llm.Model{ID: m.ID, Label: label})
	}
	if err := pager.Err(); err != nil {
		return nil, p.mapErr(ctx, err)
	}
	return out, nil
}

// Stream starts one streaming Messages request.
func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}
	s := p.client.Messages.NewStreaming(ctx, params)
	if err := s.Err(); err != nil {
		_ = s.Close()
		return nil, p.mapErr(ctx, err)
	}
	return &stream{ctx: ctx, p: p, s: s, blocks: map[int64]*toolBlock{}}, nil
}

func buildParams(req llm.Request) (sdk.MessageNewParams, error) {
	max := req.MaxTokens
	if max <= 0 {
		max = defaultMaxTokens
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	params := sdk.MessageNewParams{
		Model:     sdk.Model(model),
		MaxTokens: int64(max),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{
			Text:         req.System,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}}
	}
	for i, t := range req.Tools {
		tp, err := toolParam(t)
		if err != nil {
			return params, err
		}
		if i == len(req.Tools)-1 {
			tp.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &tp})
	}
	for _, m := range req.Messages {
		if mp, ok := messageParam(m); ok {
			params.Messages = append(params.Messages, mp)
		}
	}
	return params, nil
}

func toolParam(t llm.Tool) (sdk.ToolParam, error) {
	// A zero input schema is omitted whole by the SDK, and the API requires
	// type "object"; empty properties keep the struct (and its type) alive.
	tp := sdk.ToolParam{Name: t.Name}
	tp.InputSchema.Properties = map[string]any{}
	if t.Description != "" {
		tp.Description = sdk.String(t.Description)
	}
	if len(t.InputSchema) == 0 {
		return tp, nil
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
		return tp, fmt.Errorf("tool %s: input schema: %w", t.Name, err)
	}
	delete(schema, "type")
	if raw, ok := schema["properties"]; ok {
		tp.InputSchema.Properties = raw
		delete(schema, "properties")
	}
	if raw, ok := schema["required"]; ok {
		if err := json.Unmarshal(raw, &tp.InputSchema.Required); err != nil {
			return tp, fmt.Errorf("tool %s: input schema required: %w", t.Name, err)
		}
		delete(schema, "required")
	}
	if len(schema) > 0 {
		tp.InputSchema.ExtraFields = make(map[string]any, len(schema))
		for k, v := range schema {
			tp.InputSchema.ExtraFields[k] = v
		}
	}
	return tp, nil
}

// messageParam maps a history message. Tool results go first in a user
// message, as the API requires; empty text is dropped (the API rejects it).
func messageParam(m llm.Message) (sdk.MessageParam, bool) {
	var results, rest []sdk.ContentBlockParamUnion
	for _, part := range m.Parts {
		switch p := part.(type) {
		case llm.Text:
			if p.Text != "" {
				rest = append(rest, sdk.NewTextBlock(p.Text))
			}
		case llm.ToolUse:
			args := p.Args
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			rest = append(rest, sdk.NewToolUseBlock(p.ID, args, p.Name))
		case llm.ToolResult:
			results = append(results, sdk.NewToolResultBlock(p.ID, p.Text, p.IsError))
		}
	}
	blocks := append(results, rest...)
	if len(blocks) == 0 {
		return sdk.MessageParam{}, false
	}
	if m.Role == llm.RoleAssistant {
		return sdk.NewAssistantMessage(blocks...), true
	}
	return sdk.NewUserMessage(blocks...), true
}

type toolBlock struct {
	id, name string
	args     strings.Builder
}

type stream struct {
	ctx    context.Context
	p      *Provider
	s      *ssestream.Stream[sdk.MessageStreamEventUnion]
	blocks map[int64]*toolBlock
	queue  []llm.Event
	usage  llm.Usage
	reason string
	done   bool
}

func (s *stream) Close() error { return s.s.Close() }

func (s *stream) Next() (llm.Event, error) {
	for {
		if len(s.queue) > 0 {
			ev := s.queue[0]
			s.queue = s.queue[1:]
			return ev, nil
		}
		if s.done {
			return nil, io.EOF
		}
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if !s.s.Next() {
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
			if err := s.s.Err(); err != nil {
				return nil, s.p.mapErr(s.ctx, err)
			}
			return nil, &llm.APIError{Message: "stream ended before message_stop"}
		}
		if err := s.handle(s.s.Current()); err != nil {
			return nil, err
		}
	}
}

func (s *stream) handle(ev sdk.MessageStreamEventUnion) error {
	switch ev.Type {
	case "message_start":
		u := ev.Message.Usage
		s.usage.In = int(u.InputTokens + u.CacheCreationInputTokens)
		s.usage.Cached = int(u.CacheReadInputTokens)
		s.usage.Out = int(u.OutputTokens)
	case "content_block_start":
		cb := ev.ContentBlock
		switch cb.Type {
		case "text":
			if cb.Text != "" {
				s.queue = append(s.queue, llm.TextDelta{Text: cb.Text})
			}
		case "tool_use":
			s.blocks[ev.Index] = &toolBlock{id: cb.ID, name: cb.Name}
		}
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				s.queue = append(s.queue, llm.TextDelta{Text: ev.Delta.Text})
			}
		case "input_json_delta":
			if b := s.blocks[ev.Index]; b != nil {
				b.args.WriteString(ev.Delta.PartialJSON)
			}
		}
	case "content_block_stop":
		b := s.blocks[ev.Index]
		if b == nil {
			return nil
		}
		delete(s.blocks, ev.Index)
		args := strings.TrimSpace(b.args.String())
		if args == "" {
			args = "{}"
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(args), &obj) != nil || obj == nil {
			return &llm.APIError{Message: fmt.Sprintf("tool %s: arguments are not a JSON object", b.name)}
		}
		s.queue = append(s.queue, llm.ToolCall{ID: b.id, Name: b.name, Args: json.RawMessage(args)})
	case "message_delta":
		if r := string(ev.Delta.StopReason); r != "" {
			s.reason = r
		}
		u := ev.Usage
		if u.InputTokens > 0 || u.CacheCreationInputTokens > 0 {
			s.usage.In = int(u.InputTokens + u.CacheCreationInputTokens)
		}
		if u.CacheReadInputTokens > 0 {
			s.usage.Cached = int(u.CacheReadInputTokens)
		}
		if u.OutputTokens > 0 {
			s.usage.Out = int(u.OutputTokens)
		}
	case "message_stop":
		s.queue = append(s.queue, s.usage, llm.Stop{Reason: s.reason})
		s.done = true
	}
	return nil
}

// mapErr turns an SDK or transport error into an llm.APIError with the key
// scrubbed. A cancelled or expired ctx is returned as its own error.
func (p *Provider) mapErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ae *sdk.Error
	if errors.As(err, &ae) {
		msg := apiMessage(ae.RawJSON())
		if msg == "" {
			msg = http.StatusText(ae.StatusCode)
		}
		return &llm.APIError{Status: ae.StatusCode, Message: p.scrub(msg)}
	}
	return &llm.APIError{Message: p.scrub(err.Error())}
}

// apiMessage reads error.message from {"type":"error","error":{...}}.
func apiMessage(raw string) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return ""
	}
	return body.Error.Message
}

func (p *Provider) scrub(s string) string {
	if p.key != "" {
		s = strings.ReplaceAll(s, p.key, "[redacted]")
	}
	r := []rune(s)
	if len(r) > maxMessageLen {
		s = string(r[:maxMessageLen]) + "..."
	}
	return s
}
