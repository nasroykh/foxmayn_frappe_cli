// Package gemini adapts the Gemini API (generativelanguage.googleapis.com,
// API key only; never Vertex AI) to llm.Provider through google.golang.org/genai.
//
// What differs from the other adapters:
//   - Thought signatures. A model part (a function call, a text) may carry an
//     opaque thoughtSignature that must go back unchanged on that same part.
//     The adapter stores it as llm.Thinking{Provider: gemini, Signature:
//     standard base64 of the bytes} right before the part it belongs to, so
//     ToolUse and Text keep their shape, and puts it back on that part when
//     it replays the history. A signature that arrives on an empty text part
//     belongs to the text before it and is put there; one with no part to sit
//     on (nothing but the signature) is stored but not sent back, since a
//     part with no data is not valid input.
//   - Function calls arrive whole (the Gemini API does not stream partial
//     arguments; partialArgs is Vertex only), possibly several per chunk and
//     over several chunks. The API usually gives no call id: the adapter makes
//     one ("gemini_" + random hex) and leaves it out when it replays the call
//     and its response. A FunctionResponse is named after the ToolUse it
//     answers.
//   - Usage: In is promptTokenCount minus cachedContentTokenCount, Cached is
//     cachedContentTokenCount, Out is candidatesTokenCount plus
//     thoughtsTokenCount. The API reports no cache writes (CacheWrite 0).
//
// Retries: the SDK retries the request (408, 429, 5xx) until the response
// headers arrive, WithMaxRetries times; errors after that come from Next.
package gemini

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"sort"
	"strings"

	"google.golang.org/genai"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// BaseURL is the one host the adapter talks to outside tests.
const BaseURL = "https://generativelanguage.googleapis.com/"

const (
	maxMessageLen = 300
	// synthPrefix marks a call id the adapter made up; it is never sent.
	synthPrefix = "gemini_"
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

// Provider is the Gemini llm.Provider.
type Provider struct {
	client *genai.Client
	key    string
	retry  *genai.HTTPRetryOptions
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that authenticates with apiKey. The key goes only in
// the x-goog-api-key header. The backend, base URL and key are always set
// here, so the GOOGLE_GENAI_USE_VERTEXAI, GOOGLE_GEMINI_BASE_URL,
// GOOGLE_API_KEY and GEMINI_API_KEY environment variables have no effect.
func New(apiKey string, opts ...Option) (*Provider, error) {
	cfg := config{baseURL: BaseURL, maxRetries: 2}
	for _, o := range opts {
		o(&cfg)
	}
	if apiKey == "" {
		// genai's own error for this prints its whole config.
		return nil, errors.New("gemini: an API key is required")
	}
	hc := cfg.httpClient
	if hc == nil {
		hc = &http.Client{}
	}
	c, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      apiKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  hc,
		HTTPOptions: genai.HTTPOptions{BaseURL: cfg.baseURL},
	})
	if err != nil {
		// Its text may hold the config, key included: say only what failed.
		return nil, errors.New("gemini: the client could not be set up")
	}
	attempts := int32(cfg.maxRetries + 1)
	return &Provider{client: c, key: apiKey, retry: &genai.HTTPRetryOptions{Attempts: &attempts}}, nil
}

