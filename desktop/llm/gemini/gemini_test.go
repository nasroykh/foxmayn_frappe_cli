package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
)

const testKey = "AIzaTESTKEY-0123456789"

type captured struct {
	mu     sync.Mutex
	header http.Header
	body   []byte
	path   string
	query  string
}

func (c *captured) set(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.header, c.body, c.path, c.query = r.Header.Clone(), b, r.URL.Path, r.URL.RawQuery
}

func (c *captured) get() (http.Header, []byte, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header, c.body, c.path, c.query
}

// setEnv fills the variables Google's SDKs read; the adapter must ignore them all.
func setEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_API_KEY", "env-key-must-not-be-sent")
	t.Setenv("GEMINI_API_KEY", "env-key-must-not-be-sent-either")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "env-location")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:1/")
}

func newProvider(t *testing.T, h http.Handler) (*Provider, *captured) {
	t.Helper()
	setEnv(t)
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.set(r)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	p, err := New(testKey, WithBaseURL(srv.URL+"/"), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return p, cap
}

func serveFile(t *testing.T, name string) http.Handler {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	})
}

func collect(t *testing.T, s llm.Stream) []llm.Event {
	t.Helper()
	defer s.Close()
	var out []llm.Event
	for {
		ev, err := s.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v (events so far %#v)", err, out)
		}
		out = append(out, ev)
	}
}

func hi() llm.Request {
	return llm.Request{
		Model:    "gemini-test",
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "hi"}}}},
	}
}

func run(t *testing.T, file string, req llm.Request) ([]llm.Event, *captured) {
	t.Helper()
	p, cap := newProvider(t, serveFile(t, file))
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, s), cap
}

type sentBody struct {
	Contents []struct {
		Role  string           `json:"role"`
		Parts []map[string]any `json:"parts"`
	} `json:"contents"`
	SystemInstruction struct {
		Parts []map[string]any `json:"parts"`
	} `json:"systemInstruction"`
	GenerationConfig map[string]any   `json:"generationConfig"`
	Tools            []map[string]any `json:"tools"`
}

func sent(t *testing.T, req llm.Request) (sentBody, string) {
	t.Helper()
	_, cap := run(t, "text.sse", req)
	_, raw, _, _ := cap.get()
	var b sentBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return b, string(raw)
}

func asst(parts ...llm.Part) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Parts: parts}
}

func sig(b64 string) llm.Thinking {
	return llm.Thinking{Provider: llm.ProviderGemini, Signature: b64}
}

func TestText(t *testing.T) {
	req := hi()
	req.System = "be brief"
	req.MaxTokens = 400
	got, cap := run(t, "text.sse", req)
	want := []llm.Event{
		llm.TextDelta{Text: "Hello"},
		llm.TextDelta{Text: ", world"},
		llm.Usage{In: 40, Out: 27, Cached: 60},
		// The signature on the closing empty part belongs to the text.
		llm.Stop{Reason: llm.StopEndTurn, Message: asst(sig("dGV4dC1zaWc="), llm.Text{Text: "Hello, world"})},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %#v\nwant %#v", got, want)
	}
	h, raw, path, query := cap.get()
	if path != "/v1beta/models/gemini-test:streamGenerateContent" || query != "alt=sse" {
		t.Fatalf("path %q?%q (env base URL or Vertex used?)", path, query)
	}
	if h.Get("x-goog-api-key") != testKey {
		t.Fatalf("x-goog-api-key %q", h.Get("x-goog-api-key"))
	}
	for k, v := range h {
		for _, s := range v {
			if strings.Contains(s, "env-") {
				t.Fatalf("env value sent in %s: %q", k, s)
			}
		}
	}
	if strings.Contains(string(raw), testKey) || strings.Contains(path+query, testKey) {
		t.Fatal("key in the URL or body")
	}
	var b sentBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if len(b.SystemInstruction.Parts) != 1 || b.SystemInstruction.Parts[0]["text"] != "be brief" {
		t.Fatalf("system %s", raw)
	}
	if b.GenerationConfig["maxOutputTokens"] != float64(400) {
		t.Fatalf("generationConfig %s", raw)
	}
	if len(b.Contents) != 1 || b.Contents[0].Role != "user" || b.Contents[0].Parts[0]["text"] != "hi" {
		t.Fatalf("contents %s", raw)
	}
}

