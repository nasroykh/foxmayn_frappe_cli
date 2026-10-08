package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// importTSite is exportTSite plus a Ticket DocType with labels, every
// converted field type, Links and a table of Lines.
func importTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := exportTSite(t)
	field := func(dt, name, ftype, label, options string) {
		s.DocField(dt, name, ftype)
		if label != "" {
			s.FieldProp(dt, name, "label", label)
		}
		if options != "" {
			s.FieldProp(dt, name, "options", options)
		}
	}
	field("Ticket", "subject", "Data", "Subject", "")
	field("Ticket", "status", "Select", "Status", "\nOpen\nClosed")
	field("Ticket", "urgent", "Check", "Urgent", "")
	field("Ticket", "due", "Date", "Due Date", "")
	field("Ticket", "seen_at", "Datetime", "Seen At", "")
	field("Ticket", "effort", "Duration", "Effort", "")
	field("Ticket", "hours", "Float", "Hours", "")
	field("Ticket", "count", "Int", "Count", "")
	field("Ticket", "customer", "Link", "Customer", "Customer")
	field("Ticket", "parent_ticket", "Link", "Parent Ticket", "Ticket")
	s.ChildTable("Ticket", "lines", "Ticket Line")
	s.FieldProp("Ticket", "lines", "label", "Lines")
	field("Ticket Line", "item", "Link", "Item", "Item")
	field("Ticket Line", "qty", "Int", "Qty", "")
	s.AddDocType("Ticket", "subject", "status", "customer")
	s.Add("Customer", map[string]interface{}{"name": "ACME"}, map[string]interface{}{"name": "Bob"})
	s.Add("Item", map[string]interface{}{"name": "I-A"}, map[string]interface{}{"name": "I-B"})
	return s
}

func writeImportFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writes(s *frappetest.Site) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == http.MethodPut || r.Method == http.MethodDelete ||
			r.Method == http.MethodPost && strings.HasPrefix(r.Path, "/api/resource/") ||
			r.Method == http.MethodPost && r.Path == "/api/method/frappe.client.submit" {
			n++
		}
	}
	return n
}

func importJSON(t *testing.T, r cliResult) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, r.Stdout, r.Stderr)
	}
	return out
}

func importStatuses(out map[string]interface{}) []string {
	var st []string
	for _, r := range out["results"].([]interface{}) {
		m := r.(map[string]interface{})
		st = append(st, fmt.Sprint(m["status"]))
	}
	return st
}

