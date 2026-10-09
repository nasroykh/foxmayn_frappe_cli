// Package openaicompat adapts OpenAI-compatible Chat Completions servers
// (OpenRouter, Ollama, LM Studio, any custom base URL) to llm.Provider.
//
// What differs from the Anthropic adapter:
//   - The API has no signed thinking blocks, so llm.Thinking parts in the
//     history are not sent, and the reasoning text some servers stream
//     (delta.reasoning, delta.reasoning_content) is ignored: it is neither
//     emitted nor stored.
//   - There is no end-of-block signal. A tool call is complete when the stream
//     ends; after finish_reason "length" a call whose arguments are not a JSON
//     object is cut-off output and dropped.
//   - Usage is emitted only when the server reports it. In is prompt_tokens
//     minus the cached part, so In + Cached is the whole prompt.
//
// Retries follow the Anthropic adapter: the SDK retries only until the
// response headers arrive, errors after that are returned from Next.
package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/shared"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

const (
	maxMessageLen = 300
	maxRawArgsLen = 200
	// appName is the attribution OpenRouter shows for the app.
	appName = "Foxmayn Frappe Desktop"
	// probeTimeout bounds each local-server probe in DetectLocal.
	probeTimeout = 1500 * time.Millisecond
)

// Preset names a server: where it listens and whether it needs a key.
type Preset struct {
	ID          string
	Label       string
	BaseURL     string
	KeyRequired bool
}

// The known servers. OpenRouter needs a key; the local ones do not.
var (
	OpenRouter = Preset{ID: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", KeyRequired: true}
	Ollama     = Preset{ID: "ollama", Label: "Ollama", BaseURL: "http://localhost:11434/v1"}
	LMStudio   = Preset{ID: "lmstudio", Label: "LM Studio", BaseURL: "http://localhost:1234/v1"}
)

// Custom is a preset for any OpenAI-compatible base URL; the key is optional.
func Custom(baseURL string) Preset {
	return Preset{ID: "custom", Label: "Custom", BaseURL: baseURL}
}

// Option customises New.
type Option func(*config)

type config struct {
	httpClient *http.Client
	maxRetries int
}

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.httpClient = h } }

// WithMaxRetries sets how often the SDK retries before the stream starts.
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetries = n } }

// Provider is an OpenAI-compatible llm.Provider.
type Provider struct {
	client sdk.Client
	key    string
	preset Preset
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider for preset. A non-empty apiKey goes only in the
// Authorization header; with no key no credential header is sent at all.
// OPENAI_* environment credentials, base URL, organization, project and custom
// headers are overridden or removed.
func New(preset Preset, apiKey string, opts ...Option) *Provider {
	cfg := config{maxRetries: 2}
	for _, o := range opts {
		o(&cfg)
	}
	ro := []option.RequestOption{
		option.WithBaseURL(preset.BaseURL),
		option.WithAPIKey(apiKey),
		option.WithAdminAPIKey(""),
		option.WithOrganization(""),
		option.WithProject(""),
		option.WithHeaderDel("OpenAI-Organization"),
		option.WithHeaderDel("OpenAI-Project"),
		option.WithMaxRetries(cfg.maxRetries),
	}
	// The SDK reads OPENAI_CUSTOM_HEADERS (name: value per line) into its
	// defaults; take those headers back off.
	for _, line := range strings.Split(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		if i := strings.Index(line, ":"); i >= 0 {
			if name := strings.TrimSpace(line[:i]); name != "" {
				ro = append(ro, option.WithHeaderDel(name))
			}
		}
	}
	if preset.ID == OpenRouter.ID {
		ro = append(ro, option.WithHeader("HTTP-Referer", appName), option.WithHeader("X-Title", appName))
	}
	if cfg.httpClient != nil {
		ro = append(ro, option.WithHTTPClient(cfg.httpClient))
	}
	return &Provider{client: sdk.NewClient(ro...), key: apiKey, preset: preset}
}

// Models lists the models the server offers. OpenRouter lists hundreds, many
// without tool support, so there only models whose supported_parameters
// include "tools" are kept (all of them when a model has no such field).
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	pager := p.client.Models.ListAutoPaging(ctx)
	var out []llm.Model
	for pager.Next() {
		m := pager.Current()
		var extra struct {
			Name                string   `json:"name"`
			SupportedParameters []string `json:"supported_parameters"`
		}
		_ = json.Unmarshal([]byte(m.RawJSON()), &extra)
		if p.preset.ID == OpenRouter.ID && extra.SupportedParameters != nil && !contains(extra.SupportedParameters, "tools") {
			continue
		}
		label := extra.Name
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Stream starts one streaming Chat Completions request.
func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}
	s := p.client.Chat.Completions.NewStreaming(ctx, params)
	if err := s.Err(); err != nil {
		_ = s.Close()
		return nil, p.mapErr(ctx, err)
	}
	return &stream{ctx: ctx, p: p, s: s, calls: map[int64]*call{}}, nil
}