func TestRequestNeedsModelAndKey(t *testing.T) {
	p, cap := newProvider(t, serveFile(t, "text.sse"))
	req := hi()
	req.Model = ""
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Fatal("no model accepted")
	}
	if _, raw, _, _ := cap.get(); raw != nil {
		t.Fatal("request sent")
	}
	if _, err := New(""); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestParallelToolCallsOverChunks(t *testing.T) {
	got, _ := run(t, "tools_sig.sse", hi())
	var calls []llm.ToolCall
	for _, ev := range got {
		if c, ok := ev.(llm.ToolCall); ok {
			calls = append(calls, c)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("calls %#v", got)
	}
	if !strings.HasPrefix(calls[0].ID, synthPrefix) || !strings.HasPrefix(calls[1].ID, synthPrefix) || calls[0].ID == calls[1].ID {
		t.Fatalf("ids %q %q", calls[0].ID, calls[1].ID)
	}
	if calls[0].Name != "get_doc" || string(calls[0].Args) != `{"doctype":"Sales Invoice","name":"SINV-1"}` {
		t.Fatalf("call 0 %#v", calls[0])
	}
	if calls[1].Name != "list_docs" || string(calls[1].Args) != `{"doctype":"Customer","limit":5}` {
		t.Fatalf("call 1 %#v (%s)", calls[1], calls[1].Args)
	}
	u := got[len(got)-2].(llm.Usage)
	if u != (llm.Usage{In: 904, Out: 296, Cached: 4096}) {
		t.Fatalf("usage %#v", u)
	}
	stop := got[len(got)-1].(llm.Stop)
	want := asst(sig("AAEC/+3t"),
		llm.ToolUse{ID: calls[0].ID, Name: "get_doc", Args: calls[0].Args},
		llm.ToolUse{ID: calls[1].ID, Name: "list_docs", Args: calls[1].Args})
	if stop.Reason != llm.StopToolUse || !reflect.DeepEqual(stop.Message, want) {
		t.Fatalf("stop %#v", stop)
	}
}

// The thought signature goes back byte for byte on the part it came on, the
// made-up call ids are not sent, and each function response is named after
// its call.
func TestSignatureReplayedByteIdentical(t *testing.T) {
	got, _ := run(t, "tools_sig.sse", hi())
	stop := got[len(got)-1].(llm.Stop)
	calls := []llm.ToolUse{}
	for _, p := range stop.Message.Parts {
		if u, ok := p.(llm.ToolUse); ok {
			calls = append(calls, u)
		}
	}
	// Through the store and back, as the loop does.
	stored, err := llm.MarshalParts(stop.Message.Parts)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := llm.UnmarshalParts(stored)
	if err != nil {
		t.Fatal(err)
	}
	req := hi()
	req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, Parts: parts}, llm.Message{Role: llm.RoleUser, Parts: []llm.Part{
		llm.ToolResult{ID: calls[1].ID, Text: "[]"},
		llm.ToolResult{ID: calls[0].ID, Text: "not found", IsError: true},
		llm.ToolResult{ID: "stray", Text: "dropped"},
		llm.Text{Text: "and?"},
	}})
	b, raw := sent(t, req)
	if !strings.Contains(raw, `"thoughtSignature":"AAEC/+3t"`) {
		t.Fatalf("signature not byte-identical: %s", raw)
	}
	if len(b.Contents) != 3 {
		t.Fatalf("contents %s", raw)
	}
	m := b.Contents[1]
	if m.Role != "model" || len(m.Parts) != 2 {
		t.Fatalf("model turn %s", raw)
	}
	if m.Parts[0]["thoughtSignature"] != "AAEC/+3t" || m.Parts[1]["thoughtSignature"] != nil {
		t.Fatalf("signature on the wrong part: %v", m.Parts)
	}
	fc, _ := m.Parts[0]["functionCall"].(map[string]any)
	if fc["name"] != "get_doc" || fc["id"] != nil {
		t.Fatalf("function call %v", fc)
	}
	u := b.Contents[2]
	if u.Role != "user" || len(u.Parts) != 3 {
		t.Fatalf("user turn %s", raw)
	}
	r0, _ := u.Parts[0]["functionResponse"].(map[string]any)
	r1, _ := u.Parts[1]["functionResponse"].(map[string]any)
	if r0["name"] != "get_doc" || r0["id"] != nil || r0["response"].(map[string]any)["error"] != "not found" {
		t.Fatalf("response 0 %v", r0)
	}
	if r1["name"] != "list_docs" || r1["response"].(map[string]any)["output"] != "[]" {
		t.Fatalf("response 1 %v", r1)
	}
	if u.Parts[2]["text"] != "and?" {
		t.Fatalf("text %v", u.Parts[2])
	}
}

