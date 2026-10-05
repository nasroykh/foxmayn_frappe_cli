package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// edTSite has a draft (SO-1) and a submitted (SO-2) Sales Order with child
// rows, and fields of every kind edit-doc must leave out.
func edTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	for _, f := range [][2]string{
		{"customer", "Link"}, {"status", "Select"}, {"notes", "Small Text"}, {"total_qty", "Int"},
		{"is_urgent", "Check"}, {"phone", "Data"}, {"po_no", "Data"}, {"internal_ref", "Data"},
		{"computed", "Data"}, {"customer_name", "Data"}, {"api_password", "Password"}, {"lft", "Int"},
		{"details", "Section Break"}, {"naming", "Data"},
	} {
		s.DocField("Sales Order", f[0], f[1])
	}
	s.ChildTable("Sales Order", "items", "Sales Order Item")
	s.DocField("Sales Order Item", "item_code", "Data")
	s.DocField("Sales Order Item", "qty", "Float")
	s.DocField("Sales Order Item", "rate", "Currency")
	s.DocField("Sales Order Item", "amount", "Currency")
	s.FieldProp("Sales Order", "status", "read_only", 1)
	s.FieldProp("Sales Order", "internal_ref", "hidden", 1)
	s.FieldProp("Sales Order", "computed", "is_virtual", 1)
	s.FieldProp("Sales Order", "customer_name", "fetch_from", "customer.customer_name")
	s.FieldProp("Sales Order", "naming", "set_only_once", 1)
	s.FieldProp("Sales Order Item", "amount", "read_only", 1)
	s.AllowOnSubmit("Sales Order", "po_no")
	s.AllowOnSubmit("Sales Order Item", "rate")
	row := func(name, code string, qty, rate string) map[string]interface{} {
		return map[string]interface{}{"name": name, "item_code": code, "qty": json.Number(qty), "rate": json.Number(rate), "amount": json.Number("9.0")}
	}
	s.Add("Sales Order",
		map[string]interface{}{"name": "SO-1", "customer": "C1", "status": "Draft", "notes": "line one\nline two",
			"total_qty": json.Number("3"), "is_urgent": json.Number("0"), "phone": "0123", "internal_ref": "x",
			"customer_name": "Customer One", "api_password": "*****", "lft": json.Number("1"), "naming": "SO-",
			"items": []interface{}{row("r1", "A", "1.0", "10.0"), row("r2", "B", "2.0", "20.0")}},
		map[string]interface{}{"name": "SO-2", "customer": "C2", "docstatus": json.Number("1"), "po_no": "PO-2",
			"items": []interface{}{row("r3", "C", "1.0", "5.0"), row("r4", "E", "1.0", "4.0")}},
		map[string]interface{}{"name": "SO-3", "customer": "C3", "docstatus": json.Number("2")},
	)
	return s
}

// edTEditor replaces the editor: each time it opens, the next edit turns
// the file into what is saved. It returns the files the editor was shown.
// Prompts count as available unless --no-input is set.
func edTEditor(t *testing.T, edits ...func(string) string) *[]string {
	t.Helper()
	seen := &[]string{}
	oldRun, oldIn := runEditor, editInputDisabled
	runEditor = func(path string) error {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("editor: %v", err)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("temp file mode = %v, want 0600", st.Mode().Perm())
		}
		b, _ := os.ReadFile(path)
		*seen = append(*seen, path+"\x00"+string(b))
		if len(*seen) > len(edits) {
			t.Fatalf("editor opened %d times, expected %d", len(*seen), len(edits))
		}
		return os.WriteFile(path, []byte(edits[len(*seen)-1](string(b))), 0o600)
	}
	editInputDisabled = func() bool { return noInput }
	t.Cleanup(func() { runEditor, editInputDisabled = oldRun, oldIn })
	return seen
}

// edTFile returns the content of the n-th file shown, and checks that its
// temporary directory is gone.
func edTFile(t *testing.T, seen *[]string, n int) string {
	t.Helper()
	if len(*seen) <= n {
		t.Fatalf("editor opened %d times", len(*seen))
	}
	path, content, _ := strings.Cut((*seen)[n], "\x00")
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("temp dir %s still exists (%v)", filepath.Dir(path), err)
	}
	return content
}

func replace(old, new string) func(string) string {
	return func(s string) string {
		if !strings.Contains(s, old) {
			panic(fmt.Sprintf("edit: %q not in file:\n%s", old, s))
		}
		return strings.Replace(s, old, new, 1)
	}
}

func same(s string) string { return s }

