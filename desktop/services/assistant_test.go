package services

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/openaicompat"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const goodKey = "sk-live-0123456789abcdefGOOD"

// keyedProvider is a scripted provider whose Models call accepts one key only.
type keyedProvider struct {
	*llmtest.Provider
	key, good string
}

func (p keyedProvider) Models(ctx context.Context) ([]llm.Model, error) {
	if p.key != p.good {
		return nil, &llm.APIError{Status: 401, Message: "invalid key " + p.key}
	}
	return p.Provider.Models(ctx)
}

type assistantRig struct {
	a      *AssistantService
	h      *fakeHost
	fake   *frappetest.Site
	path   string
	dbPath string
	keys   *Keys
	prov   *llmtest.Provider
}

func newAssistantRig(t *testing.T, turns ...llmtest.Turn) *assistantRig {
	t.Helper()
	_, fake, path := engineSite(t)
	h := &fakeHost{}
	prov := llmtest.New(turns...)
	prov.SetModels([]llm.Model{{ID: "m1", Label: "Model One"}, {ID: "claude-sonnet-5-5", Label: "Sonnet"}})
	a := NewAssistantService(h, path)
	a.keys = newMemKeys()
	a.storePath = filepath.Join(t.TempDir(), "assistant.db")
	a.mk = func(_ store.Provider, key string) (llm.Provider, error) {
		return keyedProvider{prov, key, goodKey}, nil
	}
	if err := a.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.ServiceShutdown() })
	a.run.backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return &assistantRig{a: a, h: h, fake: fake, path: path, dbPath: a.storePath, keys: a.keys, prov: prov}
}

// provider saves a custom provider "p1" with a good key.
func (g *assistantRig) provider(t *testing.T) {
	t.Helper()
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://127.0.0.1:9/v1", DefaultModel: "m1"}); err != nil {
		t.Fatal(err)
	}
	if err := g.a.SetKey("p1", goodKey); err != nil {
		t.Fatal(err)
	}
}