// A call id the API gave is kept and sent back with its response.
func TestRealCallIDKept(t *testing.T) {
	req := hi()
	req.Messages = append(req.Messages,
		asst(llm.ToolUse{ID: "fc-real-1", Name: "whoami"}),
		llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{ID: "fc-real-1", Text: "me"}}})
	b, raw := sent(t, req)
	fc, _ := b.Contents[1].Parts[0]["functionCall"].(map[string]any)
	fr, _ := b.Contents[2].Parts[0]["functionResponse"].(map[string]any)
	if fc["id"] != "fc-real-1" || fr["id"] != "fc-real-1" || fr["name"] != "whoami" {
		t.Fatalf("ids %s", raw)
	}
}

func TestMissingResultGetsErrorResponse(t *testing.T) {
	req := hi()
	req.Messages = append(req.Messages, asst(llm.ToolUse{ID: "gemini_x", Name: "whoami"}))
	b, raw := sent(t, req)
	if len(b.Contents) != 3 {
		t.Fatalf("contents %s", raw)
	}
	fr, _ := b.Contents[2].Parts[0]["functionResponse"].(map[string]any)
	if fr["name"] != "whoami" || fr["response"].(map[string]any)["error"] != "no result" {
		t.Fatalf("response %v", fr)
	}
}

func TestHistorySkipsForeignThinking(t *testing.T) {
	req := hi()
	req.Messages = append(req.Messages,
		asst(
			llm.Thinking{Text: "0.2.0", Signature: "U0lH"},
			llm.Thinking{Provider: llm.ProviderAnthropic, Signature: "QU5U"},
			llm.Thinking{Provider: llm.ProviderOpenAI, Signature: "rs_1", Data: "enc"},
			llm.Text{Text: "answer"},
		),
		llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "next"}}})
	b, raw := sent(t, req)
	if strings.Contains(raw, "thoughtSignature") {
		t.Fatalf("foreign signature sent: %s", raw)
	}
	if len(b.Contents) != 3 || b.Contents[1].Parts[0]["text"] != "answer" {
		t.Fatalf("contents %s", raw)
	}
}

func TestImageEncoding(t *testing.T) {
	webp := []byte("RIFF\x00\x00\x00\x00WEBP")
	req := hi()
	req.Images = llmtest.Images{"w": webp}
	req.Messages = []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "see"}, llm.Image{AttachmentID: "w", MediaType: "image/webp"}}}}
	b, raw := sent(t, req)
	p := b.Contents[0].Parts
	if len(p) != 2 || p[0]["text"] != "see" {
		t.Fatalf("parts %s", raw)
	}
	inline, _ := p[1]["inlineData"].(map[string]any)
	if inline["mimeType"] != "image/webp" || inline["data"] != base64.StdEncoding.EncodeToString(webp) {
		t.Fatalf("inlineData %v", inline)
	}
	pr, _ := newProvider(t, serveFile(t, "text.sse"))
	req.Images = nil
	if _, err := pr.Stream(context.Background(), req); err == nil {
		t.Fatal("image without resolver accepted")
	}
}

func TestToolsSent(t *testing.T) {
	req := hi()
	req.Tools = []llm.Tool{{Name: "get_doc", Description: "Read one", InputSchema: json.RawMessage(`{"type":"object","properties":{"doctype":{"type":"string"}},"required":["doctype"]}`)}, {Name: "whoami"}}
	b, raw := sent(t, req)
	if len(b.Tools) != 1 {
		t.Fatalf("tools %s", raw)
	}
	decls, _ := b.Tools[0]["functionDeclarations"].([]any)
	if len(decls) != 2 {
		t.Fatalf("declarations %s", raw)
	}
	g := decls[0].(map[string]any)
	ps, _ := g["parametersJsonSchema"].(map[string]any)
	if g["name"] != "get_doc" || g["description"] != "Read one" || ps["required"] == nil {
		t.Fatalf("declaration %v", g)
	}
	w := decls[1].(map[string]any)["parametersJsonSchema"].(map[string]any)
	if w["type"] != "object" {
		t.Fatalf("empty schema %v", w)
	}
}

