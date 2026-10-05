package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestMCPAttachFilePolicy(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("\x00attached\xff"))
	args := map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "a.bin", "data": data}

	// File is sensitive: attach_file writes it, so the defaults refuse it,
	// and a flag cannot allow it.
	for _, p := range []struct {
		cfg   *config.MCPPolicy
		flags config.MCPPolicy
		want  string
	}{
		{nil, config.MCPPolicy{}, `DocType "File" is sensitive`},
		{nil, config.MCPPolicy{AllowDoctypes: []string{"ToDo", "File"}}, `DocType "File" is sensitive`},
		{&config.MCPPolicy{AllowDoctypes: []string{"File"}}, config.MCPPolicy{}, `DocType "ToDo" is not in sites.prod.mcp.allow_doctypes`},
		{&config.MCPPolicy{AllowDoctypes: []string{"ToDo", "File"}, DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, `DocType "ToDo" is denied`},
	} {
		s, site, _, _ := mcpTPolicy(t, p.cfg, p.flags)
		mcpTErr(t, s, "attach_file", args, p.want)
		if n := len(site.Requests()); n != 0 {
			t.Errorf("%v: %d requests sent", p, n)
		}
	}

	s, site, _, audit := mcpTPolicy(t, &config.MCPPolicy{AllowDoctypes: []string{"ToDo", "File"}}, config.MCPPolicy{})
	doc := mcpTObj(t, mcpTOK(t, s, "attach_file", args))
	if doc["file_url"] != "/private/files/a.bin" || doc["attached_to_name"] != "TD-1" {
		t.Errorf("doc = %v", doc)
	}
	if got, _ := site.File("/private/files/a.bin"); string(got) != "\x00attached\xff" {
		t.Errorf("stored %q", got)
	}
	// Text, public, in a folder.
	doc = mcpTObj(t, mcpTOK(t, s, "attach_file", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "n.txt",
		"data": "plain note", "encoding": "text", "is_private": false, "folder": "Home"}))
	if got, _ := site.File("/files/n.txt"); string(got) != "plain note" || doc["folder"] != "Home" {
		t.Errorf("doc %v, stored %q", doc, got)
	}
	// With field, the document's field is set to the file URL.
	doc = mcpTObj(t, mcpTOK(t, s, "attach_file", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "f.txt",
		"data": "in a field", "encoding": "text", "field": "description"}))
	if td, _ := site.Doc("ToDo", "TD-1"); doc["attached_to_field"] != "description" || td["description"] != doc["file_url"] {
		t.Errorf("doc %v, ToDo description %v", doc, td["description"])
	}
	if last := site.Requests()[len(site.Requests())-1]; !strings.Contains(last.Body, `"docfield":"description"`) {
		t.Errorf("attach_file body %s", last.Body)
	}
	// The audit line keeps the content's size, never the content.
	raw, err := os.ReadFile(audit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), data) || strings.Contains(string(raw), "plain note") {
		t.Errorf("audit log has the file content:\n%s", raw)
	}

	// Invalid input and oversized files are refused before any request.
	before := len(site.Requests())
	big := base64.StdEncoding.EncodeToString(make([]byte, maxAttachBytes+1))
	for _, c := range []struct {
		args map[string]interface{}
		want string
	}{
		{map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "x", "data": "%%%"}, "not valid base64"},
		{map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "x", "data": big}, "5 MiB"},
		{map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "x", "data": strings.Repeat("a", maxAttachBytes+1), "encoding": "text"}, "over the 5 MiB limit"},
		{map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "../x", "data": "eA=="}, "no path separators"},
		{map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "x", "data": "eA==", "encoding": "hex"}, "expected base64 or text"},
	} {
		mcpTErr(t, s, "attach_file", c.args, c.want)
	}
	if n := len(site.Requests()) - before; n != 0 {
		t.Errorf("%d requests sent for invalid calls", n)
	}
	// A missing document is the site's 404.
	mcpTErr(t, s, "attach_file", map[string]interface{}{"doctype": "ToDo", "name": "nope", "filename": "x", "data": "eA=="}, "not found")
}