// buildParams maps a request. The model is required: unlike a hosted vendor,
// these servers have no default to fall back on. max_tokens is sent only when
// set (every server understands it; max_completion_tokens is newer).
func buildParams(req llm.Request) (sdk.ChatCompletionNewParams, error) {
	var params sdk.ChatCompletionNewParams
	if req.Model == "" {
		return params, errors.New("no model selected")
	}
	params.Model = req.Model
	params.StreamOptions = sdk.ChatCompletionStreamOptionsParam{IncludeUsage: sdk.Bool(true)}
	if req.MaxTokens > 0 {
		params.MaxTokens = sdk.Int(int64(req.MaxTokens))
	}
	if req.System != "" {
		params.Messages = append(params.Messages, sdk.SystemMessage(req.System))
	}
	for _, t := range req.Tools {
		fn, err := toolDef(t)
		if err != nil {
			return params, err
		}
		params.Tools = append(params.Tools, sdk.ChatCompletionFunctionTool(fn))
	}
	for _, m := range req.Messages {
		params.Messages = append(params.Messages, messageParams(m)...)
	}
	return params, nil
}

func toolDef(t llm.Tool) (shared.FunctionDefinitionParam, error) {
	fn := shared.FunctionDefinitionParam{
		Name:       t.Name,
		Parameters: shared.FunctionParameters{"type": "object", "properties": map[string]any{}},
	}
	if t.Description != "" {
		fn.Description = sdk.String(t.Description)
	}
	if len(t.InputSchema) == 0 {
		return fn, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
		return fn, fmt.Errorf("tool %s: input schema: %w", t.Name, err)
	}
	if schema != nil {
		fn.Parameters = schema
	}
	return fn, nil
}

// messageParams maps a history message to chat messages. A user message gives
// one role "tool" message per ToolResult, in order and first (a tool message
// must directly follow the assistant message that made the calls), then the
// remaining text as a user message. An assistant message gives one message
// with its text and tool_calls. Thinking parts are dropped.
func messageParams(m llm.Message) []sdk.ChatCompletionMessageParamUnion {
	var out []sdk.ChatCompletionMessageParamUnion
	var text strings.Builder
	var calls []sdk.ChatCompletionMessageToolCallUnionParam
	for _, part := range m.Parts {
		switch p := part.(type) {
		case llm.Text:
			text.WriteString(p.Text)
		case llm.ToolUse:
			args := string(p.Args)
			if len(p.Args) == 0 || !json.Valid(p.Args) {
				args = "{}"
			}
			calls = append(calls, sdk.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
					ID:       p.ID,
					Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{Name: p.Name, Arguments: args},
				},
			})
		case llm.ToolResult:
			out = append(out, sdk.ToolMessage(p.Text, p.ID))
		}
	}
	if m.Role == llm.RoleAssistant {
		if text.Len() == 0 && len(calls) == 0 {
			return out
		}
		a := sdk.ChatCompletionAssistantMessageParam{ToolCalls: calls}
		if text.Len() > 0 {
			a.Content.OfString = sdk.String(text.String())
		}
		return append(out, sdk.ChatCompletionMessageParamUnion{OfAssistant: &a})
	}
	if text.Len() > 0 {
		out = append(out, sdk.UserMessage(text.String()))
	}
	return out
}

