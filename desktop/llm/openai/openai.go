// Package openai adapts the OpenAI Responses API to llm.Provider.
//
// What differs from the other adapters:
//   - Requests are stateless (store: false): nothing is kept on OpenAI's side,
//     so every turn sends the whole history. Reasoning is replayed through
//     the encrypted reasoning items the API returns when include names
//     reasoning.encrypted_content; each one is stored as llm.Thinking with
//     Provider openai, Signature the item id and Data the encrypted content.
//     They go back without their id, like the item ids of messages and
//     function calls, since no item exists on the server to refer to.
//   - The stream is a sequence of output items. Text and function call
//     arguments arrive as deltas per output_index; response.output_item.done
//     carries each finished item, which is what the history keeps.
//   - Usage: In is input_tokens minus input_tokens_details.cached_tokens,
//     Cached is cached_tokens (served from the prompt cache), CacheWrite is
//     input_tokens_details.cache_write_tokens, and Out is output_tokens
//     (reasoning tokens included).
//
// The SDK's ResponseAccumulator only takes Responses WebSocket events, not the
// SSE events of NewStreaming, so the stream is assembled here.
//
// Retries follow the other adapters: the SDK retries only until the response
// headers arrive, errors after that are returned from Next.
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// BaseURL is the one host the adapter talks to outside tests.
const BaseURL = "https://api.openai.com/v1"

const (
	maxMessageLen = 300
	maxRawArgsLen = 200
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

// Provider is the OpenAI llm.Provider.
type Provider struct {
	models    sdk.ModelService
	responses responses.ResponseService
	key       string
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that authenticates with apiKey. The key goes only in
// the Authorization header, and redirects are not followed.
//
// The services are built directly, not through sdk.NewClient: NewClient
// always applies the SDK's environment defaults (OPENAI_BASE_URL,
// OPENAI_API_KEY, OPENAI_ADMIN_KEY, OPENAI_ORG_ID, OPENAI_PROJECT_ID,
// OPENAI_WEBHOOK_SECRET, OPENAI_CUSTOM_HEADERS), and the switch that turns
// them off is internal to the SDK. So no OPENAI_* variable is read at all.
func New(apiKey string, opts ...Option) *Provider {
	cfg := config{baseURL: BaseURL, maxRetries: 2}
	for _, o := range opts {
		o(&cfg)
	}
	ro := []option.RequestOption{
		option.WithHTTPClient(llm.NoRedirectClient(cfg.httpClient)),
		option.WithBaseURL(cfg.baseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(cfg.maxRetries),
	}
	return &Provider{models: sdk.NewModelService(ro...), responses: responses.NewResponseService(ro...), key: apiKey}
}

// Models lists the models that can chat with tools through Responses, newest
// first (by the list's created time). The Models API has no capability
// field, so the list is narrowed by id: gpt-* and the o-series, minus audio,
// realtime, speech, image, search, embedding, moderation, instruct and
// deep-research models, and minus o1-mini and o1-preview (no tools, no
// system prompt). chatgpt-* models are left out: they are ChatGPT's chat
// snapshots, without function calling.
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	pager := p.models.ListAutoPaging(ctx)
	type entry struct {
		id      string
		created int64
	}
	var list []entry
	for pager.Next() {
		m := pager.Current()
		if chatModel(m.ID) {
			list = append(list, entry{m.ID, m.Created})
		}
	}
	if err := pager.Err(); err != nil {
		return nil, p.mapErr(ctx, err)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].created != list[j].created {
			return list[i].created > list[j].created
		}
		return list[i].id < list[j].id
	})
	out := make([]llm.Model, len(list))
	for i, e := range list {
		out[i] = llm.Model{ID: e.id, Label: e.id}
	}
	return out, nil
}

func chatModel(id string) bool {
	id = strings.ToLower(id)
	family := strings.HasPrefix(id, "gpt-") ||
		(len(id) > 1 && id[0] == 'o' && id[1] >= '0' && id[1] <= '9')
	if !family || strings.HasPrefix(id, "o1-mini") || strings.HasPrefix(id, "o1-preview") {
		return false
	}
	for _, s := range []string{"audio", "realtime", "transcribe", "tts", "image", "search", "embedding", "moderation", "instruct", "deep-research"} {
		if strings.Contains(id, s) {
			return false
		}
	}
	return true
}

