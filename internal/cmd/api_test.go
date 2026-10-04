package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// getdocHandler answers like frappe.desk.form.load.getdoc: the data sits next
// to "message", at the top level.
var getdocHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Add("Set-Cookie", "sid=secret-session; Path=/")
	fmt.Fprintf(w, `{"docs":[{"name":%q,"amount":1500.0}],"docinfo":{"comments":[]},"message":null}`, r.URL.Query().Get("name"))
})

func TestAPIGetPrintsWholeBody(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("GET /api/method/frappe.desk.form.load.getdoc", getdocHandler)
	r := cmdTOK(t, cmdTRun(t, s, "api", "GET", "/api/method/frappe.desk.form.load.getdoc", "-f", "doctype=ToDo", "-f", "name=TD 1"))
	// A pipe gets the bytes unchanged: 1500.0 keeps its decimal.
	want := `{"docs":[{"name":"TD 1","amount":1500.0}],"docinfo":{"comments":[]},"message":null}`
	if r.Stdout != want {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	req := s.RequestsTo("GET", "/api/method/frappe.desk.form.load.getdoc")
	if len(req) != 1 || req[0].Query.Get("doctype") != "ToDo" || req[0].Header.Get("Authorization") == "" {
		t.Fatalf("requests = %+v", req)
	}
}

func TestAPIFieldsAndMethods(t *testing.T) {
	s := frappetest.New(t)
	s.HandleMethod("my.echo", func(r *http.Request, args map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"verb": r.Method}, nil
	})

	// Fields without a method stay a GET: no accidental writes.
	cmdTOK(t, cmdTRun(t, s, "api", "/api/method/my.echo", "-f", "s=1"))
	if len(s.RequestsTo("GET", "/api/method/my.echo")) != 1 {
		t.Fatalf("requests = %+v", s.Requests())
	}

	// POST: a typed JSON body.
	cmdTOK(t, cmdTRunStdin(t, s, "from stdin", "api", "POST", "/api/method/my.echo",
		"-f", "s=1", "-F", "n=12345678901234567890", "-F", "b=true", "-F", "z=null",
		"-F", `o={"k":[1,2]}`, "-F", "txt=@-"))
	req := s.RequestsTo("POST", "/api/method/my.echo")
	if len(req) != 1 {
		t.Fatalf("requests = %+v", s.Requests())
	}
	want := `{"b":true,"n":12345678901234567890,"o":{"k":[1,2]},"s":"1","txt":"from stdin","z":null}`
	if req[0].Body != want || req[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("body = %s (%s)", req[0].Body, req[0].Header.Get("Content-Type"))
	}

	// GET with typed fields: query parameters, non-strings as JSON; the
	// path's own query is kept.
	cmdTOK(t, cmdTRun(t, s, "api", "GET", "/api/method/my.echo?a=1", "-F", "n=2", "-F", `l=["x"]`))
	req = s.RequestsTo("GET", "/api/method/my.echo")[1:]
	if len(req) != 1 || req[0].Query.Get("a") != "1" || req[0].Query.Get("n") != "2" || req[0].Query.Get("l") != `["x"]` {
		t.Fatalf("query = %v", req[0].Query)
	}

	// --input: the body is the input, fields go to the query, and the
	// default method is POST.
	cmdTOK(t, cmdTRunStdin(t, s, `{"raw":1}`, "api", "/api/method/my.echo", "--input", "-", "-f", "q=v"))
	req = s.RequestsTo("POST", "/api/method/my.echo")[1:]
	if len(req) != 1 || req[0].Body != `{"raw":1}` || req[0].Query.Get("q") != "v" {
		t.Fatalf("--input = %+v", req)
	}

	// A path without a leading slash and a lower-case method work.
	cmdTOK(t, cmdTRun(t, s, "api", "delete", "api/method/my.echo"))
	if len(s.RequestsTo("DELETE", "/api/method/my.echo")) != 1 {
		t.Fatal("DELETE not sent")
	}
}