func TestStopReasons(t *testing.T) {
	cases := []struct {
		file     string
		reason   string
		category string
	}{
		{"safety.sse", llm.StopRefusal, "safety"},
		{"blocked.sse", llm.StopRefusal, "prohibited_content"},
		{"max_tokens.sse", llm.StopMaxTokens, ""},
	}
	for _, c := range cases {
		got, _ := run(t, c.file, hi())
		stop := got[len(got)-1].(llm.Stop)
		if stop.Reason != c.reason || stop.Category != c.category {
			t.Fatalf("%s: %#v", c.file, stop)
		}
	}
}

func TestMidStreamError(t *testing.T) {
	p, _ := newProvider(t, serveFile(t, "midstream_error.sse"))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for {
		_, err := s.Next()
		if err == io.EOF {
			t.Fatal("no error")
		}
		if err != nil {
			var ae *llm.APIError
			if !errors.As(err, &ae) || ae.Status != 503 || !llm.IsRetryable(err) {
				t.Fatalf("err %v", err)
			}
			return
		}
	}
}

func TestStreamEndsEarly(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"}}]}`+"\n\n")
	}))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for {
		_, err := s.Next()
		if err == io.EOF {
			t.Fatal("no error")
		}
		if err != nil {
			if !llm.IsRetryable(err) {
				t.Fatalf("err %v", err)
			}
			return
		}
	}
}

func TestErrorStatus(t *testing.T) {
	keyInvalid := `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key. ` + testKey + `","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`
	cases := []struct {
		status int
		body   string
		want   int
		auth   bool
		rate   bool
	}{
		{400, keyInvalid, 401, true, false},
		{403, `{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`, 403, true, false},
		{429, `{"error":{"code":429,"message":"Quota exceeded ` + testKey + `","status":"RESOURCE_EXHAUSTED"}}`, 429, false, true},
		{400, `{"error":{"code":400,"message":"Bad schema","status":"INVALID_ARGUMENT"}}`, 400, false, false},
		{500, `oops ` + testKey, 500, false, false},
	}
	for _, c := range cases {
		p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, err := p.Stream(context.Background(), hi())
		var ae *llm.APIError
		if !errors.As(err, &ae) || ae.Status != c.want {
			t.Fatalf("status %d: got %v", c.status, err)
		}
		if llm.IsAuth(err) != c.auth || llm.IsRateLimit(err) != c.rate {
			t.Fatalf("status %d: auth=%v rate=%v", c.status, llm.IsAuth(err), llm.IsRateLimit(err))
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("key leaked in %q", err.Error())
		}
		if _, err := p.Models(context.Background()); !errors.As(err, &ae) || ae.Status != c.want || strings.Contains(err.Error(), testKey) {
			t.Fatalf("models %d: %v", c.status, err)
		}
	}
}

