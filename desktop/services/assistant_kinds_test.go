package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// cloudTransport answers as api.openai.com and the Gemini API would: the
// model list for goodKey, a refusal that echoes any other key, or a 500 that
// echoes the key when fail is set. It records every request.
type cloudTransport struct {
	mu   sync.Mutex
	reqs []*http.Request
	fail atomic.Bool
}

func (ct *cloudTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ct.mu.Lock()
	ct.reqs = append(ct.reqs, r.Clone(context.Background()))
	ct.mu.Unlock()
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.URL.Host == "generativelanguage.googleapis.com" {
		key = r.Header.Get("x-goog-api-key")
	}
	status, body := 200, ""
	switch {
	case ct.fail.Load():
		status, body = 500, `{"error":{"code":500,"message":"internal trouble with `+key+`","status":"INTERNAL"}}`
	case key != goodKey && r.URL.Host == "api.openai.com":
		status, body = 401, `{"error":{"message":"Incorrect API key provided: `+key+`","type":"invalid_request_error","code":"invalid_api_key"}}`
	case key != goodKey:
		status, body = 400, `{"error":{"code":400,"message":"API key not valid: `+key+`","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`
	case r.URL.Host == "api.openai.com":
		body = `{"object":"list","data":[{"id":"gpt-a","object":"model","created":1,"owned_by":"openai"},{"id":"gpt-b","object":"model","created":2,"owned_by":"openai"}]}`
	default:
		body = `{"models":[{"name":"models/gemini-a","displayName":"Gemini A","supportedGenerationMethods":["generateContent"]}]}`
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   io.NopCloser(strings.NewReader(body)), Request: r,
	}, nil
}

func (ct *cloudTransport) requests() []*http.Request {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return append([]*http.Request(nil), ct.reqs...)
}

// useTransport routes every default-client request through rt for the test.
func useTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = old })
}

