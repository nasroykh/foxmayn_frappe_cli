package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	_, path := openTemp(t)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
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
	// A message bumps updated, so a moves to the front.
	time.Sleep(5 * time.Millisecond)
	if _, err := s.AppendMessage(a.ID, "user", `[{"text":"hi"}]`); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListConversations()
	if err != nil || len(list) != 2 || list[0].ID != a.ID {
		t.Fatalf("list = %+v, %v", list, err)
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
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AppendMessage(c.ID, "user", `[{"text":"x"}]`); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	msgs, err := s.ListMessages(c.ID)
	if err != nil || len(msgs) != 20 {
		t.Fatalf("got %d, %v", len(msgs), err)
	}
	for i, m := range msgs {
		if m.Seq != i+1 {
			t.Fatalf("seq gap at %d: %d", i, m.Seq)
		}
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
	tc, _ := s.InsertToolCall(r.ID, "", "get_doc", "acme", `{}`, "ok")
	_ = tc
	_ = s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 1, Output: 1})
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"messages": 1, "runs": 0, "tool_calls": 0, "usage": 0, "messages_fts": 1, "conversations": 1} {
		if got := count(t, s, table); got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	if hits, _ := s.Search("doomed"); len(hits) != 0 {
		t.Fatalf("search still finds deleted: %+v", hits)
	}
	if hits, _ := s.Search("survivor"); len(hits) != 1 {
		t.Fatalf("survivor lost: %+v", hits)
	}
}

func TestSearchFollowsChanges(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	m1, _ := s.AppendMessage(c.ID, "user", `[{"type":"text","text":"how many invoices are overdue"}]`)
	m2, _ := s.AppendMessage(c.ID, "assistant", `[{"text":"twelve"},{"text":"unpaid invoices"},{"tool_use":"ignored_token"}]`)
	// A message whose parts are not valid JSON must not break the insert.
	if _, err := s.AppendMessage(c.ID, "user", `not json`); err != nil {
		t.Fatalf("invalid parts: %v", err)
	}

	hits, err := s.Search("invoices")
	if err != nil || len(hits) != 2 || hits[0].ID != m1.ID || hits[1].ID != m2.ID {
		t.Fatalf("invoices: %+v %v", hits, err)
	}
	if hits, _ := s.Search("ignored_token"); len(hits) != 0 {
		t.Fatalf("non-text part indexed: %+v", hits)
	}
	if hits, _ := s.Search("overdue AND invoices"); len(hits) != 1 || hits[0].ID != m1.ID {
		t.Fatalf("AND: %+v", hits)
	}

	if err := s.UpdateMessageParts(m1.ID, `[{"text":"customers with credit notes"}]`); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("overdue"); len(hits) != 0 {
		t.Fatalf("old text still indexed: %+v", hits)
	}
	if hits, _ := s.Search("credit"); len(hits) != 1 || hits[0].ID != m1.ID {
		t.Fatalf("new text: %+v", hits)
	}

	if err := s.DeleteMessage(m2.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("twelve"); len(hits) != 0 {
		t.Fatalf("deleted message found: %+v", hits)
	}
	if hits, err := s.Search("   "); err != nil || hits != nil {
		t.Fatalf("blank query: %+v %v", hits, err)
	}
	if _, err := s.Search(`"unterminated`); err == nil {
		t.Fatal("malformed FTS query should error")
	}
}

// A full recorded run. The provider key is only ever held by the keychain; the
// store has no column for it, so the bytes of the db and its WAL must not
// contain it even when every other provider field is saved.
func TestNoSecretInDatabaseBytes(t *testing.T) {
	const key = "sk-ant-test-SECRET123"
	s, path := openTemp(t)
	if err := s.UpsertProvider(Provider{ID: "anthropic", Kind: "anthropic", Label: "Claude", BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-5-5"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProvider(Provider{ID: "local", Kind: "ollama", Label: "Ollama", BaseURL: "http://localhost:11434/v1"}); err != nil {
		t.Fatal(err)
	}
	c := mustConv(t, s, "Overdue invoices")
	if _, err := s.AppendMessage(c.ID, "user", `[{"text":"which invoices are overdue?"}]`); err != nil {
		t.Fatal(err)
	}
	r, _ := s.CreateRun(c.ID)
	am, _ := s.AppendMessage(c.ID, "assistant", `[{"tool_use":{"id":"t1","name":"list_docs"}}]`)
	tc, err := s.InsertToolCall(r.ID, am.ID, "list_docs", "acme", `{"doctype":"Sales Invoice"}`, "running")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.FinishToolCall(tc.ID, `[{"name":"SINV-0001"}]`, "ok", "none")
	_ = s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 900, Output: 40, Cached: 800})
	_, _ = s.AppendMessage(c.ID, "assistant", `[{"text":"One invoice is overdue: SINV-0001."}]`)
	_ = s.FinishRun(r.ID, "done", "", 1)

	// Checkpoint so the main file is complete, but also scan the WAL while it
	// still exists (before checkpoint) and again after.
	scan := func(label string) {
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			b, err := os.ReadFile(p)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(b, []byte(key)) || bytes.Contains(b, []byte("SECRET123")) {
				t.Fatalf("%s: secret found in %s", label, filepath.Base(p))
			}
		}
	}
	scan("before checkpoint")
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	scan("after checkpoint")

	// And structurally: no providers column can hold a secret.
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('providers')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"key", "secret", "token", "password"} {
			if strings.Contains(strings.ToLower(name), bad) {
				t.Fatalf("providers has column %q", name)
			}
		}
	}
}
