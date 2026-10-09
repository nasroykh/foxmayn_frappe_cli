package store

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPinArchiveStates(t *testing.T) {
	s, _ := openTemp(t)
	a, b := mustConv(t, s, "a"), mustConv(t, s, "b")
	setUpdated(t, s, a.ID, 1000)
	if err := s.SetPinned(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetArchived(b.ID, true); err != nil {
		t.Fatal(err)
	}
	st, err := s.ConvStates()
	if err != nil {
		t.Fatal(err)
	}
	if !st[a.ID].Pinned || st[a.ID].Archived || !st[b.ID].Archived || st[b.ID].Pinned {
		t.Fatalf("states: %+v", st)
	}
	got, _ := s.GetConversation(a.ID)
	if got.Updated.UnixMilli() != 1000 {
		t.Errorf("pinning touched the updated time: %v", got.Updated)
	}
	if err := s.SetPinned(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if one, _ := s.GetConvState(a.ID); one.Pinned {
		t.Error("unpin did not stick")
	}
	if err := s.SetPinned("nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("pin unknown: %v", err)
	}
	if _, err := s.GetConvState("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("state of unknown: %v", err)
	}
}

func TestSettings(t *testing.T) {
	s, _ := openTemp(t)
	if v, err := s.GetSetting("retention_days"); err != nil || v != "" {
		t.Fatalf("unset: %q %v", v, err)
	}
	for _, v := range []string{"30", "90"} {
		if err := s.SetSetting("retention_days", v); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetSetting("retention_days"); got != v {
			t.Errorf("got %q want %q", got, v)
		}
	}
}

func addMsg(t *testing.T, s *Store, convID, role, text string) Message {
	t.Helper()
	m, err := s.AppendMessage(convID, role, fmt.Sprintf(`[{"type":"text","text":%q}]`, text))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSearchConversationsFilters(t *testing.T) {
	s, _ := openTemp(t)
	mk := func(title, site, profile string, updated time.Time, text string) Conversation {
		c, err := s.InsertConversation(Conversation{Title: title, Site: site, Mode: "read", ProviderID: "p", Model: "m", ProfileID: profile})
		if err != nil {
			t.Fatal(err)
		}
		addMsg(t, s, c.ID, "user", text)
		setUpdated(t, s, c.ID, updated.UnixMilli())
		return c
	}
	d := func(day int) time.Time { return time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC) }
	c1 := mk("one", "prod", "explore", d(1), "overdue invoices of the quarter")
	c2 := mk("two", "prod", "", d(10), "overdue invoices again")
	c3 := mk("three", "dev", "explore", d(20), "overdue invoices in dev")
	mk("four", "prod", "", d(25), "something else entirely")
	if err := s.SetArchived(c2.ID, true); err != nil {
		t.Fatal(err)
	}
	ids := func(hits []SearchHit) string {
		var out []string
		for _, h := range hits {
			out = append(out, h.ConvID)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	want := func(cs ...Conversation) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	cases := []struct {
		name string
		f    SearchFilter
		want string
	}{
		{"no filter hides archived", SearchFilter{}, want(c1, c3)},
		{"archived view", SearchFilter{Archived: true}, want(c2)},
		{"site", SearchFilter{Site: "dev"}, want(c3)},
		{"profile", SearchFilter{ProfileID: "explore"}, want(c1, c3)},
		{"from", SearchFilter{From: d(15)}, want(c3)},
		{"to", SearchFilter{To: d(15)}, want(c1)},
		{"range", SearchFilter{From: d(1), To: d(20)}, want(c1, c3)},
		{"site and profile", SearchFilter{Site: "prod", ProfileID: "explore"}, want(c1)},
	}
	for _, c := range cases {
		hits, err := s.SearchConversations("overdue invoices", c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := ids(hits); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	hits, _ := s.SearchConversations("overdue", SearchFilter{Site: "dev"})
	if len(hits) != 1 || hits[0].Title != "three" || hits[0].Site != "dev" || !strings.Contains(hits[0].Snippet, "overdue") || hits[0].MsgID == "" {
		t.Fatalf("hit: %+v", hits)
	}
	if hits, _ := s.SearchConversations("   ", SearchFilter{}); hits != nil {
		t.Errorf("empty query: %v", hits)
	}
	if hits, _ := s.SearchConversations("overdue", SearchFilter{Limit: 1}); len(hits) != 1 {
		t.Errorf("limit: %d", len(hits))
	}
	// Quoted tokens: FTS syntax in the query is literal text.
	if _, err := s.SearchConversations(`NEAR( "x" OR AND`, SearchFilter{}); err != nil {
		t.Errorf("odd query: %v", err)
	}
}

func TestSearchSkipsEphemeralAndDeletesIt(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "secret")
	addMsg(t, s, c.ID, "user", "private talk")
	if err := s.SetEphemeral(c.ID, true); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.SearchConversations("private", SearchFilter{}); len(hits) != 0 {
		t.Errorf("ephemeral found: %v", hits)
	}
	n, err := s.DeleteEphemeral()
	if err != nil || n != 1 {
		t.Fatalf("delete ephemeral: %d %v", n, err)
	}
	if count(t, s, "messages") != 0 || count(t, s, "conversations") != 0 {
		t.Error("ephemeral rows left")
	}
}

func TestSweepRetention(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -100).UnixMilli()
	fresh := now.AddDate(0, 0, -5).UnixMilli()
	mk := func(title string, updated int64) Conversation {
		c := mustConv(t, s, title)
		addMsg(t, s, c.ID, "user", "words in "+title)
		setUpdated(t, s, c.ID, updated)
		return c
	}
	oldPlain := mk("old plain", old)
	oldPinned := mk("old pinned", old)
	oldActive := mk("old active", old)
	oldPaused := mk("old paused", old)
	young := mk("young", fresh)
	oldWithRun := mk("old with finished run", old)
	if err := s.SetPinned(oldPinned.ID, true); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(oldPaused.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, "paused", "", 25); err != nil {
		t.Fatal(err)
	}
	r2, _ := s.CreateRun(oldWithRun.ID)
	_ = s.FinishRun(r2.ID, "done", "", 1)
	if _, err := s.InsertToolCall(r2.ID, "", "list_docs", "acme", "{}", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(Usage{RunID: r2.ID, Turn: 1, Input: 1, Output: 1}); err != nil {
		t.Fatal(err)
	}

	n, err := s.SweepRetention(now.AddDate(0, 0, -90), []string{oldActive.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted %d, want 2", n)
	}
	for _, c := range []Conversation{oldPlain, oldWithRun} {
		if _, err := s.GetConversation(c.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s survived: %v", c.Title, err)
		}
	}
	for _, c := range []Conversation{oldPinned, oldActive, oldPaused, young} {
		if _, err := s.GetConversation(c.ID); err != nil {
			t.Errorf("%s was deleted: %v", c.Title, err)
		}
	}
	if hits, _ := s.SearchConversations("old plain", SearchFilter{}); len(hits) != 0 {
		t.Errorf("search still finds a deleted conversation: %v", hits)
	}
	if got := count(t, s, "runs"); got != 1 {
		t.Errorf("runs: %d, want the paused one", got)
	}
	for _, tbl := range []string{"tool_calls", "usage"} {
		if got := count(t, s, tbl); got != 0 {
			t.Errorf("%s: %d rows left", tbl, got)
		}
	}
}

// seedBulk fills a store with convs conversations of msgs messages each,
// drawn from a fixed vocabulary, in one transaction.
func seedBulk(t *testing.T, s *Store, convs, msgs int) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	vocab := make([]string, 300)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("w%03dx", i)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	now := nowMS()
	for c := 0; c < convs; c++ {
		id := fmt.Sprintf("conv%04d", c)
		site := []string{"prod", "dev", "staging"}[c%3]
		if _, err := tx.Exec(`INSERT INTO conversations(id,title,site,mode,provider_id,model,created,updated) VALUES(?,?,?,?,?,?,?,?)`,
			id, "Conversation "+id, site, "read", "p", "m", now, now-int64(c)*1000); err != nil {
			t.Fatal(err)
		}
		for m := 0; m < msgs; m++ {
			words := make([]string, 40)
			for i := range words {
				words[i] = vocab[rng.Intn(len(vocab))]
			}
			role := "user"
			if m%2 == 1 {
				role = "assistant"
			}
			if _, err := tx.Exec(`INSERT INTO messages(id,conv_id,seq,role,parts_json,created) VALUES(?,?,?,?,?,?)`,
				fmt.Sprintf("%s-m%02d", id, m), id, m+1, role, fmt.Sprintf(`[{"type":"text","text":%q}]`, strings.Join(words, " ")), now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return vocab
}

// TestSearchSpeed: 500 conversations of 20 messages, 20 queries (some with
// filters), p95 under 200 ms. The timing is not checked under -race.
func TestSearchSpeed(t *testing.T) {
	s, _ := openTemp(t)
	vocab := seedBulk(t, s, 500, 20)
	rng := rand.New(rand.NewSource(11))
	var times []time.Duration
	for i := 0; i < 20; i++ {
		q := vocab[rng.Intn(len(vocab))]
		if i%2 == 1 {
			q += " " + vocab[rng.Intn(len(vocab))]
		}
		f := SearchFilter{}
		if i%4 == 3 {
			f.Site = "dev"
			f.From = time.Now().Add(-time.Hour)
		}
		start := time.Now()
		hits, err := s.SearchConversations(q, f)
		times = append(times, time.Since(start))
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if i == 0 && len(hits) == 0 {
			t.Errorf("query %q found nothing", q)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p95 := times[18] // 20 samples: the 19th is the 95th percentile
	t.Logf("p95 %v, max %v", p95, times[19])
	if raceEnabled {
		t.Skip("timing is not checked under -race")
	}
	if p95 >= 200*time.Millisecond {
		t.Errorf("p95 %v, want < 200ms", p95)
	}
}

func TestImportConversationMakesNewIDsAndRefusesBadRefs(t *testing.T) {
	s, _ := openTemp(t)
	in := ExportData{
		Conversation: Conversation{Title: "t", Site: "acme", Mode: "read", ProviderID: "p", Model: "m"},
		Messages: []Message{
			{ID: "m1", Role: "user", PartsJSON: `[{"type":"text","text":"hello"}]`, Created: time.UnixMilli(5)},
			{ID: "m2", Role: "assistant", PartsJSON: `[{"type":"tool_use","id":"u1","name":"x","args":{}}]`, Created: time.UnixMilli(6)},
		},
		Runs:      []Run{{ID: "r1", Status: "done", Steps: 1, Started: time.UnixMilli(5), Ended: time.UnixMilli(7)}},
		ToolCalls: []ToolCall{{ID: "t1", RunID: "r1", MsgID: "m2", Tool: "x", Site: "acme", ArgsJSON: "{}", Status: "ok", Started: time.UnixMilli(6)}},
		Usage:     []UsageRow{{RunID: "r1", Turn: 1, Input: 3, Output: 4}},
	}
	c, err := s.ImportConversation(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.ReadConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Conversation.ID == "" || len(out.Messages) != 2 || len(out.Runs) != 1 || len(out.ToolCalls) != 1 || len(out.Usage) != 1 {
		t.Fatalf("read back: %+v", out)
	}
	for _, id := range []string{out.Messages[0].ID, out.Messages[1].ID, out.Runs[0].ID, out.ToolCalls[0].ID} {
		if id == "m1" || id == "m2" || id == "r1" || id == "t1" {
			t.Errorf("a file id was kept: %s", id)
		}
	}
	if out.ToolCalls[0].MsgID != out.Messages[1].ID || out.ToolCalls[0].RunID != out.Runs[0].ID || out.Usage[0].RunID != out.Runs[0].ID {
		t.Error("links were not remapped")
	}
	if out.Messages[0].Created.UnixMilli() != 5 || out.Runs[0].Ended.UnixMilli() != 7 {
		t.Error("message and run times should be kept")
	}
	if hits, _ := s.SearchConversations("hello", SearchFilter{}); len(hits) != 1 {
		t.Errorf("imported text is not searchable: %v", hits)
	}

	bad := map[string]func(d *ExportData){
		"tool call to an unknown run":     func(d *ExportData) { d.ToolCalls[0].RunID = "zzz" },
		"tool call to an unknown message": func(d *ExportData) { d.ToolCalls[0].MsgID = "zzz" },
		"usage to an unknown run":         func(d *ExportData) { d.Usage[0].RunID = "zzz" },
		"duplicate message id":            func(d *ExportData) { d.Messages[1].ID = "m1" },
		"duplicate run id":                func(d *ExportData) { d.Runs = append(d.Runs, d.Runs[0]) },
		"empty message id":                func(d *ExportData) { d.Messages[0].ID = "" },
		"duplicate usage row":             func(d *ExportData) { d.Usage = append(d.Usage, d.Usage[0]) },
	}
	before := count(t, s, "conversations")
	for name, mut := range bad {
		d := in
		d.Messages = append([]Message(nil), in.Messages...)
		d.Runs = append([]Run(nil), in.Runs...)
		d.ToolCalls = append([]ToolCall(nil), in.ToolCalls...)
		d.Usage = append([]UsageRow(nil), in.Usage...)
		mut(&d)
		if _, err := s.ImportConversation(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := count(t, s, "conversations"); got != before {
		t.Errorf("a refused import left %d conversations behind", got-before)
	}
}