func TestAssistantOpenAIGeminiArePinned(t *testing.T) {
	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer evil.Close()
	g := newAssistantRig(t)
	for _, kind := range []string{KindOpenAI, KindGemini} {
		if _, err := g.a.SaveProvider(ProviderInfo{Kind: kind, BaseURL: evil.URL}); errorCode(t, err) != CodeInvalid || err.(*Error).Field != "baseURL" {
			t.Errorf("%s with an evil address: %v", kind, err)
		}
	}
	// The official address is accepted; what is stored is the pinned value,
	// with the kind's label.
	for kind, official := range map[string]string{
		KindOpenAI: "https://api.openai.com/v1/",
		KindGemini: "https://generativelanguage.googleapis.com/",
	} {
		p, err := g.a.SaveProvider(ProviderInfo{Kind: kind, BaseURL: official})
		if err != nil || p.BaseURL != "" || p.ID != kind || p.DefaultModel != "" {
			t.Fatalf("%s = %+v, %v", kind, p, err)
		}
		if want := map[string]string{KindOpenAI: "OpenAI", KindGemini: "Google Gemini"}[kind]; p.Label != want {
			t.Errorf("%s label %q", kind, p.Label)
		}
	}
	// A row written behind the service's back still cannot redirect the key.
	for _, row := range []store.Provider{
		{ID: "openai", Kind: KindOpenAI, Label: "O", BaseURL: evil.URL},
		{ID: "gemini", Kind: KindGemini, Label: "G", BaseURL: evil.URL},
	} {
		if err := g.a.st.UpsertProvider(row); err != nil {
			t.Fatal(err)
		}
		rt := &recordingTransport{}
		old := http.DefaultTransport
		http.DefaultTransport = rt
		prov, err := makeProvider(row, "sk-live-0123456789abcdefPIN1")
		if err != nil {
			http.DefaultTransport = old
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, _ = prov.Models(ctx)
		cancel()
		http.DefaultTransport = old
		if len(rt.hosts) == 0 {
			t.Fatalf("%s: no request seen", row.Kind)
		}
		want := "api.openai.com"
		if row.Kind == KindGemini {
			want = "generativelanguage.googleapis.com"
		}
		for _, h := range rt.hosts {
			if h != want {
				t.Errorf("%s: request to %q", row.Kind, h)
			}
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the evil server got %d requests", n)
	}
}

// SetKey checks a key with the provider's model list and keeps it only when
// it is accepted; neither the refused key nor the saved one reaches an error,
// the provider list or the model list, and the key goes only in its header.
func TestAssistantOpenAIGeminiSetKeyLeaksNoKey(t *testing.T) {
	ct := &cloudTransport{}
	useTransport(t, ct)
	g := newAssistantRig(t)
	g.a.mk = makeProvider
	bad := "sk-bad-0123456789abcdefBAD1"
	for _, kind := range []string{KindOpenAI, KindGemini} {
		ct.fail.Store(false)
		p, err := g.a.SaveProvider(ProviderInfo{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		// No key yet: the provider cannot be used.
		if _, err := g.a.ListModels(p.ID); errorCode(t, err) != CodeAuth {
			t.Fatalf("%s without a key: %v", kind, err)
		}
		err = g.a.SetKey(p.ID, bad)
		if errorCode(t, err) != CodeAuth {
			t.Fatalf("%s bad key: %v", kind, err)
		}
		if strings.Contains(err.Error(), bad) || strings.Contains(err.(*Error).Detail, bad) {
			t.Fatalf("%s: error holds the key: %#v", kind, err)
		}
		if _, err := g.keys.Get(p.ID); err == nil {
			t.Fatalf("%s: rejected key kept", kind)
		}
		if err := g.a.SetKey(p.ID, goodKey); err != nil {
			t.Fatalf("%s good key: %v", kind, err)
		}
		models, err := g.a.ListModels(p.ID)
		if err != nil || len(models) == 0 {
			t.Fatalf("%s models = %+v, %v", kind, models, err)
		}
		for _, m := range models {
			if m.Default {
				t.Errorf("%s: %s flagged default without a chosen model", kind, m.ID)
			}
		}
		if kind == KindOpenAI && models[0].ID != "gpt-b" {
			t.Errorf("openai models not newest first: %+v", models)
		}
		ct.fail.Store(true)
		_, err = g.a.ListModels(p.ID)
		if err == nil || strings.Contains(err.Error(), goodKey) || strings.Contains(err.(*Error).Detail, goodKey) {
			t.Fatalf("%s failing list: %#v", kind, err)
		}
		list, _ := g.a.ListProviders()
		for _, s := range []string{goodKey, bad} {
			if strings.Contains(string(mustJSON(t, list)), s) {
				t.Errorf("ListProviders holds %q", s)
			}
		}
	}
	for _, r := range ct.requests() {
		if strings.Contains(r.URL.String(), goodKey) || strings.Contains(r.URL.String(), bad) {
			t.Errorf("key in the URL %s", r.URL)
		}
		switch r.URL.Host {
		case "api.openai.com":
			if r.Header.Get("x-goog-api-key") != "" {
				t.Errorf("openai request carries a Gemini header")
			}
		case "generativelanguage.googleapis.com":
			if r.Header.Get("Authorization") != "" {
				t.Errorf("gemini request carries Authorization")
			}
		default:
			t.Errorf("request to %s", r.URL.Host)
		}
	}
	// Saving the same pinned provider again keeps its key.
	p, err := g.a.SaveProvider(ProviderInfo{ID: KindGemini, Kind: KindGemini, Label: "Mine"})
	if err != nil || p.KeyCleared || !p.KeySet {
		t.Fatalf("re-save: %+v, %v", p, err)
	}
}

// Every adapter kind is cloud unless it is a known local one; the new kinds
// need a key like the other hosted ones.
func TestAssistantNewKindsNeedAKey(t *testing.T) {
	for _, kind := range []string{KindOpenAI, KindGemini} {
		if !validKind(kind) || !keyRequired(kind) || officialBase(kind) == "" {
			t.Errorf("%s: valid=%v key=%v base=%q", kind, validKind(kind), keyRequired(kind), officialBase(kind))
		}
	}
	if _, err := makeProvider(store.Provider{ID: "gemini", Kind: KindGemini}, ""); errorCode(t, err) != CodeAuth {
		t.Errorf("gemini without a key: %v", err)
	}
}
