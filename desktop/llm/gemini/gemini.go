// Package gemini adapts the Gemini API (generativelanguage.googleapis.com,
// API key only; never Vertex AI) to llm.Provider over net/http and the REST
// wire format: POST /v1beta/models/{model}:streamGenerateContent?alt=sse and
// GET /v1beta/models. No SDK: the key goes only in the x-goog-api-key header,
// redirects are never followed (Go would copy that header to the new host),
// and no environment variable is read.
//
// What differs from the other adapters:
//   - Thought signatures. A model part (a function call, a text) may carry an
//     opaque thoughtSignature that must go back unchanged on that same part.
//     The adapter stores the string as received in llm.Thinking{Provider:
//     gemini, Signature} right before the part it belongs to, so ToolUse and
//     Text keep their shape, and puts it back on that part, byte for byte,
//     when it replays the history. Text after a signed text part starts a new
//     part, so signed and unsigned text are never merged. A signature on an
//     empty text part belongs to the text run right before it when that run
//     has none. Any other signature (on a thought part, a second one for the
//     same run, one with no run before it) has no part to sit on: it is
//     stored with Data "unattached" and never sent back, neither alone (a
//     part with no data is not valid input) nor on another part.
//   - Finish reasons that mean "no usable answer" (MALFORMED_FUNCTION_CALL,
//     UNEXPECTED_TOOL_CALL, TOO_MANY_TOOL_CALLS, MISSING_THOUGHT_SIGNATURE,
//     MALFORMED_RESPONSE, OTHER, LANGUAGE) end the stream with an
//     *llm.APIError (502, Category the lower-case reason) instead of a Stop,
//     so the user is told why there is no reply.
//   - Function calls arrive whole (the Gemini API does not stream partial
//     arguments; partialArgs is Vertex only), possibly several per chunk and
//     over several chunks. The API usually gives no call id: the adapter makes
//     one ("gemini_" + random hex) and leaves it out when it replays the call
//     and its response. A functionResponse is named after the ToolUse it
//     answers.
//   - Usage: In is promptTokenCount minus cachedContentTokenCount, Cached is
//     cachedContentTokenCount, Out is candidatesTokenCount plus
//     thoughtsTokenCount. The API reports no cache writes (CacheWrite 0).
//
// Retries: a request is retried (408, 429, 500, 502, 503, 504, transport
// errors) until the response headers arrive, WithMaxRetries times; errors
// after that come from Next.
package gemini

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// BaseURL is the one host the adapter talks to outside tests.
const BaseURL = "https://generativelanguage.googleapis.com/"

const (
	maxMessageLen = 300
	// synthPrefix marks a call id the adapter made up; it is never sent.
	synthPrefix = "gemini_"
	// maxLine bounds one SSE line, maxEvent one event (its data lines joined).
	maxLine  = 4 << 20
	maxEvent = 8 << 20
	// maxErrorBody bounds an error body read; maxModelsBody a models page.
	maxErrorBody  = 64 << 10
	maxModelsBody = 8 << 20
	// maxModelPages bounds the pageToken loop of Models.
	maxModelPages = 20
	// maxRetryAfter is the longest Retry-After waited for; a longer one is
	// returned as the error.
	maxRetryAfter = 10 * time.Second
	// unattached marks (in Thinking.Data) a signature that belongs to no
	// part; it is stored but never replayed.
	unattached = "unattached"
)

// failReasons are the finish reasons that leave no usable answer, with what
// the user is told.
var failReasons = map[string]string{
	"MALFORMED_FUNCTION_CALL":   "the model produced a tool call that could not be read",
	"UNEXPECTED_TOOL_CALL":      "the model called a tool that was not offered",
	"TOO_MANY_TOOL_CALLS":       "the model made too many tool calls in one turn",
	"MISSING_THOUGHT_SIGNATURE": "the API asked for a thought signature the conversation does not have",
	"MALFORMED_RESPONSE":        "the model's answer could not be read",
	"OTHER":                     "the model stopped for a reason it did not give",
	"LANGUAGE":                  "the model does not support the language of the conversation",
}