// What export writes, import reads back as unchanged: CSV (formula
// escapes, Duration text, continuation rows, a multi-line cell), JSON and
// NDJSON.
func TestImportRoundTripUnchanged(t *testing.T) {
	s := exportTSite(t)
	for _, f := range []struct{ file, output string }{{"o.csv", "csv"}, {"o.json", "json"}, {"o.ndjson", "ndjson"}} {
		exp := cmdTRun(t, s, "--output", f.output, "export", "-d", "Order")
		if exp.Code != 0 {
			t.Fatalf("export: %s", exp.Stderr)
		}
		path := writeImportFile(t, f.file, exp.Stdout)
		before := writes(s)
		r := cmdTRun(t, s, "--json", "import", "-d", "Order", path, "--mode", "update")
		if r.Code != 0 {
			t.Fatalf("%s: exit %d %s\n%s", f.output, r.Code, r.Stderr, r.Stdout)
		}
		out := importJSON(t, r)
		if got := importStatuses(out); !reflect.DeepEqual(got, []string{"unchanged", "unchanged", "unchanged"}) || out["unchanged"] != float64(3) {
			t.Errorf("%s: %v", f.output, out)
		}
		if writes(s) != before {
			t.Errorf("%s: an unchanged import wrote", f.output)
		}
	}
	// The same through stdin.
	exp := cmdTRun(t, s, "export", "-d", "Order")
	r := cmdTRunStdin(t, s, exp.Stdout, "import", "-d", "Order", "-", "--mode", "update")
	if r.Code != 0 || !strings.Contains(r.Stderr, "0 created, 0 updated, 3 unchanged") {
		t.Errorf("stdin: exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

// Insert: header forms (label, "Label (fieldname)", ID, table.field,
// "Label (Table)"), continuation rows, blank rows, every conversion.
func TestImportInsertCSV(t *testing.T) {
	s := importTSite(t)
	csv := "\xef\xbb\xbfID,Subject,Status (status),urgent,Due Date,seen_at,effort,hours,count,customer,lines.item,Qty (Lines),Owner\n" +
		",First,Open,yes,31-01-2026,2026-01-31 10:05:00,1d 2h,\"1,5\",7,ACME,I-A,1,x@y\n" +
		",,,,,,,,,,I-B,2,\n" +
		"\n" +
		",,,,,,,,,,I-A,3,\n" +
		",'=Second,,N,01-02-2026,2026-02-01 22:00:00,'-30m,2.50,+4,Bob,,,\n"
	// "1,5" is a grouped number: refused. Fix it and import.
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", csv), "--mode", "insert")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, `line 2, hours "1,5": not a number`) || writes(s) != 0 {
		t.Fatalf("exit %d\n%s", r.Code, r.Stderr)
	}
	csv = strings.Replace(csv, `"1,5"`, "1.5", 1)
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "t.csv", csv), "--mode", "insert")
	if r.Code != 0 {
		t.Fatalf("exit %d\n%s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "ignored columns") || !strings.Contains(r.Stderr, "Owner") {
		t.Errorf("no warning for Owner: %s", r.Stderr)
	}
	out := importJSON(t, r)
	res := out["results"].([]interface{})
	if len(res) != 2 || out["created"] != float64(2) {
		t.Fatalf("%v", out)
	}
	first := res[0].(map[string]interface{})
	second := res[1].(map[string]interface{})
	if first["row"] != float64(2) || second["row"] != float64(6) {
		t.Errorf("rows %v %v", first["row"], second["row"])
	}
	d1, _ := s.Doc("Ticket", fmt.Sprint(first["name"]))
	d2, _ := s.Doc("Ticket", fmt.Sprint(second["name"]))
	want1 := map[string]interface{}{"subject": "First", "status": "Open", "urgent": json.Number("1"), "due": "2026-01-31",
		"seen_at": "2026-01-31 10:05:00", "effort": json.Number("93600"), "hours": json.Number("1.5"), "count": json.Number("7"), "customer": "ACME"}
	for k, v := range want1 {
		if !reflect.DeepEqual(d1[k], v) {
			t.Errorf("first %s = %#v, want %#v", k, d1[k], v)
		}
	}
	if _, ok := d1["owner"]; ok && d1["owner"] == "x@y" {
		t.Errorf("owner was sent")
	}
	lines := rowsOf(d1["lines"])
	if len(lines) != 3 || lines[0]["item"] != "I-A" || lines[1]["item"] != "I-B" || lines[2]["qty"] != json.Number("3") {
		t.Errorf("lines = %v", lines)
	}
	want2 := map[string]interface{}{"subject": "=Second", "urgent": json.Number("0"), "due": "2026-02-01",
		"seen_at": "2026-02-01 22:00:00", "effort": json.Number("-1800"), "hours": json.Number("2.50"), "count": json.Number("4")}
	for k, v := range want2 {
		if !reflect.DeepEqual(d2[k], v) {
			t.Errorf("second %s = %#v, want %#v", k, d2[k], v)
		}
	}
	if _, ok := d2["lines"]; ok && len(rowsOf(d2["lines"])) != 0 {
		t.Errorf("second lines = %v", d2["lines"])
	}
}

// Every value problem is listed with its line, column and value, and
// nothing is written; --json puts them on stdout.
func TestImportProblemsWriteNothing(t *testing.T) {
	s := importTSite(t)
	csv := "subject,status,urgent,due,effort,count,customer,lines.item,lines.qty\n" +
		"A,Pending,maybe,31-01-2026,2 hours,1.5,Nobody,I-A,1\n" +
		",,,,,,,I-Z,x\n" +
		"B,Open,1,2026-02-30,1h,2,ACME,,\n"
	path := writeImportFile(t, "bad.csv", csv)
	r := cmdTRun(t, s, "import", "-d", "Ticket", path, "--mode", "insert")
	if r.Code != exitValidation || writes(s) != 0 {
		t.Fatalf("exit %d, %d writes\n%s", r.Code, writes(s), r.Stderr)
	}
	for _, want := range []string{
		`line 2, status "Pending": not one of the options: Open, Closed`,
		`line 2, urgent "maybe": not a Check value`,
		`line 2, effort "2 hours": not a duration`,
		`line 2, count "1.5": not a whole number`,
		`line 2, customer "Nobody": no Customer named "Nobody" (or your User Permissions hide it from you)`,
		`line 3, lines.item "I-Z": no Item named "I-Z"`,
		`line 3, lines.qty "x": not a whole number`,
		// The column's format is dd-mm-yyyy (31-01-2026); 2026-02-30 is not.
		`line 4, due "2026-02-30": not a valid date: use dd-mm-yyyy`,
		"8 problems in " + path + "; nothing was written",
	} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("missing %q in\n%s", want, r.Stderr)
		}
	}
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", path, "--mode", "insert")
	out := importJSON(t, r)
	probs := out["problems"].([]interface{})
	if r.Code != exitValidation || len(probs) != 8 {
		t.Fatalf("exit %d: %v", r.Code, out)
	}
	p := probs[0].(map[string]interface{})
	if p["row"] != float64(2) || p["column"] == "" || p["reason"] == "" {
		t.Errorf("problem = %v", p)
	}
}

// Header problems are usage errors, all listed; so are refused formats
// and an update without names.
func TestImportUsageErrors(t *testing.T) {
	s := importTSite(t)
	for _, c := range []struct {
		file, content string
		args          []string
		want          []string
	}{
		{"h.csv", "subject,nope,lines,Subject\nA,1,2,3\n", nil,
			[]string{`column 2 "nope" is not a field of Ticket`, `column 3 "lines" is a table`, `columns 1 "subject" and 4 "Subject" both set subject`}},
		{"t.tsv", "subject\nA\n", nil, []string{"TSV is not imported"}},
		{"t.xlsx", "PK", nil, []string{"Excel files are read only by Frappe"}},
		{"t.csv", "subject\nA\n", []string{"--format", "yaml"}, []string{"--format must be csv, json or ndjson"}},
		{"u.csv", "subject\nA\n", []string{"--mode", "update"}, []string{"needs a name (or ID) column"}},
		{"e.csv", "subject\n", nil, []string{"has no documents"}},
		{"j.json", `[{"subject":"A","bogus":1}]`, nil, []string{`"bogus" is not a field of Ticket`}},
	} {
		args := append([]string{"import", "-d", "Ticket", writeImportFile(t, c.file, c.content), "--mode", "insert"}, c.args...)
		r := cmdTRun(t, s, args...)
		if r.Code != exitUsage {
			t.Errorf("%s: exit %d\n%s", c.file, r.Code, r.Stderr)
		}
		for _, w := range c.want {
			if !strings.Contains(r.Stderr, w) {
				t.Errorf("%s: missing %q in %s", c.file, w, r.Stderr)
			}
		}
	}
	if r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "m.csv", "subject\nA\n"), "--mode", "upsert"); r.Code != exitUsage {
		t.Errorf("--mode upsert: exit %d", r.Code)
	}
	if writes(s) != 0 {
		t.Errorf("a usage error wrote")
	}
}