func TestCancelMidStream(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := p.Stream(ctx, hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ev, err := s.Next(); err != nil || ev != (llm.TextDelta{Text: "x"}) {
		t.Fatalf("first: %#v %v", ev, err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Next()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	t0 := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if d := time.Since(t0); d > time.Second {
			t.Fatalf("took %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not return within 1s of cancel")
	}
}

func TestModelsFiltered(t *testing.T) {
	p, cap := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[
			{"name":"models/gemini-2.0-flash","displayName":"Gemini 2.0 Flash","supportedGenerationMethods":["generateContent","countTokens"]},
			{"name":"models/gemini-9-pro","displayName":"Gemini 9 Pro","supportedGenerationMethods":["generateContent"]},
			{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]},
			{"name":"models/gemini-embedding-001","supportedGenerationMethods":["embedContent","generateContent"]},
			{"name":"models/gemini-9-flash-preview-tts","supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-9-flash-image","supportedGenerationMethods":["generateContent"]},
			{"name":"models/gemini-9-flash-live","supportedGenerationMethods":["bidiGenerateContent"]},
			{"name":"models/gemma-3-27b-it","supportedGenerationMethods":["generateContent"]}
		]}`)
	}))
	list, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Model{{ID: "gemini-9-pro", Label: "Gemini 9 Pro"}, {ID: "gemini-2.0-flash", Label: "Gemini 2.0 Flash"}}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("models %#v", list)
	}
	if _, _, path, _ := cap.get(); path != "/v1beta/models" {
		t.Fatalf("path %q", path)
	}
}

func TestPinnedHost(t *testing.T) {
	if BaseURL != "https://generativelanguage.googleapis.com/" {
		t.Fatalf("BaseURL %q", BaseURL)
	}
}

func TestOversizedLineAndEvent(t *testing.T) {
	for name, body := range map[string]string{
		"line":  "data: " + strings.Repeat("x", maxLine+1) + "\n\n",
		"event": strings.Repeat("data: "+strings.Repeat("x", 1<<20)+"\n", 9) + "\n",
	} {
		p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, body)
		}))
		_, err := p.Stream(context.Background(), hi())
		var ae *llm.APIError
		if !errors.As(err, &ae) || ae.Status != 502 {
			t.Fatalf("%s: err %v", name, err)
		}
	}
}

// A redirect is never followed: Go would copy x-goog-api-key to the new host.
func TestRedirectNotFollowed(t *testing.T) {
	var hits sync.Map
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Store("other", r.Header.Get("x-goog-api-key"))
	}))
	t.Cleanup(other.Close)
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	_, err := p.Stream(context.Background(), hi())
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Status != 502 {
		t.Fatalf("err %v", err)
	}
	if _, ok := hits.Load("other"); ok {
		t.Fatal("redirect followed")
	}
}

func TestModelsPaged(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.URL.Query().Get("pageToken"))
		mu.Unlock()
		if r.URL.Query().Get("key") != "" {
			t.Error("key in the URL")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-1-a","supportedGenerationMethods":["generateContent"]}],"nextPageToken":"p2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-2-b","displayName":"B","supportedGenerationMethods":["generateContent"]}]}`)
	}))
	list, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Model{{ID: "gemini-2-b", Label: "B"}, {ID: "gemini-1-a", Label: "gemini-1-a"}}
	if !reflect.DeepEqual(list, want) || !reflect.DeepEqual(tokens, []string{"", "p2"}) {
		t.Fatalf("models %#v tokens %q", list, tokens)
	}
}

func TestRetryBeforeStream(t *testing.T) {
	setEnv(t)
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := os.ReadFile(filepath.Join("testdata", "text.sse"))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	p, err := New(testKey, WithBaseURL(srv.URL+"/"), WithMaxRetries(1))
	if err != nil {
		t.Fatal(err)
	}
	p.backoff = time.Millisecond
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, s)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 4 || n != 2 {
		t.Fatalf("attempts %d events %#v", n, got)
	}
}

// Text after a signed text part is its own part: its signature covers only
// its own text.
func TestSignedTextNotMerged(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"A","thoughtSignature":"c2ln"}],"role":"model"}}]}`+"\n\n"+
			`data: {"candidates":[{"content":{"parts":[{"text":"B"}],"role":"model"},"finishReason":"STOP"}]}`+"\n\n")
	}))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, s)
	want := asst(sig("c2ln"), llm.Text{Text: "A"}, llm.Text{Text: "B"})
	if stop := got[len(got)-1].(llm.Stop); !reflect.DeepEqual(stop.Message, want) {
		t.Fatalf("stop %#v", stop.Message)
	}
}

// A signature with no part of its own (on a thought, or a second one for a
// run) is kept but never replayed onto another part.
func TestOrphanSignatureNotReplayed(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"plan","thought":true,"thoughtSignature":"VEhPVUdIVA=="}],"role":"model"}}]}`+"\n\n"+
			`data: {"candidates":[{"content":{"parts":[{"text":"A","thoughtSignature":"Rmlyc3Q="},{"text":"","thoughtSignature":"U2Vjb25k"}],"role":"model"}}]}`+"\n\n"+
			`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"whoami","args":{}}}],"role":"model"},"finishReason":"STOP"}]}`+"\n\n")
	}))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, s)
	stop := got[len(got)-1].(llm.Stop)
	var call llm.ToolUse
	for _, pt := range stop.Message.Parts {
		if u, ok := pt.(llm.ToolUse); ok {
			call = u
		}
	}
	orphan := func(b64 string) llm.Thinking {
		return llm.Thinking{Provider: llm.ProviderGemini, Signature: b64, Data: unattached}
	}
	want := asst(orphan("VEhPVUdIVA=="), sig("Rmlyc3Q="), llm.Text{Text: "A"}, orphan("U2Vjb25k"), call)
	if !reflect.DeepEqual(stop.Message, want) {
		t.Fatalf("stop\n got %#v\nwant %#v", stop.Message, want)
	}
	stored, err := llm.MarshalParts(stop.Message.Parts)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := llm.UnmarshalParts(stored)
	if err != nil {
		t.Fatal(err)
	}
	req := hi()
	req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, Parts: parts},
		llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{ID: call.ID, Text: "me"}}})
	b, raw := sent(t, req)
	if strings.Contains(raw, "VEhPVUdIVA==") || strings.Contains(raw, "U2Vjb25k") {
		t.Fatalf("orphan signature replayed: %s", raw)
	}
	m := b.Contents[1].Parts
	if len(m) != 2 || m[0]["thoughtSignature"] != "Rmlyc3Q=" || m[1]["thoughtSignature"] != nil {
		t.Fatalf("model parts %v", m)
	}
}

