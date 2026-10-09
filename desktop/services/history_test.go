package services

import (
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// dialogHost is a Host that answers the file dialogs with fixed paths.
type dialogHost struct {
	*fakeHost
	save, open string
	asked      []string
}

func (h *dialogHost) SaveFileDialog(title, filename, filterName, pattern string) (string, error) {
	h.asked = append(h.asked, "save:"+filename+":"+pattern)
	return h.save, nil
}

func (h *dialogHost) OpenFileDialog(title, filterName, pattern string) (string, error) {
	h.asked = append(h.asked, "open:"+pattern)
	return h.open, nil
}

// historyRig is an assistant rig whose host answers file dialogs.
func historyRig(t *testing.T, turns ...llmtest.Turn) (*assistantRig, *dialogHost) {
	t.Helper()
	g := newAssistantRig(t, turns...)
	dh := &dialogHost{fakeHost: g.h}
	g.a.host = dh
	return g, dh
}

// converse sends one message in a new conversation and waits for the answer.
func (g *assistantRig) converse(t *testing.T, text string) Conversation {
	t.Helper()
	c := g.conv(t, "read")
	if _, err := g.a.Send(c.ID, text, nil); err != nil {
		t.Fatal(err)
	}
	g.done(t, len(g.h.named(EventChatDone))+1)
	return c
}

var toolThenAnswer = []llmtest.Turn{
	{Events: []llm.Event{
		llm.TextDelta{Text: "Let me look. "},
		call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`),
		llm.Usage{In: 100, Out: 20, Cached: 7},
		llm.Stop{Reason: llm.StopToolUse},
	}},
	textTurn("TD-1 is alpha."),
}

// ageConv sets a conversation's updated time through a second connection.
func ageConv(t *testing.T, dbPath, id string, when time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE conversations SET updated=? WHERE id=?`, when.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
}

func convIDs(t *testing.T, a *AssistantService) map[string]Conversation {
	t.Helper()
	list, err := a.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Conversation{}
	for _, c := range list {
		out[c.ID] = c
	}
	return out
}

// normalize turns an export into a value that does not depend on the ids
// the store made: ids become their position, links follow, the
// conversation's own times go.
func normalize(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	conv := f["conversation"].(map[string]any)
	delete(conv, "id")
	delete(conv, "created")
	delete(conv, "updated")
	// An import never takes the site context: the first run collects its own.
	conv["site_context"], conv["site_context_key"] = "", ""
	// An import always makes the title the user's.
	conv["title_source"] = ""
	msgIdx, runIdx := map[string]string{}, map[string]string{}
	for i, m := range f["messages"].([]any) {
		mm := m.(map[string]any)
		msgIdx[mm["id"].(string)] = "m" + string(rune('0'+i%10)) + string(rune('0'+i/10))
		mm["id"] = msgIdx[mm["id"].(string)]
	}
	for i, r := range f["runs"].([]any) {
		rr := r.(map[string]any)
		runIdx[rr["id"].(string)] = "r" + string(rune('0'+i))
		rr["id"] = runIdx[rr["id"].(string)]
	}
	for i, c := range f["tool_calls"].([]any) {
		cc := c.(map[string]any)
		cc["id"] = "t" + string(rune('0'+i))
		cc["run_id"] = runIdx[cc["run_id"].(string)]
		if id := cc["msg_id"].(string); id != "" {
			cc["msg_id"] = msgIdx[id]
		}
	}
	for _, u := range f["usage"].([]any) {
		uu := u.(map[string]any)
		uu["run_id"] = runIdx[uu["run_id"].(string)]
	}
	return f
}

func TestExportImportExportRoundTrip(t *testing.T) {
	g, dh := historyRig(t, toolThenAnswer...)
	c := g.converse(t, "What is TD-1?")
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")

	dh.save = first
	got, err := g.a.ExportConversation(c.ID, "json")
	if err != nil || got != first {
		t.Fatalf("export: %q %v", got, err)
	}
	raw1, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	var shape struct {
		Format    string           `json:"format"`
		Version   int              `json:"version"`
		Messages  []map[string]any `json:"messages"`
		Runs      []map[string]any `json:"runs"`
		ToolCalls []map[string]any `json:"tool_calls"`
		Usage     []map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(raw1, &shape); err != nil {
		t.Fatal(err)
	}
	if shape.Format != "foxmayn-desktop-conversation" || shape.Version != 1 || len(shape.Messages) != 4 || len(shape.Runs) != 1 || len(shape.ToolCalls) != 1 || len(shape.Usage) == 0 {
		t.Fatalf("export shape: %s", raw1)
	}
	if !strings.Contains(string(raw1), `"title_source": ""`) {
		t.Errorf("the export should carry the title's origin: %s", raw1)
	}
	if len(dh.asked) == 0 || dh.asked[0] != "save:what-is-td-1.json:*.json" {
		t.Errorf("dialog: %v", dh.asked)
	}

	dh.open = first
	res, err := g.a.ImportConversation()
	if err != nil || res.Cancelled {
		t.Fatalf("import: %+v %v", res, err)
	}
	if res.Conversation.ID == c.ID || res.Conversation.Mode != ModeRead {
		t.Errorf("imported: %+v", res.Conversation)
	}
	detail, err := g.a.GetConversation(res.Conversation.ID)
	if err != nil || len(detail.Messages) == 0 || detail.Messages[len(detail.Messages)-1].Text != "TD-1 is alpha." {
		t.Fatalf("imported conversation: %+v %v", detail, err)
	}
	if len(convIDs(t, g.a)) != 2 {
		t.Error("the import should add a conversation")
	}

	dh.save = second
	if _, err := g.a.ExportConversation(res.Conversation.ID, "json"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(second)
	if !strings.Contains(string(raw2), `"title_source": "user"`) {
		t.Errorf("an imported title should be the user's: %s", raw2)
	}
	n1, n2 := normalize(t, raw1), normalize(t, raw2)
	b1, _ := json.Marshal(n1)
	b2, _ := json.Marshal(n2)
	if string(b1) != string(b2) {
		t.Errorf("export -> import -> export differs\nfirst:  %s\nsecond: %s", b1, b2)
	}
	// New ids everywhere.
	for _, id := range []string{c.ID, shape.Messages[0]["id"].(string), shape.Runs[0]["id"].(string), shape.ToolCalls[0]["id"].(string)} {
		if strings.Contains(string(raw2), id) {
			t.Errorf("id %s of the first conversation is in the second export", id)
		}
	}
}

func TestExportCancelledAndNoDialogs(t *testing.T) {
	g, dh := historyRig(t, textTurn("hi"))
	c := g.converse(t, "hello")
	dh.save = ""
	if p, err := g.a.ExportConversation(c.ID, "md"); err != nil || p != "" {
		t.Errorf("cancelled export: %q %v", p, err)
	}
	dh.open = ""
	if res, err := g.a.ImportConversation(); err != nil || !res.Cancelled {
		t.Errorf("cancelled import: %+v %v", res, err)
	}
	if _, err := g.a.ExportConversation(c.ID, "pdf"); errorCode(t, err) != CodeInvalid {
		t.Errorf("bad format: %v", err)
	}
	if _, err := g.a.ExportConversation("nope", "json"); errorCode(t, err) != CodeNotFound {
		t.Errorf("unknown conversation: %v", err)
	}
	g.a.host = g.h // no dialogs
	if _, err := g.a.ExportConversation(c.ID, "json"); errorCode(t, err) != CodeUnavailable {
		t.Errorf("no dialogs: %v", err)
	}
	if _, err := g.a.ImportConversation(); errorCode(t, err) != CodeUnavailable {
		t.Errorf("no dialogs: %v", err)
	}
}

func TestExportsCarryNoKey(t *testing.T) {
	g, dh := historyRig(t, toolThenAnswer...)
	g.provider(t) // saves goodKey for p1
	c := g.conv(t, "read")
	// A person may paste a key into the chat.
	if _, err := g.a.Send(c.ID, "my key is "+goodKey+" ok?", nil); err != nil {
		t.Fatal(err)
	}
	g.done(t, 1)
	dir := t.TempDir()
	for _, format := range []string{"json", "md"} {
		path := filepath.Join(dir, "out."+format)
		dh.save = path
		if _, err := g.a.ExportConversation(c.ID, format); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), goodKey) || strings.Contains(string(b), goodKey[:12]) {
			t.Errorf("%s export carries the key: %.300s", format, b)
		}
		if !strings.Contains(string(b), "[removed]") || !strings.Contains(string(b), "my key is") {
			t.Errorf("%s export should keep the text around the key: %.300s", format, b)
		}
	}
}

func TestImportRefusesBadFiles(t *testing.T) {
	g, dh := historyRig(t, textTurn("hi"))
	c := g.converse(t, "hello")
	good := filepath.Join(t.TempDir(), "good.json")
	dh.save = good
	if _, err := g.a.ExportConversation(c.ID, "json"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(good)
	var base map[string]any
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	clone := func(mut func(m map[string]any)) []byte {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mut(m)
		b, _ := json.Marshal(m)
		return b
	}
	before := len(convIDs(t, g.a))
	bad := map[string][]byte{
		"not json":        []byte("hello"),
		"empty":           nil,
		"trailing data":   append(append([]byte{}, raw...), []byte(`{"x":1}`)...),
		"wrong format":    clone(func(m map[string]any) { m["format"] = "something-else" }),
		"no format":       clone(func(m map[string]any) { delete(m, "format") }),
		"version 2":       clone(func(m map[string]any) { m["version"] = 2 }),
		"version 0":       clone(func(m map[string]any) { m["version"] = 0 }),
		"no site":         clone(func(m map[string]any) { m["conversation"].(map[string]any)["site"] = "" }),
		"bad role":        clone(func(m map[string]any) { m["messages"].([]any)[0].(map[string]any)["role"] = "system" }),
		"parts not array": clone(func(m map[string]any) { m["messages"].([]any)[0].(map[string]any)["parts"] = "x" }),
		"unknown part": clone(func(m map[string]any) {
			m["messages"].([]any)[0].(map[string]any)["parts"] = []any{map[string]any{"type": "script"}}
		}),
		"bad time": clone(func(m map[string]any) { m["messages"].([]any)[0].(map[string]any)["created"] = "yesterday" }),
		"duplicate id": clone(func(m map[string]any) {
			ms := m["messages"].([]any)
			ms[1].(map[string]any)["id"] = ms[0].(map[string]any)["id"]
		}),
		"run link": clone(func(m map[string]any) {
			m["tool_calls"] = []any{map[string]any{"id": "t", "run_id": "ghost", "msg_id": "", "tool": "x", "site": "prod", "args_json": "{}", "status": "ok"}}
		}),
		"bad tool args": clone(func(m map[string]any) {
			rid := m["runs"].([]any)[0].(map[string]any)["id"]
			m["tool_calls"] = []any{map[string]any{"id": "t", "run_id": rid, "msg_id": "", "tool": "x", "site": "prod", "args_json": "{not json", "status": "ok"}}
		}),
		"negative usage": clone(func(m map[string]any) {
			rid := m["runs"].([]any)[0].(map[string]any)["id"]
			m["usage"] = []any{map[string]any{"run_id": rid, "turn": 1, "kind": "turn", "input": -1}}
		}),
		"long id": clone(func(m map[string]any) {
			m["messages"].([]any)[0].(map[string]any)["id"] = strings.Repeat("x", 5000)
		}),
	}
	for name, b := range bad {
		path := filepath.Join(t.TempDir(), "f.json")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		dh.open = path
		if _, err := g.a.ImportConversation(); err == nil || errorCode(t, err) != CodeInvalid {
			t.Errorf("%s: want an invalid error, got %v", name, err)
		}
	}
	if got := len(convIDs(t, g.a)); got != before {
		t.Errorf("refused imports left %d conversations", got-before)
	}

	// Over the size cap: refused from the file size, before it is read.
	big := filepath.Join(t.TempDir(), "big.json")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImportBytes + 1); err != nil {
		t.Skipf("cannot make a large file here: %v", err)
	}
	f.Close()
	dh.open = big
	if _, err := g.a.ImportConversation(); err == nil || errorCode(t, err) != CodeInvalid || !strings.Contains(err.Error(), "too large") {
		t.Errorf("oversize: %v", err)
	}
	// A folder is not a file.
	dh.open = t.TempDir()
	if _, err := g.a.ImportConversation(); err == nil {
		t.Error("a folder was accepted")
	}
}

// TestImportDoesNotTrustTheFile: the write mode, ids, providers, profiles,
// images and unfinished runs of a file are not taken as they are.
func TestImportDoesNotTrustTheFile(t *testing.T) {
	g, _ := historyRig(t)
	g.provider(t)
	images, _ := llm.MarshalParts([]llm.Part{llm.Text{Text: "look"}, llm.Image{AttachmentID: "att-1", MediaType: "image/png"}})
	file := exportFile{
		Format: exportFormat, Version: exportVersion,
		Conversation: exportConv{ID: "evil-id", Title: "Imported", Site: "prod", Mode: "ask", ProviderID: "ghost", Model: "m", ProfileID: "no-such-profile",
			SiteContext: "ctx", SiteContextKey: "k"},
		Profile:  json.RawMessage(`{"name":"anything","mode":"ask"}`),
		Messages: []exportMessage{{ID: "m1", Role: "user", Parts: json.RawMessage(images), Created: "2026-01-02T03:04:05Z"}},
		Runs:     []exportRun{{ID: "r1", Status: "running", Steps: 2, Started: "2026-01-02T03:04:05Z"}, {ID: "r2", Status: "paused"}},
		ToolCalls: []exportToolCall{
			{ID: "t1", RunID: "r1", MsgID: "m1", Tool: "get_doc", Site: "prod", ArgsJSON: "{}", Status: "running"},
		},
	}
	raw, _ := json.Marshal(file)
	c, err := g.a.importBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "evil-id" || c.Mode != ModeRead || c.ProviderID != "" || c.Model != "" || c.ProfileID != "" {
		t.Errorf("conversation: %+v", c)
	}
	d, err := g.a.GetConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.PausedRunID != "" || d.ActiveRunID != "" {
		t.Errorf("an imported run can be continued: %+v", d)
	}
	if len(d.Messages) != 1 || !strings.Contains(d.Messages[0].Text, "look") {
		t.Fatalf("messages: %+v", d.Messages)
	}
	_, st, done, _ := g.a.enter()
	defer done()
	stored, _ := st.ListMessages(c.ID)
	if strings.Contains(stored[0].PartsJSON, "att-1") || !strings.Contains(stored[0].PartsJSON, "image not included") {
		t.Errorf("the image was kept: %s", stored[0].PartsJSON)
	}
	runs, _ := st.ListRuns(c.ID)
	for _, r := range runs {
		if r.Status != RunCancelled {
			t.Errorf("run status %q", r.Status)
		}
	}
	var calls []store.ToolCall
	for _, r := range runs {
		cs, _ := st.ListToolCalls(r.ID)
		calls = append(calls, cs...)
	}
	if len(calls) != 1 || calls[0].Status != ToolStopped {
		t.Errorf("tool calls: %+v", calls)
	}
	// A provider and a profile that exist here are kept.
	file.Conversation.ProviderID, file.Conversation.ProfileID = "p1", PresetExplore
	raw, _ = json.Marshal(file)
	c, err = g.a.importBytes(raw)
	if err != nil || c.ProviderID != "p1" || c.ProfileID != PresetExplore || c.Mode != ModeRead {
		t.Errorf("known provider and profile: %+v %v", c, err)
	}
}

func fixedExport() exportFile {
	at := func(m int) time.Time { return time.Date(2026, 10, 9, 10, m, 0, 0, time.UTC) }
	parts := func(ps ...llm.Part) string {
		s, err := llm.MarshalParts(ps)
		if err != nil {
			panic(err)
		}
		return s
	}
	d := store.ExportData{
		Conversation: store.Conversation{ID: "c1", Title: "Overdue invoices\n# not a heading", Site: "prod", Mode: "read", ProviderID: "p1", Model: "claude-sonnet-5-5",
			ProfileID: PresetExplore, Created: at(0), Updated: at(5)},
		Messages: []store.Message{
			{ID: "m1", Role: "user", PartsJSON: parts(llm.Text{Text: "Which invoices are overdue?"}, llm.Image{AttachmentID: "a1", MediaType: "image/png"}), Created: at(0)},
			{ID: "m2", Role: "assistant", PartsJSON: parts(llm.Thinking{Text: "private reasoning", Signature: "sig"}, llm.Text{Text: "I will look."},
				llm.ToolUse{ID: "u1", Name: "list_docs", Args: json.RawMessage(`{"doctype":"Sales Invoice","filters":{"status":"Overdue"}}`)},
				llm.ToolUse{ID: "u2", Name: "get_doc", Args: json.RawMessage(`{"doctype":"Sales Invoice","name":"<b>SINV-1</b>"}`)}), Created: at(1)},
			{ID: "m3", Role: "user", PartsJSON: parts(
				llm.ToolResult{ID: "u1", Text: "[{\"name\":\"SINV-1\"}]\n```\nnot a fence end\n```"},
				llm.ToolResult{ID: "u2", Text: strings.Repeat("x", 60), IsError: true}), Created: at(2)},
			{ID: "m4", Role: "assistant", PartsJSON: parts(llm.Text{Text: "SINV-1 is overdue."}), Created: at(3)},
		},
		Runs: []store.Run{{ID: "r1", Status: "done", Steps: 2, Started: at(1), Ended: at(3)}},
		ToolCalls: []store.ToolCall{
			{ID: "t1", RunID: "r1", MsgID: "m2", Tool: "list_docs", Site: "prod", ArgsJSON: "{}", Status: "ok", Started: at(1)},
			{ID: "t2", RunID: "r1", MsgID: "m2", Tool: "get_doc", Site: "prod", ArgsJSON: "{}", Status: "error", Approval: "declined", Started: at(1)},
		},
	}
	prof, _ := presetByID(PresetExplore)
	f, err := buildExport(d, &prof)
	if err != nil {
		panic(err)
	}
	return f
}

func TestMarkdownGolden(t *testing.T) {
	defer func(n int) { markdownResultLimit = n }(markdownResultLimit)
	markdownResultLimit = 40
	got := renderMarkdown(fixedExport())
	golden := filepath.Join("testdata", "history_markdown.golden.md")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("markdown differs from %s (run with -update to rewrite it)\n--- got ---\n%s", golden, got)
	}
	for _, leak := range []string{"private reasoning", "sig", "a1"} {
		if strings.Contains(got, "\n"+leak) && leak != "sig" {
			t.Errorf("markdown holds %q", leak)
		}
	}
	if strings.Contains(got, "private reasoning") || strings.Contains(got, "attachment") {
		t.Error("markdown leaks reasoning or an attachment id")
	}
}

func TestPinArchiveAndSearchService(t *testing.T) {
	g, _ := historyRig(t, textTurn("The overdue invoices are listed."), textTurn("Nothing about that."))
	c1 := g.converse(t, "Show overdue invoices")
	c2 := g.converse(t, "Something else")
	if err := g.a.Pin(c1.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := g.a.Archive(c2.ID, true); err != nil {
		t.Fatal(err)
	}
	list := convIDs(t, g.a)
	if !list[c1.ID].Pinned || list[c1.ID].Archived || !list[c2.ID].Archived {
		t.Errorf("list flags: %+v %+v", list[c1.ID], list[c2.ID])
	}
	hits, err := g.a.Search("overdue", SearchFilter{}, 0)
	if err != nil || len(hits) == 0 || hits[0].ConvID != c1.ID || !hits[0].Pinned || !strings.Contains(strings.ToLower(hits[0].Snippet), "overdue") {
		t.Fatalf("search: %+v %v", hits, err)
	}
	if hits, _ := g.a.Search("something", SearchFilter{}, 0); len(hits) != 0 {
		t.Errorf("archived found in the normal view: %+v", hits)
	}
	if hits, _ := g.a.Search("something", SearchFilter{Archived: true}, 0); len(hits) != 1 || hits[0].ConvID != c2.ID {
		t.Errorf("archive view: %+v", hits)
	}
	today := time.Now().Format("2006-01-02")
	if hits, _ := g.a.Search("overdue", SearchFilter{Site: "prod", From: today, To: today}, 0); len(hits) == 0 {
		t.Error("site and today's range should find it")
	}
	if hits, _ := g.a.Search("overdue", SearchFilter{Site: "other"}, 0); len(hits) != 0 {
		t.Error("another site should not")
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if hits, _ := g.a.Search("overdue", SearchFilter{To: yesterday}, 0); len(hits) != 0 {
		t.Error("a range ending yesterday should not")
	}
	if _, err := g.a.Search("x", SearchFilter{From: "10/09/2026"}, 0); errorCode(t, err) != CodeInvalid {
		t.Errorf("bad date: %v", err)
	}
	if _, err := g.a.Search(strings.Repeat("a", 600), SearchFilter{}, 0); errorCode(t, err) != CodeInvalid {
		t.Errorf("long query: %v", err)
	}
	if hits, err := g.a.Search(`"`, SearchFilter{}, 0); err != nil || len(hits) != 0 {
		t.Errorf("quote: %v %v", hits, err)
	}
	if err := g.a.Pin("nope", true); errorCode(t, err) != CodeNotFound {
		t.Errorf("pin unknown: %v", err)
	}
	if err := g.a.Archive(c2.ID, false); err != nil {
		t.Fatal(err)
	}
	if hits, _ := g.a.Search("something", SearchFilter{}, 0); len(hits) != 1 {
		t.Errorf("restored: %+v", hits)
	}
}

func TestRetentionSettingAndSweep(t *testing.T) {
	g, _ := historyRig(t, textTurn("a"), textTurn("b"), textTurn("c"), textTurn("d"))
	plain := g.converse(t, "plain")
	pinned := g.converse(t, "pinned")
	active := g.converse(t, "active")
	young := g.converse(t, "young")
	old := time.Now().AddDate(0, 0, -100)
	for _, c := range []Conversation{plain, pinned, active} {
		ageConv(t, g.dbPath, c.ID, old)
	}
	ageConv(t, g.dbPath, young.ID, time.Now().AddDate(0, 0, -40))
	if err := g.a.Pin(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	g.a.run.mu.Lock()
	g.a.run.active["fake-run"] = &activeRun{conv: store.Conversation{ID: active.ID}}
	g.a.run.mu.Unlock()
	defer func() {
		g.a.run.mu.Lock()
		delete(g.a.run.active, "fake-run")
		g.a.run.mu.Unlock()
	}()

	if n, err := g.a.GetRetention(); err != nil || n != 0 {
		t.Fatalf("default retention: %d %v", n, err)
	}
	if n, err := g.a.sweep(); err != nil || n != 0 || len(convIDs(t, g.a)) != 4 {
		t.Fatalf("forever deletes nothing: %d %v", n, err)
	}
	for _, bad := range []int{-1, 7, 31, 365} {
		if err := g.a.SetRetention(bad); errorCode(t, err) != CodeInvalid {
			t.Errorf("SetRetention(%d): %v", bad, err)
		}
	}
	if err := g.a.SetRetention(90); err != nil {
		t.Fatal(err)
	}
	if n, _ := g.a.GetRetention(); n != 90 {
		t.Errorf("retention %d", n)
	}
	if len(convIDs(t, g.a)) != 4 {
		t.Error("setting the period must not delete anything by itself")
	}
	n, err := g.a.sweep()
	if err != nil || n != 1 {
		t.Fatalf("90 days: %d %v", n, err)
	}
	left := convIDs(t, g.a)
	if _, ok := left[plain.ID]; ok || len(left) != 3 {
		t.Errorf("after 90 days: %v", left)
	}
	if err := g.a.SetRetention(30); err != nil {
		t.Fatal(err)
	}
	n, _ = g.a.sweep()
	left = convIDs(t, g.a)
	if n != 1 || len(left) != 2 {
		t.Fatalf("30 days: deleted %d, left %d", n, len(left))
	}
	if _, ok := left[young.ID]; ok {
		t.Error("the 40 day old conversation should go at 30 days")
	}
	if _, ok := left[pinned.ID]; !ok {
		t.Error("pinned must stay")
	}
	if _, ok := left[active.ID]; !ok {
		t.Error("a conversation with a run in progress must stay")
	}
}

func TestEphemeralConversationsAreNotKept(t *testing.T) {
	g, _ := historyRig(t, textTurn("secret answer"))
	c := g.conv(t, "read")
	ghost, err := g.a.SaveProfile(Profile{Name: "Ghost", Mode: "read", StepLimit: 5, KeepHistory: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.a.SetConversationProfile(c.ID, ghost.ID); err != nil {
		t.Fatal(err)
	}
	if !convIDs(t, g.a)[c.ID].Ephemeral {
		t.Fatal("a profile without history should make the conversation ephemeral")
	}
	if _, err := g.a.Send(c.ID, "tell me a secret", nil); err != nil {
		t.Fatal(err)
	}
	g.done(t, 1)
	if hits, _ := g.a.Search("secret", SearchFilter{}, 0); len(hits) != 0 {
		t.Errorf("an ephemeral conversation is searchable: %+v", hits)
	}
	// Back to a profile that keeps history.
	if _, err := g.a.SetConversationProfile(c.ID, PresetExplore); err != nil {
		t.Fatal(err)
	}
	if convIDs(t, g.a)[c.ID].Ephemeral {
		t.Error("a profile that keeps history should clear the flag")
	}
	if _, err := g.a.SetConversationProfile(c.ID, ghost.ID); err != nil {
		t.Fatal(err)
	}

	// Closing the app deletes it.
	if err := g.a.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(g.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.GetConversation(c.ID); err == nil {
		t.Fatal("the ephemeral conversation survived the close")
	}

	// One left behind by a crash goes at the next start.
	left, err := st.CreateConversation("left", "prod", "read", "p1", "m1")
	if err != nil {
		t.Fatal(err)
	}
	keep, _ := st.CreateConversation("keep", "prod", "read", "p1", "m1")
	if err := st.SetEphemeral(left.ID, true); err != nil {
		t.Fatal(err)
	}
	st.Close()
	a2 := NewAssistantService(&fakeHost{}, g.path)
	a2.keys = newMemKeys()
	a2.storePath = g.dbPath
	if err := a2.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a2.ServiceShutdown() })
	list := convIDs(t, a2)
	if _, ok := list[left.ID]; ok {
		t.Error("the ephemeral conversation was still there after the start")
	}
	if _, ok := list[keep.ID]; !ok {
		t.Error("an ordinary conversation was deleted at the start")
	}
}

func TestSweepRunsAtStartAndEveryInterval(t *testing.T) {
	g, _ := historyRig(t, textTurn("a"))
	first := g.converse(t, "first")
	old := time.Now().AddDate(0, 0, -200)
	ageConv(t, g.dbPath, first.ID, old)
	if err := g.a.SetRetention(30); err != nil {
		t.Fatal(err)
	}
	if err := g.a.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if g.a.hist.done == nil {
		t.Fatal("no sweep goroutine was started")
	}
	select {
	case <-g.a.hist.done:
	default:
		t.Fatal("the sweep goroutine is still running after the shutdown")
	}

	// A new service over the same file: the start sweep removes the old one,
	// then the ticker removes one that grows old later.
	a2 := NewAssistantService(&fakeHost{}, g.path)
	a2.keys = newMemKeys()
	a2.storePath = g.dbPath
	a2.hist.every = 10 * time.Millisecond
	if err := a2.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, ok := convIDs(t, a2)[first.ID]; return !ok })

	st, err := store.Open(g.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	later, _ := st.CreateConversation("later", "prod", "read", "p1", "m1")
	st.Close()
	ageConv(t, g.dbPath, later.ID, old)
	waitFor(t, func() bool { _, ok := convIDs(t, a2)[later.ID]; return !ok })

	if err := a2.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a2.hist.done:
	default:
		t.Fatal("the ticker goroutine is still running after the shutdown")
	}
}