// Stream starts one streaming Responses request.
func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	params, err := buildParams(ctx, req)
	if err != nil {
		return nil, err
	}
	s := p.responses.NewStreaming(ctx, params)
	if err := s.Err(); err != nil {
		_ = s.Close()
		return nil, p.mapErr(ctx, err)
	}
	return &stream{ctx: ctx, p: p, s: s, items: map[int64]*item{}}, nil
}

// buildParams maps a request. The model is required: the app takes it from
// the provider's model list, there is no built-in default.
func buildParams(ctx context.Context, req llm.Request) (responses.ResponseNewParams, error) {
	var params responses.ResponseNewParams
	if req.Model == "" {
		return params, errors.New("no model selected")
	}
	params.Model = req.Model
	params.Store = param.NewOpt(false)
	params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	if req.System != "" {
		params.Instructions = param.NewOpt(req.System)
	}
	if req.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(req.MaxTokens))
	}
	for _, t := range req.Tools {
		ft, err := toolParam(t)
		if err != nil {
			return params, err
		}
		params.Tools = append(params.Tools, responses.ToolUnionParam{OfFunction: &ft})
	}
	items, err := inputItems(ctx, req.Images, req.Messages)
	if err != nil {
		return params, err
	}
	params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: items}
	return params, nil
}

// toolParam maps a tool. strict stays off: the ffc schemas have optional
// properties, which strict mode does not allow.
func toolParam(t llm.Tool) (responses.FunctionToolParam, error) {
	ft := responses.FunctionToolParam{
		Name:       t.Name,
		Strict:     param.NewOpt(false),
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	if t.Description != "" {
		ft.Description = param.NewOpt(t.Description)
	}
	if len(t.InputSchema) == 0 {
		return ft, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
		return ft, fmt.Errorf("tool %s: input schema: %w", t.Name, err)
	}
	if schema != nil {
		ft.Parameters = schema
	}
	return ft, nil
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

// inputItems maps the history to input items, in order.
//   - An assistant message gives, part by part: its own reasoning items
//     (another provider's Thinking is skipped), its text as an assistant
//     message, and a function_call per ToolUse (a ToolUse without an id gets
//     "call_<position>").
//   - The function_call_output items for those calls come next, one per call
//     in call order: the API refuses a call without an output and an output
//     without a call. A call with no result gets "Error: no result"; a result
//     that matches no call is dropped; a result with an empty id answers the
//     next call that had none. IsError results are prefixed "Error: ".
//   - A user message's text and images follow as one user message.
func inputItems(ctx context.Context, images llm.ImageResolver, in []llm.Message) (responses.ResponseInputParam, error) {
	msgs := mergeSameRole(in)
	var out responses.ResponseInputParam
	var pending []string // call ids of the last assistant message
	var idless []bool    // per pending call: it was stored without an id
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			pending, idless = nil, nil
			var text strings.Builder
			flush := func() {
				if text.Len() > 0 {
					out = append(out, responses.ResponseInputItemParamOfMessage(text.String(), responses.EasyInputMessageRoleAssistant))
					text.Reset()
				}
			}
			for n, part := range m.Parts {
				switch p := part.(type) {
				case llm.Text:
					text.WriteString(p.Text)
				case llm.Thinking:
					if llm.ForeignThinking(p, llm.ProviderOpenAI) || p.Data == "" {
						continue
					}
					flush()
					out = append(out, reasoningItem(p.Data))
				case llm.ToolUse:
					flush()
					id := p.ID
					if id == "" {
						id = "call_" + strconv.Itoa(n)
					}
					args := string(p.Args)
					if len(p.Args) == 0 || !json.Valid(p.Args) {
						args = "{}"
					}
					pending, idless = append(pending, id), append(idless, p.ID == "")
					out = append(out, responses.ResponseInputItemParamOfFunctionCall(args, id, p.Name))
				}
			}
			flush()
			continue
		}
		results := map[string]string{}
		var anon []string // results with an empty id, in order
		for _, part := range m.Parts {
			if r, ok := part.(llm.ToolResult); ok {
				if r.ID == "" {
					anon = append(anon, resultText(r))
				} else if _, dup := results[r.ID]; !dup {
					results[r.ID] = resultText(r)
				}
			}
		}
		for n, id := range pending {
			c, ok := results[id]
			if !ok && idless[n] && len(anon) > 0 {
				// A call stored without an id takes the next result without one.
				c, ok, anon = anon[0], true, anon[1:]
			}
			if !ok {
				c = "Error: no result"
			}
			out = append(out, functionOutput(id, c))
		}
		pending, idless = nil, nil
		um, ok, err := userMessage(ctx, images, m)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, um)
		}
	}
	// A history that ends with calls still gets their outputs.
	for _, id := range pending {
		out = append(out, functionOutput(id, "Error: no result"))
	}
	return out, nil
}

