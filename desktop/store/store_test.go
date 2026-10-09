package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "assistant.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mustConv(t *testing.T, s *Store, title string) Conversation {
	t.Helper()
	c, err := s.CreateConversation(title, "acme", "read", "prov1", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func setUpdated(t *testing.T, s *Store, convID string, ms int64) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE conversations SET updated=? WHERE id=?`, ms, convID); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Skip("no user config dir")
	}
	if filepath.Base(p) != "assistant.db" || filepath.Base(filepath.Dir(p)) != "Foxmayn Frappe Desktop" {
		t.Fatalf("path = %q", p)
	}
}

func TestMigrateTwiceIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := mustConv(t, s, "keep me")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s.Close()
	if _, err := s.GetConversation(c.ID); err != nil {
		t.Fatalf("data lost across reopen: %v", err)
	}
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	var fk, bt int
	_ = s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	_ = s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&bt)
	var jm string
	_ = s.db.QueryRow(`PRAGMA journal_mode`).Scan(&jm)
	if fk != 1 || bt <= 0 || jm != "wal" {
		t.Fatalf("pragmas fk=%d busy=%d journal=%s", fk, bt, jm)
	}
}

func TestNewerVersionRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)+1)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "newer app version") {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentOpenFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	const n = 4
	stores := make([]*Store, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = Open(path)
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("open %d: %v", i, errs[i])
		}
		defer stores[i].Close()
	}
	var v int
	if err := stores[0].db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("user_version = %d, %v", v, err)
	}
}

func TestOddPaths(t *testing.T) {
	name := "my dir #1 100% what"
	if runtime.GOOS != "windows" {
		name += "?"
	}
	dir := filepath.Join(t.TempDir(), name)
	path := filepath.Join(dir, "assistant file#.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c := mustConv(t, s, "odd")
	_ = s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db not at the requested path: %v", err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.GetConversation(c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "assistant.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustConv(t, s, "c") // makes sure -wal exists
	// Reopen: the sidecar files exist now and must be tightened too.
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil {
			if strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
				continue
			}
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", filepath.Base(p), fi.Mode().Perm())
		}
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}
}

func TestConversationCRUD(t *testing.T) {
	s, _ := openTemp(t)
	a := mustConv(t, s, "first")
	b := mustConv(t, s, "second")
	if a.ID == b.ID || len(a.ID) != 32 {
		t.Fatalf("ids %q %q", a.ID, b.ID)
	}
	got, err := s.GetConversation(a.ID)
	if err != nil || got.Title != "first" || got.Site != "acme" || got.Mode != "read" || got.ProviderID != "prov1" || got.Model != "claude-sonnet-5-5" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if got.Created.Location().String() != "UTC" {
		t.Fatalf("not UTC: %v", got.Created.Location())
	}
	setUpdated(t, s, a.ID, 1000)
	setUpdated(t, s, b.ID, 2000)
	list, err := s.ListConversations()
	if err != nil || len(list) != 2 || list[0].ID != b.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// A message bumps updated, so a moves to the front.
	if _, err := s.AppendMessage(a.ID, "user", `[{"text":"hi"}]`); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListConversations()
	if list[0].ID != a.ID {
		t.Fatalf("append did not bump updated: %+v", list)
	}
	// So does editing a message.
	setUpdated(t, s, a.ID, 1000)
	msgs, _ := s.ListMessages(a.ID)
	if err := s.UpdateMessageParts(msgs[0].ID, `[{"text":"hello"}]`); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListConversations()
	if list[0].ID != a.ID {
		t.Fatalf("update did not bump updated: %+v", list)
	}
	if err := s.RenameConversation(b.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConversation(b.ID)
	if got.Title != "renamed" {
		t.Fatalf("title %q", got.Title)
	}
	if err := s.DeleteConversation(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetConversation(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.DeleteConversation(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := s.RenameConversation("nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename missing: %v", err)
	}
	if err := s.UpdateMessageParts("nope", `[]`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if err := s.DeleteConversation("nope"); err == nil || !strings.Contains(err.Error(), "delete conversation") {
		t.Fatalf("no op context: %v", err)
	}
}

func TestForeignKeyFailuresAreNotFound(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.CreateRun("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := s.InsertToolCall("missing", "", "get_doc", "acme", `{}`, "running"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("InsertToolCall: %v", err)
	}
	if err := s.AddUsage(Usage{RunID: "missing", Turn: 1}); err == nil {
		t.Fatal("AddUsage for a missing run should fail")
	}
}

func TestMessagesSeq(t *testing.T) {
	s, _ := openTemp(t)
	a := mustConv(t, s, "a")
	b := mustConv(t, s, "b")
	for i := 1; i <= 3; i++ {
		m, err := s.AppendMessage(a.ID, "user", `[{"text":"x"}]`)
		if err != nil || m.Seq != i {
			t.Fatalf("seq %d: %+v %v", i, m, err)
		}
	}
	m, err := s.AppendMessage(b.ID, "assistant", `[]`)
	if err != nil || m.Seq != 1 {
		t.Fatalf("b seq: %+v %v", m, err)
	}
	if _, err := s.AppendMessage("missing", "user", `[]`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing conv: %v", err)
	}
	msgs, err := s.ListMessages(a.ID)
	if err != nil || len(msgs) != 3 || msgs[0].Seq != 1 || msgs[2].Seq != 3 || msgs[0].Role != "user" {
		t.Fatalf("list = %+v, %v", msgs, err)
	}
}

func TestConcurrentAppend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stores int
	}{{"one store", 1}, {"two stores on one file", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "a.db")
			var stores []*Store
			for i := 0; i < tc.stores; i++ {
				s, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				stores = append(stores, s)
			}
			c := mustConv(t, stores[0], "c")
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := stores[i%len(stores)].AppendMessage(c.ID, "user", `[{"text":"x"}]`); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			msgs, err := stores[0].ListMessages(c.ID)
			if err != nil || len(msgs) != 20 {
				t.Fatalf("got %d, %v", len(msgs), err)
			}
			for i, m := range msgs {
				if m.Seq != i+1 {
					t.Fatalf("seq gap at %d: %d", i, m.Seq)
				}
			}
		})
	}
}

func TestRunToolCallUsageRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	m, _ := s.AppendMessage(c.ID, "assistant", `[{"tool_use":"get_doc"}]`)
	r, err := s.CreateRun(c.ID)
	if err != nil || r.Status != "running" || !r.Ended.IsZero() {
		t.Fatalf("run %+v %v", r, err)
	}
	tc, err := s.InsertToolCall(r.ID, m.ID, "get_doc", "acme", `{"doctype":"User"}`, "running")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishToolCall(tc.ID, "result text", "ok", "none"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 100, Output: 20, Cached: 5}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 101, Output: 21, Cached: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(r.ID, "done", "", 1); err != nil {
		t.Fatal(err)
	}
	gr, err := s.GetRun(r.ID)
	if err != nil || gr.Status != "done" || gr.Steps != 1 || gr.Ended.IsZero() {
		t.Fatalf("run %+v %v", gr, err)
	}
	tcs, err := s.ListToolCalls(r.ID)
	if err != nil || len(tcs) != 1 || tcs[0].ResultText != "result text" || tcs[0].Status != "ok" || tcs[0].Approval != "none" || tcs[0].MsgID != m.ID || tcs[0].Ended.IsZero() {
		t.Fatalf("tool calls %+v %v", tcs, err)
	}
	us, err := s.ListUsage(r.ID)
	if err != nil || len(us) != 1 || us[0].Input != 101 || us[0].Cached != 6 {
		t.Fatalf("usage %+v %v", us, err)
	}
	if err := s.FinishRun("nope", "done", "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish missing: %v", err)
	}
	// A tool call without a message is allowed.
	if _, err := s.InsertToolCall(r.ID, "", "list_docs", "acme", `{}`, "running"); err != nil {
		t.Fatal(err)
	}
}

func TestProviders(t *testing.T) {
	s, _ := openTemp(t)
	p := Provider{ID: "p1", Kind: "anthropic", Label: "Claude", BaseURL: "", DefaultModel: "claude-sonnet-5-5"}
	if err := s.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	p.Label = "Claude (work)"
	if err := s.UpsertProvider(p); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProvider(Provider{ID: "p2", Kind: "ollama", Label: "Local", BaseURL: "http://localhost:11434/v1"}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ListProviders()
	if err != nil || len(ps) != 2 || ps[0] != p || ps[1].BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("providers %+v %v", ps, err)
	}
	if err := s.DeleteProvider("p1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProvider("p1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteConversationCascades(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	other := mustConv(t, s, "other")
	if _, err := s.AppendMessage(other.ID, "user", `[{"text":"survivor"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(c.ID, "user", `[{"text":"doomed words"}]`); err != nil {
		t.Fatal(err)
	}
	r, _ := s.CreateRun(c.ID)
	if _, err := s.InsertToolCall(r.ID, "", "get_doc", "acme", `{}`, "ok"); err != nil {
		t.Fatal(err)
	}
	_ = s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 1, Output: 1})
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"messages": 1, "runs": 0, "tool_calls": 0, "usage": 0, "messages_fts": 1, "conversations": 1} {
		if got := count(t, s, table); got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	if hits, _ := s.Search("doomed", 0); len(hits) != 0 {
		t.Fatalf("search still finds deleted: %+v", hits)
	}
	if hits, _ := s.Search("survivor", 0); len(hits) != 1 {
		t.Fatalf("survivor lost: %+v", hits)
	}
}