func TestAPIRefusesOtherHostsAndAuthHeaders(t *testing.T) {
	s := frappetest.New(t)
	for _, args := range [][]string{
		{"api", "https://evil.example/api/method/x"},
		{"api", "//evil.example/api/method/x"},
		{"api", "http:/x"},
		{"api", "/api/method/x", "-H", "Authorization: token a:b"},
		{"api", "/api/method/x", "-H", "cookie: sid=x"},
		{"api", "/api/method/x", "-f", "novalue"},
		{"api", "/api/method/x", "-F", "o={bad"},
		{"api", "GET POST", "/api/method/x"},
	} {
		r := cmdTRun(t, s, args...)
		if r.Code != exitUsage {
			t.Errorf("%v: exit %d (%v), want %d", args, r.Code, r.Err, exitUsage)
		}
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("%d requests sent", n)
	}
}

func TestAPIErrorStatus(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo")
	r := cmdTRun(t, s, "api", "/api/resource/ToDo/missing")
	if r.Code != exitNotFound {
		t.Fatalf("exit %d (%v)", r.Code, r.Err)
	}
	// The error body goes to stdout like any other body.
	if !strings.Contains(r.Stdout, "DoesNotExistError") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	r = cmdTRun(t, s, "api", "/api/resource/ToDo/missing", "--silent")
	if r.Code != exitNotFound || r.Stdout != "" {
		t.Fatalf("--silent: exit %d stdout %q", r.Code, r.Stdout)
	}
}

func TestAPIBinaryDownload(t *testing.T) {
	s := frappetest.New(t)
	pdf := []byte("%PDF-1.4\x00\x01\x02\xff binary")
	s.Handle("GET /private/files/inv.pdf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	}))
	out := filepath.Join(t.TempDir(), "inv.pdf")
	cmdTOK(t, cmdTRun(t, s, "api", "/private/files/inv.pdf", "--output-file", out))
	if got, _ := os.ReadFile(out); string(got) != string(pdf) {
		t.Fatalf("file = %q", got)
	}

	// A pipe gets the bytes; a terminal refuses them.
	if r := cmdTOK(t, cmdTRun(t, s, "api", "/private/files/inv.pdf")); r.Stdout != string(pdf) {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	withTerminalStdout(t)
	r := cmdTRun(t, s, "api", "/private/files/inv.pdf")
	if r.Code != exitUsage || r.Stdout != "" || !strings.Contains(r.Stderr, "--output-file") {
		t.Fatalf("terminal: exit %d stdout %q stderr %q", r.Code, r.Stdout, r.Stderr)
	}
}

func TestAPITerminalFormatting(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("GET /api/method/frappe.desk.form.load.getdoc", getdocHandler)
	s.Handle("GET /page", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<b>hi</b>\x1b]0;title\x07"))
	}))
	withTerminalStdout(t)
	r := cmdTOK(t, cmdTRun(t, s, "api", "GET", "/api/method/frappe.desk.form.load.getdoc", "-f", "name=a"))
	if !strings.Contains(r.Stdout, "\n  \"docs\": [\n") || !strings.Contains(r.Stdout, "1500.0") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "api", "/page"))
	if r.Stdout != "<b>hi</b>]0;title\n" {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestAPIInclude(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("GET /api/method/frappe.desk.form.load.getdoc", getdocHandler)
	r := cmdTOK(t, cmdTRun(t, s, "api", "-i", "/api/method/frappe.desk.form.load.getdoc"))
	if !strings.HasPrefix(r.Stderr, "HTTP/1.1 200 OK\n") || !strings.Contains(r.Stderr, "Content-Type: application/json\n") {
		t.Fatalf("stderr = %q", r.Stderr)
	}
	if strings.Contains(r.Stderr, "secret-session") || !strings.Contains(r.Stderr, "Set-Cookie: sid=(hidden)") {
		t.Fatalf("cookie not hidden: %q", r.Stderr)
	}
}

func TestAPIPaginateResource(t *testing.T) {
	s := frappetest.New(t)
	for i := 0; i < 5; i++ {
		s.Add("ToDo", map[string]interface{}{"name": fmt.Sprintf("t%d", i)})
	}
	r := cmdTOK(t, cmdTRun(t, s, "api", "/api/resource/ToDo?limit=2", "--paginate", "-f", "order_by=name asc"))
	var out struct{ Data []map[string]string }
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || len(out.Data) != 5 || out.Data[4]["name"] != "t4" {
		t.Fatalf("stdout = %s (%v)", r.Stdout, err)
	}
	req := s.RequestsTo("GET", "/api/resource/ToDo")
	if len(req) != 3 || req[2].Query.Get("limit_start") != "4" || req[2].Query.Get("limit_page_length") != "2" || req[2].Query.Get("limit") != "" {
		t.Fatalf("requests = %+v", req)
	}

	for _, args := range [][]string{
		{"api", "/api/method/x", "--paginate"},
		{"api", "POST", "/api/resource/ToDo", "--paginate"},
		{"api", "/api/resource/ToDo?limit=0", "--paginate"},
	} {
		if r := cmdTRun(t, s, args...); r.Code != exitUsage {
			t.Errorf("%v: exit %d (%v)", args, r.Code, r.Err)
		}
	}
}

func TestAPIPaginateV2(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("GET /api/v2/document/Currency", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("start") == "0" {
			fmt.Fprint(w, `{"has_next_page":true,"data":[{"name":"A"},{"name":"B"}]}`)
			return
		}
		fmt.Fprint(w, `{"has_next_page":false,"data":[{"name":"C"}]}`)
	}))
	r := cmdTOK(t, cmdTRun(t, s, "api", "/api/v2/document/Currency", "--paginate"))
	if strings.TrimSpace(r.Stdout) != `{"data":[{"name":"A"},{"name":"B"},{"name":"C"}]}` {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	req := s.RequestsTo("GET", "/api/v2/document/Currency")
	if len(req) != 2 || req[1].Query.Get("start") != "2" || req[0].Query.Get("limit") != "500" {
		t.Fatalf("requests = %+v", req)
	}
}