// Models lists the models that answer generateContent, without embedding,
// speech, image and live models, newest generation first. The list carries
// no dates, so "newest" is the id in descending order (gemini-3 before
// gemini-2.5).
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	var out []llm.Model
	for m, err := range p.client.Models.All(ctx) {
		if err != nil {
			return nil, p.mapErr(ctx, err)
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if !chatModel(id, m.SupportedActions) {
			continue
		}
		label := m.DisplayName
		if label == "" {
			label = id
		}
		out = append(out, llm.Model{ID: id, Label: label})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func chatModel(id string, actions []string) bool {
	if !strings.HasPrefix(id, "gemini-") {
		return false
	}
	gen := false
	for _, a := range actions {
		if a == "generateContent" {
			gen = true
		}
	}
	if !gen {
		return false
	}
	for _, s := range []string{"embedding", "tts", "image", "live", "native-audio"} {
		if strings.Contains(id, s) {
			return false
		}
	}
	return true
}

// Stream starts one streamGenerateContent request. The SDK sends it before
// the first event is read, so an HTTP error is returned here.
func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	if req.Model == "" {
		return nil, errors.New("no model selected")
	}
	contents, err := buildContents(ctx, req.Images, req.Messages)
	if err != nil {
		return nil, err
	}
	cfg, err := buildConfig(req)
	if err != nil {
		return nil, err
	}
	cfg.HTTPOptions = &genai.HTTPOptions{RetryOptions: p.retry}
	next, stop := iter.Pull2(p.client.Models.GenerateContentStream(ctx, req.Model, contents, cfg))
	s := &stream{ctx: ctx, p: p, next: next, stopIter: stop}
	resp, err, ok := next()
	if !ok {
		stop()
		return nil, &llm.APIError{Status: 502, Message: "the stream ended before any answer"}
	}
	if err != nil {
		stop()
		return nil, p.mapErr(ctx, err)
	}
	s.handle(resp)
	return s, nil
}

func buildConfig(req llm.Request) (*genai.GenerateContentConfig, error) {
	cfg := &genai.GenerateContentConfig{}
	if req.System != "" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: req.System}}}
	}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = int32(req.MaxTokens)
	}
	if len(req.Tools) > 0 {
		var decls []*genai.FunctionDeclaration
		for _, t := range req.Tools {
			schema := json.RawMessage(`{"type":"object","properties":{}}`)
			if len(t.InputSchema) > 0 {
				if !json.Valid(t.InputSchema) {
					return nil, fmt.Errorf("tool %s: input schema is not valid JSON", t.Name)
				}
				schema = t.InputSchema
			}
			decls = append(decls, &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: schema})
		}
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
	}
	return cfg, nil
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

// pendingCall is a function call of the last model turn, waiting for its
// response.
type pendingCall struct{ id, name string }

// buildContents maps the history.
//   - A model turn gives its parts in order: text, function calls, each with
//     the thought signature of the Gemini Thinking right before it. Another
//     provider's Thinking is skipped.
//   - The next user turn starts with one function response per call of that
//     model turn, in call order, named after the call (the API wants as many
//     responses as calls). A call with no result gets an error response; a
//     result that matches no call is dropped. Its text and images follow.
func buildContents(ctx context.Context, images llm.ImageResolver, in []llm.Message) ([]*genai.Content, error) {
	var out []*genai.Content
	var pending []pendingCall
	for _, m := range mergeSameRole(in) {
		if m.Role == llm.RoleAssistant {
			parts, calls := modelParts(m)
			pending = calls
			if len(parts) > 0 {
				out = append(out, &genai.Content{Role: genai.RoleModel, Parts: parts})
			}
			continue
		}
		results := map[string]llm.ToolResult{}
		for _, part := range m.Parts {
			if r, ok := part.(llm.ToolResult); ok {
				if _, dup := results[r.ID]; !dup {
					results[r.ID] = r
				}
			}
		}
		var parts []*genai.Part
		for _, c := range pending {
			r, ok := results[c.id]
			if !ok {
				r = llm.ToolResult{ID: c.id, Text: "no result", IsError: true}
			}
			parts = append(parts, functionResponse(c, r))
		}
		pending = nil
		for _, part := range m.Parts {
			switch p := part.(type) {
			case llm.Text:
				if p.Text != "" {
					parts = append(parts, &genai.Part{Text: p.Text})
				}
			case llm.Image:
				b, err := llm.ImageBytes(ctx, images, p)
				if err != nil {
					return nil, err
				}
				parts = append(parts, &genai.Part{InlineData: &genai.Blob{MIMEType: p.MediaType, Data: b}})
			}
		}
		if len(parts) > 0 {
			out = append(out, &genai.Content{Role: genai.RoleUser, Parts: parts})
		}
	}
	if len(pending) > 0 {
		var parts []*genai.Part
		for _, c := range pending {
			parts = append(parts, functionResponse(c, llm.ToolResult{ID: c.id, Text: "no result", IsError: true}))
		}
		out = append(out, &genai.Content{Role: genai.RoleUser, Parts: parts})
	}
	return out, nil
}

