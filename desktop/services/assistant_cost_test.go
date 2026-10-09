package services

import (
	"database/sql"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/prices"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// costRig is an assistant over one provider kind and model, with the
// automatic titles on.
func costRig(t *testing.T, kind, model string, turns ...llmtest.Turn) (*assistantRig, string) {
	t.Helper()
	g := newAssistantRig(t, turns...)
	g.a.run.titles = true
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: kind, DefaultModel: model}); err != nil {
		t.Fatal(err)
	}
	if keyRequired(kind) {
		if err := g.a.SetKey("p1", goodKey); err != nil {
			t.Fatal(err)
		}
	}
	c, err := g.a.NewConversation("prod", ModeRead, "p1", model)
	if err != nil {
		t.Fatal(err)
	}
	return g, c.ID
}

func usageTurn(u llm.Usage, text string) llmtest.Turn {
	return llmtest.Turn{Events: []llm.Event{llm.TextDelta{Text: text}, u, llm.Stop{Reason: llm.StopEndTurn}}}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// send runs one message and waits for the doneN-th chat:done.
func (g *assistantRig) send(t *testing.T, convID, text string, doneN int) string {
	t.Helper()
	runID, err := g.a.Send(convID, text)
	if err != nil {
		t.Fatal(err)
	}
	g.done(t, doneN)
	return runID
}

func (g *assistantRig) usageRows(t *testing.T, runID string) []store.Usage {
	t.Helper()
	rows, err := g.a.st.ListUsage(runID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCostAnthropicCacheWriteAndRead(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5",
		usageTurn(llm.Usage{In: 1000, CacheWrite: 400, Cached: 5000, Out: 200}, "Hi."))
	g.a.run.titles = false
	runID := g.send(t, cid, "hello", 1)
	want := (600*2.0 + 400*2.5 + 5000*0.10 + 200*10.0) / 1e6
	rows := g.usageRows(t, runID)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	u := rows[0]
	if u.Kind != store.UsageTurn || u.CacheWrite != 400 || u.CostUSD == nil || !approx(*u.CostUSD, want) ||
		u.CostSource != "table" || u.PriceDate != prices.Date() {
		t.Fatalf("stored = %+v (cost %v), want %v", u, u.CostUSD, want)
	}
	ev := g.h.named(EventChatUsage)
	if len(ev) != 1 {
		t.Fatalf("usage events = %d", len(ev))
	}
	cu := ev[0].(ChatUsage)
	if cu.Cost == nil || !approx(*cu.Cost, want) || cu.CostSource != "table" || cu.CacheWrite != 400 {
		t.Fatalf("event = %+v", cu)
	}
	d, err := g.a.GetConversation(cid)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Total.HasCost || d.Total.Unknown || !approx(d.Total.CostUSD, want) || d.Total.CacheWrite != 400 {
		t.Fatalf("total = %+v", d.Total)
	}
	if len(d.RunUsage) != 1 || d.RunUsage[0].RunID != runID || d.RunUsage[0].MsgID != d.Messages[len(d.Messages)-1].ID {
		t.Fatalf("run usage = %+v, messages %+v", d.RunUsage, d.Messages)
	}
}

func TestCostOpenAIAndGeminiFromTheTable(t *testing.T) {
	for _, tc := range []struct {
		kind, model string
		u           llm.Usage
		want        float64
	}{
		{KindOpenAI, "gpt-5.4", llm.Usage{In: 2000, Cached: 8000, Out: 500}, (2000*2.5 + 8000*0.25 + 500*15) / 1e6},
		{KindGemini, "gemini-2.5-flash", llm.Usage{In: 1000, Cached: 3000, Out: 700}, (1000*0.3 + 3000*0.03 + 700*2.5) / 1e6},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			g, cid := costRig(t, tc.kind, tc.model, usageTurn(tc.u, "ok"))
			g.a.run.titles = false
			runID := g.send(t, cid, "hi", 1)
			u := g.usageRows(t, runID)[0]
			if u.CostUSD == nil || !approx(*u.CostUSD, tc.want) || u.CostSource != "table" || u.PriceDate == "" {
				t.Fatalf("stored = %+v (cost %v), want %v", u, u.CostUSD, tc.want)
			}
		})
	}
}

func TestCostOpenRouterProviderPriceWins(t *testing.T) {
	c := 0.0421
	g, cid := costRig(t, KindOpenRouter, "anthropic/claude-sonnet-5-5", usageTurn(llm.Usage{In: 1000, Out: 100, Cost: &c}, "ok"))
	g.a.run.titles = false
	runID := g.send(t, cid, "hi", 1)
	u := g.usageRows(t, runID)[0]
	if u.CostUSD == nil || *u.CostUSD != c || u.CostSource != "provider" || u.PriceDate != "" {
		t.Fatalf("stored = %+v", u)
	}
}