func (g *assistantRig) conv(t *testing.T, mode string) Conversation {
	t.Helper()
	g.provider(t)
	c, err := g.a.NewConversation("prod", mode, "p1", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (g *assistantRig) done(t *testing.T, n int) ChatDone {
	t.Helper()
	waitFor(t, func() bool { return len(g.h.named(EventChatDone)) >= n })
	return g.h.named(EventChatDone)[n-1].(ChatDone)
}

func errorCode(t *testing.T, err error) string {
	t.Helper()
	se, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	return se.Code
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertClean scans every event, every returned value and the store file for
// the secrets.
func (g *assistantRig) assertClean(t *testing.T, convID string, secrets ...string) {
	t.Helper()
	var blobs [][]byte
	g.h.mu.Lock()
	for _, e := range g.h.events {
		blobs = append(blobs, mustJSON(t, e.data))
	}
	g.h.mu.Unlock()
	add := func(v any, err error) {
		if err != nil {
			t.Fatal(err)
		}
		blobs = append(blobs, mustJSON(t, v))
	}
	add(g.a.GetConversation(convID))
	add(g.a.ListConversations())
	add(g.a.ListProviders())
	add(g.a.KeyStatus("p1"))
	add(g.a.ListModels("p1"))
	blobs = append(blobs, mustJSON(t, g.a.PendingApprovals(convID)))
	for _, b := range blobs {
		for _, s := range secrets {
			if strings.Contains(string(b), s) {
				t.Fatalf("secret %q found in %.200s", s, b)
			}
		}
	}
	// The store keeps thinking signatures (the provider needs them back), so
	// only the API keys are looked for in its file.
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(g.dbPath + suffix)
		if err != nil {
			continue
		}
		for _, s := range secrets {
			if strings.HasPrefix(s, "sk-") && strings.Contains(string(raw), s) {
				t.Fatalf("key %q found in the store file %s", s, g.dbPath+suffix)
			}
		}
	}
}

func TestAssistantSendReadsAndLeaksNoKey(t *testing.T) {
	g := newAssistantRig(t,
		llmtest.Turn{Events: []llm.Event{
			llm.Thinking{Text: "pondering", Signature: "SIG-SECRET-SIGNATURE"},
			llm.TextDelta{Text: "Looking. "},
			call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`),
			llm.Stop{Reason: llm.StopToolUse},
		}},
		textTurn("It says alpha."),
	)
	c := g.conv(t, "")
	if c.Mode != ModeRead || c.Model != "m1" || c.Title != "" {
		t.Fatalf("conv = %+v", c)
	}
	runID, err := g.a.Send(c.ID, "what is TD-1?")
	if err != nil || runID == "" {
		t.Fatalf("Send = %q, %v", runID, err)
	}
	if d := g.done(t, 1); d.Status != RunDone || d.RunID != runID {
		t.Fatalf("done = %+v", d)
	}
	det, err := g.a.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if det.Conversation.Title != "what is TD-1?" || det.ActiveRunID != "" || det.PausedRunID != "" {
		t.Errorf("detail = %+v", det)
	}
	if len(det.Messages) != 3 || det.Messages[0].Role != "user" || det.Messages[1].Role != "assistant" || det.Messages[2].Text != "It says alpha." {
		t.Fatalf("messages = %+v", det.Messages)
	}
	tools := det.Messages[1].Tools
	if det.Messages[1].Text != "Looking. " || len(tools) != 1 || tools[0].Tool != "get_doc" || tools[0].Status != ToolOK || !strings.Contains(tools[0].Summary, "TD-1") {
		t.Errorf("assistant message = %+v", det.Messages[1])
	}
	// The row's id is the callID of its chat:tool events.
	found := false
	for _, e := range g.h.named(EventChatTool) {
		if tools[0].ID != "" && e.(ChatTool).CallID == tools[0].ID {
			found = true
		}
	}
	if !found {
		t.Error("no chat:tool event carries the call id")
	}
	// Nothing that leaves Go holds the key or a thinking signature.
	g.assertClean(t, c.ID, goodKey, "SIG-SECRET-SIGNATURE")
}

func TestAssistantProviderErrorsLeakNoKey(t *testing.T) {
	g := newAssistantRig(t)
	c := g.conv(t, ModeRead)
	// The provider rejects the stored key and echoes it in its error.
	g.a.mk = func(_ store.Provider, key string) (llm.Provider, error) {
		return llmtest.New(llmtest.Turn{StreamErr: &llm.APIError{Status: 401, Message: "bad key " + key}}), nil
	}
	if _, err := g.a.Send(c.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	if d := g.done(t, 1); d.Status != RunError {
		t.Fatalf("done = %+v", d)
	}
	if ev := g.h.named(EventChatError); len(ev) != 1 {
		t.Fatalf("errors = %v", ev)
	}
	// The redactor catches a key-shaped string whatever the adapter did.
	g.assertClean(t, c.ID, goodKey)
}

func TestAssistantSetKeyRejectedLeavesNoKey(t *testing.T) {
	g := newAssistantRig(t)
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindCustom, BaseURL: "http://localhost:1/v1"}); err != nil {
		t.Fatal(err)
	}
	bad := "sk-bad-0123456789abcdefBAD1"
	err := g.a.SetKey("p1", bad)
	if errorCode(t, err) != CodeAuth {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), bad) {
		t.Errorf("error holds the key: %v", err)
	}
	if st, _ := g.a.KeyStatus("p1"); st.Set {
		t.Errorf("rejected key kept: %+v", st)
	}
	if _, err := g.keys.Get("p1"); err == nil {
		t.Error("keychain holds the rejected key")
	}
	// A good key is kept; a later bad one does not replace it.
	if err := g.a.SetKey("p1", goodKey); err != nil {
		t.Fatal(err)
	}
	if err := g.a.SetKey("p1", bad); errorCode(t, err) != CodeAuth {
		t.Fatalf("second bad key: %v", err)
	}
	if got, _ := g.keys.Get("p1"); got != goodKey {
		t.Errorf("key = %q, want the earlier good one", got)
	}
	list, err := g.a.ListProviders()
	if err != nil || len(list) != 1 || !list[0].KeySet || list[0].KeyLast4 != "GOOD" {
		t.Fatalf("providers = %+v, %v", list, err)
	}
	for _, s := range []string{goodKey, bad} {
		if strings.Contains(string(mustJSON(t, list)), s) {
			t.Errorf("ListProviders holds %q", s)
		}
	}
	if err := g.a.SetKey("nope", goodKey); errorCode(t, err) != CodeNotFound {
		t.Errorf("unknown provider: %v", err)
	}
	if err := g.a.SetKey("p1", "  "); errorCode(t, err) != CodeInvalid {
		t.Errorf("blank key: %v", err)
	}
}

func TestAssistantDeleteProviderDeletesKey(t *testing.T) {
	g := newAssistantRig(t)
	g.provider(t)
	if err := g.a.DeleteProvider("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.keys.Get("p1"); err == nil {
		t.Error("key outlived its provider")
	}
	if list, _ := g.a.ListProviders(); len(list) != 0 {
		t.Errorf("providers = %+v", list)
	}
	if err := g.a.DeleteProvider("p1"); errorCode(t, err) != CodeNotFound {
		t.Errorf("second delete: %v", err)
	}
}

func TestAssistantSaveProviderValidation(t *testing.T) {
	g := newAssistantRig(t)
	for name, tc := range map[string]struct {
		p     ProviderInfo
		field string
	}{
		"kind":       {ProviderInfo{Kind: "vertex"}, "kind"},
		"http":       {ProviderInfo{Kind: KindCustom, BaseURL: "http://example.com/v1"}, "baseURL"},
		"no host":    {ProviderInfo{Kind: KindCustom, BaseURL: "https://"}, "baseURL"},
		"userinfo":   {ProviderInfo{Kind: KindCustom, BaseURL: "https://u:p@example.com/v1"}, "baseURL"},
		"ftp":        {ProviderInfo{Kind: KindCustom, BaseURL: "ftp://example.com"}, "baseURL"},
		"custom url": {ProviderInfo{Kind: KindCustom}, "baseURL"},
		"id":         {ProviderInfo{ID: "Bad Id", Kind: KindOllama}, "provider"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := g.a.SaveProvider(tc.p)
			if errorCode(t, err) != CodeInvalid || err.(*Error).Field != tc.field {
				t.Errorf("err = %#v", err)
			}
		})
	}
	// Defaults and ids.
	p, err := g.a.SaveProvider(ProviderInfo{Kind: KindAnthropic})
	if err != nil || p.ID != "anthropic" || p.Label != "Anthropic" || p.DefaultModel != "claude-sonnet-5-5" || p.BaseURL != "" {
		t.Fatalf("anthropic = %+v, %v", p, err)
	}
	p, err = g.a.SaveProvider(ProviderInfo{Kind: KindOllama})
	if err != nil || p.ID != "ollama" || p.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("ollama = %+v, %v", p, err)
	}
	for _, want := range []string{"custom", "custom-2", "custom-3"} {
		p, err = g.a.SaveProvider(ProviderInfo{Kind: KindCustom, BaseURL: "https://llm.example.com/v1"})
		if err != nil || p.ID != want {
			t.Fatalf("custom = %+v, %v (want %s)", p, err, want)
		}
	}
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "ollama", Kind: KindLMStudio}); errorCode(t, err) != CodeInvalid {
		t.Errorf("kind change accepted: %v", err)
	}
	p, err = g.a.SaveProvider(ProviderInfo{ID: "ollama", Kind: KindOllama, Label: "Mine", DefaultModel: " llama3 "})
	if err != nil || p.Label != "Mine" || p.DefaultModel != "llama3" {
		t.Fatalf("update = %+v, %v", p, err)
	}
	if list, _ := g.a.ListProviders(); len(list) != 5 {
		t.Errorf("%d providers", len(list))
	}
}

func TestAssistantNewConversationValidation(t *testing.T) {
	g := newAssistantRig(t)
	g.provider(t)
	for i, tc := range []struct {
		site, mode, prov string
		code, field      string
	}{
		{"prod", "write", "p1", CodeInvalid, "mode"},
		{"", "read", "p1", CodeInvalid, "site"},
		{"nosuch", "read", "p1", CodeNotFound, "site"},
		{"prod", "read", "ghost", CodeNotFound, "provider"},
		{"prod", "read", "Bad Id", CodeInvalid, "provider"},
	} {
		_, err := g.a.NewConversation(tc.site, tc.mode, tc.prov, "")
		if err == nil || errorCode(t, err) != tc.code || err.(*Error).Field != tc.field {
			t.Errorf("case %d: err = %#v", i, err)
		}
	}
	c, err := g.a.NewConversation("prod", ModeAsk, "p1", " other-model ")
	if err != nil || c.Mode != ModeAsk || c.Model != "other-model" {
		t.Fatalf("conv = %+v, %v", c, err)
	}
	list, err := g.a.ListConversations()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := g.a.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.a.GetConversation(c.ID); errorCode(t, err) != CodeNotFound {
		t.Errorf("deleted conversation: %v", err)
	}
	if list, _ := g.a.ListConversations(); list == nil || len(list) != 0 {
		t.Errorf("list = %#v", list)
	}
}

func TestAssistantModeSwitchRefusedDuringRun(t *testing.T) {
	g := newAssistantRig(t, toolTurn(call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`)), textTurn("done"))
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	g.fake.Handle("GET /api/resource/ToDo/TD-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	c := g.conv(t, ModeRead)
	runID, err := g.a.Send(c.ID, "go")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("tool never called")
	}
	if err := g.a.SetConversationMode(c.ID, ModeAsk); errorCode(t, err) != CodeInvalid {
		t.Errorf("mode switch during a run: %v", err)
	}
	if err := g.a.DeleteConversation(c.ID); errorCode(t, err) != CodeInvalid {
		t.Errorf("delete during a run: %v", err)
	}
	if _, err := g.a.Send(c.ID, "again"); errorCode(t, err) != CodeInvalid {
		t.Errorf("second send: %v", err)
	}
	det, _ := g.a.GetConversation(c.ID)
	if det.ActiveRunID != runID {
		t.Errorf("active run = %q", det.ActiveRunID)
	}
	if len(det.Messages) != 2 || len(det.Messages[1].Tools) != 1 || det.Messages[1].Tools[0].Status != ToolRunning {
		t.Errorf("messages mid-run = %+v", det.Messages)
	}
	t0 := time.Now()
	g.a.Cancel(runID)
	if d := g.done(t, 1); d.Status != RunCancelled || time.Since(t0) > time.Second {
		t.Errorf("done = %+v after %v", d, time.Since(t0))
	}
	close(release)
	if err := g.a.SetConversationMode(c.ID, ModeAsk); err != nil {
		t.Fatalf("mode switch after the run: %v", err)
	}
	if err := g.a.SetConversationMode(c.ID, "root"); errorCode(t, err) != CodeInvalid {
		t.Errorf("bad mode: %v", err)
	}
	if err := g.a.SetConversationMode("nope", ModeRead); errorCode(t, err) != CodeNotFound {
		t.Errorf("unknown conversation: %v", err)
	}
	det, _ = g.a.GetConversation(c.ID)
	if det.Conversation.Mode != ModeAsk || det.Messages[1].Tools[0].Status != ToolStopped {
		t.Errorf("detail = %+v", det)
	}
}

func TestAssistantAnswerChecksTheConversation(t *testing.T) {
	g := newAssistantRig(t, toolTurn(call("c1", "create_doc", `{"doctype":"ToDo","data":{"description":"new"}}`)), textTurn("done"))
	c := g.conv(t, ModeAsk)
	other, err := g.a.NewConversation("prod", ModeRead, "p1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.a.Send(c.ID, "add a todo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(g.h.named(EventChatApproval)) == 1 })
	card := g.h.named(EventChatApproval)[0].(ChatApproval)
	if p := g.a.PendingApprovals(c.ID); len(p) != 1 || p[0].ApprovalID != card.ApprovalID {
		t.Fatalf("pending = %+v", p)
	}
	if p := g.a.PendingApprovals(other.ID); p == nil || len(p) != 0 {
		t.Errorf("pending of another = %#v", p)
	}
	if err := g.a.Answer(other.ID, card.ApprovalID, true); errorCode(t, err) != CodeNotFound {
		t.Errorf("answer from the wrong conversation: %v", err)
	}
	if g.fake.Count("ToDo") != 3 {
		t.Fatal("the wrong conversation approved a change")
	}
	if err := g.a.Answer(c.ID, card.ApprovalID, true); err != nil {
		t.Fatal(err)
	}
	if d := g.done(t, 1); d.Status != RunDone {
		t.Fatalf("done = %+v", d)
	}
	if g.fake.Count("ToDo") != 4 {
		t.Errorf("%d ToDos", g.fake.Count("ToDo"))
	}
	det, _ := g.a.GetConversation(c.ID)
	if tc := det.Messages[1].Tools[0]; tc.Approval != ApprovalApproved || tc.Status != ToolOK {
		t.Errorf("tool = %+v", tc)
	}
	g.assertClean(t, c.ID, goodKey)
}

func TestAssistantContinueNeedsAPausedRun(t *testing.T) {
	g := newAssistantRig(t, textTurn("hi"))
	c := g.conv(t, ModeRead)
	runID, err := g.a.Send(c.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	g.done(t, 1)
	if err := g.a.Continue(runID); errorCode(t, err) != CodeInvalid {
		t.Errorf("continue a finished run: %v", err)
	}
	if err := g.a.Continue("nope"); errorCode(t, err) != CodeNotFound {
		t.Errorf("continue an unknown run: %v", err)
	}
	if _, err := g.a.Send(c.ID, strings.Repeat("x", maxSendChars+1)); errorCode(t, err) != CodeInvalid {
		t.Errorf("long message: %v", err)
	}
	if _, err := g.a.Send("nope", "hi"); errorCode(t, err) != CodeNotFound {
		t.Errorf("unknown conversation: %v", err)
	}
}

func TestAssistantListModelsAndDetectLocal(t *testing.T) {
	g := newAssistantRig(t)
	if _, err := g.a.SaveProvider(ProviderInfo{Kind: KindAnthropic}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.a.ListModels("anthropic"); errorCode(t, err) != CodeAuth {
		t.Errorf("no key: %v", err)
	}
	if err := g.a.SetKey("anthropic", goodKey); err != nil {
		t.Fatal(err)
	}
	ms, err := g.a.ListModels("anthropic")
	if err != nil || len(ms) != 2 || ms[0].Default || !ms[1].Default || ms[1].ID != "claude-sonnet-5-5" {
		t.Fatalf("models = %+v, %v", ms, err)
	}
	// A local server needs no key.
	if _, err := g.a.SaveProvider(ProviderInfo{Kind: KindOllama}); err != nil {
		t.Fatal(err)
	}
	g.a.mk = func(_ store.Provider, key string) (llm.Provider, error) {
		if key != "" {
			t.Errorf("local provider got key %q", key)
		}
		return g.prov, nil
	}
	if ms, err := g.a.ListModels("ollama"); err != nil || len(ms) != 2 {
		t.Errorf("ollama models = %+v, %v", ms, err)
	}
	g.a.detect = func(context.Context) []openaicompat.Preset {
		return []openaicompat.Preset{openaicompat.Ollama, openaicompat.LMStudio}
	}
	got := g.a.DetectLocal()
	if len(got) != 2 || got[0].ID != "ollama" || got[0].Kind != KindOllama || got[1].Kind != KindLMStudio || got[1].BaseURL != "http://localhost:1234/v1" {
		t.Errorf("detected = %+v", got)
	}
	g.a.detect = func(context.Context) []openaicompat.Preset { return nil }
	if got := g.a.DetectLocal(); got == nil || len(got) != 0 {
		t.Errorf("none detected = %#v", got)
	}
}

func TestAssistantRemovedProviderFailsTheRun(t *testing.T) {
	g := newAssistantRig(t)
	c := g.conv(t, ModeRead)
	if err := g.a.DeleteProvider("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.a.Send(c.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	if d := g.done(t, 1); d.Status != RunError {
		t.Fatalf("done = %+v", d)
	}
	ev := g.h.named(EventChatError)
	if len(ev) != 1 || ev[0].(ChatError).Error.Code != CodeNotFound {
		t.Errorf("errors = %+v", ev)
	}
}

func TestAssistantConfigChangedInvalidatesTheEngine(t *testing.T) {
	g := newAssistantRig(t, textTurn("hi"), textTurn("again"))
	c := g.conv(t, ModeRead)
	if _, err := g.a.Send(c.ID, "one"); err != nil {
		t.Fatal(err)
	}
	g.done(t, 1)
	count := func() int {
		g.a.engine.mu.Lock()
		defer g.a.engine.mu.Unlock()
		return len(g.a.engine.servers)
	}
	if count() == 0 {
		t.Fatal("no server cached after a run")
	}
	g.h.Emit(EventConfigChanged, ConfigChanged{Path: g.path, Exists: true})
	if count() != 0 {
		t.Error("config:changed left the cached servers")
	}
	if _, err := g.a.Send(c.ID, "two"); err != nil {
		t.Fatal(err)
	}
	if d := g.done(t, 2); d.Status != RunDone {
		t.Errorf("done = %+v", d)
	}
}

func TestAssistantUnavailableWithoutAStore(t *testing.T) {
	a := NewAssistantService(&fakeHost{}, "x")
	a.storePath = t.TempDir() // a directory, not a database file
	if err := a.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup must not stop the app: %v", err)
	}
	if _, err := a.ListConversations(); errorCode(t, err) != CodeUnavailable {
		t.Errorf("err = %v", err)
	}
	if _, err := a.Send("c", "hi"); errorCode(t, err) != CodeUnavailable {
		t.Errorf("err = %v", err)
	}
	if p := a.PendingApprovals("c"); p == nil || len(p) != 0 {
		t.Errorf("pending = %#v", p)
	}
	a.Cancel("r")
	if err := a.ServiceShutdown(); err != nil {
		t.Error(err)
	}
}