// reasoningItem replays an encrypted reasoning item: type, an empty summary
// (required) and the encrypted content, with no id. The SDK's struct always
// writes its required id, so the item is sent as raw JSON.
func reasoningItem(data string) responses.ResponseInputItemUnionParam {
	raw, _ := json.Marshal(map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": data})
	r := param.Override[responses.ResponseReasoningItemParam](json.RawMessage(raw))
	return responses.ResponseInputItemUnionParam{OfReasoning: &r}
}

func functionOutput(id, text string) responses.ResponseInputItemUnionParam {
	o := responses.ResponseInputItemParamOfFunctionCallOutput(text)
	o.OfFunctionCallOutput.CallID = param.NewOpt(id)
	return o
}

func resultText(r llm.ToolResult) string {
	if r.IsError {
		return "Error: " + r.Text
	}
	return r.Text
}

// userMessage maps the text, images and PDF files of a user message; ok is
// false when it has none.
func userMessage(ctx context.Context, images llm.ImageResolver, m llm.Message) (responses.ResponseInputItemUnionParam, bool, error) {
	var text strings.Builder
	var imgs responses.ResponseInputMessageContentListParam
	for _, part := range m.Parts {
		switch p := part.(type) {
		case llm.Text:
			text.WriteString(p.Text)
		case llm.Image:
			b, err := llm.ImageBytes(ctx, images, p)
			if err != nil {
				return responses.ResponseInputItemUnionParam{}, false, err
			}
			img := responses.ResponseInputImageParam{
				Detail:   responses.ResponseInputImageDetailAuto,
				ImageURL: param.NewOpt("data:" + p.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b)),
			}
			imgs = append(imgs, responses.ResponseInputContentUnionParam{OfInputImage: &img})
		case llm.Document:
			b, err := llm.DocumentBytes(ctx, images, p)
			if err != nil {
				return responses.ResponseInputItemUnionParam{}, false, err
			}
			name := p.Name
			if name == "" {
				name = "document.pdf"
			}
			f := responses.ResponseInputFileParam{
				Filename: param.NewOpt(name),
				FileData: param.NewOpt("data:application/pdf;base64," + base64.StdEncoding.EncodeToString(b)),
			}
			imgs = append(imgs, responses.ResponseInputContentUnionParam{OfInputFile: &f})
		}
	}
	if len(imgs) == 0 {
		if text.Len() == 0 {
			return responses.ResponseInputItemUnionParam{}, false, nil
		}
		return responses.ResponseInputItemParamOfMessage(text.String(), responses.EasyInputMessageRoleUser), true, nil
	}
	var content responses.ResponseInputMessageContentListParam
	if text.Len() > 0 {
		content = append(content, responses.ResponseInputContentParamOfInputText(text.String()))
	}
	return responses.ResponseInputItemParamOfMessage(append(content, imgs...), responses.EasyInputMessageRoleUser), true, nil
}

// item accumulates one output item, keyed by its output_index.
type item struct {
	kind     string // message, function_call, reasoning, or another type
	id       string
	callID   string
	name     string
	text     strings.Builder // message text, or function call arguments
	refusal  strings.Builder
	data     string // reasoning encrypted_content
	done     bool   // response.output_item.done seen
	finished bool   // the done item's text replaced the deltas
}

type stream struct {
	ctx     context.Context
	p       *Provider
	s       *ssestream.Stream[responses.ResponseStreamEventUnion]
	items   map[int64]*item
	order   []int64 // output indexes in the order they started
	queue   []llm.Event
	usage   llm.Usage
	reason  string
	refused bool
	done    bool
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
			// A cut connection looks like a bad gateway: worth a retry.
			return nil, &llm.APIError{Status: 502, Message: "stream ended before response.completed"}
		}
		if err := s.handle(s.s.Current()); err != nil {
			s.done = true
			return nil, err
		}
	}
}