// Update: changed fields only, with modified; a table in the file
// replaces the rows (kept rows keep their name and other values, new
// rows have none, left-out rows go); a blank cell does not clear.
func TestImportUpdateTable(t *testing.T) {
	s := exportTSite(t)
	csv := "name,customer,notes,items.name,items.qty\n" +
		"O-1,Alice,,I-1,10\n" +
		",,,,5\n" +
		",,,I-3,3\n" +
		"O-2,Bob,,,\n"
	r := cmdTRun(t, s, "--json", "import", "-d", "Order", writeImportFile(t, "u.csv", csv), "--mode", "update")
	if r.Code != 0 {
		t.Fatalf("exit %d %s", r.Code, r.Stderr)
	}
	out := importJSON(t, r)
	if got := importStatuses(out); !reflect.DeepEqual(got, []string{"updated", "unchanged"}) {
		t.Errorf("statuses %v", got)
	}
	puts := s.RequestsTo("PUT", "/api/resource/Order/O-1")
	if len(puts) != 1 || len(s.RequestsTo("PUT", "/api/resource/Order/O-2")) != 0 {
		t.Fatalf("puts: %d", len(puts))
	}
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(puts[0].Body), &body)
	if _, ok := body["modified"]; !ok || body["customer"] != "Alice" || body["notes"] != nil {
		t.Errorf("body %v", body)
	}
	if _, ok := body["taxes"]; ok {
		t.Errorf("taxes sent: %v", body)
	}
	doc, _ := s.Doc("Order", "O-1")
	items := rowsOf(doc["items"])
	if len(items) != 3 || items[0]["name"] != "I-1" || items[0]["qty"] != json.Number("10") || items[0]["item"] != "a" ||
		items[1]["name"] == "I-2" || items[1]["qty"] != json.Number("5") || items[2]["name"] != "I-3" || items[2]["item"] != "c" {
		t.Errorf("items %v", items)
	}
	if doc["notes"] != "-5" || len(rowsOf(doc["taxes"])) != 1 {
		t.Errorf("doc %v", doc)
	}
	// Moving a row is a change.
	r = cmdTRun(t, s, "--json", "import", "-d", "Order", writeImportFile(t, "m.csv",
		fmt.Sprintf("name,items.name\nO-1,I-3\n,%s\n,I-1\n", items[1]["name"])), "--mode", "update")
	if got := importStatuses(importJSON(t, r)); !reflect.DeepEqual(got, []string{"updated"}) {
		t.Errorf("move: %v", got)
	}
}

// A document saved by someone else since it was read is a conflict.
func TestImportConflict(t *testing.T) {
	s := exportTSite(t)
	s.Handle("PUT /api/resource/Order/O-1", frappetest.ErrorHandler(&frappetest.Error{Status: http.StatusExpectationFailed,
		ExcType: "TimestampMismatchError", Message: "Document has been modified after you have opened it"}))
	r := cmdTRun(t, s, "--json", "import", "-d", "Order", writeImportFile(t, "c.csv", "name,customer\nO-1,X\nO-2,Y\n"), "--mode", "update")
	if r.Code != exitPartial {
		t.Fatalf("exit %d %s", r.Code, r.Stderr)
	}
	res := importJSON(t, r)["results"].([]interface{})
	first := res[0].(map[string]interface{})
	if first["status"] != "failed" || !strings.Contains(fmt.Sprint(first["error"]), "was changed on the server") {
		t.Errorf("%v", first)
	}
	if res[1].(map[string]interface{})["status"] != "updated" {
		t.Errorf("%v", res[1])
	}
}