func edTPuts(s *frappetest.Site, name string) []frappetest.Request {
	return s.RequestsTo(http.MethodPut, "/api/resource/Sales Order/"+name)
}

func edTBody(t *testing.T, r frappetest.Request) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(r.Body))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("PUT body: %v (%s)", err, r.Body)
	}
	return m
}

func TestEditDocFileAndFields(t *testing.T) {
	s := edTSite(t)
	before, _ := s.Doc("Sales Order", "SO-1")
	seen := edTEditor(t, replace("customer: C1", "customer: C9"))
	r := cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes", "--json", "--keys", "customer"))
	file := edTFile(t, seen, 0)
	for _, want := range []string{"# Editing Sales Order SO-1", "customer: C1", "notes: |-\n  line one\n  line two",
		"total_qty: 3", "is_urgent: 0", `phone: "0123"`, "items:\n  - name: r1\n    item_code: A\n    qty: 1.0\n    rate: 10.0"} {
		if !strings.Contains(file, want) {
			t.Errorf("file lacks %q:\n%s", want, file)
		}
	}
	for _, absent := range []string{"status", "internal_ref", "computed", "customer_name", "api_password", "lft",
		"details", "naming", "amount", "modified", "owner", "docstatus", "idx", "creation"} {
		if strings.Contains(file, absent+":") {
			t.Errorf("file shows %s:\n%s", absent, file)
		}
	}
	if got := cmdTObj(t, r); got["customer"] != "C9" || len(got) != 1 {
		t.Errorf("output = %v", got)
	}
	puts := edTPuts(s, "SO-1")
	if len(puts) != 1 {
		t.Fatalf("PUTs = %d", len(puts))
	}
	body := edTBody(t, puts[0])
	if len(body) != 2 || body["customer"] != "C9" || body["modified"] != before["modified"] {
		t.Errorf("PUT body = %v, want customer and modified only", body)
	}
	if !strings.Contains(r.Stderr, `customer: "C1" → "C9"`) {
		t.Errorf("stderr lacks the diff: %s", r.Stderr)
	}
}

func TestEditDocValues(t *testing.T) {
	s := edTSite(t)
	edTEditor(t, func(f string) string {
		f = replace("total_qty: 3", "total_qty: 0x10")(f)
		f = replace("is_urgent: 0", "is_urgent: true")(f)
		f = replace(`phone: "0123"`, "phone: 0456")(f)
		return replace("notes: |-\n  line one\n  line two", "notes: null")(f)
	})
	cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	body := edTBody(t, edTPuts(s, "SO-1")[0])
	want := map[string]string{"total_qty": "16", "is_urgent": "1", "phone": "0456", "notes": "<nil>"}
	for k, w := range want {
		if got := fmt.Sprint(body[k]); got != w {
			t.Errorf("%s = %s, want %s", k, got, w)
		}
	}
	if _, isNum := body["total_qty"].(json.Number); !isNum {
		t.Errorf("total_qty sent as %T", body["total_qty"])
	}
	if _, isStr := body["phone"].(string); !isStr {
		t.Errorf("phone sent as %T", body["phone"])
	}
}

func TestEditDocCancelled(t *testing.T) {
	cases := map[string]struct {
		edit func(string) string
		want string
	}{
		"unchanged":     {same, "no changes made"},
		"comments only": {func(f string) string { return "# a note\n" + f }, "no changes made"},
		"field removed": {replace("customer: C1\n", ""), "no changes made"},
		"empty":         {func(string) string { return "# nothing\n\n" }, "the file is empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := edTSite(t)
			edTEditor(t, c.edit)
			r := cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--json"))
			if got := cmdTObj(t, r); !strings.Contains(r.Stderr, "Edit cancelled, "+c.want) || got["cancelled"] != true || got["reason"] != c.want {
				t.Errorf("stdout %q, stderr %q", r.Stdout, r.Stderr)
			}
			if n := len(edTPuts(s, "SO-1")); n != 0 {
				t.Errorf("%d PUTs", n)
			}
		})
	}
}