// call accumulates one streamed tool call.
type call struct {
	id, name string
	args     strings.Builder
}

type stream struct {
	ctx       context.Context
	p         *Provider
	s         *ssestream.Stream[sdk.ChatCompletionChunk]
	text      strings.Builder
	calls     map[int64]*call
	queue     []llm.Event
	usage     llm.Usage
	haveUsage bool
	reason    string
	done      bool
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
			// The stream ended cleanly: usage, when sent, came after the
			// finish_reason chunk, so the turn is complete only now.
			if s.reason == "" {
				return nil, &llm.APIError{Message: "stream ended before finish_reason"}
			}
			s.stop()
			continue
		}
		s.handle(s.s.Current())
	}
}

func (s *stream) handle(ch sdk.ChatCompletionChunk) {
	if u := ch.Usage; u.JSON.PromptTokens.Valid() || u.JSON.CompletionTokens.Valid() {
		cached := int(u.PromptTokensDetails.CachedTokens)
		s.usage.In = int(u.PromptTokens) - cached
		s.usage.Out = int(u.CompletionTokens)
		s.usage.Cached = cached
		var extra struct {
			Cost *float64 `json:"cost"`
		}
		if json.Unmarshal([]byte(u.RawJSON()), &extra) == nil {
			s.usage.Cost = extra.Cost
		}
		s.haveUsage = true
	}
	if len(ch.Choices) == 0 {
		return
	}
	c := ch.Choices[0]
	if t := c.Delta.Content; t != "" {
		s.text.WriteString(t)
		s.queue = append(s.queue, llm.TextDelta{Text: t})
	}
	for _, d := range c.Delta.ToolCalls {
		cl := s.calls[d.Index]
		if cl == nil {
			cl = &call{}
			s.calls[d.Index] = cl
		}
		if cl.id == "" {
			cl.id = d.ID
		}
		if cl.name == "" {
			cl.name = d.Function.Name
		}
		cl.args.WriteString(d.Function.Arguments)
	}
	if c.FinishReason != "" {
		s.reason = c.FinishReason
	}
}

// stop ends the turn. Tool calls go out in index order, then usage, then Stop
// carrying the assistant message (text first, then the tool uses). After
// length, calls with unusable arguments are cut-off output and dropped; after
// any other reason they reach the loop with ArgsError set.
func (s *stream) stop() {
	reason := mapReason(s.reason)
	// Ollama (and some others) answer "stop" even when they called tools.
	if reason == llm.StopEndTurn && len(s.calls) > 0 {
		reason = llm.StopToolUse
	}
	cut := reason == llm.StopMaxTokens
	var parts []llm.Part
	if t := s.text.String(); t != "" {
		parts = append(parts, llm.Text{Text: t})
	}
	idx := make([]int64, 0, len(s.calls))
	for i := range s.calls {
		idx = append(idx, i)
	}
	sort.Slice(idx, func(a, b int) bool { return idx[a] < idx[b] })
	for _, i := range idx {
		tc, ok := toolCall(i, s.calls[i])
		if cut && !ok {
			continue
		}
		s.queue = append(s.queue, tc)
		parts = append(parts, llm.ToolUse{ID: tc.ID, Name: tc.Name, Args: tc.Args})
	}
	stop := llm.Stop{Reason: reason}
	if len(parts) > 0 {
		stop.Message = llm.Message{Role: llm.RoleAssistant, Parts: parts}
	}
	if s.haveUsage {
		s.queue = append(s.queue, s.usage)
	}
	s.queue = append(s.queue, stop)
	s.calls = map[int64]*call{}
	s.done = true
}