// modelID is what a model id may look like once "models/" is cut off.
var modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Option customises New.
type Option func(*config)

type config struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
}

// WithBaseURL points the adapter at another server (tests).
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithHTTPClient replaces the HTTP client. Its redirect policy is replaced by
// one that follows none.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.httpClient = h } }

// WithMaxRetries sets how often a request is retried before the stream starts.
func WithMaxRetries(n int) Option { return func(c *config) { c.maxRetries = n } }

// Provider is the Gemini llm.Provider.
type Provider struct {
	hc         *http.Client
	base       string
	key        string
	maxRetries int
	backoff    time.Duration
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that authenticates with apiKey, sent only in the
// x-goog-api-key header.
func New(apiKey string, opts ...Option) (*Provider, error) {
	cfg := config{baseURL: BaseURL, maxRetries: 2}
	for _, o := range opts {
		o(&cfg)
	}
	if apiKey == "" {
		return nil, errors.New("gemini: an API key is required")
	}
	u, err := url.Parse(cfg.baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("gemini: the base URL is not valid")
	}
	hc := llm.NoRedirectClient(cfg.httpClient)
	if cfg.maxRetries < 0 {
		cfg.maxRetries = 0
	}
	return &Provider{
		hc:         hc,
		base:       strings.TrimRight(cfg.baseURL, "/"),
		key:        apiKey,
		maxRetries: cfg.maxRetries,
		backoff:    time.Second,
	}, nil
}

// --- wire types (REST, v1beta) ---

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	InlineData       *blob             `json:"inlineData,omitempty"`
}

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	ID       string            `json:"id,omitempty"`
	Name     string            `json:"name"`
	Response map[string]string `json:"response"`
}

type blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionDeclaration struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
}

type tool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type request struct {
	Contents          []content         `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
	Tools             []tool            `json:"tools,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
}

type candidate struct {
	Content      *content `json:"content"`
	FinishReason string   `json:"finishReason"`
}

type response struct {
	Candidates     []candidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
	Error         *errorBody     `json:"error"`
}