// Finish reasons with no usable answer end the stream with an error that
// says why.
func TestFailFinishReasons(t *testing.T) {
	for r := range failReasons {
		p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"},"finishReason":"`+r+`"}]}`+"\n\n")
		}))
		s, err := p.Stream(context.Background(), hi())
		if err != nil {
			t.Fatal(err)
		}
		var last error
		for {
			_, err := s.Next()
			if err != nil {
				last = err
				break
			}
		}
		s.Close()
		var ae *llm.APIError
		if !errors.As(last, &ae) || ae.Status != 502 || ae.Category != strings.ToLower(r) || ae.Message == "" || !llm.IsRetryable(last) {
			t.Fatalf("%s: %v", r, last)
		}
	}
}

// Calls stored without an id are answered by the results without one, in
// order, on both sides.
func TestIDlessCallsAndResults(t *testing.T) {
	req := hi()
	req.Messages = append(req.Messages,
		asst(llm.ToolUse{Name: "a"}, llm.ToolUse{Name: "b"}),
		llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.ToolResult{Text: "ra"}, llm.ToolResult{Text: "rb", IsError: true}}})
	b, raw := sent(t, req)
	u := b.Contents[2].Parts
	if len(u) != 2 {
		t.Fatalf("user turn %s", raw)
	}
	r0, _ := u[0]["functionResponse"].(map[string]any)
	r1, _ := u[1]["functionResponse"].(map[string]any)
	if r0["name"] != "a" || r0["response"].(map[string]any)["output"] != "ra" ||
		r1["name"] != "b" || r1["response"].(map[string]any)["error"] != "rb" {
		t.Fatalf("responses %s", raw)
	}
}

// An error body after the events is read only up to maxErrorBody.
func TestBareErrorBounded(t *testing.T) {
	p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"}}]}`+"\n\n")
		_, _ = io.WriteString(w, `{"error":{"code":503,"message":"`+strings.Repeat("y", 3*maxErrorBody)+`"}}`+"\n")
	}))
	s, err := p.Stream(context.Background(), hi())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for {
		_, err := s.Next()
		if err != nil {
			var ae *llm.APIError
			if !errors.As(err, &ae) || ae.Status != 502 {
				t.Fatalf("err %v", err)
			}
			return
		}
	}
}

// SSE allows CRLF, LF and bare CR line endings, and an event's data may be
// spread over several data lines (joined with a newline).
func TestLineEndings(t *testing.T) {
	crlf, err := os.ReadFile(filepath.Join("testdata", "crlf.sse"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(crlf), "\r\n") {
		t.Fatal("crlf.sse lost its CRLF line endings (see testdata/.gitattributes)")
	}
	lf := strings.ReplaceAll(string(crlf), "\r\n", "\n")
	two := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello, world\"}],\"role\":\"model\"},\n" +
		"data: \"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2}}\n\n"
	for name, body := range map[string]string{
		"crlf":      string(crlf),
		"bare cr":   strings.ReplaceAll(lf, "\n", "\r"),
		"two lines": two,
	} {
		p, _ := newProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, body)
		}))
		s, err := p.Stream(context.Background(), hi())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := collect(t, s)
		var text strings.Builder
		for _, ev := range got {
			if d, ok := ev.(llm.TextDelta); ok {
				text.WriteString(d.Text)
			}
		}
		stop, ok := got[len(got)-1].(llm.Stop)
		if text.String() != "Hello, world" || !ok || stop.Reason != llm.StopEndTurn {
			t.Fatalf("%s: %#v", name, got)
		}
	}
}