// A missing document fails alone; --fail-fast skips the rest.
func TestImportPartialFailure(t *testing.T) {
	s := exportTSite(t)
	path := writeImportFile(t, "p.csv", "name,customer\nO-9,X\nO-2,Y\n")
	r := cmdTRun(t, s, "import", "-d", "Order", path, "--mode", "update")
	if r.Code != exitPartial || !strings.Contains(r.Stdout, "failed") || !strings.Contains(r.Stderr, "1 updated") {
		t.Errorf("exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	r = cmdTRun(t, s, "--json", "import", "-d", "Order", path, "--mode", "update", "--fail-fast")
	if got := importStatuses(importJSON(t, r)); r.Code != exitPartial || !reflect.DeepEqual(got, []string{"failed", "skipped"}) {
		t.Errorf("fail-fast: exit %d %v", r.Code, got)
	}
}

// --dry-run reads but writes nothing, and reports what it would do.
func TestImportDryRun(t *testing.T) {
	s := exportTSite(t)
	path := writeImportFile(t, "d.csv", "name,customer\nO-1,X\nO-2,Bob\n")
	r := cmdTRun(t, s, "--json", "import", "-d", "Order", path, "--mode", "update", "--dry-run")
	if r.Code != 0 || writes(s) != 0 {
		t.Fatalf("exit %d, %d writes %s", r.Code, writes(s), r.Stderr)
	}
	out := importJSON(t, r)
	reqs := out["requests"].([]interface{})
	if out["dry_run"] != true || !reflect.DeepEqual(importStatuses(out), []string{"updated", "unchanged"}) || len(reqs) != 1 {
		t.Fatalf("%v", out)
	}
	req := reqs[0].(map[string]interface{})
	ch := req["changes"].(map[string]interface{})["customer"].(map[string]interface{})
	if req["method"] != "PUT" || ch["from"] != "=cmd" || ch["to"] != "X" {
		t.Errorf("request %v", req)
	}
	r = cmdTRun(t, s, "import", "-d", "Order", writeImportFile(t, "i.csv", "customer\nNew\n"), "--mode", "insert", "--dry-run")
	if r.Code != 0 || writes(s) != 0 || !strings.Contains(r.Stdout, "POST") || !strings.Contains(r.Stderr, "Dry run: 1 created") {
		t.Errorf("insert: exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

// --submit: a submittable DocType only, no active workflow; each draft
// is submitted after the write, unchanged drafts too.
func TestImportSubmit(t *testing.T) {
	s := importTSite(t)
	csv := writeImportFile(t, "s.csv", "subject\nA\n")
	if r := cmdTRun(t, s, "import", "-d", "Ticket", csv, "--mode", "insert", "--submit"); r.Code != exitUsage || !strings.Contains(r.Stderr, "not submittable") {
		t.Errorf("not submittable: exit %d %s", r.Code, r.Stderr)
	}
	s.DocTypeFlags("Ticket", map[string]interface{}{"is_submittable": 1})
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", csv, "--mode", "insert", "--submit")
	if r.Code != 0 {
		t.Fatalf("exit %d %s", r.Code, r.Stderr)
	}
	out := importJSON(t, r)
	first := out["results"].([]interface{})[0].(map[string]interface{})
	doc, _ := s.Doc("Ticket", fmt.Sprint(first["name"]))
	if first["submitted"] != true || out["submitted"] != float64(1) || fmt.Sprint(doc["docstatus"]) != "1" {
		t.Errorf("%v / %v", out, doc)
	}
	// An unchanged draft is submitted; a submitted document is left alone.
	s.Add("Ticket", map[string]interface{}{"name": "T-D", "subject": "draft"})
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "u.csv", fmt.Sprintf("name,subject\nT-D,draft\n%s,A\n", first["name"])), "--mode", "update", "--submit")
	out = importJSON(t, r)
	res := out["results"].([]interface{})
	if r.Code != 0 || res[0].(map[string]interface{})["submitted"] != true || res[1].(map[string]interface{})["submitted"] != nil {
		t.Errorf("exit %d %v", r.Code, out)
	}
	s.Add("Workflow", map[string]interface{}{"name": "WF", "document_type": "Ticket", "is_active": json.Number("1")})
	before := writes(s)
	if r := cmdTRun(t, s, "import", "-d", "Ticket", csv, "--mode", "insert", "--submit"); r.Code != exitValidation || !strings.Contains(r.Stderr, `uses the workflow "WF"`) || writes(s) != before {
		t.Errorf("workflow: exit %d %s", r.Code, r.Stderr)
	}
}

// JSON and NDJSON documents with nested rows.
func TestImportJSONInsert(t *testing.T) {
	s := importTSite(t)
	js := `[{"subject":"J1","urgent":true,"due":"2026-03-04","effort":90,"customer":"ACME",
	         "lines":[{"item":"I-A","qty":2},{"item":"I-B","qty":"3"}]},
	        {"subject":"J2","status":null,"lines":[]}]`
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "t.json", js), "--mode", "insert")
	if r.Code != 0 {
		t.Fatalf("exit %d %s", r.Code, r.Stderr)
	}
	res := importJSON(t, r)["results"].([]interface{})
	d, _ := s.Doc("Ticket", fmt.Sprint(res[0].(map[string]interface{})["name"]))
	lines := rowsOf(d["lines"])
	if d["urgent"] != json.Number("1") || d["effort"] != json.Number("90") || d["due"] != "2026-03-04" ||
		len(lines) != 2 || lines[1]["qty"] != json.Number("3") {
		t.Errorf("doc %v", d)
	}
	nd := "{\"subject\":\"N1\",\"customer\":\"Nobody\"}\n\n{\"subject\":\"N2\",\"count\":\"x\"}\n"
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.ndjson", nd), "--mode", "insert")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, `line 1, customer "Nobody"`) || !strings.Contains(r.Stderr, `line 3, count "x"`) {
		t.Errorf("ndjson: exit %d %s", r.Code, r.Stderr)
	}
}

