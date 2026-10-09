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
//     minus the cached part, so In + Cached is the whole prompt; CacheWrite is
//     prompt_tokens_details.cache_write_tokens when the server sends it.
//
// Retries follow the Anthropic adapter: the SDK retries only until the
// response headers arrive, errors after that are returned from Next.
package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	// appName and appURL are the attribution OpenRouter shows for the app
	// (X-Title and HTTP-Referer).
	appName = "Foxmayn Frappe Desktop"
	appURL  = "https://github.com/nasroykh/foxmayn_frappe_cli"
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
	models sdk.ModelService
	chat   sdk.ChatService
	key    string
	preset Preset
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider for preset. A non-empty apiKey goes only in the
// Authorization header; with no key no credential header is sent at all.
// Redirects are not followed.
//
// The services are built directly, not through sdk.NewClient, which always
// applies the SDK's OPENAI_* environment defaults (base URL, keys,
// organization, project, custom headers) through a switch the SDK keeps
// internal. So no OPENAI_* variable is read at all.
func New(preset Preset, apiKey string, opts ...Option) *Provider {
	cfg := config{maxRetries: 2}
	for _, o := range opts {
		o(&cfg)
	}
	ro := []option.RequestOption{
		option.WithHTTPClient(llm.NoRedirectClient(cfg.httpClient)),
		option.WithBaseURL(preset.BaseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(cfg.maxRetries),
	}
	if preset.ID == OpenRouter.ID {
		ro = append(ro, option.WithHeader("HTTP-Referer", appURL), option.WithHeader("X-Title", appName))
	}
	return &Provider{models: sdk.NewModelService(ro...), chat: sdk.NewChatService(ro...), key: apiKey, preset: preset}
}

// Models lists the models the server offers. OpenRouter lists hundreds, many
// without tool support, so there only models whose supported_parameters
// include "tools" are kept (all of them when a model has no such field).
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	pager := p.models.ListAutoPaging(ctx)
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
	params, err := buildParams(ctx, req)
	if err != nil {
		return nil, err
	}
	s := p.chat.Completions.NewStreaming(ctx, params)
	if err := s.Err(); err != nil {
		_ = s.Close()
		return nil, p.mapErr(ctx, err)
	}
	return &stream{ctx: ctx, p: p, s: s, byIndex: map[int64]*call{}}, nil
}

// buildParams maps a request. The model is required: unlike a hosted vendor,
// these servers have no default to fall back on. max_tokens is sent only when
// set (every server understands it; max_completion_tokens is newer). Image
// parts are read through req.Images here.
func buildParams(ctx context.Context, req llm.Request) (sdk.ChatCompletionNewParams, error) {
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
	hist, err := historyParams(ctx, req.Images, req.Messages)
	if err != nil {
		return params, err
	}
	params.Messages = append(params.Messages, hist...)
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

// mergeSameRole joins consecutive messages of one role. It copies; the
// caller's messages are not touched.
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

// historyParams maps the history to chat messages.
//   - Thinking parts are dropped, whatever their Provider (these servers have
//     no reasoning blocks to replay); an assistant turn with nothing else
//     sends nothing.
//   - A user message with images sends content parts (text, then each image
//     as a data: URL), else plain text.
//   - An assistant message gives one message with its text and tool_calls (a
//     ToolUse without an id gets "call_<position>").
//   - The tool messages for those calls come directly after it, one per call
//     in the call order, as the API requires. A call with no result gets
//     "Error: no result"; a result that matches no call is dropped. A result
//     with an empty id answers the next call that had none.
//   - The remaining text of the user message that held the results follows as
//     a user message.
//
// IsError results are prefixed "Error: ", since the chat API has no flag.
func historyParams(ctx context.Context, images llm.ImageResolver, in []llm.Message) ([]sdk.ChatCompletionMessageParamUnion, error) {
	msgs := mergeSameRole(in)
	var out []sdk.ChatCompletionMessageParamUnion
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role != llm.RoleAssistant {
			um, ok, err := userMessage(ctx, images, m)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, um)
			}
			continue
		}
		var text strings.Builder
		var calls []sdk.ChatCompletionMessageToolCallUnionParam
		var ids, origIDs []string
		for n, part := range m.Parts {
			switch p := part.(type) {
			case llm.Text:
				text.WriteString(p.Text)
			case llm.ToolUse:
				id := p.ID
				if id == "" {
					id = "call_" + strconv.Itoa(n)
				}
				args := string(p.Args)
				if len(p.Args) == 0 || !json.Valid(p.Args) {
					args = "{}"
				}
				ids, origIDs = append(ids, id), append(origIDs, p.ID)
				calls = append(calls, sdk.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
						ID:       id,
						Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{Name: p.Name, Arguments: args},
					},
				})
			}
		}
		if text.Len() > 0 || len(calls) > 0 {
			a := sdk.ChatCompletionAssistantMessageParam{ToolCalls: calls}
			if text.Len() > 0 {
				a.Content.OfString = sdk.String(text.String())
			}
			out = append(out, sdk.ChatCompletionMessageParamUnion{OfAssistant: &a})
		}
		if len(ids) == 0 {
			continue
		}
		var follow *llm.Message
		if i+1 < len(msgs) && msgs[i+1].Role == llm.RoleUser {
			follow = &msgs[i+1]
			i++
		}
		results := map[string]string{}
		var anon []string // results with an empty id, in order
		if follow != nil {
			for _, part := range follow.Parts {
				if r, ok := part.(llm.ToolResult); ok {
					c := resultText(r)
					if r.ID == "" {
						anon = append(anon, c)
					} else if _, dup := results[r.ID]; !dup {
						results[r.ID] = c
					}
				}
			}
		}
		for n, id := range ids {
			c, ok := results[id]
			if !ok && origIDs[n] == "" && len(anon) > 0 {
				c, ok, anon = anon[0], true, anon[1:]
			}
			if !ok {
				c = "Error: no result"
			}
			out = append(out, sdk.ToolMessage(c, id))
		}
		if follow != nil {
			um, ok, err := userMessage(ctx, images, *follow)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, um)
			}
		}
	}
	return out, nil
}