func (s *stream) at(index int64) *item {
	it := s.items[index]
	if it == nil {
		it = &item{}
		s.items[index] = it
		s.order = append(s.order, index)
	}
	return it
}

func (s *stream) handle(ev responses.ResponseStreamEventUnion) error {
	switch ev.Type {
	case "response.output_item.added":
		it := s.at(ev.OutputIndex)
		it.kind, it.id, it.callID, it.name = ev.Item.Type, ev.Item.ID, ev.Item.CallID, ev.Item.Name
	case "response.output_text.delta":
		it := s.at(ev.OutputIndex)
		if ev.Delta != "" && !it.finished {
			it.text.WriteString(ev.Delta)
			s.queue = append(s.queue, llm.TextDelta{Text: ev.Delta})
		}
	case "response.refusal.delta":
		it := s.at(ev.OutputIndex)
		it.refusal.WriteString(ev.Delta)
		s.refused = true
	case "response.function_call_arguments.delta":
		it := s.at(ev.OutputIndex)
		if !it.finished {
			it.text.WriteString(ev.Delta)
		}
	case "response.output_item.done":
		s.finish(ev.OutputIndex, ev.Item)
	case "response.completed", "response.incomplete":
		s.setUsage(ev.Response.Usage)
		s.reason = llm.StopEndTurn
		if ev.Type == "response.incomplete" {
			switch ev.Response.IncompleteDetails.Reason {
			case "max_output_tokens":
				s.reason = llm.StopMaxTokens
			case "content_filter":
				s.reason = llm.StopRefusal
			default:
				s.reason = ev.Response.IncompleteDetails.Reason
			}
		}
		s.stop()
	case "response.failed":
		e := ev.Response.Error
		msg := e.Message
		if msg == "" {
			msg = "the response failed"
		}
		return &llm.APIError{Status: statusFromCode(string(e.Code)), Message: s.p.scrub(msg)}
	case "error":
		msg := ev.Message
		if msg == "" {
			msg = "the stream reported an error"
		}
		return &llm.APIError{Status: statusFromCode(ev.Code), Message: s.p.scrub(msg)}
	}
	return nil
}

// finish records a finished output item. Its content is authoritative: the
// text and arguments in it replace what the deltas built. A reasoning item
// is emitted as Thinking now, like a finished thinking block elsewhere.
func (s *stream) finish(index int64, oi responses.ResponseOutputItemUnion) {
	it := s.at(index)
	it.kind, it.done = oi.Type, true
	if oi.ID != "" {
		it.id = oi.ID
	}
	switch oi.Type {
	case "message":
		var text, refusal strings.Builder
		for _, c := range oi.Content {
			switch c.Type {
			case "output_text":
				text.WriteString(c.Text)
			case "refusal":
				refusal.WriteString(c.Refusal)
			}
		}
		if text.Len() > 0 || refusal.Len() > 0 {
			it.text.Reset()
			it.text.WriteString(text.String())
			it.refusal.Reset()
			it.refusal.WriteString(refusal.String())
			it.finished = true
		}
		if refusal.Len() > 0 {
			s.refused = true
		}
	case "function_call":
		if oi.CallID != "" {
			it.callID = oi.CallID
		}
		if oi.Name != "" {
			it.name = oi.Name
		}
		if a := oi.Arguments.OfString; a != "" {
			it.text.Reset()
			it.text.WriteString(a)
			it.finished = true
		}
	case "reasoning":
		it.data = oi.EncryptedContent
		if it.data != "" {
			s.queue = append(s.queue, llm.Thinking{Provider: llm.ProviderOpenAI, Signature: it.id, Data: it.data})
		}
	}
}

func (s *stream) setUsage(u responses.ResponseUsage) {
	cached := int(u.InputTokensDetails.CachedTokens)
	s.usage = llm.Usage{
		In:         int(u.InputTokens) - cached,
		Out:        int(u.OutputTokens),
		Cached:     cached,
		CacheWrite: int(u.InputTokensDetails.CacheWriteTokens),
	}
}

