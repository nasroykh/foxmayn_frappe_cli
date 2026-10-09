package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

func TestAssistantAddressChangeClearsTheKey(t *testing.T) {
	g := newAssistantRig(t)
	g.provider(t)
	same, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1", Label: "Renamed"})
	if err != nil || same.KeyCleared || !same.KeySet {
		t.Fatalf("same address: %+v, %v", same, err)
	}
	moved, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "https://llm.example.com/v1"})
	if err != nil || !moved.KeyCleared || moved.KeySet {
		t.Fatalf("moved: %+v, %v", moved, err)
	}
	if _, err := g.keys.Get("p1"); err == nil {
		t.Error("the key survived an address change")
	}
	// A kind change is refused outright.
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindOllama}); errorCode(t, err) != CodeInvalid {
		t.Errorf("kind change: %v", err)
	}
}

type recordingTransport struct {
	mu    sync.Mutex
	hosts []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.hosts = append(rt.hosts, r.URL.Host)
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: 401, Status: "401 Unauthorized", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   http.NoBody, Request: r,
	}, nil
}

func TestAssistantHostedProvidersArePinned(t *testing.T) {
	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer evil.Close()
	g := newAssistantRig(t)
	for _, kind := range []string{KindAnthropic, KindOpenRouter} {
		_, err := g.a.SaveProvider(ProviderInfo{Kind: kind, BaseURL: evil.URL})
		if errorCode(t, err) != CodeInvalid || err.(*Error).Field != "baseURL" {
			t.Errorf("%s with an evil address: %v", kind, err)
		}
	}
	// The official address is accepted; what is stored is the pinned value.
	p, err := g.a.SaveProvider(ProviderInfo{Kind: KindAnthropic, BaseURL: "https://api.anthropic.com/"})
	if err != nil || p.BaseURL != "" {
		t.Fatalf("anthropic = %+v, %v", p, err)
	}
	// A row written behind the service's back still cannot redirect the key:
	// the real client ignores the stored address.
	for _, row := range []store.Provider{
		{ID: "anthropic", Kind: KindAnthropic, Label: "A", BaseURL: evil.URL},
		{ID: "openrouter", Kind: KindOpenRouter, Label: "O", BaseURL: evil.URL},
	} {
		if err := g.a.st.UpsertProvider(row); err != nil {
			t.Fatal(err)
		}
		rt := &recordingTransport{}
		old := http.DefaultTransport
		http.DefaultTransport = rt
		prov, err := makeProvider(row, "sk-live-0123456789abcdefPIN1")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, _ = prov.Models(ctx)
		cancel()
		http.DefaultTransport = old
		if len(rt.hosts) == 0 {
			t.Fatalf("%s: no request seen", row.Kind)
		}
		want := "api.anthropic.com"
		if row.Kind == KindOpenRouter {
			want = "openrouter.ai"
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

// blockingProvider's Models waits for release or its context.
type blockingProvider struct {
	*llmtest.Provider
	entered chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func (p *blockingProvider) Models(ctx context.Context) ([]llm.Model, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, p.err
}

func TestAssistantSetKeyConcurrentEndsWithTheGoodKey(t *testing.T) {
	for i := 0; i < 20; i++ {
		g := newAssistantRig(t)
		if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1"}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var badErr, goodErr error
		wg.Add(2)
		go func() { defer wg.Done(); badErr = g.a.SetKey("p1", "sk-bad-0123456789abcdefBAD1") }()
		go func() { defer wg.Done(); goodErr = g.a.SetKey("p1", goodKey) }()
		wg.Wait()
		if goodErr != nil || errorCode(t, badErr) != CodeAuth {
			t.Fatalf("good = %v, bad = %v", goodErr, badErr)
		}
		if got, _ := g.keys.Get("p1"); got != goodKey {
			t.Fatalf("round %d: key = %q", i, got)
		}
	}
}

func TestAssistantDeleteDuringASlowCheckLeavesNoKey(t *testing.T) {
	g := newAssistantRig(t)
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1"}); err != nil {
		t.Fatal(err)
	}
	bp := &blockingProvider{Provider: g.prov, entered: make(chan struct{}), release: make(chan struct{})}
	g.a.mk = func(store.Provider, string) (llm.Provider, error) { return bp, nil }
	setDone := make(chan error, 1)
	go func() { setDone <- g.a.SetKey("p1", goodKey) }()
	<-bp.entered
	delDone := make(chan error, 1)
	go func() { delDone <- g.a.DeleteProvider("p1") }()
	select {
	case <-delDone:
		t.Fatal("DeleteProvider did not wait for the check")
	case <-time.After(100 * time.Millisecond):
	}
	close(bp.release)
	if err := <-setDone; err != nil {
		t.Fatal(err)
	}
	if err := <-delDone; err != nil {
		t.Fatal(err)
	}
	if _, err := g.keys.Get("p1"); err == nil {
		t.Error("the key outlived the delete")
	}
}

func TestAssistantSetKeyServerErrorSavesNothing(t *testing.T) {
	g := newAssistantRig(t)
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1"}); err != nil {
		t.Fatal(err)
	}
	g.a.mk = func(_ store.Provider, key string) (llm.Provider, error) {
		return &blockingProvider{Provider: g.prov, entered: make(chan struct{}), release: closedChan(), err: &llm.APIError{Status: 500, Message: "boom " + key}}, nil
	}
	err := g.a.SetKey("p1", goodKey)
	if errorCode(t, err) != CodeNetwork || strings.Contains(err.Error(), goodKey) {
		t.Fatalf("err = %v", err)
	}
	if _, err := g.keys.Get("p1"); err == nil {
		t.Error("a key that could not be checked was saved")
	}
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func TestAssistantShutdownWaitsForCallsAndThenRefuses(t *testing.T) {
	g := newAssistantRig(t)
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1"}); err != nil {
		t.Fatal(err)
	}
	bp := &blockingProvider{Provider: g.prov, entered: make(chan struct{}), release: make(chan struct{})}
	g.a.mk = func(store.Provider, string) (llm.Provider, error) { return bp, nil }
	setDone := make(chan error, 1)
	go func() { setDone <- g.a.SetKey("p1", goodKey) }()
	<-bp.entered
	if err := g.a.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-setDone:
		if errorCode(t, err) != CodeNetwork {
			t.Errorf("in-flight call = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight call never ended")
	}
	if _, err := g.a.ListConversations(); errorCode(t, err) != CodeUnavailable {
		t.Errorf("after shutdown: %v", err)
	}
	if _, err := g.a.Send("c", "hi"); errorCode(t, err) != CodeUnavailable {
		t.Errorf("send after shutdown: %v", err)
	}
	if err := g.a.SetKey("p1", goodKey); errorCode(t, err) != CodeUnavailable {
		t.Errorf("setkey after shutdown: %v", err)
	}
}

func TestLoopContinueSeesTheCurrentMode(t *testing.T) {
	var turns []llmtest.Turn
	for i := 0; i < loopStepBudget; i++ {
		turns = append(turns, toolTurn(call(fmt.Sprint("c", i), "get_doc", `{"doctype":"ToDo","name":"TD-1"}`)))
	}
	turns = append(turns, textTurn("finished"))
	g := newLoopRig(t, turns...)
	cid := g.conv(t, "ask")
	runID, err := g.r.start(cid, "loop")
	if err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunPaused {
		t.Fatalf("done = %+v", d)
	}
	if !hasWriteTool(g.prov.Requests()[0].Tools) {
		t.Fatal("ask mode offered no write tool")
	}
	if err := g.st.SetConversationMode(cid, "read"); err != nil {
		t.Fatal(err)
	}
	if err := g.r.continueRun(runID); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 2); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	rq := g.prov.Requests()
	if hasWriteTool(rq[len(rq)-1].Tools) {
		t.Error("the resumed run is still in ask mode")
	}
	// A conversation deleted while paused cannot continue.
	if err := g.st.DeleteConversation(cid); err != nil {
		t.Fatal(err)
	}
	if err := g.r.continueRun(runID); err == nil {
		t.Error("continued a run of a deleted conversation")
	}
}

func hasWriteTool(tools []llm.Tool) bool {
	for _, tl := range tools {
		if tl.Name == "create_doc" || tl.Name == "update_doc" || tl.Name == "delete_doc" {
			return true
		}
	}
	return false
}

func TestApprovalUnaskedWriteEndsTheRun(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "create_doc", `{"doctype":"ToDo","data":{"description":"new"}}`)), textTurn("never"))
	// Stage a call that ffc owed a question for but did not ask.
	g.r.afterCall = func(e *callEntry) {
		if e.call.Name == "create_doc" {
			e.cls.WillAsk = true
		}
	}
	cid := g.conv(t, "ask")
	runID, err := g.r.start(cid, "add")
	if err != nil {
		t.Fatal(err)
	}
	card := g.waitApproval(t, 1)
	if err := g.r.answer(cid, card.ApprovalID, true); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunError {
		t.Fatalf("done = %+v", d)
	}
	if ev := g.h.named(EventChatError); len(ev) != 1 || !strings.Contains(ev[0].(ChatError).Error.Message, "without the confirmation") {
		t.Errorf("errors = %+v", ev)
	}
	calls, _ := g.st.ListToolCalls(runID)
	if len(calls) != 1 || calls[0].Status != ToolError || !strings.HasPrefix(calls[0].ResultText, approvalUnaskedWarning) {
		t.Errorf("calls = %+v", calls)
	}
	if g.fake.Count("ToDo") != 4 {
		t.Error("the change should have been applied")
	}
	if n := len(g.prov.Requests()); n != 1 {
		t.Errorf("%d model turns after the violation", n)
	}
	res := g.toolResults(t, cid)
	if len(res) != 1 || !res[0].IsError || !strings.Contains(res[0].Text, "applied") {
		t.Errorf("results = %+v", res)
	}
}