type errorBody struct {
	Code    int    `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Details []struct {
		Reason string `json:"reason"`
	} `json:"details"`
}

// --- HTTP ---

// do sends one request, retrying before the response headers arrive. Any
// status but 200 is returned as an *llm.APIError.
func (p *Provider) do(ctx context.Context, method, u string, body []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return nil, errors.New("gemini: the request could not be built")
		}
		req.Header.Set("x-goog-api-key", p.key)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := p.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < p.maxRetries {
				if werr := p.wait(ctx, attempt, 0); werr != nil {
					return nil, werr
				}
				continue
			}
			// Transport failure (connection reset, DNS): retryable later.
			return nil, &llm.APIError{Status: 502, Message: p.scrub(err.Error())}
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		if retryStatus(resp.StatusCode) && attempt < p.maxRetries {
			if after, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
				drain(resp)
				if werr := p.wait(ctx, attempt, after); werr != nil {
					return nil, werr
				}
				continue
			}
		}
		return nil, p.statusErr(resp)
	}
}

func retryStatus(s int) bool {
	switch s {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// retryAfter reads a Retry-After in seconds; ok is false for one longer than
// maxRetryAfter (not worth waiting for).
func retryAfter(h string) (time.Duration, bool) {
	if h == "" {
		return 0, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || n < 0 {
		return 0, true
	}
	d := time.Duration(n) * time.Second
	return d, d <= maxRetryAfter
}

// wait sleeps before retry attempt+1: after when the server named a time,
// else the backoff doubled per attempt.
func (p *Provider) wait(ctx context.Context, attempt int, after time.Duration) error {
	d := after
	if d == 0 {
		d = p.backoff << attempt
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()
}

// statusErr reads a bounded error body and closes it.
func (p *Provider) statusErr(resp *http.Response) error {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	status := resp.StatusCode
	if status < 400 {
		// A redirect (never followed) or another unexpected answer.
		return &llm.APIError{Status: 502, Message: fmt.Sprintf("unexpected answer (HTTP %d)", status)}
	}
	eb := parseErrorBody(raw)
	msg := ""
	if eb != nil {
		msg = eb.Message
		if keyRefused(eb) {
			// The API answers a bad key with 400 INVALID_ARGUMENT.
			status = 401
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	if msg == "" {
		msg = "the server reported an error"
	}
	return &llm.APIError{Status: status, Message: p.scrub(msg)}
}

// parseErrorBody reads {"error":{...}}, or the list form [{"error":{...}}].
func parseErrorBody(raw []byte) *errorBody {
	var one struct {
		Error *errorBody `json:"error"`
	}
	if json.Unmarshal(raw, &one) == nil && one.Error != nil {
		return one.Error
	}
	var list []struct {
		Error *errorBody `json:"error"`
	}
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 && list[0].Error != nil {
		return list[0].Error
	}
	return nil
}

// streamErr maps an error object sent inside a stream.
func (p *Provider) streamErr(eb *errorBody) error {
	status := eb.Code
	if status < 400 {
		status = statusFromName(eb.Status)
	}
	if keyRefused(eb) {
		status = 401
	}
	if status == 0 {
		status = 500
	}
	msg := eb.Message
	if msg == "" {
		msg = "the stream reported an error"
	}
	return &llm.APIError{Status: status, Message: p.scrub(msg)}
}

// keyRefused reports an ErrorInfo detail with reason API_KEY_INVALID.
func keyRefused(eb *errorBody) bool {
	for _, d := range eb.Details {
		if d.Reason == "API_KEY_INVALID" {
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

// --- Models ---

// Models lists the models that answer generateContent, without embedding,
// speech, image and live models, newest generation first. The list carries
// no dates, so "newest" is the id in descending order (gemini-3 before
// gemini-2.5).
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	var out []llm.Model
	token := ""
	for page := 0; page < maxModelPages; page++ {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		resp, err := p.do(ctx, http.MethodGet, p.base+"/v1beta/models?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody+1))
		_ = resp.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &llm.APIError{Status: 502, Message: p.scrub(err.Error())}
		}
		if len(raw) > maxModelsBody {
			return nil, &llm.APIError{Status: 502, Message: "the model list is too large"}
		}
		var body struct {
			Models []struct {
				Name                       string   `json:"name"`
				DisplayName                string   `json:"displayName"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, &llm.APIError{Status: 502, Message: "the model list could not be read"}
		}
		for _, m := range body.Models {
			id := strings.TrimPrefix(m.Name, "models/")
			if !chatModel(id, m.SupportedGenerationMethods) {
				continue
			}
			label := m.DisplayName
			if label == "" {
				label = id
			}
			out = append(out, llm.Model{ID: id, Label: label})
		}
		if body.NextPageToken == "" || body.NextPageToken == token {
			break
		}
		token = body.NextPageToken
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

// --- Stream ---