// userMessage maps the text and images of a user message; ok is false when
// it has neither (tool results go elsewhere).
func userMessage(ctx context.Context, images llm.ImageResolver, m llm.Message) (sdk.ChatCompletionMessageParamUnion, bool, error) {
	t := userText(m)
	var imgs []sdk.ChatCompletionContentPartUnionParam
	for _, part := range m.Parts {
		img, ok := part.(llm.Image)
		if !ok {
			continue
		}
		b, err := llm.ImageBytes(ctx, images, img)
		if err != nil {
			return sdk.ChatCompletionMessageParamUnion{}, false, err
		}
		imgs = append(imgs, sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{
			URL: "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b),
		}))
	}
	if len(imgs) == 0 {
		if t == "" {
			return sdk.ChatCompletionMessageParamUnion{}, false, nil
		}
		return sdk.UserMessage(t), true, nil
	}
	var parts []sdk.ChatCompletionContentPartUnionParam
	if t != "" {
		parts = append(parts, sdk.TextContentPart(t))
	}
	return sdk.UserMessage(append(parts, imgs...)), true, nil
}

func resultText(r llm.ToolResult) string {
	if r.IsError {
		return "Error: " + r.Text
	}
	return r.Text
}

func userText(m llm.Message) string {
	var b strings.Builder
	for _, part := range m.Parts {
		if t, ok := part.(llm.Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
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
	calls     []*call         // tool calls in the order they started
	byIndex   map[int64]*call // the wire index's current call
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
				// A cut connection looks like a bad gateway: worth a retry.
				return nil, &llm.APIError{Status: 502, Message: "stream ended before finish_reason"}
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
		// OpenRouter reports cache writes (Anthropic and Gemini routes) as
		// prompt_tokens_details.cache_write_tokens, a part of the uncached
		// prompt; other servers leave it out (0).
		s.usage.CacheWrite = int(u.PromptTokensDetails.CacheWriteTokens)
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
	// Only the first choice is used (n is never set); skip any other.
	c := ch.Choices[0]
	if c.Index != 0 {
		return
	}
	// Reasoning text (delta.reasoning, delta.reasoning_content) is dropped on
	// purpose: it is neither shown nor replayed, so no field of the delta
	// other than content and tool_calls is read.
	if t := c.Delta.Content; t != "" {
		s.text.WriteString(t)
		s.queue = append(s.queue, llm.TextDelta{Text: t})
	}
	for _, d := range c.Delta.ToolCalls {
		// A call is keyed by its wire index, but some servers (Gemini, some
		// Ollama routes) send index 0 for every call: a new non-empty id on
		// a slot that already has another id starts a new call.
		cl := s.byIndex[d.Index]
		if cl == nil || (d.ID != "" && cl.id != "" && d.ID != cl.id) {
			cl = &call{}
			s.byIndex[d.Index] = cl
			s.calls = append(s.calls, cl)
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
	for i, c := range s.calls {
		tc, ok := toolCall(int64(i), c)
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
	s.calls, s.byIndex = nil, map[int64]*call{}
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
		msg, code, kind := errorFields(gjsonError(se.Event.Data))
		status := code
		if status < 400 {
			status = statusFromType(kind)
		}
		if msg == "" {
			msg = "the stream reported an error"
		}
		return &llm.APIError{Status: status, Message: p.scrub(msg)}
	}
	// Transport failure (connection reset, DNS, decode): retryable.
	return &llm.APIError{Status: 502, Message: p.scrub(err.Error())}
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

// errorFields reads message, a numeric code (OpenRouter style) and the type from
// an error object; a string code is appended to the type. A string error body is its own message.
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
	typ = body.Type
	c := strings.Trim(string(body.Code), `"`)
	if n, err := strconv.Atoi(c); err == nil {
		code = n
	} else if c != "null" && c != "" {
		typ += " " + c // a string code such as "rate_limit_exceeded"
	}
	return body.Message, code, typ
}

// statusFromType maps the error type of a mid-stream error event to the HTTP
// status the same error carries outside a stream.
func statusFromType(t string) int {
	switch {
	case strings.Contains(t, "rate_limit"):
		return 429
	case strings.Contains(t, "overloaded"):
		return 529
	case strings.Contains(t, "server_error"), strings.Contains(t, "api_error"):
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
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	return res.StatusCode == http.StatusOK
}