func TestSameValueNormalisesOnlyFlagsAndNumbers(t *testing.T) {
	for _, tc := range []struct {
		a, b any
		want bool
	}{
		{"0123", "123", false},
		{" 5", "5", false},
		{"1.0", "1", false},
		{"5", "5", true},
		{true, float64(1), true},
		{float64(1), true, true},
		{float64(5), "5", true},
		{"5", float64(5), true},
		{false, float64(0), true},
		{float64(5), " 5", false},
		{float64(1), "1e0", false},
		{float64(1), "Inf", false},
		{float64(5), "6", false},
	} {
		if got := sameValue(tc.a, tc.b); got != tc.want {
			t.Errorf("sameValue(%#v, %#v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	d := diffFields(map[string]any{"code": "123", "a": "5", "b": "1"}, map[string]any{"code": "0123", "a": " 5", "b": "1.0"})
	if len(d) != 3 {
		t.Errorf("diff = %+v", d)
	}
}

func TestCheckBaseURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://localhost.evil.com":      false,
		"http://127.0.0.1.nip.io":        false,
		"http://localhost@evil.com":      false,
		"http://[::1]:1234":              true,
		"HTTP://localhost:11434":         true,
		"http://localhost:11434/v1":      true,
		"http://127.0.0.1:1234":          true,
		"https://api.example.com/v1":     true,
		"http://example.com":             false,
		"https://user:pw@example.com/v1": false,
	} {
		err := checkBaseURL(raw)
		if (err == nil) != ok {
			t.Errorf("checkBaseURL(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestAssistantModelNamesAreChecked(t *testing.T) {
	g := newAssistantRig(t)
	g.provider(t)
	long := strings.Repeat("m", maxModelChars+1)
	for _, bad := range []string{long, "gpt\x00x", "a\nb", "a\u0085b"} {
		if _, err := g.a.NewConversation("prod", ModeRead, "p1", bad); errorCode(t, err) != CodeInvalid || err.(*Error).Field != "model" {
			t.Errorf("NewConversation(%q): %v", bad, err)
		}
		if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1", DefaultModel: bad}); errorCode(t, err) != CodeInvalid || err.(*Error).Field != "defaultModel" {
			t.Errorf("SaveProvider(%q): %v", bad, err)
		}
	}
	if _, err := g.a.NewConversation("prod", ModeRead, "p1", strings.Repeat("m", maxModelChars)); err != nil {
		t.Errorf("a 200 character model: %v", err)
	}
}