func mapReason(r string) string {
	switch r {
	case "stop":
		return llm.StopEndTurn
	case "tool_calls", "function_call":
		return llm.StopToolUse
	case "length":
		return llm.StopMaxTokens
	case "content_filter":
		return llm.StopRefusal
	}
	return r
}

// toolCall assembles a call. ok is false when its arguments are not a JSON
// object; ArgsError is then set and Args is the raw text if that is valid
// JSON, else "{}". A server that sends no id gets a synthetic one so results
// can still be matched.
func toolCall(index int64, c *call) (tc llm.ToolCall, ok bool) {
	raw := strings.TrimSpace(c.args.String())
	if raw == "" {
		raw = "{}"
	}
	id := c.id
	if id == "" {
		id = "call_" + strconv.FormatInt(index, 10)
	}
	tc = llm.ToolCall{ID: id, Name: c.name, Args: json.RawMessage(raw)}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &obj) == nil && obj != nil {
		return tc, true
	}
	tc.ArgsError = "arguments are not a JSON object: " + clip(raw, maxRawArgsLen)
	if !json.Valid([]byte(raw)) {
		tc.Args = json.RawMessage("{}")
	}
	return tc, false
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
		msg, _, _ := errorFields(ae.RawJSON())
		if msg == "" {
			msg = http.StatusText(status)
		}
		if msg == "" {
			msg = "the server reported an error"
		}
		return &llm.APIError{Status: status, Message: p.scrub(msg)}
	}
	var se *ssestream.StreamError
	if errors.As(err, &se) {
		msg, code, typ := errorFields(gjsonError(se.Event.Data))
		status := code
		if status < 400 {
			status = statusFromType(typ)
		}
		if msg == "" {
			msg = "the stream reported an error"
		}
		return &llm.APIError{Status: status, Message: p.scrub(msg)}
	}
	return &llm.APIError{Message: p.scrub(err.Error())}
}

// gjsonError returns the "error" member of an SSE error event, or the event
// itself when it has none.
func gjsonError(data []byte) string {
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && len(body.Error) > 0 {
		return string(body.Error)
	}
	return string(data)
}

// errorFields reads message, a numeric code (OpenRouter style) and type from an
// error object. A string error body is its own message.
func errorFields(raw string) (msg string, code int, typ string) {
	var body struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
		Type    string          `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		var s string
		if json.Unmarshal([]byte(raw), &s) == nil {
			return s, 0, ""
		}
		return "", 0, ""
	}
	code, _ = strconv.Atoi(strings.Trim(string(body.Code), `"`))
	return body.Message, code, body.Type
}

// statusFromType maps the error type of a mid-stream error event to the HTTP
// status the same error carries outside a stream.
func statusFromType(t string) int {
	switch {
	case strings.Contains(t, "rate_limit"):
		return 429
	case strings.Contains(t, "overloaded"):
		return 529
	case strings.Contains(t, "server_error"), t == "api_error":
		return 500
	}
	return 0
}

func (p *Provider) scrub(s string) string {
	if p.key != "" {
		s = strings.ReplaceAll(s, p.key, "[redacted]")
	}
	return clip(s, maxMessageLen)
}

// DetectLocal probes the default Ollama and LM Studio addresses in parallel and
// returns the presets whose /models endpoint answers, in that order.
func DetectLocal(ctx context.Context) []Preset {
	return detect(ctx, []Preset{Ollama, LMStudio}, probeTimeout)
}

func detect(ctx context.Context, presets []Preset, timeout time.Duration) []Preset {
	up := make([]bool, len(presets))
	var wg sync.WaitGroup
	for i, pr := range presets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			up[i] = probe(ctx, pr, timeout)
		}()
	}
	wg.Wait()
	var out []Preset
	for i, pr := range presets {
		if up[i] {
			out = append(out, pr)
		}
	}
	return out
}

func probe(ctx context.Context, pr Preset, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(pr.BaseURL, "/")+"/models", nil)
	if err != nil {
		return false
	}
	// A client of its own: the default one honours proxy variables, which would
	// send a localhost probe through a proxy.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	return res.StatusCode == http.StatusOK
}