// Names on insert: kept with prompt naming, dropped (with a warning)
// otherwise, put in the naming field of "field:x" naming.
func TestImportInsertNames(t *testing.T) {
	s := importTSite(t)
	path := writeImportFile(t, "n.csv", "name,subject,lines.name,lines.item\nMY-1,A,r1,I-A\n")
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", path, "--mode", "insert")
	name := fmt.Sprint(importJSON(t, r)["results"].([]interface{})[0].(map[string]interface{})["name"])
	if r.Code != 0 || name == "MY-1" || !strings.Contains(r.Stderr, "the name column is ignored on insert") || !strings.Contains(r.Stderr, "row names") {
		t.Errorf("hash: exit %d %s %s", r.Code, name, r.Stderr)
	}
	if d, _ := s.Doc("Ticket", name); rowsOf(d["lines"])[0]["name"] == "r1" {
		t.Errorf("row name sent: %v", d["lines"])
	}
	s.DocTypeFlags("Ticket", map[string]interface{}{"autoname": "Prompt"})
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", path, "--mode", "insert")
	if _, ok := s.Doc("Ticket", "MY-1"); r.Code != 0 || !ok {
		t.Errorf("prompt: exit %d %s", r.Code, r.Stderr)
	}
	s.DocTypeFlags("Ticket", map[string]interface{}{"autoname": "field:subject"})
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "f.csv", "name,status\nNamed,Open\n"), "--mode", "insert")
	created := s.RequestsTo("POST", "/api/resource/Ticket")
	if r.Code != 0 || !strings.Contains(created[len(created)-1].Body, `"subject":"Named"`) {
		t.Errorf("field naming: exit %d %s", r.Code, created[len(created)-1].Body)
	}
	// Two documents with one name are a problem when the name is sent.
	s.DocTypeFlags("Ticket", map[string]interface{}{"autoname": "prompt"})
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "d.csv", "name,subject\nD-1,A\nd-1,B\n"), "--mode", "insert")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, `line 3, name "d-1": the same document as line 2`) {
		t.Errorf("duplicate: exit %d %s", r.Code, r.Stderr)
	}
}

// A Link to the imported DocType may name a document of the same file;
// Link checks go in one GET per target DocType.
func TestImportLinkChecks(t *testing.T) {
	s := importTSite(t)
	s.DocTypeFlags("Ticket", map[string]interface{}{"autoname": "prompt"})
	csv := "name,subject,customer,parent_ticket\nP-1,A,ACME,\nP-2,B,Bob,P-1\n"
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "l.csv", csv), "--mode", "insert")
	if r.Code != 0 {
		t.Fatalf("exit %d %s", r.Code, r.Stderr)
	}
	reqs := s.RequestsTo("GET", "/api/resource/Customer")
	if len(reqs) != 1 || !strings.Contains(reqs[0].Query.Get("filters"), `"in"`) {
		t.Errorf("customer checks: %v", reqs)
	}
	// Unreadable target: a warning, the site decides.
	s.Handle("GET /api/resource/Customer", frappetest.ErrorHandler(frappetest.Permission("no")))
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "l2.csv", "name,customer\nP-3,Whoever\n"), "--mode", "insert")
	if r.Code != 0 || !strings.Contains(r.Stderr, "may not read Customer") {
		t.Errorf("403: exit %d %s", r.Code, r.Stderr)
	}
}

func TestImportDateGuessing(t *testing.T) {
	for in, want := range map[string]string{
		"31-01-2026": "%d-%m-%Y", "01-31-2026": "%m-%d-%Y", "2026-01-31": "%Y-%m-%d", "31/01/26": "%d/%m/%y",
		"01/31/2026": "%m/%d/%Y", "31 Jan 2026": "%d %b %Y", "31 january 2026": "%d %B %Y", "31.01.2026": "%d.%m.%Y",
		"2026-01-31 10:05:00": "%Y-%m-%d %H:%M:%S", "2026-01-31 10:05:00.123": "%Y-%m-%d %H:%M:%S.%f",
		"31-01-2026 10:05 PM": "%d-%m-%Y %I:%M %p", "10:05": "%H:%M", "soon": "", "2026-13-45": "",
	} {
		if got := guessDateFormat(in); got != want {
			t.Errorf("guess(%q) = %q, want %q", in, got, want)
		}
	}
	// One format per column, the most common: 01-02-2026 reads as 1 Feb.
	if f := columnDateFormat([]string{"01-02-2026", "31-01-2026", "13-01-2026"}); f != "%d-%m-%Y" {
		t.Errorf("column format %q", f)
	}
	date := client.FormField{Fieldtype: "Date"}
	if v, err := convertImportValue(date, "01-02-2026", "%d-%m-%Y"); err != nil || v != "2026-02-01" {
		t.Errorf("date %v %v", v, err)
	}
	dt := client.FormField{Fieldtype: "Datetime"}
	if v, err := convertImportValue(dt, "31-01-2026 10:05 PM", "%d-%m-%Y %I:%M %p"); err != nil || v != "2026-01-31 22:05:00" {
		t.Errorf("datetime %v %v", v, err)
	}
	if v, err := convertImportValue(dt, "2026-01-31", "%Y-%m-%d"); err != nil || v != "2026-01-31 00:00:00" {
		t.Errorf("datetime from a date %v %v", v, err)
	}
	// A fraction of a second, or none, in a column of the other kind.
	if v, err := convertImportValue(dt, "2026-01-31 10:05:00.25", "%Y-%m-%d %H:%M:%S"); err != nil || v != "2026-01-31 10:05:00.250000" {
		t.Errorf("fraction %v %v", v, err)
	}
	if v, err := convertImportValue(dt, "2026-01-31 10:05:00", "%Y-%m-%d %H:%M:%S.%f"); err != nil || v != "2026-01-31 10:05:00" {
		t.Errorf("no fraction %v %v", v, err)
	}
	if _, err := convertImportValue(date, "29-02-2025", "%d-%m-%Y"); err == nil {
		t.Errorf("29-02-2025 accepted")
	}
}