// stop ends the turn. Tool calls go out in output order, then usage, then
// Stop carrying the assistant message (reasoning, text and tool uses in
// output order). After max_tokens, items that did not finish and calls whose
// arguments are not a JSON object are cut-off output and dropped; after any
// other reason an unusable call reaches the loop with ArgsError set. A
// refusal ends the turn as StopRefusal with the refusal text as the answer.
func (s *stream) stop() {
	reason := s.reason
	var calls int
	for _, idx := range s.order {
		if s.items[idx].kind == "function_call" {
			calls++
		}
	}
	if reason == llm.StopEndTurn && calls > 0 {
		reason = llm.StopToolUse
	}
	if s.refused && reason == llm.StopEndTurn {
		reason = llm.StopRefusal
	}
	cut := reason == llm.StopMaxTokens
	sort.Slice(s.order, func(i, j int) bool { return s.order[i] < s.order[j] })
	var parts []llm.Part
	for _, idx := range s.order {
		it := s.items[idx]
		switch it.kind {
		case "reasoning":
			if it.done && it.data != "" {
				parts = append(parts, llm.Thinking{Provider: llm.ProviderOpenAI, Signature: it.id, Data: it.data})
			}
		case "message":
			t := it.text.String()
			if r := it.refusal.String(); r != "" {
				if t == "" {
					// A refusal streams no text deltas: show it now.
					s.queue = append(s.queue, llm.TextDelta{Text: r})
				}
				t += r
			}
			if t != "" {
				parts = append(parts, llm.Text{Text: t})
			}
		case "function_call":
			tc, ok := toolCall(it)
			if cut && (!ok || !it.done) {
				continue
			}
			s.queue = append(s.queue, tc)
			parts = append(parts, llm.ToolUse{ID: tc.ID, Name: tc.Name, Args: tc.Args})
		}
	}
	st := llm.Stop{Reason: reason}
	if len(parts) > 0 {
		st.Message = llm.Message{Role: llm.RoleAssistant, Parts: parts}
	}
	s.queue = append(s.queue, s.usage, st)
	s.items, s.order = map[int64]*item{}, nil
	s.done = true
}

// toolCall assembles a call. ok is false when its arguments are not a JSON
// object (or the item never finished); ArgsError is then set and Args is the
// raw text if that is valid JSON, else "{}".
func toolCall(it *item) (tc llm.ToolCall, ok bool) {
	raw := strings.TrimSpace(it.text.String())
	if raw == "" {
		raw = "{}"
	}
	tc = llm.ToolCall{ID: it.callID, Name: it.name, Args: json.RawMessage(raw)}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &obj) == nil && obj != nil {
		if it.done {
			return tc, true
		}
		tc.ArgsError = "the arguments were cut off before they were complete"
		return tc, false
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
		msg, code, kind := errorFields(errorMember(se.Event.Data))
		status := code
		if status < 400 {
			status = statusFromCode(kind)
		}
		if msg == "" {
			msg = "the stream reported an error"
		}
		return &llm.APIError{Status: status, Message: p.scrub(msg)}
	}
	// Transport failure (connection reset, DNS, decode): retryable.
	return &llm.APIError{Status: 502, Message: p.scrub(err.Error())}
}

// errorMember returns the "error" member of an SSE event, or the event itself
// when it has none.
func errorMember(data []byte) string {
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && len(body.Error) > 0 {
		return string(body.Error)
	}
	return string(data)
}

// errorFields reads message, a numeric code and the type from an error
// object; a string code is appended to the type.
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
		typ += " " + c
	}
	return body.Message, code, typ
}

// statusFromCode maps the error code or type of a failed response or an
// error event to the HTTP status the same error carries outside a stream, so
// llm.IsRetryable and llm.IsAuth classify it. An unknown code is a 400.
func statusFromCode(c string) int {
	switch {
	case strings.Contains(c, "rate_limit"), strings.Contains(c, "insufficient_quota"):
		return 429
	case strings.Contains(c, "overloaded"):
		return 529
	case strings.Contains(c, "server_error"), strings.Contains(c, "api_error"), strings.Contains(c, "timeout"):
		return 500
	case strings.Contains(c, "invalid_api_key"), strings.Contains(c, "authentication"):
		return 401
	}
	return 400
}

func (p *Provider) scrub(s string) string {
	if p.key != "" {
		s = strings.ReplaceAll(s, p.key, "[redacted]")
	}
	return clip(s, maxMessageLen)
}