// modelParts maps a model turn and returns its calls.
func modelParts(m llm.Message) ([]*genai.Part, []pendingCall) {
	var parts []*genai.Part
	var calls []pendingCall
	var sig []byte
	for _, part := range m.Parts {
		switch p := part.(type) {
		case llm.Thinking:
			if llm.ForeignThinking(p, llm.ProviderGemini) {
				continue
			}
			if b, err := base64.StdEncoding.DecodeString(p.Signature); err == nil && len(b) > 0 {
				sig = b
			}
		case llm.Text:
			if p.Text == "" {
				continue
			}
			parts = append(parts, &genai.Part{Text: p.Text, ThoughtSignature: sig})
			sig = nil
		case llm.ToolUse:
			args := map[string]any{}
			if len(p.Args) > 0 {
				if err := json.Unmarshal(p.Args, &args); err != nil || args == nil {
					args = map[string]any{}
				}
			}
			parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: sentID(p.ID), Name: p.Name, Args: args}, ThoughtSignature: sig})
			sig = nil
			calls = append(calls, pendingCall{id: p.ID, name: p.Name})
		}
	}
	return parts, calls
}

func functionResponse(c pendingCall, r llm.ToolResult) *genai.Part {
	key := "output"
	if r.IsError {
		key = "error"
	}
	return &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: sentID(c.id), Name: c.name, Response: map[string]any{key: r.Text}}}
}

// sentID is the call id to send: none for one the adapter made up.
func sentID(id string) string {
	if strings.HasPrefix(id, synthPrefix) {
		return ""
	}
	return id
}

func newCallID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return synthPrefix + hex.EncodeToString(b[:])
}

// element is one model part being built: a text run or a function call,
// with the thought signature that came on it.
type element struct {
	call *genai.FunctionCall
	text strings.Builder
	sig  []byte
}

type stream struct {
	ctx      context.Context
	p        *Provider
	next     func() (*genai.GenerateContentResponse, error, bool)
	stopIter func()
	elems    []*element
	textRun  *element // the text element new text joins, nil after a call
	queue    []llm.Event
	usage    llm.Usage
	reason   string
	category string
	done     bool
	closed   bool
}

func (s *stream) Close() error {
	if !s.closed {
		s.closed = true
		s.stopIter()
	}
	return nil
}

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
		resp, err, ok := s.next()
		if !ok {
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
			if s.reason == "" {
				// A cut connection looks like a bad gateway: worth a retry.
				return nil, &llm.APIError{Status: 502, Message: "stream ended before finishReason"}
			}
			s.stop()
			continue
		}
		if err != nil {
			s.done = true
			return nil, s.p.mapErr(s.ctx, err)
		}
		s.handle(resp)
	}
}

func (s *stream) handle(resp *genai.GenerateContentResponse) {
	if resp == nil {
		return
	}
	if u := resp.UsageMetadata; u != nil {
		cached := int(u.CachedContentTokenCount)
		s.usage = llm.Usage{
			In:     int(u.PromptTokenCount) - cached,
			Out:    int(u.CandidatesTokenCount) + int(u.ThoughtsTokenCount),
			Cached: cached,
		}
	}
	if pf := resp.PromptFeedback; pf != nil && pf.BlockReason != "" && len(resp.Candidates) == 0 {
		s.reason, s.category = llm.StopRefusal, strings.ToLower(string(pf.BlockReason))
		return
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
		return
	}
	c := resp.Candidates[0]
	if c.Content != nil {
		for _, part := range c.Content.Parts {
			if part != nil {
				s.part(part)
			}
		}
	}
	if c.FinishReason != "" {
		s.finishReason(c.FinishReason)
	}
}

func (s *stream) part(p *genai.Part) {
	sig := p.ThoughtSignature
	switch {
	case p.FunctionCall != nil:
		fc := *p.FunctionCall
		if fc.ID == "" {
			fc.ID = newCallID()
		}
		s.elems = append(s.elems, &element{call: &fc, sig: sig})
		s.textRun = nil
	case p.Text != "" && !p.Thought:
		if s.textRun == nil || len(sig) > 0 {
			s.textRun = &element{sig: sig}
			s.elems = append(s.elems, s.textRun)
		}
		s.textRun.text.WriteString(p.Text)
		s.queue = append(s.queue, llm.TextDelta{Text: p.Text})
	case len(sig) > 0:
		// A signature on its own (empty text or a thought summary): it
		// belongs to the text run before it when that has none.
		if s.textRun != nil && len(s.textRun.sig) == 0 {
			s.textRun.sig = sig
			return
		}
		s.elems = append(s.elems, &element{sig: sig})
		s.textRun = nil
	}
}

