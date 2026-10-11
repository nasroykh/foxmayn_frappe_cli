package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func activityLog(t *testing.T, cur, old string) *ActivityService {
	t.Helper()
	dir := t.TempDir()
	s := NewActivityService(filepath.Join(dir, "config.yaml"))
	if cur != "" {
		os.WriteFile(s.path, []byte(cur), 0o600)
	}
	if old != "" {
		os.WriteFile(s.path+".1", []byte(old), 0o600)
	}
	return s
}

func line(ts, site, client, tool, status, run string) string {
	return fmt.Sprintf(`{"time":%q,"site":%q,"client":%q,"tool":%q,"run_id":%q,"status":%q,"duration_ms":12,"doctypes":["ToDo"],"names":["TD-1"],"args":{"doctype":"ToDo","data":{"keys":["status"],"bytes":20}}}`+"\n",
		ts, site, client, tool, run, status)
}

func TestActivityQuery(t *testing.T) {
	old := line("2026-10-10T09:00:00Z", "prod", "claude-ai", "list_docs", "ok", "")
	cur := line("2026-10-11T09:00:00Z", "prod", "foxmayn-desktop", "get_doc", "ok", "r1") +
		"not json\n" +
		line("2026-10-11T10:00:00Z", "dev", "foxmayn-desktop", "update_doc", "declined", "r1") +
		`{"time":"2026-10-11T11:00:00Z","tool":"get_doc","status":"ok","error":"bad ‮ text"}` + "\n" +
		strings.Repeat("x", activityMaxLine+10) + "\n"
	s := activityLog(t, cur, old)

	p, err := s.Query(ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Exists || p.Total != 4 || p.Skipped != 2 {
		t.Fatalf("total %d skipped %d exists %v", p.Total, p.Skipped, p.Exists)
	}
	// Newest first, the rotated half last.
	if p.Entries[0].Error != "bad  text" || p.Entries[3].Tool != "list_docs" {
		t.Fatalf("order or sanitising: %+v", p.Entries)
	}
	if strings.Join(p.Clients, ",") != "claude-ai,foxmayn-desktop" || strings.Join(p.Sites, ",") != "dev,prod" {
		t.Fatalf("facets %v %v", p.Clients, p.Sites)
	}
	if !strings.Contains(p.Entries[1].Args, `"keys"`) {
		t.Fatalf("args %q", p.Entries[1].Args)
	}

	p, _ = s.Query(ActivityQuery{RunID: "r1"})
	if p.Total != 2 || p.Entries[0].Tool != "update_doc" {
		t.Fatalf("run filter: %+v", p)
	}
	p, _ = s.Query(ActivityQuery{Site: "prod", Since: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)})
	if p.Total != 1 || p.Entries[0].Tool != "get_doc" {
		t.Fatalf("site and since: %+v", p)
	}
	p, _ = s.Query(ActivityQuery{Offset: 1, Limit: 2})
	if p.Total != 4 || len(p.Entries) != 2 || p.Entries[0].Tool != "update_doc" {
		t.Fatalf("paging: %+v", p.Entries)
	}
	for _, q := range []ActivityQuery{{Limit: 201}, {Limit: -1}, {Offset: -1}} {
		if _, err := s.Query(q); errorCode(t, err) != CodeInvalid {
			t.Errorf("%+v: %v", q, err)
		}
	}
}

func TestActivityNoLog(t *testing.T) {
	p, err := activityLog(t, "", "").Query(ActivityQuery{})
	if err != nil || p.Exists || p.Total != 0 || p.Entries == nil {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestActivityRefusesALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.txt")
	os.WriteFile(target, []byte(line("2026-10-11T09:00:00Z", "a", "b", "c", "ok", "")), 0o600)
	s := NewActivityService(filepath.Join(dir, "config.yaml"))
	if err := os.Symlink(target, s.path); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := s.Query(ActivityQuery{}); errorCode(t, err) != CodeFailed {
		t.Fatalf("link: %v", err)
	}
}

// A file larger than activityMaxFile is read from its end; the first,
// partial line is dropped, not counted as damaged.
func TestActivityBigFile(t *testing.T) {
	one := line("2026-10-11T09:00:00Z", "prod", "c", "get_doc", "ok", "")
	n := activityMaxFile/len(one) + 50
	s := activityLog(t, strings.Repeat(one, n), "")
	p, err := s.Query(ActivityQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Skipped != 0 || p.Total < n-100 || p.Total > n {
		t.Fatalf("total %d of %d, skipped %d", p.Total, n, p.Skipped)
	}
}

func TestActivityDeepArgs(t *testing.T) {
	const depth = 9000
	deep := strings.Repeat("[", depth) + strings.Repeat("]", depth)
	one := fmt.Sprintf(`{"time":"2026-10-11T09:00:00Z","site":"s","tool":"t","status":"ok","args":%s}`+"\n", deep)
	s := activityLog(t, strings.Repeat(one, 200), "")
	start := time.Now()
	p, err := s.Query(ActivityQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	if len(p.Entries) != 200 || len(p.Entries[0].Args) > activityMaxArgs+8 {
		t.Fatalf("entries %d, args %d bytes", len(p.Entries), len(p.Entries[0].Args))
	}
	ok := entryOf(auditLine{Args: []byte(`{"a":["]",{"b":1}]}`)})
	if !strings.Contains(ok.Args, `"b": 1`) {
		t.Fatalf("normal args lost: %q", ok.Args)
	}
}

func TestActivityFacets(t *testing.T) {
	var b strings.Builder
	for i := 0; i < activityMaxFacet*3; i++ {
		b.WriteString(line("2026-10-11T09:00:00Z", fmt.Sprintf("site-%d", i), "c", "t", "ok", ""))
		b.WriteString(line("2026-10-11T09:00:00Z", "dup", "c", "t", "ok", ""))
	}
	long := strings.Repeat("x", activityMaxField+500)
	b.WriteString(line("2026-10-11T09:00:00Z", "a\u202eb", long, "t", "ok", ""))
	s := activityLog(t, b.String(), "")
	p, err := s.Query(ActivityQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sites) > activityMaxFacet {
		t.Fatalf("%d sites", len(p.Sites))
	}
	n := 0
	for _, v := range p.Sites {
		if v == "dup" {
			n++
		}
		if strings.ContainsRune(v, 0x202e) {
			t.Fatalf("unsanitized %q", v)
		}
	}
	if n != 1 {
		t.Fatalf("dup listed %d times", n)
	}
	if len(p.Clients) != 2 {
		t.Fatalf("clients %d", len(p.Clients))
	}
	for _, v := range p.Clients {
		if len([]rune(v)) > activityMaxField+1 {
			t.Fatalf("client not cut: %d runes", len([]rune(v)))
		}
	}
}