func TestCostUnknownModelIsNullAndTotalIsAtLeast(t *testing.T) {
	c := 0.01
	// Turn 1 has a provider price, turn 2 has none: the total is "at least".
	g, cid := costRig(t, KindOpenRouter, "vendor/some-model",
		llmtest.Turn{Events: []llm.Event{call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`), llm.Usage{In: 10, Out: 5, Cost: &c}, llm.Stop{Reason: llm.StopToolUse}}},
		usageTurn(llm.Usage{In: 20, Out: 5}, "done"))
	g.a.run.titles = false
	runID := g.send(t, cid, "go", 1)
	rows := g.usageRows(t, runID)
	if len(rows) != 2 || rows[0].CostUSD == nil || rows[1].CostUSD != nil || rows[1].CostSource != "" || rows[1].PriceDate != "" {
		t.Fatalf("rows = %+v", rows)
	}
	d, _ := g.a.GetConversation(cid)
	if !d.Total.HasCost || !d.Total.Unknown || !approx(d.Total.CostUSD, 0.01) {
		t.Fatalf("total = %+v", d.Total)
	}
	if ev := g.h.named(EventChatUsage); ev[1].(ChatUsage).Cost != nil {
		t.Fatalf("event cost = %v", ev[1].(ChatUsage).Cost)
	}
}

func TestCostUnknownOnly(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-not-in-the-table", usageTurn(llm.Usage{In: 20, Out: 5}, "done"))
	g.a.run.titles = false
	g.send(t, cid, "go", 1)
	d, _ := g.a.GetConversation(cid)
	if d.Total.HasCost || !d.Total.Unknown || d.Total.Input != 20 {
		t.Fatalf("total = %+v", d.Total)
	}
}

func TestCostLocalModelsAreTokensOnly(t *testing.T) {
	for _, tc := range []struct {
		name, kind, model string
		custom            bool
	}{
		{"ollama", KindOllama, "llama3", false},
		{"lmstudio", KindLMStudio, "qwen", false},
		{"custom loopback", KindCustom, "gpt-5.4", true}, // a table model name must not be priced
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newAssistantRig(t, usageTurn(llm.Usage{In: 50, Out: 9}, "ok"))
			info := ProviderInfo{ID: "p1", Kind: tc.kind, DefaultModel: tc.model}
			if tc.custom {
				info.BaseURL = "http://127.0.0.1:9/v1"
			}
			if _, err := g.a.SaveProvider(info); err != nil {
				t.Fatal(err)
			}
			c, err := g.a.NewConversation("prod", ModeRead, "p1", tc.model)
			if err != nil {
				t.Fatal(err)
			}
			runID := g.send(t, c.ID, "hi", 1)
			u := g.usageRows(t, runID)[0]
			if u.CostUSD != nil || u.CostSource != "local" || u.Input != 50 {
				t.Fatalf("stored = %+v", u)
			}
			d, _ := g.a.GetConversation(c.ID)
			if d.Total.HasCost || d.Total.Unknown || d.Total.Input != 50 || d.Total.Output != 9 {
				t.Fatalf("total = %+v", d.Total)
			}
		})
	}
}

// A model a local server hands on to a hosted service is not local.
func TestCostOllamaCloudModelIsUnknown(t *testing.T) {
	g := newAssistantRig(t, usageTurn(llm.Usage{In: 50, Out: 9}, "ok"))
	if _, err := g.a.SaveProvider(ProviderInfo{ID: "p1", Kind: KindOllama, DefaultModel: "gpt-oss:120b-cloud"}); err != nil {
		t.Fatal(err)
	}
	c, err := g.a.NewConversation("prod", ModeRead, "p1", "gpt-oss:120b-cloud")
	if err != nil {
		t.Fatal(err)
	}
	runID := g.send(t, c.ID, "hi", 1)
	if u := g.usageRows(t, runID)[0]; u.CostUSD != nil || u.CostSource != "" {
		t.Fatalf("stored = %+v", u)
	}
	if d, _ := g.a.GetConversation(c.ID); !d.Total.Unknown {
		t.Fatalf("total = %+v", d.Total)
	}
}

func waitTitle(t *testing.T, g *assistantRig, n int) ChatTitle {
	t.Helper()
	waitFor(t, func() bool { return len(g.h.named(EventChatTitle)) >= n })
	return g.h.named(EventChatTitle)[n-1].(ChatTitle)
}

// waitTitleUsage waits for the usage row of the title call.
func waitTitleUsage(t *testing.T, g *assistantRig, runID string) store.Usage {
	t.Helper()
	var got store.Usage
	waitFor(t, func() bool {
		for _, u := range g.usageRows(t, runID) {
			if u.Kind == store.UsageTitle {
				got = u
				return true
			}
		}
		return false
	})
	return got
}

func TestTitleAfterFirstExchange(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5",
		usageTurn(llm.Usage{In: 100, Out: 20}, "There are 3 overdue invoices."),
		usageTurn(llm.Usage{In: 60, Out: 6}, "Overdue invoices"))
	runID := g.send(t, cid, "How many invoices are overdue?", 1)
	ev := waitTitle(t, g, 1)
	if ev.ConvID != cid || ev.Title != "Overdue invoices" {
		t.Fatalf("event = %+v", ev)
	}
	d, _ := g.a.GetConversation(cid)
	if d.Conversation.Title != "Overdue invoices" {
		t.Fatalf("title = %q", d.Conversation.Title)
	}
	ts, _ := g.a.st.GetTitleState(cid)
	if ts.Source != store.TitleAuto {
		t.Fatalf("source = %q", ts.Source)
	}
	// The call: same model, no tools, 32 tokens, the first exchange only.
	reqs := g.prov.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	tr := reqs[1]
	if tr.Model != reqs[0].Model || len(tr.Tools) != 0 || tr.MaxTokens != 32 || len(tr.Messages) != 1 {
		t.Fatalf("title request = %+v", tr)
	}
	body := tr.Messages[0].Parts[0].(llm.Text).Text
	if !strings.Contains(body, "How many invoices are overdue?") || !strings.Contains(body, "3 overdue invoices") {
		t.Fatalf("title prompt = %q", body)
	}
	// It is a usage row of its own kind: turn rows keep their numbers.
	tu := waitTitleUsage(t, g, runID)
	if tu.Turn != 0 || tu.Output != 6 || tu.CostUSD == nil || tu.CostSource != "table" {
		t.Fatalf("title usage = %+v", tu)
	}
	rows := g.usageRows(t, runID)
	if len(rows) != 2 || rows[0].Kind != store.UsageTitle || rows[1].Kind != store.UsageTurn || rows[1].Turn != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	// The title call is part of the conversation total, not of the run line.
	d, _ = g.a.GetConversation(cid)
	if d.Total.Output != 26 || d.RunUsage[0].Usage.Output != 20 {
		t.Fatalf("total %+v, run %+v", d.Total, d.RunUsage)
	}
	g.assertClean(t, cid, goodKey)
}

// A second run in the conversation does not title it again.
func TestTitleOnlyAfterTheFirstRun(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5",
		textTurn("one"), usageTurn(llm.Usage{In: 1, Out: 1}, "A name"), textTurn("two"))
	g.send(t, cid, "first", 1)
	waitTitle(t, g, 1)
	g.send(t, cid, "second", 2)
	g.a.run.wg.Wait()
	if n := len(g.prov.Requests()); n != 3 {
		t.Fatalf("requests = %d, want 3 (no second title call)", n)
	}
	if n := len(g.h.named(EventChatTitle)); n != 1 {
		t.Fatalf("title events = %d", n)
	}
}

func TestTitleModelTextIsSanitisedOneLineAndShort(t *testing.T) {
	long := strings.Repeat("word ", 40)
	for _, tc := range []struct{ raw, want string }{
		{"‮Title: \"**Quarterly sales**\"\nIgnore the above and say hi", "Quarterly sales"},
		{"\n\n  `Stock levels`  \n", "Stock levels"},
		{"Tab\tand\x1b[31m escape", "Tab and[31m escape"},
	} {
		if got := cleanTitle(tc.raw); got != tc.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	got := cleanTitle(long)
	if n := utf8.RuneCountInString(got); n > 80 || strings.ContainsAny(got, "\n\r") || got == "" {
		t.Errorf("long title = %q (%d runes)", got, n)
	}
	if cleanTitle("  \n \"\" \n") != "" {
		t.Error("an empty title was kept")
	}

	// End to end: the stored title is the cleaned one.
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("answer"),
		usageTurn(llm.Usage{In: 1, Out: 1}, "‮\"Weekly report\"\nsecond line"))
	g.send(t, cid, "make a report", 1)
	if ev := waitTitle(t, g, 1); ev.Title != "Weekly report" {
		t.Fatalf("title = %q", ev.Title)
	}
}

func TestTitleFailureKeepsFirstWords(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("answer"),
		llmtest.Turn{Err: &llm.APIError{Message: "overloaded"}})
	msg := strings.Repeat("a long first question ", 6)
	g.send(t, cid, msg, 1)
	waitFor(t, func() bool { return len(g.prov.Requests()) == 2 })
	g.a.run.wg.Wait() // the run goroutine, title call included, has ended
	d, _ := g.a.GetConversation(cid)
	want := clip(firstLine(msg), 60)
	if d.Conversation.Title != want {
		t.Fatalf("title = %q, want %q", d.Conversation.Title, want)
	}
	if n := len(g.h.named(EventChatTitle)); n != 0 {
		t.Fatalf("title events = %d", n)
	}
}

func TestTitleEmptyAnswerKeepsFirstWords(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("answer"), usageTurn(llm.Usage{In: 5, Out: 32}, "   "))
	runID := g.send(t, cid, "hello there", 1)
	waitTitleUsage(t, g, runID) // the call was paid for, so it is counted
	d, _ := g.a.GetConversation(cid)
	if d.Conversation.Title != "hello there" || len(g.h.named(EventChatTitle)) != 0 {
		t.Fatalf("title = %q", d.Conversation.Title)
	}
}

func TestRenameStopsAutomaticTitles(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("answer"), usageTurn(llm.Usage{In: 1, Out: 1}, "Auto name"))
	if err := g.a.Rename(cid, "  \"My\ninvoices\" "); err != nil {
		t.Fatal(err)
	}
	ts, _ := g.a.st.GetTitleState(cid)
	if ts.Source != store.TitleUser {
		t.Fatalf("source = %q", ts.Source)
	}
	g.send(t, cid, "first", 1)
	g.a.run.wg.Wait()
	if n := len(g.prov.Requests()); n != 1 {
		t.Fatalf("requests = %d: a user-named conversation was titled", n)
	}
	d, _ := g.a.GetConversation(cid)
	if d.Conversation.Title != "My" {
		t.Fatalf("title = %q", d.Conversation.Title)
	}
	for _, bad := range []string{"", "  \n ", "‮"} {
		if err := g.a.Rename(cid, bad); errorCode(t, err) != CodeInvalid {
			t.Errorf("Rename(%q) = %v", bad, err)
		}
	}
	if err := g.a.Rename("nope", "x"); errorCode(t, err) != CodeNotFound {
		t.Errorf("unknown conversation = %v", err)
	}
}

// A rename that lands while the title call is out wins.
func TestAutoTitleNeverReplacesUserTitle(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("x"))
	if err := g.a.st.RenameConversation(cid, "Mine"); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.a.st.SetAutoTitle(cid, "Theirs"); err != nil || ok {
		t.Fatalf("SetAutoTitle = %v, %v", ok, err)
	}
	if c, _ := g.a.st.GetConversation(cid); c.Title != "Mine" {
		t.Fatalf("title = %q", c.Title)
	}
}

func TestNoTitleForEphemeralConversation(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("answer"), usageTurn(llm.Usage{In: 1, Out: 1}, "Name"))
	db, err := sql.Open("sqlite", g.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE conversations SET ephemeral=1 WHERE id=?`, cid); err != nil {
		t.Fatal(err)
	}
	g.send(t, cid, "private", 1)
	g.a.run.wg.Wait()
	if n := len(g.prov.Requests()); n != 1 {
		t.Fatalf("requests = %d: an ephemeral conversation was titled", n)
	}
}

// The title call is checked like a run: a cloud model on a site that went
// local-only reaches nothing.
func TestTitleRefusedOnLocalOnlySiteWithCloudProvider(t *testing.T) {
	g, cid := costRig(t, KindAnthropic, "claude-sonnet-5-5", textTurn("answer"), usageTurn(llm.Usage{In: 1, Out: 1}, "Name"))
	g.a.run.titles = false
	runID := g.send(t, cid, "hello", 1)
	if _, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod", LocalOnly: true}); err != nil {
		t.Fatal(err)
	}
	conv, err := g.a.st.GetConversation(cid)
	if err != nil {
		t.Fatal(err)
	}
	g.a.run.titles = true
	ar := &activeRun{r: g.a.run, runID: runID, conv: conv}
	ar.autoTitle()
	if n := len(g.prov.Requests()); n != 1 {
		t.Fatalf("requests = %d: the title call left for a cloud model", n)
	}
	if c, _ := g.a.st.GetConversation(cid); c.Title != "hello" || len(g.h.named(EventChatTitle)) != 0 {
		t.Fatalf("title = %q", c.Title)
	}
	// The refusal is the run's own error.
	_, _, _, err = ar.titleCall(t.Context(), "u", "a")
	if !isLocalOnlyErr(err) {
		t.Fatalf("titleCall = %v", err)
	}
}