func (s *stream) finishReason(r genai.FinishReason) {
	switch r {
	case genai.FinishReasonStop:
		s.reason = llm.StopEndTurn
	case genai.FinishReasonMaxTokens:
		s.reason = llm.StopMaxTokens
	case genai.FinishReasonSafety, genai.FinishReasonRecitation, genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent, genai.FinishReasonSPII, genai.FinishReasonImageSafety,
		genai.FinishReasonImageProhibitedContent:
		s.reason, s.category = llm.StopRefusal, strings.ToLower(string(r))
	default:
		s.reason = strings.ToLower(string(r))
	}
}

// stop ends the turn: the calls in order, usage, then Stop with the model
// parts (each signature as a Gemini Thinking right before its part).
func (s *stream) stop() {
	reason := s.reason
	var parts []llm.Part
	var calls []llm.ToolCall
	for _, e := range s.elems {
		if len(e.sig) > 0 {
			parts = append(parts, llm.Thinking{Provider: llm.ProviderGemini, Signature: base64.StdEncoding.EncodeToString(e.sig)})
		}
		switch {
		case e.call != nil:
			tc := toolCall(e.call)
			calls = append(calls, tc)
			parts = append(parts, llm.ToolUse{ID: tc.ID, Name: tc.Name, Args: tc.Args})
		case e.text.Len() > 0:
			parts = append(parts, llm.Text{Text: e.text.String()})
		}
	}
	if reason == llm.StopEndTurn && len(calls) > 0 {
		reason = llm.StopToolUse
	}
	for _, c := range calls {
		s.queue = append(s.queue, c)
	}
	st := llm.Stop{Reason: reason, Category: s.category}
	if len(parts) > 0 {
		st.Message = llm.Message{Role: llm.RoleAssistant, Parts: parts}
	}
	s.queue = append(s.queue, s.usage, st)
	s.elems, s.textRun = nil, nil
	s.done = true
}

// toolCall turns a function call into a ToolCall. Its arguments arrive
// parsed, so they are always a JSON object ("{}" when absent).
func toolCall(fc *genai.FunctionCall) llm.ToolCall {
	args := json.RawMessage("{}")
	if len(fc.Args) > 0 {
		if b, err := json.Marshal(fc.Args); err == nil {
			args = b
		}
	}
	return llm.ToolCall{ID: fc.ID, Name: fc.Name, Args: args}
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
	var ae genai.APIError
	if errors.As(err, &ae) {
		status := ae.Code
		if status < 400 {
			status = statusFromName(ae.Status)
		}
		if keyRefused(ae) {
			// The API answers a bad key with 400 INVALID_ARGUMENT.
			status = 401
		}
		msg := ae.Message
		if msg == "" {
			msg = http.StatusText(status)
		}
		if msg == "" {
			msg = "the server reported an error"
		}
		return &llm.APIError{Status: status, Message: p.scrub(msg)}
	}
	// Transport failure (connection reset, DNS, decode): retryable.
	return &llm.APIError{Status: 502, Message: p.scrub(err.Error())}
}

// keyRefused reports an ErrorInfo detail with reason API_KEY_INVALID.
func keyRefused(ae genai.APIError) bool {
	for _, d := range ae.Details {
		if r, _ := d["reason"].(string); r == "API_KEY_INVALID" {
			return true
		}
	}
	return false
}

// statusFromName maps a google.rpc status name to its HTTP status.
func statusFromName(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE":
		return 400
	case "UNAUTHENTICATED":
		return 401
	case "PERMISSION_DENIED":
		return 403
	case "NOT_FOUND":
		return 404
	case "RESOURCE_EXHAUSTED":
		return 429
	case "INTERNAL", "UNKNOWN", "DATA_LOSS":
		return 500
	case "UNAVAILABLE":
		return 503
	case "DEADLINE_EXCEEDED":
		return 504
	}
	return 0
}

func (p *Provider) scrub(s string) string {
	if p.key != "" {
		s = strings.ReplaceAll(s, p.key, "[redacted]")
	}
	r := []rune(s)
	if len(r) > maxMessageLen {
		return string(r[:maxMessageLen]) + "..."
	}
	return s
}