func TestEditDocInvalidFileReopens(t *testing.T) {
	s := edTSite(t)
	seen := edTEditor(t,
		replace("customer: C1", "customer: [C1"),
		func(f string) string {
			return replace("customer: [C1", "customer: C2\nstatus: Closed")(f)
		},
		replace("total_qty: 3", "total_qty: many"),
		func(f string) string {
			f = replace("status: Closed\n", "")(f)
			return replace("total_qty: many", "total_qty: 4")(f)
		},
	)
	cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	second, third, fourth := edTFile(t, seen, 1), edTFile(t, seen, 2), edTFile(t, seen, 3)
	if !strings.HasPrefix(second, "# ERROR: ") || !strings.Contains(second, "customer: [C1") {
		t.Errorf("second file:\n%s", second)
	}
	if !strings.Contains(third, `"status" is not an editable field of Sales Order`) || strings.Count(third, "# ERROR: ") != 2 {
		t.Errorf("third file:\n%s", third)
	}
	// The line number points at the line in the file shown.
	lines := strings.Split(fourth, "\n")
	var at string
	for _, l := range lines {
		if strings.HasPrefix(l, "# ERROR: line ") {
			at = strings.Fields(strings.TrimPrefix(l, "# ERROR: line "))[0]
		}
	}
	var n int
	if _, err := fmt.Sscanf(at, "%d:", &n); err != nil || n < 1 || n > len(lines) || !strings.HasPrefix(lines[n-1], "status:") {
		t.Errorf("error line %q does not point at status:\n%s", at, fourth)
	}
	body := edTBody(t, edTPuts(s, "SO-1")[0])
	if body["customer"] != "C2" || fmt.Sprint(body["total_qty"]) != "4" {
		t.Errorf("body = %v", body)
	}
}

func TestEditDocInvalidFileGivenUp(t *testing.T) {
	s := edTSite(t)
	edTEditor(t, replace("customer: C1", "customer: [C1"), same)
	r := cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes")
	lcTCode(t, r, exitUsage, "still has an error, nothing was saved")
	if n := len(edTPuts(s, "SO-1")); n != 0 {
		t.Errorf("%d PUTs", n)
	}
}

func TestEditDocConflict(t *testing.T) {
	s := edTSite(t)
	other, err := client.New(context.Background(), &config.SiteConfig{URL: s.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	edTEditor(t, func(f string) string {
		// Someone saves the document while the editor is open.
		if _, err := other.UpdateDoc(context.Background(), "Sales Order", "SO-1", map[string]interface{}{"phone": "999"}); err != nil {
			t.Fatal(err)
		}
		return replace("customer: C1", "customer: C9")(f)
	})
	r := cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes")
	lcTCode(t, r, exitValidation, "Sales Order SO-1 was changed on the server since you opened it; nothing was saved", "TimestampMismatchError")
	if d, _ := s.Doc("Sales Order", "SO-1"); d["customer"] != "C1" || d["phone"] != "999" {
		t.Errorf("stored = %v", d)
	}
}

func TestEditDocChildRows(t *testing.T) {
	s := edTSite(t)
	edTEditor(t, func(f string) string {
		f = replace("  - name: r1\n    item_code: A\n    qty: 1.0", "  - name: r1\n    item_code: A\n    qty: 5")(f)
		f = replace("  - name: r2\n    item_code: B\n    qty: 2.0\n    rate: 20.0\n", "")(f)
		return f + "  - item_code: D\n    qty: 1\n"
	})
	r := cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	for _, want := range []string{"items (the whole table is saved)", "~ row r1: qty: 1.0 → 5;", `+ new row: item_code: "D", qty: 1`, "- REMOVED row r2", "nothing is carried over from a removed row"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Stderr)
		}
	}
	body := edTBody(t, edTPuts(s, "SO-1")[0])
	items, _ := body["items"].([]interface{})
	if len(items) != 2 || len(body) != 2 {
		t.Fatalf("body = %v", body)
	}
	kept, added := items[0].(map[string]interface{}), items[1].(map[string]interface{})
	// The kept row goes back whole: read-only columns are not lost.
	if kept["name"] != "r1" || fmt.Sprint(kept["qty"]) != "5" || fmt.Sprint(kept["amount"]) != "9.0" || kept["item_code"] != "A" || fmt.Sprint(kept["idx"]) != "1" {
		t.Errorf("kept row = %v", kept)
	}
	if _, has := added["name"]; has || added["item_code"] != "D" || fmt.Sprint(added["idx"]) != "2" {
		t.Errorf("added row = %v", added)
	}
	d, _ := s.Doc("Sales Order", "SO-1")
	rows := rowsOf(d["items"])
	if len(rows) != 2 || rows[0]["name"] != "r1" || rows[1]["name"] == "" || rows[1]["name"] == "r2" {
		t.Errorf("stored rows = %v", rows)
	}
}