func TestMCPListAttachments(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{AllowDoctypes: []string{"ToDo", "File"}}, config.MCPPolicy{})
	mcpTOK(t, s, "attach_file", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "filename": "a.txt", "data": "a", "encoding": "text"})
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(mcpTOK(t, s, "list_attachments", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["file_url"] != "/private/files/a.txt" {
		t.Errorf("rows = %v", rows)
	}
	q := site.RequestsTo(http.MethodGet, "/api/resource/File")[0].Query
	if !strings.Contains(q.Get("filters"), `"attached_to_name","=","TD-1"`) || q.Get("limit_page_length") != "100" {
		t.Errorf("query = %v", q)
	}
	// It reads File: the DocType rules for File apply.
	s, _, _, _ = mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"File"}}, config.MCPPolicy{})
	mcpTErr(t, s, "list_attachments", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, `DocType "File" is denied`)
	// Read-only servers keep the read tools of the set.
	s, _, _, _ = mcpTPolicy(t, &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{})
	names := mcpTToolNames(t, s)
	if !contains(names, "list_attachments", false) || !contains(names, "get_print_html", false) || contains(names, "attach_file", false) {
		t.Errorf("read-only tools = %v", names)
	}
}

func TestMCPGetPrintHTML(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	site.SetPrintHTML(`<style>.x{color:red}</style><div class="print-format"><h2>ToDo</h2><table><tr><td>Total</td><td>1&nbsp;500.00</td></tr></table><p>Thanks<br>Bye</p></div>`, ".print-format{}")
	res := mcpTObj(t, mcpTOK(t, s, "get_print_html", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "print_format": "Standard", "no_letterhead": true}))
	if !strings.Contains(res["html"].(string), "<h2>ToDo</h2>") || res["style"] != ".print-format{}" {
		t.Errorf("res = %v", res)
	}
	q := site.RequestsTo(http.MethodGet, "/api/method/frappe.www.printview.get_html_and_style")[0].Query
	if q.Get("print_format") != "Standard" || q.Get("no_letterhead") != "1" {
		t.Errorf("query = %v", q)
	}
	res = mcpTObj(t, mcpTOK(t, s, "get_print_html", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "text_only": true}))
	if res["text"] != "ToDo\nTotal 1 500.00\nThanks\nBye" || res["html"] != nil || res["style"] != nil {
		t.Errorf("text = %q (%v)", res["text"], res)
	}
	mcpTErr(t, s, "get_print_html", map[string]interface{}{"doctype": "ToDo", "name": "nope"}, "not found")

	// Too large: the style goes first, then the HTML is cut to fit.
	site.SetPrintHTML("<p>"+strings.Repeat("é<b>x</b>", 60000)+"</p>", strings.Repeat("s", 100000))
	text := mcpTOK(t, s, "get_print_html", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
	res = mcpTObj(t, text)
	if len(text) > maxToolResultBytes || res["truncated"] != true || res["style_omitted"] != true || res["style"] != nil {
		t.Errorf("%d bytes, truncated %v, style_omitted %v", len(text), res["truncated"], res["style_omitted"])
	}
	if h := res["html"].(string); len(h) < 100<<10 || !strings.HasPrefix(h, "<p>é") {
		t.Errorf("html cut to %d bytes", len(h))
	}
	// The style alone fits once the HTML is small enough.
	site.SetPrintHTML("<p>"+strings.Repeat("x", 400<<10)+"</p>", strings.Repeat("s", 200<<10))
	res = mcpTObj(t, mcpTOK(t, s, "get_print_html", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}))
	if res["style_omitted"] != true || res["truncated"] != nil {
		t.Errorf("res keys: style_omitted %v truncated %v", res["style_omitted"], res["truncated"])
	}
}

func TestHTMLText(t *testing.T) {
	for in, want := range map[string]string{
		"<p>a</p><p>b</p>": "a\nb",
		"<script>alert(1)</script><div>x &amp; y</div>":       "x & y",
		"<head><title>t</title></head><body>  a   b  </body>": "a b",
		"<p>a</p>\n\n\n<p>b</p>":                              "a\nb",
	} {
		if got := htmlText(in); got != want {
			t.Errorf("htmlText(%q) = %q, want %q", in, got, want)
		}
	}
}