// Stream starts one streamGenerateContent request and reads its first event,
// so an HTTP error or an error in the first event is returned here.
func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	if req.Model == "" {
		return nil, errors.New("no model selected")
	}
	model := strings.TrimPrefix(req.Model, "models/")
	if !modelID.MatchString(model) {
		return nil, fmt.Errorf("gemini: %q is not a model id", req.Model)
	}
	contents, err := buildContents(ctx, req.Images, req.Messages)
	if err != nil {
		return nil, err
	}
	body := request{Contents: contents}
	if req.System != "" {
		body.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}
	if req.MaxTokens > 0 {
		body.GenerationConfig = &generationConfig{MaxOutputTokens: req.MaxTokens}
	}
	if body.Tools, err = buildTools(req.Tools); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: the request could not be encoded: %w", err)
	}
	resp, err := p.do(ctx, http.MethodPost, p.base+"/v1beta/models/"+model+":streamGenerateContent?alt=sse", raw)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	sc.Split(scanLines)
	s := &stream{ctx: ctx, p: p, body: resp.Body, sc: sc}
	ev, err := s.event()
	if err == io.EOF {
		s.Close()
		return nil, &llm.APIError{Status: 502, Message: "the stream ended before any answer"}
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.handle(ev); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func buildTools(tools []llm.Tool) ([]tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	var decls []functionDeclaration
	for _, t := range tools {
		schema := json.RawMessage(`{"type":"object","properties":{}}`)
		if len(t.InputSchema) > 0 {
			if !json.Valid(t.InputSchema) {
				return nil, fmt.Errorf("tool %s: input schema is not valid JSON", t.Name)
			}
			schema = t.InputSchema
		}
		decls = append(decls, functionDeclaration{Name: t.Name, Description: t.Description, ParametersJSONSchema: schema})
	}
	return []tool{{FunctionDeclarations: decls}}, nil
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
//     result that matches no call is dropped; a result with an empty id
//     answers the next call that had none. Its text and images follow.
func buildContents(ctx context.Context, images llm.ImageResolver, in []llm.Message) ([]content, error) {
	var out []content
	var pending []pendingCall
	for _, m := range mergeSameRole(in) {
		if m.Role == llm.RoleAssistant {
			parts, calls := modelParts(m)
			pending = calls
			if len(parts) > 0 {
				out = append(out, content{Role: "model", Parts: parts})
			}
			continue
		}
		results := map[string]llm.ToolResult{}
		var anon []llm.ToolResult // results with an empty id, in order
		for _, pt := range m.Parts {
			if r, ok := pt.(llm.ToolResult); ok {
				if r.ID == "" {
					anon = append(anon, r)
				} else if _, dup := results[r.ID]; !dup {
					results[r.ID] = r
				}
			}
		}
		var parts []part
		for _, c := range pending {
			r, ok := results[c.id]
			if !ok && c.id == "" && len(anon) > 0 {
				// A call stored without an id takes the next result without one.
				r, ok, anon = anon[0], true, anon[1:]
			}
			if !ok {
				r = llm.ToolResult{ID: c.id, Text: "no result", IsError: true}
			}
			parts = append(parts, responsePart(c, r))
		}
		pending = nil
		for _, pt := range m.Parts {
			switch p := pt.(type) {
			case llm.Text:
				if p.Text != "" {
					parts = append(parts, part{Text: p.Text})
				}
			case llm.Image:
				b, err := llm.ImageBytes(ctx, images, p)
				if err != nil {
					return nil, err
				}
				parts = append(parts, part{InlineData: &blob{MimeType: p.MediaType, Data: base64.StdEncoding.EncodeToString(b)}})
			case llm.Document:
				b, err := llm.DocumentBytes(ctx, images, p)
				if err != nil {
					return nil, err
				}
				parts = append(parts, part{InlineData: &blob{MimeType: p.MediaType, Data: base64.StdEncoding.EncodeToString(b)}})
			}
		}
		if len(parts) > 0 {
			out = append(out, content{Role: "user", Parts: parts})
		}
	}
	if len(pending) > 0 {
		var parts []part
		for _, c := range pending {
			parts = append(parts, responsePart(c, llm.ToolResult{ID: c.id, Text: "no result", IsError: true}))
		}
		out = append(out, content{Role: "user", Parts: parts})
	}
	return out, nil
}

// modelParts maps a model turn and returns its calls.
func modelParts(m llm.Message) ([]part, []pendingCall) {
	var parts []part
	var calls []pendingCall
	sig := ""
	for _, pt := range m.Parts {
		switch p := pt.(type) {
		case llm.Thinking:
			if llm.ForeignThinking(p, llm.ProviderGemini) || p.Data == unattached {
				continue
			}
			if p.Signature != "" {
				sig = p.Signature
			}
		case llm.Text:
			if p.Text == "" {
				continue
			}
			parts = append(parts, part{Text: p.Text, ThoughtSignature: sig})
			sig = ""
		case llm.ToolUse:
			parts = append(parts, part{FunctionCall: &functionCall{ID: sentID(p.ID), Name: p.Name, Args: objectArgs(p.Args)}, ThoughtSignature: sig})
			sig = ""
			calls = append(calls, pendingCall{id: p.ID, name: p.Name})
		}
	}
	return parts, calls
}

// objectArgs returns args compacted when they are a JSON object, else "{}".
func objectArgs(args json.RawMessage) json.RawMessage {
	t := bytes.TrimSpace(args)
	if len(t) == 0 || t[0] != '{' || !json.Valid(t) {
		return json.RawMessage("{}")
	}
	var b bytes.Buffer
	if json.Compact(&b, t) != nil {
		return json.RawMessage("{}")
	}
	return b.Bytes()
}

func responsePart(c pendingCall, r llm.ToolResult) part {
	key := "output"
	if r.IsError {
		key = "error"
	}
	return part{FunctionResponse: &functionResponse{ID: sentID(c.id), Name: c.name, Response: map[string]string{key: r.Text}}}
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
	call   *functionCall
	text   strings.Builder
	sig    string
	orphan bool // a signature with no part (stored with Data unattached)
}

type stream struct {
	ctx      context.Context
	p        *Provider
	body     io.ReadCloser
	sc       *bufio.Scanner
	elems    []*element
	textRun  *element // the text element new text joins, nil after a call
	queue    []llm.Event
	usage    llm.Usage
	reason   string
	category string
	fail     *llm.APIError // a finish reason with no usable answer
	done     bool
	closed   bool
}

func (s *stream) Close() error {
	if !s.closed {
		s.closed = true
		_ = s.body.Close()
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
		resp, err := s.event()
		if err == io.EOF {
			if s.reason == "" {
				// A cut connection looks like a bad gateway: worth a retry.
				s.done = true
				return nil, &llm.APIError{Status: 502, Message: "stream ended before finishReason"}
			}
			if s.fail != nil {
				s.done = true
				return nil, s.fail
			}
			s.stop()
			continue
		}
		if err == nil {
			err = s.handle(resp)
		}
		if err != nil {
			s.done = true
			return nil, err
		}
	}
}

// scanLines splits at CRLF, LF or a bare CR, the three SSE line endings.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		if i+1 < len(data) {
			if data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
		if atEOF {
			return i + 1, data[:i], nil
		}
		// A CR at the end of the buffer: an LF may follow.
		return 0, nil, nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// event reads the next SSE event and decodes its data. It returns io.EOF at
// the end of the body. A line outside an event that holds JSON is an error
// body (the API may end a stream with a bare {"error":...}); the rest of the
// body is read, bounded, and returned as that error.
func (s *stream) event() (*response, error) {
	var data []byte
	have := false
	for {
		if !s.sc.Scan() {
			err := s.sc.Err()
			if s.ctx.Err() != nil {
				return nil, s.ctx.Err()
			}
			if errors.Is(err, bufio.ErrTooLong) {
				return nil, &llm.APIError{Status: 502, Message: "a stream line is too long"}
			}
			if err != nil {
				return nil, &llm.APIError{Status: 502, Message: s.p.scrub(err.Error())}
			}
			if have {
				return s.decode(data)
			}
			return nil, io.EOF
		}
		line := s.sc.Bytes()
		switch {
		case len(line) == 0:
			if have {
				return s.decode(data)
			}
		case line[0] == ':':
			// comment
		case bytes.HasPrefix(line, []byte("data:")):
			v := bytes.TrimPrefix(line[len("data:"):], []byte(" "))
			if len(data)+len(v)+1 > maxEvent {
				return nil, &llm.APIError{Status: 502, Message: "a stream event is too large"}
			}
			if have {
				data = append(data, '\n')
			}
			data = append(data, v...)
			have = true
		case line[0] == '{' || line[0] == '[':
			return nil, s.bareError(line)
		default:
			// event:, id:, retry: and unknown fields carry nothing used here.
		}
	}
}

// bareError reads an error body that starts at line, up to maxErrorBody.
func (s *stream) bareError(first []byte) error {
	buf := append([]byte(nil), first...)
	for len(buf) < maxErrorBody && s.sc.Scan() {
		buf = append(buf, '\n')
		buf = append(buf, s.sc.Bytes()...)
	}
	if len(buf) > maxErrorBody {
		buf = buf[:maxErrorBody]
	}
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if eb := parseErrorBody(buf); eb != nil {
		return s.p.streamErr(eb)
	}
	return &llm.APIError{Status: 502, Message: "the stream could not be read"}
}

func (s *stream) decode(data []byte) (*response, error) {
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		if eb := parseErrorBody(data); eb != nil {
			return nil, s.p.streamErr(eb)
		}
		return nil, &llm.APIError{Status: 502, Message: "a stream event could not be read"}
	}
	return &r, nil
}

func (s *stream) handle(resp *response) error {
	if resp == nil {
		return nil
	}
	if resp.Error != nil {
		return s.p.streamErr(resp.Error)
	}
	if u := resp.UsageMetadata; u != nil {
		cached := u.CachedContentTokenCount
		s.usage = llm.Usage{
			In:     u.PromptTokenCount - cached,
			Out:    u.CandidatesTokenCount + u.ThoughtsTokenCount,
			Cached: cached,
		}
	}
	if pf := resp.PromptFeedback; pf != nil && pf.BlockReason != "" && len(resp.Candidates) == 0 {
		s.reason, s.category = llm.StopRefusal, strings.ToLower(pf.BlockReason)
		return nil
	}
	if len(resp.Candidates) == 0 {
		return nil
	}
	c := resp.Candidates[0]
	if c.Content != nil {
		for i := range c.Content.Parts {
			s.part(&c.Content.Parts[i])
		}
	}
	if c.FinishReason != "" {
		s.finishReason(c.FinishReason)
	}
	return nil
}

func (s *stream) part(p *part) {
	sig := p.ThoughtSignature
	switch {
	case p.FunctionCall != nil:
		fc := *p.FunctionCall
		if fc.ID == "" {
			fc.ID = newCallID()
		}
		s.elems = append(s.elems, &element{call: &fc, sig: sig})
		s.textRun = nil
	case p.Thought:
		// A thought summary is not shown or replayed; a signature on it
		// belongs to no part the adapter sends back.
		if sig != "" {
			s.orphan(sig)
		}
	case p.Text != "":
		// Never merge into a signed run: its signature covers its own text.
		if s.textRun == nil || sig != "" || s.textRun.sig != "" {
			s.textRun = &element{sig: sig}
			s.elems = append(s.elems, s.textRun)
		}
		s.textRun.text.WriteString(p.Text)
		s.queue = append(s.queue, llm.TextDelta{Text: p.Text})
	case sig != "":
		// A signature on an empty text part belongs to the text run right
		// before it when that has none.
		if s.textRun != nil && s.textRun.sig == "" {
			s.textRun.sig = sig
			return
		}
		s.orphan(sig)
	}
}

// orphan keeps a signature that belongs to no part.
func (s *stream) orphan(sig string) {
	s.elems = append(s.elems, &element{sig: sig, orphan: true})
	s.textRun = nil
}

func (s *stream) finishReason(r string) {
	if msg, ok := failReasons[r]; ok {
		s.reason = strings.ToLower(r)
		s.fail = &llm.APIError{Status: 502, Category: strings.ToLower(r), Message: msg}
		return
	}
	switch r {
	case "STOP":
		s.reason = llm.StopEndTurn
	case "MAX_TOKENS":
		s.reason = llm.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT":
		s.reason, s.category = llm.StopRefusal, strings.ToLower(r)
	default:
		s.reason = strings.ToLower(r)
	}
}

// stop ends the turn: the calls in order, usage, then Stop with the model
// parts (each signature as a Gemini Thinking right before its part).
func (s *stream) stop() {
	reason := s.reason
	var parts []llm.Part
	var calls []llm.ToolCall
	for _, e := range s.elems {
		if e.orphan {
			parts = append(parts, llm.Thinking{Provider: llm.ProviderGemini, Signature: e.sig, Data: unattached})
			continue
		}
		if e.sig != "" {
			parts = append(parts, llm.Thinking{Provider: llm.ProviderGemini, Signature: e.sig})
		}
		switch {
		case e.call != nil:
			tc := llm.ToolCall{ID: e.call.ID, Name: e.call.Name, Args: objectArgs(e.call.Args)}
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