func TestEditDocRowErrors(t *testing.T) {
	cases := map[string]struct {
		edit func(string) string
		want string
	}{
		"unknown row":       {replace("name: r1", "name: r9"), `items has no row "r9"`},
		"duplicate row":     {replace("name: r2", "name: r1"), `row "r1" of items appears twice`},
		"read-only cell":    {replace("item_code: A", "item_code: A\n    amount: 1"), `"amount" is not an editable field of items rows`},
		"not a list":        {func(f string) string { return f[:strings.Index(f, "items:")] + "items: 3\n" }, "items: expected a list of rows"},
		"name line deleted": {replace("  - name: r2\n    item_code", "  - item_code"), `this row is row "r2" of items without its "name" line`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := edTSite(t)
			seen := edTEditor(t, c.edit, same)
			lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"), exitUsage)
			if f := edTFile(t, seen, 1); !strings.Contains(f, c.want) {
				t.Errorf("reopened file lacks %q:\n%s", c.want, f)
			}
		})
	}
}

func TestEditDocSubmitted(t *testing.T) {
	s := edTSite(t)
	seen := edTEditor(t, func(f string) string {
		f = replace("po_no: PO-2", "po_no: PO-22")(f)
		return replace("rate: 5.0", "rate: 6.5")(f)
	})
	cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-2", "--yes"))
	file := edTFile(t, seen, 0)
	if !strings.Contains(file, "only the fields allowed on submit") || strings.Contains(file, "customer:") ||
		strings.Contains(file, "item_code:") || !strings.Contains(file, "  - name: r3\n    rate: 5.0") {
		t.Errorf("file:\n%s", file)
	}
	d, _ := s.Doc("Sales Order", "SO-2")
	rows := rowsOf(d["items"])
	if d["po_no"] != "PO-22" || len(rows) != 2 || fmt.Sprint(rows[0]["rate"]) != "6.5" || rows[0]["item_code"] != "C" {
		t.Errorf("stored = %v", d)
	}

	// Rows of a table that is not "allow on submit" stay.
	s = edTSite(t)
	seen = edTEditor(t, func(f string) string { return f + "  - rate: 1\n" }, same)
	lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-2", "--yes"), exitUsage)
	if f := edTFile(t, seen, 1); !strings.Contains(f, "rows cannot be added to items after submit") {
		t.Errorf("reopened file:\n%s", f)
	}
	for name, c := range map[string]struct {
		edit func(string) string
		want string
	}{
		"null":    {func(f string) string { return f[:strings.Index(f, "items:")] + "items: null\n" }, "rows cannot be removed from items after submit"},
		"emptied": {func(f string) string { return f[:strings.Index(f, "items:")] + "items: []\n" }, "rows cannot be removed from items after submit"},
		"moved": {func(f string) string {
			i := strings.Index(f, "  - name: r3")
			j := strings.Index(f, "  - name: r4")
			return f[:i] + f[j:] + f[i:j]
		}, "rows of items cannot be moved after submit"},
	} {
		seen = edTEditor(t, c.edit, same)
		lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-2", "--yes"), exitUsage)
		if f := edTFile(t, seen, 1); !strings.Contains(f, c.want) {
			t.Errorf("%s: reopened file:\n%s", name, f)
		}
	}
	if n := len(edTPuts(s, "SO-2")); n != 0 {
		t.Errorf("%d PUTs", n)
	}

	// A cancelled document is not edited.
	lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-3", "--yes"), exitValidation, "is cancelled", "amend-doc")
}