func TestImportValueConversion(t *testing.T) {
	ft := func(t string) client.FormField { return client.FormField{Fieldtype: t} }
	ok := []struct {
		f    client.FormField
		in   interface{}
		want interface{}
	}{
		{ft("Check"), "Yes", json.Number("1")}, {ft("Check"), "t", json.Number("1")}, {ft("Check"), "FALSE", json.Number("0")},
		{ft("Check"), "n", json.Number("0")}, {ft("Check"), "1", json.Number("1")}, {ft("Check"), false, json.Number("0")},
		{ft("Int"), "+12", json.Number("12")}, {ft("Int"), "12.0", json.Number("12")}, {ft("Int"), json.Number("-3"), json.Number("-3")},
		{ft("Float"), ".5", json.Number("0.5")}, {ft("Currency"), "1500.50", json.Number("1500.50")}, {ft("Percent"), "1e2", json.Number("1e2")},
		{ft("Duration"), "1d 2h 3m 4s", json.Number("93784")}, {ft("Duration"), "45s", json.Number("45")}, {ft("Duration"), "2h 5s", json.Number("7205")},
		{ft("Duration"), "-1h 1m 40s", json.Number("-3700")}, {ft("Duration"), json.Number("12.5"), json.Number("12.5")},
		{client.FormField{Fieldtype: "Select", Options: "A\nB"}, "B", "B"}, {client.FormField{Fieldtype: "Select"}, "any", "any"},
		{ft("Data"), "0123", "0123"}, {ft("Link"), " ACME ", "ACME"},
		{ft("Check"), "0", json.Number("0")}, {ft("Check"), json.Number("1"), json.Number("1")},
		{ft("Time"), "10:00", "10:00:00"}, {ft("Time"), "9:05", "09:05:00"}, {ft("Time"), "23:59:59", "23:59:59"},
		{ft("Time"), "09:05:00.5", "09:05:00.500000"}, {ft("Time"), "09:05:00.000", "09:05:00"},
	}
	for _, c := range ok {
		got, err := convertImportValue(c.f, c.in, "")
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %#v = %#v, %v; want %#v", c.f.Fieldtype, c.in, got, err, c.want)
		}
	}
	bad := []struct {
		f  client.FormField
		in interface{}
	}{
		{ft("Check"), "maybe"}, {ft("Check"), "2"}, {ft("Check"), "-1"}, {ft("Check"), json.Number("5")},
		{ft("Time"), "24:00"}, {ft("Time"), "10:60"}, {ft("Time"), "10"}, {ft("Time"), "10:5"}, {ft("Time"), "10:00 PM"},
		{ft("Time"), json.Number("10")}, {ft("Int"), "2.5"}, {ft("Int"), "1,000"}, {ft("Float"), "1_000"}, {ft("Float"), "inf"},
		{ft("Duration"), "1h2m"}, {ft("Duration"), "90"}, {ft("Duration"), "2m 1h"}, {client.FormField{Fieldtype: "Select", Options: "A\nB"}, "a"},
		{ft("Data"), map[string]interface{}{}}, {ft("Data"), true}, {ft("Date"), json.Number("20260101")},
	}
	for _, c := range bad {
		if got, err := convertImportValue(c.f, c.in, ""); err == nil {
			t.Errorf("%s %#v accepted as %#v", c.f.Fieldtype, c.in, got)
		}
	}
	// The site answers a Time as "9:05:00": equal to the file's 09:05.
	tm := ft("Time")
	if !sameImportValue(tm, "9:05:00", "09:05:00") || !sameImportValue(tm, "9:05:00.500000", "09:05:00.500000") || sameImportValue(tm, "9:05:00", "09:06:00") {
		t.Errorf("Time comparison")
	}
}

// Header forms, from Frappe's build_fields_dict_for_column_matching.
func TestImportHeaderMap(t *testing.T) {
	label := func(name, ftype, l string) client.FormField {
		return client.FormField{Fieldname: name, Fieldtype: ftype, Label: l}
	}
	metas := map[string]*client.FormMeta{
		"T": {Name: "T", Autoname: "field:code", Fields: []client.FormField{
			label("code", "Data", "Code"), label("title", "Data", "Title"), label("other_title", "Data", "Title"),
			label("sb", "Section Break", "Details"), {Fieldname: "rows", Fieldtype: "Table", Options: "T Row", Label: "Rows"}}},
		"T Row": {Name: "T Row", Fields: []client.FormField{label("qty", "Int", "Qty")}},
	}
	m, err := newImportMeta("T", metas)
	if err != nil {
		t.Fatal(err)
	}
	h := m.headerMap()
	for header, want := range map[string]string{
		"name": "name", "ID": "name", "code": "code", "Code": "code", "ID (Code)": "code", "Title": "title",
		"other_title": "other_title", "Title (other_title)": "other_title", "rows.qty": "rows.qty", "Qty (Rows)": "rows.qty",
		"rows.name": "rows.name", "ID (Rows)": "rows.name", "Owner": "owner", "rows.idx": "rows.idx",
	} {
		if got, ok := h[header]; !ok || got.key() != want {
			t.Errorf("%q → %q (%v), want %q", header, got.key(), ok, want)
		}
	}
	for _, missing := range []string{"Details", "sb", "rows", "Rows", "qty"} {
		if _, ok := h[missing]; ok {
			t.Errorf("%q matched", missing)
		}
	}
}