// Valid JSON that is not an array of objects must insert and update fine; it
// is just not indexed.
func TestPartsShapes(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	for _, parts := range []string{
		`["hello"]`, `"hi"`, `{"text":"obj"}`, `42`, `null`, `[1,{"text":"mixed"},null]`, `not json`, `[]`,
	} {
		m, err := s.AppendMessage(c.ID, "user", parts)
		if err != nil {
			t.Fatalf("append %s: %v", parts, err)
		}
		if err := s.UpdateMessageParts(m.ID, `{"text":"updated obj"}`); err != nil {
			t.Fatalf("update after %s: %v", parts, err)
		}
		if err := s.UpdateMessageParts(m.ID, parts); err != nil {
			t.Fatalf("update to %s: %v", parts, err)
		}
	}
	for _, q := range []string{"hello", "obj", "updated"} {
		if hits, err := s.Search(q, 0); err != nil || len(hits) != 0 {
			t.Errorf("%q indexed: %+v %v", q, hits, err)
		}
	}
	// Only the object element of a mixed array is indexed.
	if hits, err := s.Search("mixed", 0); err != nil || len(hits) != 1 {
		t.Errorf("mixed: %+v %v", hits, err)
	}
}

func TestSearchFollowsChanges(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	m1, _ := s.AppendMessage(c.ID, "user", `[{"type":"text","text":"how many invoices are overdue"}]`)
	m2, _ := s.AppendMessage(c.ID, "assistant", `[{"text":"twelve"},{"text":"unpaid invoices"},{"tool_use":"ignored_token"}]`)

	hits, err := s.Search("invoices", 0)
	if err != nil || len(hits) != 2 {
		t.Fatalf("invoices: %+v %v", hits, err)
	}
	if hits, _ := s.Search("ignored_token", 0); len(hits) != 0 {
		t.Fatalf("non-text part indexed: %+v", hits)
	}
	if hits, _ := s.Search("overdue invoices", 0); len(hits) != 1 || hits[0].ID != m1.ID {
		t.Fatalf("AND of words: %+v", hits)
	}
	if err := s.UpdateMessageParts(m1.ID, `[{"text":"customers with credit notes"}]`); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("overdue", 0); len(hits) != 0 {
		t.Fatalf("old text still indexed: %+v", hits)
	}
	if hits, _ := s.Search("credit", 0); len(hits) != 1 || hits[0].ID != m1.ID {
		t.Fatalf("new text: %+v", hits)
	}
	if err := s.DeleteMessage(m2.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("twelve", 0); len(hits) != 0 {
		t.Fatalf("deleted message found: %+v", hits)
	}
}

func TestSearchQueries(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	for _, text := range []string{
		"Invoice SINV-0001 is overdue by 12 days",
		"customer foo:bar paid via a.b gateway",
		"it's the customer's credit note",
		"the tab stops at 100% and *stars* appear",
		`he said "hello world" twice`,
		"NEAR the warehouse WH-Main - Acme",
	} {
		if _, err := s.AppendMessage(c.ID, "assistant", fmt.Sprintf(`[{"text":%q}]`, text)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"SINV-0001", 1},
		{"sinv-0001", 1},
		{"SINV", 1},
		{"0001", 1},
		{"foo:bar", 1},
		{"a.b", 1},
		{"it's", 1},
		{"customer's", 1},
		{"100%", 1},
		{"*", 0},
		{"stars", 1},
		{`"hello world"`, 1},
		{`hello "`, 1},
		{"NEAR(", 1},
		{"WH-Main", 1},
		{"overdue 12", 1},
		{"overdue warehouse", 0},
		{"customer", 2},
		{"AND", 1}, // an operator word is just a word
		{"OR NOT", 0},
		{"", 0},
		{"  \t\n", 0},
		{"nomatch", 0},
	} {
		hits, err := s.Search(tc.query, 0)
		if err != nil {
			t.Errorf("Search(%q): %v", tc.query, err)
			continue
		}
		if len(hits) != tc.want {
			t.Errorf("Search(%q) = %d hits, want %d", tc.query, len(hits), tc.want)
		}
	}
}

func TestSearchRankAndLimit(t *testing.T) {
	s, _ := openTemp(t)
	old := mustConv(t, s, "old")
	recent := mustConv(t, s, "recent")
	setUpdated(t, s, old.ID, 1000)
	setUpdated(t, s, recent.ID, 2000)
	add := func(c Conversation, text string) Message {
		m, err := s.AppendMessage(c.ID, "user", fmt.Sprintf(`[{"text":%q}]`, text))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	weak := add(old, "invoice mentioned once among many other unrelated words here today")
	strong := add(old, "invoice invoice invoice")
	tie := add(recent, "invoice invoice invoice")
	// Keep the explicit timestamps set above (append bumps updated).
	setUpdated(t, s, old.ID, 1000)
	setUpdated(t, s, recent.ID, 2000)

	hits, err := s.Search("invoice", 0)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits %+v %v", hits, err)
	}
	if hits[2].ID != weak.ID {
		t.Errorf("weakest match not last: %v", []string{hits[0].ID, hits[1].ID, hits[2].ID})
	}
	// Equal scores: the more recently updated conversation first.
	if hits[0].ID != tie.ID || hits[1].ID != strong.ID {
		t.Errorf("tie order wrong: got %s %s, want %s %s", hits[0].ID, hits[1].ID, tie.ID, strong.ID)
	}
	if hits, _ := s.Search("invoice", 2); len(hits) != 2 {
		t.Errorf("limit 2 gave %d", len(hits))
	}
	for i := 0; i < 60; i++ {
		add(recent, "bulk entry")
	}
	if hits, _ := s.Search("bulk", 0); len(hits) != 50 {
		t.Errorf("default limit gave %d", len(hits))
	}
}

// Providers have no place for a secret: the table must stay free of any
// key-like column, so a future change cannot quietly start storing one.
func TestProvidersTableHasNoSecretColumn(t *testing.T) {
	s, _ := openTemp(t)
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('providers')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
		for _, bad := range []string{"key", "secret", "token", "password"} {
			if strings.Contains(strings.ToLower(name), bad) {
				t.Fatalf("providers has column %q", name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cols) != 5 {
		t.Fatalf("providers columns = %v", cols)
	}
}