func TestEditDocNeedsTerminal(t *testing.T) {
	s := edTSite(t)
	// The test's stdin is not a terminal.
	lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1"), exitUsage, "opens an editor", "not a terminal")
	edTEditor(t)
	lcTCode(t, cmdTRun(t, s, "--no-input", "edit-doc", "-d", "Sales Order", "-n", "SO-1"), exitUsage, "opens an editor")
	if n := len(s.Requests()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestEditDocConfirm(t *testing.T) {
	s := edTSite(t)
	edTEditor(t, replace("customer: C1", "customer: C9"))
	editInputDisabled = func() bool { return false }
	// The editor ran, but the question needs a terminal: --yes is the way out.
	lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1"), exitUsage, "pass --yes")
	if n := len(edTPuts(s, "SO-1")); n != 0 {
		t.Errorf("%d PUTs", n)
	}
}

func TestEditDocDryRun(t *testing.T) {
	s := edTSite(t)
	edTEditor(t, func(f string) string {
		f = replace("customer: C1", "customer: C9")(f)
		return replace("  - name: r2\n    item_code: B\n    qty: 2.0\n    rate: 20.0\n", "")(f)
	})
	r := cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--dry-run", "--json"))
	reqs := dryTPlan(t, r)
	if len(reqs) != 1 || reqs[0]["method"] != "PUT" {
		t.Fatalf("plan = %v", reqs)
	}
	ch, _ := reqs[0]["changes"].(map[string]interface{})
	items, _ := ch["items"].(map[string]interface{})
	if c, _ := ch["customer"].(map[string]interface{}); c["to"] != "C9" || !strings.Contains(fmt.Sprint(items["to"]), "1 removed (r2)") {
		t.Errorf("changes = %v", ch)
	}
	if n := len(edTPuts(s, "SO-1")); n != 0 {
		t.Errorf("%d PUTs", n)
	}
}

func TestEditDocRealEditor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the editor")
	}
	s := edTSite(t)
	script := filepath.Join(t.TempDir(), "ed.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsed -e 's/^customer: C1$/customer: C7/' \"$1\" > \"$1.new\" && mv \"$1.new\" \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", script)
	old := editInputDisabled
	editInputDisabled = func() bool { return false }
	t.Cleanup(func() { editInputDisabled = old })
	cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	if d, _ := s.Doc("Sales Order", "SO-1"); d["customer"] != "C7" {
		t.Errorf("customer = %v", d["customer"])
	}
	// A failing editor saves nothing.
	t.Setenv("EDITOR", "false")
	lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"), exitGeneric, "editor false", "nothing was saved")
}

func TestUpdateDocDiffAndIfUnmodified(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"Closed","priority":1}`, "--diff"))
	if !strings.Contains(r.Stderr, "Changes to ToDo TD-1:\n  status: \"Open\" → \"Closed\"\n") || strings.Contains(r.Stderr, "priority") {
		t.Errorf("stderr = %s", r.Stderr)
	}
	if d, _ := s.Doc("ToDo", "TD-1"); d["status"] != "Closed" {
		t.Errorf("not updated: %v", d)
	}
	r = cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"Closed"}`, "--diff"))
	if !strings.Contains(r.Stderr, "No changes to ToDo TD-1") {
		t.Errorf("stderr = %s", r.Stderr)
	}

	d, _ := s.Doc("ToDo", "TD-1")
	stale := fmt.Sprint(d["modified"])
	cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"Open"}`, "--if-unmodified", stale))
	r = cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"Closed"}`, "--if-unmodified", stale, "--diff")
	lcTCode(t, r, exitValidation, "ToDo TD-1 was changed on the server since --if-unmodified", "nothing was saved")
	if d, _ := s.Doc("ToDo", "TD-1"); d["status"] != "Open" {
		t.Errorf("stale update applied: %v", d)
	}
	// No diff for an update that was not made.
	if strings.Contains(r.Stderr, "Changes to") {
		t.Errorf("diff printed for a failed update: %s", r.Stderr)
	}
}

// --diff sends the modified it read, so the diff shown cannot be stale.
func TestUpdateDocDiffPinsModified(t *testing.T) {
	s := cmdTSite(t)
	cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"Closed"}`, "--diff"))
	puts := s.RequestsTo(http.MethodPut, "/api/resource/ToDo/TD-1")
	if len(puts) != 1 || !strings.Contains(puts[0].Body, `"modified"`) {
		t.Errorf("PUT = %+v", puts)
	}
	// Someone saves between the read and the update.
	s.Handle("GET /api/resource/ToDo/TD-2", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"name":"TD-2","status":"Closed","modified":"2000-01-01 00:00:00.000000"}}`))
	}))
	r := cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-2", "--data", `{"status":"Open"}`, "--diff")
	lcTCode(t, r, exitValidation, "ToDo TD-2 was changed on the server since it was read for --diff")
	if strings.Contains(r.Stderr, "Changes to") {
		t.Errorf("diff printed: %s", r.Stderr)
	}
	// Without --diff nothing is pinned.
	cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-2", "--data", `{"status":"Open"}`))
}

func TestMCPUpdateDocIfUnmodified(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1", "status": "Open"})
	d, _ := site.Doc("ToDo", "TD-1")
	stale := fmt.Sprint(d["modified"])
	mcpTOK(t, s, "update_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"status": "Closed"}, "if_unmodified": stale})
	res := callTool(t, s, "update_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"status": "Open"}, "if_unmodified": stale})
	if txt := resultText(t, res); !res.IsError || !strings.Contains(txt, "changed on the server since if_unmodified") {
		t.Errorf("stale call: error=%v %s", res.IsError, txt)
	}
	if d, _ := site.Doc("ToDo", "TD-1"); d["status"] != "Closed" {
		t.Errorf("stale update applied: %v", d)
	}
}
