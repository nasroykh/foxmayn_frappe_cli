// Package anthropic adapts the Anthropic Messages API to llm.Provider.
//
// Retries are split in two. The SDK retries a request (connection errors, 408,
// 409, 429, 5xx, honouring Retry-After) only until the response headers
// arrive; WithMaxRetries sets that count (default 2). Once the stream has
// started, an error is returned from Next and the loop owns the decision
// (llm.IsRetryable), because the text already shown cannot be taken back.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// DefaultModel is the model the app picks first.
const DefaultModel = "claude-sonnet-5-5"

const (
	defaultBaseURL = "https://api.anthropic.com/"
	// defaultMaxTokens applies when Request.MaxTokens is 0. It leaves room for
	// adaptive thinking plus an answer; the turn streams, so there is no
	// request-timeout concern.
	defaultMaxTokens = 32000
	maxMessageLen    = 300
	maxRawArgsLen    = 200
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

// WithMaxRetries sets how often the SDK retries before the stream starts.
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetries = n } }

// Provider is the Anthropic llm.Provider.
type Provider struct {
	client sdk.Client
	key    string
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that authenticates with apiKey. The key goes only in
// the x-api-key header; ANTHROPIC_* environment credentials, base URL and
// custom headers are overridden or removed.
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
	// The SDK reads ANTHROPIC_CUSTOM_HEADERS (name: value per line) into its
	// defaults when no env credential is set; take those headers back off.
	for _, line := range strings.Split(os.Getenv("ANTHROPIC_CUSTOM_HEADERS"), "\n") {
		if i := strings.Index(line, ":"); i >= 0 {
			if name := strings.TrimSpace(line[:i]); name != "" {
				ro = append(ro, option.WithHeaderDel(name))
			}
		}
	}
	if cfg.httpClient != nil {
		ro = append(ro, option.WithHTTPClient(cfg.httpClient))
	}
	return &Provider{client: sdk.NewClient(ro...), key: apiKey}
}

// Models lists the chat models the account can use (ids starting "claude-").
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	pager := p.client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})
	var out []llm.Model
	for pager.Next() {
		m := pager.Current()
		if !strings.HasPrefix(m.ID, "claude-") {
			continue
		}
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
	return &stream{ctx: ctx, p: p, s: s, blocks: map[int64]*block{}}, nil
}

// buildParams maps a request. Cache breakpoints (three of the four allowed):
// the last tool, the system block, and the conversation tail through the
// top-level automatic cache_control, which the API places on the last
// cacheable block.
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
		Model:        sdk.Model(model),
		MaxTokens:    int64(max),
		CacheControl: sdk.NewCacheControlEphemeralParam(),
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
	for _, m := range mergeSameRole(req.Messages) {
		if mp, ok := messageParam(m); ok {
			params.Messages = append(params.Messages, mp)
		}
	}
	return params, nil
}

// mergeSameRole joins consecutive messages of one role: the API wants
// alternating turns, and a tool result plus the user's next text belong in one
// user message. It copies; req.Messages is not touched.
func mergeSameRole(in []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range in {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Parts = append(out[n-1].Parts[:len(out[n-1].Parts):len(out[n-1].Parts)], m.Parts...)
			continue
		}
		out = append(out, m)
	}
	return out
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

