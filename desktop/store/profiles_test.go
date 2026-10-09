package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// openV1 creates a database at schema version 1, as 0.2.0 wrote it.
func openV1(t *testing.T, path string) *sql.DB {
	t.Helper()
	uri, err := dsn(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrateV1ToV2KeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	db := openV1(t, path)
	for _, q := range []string{
		`INSERT INTO conversations(id,title,site,mode,provider_id,model,created,updated) VALUES('c1','old','acme','ask','p1','m',1,2)`,
		`INSERT INTO messages(id,conv_id,seq,role,parts_json,created) VALUES('m1','c1',1,'user','[{"text":"hello invoices"}]',1)`,
		`INSERT INTO runs(id,conv_id,status,started) VALUES('r1','c1','done',1)`,
		`INSERT INTO tool_calls(id,run_id,msg_id,tool,site,args_json,status,started) VALUES('t1','r1','m1','get_doc','acme','{}','ok',1)`,
		`INSERT INTO usage(run_id,turn,input,output,cached) VALUES('r1',1,10,5,3)`,
		`INSERT INTO providers(id,kind,label) VALUES('p1','anthropic','Anthropic')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetConversation("c1")
	if err != nil || c.Title != "old" || c.Mode != "ask" || c.ProfileID != "" || c.SiteURL != "" || c.SiteContext != "" {
		t.Fatalf("conversation = %+v, %v", c, err)
	}
	if m, _ := s.ListMessages("c1"); len(m) != 1 {
		t.Errorf("messages = %+v", m)
	}
	if hits, _ := s.Search("invoices", 0); len(hits) != 1 {
		t.Errorf("search after migration = %+v", hits)
	}
	if tc, _ := s.ListToolCalls("r1"); len(tc) != 1 {
		t.Errorf("tool calls = %+v", tc)
	}
	if u, _ := s.ListUsage("r1"); len(u) != 1 || u[0].Cached != 3 {
		t.Errorf("usage = %+v", u)
	}
	if p, _ := s.ListProviders(); len(p) != 1 {
		t.Errorf("providers = %+v", p)
	}
	// usage is keyed by (run, turn, kind) now: another kind of row for the
	// same turn does not replace the turn's row, and AddUsage still upserts.
	if _, err := s.db.Exec(`INSERT INTO usage(run_id,turn,kind,input,output) VALUES('r1',1,'title',1,1)`); err != nil {
		t.Fatalf("second kind for one turn: %v", err)
	}
	if err := s.AddUsage(Usage{RunID: "r1", Turn: 1, Input: 11, Output: 6, Cached: 3}); err != nil {
		t.Fatal(err)
	}
	var turnIn, rowsN int
	_ = s.db.QueryRow(`SELECT input FROM usage WHERE run_id='r1' AND turn=1 AND kind='turn'`).Scan(&turnIn)
	_ = s.db.QueryRow(`SELECT count(*) FROM usage WHERE run_id='r1'`).Scan(&rowsN)
	if turnIn != 11 || rowsN != 2 {
		t.Errorf("usage after upsert: input %d, %d rows", turnIn, rowsN)
	}
	if _, err := s.db.Exec(`DELETE FROM usage WHERE kind='title'`); err != nil {
		t.Fatal(err)
	}
	// The new columns have their defaults on the old rows.
	var pinned, archived, ephemeral, cacheWrite int
	var titleSource, costSource, priceDate, kind string
	var cost sql.NullFloat64
	if err := s.db.QueryRow(`SELECT pinned,archived,ephemeral,title_source FROM conversations WHERE id='c1'`).Scan(&pinned, &archived, &ephemeral, &titleSource); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT cache_write,cost_usd,cost_source,price_date,kind FROM usage WHERE run_id='r1'`).Scan(&cacheWrite, &cost, &costSource, &priceDate, &kind); err != nil {
		t.Fatal(err)
	}
	if pinned+archived+ephemeral+cacheWrite != 0 || titleSource != "" || cost.Valid || costSource != "" || priceDate != "" || kind != "turn" {
		t.Errorf("defaults: %d %d %d %d %q %v %q %q %q", pinned, archived, ephemeral, cacheWrite, titleSource, cost, costSource, priceDate, kind)
	}
	for _, table := range []string{"profiles", "site_settings", "attachments", "settings"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: %d, %v", table, n, err)
		}
	}
	var idx int
	_ = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='conversations_site_updated'`).Scan(&idx)
	if idx != 1 {
		t.Error("index conversations_site_updated missing")
	}
	_ = s.Close()

	// Opening again is a no-op: same version, rows intact.
	s, err = Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 2 || len(migrations) != 2 {
		t.Fatalf("user_version = %d (%d migrations), %v", v, len(migrations), err)
	}
	if _, err := s.GetConversation("c1"); err != nil {
		t.Fatal(err)
	}
}

// TestMigrateFailureRollsBack makes the last statement of migration 2 fail:
// nothing of it stays, the file is still at version 1 with its rows, and a
// later Open with the real migration succeeds.
func TestMigrateFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	db := openV1(t, path)
	for _, q := range []string{
		`INSERT INTO conversations(id,title,site,mode,provider_id,model,created,updated) VALUES('c1','old','acme','ask','p1','m',1,2)`,
		`INSERT INTO runs(id,conv_id,status,started) VALUES('r1','c1','done',1)`,
		`INSERT INTO usage(run_id,turn,input,output,cached) VALUES('r1',1,10,5,3)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	real := migrations[1]
	migrations[1] = real + "\nCREATE TABLE settings (key TEXT);" // already exists
	_, err := Open(path)
	migrations[1] = real
	if err == nil || !strings.Contains(err.Error(), "migrate store to v2") {
		t.Fatalf("Open with a failing migration = %v", err)
	}

	db = openRaw(t, path)
	var v, convs, usage, tables int
	_ = db.QueryRow(`PRAGMA user_version`).Scan(&v)
	_ = db.QueryRow(`SELECT count(*) FROM conversations WHERE id='c1' AND title='old'`).Scan(&convs)
	_ = db.QueryRow(`SELECT count(*) FROM usage WHERE run_id='r1' AND input=10`).Scan(&usage)
	_ = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('profiles','site_settings','usage_v2','attachments')`).Scan(&tables)
	var cols int
	_ = db.QueryRow(`SELECT count(*) FROM pragma_table_info('conversations') WHERE name='profile_id'`).Scan(&cols)
	_ = db.Close()
	if v != 1 || convs != 1 || usage != 1 || tables != 0 || cols != 0 {
		t.Fatalf("after a failed migration: version %d, conversation %d, usage %d, new tables %d, new columns %d", v, convs, usage, tables, cols)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open after the failure: %v", err)
	}
	defer s.Close()
	if u, _ := s.ListUsage("r1"); len(u) != 1 || u[0].Input != 10 {
		t.Errorf("usage = %+v", u)
	}
}

// openRaw opens a store file without migrating it.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	uri, err := dsn(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrateRefusesV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "schema 3, this app knows 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestAttachmentsCascade(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "x")
	m, err := s.AppendMessage(c.ID, "user", `[{"text":"hi"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO attachments(id,conv_id,msg_id,name,mime,size,sha256,created) VALUES('a1',?,?,'f.csv','text/csv',3,'x',1)`, c.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessage(m.ID); err != nil {
		t.Fatal(err)
	}
	var msg sql.NullString
	if err := s.db.QueryRow(`SELECT msg_id FROM attachments WHERE id='a1'`).Scan(&msg); err != nil || msg.Valid {
		t.Fatalf("msg_id after message delete = %v, %v", msg, err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM attachments`).Scan(&n)
	if n != 0 {
		t.Errorf("%d attachments left", n)
	}
}

func TestProfilesCRUD(t *testing.T) {
	s, _ := openTemp(t)
	p, err := s.SaveProfile(Profile{Name: "Mine", Mode: "read", ToolsetsJSON: `["core"]`, PolicyJSON: `{}`, DenyToolsJSON: `[]`, StepLimit: 7, KeepHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ID) != 32 || p.StepLimit != 7 || p.Created.IsZero() {
		t.Fatalf("saved = %+v", p)
	}
	p.Name, p.CallMethod = "Renamed", true
	if _, err := s.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProfile(p.ID)
	if err != nil || got.Name != "Renamed" || !got.CallMethod {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if list, _ := s.ListProfiles(); len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
	c, err := s.InsertConversation(Conversation{Title: "t", Site: "acme", Mode: "ask", ProviderID: "p", Model: "m", ProfileID: p.ID, SiteURL: "https://acme.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationSiteContext(c.ID, "ctx", "k1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(p.ID, "explore"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetConversation(c.ID)
	if c.ProfileID != "explore" || c.SiteContext != "" || c.SiteURL != "https://acme.test" {
		t.Errorf("conversation after delete = %+v", c)
	}
	if _, err := s.GetProfile(p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get deleted = %v", err)
	}
	if err := s.DeleteProfile(p.ID, "explore"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice = %v", err)
	}
	if err := s.SetConversationProfile("nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("set on unknown = %v", err)
	}
	if err := s.SetConversationProvider(c.ID, "p2", "m2"); err != nil {
		t.Fatal(err)
	}
	if c, _ = s.GetConversation(c.ID); c.ProviderID != "p2" || c.Model != "m2" {
		t.Errorf("provider = %+v", c)
	}
}

func TestSiteSettings(t *testing.T) {
	s, _ := openTemp(t)
	for i, ss := range []SiteSettings{
		{Site: "a", URL: "https://a.test", Instructions: "one"},
		{Site: "b", URL: "https://b.test", LocalOnly: true},
		{Site: "a", URL: "https://a.test", Instructions: "two", LocalOnly: true},
	} {
		if err := s.SaveSiteSettings(ss); err != nil {
			t.Fatal(fmt.Sprint(i, err))
		}
	}
	list, err := s.ListSiteSettings()
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	for _, ss := range list {
		if ss.Site == "a" && (ss.Instructions != "two" || !ss.LocalOnly) {
			t.Errorf("a = %+v", ss)
		}
	}
}