// Password fields are refused (exit 2) in every form, and their values
// never appear in any output.
func TestImportRefusesPasswords(t *testing.T) {
	s := exportTSite(t)
	s.DocField("Order Item", "pin", "Password")
	for _, c := range []struct{ file, content string }{
		{"p.csv", "name,secret\nO-1,hunter2\n"},
		{"c.csv", "name,items.name,items.pin\nO-1,I-1,hunter2\n"},
		{"p.json", `[{"name":"O-1","secret":"hunter2"}]`},
		{"c.ndjson", `{"name":"O-1","items":[{"name":"I-1","pin":"hunter2"}]}` + "\n"},
	} {
		path := writeImportFile(t, c.file, c.content)
		for _, extra := range [][]string{nil, {"--json"}, {"--dry-run"}} {
			args := append([]string{"import", "-d", "Order", path, "--mode", "update"}, extra...)
			r := cmdTRun(t, s, args...)
			if r.Code != exitUsage || !strings.Contains(r.Stderr, "is a Password field") {
				t.Errorf("%s %v: exit %d %s", c.file, extra, r.Code, r.Stderr)
			}
			if strings.Contains(r.Stdout+r.Stderr, "hunter2") {
				t.Errorf("%s %v: the password was printed:\n%s\n%s", c.file, extra, r.Stdout, r.Stderr)
			}
		}
	}
	if writes(s) != 0 {
		t.Errorf("a refused file wrote")
	}
}

// An update whose table rows carry no row names, for a document that has
// rows on the site, would reset their other columns: a problem, nothing
// written. A document without rows on the site, and an insert, are fine.
func TestImportUnnamedRowsUpdate(t *testing.T) {
	s := exportTSite(t)
	path := writeImportFile(t, "u.csv", "name,items.qty\nO-1,5\n,6\nO-2,7\n")
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		r := cmdTRun(t, s, append([]string{"import", "-d", "Order", path, "--mode", "update"}, extra...)...)
		if r.Code != exitValidation || writes(s) != 0 ||
			!strings.Contains(r.Stderr, "line 2, items.name: O-1 has 3 items rows on the site and the file names none of them") ||
			!strings.Contains(r.Stderr, "ffc export") || strings.Contains(r.Stderr, "O-2 has") {
			t.Errorf("%v: exit %d, %d writes\n%s", extra, r.Code, writes(s), r.Stderr)
		}
	}
	r := cmdTRun(t, s, "import", "-d", "Order", writeImportFile(t, "o2.csv", "name,items.qty\nO-2,7\n"), "--mode", "update")
	if d, _ := s.Doc("Order", "O-2"); r.Code != 0 || len(rowsOf(d["items"])) != 1 {
		t.Errorf("O-2: exit %d %s", r.Code, r.Stderr)
	}
	r = cmdTRun(t, s, "import", "-d", "Order", writeImportFile(t, "i.csv", "customer,items.qty\nNew,5\n"), "--mode", "insert")
	if r.Code != 0 {
		t.Errorf("insert: exit %d %s", r.Code, r.Stderr)
	}
}