// messageParam maps a history message. Parts keep their order, except that
// tool results come first in a user message, as the API requires. Thinking
// parts are replayed verbatim. Empty text is dropped (the API rejects it).
func messageParam(m llm.Message) (sdk.MessageParam, bool) {
	var results, rest []sdk.ContentBlockParamUnion
	for _, part := range m.Parts {
		switch p := part.(type) {
		case llm.Text:
			if p.Text != "" {
				rest = append(rest, sdk.NewTextBlock(p.Text))
			}
		case llm.Thinking:
			if p.Redacted {
				rest = append(rest, sdk.NewRedactedThinkingBlock(p.Data))
			} else {
				rest = append(rest, sdk.NewThinkingBlock(p.Signature, p.Text))
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

// block accumulates one streamed content block (text, thinking, redacted
// thinking or tool_use), kept in stream order.
type block struct {
	kind     string
	id, name string
	buf      strings.Builder // text, thinking text or tool arguments
	sig      strings.Builder
	data     string // redacted thinking payload
	done     bool   // content_block_stop seen
}

type stream struct {
	ctx    context.Context
	p      *Provider
	s      *ssestream.Stream[sdk.MessageStreamEventUnion]
	blocks map[int64]*block
	order  []*block // every block, in stream (index) order
	queue  []llm.Event
	usage  llm.Usage
	reason string
	cat    string
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
		s.handle(s.s.Current())
	}
}

func (s *stream) open(index int64, b *block) *block {
	s.blocks[index] = b
	s.order = append(s.order, b)
	return b
}

func (s *stream) handle(ev sdk.MessageStreamEventUnion) {
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
			b := s.open(ev.Index, &block{kind: "text"})
			if cb.Text != "" {
				b.buf.WriteString(cb.Text)
				s.queue = append(s.queue, llm.TextDelta{Text: cb.Text})
			}
		case "tool_use":
			s.open(ev.Index, &block{kind: "tool_use", id: cb.ID, name: cb.Name})
		case "thinking":
			b := s.open(ev.Index, &block{kind: "thinking"})
			b.buf.WriteString(cb.Thinking)
			b.sig.WriteString(cb.Signature)
		case "redacted_thinking":
			// Complete in the start event; nothing follows but the stop.
			s.open(ev.Index, &block{kind: "redacted", data: cb.Data, done: true})
			s.queue = append(s.queue, llm.Thinking{Redacted: true, Data: cb.Data})
		}
	case "content_block_delta":
		b := s.blocks[ev.Index]
		if b == nil {
			return
		}
		switch ev.Delta.Type {
		case "text_delta":
			if b.kind == "text" && ev.Delta.Text != "" {
				b.buf.WriteString(ev.Delta.Text)
				s.queue = append(s.queue, llm.TextDelta{Text: ev.Delta.Text})
			}
		case "input_json_delta":
			if b.kind == "tool_use" {
				b.buf.WriteString(ev.Delta.PartialJSON)
			}
		case "thinking_delta":
			if b.kind == "thinking" {
				b.buf.WriteString(ev.Delta.Thinking)
			}
		case "signature_delta":
			if b.kind == "thinking" {
				b.sig.WriteString(ev.Delta.Signature)
			}
		}
	case "content_block_stop":
		b := s.blocks[ev.Index]
		if b == nil {
			return
		}
		delete(s.blocks, ev.Index)
		b.done = true
		if b.kind == "thinking" {
			s.queue = append(s.queue, llm.Thinking{Text: b.buf.String(), Signature: b.sig.String()})
		}
	case "message_delta":
		if r := string(ev.Delta.StopReason); r != "" {
			s.reason = r
		}
		if c := string(ev.Delta.StopDetails.Category); c != "" {
			s.cat = c
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
		s.stop()
	}
}

// stop ends the turn. Tool calls are emitted here, in block order, valid and
// unusable ones alike (the stop reason is known now). The assistant message
// goes out on the Stop event with its parts in block order. After max_tokens,
// tool calls and thinking blocks that did not finish are cut-off output and
// are dropped; after any other reason an unusable call reaches the loop with
// ArgsError set, so it can answer with an error result.
func (s *stream) stop() {
	cut := s.reason == llm.StopMaxTokens
	var parts []llm.Part
	for _, b := range s.order {
		switch b.kind {
		case "text":
			if t := b.buf.String(); t != "" {
				parts = append(parts, llm.Text{Text: t})
			}
		case "thinking":
			if b.done {
				parts = append(parts, llm.Thinking{Text: b.buf.String(), Signature: b.sig.String()})
			}
		case "redacted":
			parts = append(parts, llm.Thinking{Redacted: true, Data: b.data})
		case "tool_use":
			call, ok := toolCall(b)
			if cut && (!ok || !b.done) {
				continue
			}
			s.queue = append(s.queue, call)
			parts = append(parts, llm.ToolUse{ID: call.ID, Name: call.Name, Args: call.Args})
		}
	}
	stop := llm.Stop{Reason: s.reason, Category: s.cat}
	if len(parts) > 0 {
		stop.Message = llm.Message{Role: llm.RoleAssistant, Parts: parts}
	}
	s.queue = append(s.queue, s.usage, stop)
	s.blocks, s.order = map[int64]*block{}, nil
	s.done = true
}

// toolCall assembles a tool block. ok is false when its arguments are not a
// JSON object (or the block never finished); ArgsError is then set and Args is
// the raw text if that is valid JSON, else "{}".
func toolCall(b *block) (call llm.ToolCall, ok bool) {
	raw := strings.TrimSpace(b.buf.String())
	if raw == "" {
		raw = "{}"
	}
	call = llm.ToolCall{ID: b.id, Name: b.name, Args: json.RawMessage(raw)}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &obj) == nil && obj != nil {
		if b.done {
			return call, true
		}
		call.ArgsError = "the arguments were cut off before they were complete"
		return call, false
	}
	call.ArgsError = "arguments are not a JSON object: " + clip(raw, maxRawArgsLen)
	if !json.Valid([]byte(raw)) {
		call.Args = json.RawMessage("{}")
	}
	return call, false
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
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
		status := ae.StatusCode
		if status < 400 {
			// An error event inside a 200 stream: the status is in the type.
			status = statusFromType(string(ae.Type()))
		}
		msg := apiMessage(ae.RawJSON())
		if msg == "" && status != 0 {
			msg = http.StatusText(status)
		}
		if msg == "" {
			msg = "the stream reported an error"
		}
		return &llm.APIError{Status: status, Message: p.scrub(msg)}
	}
	return &llm.APIError{Message: p.scrub(err.Error())}
}

// statusFromType maps the error.type of a mid-stream error event to the HTTP
// status the same error carries outside a stream.
func statusFromType(t string) int {
	switch t {
	case "overloaded_error":
		return 529
	case "rate_limit_error":
		return 429
	case "api_error":
		return 500
	}
	return 0
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
	return clip(s, maxMessageLen)
}