func TestAPIPasswordSession(t *testing.T) {
	s := frappetest.New(t)
	s.Add("ToDo", map[string]interface{}{"name": "a"})
	r := cmdTOK(t, cmdTExec(t, fakeConfig(t, s, "password"), "", "api", "/api/resource/ToDo/a"))
	if !strings.Contains(r.Stdout, `"name":"a"`) || s.Logins() != 1 || s.Logouts() != 1 {
		t.Fatalf("stdout %q logins %d logouts %d", r.Stdout, s.Logins(), s.Logouts())
	}
}

func TestCallMethodRaw(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("POST /api/method/frappe.desk.form.load.getdoc", getdocHandler)
	r := cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.desk.form.load.getdoc", "--raw"))
	out := cmdTObj(t, r)
	if _, ok := out["docinfo"]; !ok || out["docs"] == nil {
		t.Fatalf("out = %v", out)
	}
	r = cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.desk.form.load.getdoc"))
	if strings.TrimSpace(r.Stdout) != "null" {
		t.Fatalf("without --raw: %q", r.Stdout)
	}
}

func TestMCPCallMethodFullResponse(t *testing.T) {
	srv, site := newMCPFake(t, false)
	site.Handle("POST /api/method/frappe.desk.form.load.getdoc", getdocHandler)
	res := callTool(t, srv, "call_method", map[string]interface{}{"method": "frappe.desk.form.load.getdoc", "full_response": true})
	if txt := resultText(t, res); res.IsError || !strings.Contains(txt, "docinfo") {
		t.Fatalf("result = %s", txt)
	}
}

// withTerminalStdout makes the api command treat stdout as a terminal.
func withTerminalStdout(t *testing.T) {
	t.Helper()
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = prev })
}