// A table value the site did not save as sent is reported, like a field.
func TestImportNotKeptTable(t *testing.T) {
	s := importTSite(t)
	answer := `{"data":{"name":"T-X","subject":"A","docstatus":0,"lines":[{"name":"r1","item":"I-A","qty":9,"modified":"later"}]}}`
	s.Handle("POST /api/resource/Ticket", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	path := writeImportFile(t, "k.csv", "subject,lines.item,lines.qty\nA,I-A,1\n")
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", path, "--mode", "insert")
	res := importJSON(t, r)["results"].([]interface{})[0].(map[string]interface{})
	if r.Code != 0 || res["warning"] != "not saved as sent: lines row 1 qty: sent 1, saved 9" {
		t.Errorf("exit %d %v", r.Code, res)
	}
	answer = `{"data":{"name":"T-X","subject":"A","docstatus":0,"lines":[]}}`
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", path, "--mode", "insert")
	res = importJSON(t, r)["results"].([]interface{})[0].(map[string]interface{})
	if res["warning"] != "not saved as sent: lines: sent 1 rows, saved 0" {
		t.Errorf("%v", res)
	}
}

// Same-file Links: only to names an insert keeps, only to a document
// above; then the writes keep the file's order. Long Link lists are split
// by URL length.
func TestImportSameFileLinks(t *testing.T) {
	s := importTSite(t)
	csv := "name,subject,parent_ticket\nP-1,A,\nP-2,B,P-1\n"
	// Hash naming: P-1 will not be P-1.
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "h.csv", csv), "--mode", "insert")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, `line 3, parent_ticket "P-1": no Ticket named "P-1"`) || writes(s) != 0 {
		t.Errorf("hash: exit %d %s", r.Code, r.Stderr)
	}
	s.DocTypeFlags("Ticket", map[string]interface{}{"autoname": "prompt"})
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "o.csv", "name,subject,parent_ticket\nQ-2,B,Q-1\nQ-1,A,\nQ-3,C,Q-3\n"), "--mode", "insert")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, `line 2, parent_ticket "Q-1": Ticket "Q-1" is created by line 3, after this document`) ||
		!strings.Contains(r.Stderr, `line 4, parent_ticket "Q-3": the document links to itself`) || writes(s) != 0 {
		t.Errorf("order: exit %d %s", r.Code, r.Stderr)
	}
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "c.csv", csv), "--mode", "insert", "--concurrency", "4")
	posts := s.RequestsTo("POST", "/api/resource/Ticket")
	if r.Code != 0 || !strings.Contains(r.Stderr, "writing one at a time") || len(posts) != 2 ||
		!strings.Contains(posts[0].Body, `"P-1"`) || !strings.Contains(posts[1].Body, `"parent_ticket":"P-1"`) {
		t.Errorf("concurrency: exit %d %s", r.Code, r.Stderr)
	}
	// 30 names of 300 characters: more than one GET, each URL bounded.
	var b strings.Builder
	b.WriteString("subject,customer\n")
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("%03d%s", i, strings.Repeat("c", 297))
		s.Add("Customer", map[string]interface{}{"name": name})
		fmt.Fprintf(&b, "S%d,%s\n", i, name)
	}
	before := len(s.RequestsTo("GET", "/api/resource/Customer"))
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "l.csv", b.String()), "--mode", "insert", "--dry-run")
	gets := s.RequestsTo("GET", "/api/resource/Customer")[before:]
	if r.Code != 0 || len(gets) != 3 {
		t.Fatalf("exit %d, %d GETs %s", r.Code, len(gets), r.Stderr)
	}
	for _, g := range gets {
		if n := len(url.QueryEscape(g.Query.Get("filters"))); n > linkURLBudget {
			t.Errorf("filters of %d bytes", n)
		}
	}
}

// Row grouping: an owner or docstatus value starts a document (Frappe
// matches them); a row with values only in ignored or untitled columns is
// a problem, never dropped silently.
func TestImportIgnoredColumnsGrouping(t *testing.T) {
	s := importTSite(t)
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "o.csv", "subject,lines.item,owner\nA,I-A,\n,I-B,x@y\n"), "--mode", "insert")
	if out := importJSON(t, r); r.Code != 0 || out["created"] != float64(2) {
		t.Errorf("owner: exit %d %v", r.Code, out)
	}
	for _, csv := range []string{
		"subject,lines.item,creation\nA,I-A,\n,,2026-01-01\n",
		"subject,,lines.item\nA,,I-A\n,zzz,\n",
		"subject,owner\nA,\n,x@y\n",
	} {
		r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "g.csv", csv), "--mode", "insert")
		if r.Code != exitValidation || !strings.Contains(r.Stderr, "line 3: the row has values only in ignored or untitled columns") {
			t.Errorf("%q: exit %d %s", csv, r.Code, r.Stderr)
		}
	}
}

// Insert: a row with only a row name is no row; a BOM before JSON or
// NDJSON is skipped.
func TestImportInsertNameOnlyRowsAndBOM(t *testing.T) {
	s := importTSite(t)
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "n.csv", "subject,lines.name,lines.item\nA,r1,I-A\n,r2,\n"), "--mode", "insert")
	name := fmt.Sprint(importJSON(t, r)["results"].([]interface{})[0].(map[string]interface{})["name"])
	if d, _ := s.Doc("Ticket", name); r.Code != 0 || len(rowsOf(d["lines"])) != 1 {
		t.Errorf("exit %d %v", r.Code, d["lines"])
	}
	for file, content := range map[string]string{"b.json": "\xef\xbb\xbf[{\"subject\":\"B\"}]", "b.ndjson": "\xef\xbb\xbf{\"subject\":\"B\"}\n"} {
		if r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, file, content), "--mode", "insert"); r.Code != 0 {
			t.Errorf("%s: exit %d %s", file, r.Code, r.Stderr)
		}
	}
}

// A date column whose day and month could be swapped is read day first,
// with a warning.
func TestImportAmbiguousDates(t *testing.T) {
	s := importTSite(t)
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "a.csv", "subject,due\nA,01-02-2026\nB,03-04-2026\n"), "--mode", "insert")
	if r.Code != 0 || !strings.Contains(r.Stderr, `column due: every date also reads as mm-dd-yyyy ("01-02-2026" read as dd-mm-yyyy)`) {
		t.Errorf("exit %d %s", r.Code, r.Stderr)
	}
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "b.csv", "subject,due\nA,01-02-2026\nB,31-01-2026\n"), "--mode", "insert")
	if r.Code != 0 || strings.Contains(r.Stderr, "also reads as") {
		t.Errorf("unambiguous: exit %d %s", r.Code, r.Stderr)
	}
	if w := ambiguousDates([]string{"05-05-2026", "2026-01-01"}, "%d-%m-%Y"); w != "" {
		t.Errorf("same date either way: %q", w)
	}
}
