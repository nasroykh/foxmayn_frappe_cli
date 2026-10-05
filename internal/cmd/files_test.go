package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestUploadCommand(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	dir := t.TempDir()
	src := filepath.Join(dir, "report.bin")
	content := []byte("\x00\x01 report \xff")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}

	// Private by default, attached in Home/Attachments.
	r := runFFC(t, cfg, "", "upload", src, "-d", "ToDo", "-n", "TD-1", "--json")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	doc := mcpTObj(t, r.Stdout)
	if doc["file_url"] != "/private/files/report.bin" || doc["is_private"] != float64(1) || doc["folder"] != "Home/Attachments" {
		t.Errorf("doc = %v", doc)
	}
	if got, _ := site.File("/private/files/report.bin"); !bytes.Equal(got, content) {
		t.Errorf("stored %q", got)
	}

	// --public, stdin with --filename, --field sets the document's field.
	r = runFFC(t, cfg, "hello", "upload", "-", "--filename", "hello.txt", "-d", "ToDo", "-n", "TD-2", "--public", "--field", "image", "--json")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if doc := mcpTObj(t, r.Stdout); doc["file_url"] != "/files/hello.txt" || doc["attached_to_field"] != "image" {
		t.Errorf("doc = %v", doc)
	}
	if d, _ := site.Doc("ToDo", "TD-2"); d["image"] != "/files/hello.txt" {
		t.Errorf("TD-2.image = %v", d["image"])
	}

	// Usage errors and refusals send nothing.
	before := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file"))
	for _, c := range []struct {
		stdin string
		args  []string
		code  int
		want  string
	}{
		{"x", []string{"upload", "-", "-d", "ToDo", "-n", "TD-1"}, exitUsage, "--filename"},
		{"", []string{"upload", src, "-d", "ToDo", "-n", "TD-1", "--filename", "a/b"}, exitUsage, "no path separators"},
		{"", []string{"upload", dir, "-d", "ToDo", "-n", "TD-1"}, exitUsage, "is a directory"},
		{"", []string{"upload", src, "-d", "ToDo", "-n", "missing"}, exitNotFound, "not found"},
		{"", []string{"upload", src, "-d", "ToDo"}, exitUsage, `"name" not set`},
	} {
		r := runFFC(t, cfg, c.stdin, c.args...)
		if r.Code != c.code || !strings.Contains(r.Stderr, c.want) {
			t.Errorf("%v: exit %d, stderr %q; want %d and %q", c.args, r.Code, r.Stderr, c.code, c.want)
		}
	}
	if n := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file")); n != before {
		t.Errorf("%d uploads sent by refused commands", n-before)
	}
	if n := site.Count("File"); n != 2 {
		t.Errorf("%d File documents, want 2", n)
	}
}

func TestUploadSizeLimit(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	site.SetMaxFileSize(10)
	src := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 11), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, stdin := range []bool{false, true} {
		args := []string{"upload", src, "-d", "ToDo", "-n", "TD-1"}
		in := ""
		if stdin {
			args, in = []string{"upload", "-", "--filename", "big.txt", "-d", "ToDo", "-n", "TD-1"}, strings.Repeat("x", 11)
		}
		r := runFFC(t, cfg, in, args...)
		if r.Code != exitValidation || !strings.Contains(r.Stderr, "over the site's upload limit") {
			t.Errorf("stdin %v: exit %d: %s", stdin, r.Code, r.Stderr)
		}
	}
	if n := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file")); n != 0 {
		t.Errorf("%d uploads sent", n)
	}

	// A site that does not report its limit answers 413 itself.
	site.Handle("GET /api/method/frappe.core.api.file.get_max_file_size", frappetest.ErrorHandler(frappetest.NotFound("no such method")))
	r := runFFC(t, cfg, "", "upload", src, "-d", "ToDo", "-n", "TD-1")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "413") {
		t.Errorf("413: exit %d: %s", r.Code, r.Stderr)
	}
}

func TestUploadDryRun(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	src := filepath.Join(t.TempDir(), "s.txt")
	if err := os.WriteFile(src, []byte("dry-run-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runFFC(t, cfg, "", "upload", src, "-d", "ToDo", "-n", "TD-1", "--dry-run", "--json")
	if r.Code != 0 || strings.Contains(r.Stdout, "dry-run-secret") || !strings.Contains(r.Stdout, "s.txt, 14 bytes, not shown") ||
		!strings.Contains(r.Stdout, `"is_private": 1`) {
		t.Errorf("exit %d: %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if n := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file")); n != 0 || site.Count("File") != 0 {
		t.Errorf("dry run sent %d uploads", n)
	}
}

func TestDownloadCommand(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "password")
	dir := t.TempDir()
	content := []byte("\x00private\xff")
	src := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := runFFC(t, cfg, "", "upload", src, "-d", "ToDo", "-n", "TD-1"); r.Code != 0 {
		t.Fatalf("upload: %s", r.Stderr)
	}

	out := filepath.Join(dir, "out.bin")
	r := runFFC(t, cfg, "", "download", "/private/files/in.bin", "-o", out)
	got, _ := os.ReadFile(out)
	if r.Code != 0 || !bytes.Equal(got, content) {
		t.Fatalf("exit %d %s; got %q", r.Code, r.Stderr, got)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("private file saved %v", fi.Mode().Perm())
	}
	// An existing file is kept without --force, replaced with it.
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = runFFC(t, cfg, "", "download", "/private/files/in.bin", "-o", out)
	if got, _ := os.ReadFile(out); r.Code != exitUsage || string(got) != "old" || !strings.Contains(r.Stderr, "--force") {
		t.Errorf("no --force: exit %d %s, file %q", r.Code, r.Stderr, got)
	}
	r = runFFC(t, cfg, "", "download", site.URL+"/private/files/in.bin", "-o", out, "--force", "--json")
	if got, _ := os.ReadFile(out); r.Code != 0 || !bytes.Equal(got, content) || !strings.Contains(r.Stdout, `"bytes": 9`) {
		t.Errorf("--force with a site URL: exit %d %s %s, file %q", r.Code, r.Stdout, r.Stderr, got)
	}

	// To stdout: the bytes unchanged on a pipe, refused on a terminal.
	r = runFFC(t, cfg, "", "download", "/private/files/in.bin", "-o", "-")
	if r.Code != 0 || r.Stdout != string(content) {
		t.Errorf("stdout: exit %d %q", r.Code, r.Stdout)
	}
	old := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	r = runFFC(t, cfg, "", "download", "/private/files/in.bin", "-o", "-")
	stdoutIsTerminal = old
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "binary response") {
		t.Errorf("terminal: exit %d %s", r.Code, r.Stderr)
	}

	// The default name is the file's own, in the current directory.
	t.Chdir(dir)
	if r := runFFC(t, cfg, "", "download", "/private/files/in.bin"); r.Code != exitUsage {
		t.Errorf("default name over the existing in.bin: exit %d %s", r.Code, r.Stderr)
	}
	if err := os.Remove(filepath.Join(dir, "in.bin")); err != nil {
		t.Fatal(err)
	}
	if r := runFFC(t, cfg, "", "download", "/private/files/in.bin"); r.Code != 0 {
		t.Errorf("default name: exit %d %s", r.Code, r.Stderr)
	}

	// A public file gets the mode of any new file (0666 less the umask).
	pub := filepath.Join(dir, "pub.txt")
	if err := os.WriteFile(pub, []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := runFFC(t, cfg, "", "upload", pub, "-d", "ToDo", "-n", "TD-1", "--public"); r.Code != 0 {
		t.Fatalf("upload --public: %s", r.Stderr)
	}
	ref, err := os.OpenFile(filepath.Join(dir, "ref"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	_ = ref.Close()
	want, _ := os.Stat(ref.Name())
	pubOut := filepath.Join(dir, "pub-out.txt")
	if r := runFFC(t, cfg, "", "download", "/files/pub.txt", "-o", pubOut); r.Code != 0 {
		t.Fatalf("public download: %s", r.Stderr)
	}
	if fi, _ := os.Stat(pubOut); fi.Mode().Perm() != want.Mode().Perm() {
		t.Errorf("public file saved %v, want %v", fi.Mode().Perm(), want.Mode().Perm())
	}

	// A missing private file is Frappe's 403 page: no file is written.
	missing := filepath.Join(dir, "missing.bin")
	r = runFFC(t, cfg, "", "download", "/private/files/nope.bin", "-o", missing)
	if _, err := os.Stat(missing); r.Code != exitPermission || !strings.Contains(r.Stderr, "does not exist or your user may not read it") || err == nil {
		t.Errorf("missing: exit %d %s (stat %v)", r.Code, r.Stderr, err)
	}
	r = runFFC(t, cfg, "", "download", "/files/nope.bin", "-o", missing)
	if r.Code != exitNotFound {
		t.Errorf("missing public file: exit %d %s", r.Code, r.Stderr)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ffc-download-") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
}

func TestDownloadRefusesOtherHosts(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	other := frappetest.New(t)
	for _, u := range []string{
		other.URL + "/private/files/x",
		"//evil.example/files/x",
		strings.Replace(site.URL, "http://", "https://", 1) + "/files/x",
		"/api/method/frappe.auth.get_logged_user",
		"/files/../api/method/x",
	} {
		r := runFFC(t, cfg, "", "download", u, "-o", filepath.Join(t.TempDir(), "x"))
		if r.Code != exitUsage {
			t.Errorf("%s: exit %d %s", u, r.Code, r.Stderr)
		}
	}
	if n := len(site.Requests()) + len(other.Requests()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestAttachmentsCommand(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	for i, n := range []string{"TD-1", "TD-2", "TD-1"} {
		r := runFFC(t, cfg, strings.Repeat("x", i+1), "upload", "-", "--filename", n+"-"+string(rune('a'+i))+".txt", "-d", "ToDo", "-n", n)
		if r.Code != 0 {
			t.Fatal(r.Stderr)
		}
	}
	r := runFFC(t, cfg, "", "attachments", "-d", "ToDo", "-n", "TD-1", "--json")
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &rows); err != nil || r.Code != 0 {
		t.Fatalf("exit %d %v: %s %s", r.Code, err, r.Stdout, r.Stderr)
	}
	if len(rows) != 2 || rows[0]["file_name"] != "TD-1-a.txt" || rows[1]["file_url"] != "/private/files/TD-1-c.txt" {
		t.Errorf("rows = %v", rows)
	}
	r = runFFC(t, cfg, "", "attachments", "-d", "ToDo", "-n", "TD-1")
	if r.Code != 0 || !strings.Contains(r.Stdout, "TD-1-c.txt") || strings.Contains(r.Stdout, "TD-2") {
		t.Errorf("table: %s", r.Stdout)
	}
}

func TestPDFCommand(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	dir := t.TempDir()
	out := filepath.Join(dir, "td.pdf")
	r := runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "TD-1", "--format", "Standard", "--no-letterhead", "--lang", "fr", "-o", out)
	got, _ := os.ReadFile(out)
	if r.Code != 0 || !bytes.HasPrefix(got, []byte("%PDF-")) {
		t.Fatalf("exit %d %s: %q", r.Code, r.Stderr, got)
	}
	q := site.RequestsTo(http.MethodGet, "/api/method/frappe.utils.print_format.download_pdf")[0].Query
	if q.Get("format") != "Standard" || q.Get("no_letterhead") != "1" || q.Get("language") != "fr" || q.Get("letterhead") != "" {
		t.Errorf("query = %v", q)
	}

	// The default file is <name>.pdf.
	t.Chdir(dir)
	if r := runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "TD-2"); r.Code != 0 {
		t.Errorf("default name: exit %d %s", r.Code, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "TD-2.pdf")); err != nil {
		t.Error(err)
	}

	// --letterhead and --no-letterhead exclude each other.
	if r := runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "TD-1", "--letterhead", "L", "--no-letterhead", "-o", out, "--force"); r.Code != exitUsage {
		t.Errorf("both letterhead flags: exit %d", r.Code)
	}
	// A missing document: the JSON error, no file.
	if r := runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "nope", "-o", filepath.Join(dir, "nope.pdf")); r.Code != exitNotFound {
		t.Errorf("missing: exit %d %s", r.Code, r.Stderr)
	}

	// A 200 that is not a PDF (an HTML page) is never saved.
	html := filepath.Join(dir, "html.pdf")
	site.Handle("GET /api/method/frappe.utils.print_format.download_pdf", frappetest.HTMLPage(http.StatusOK))
	r = runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "TD-1", "-o", html)
	if _, err := os.Stat(html); r.Code == 0 || err == nil || !strings.Contains(r.Stderr, "not a PDF") {
		t.Errorf("HTML page: exit %d %s (stat %v)", r.Code, r.Stderr, err)
	}
	// Labelled PDF but not one.
	site.Handle("GET /api/method/frappe.utils.print_format.download_pdf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, "<html>")
	}))
	if r := runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "TD-1", "-o", html); r.Code == 0 {
		t.Errorf("fake PDF saved: %s", r.Stderr)
	}

	// v16's concurrency limit: 503 with Retry-After, retried.
	var n atomic.Int32
	site.Handle("GET /api/method/frappe.utils.print_format.download_pdf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"exc_type":"ServiceUnavailableError","exception":"Server is busy. Please try again in a few seconds."}`)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, "%PDF-1.7 retried")
	}))
	busy := filepath.Join(dir, "busy.pdf")
	r = runFFC(t, cfg, "", "pdf", "-d", "ToDo", "-n", "TD-1", "-o", busy)
	if got, _ := os.ReadFile(busy); r.Code != 0 || n.Load() != 2 || string(got) != "%PDF-1.7 retried" {
		t.Errorf("503 retry: exit %d %s, %d requests, %q", r.Code, r.Stderr, n.Load(), got)
	}
}

func TestPDFFileName(t *testing.T) {
	for in, want := range map[string]string{
		"SINV-0001":               "SINV-0001.pdf",
		"A B/C\\D":                "A-B-C-D.pdf",
		"x:y*z?\"<>|":             "x-y-z-----.pdf",
		"evil\u202egpj.exe":       "evilgpj.exe.pdf",
		"line\nbreak\ttab\x1b[2J": "line-break-tab[2J.pdf",
		"..":                      "document.pdf",
		"\u200b":                  "document.pdf",
		"":                        "document.pdf",
	} {
		if got := pdfFileName(in); got != want {
			t.Errorf("pdfFileName(%q) = %q, want %q", in, got, want)
		}
		if err := checkFileName(pdfFileName(in)); err != nil {
			t.Errorf("pdfFileName(%q) fails checkFileName: %v", in, err)
		}
	}
	for _, bad := range []string{"a\nb", "a\tb", "a\u202eb", "a/b", ".."} {
		if checkFileName(bad) == nil {
			t.Errorf("checkFileName(%q) accepted", bad)
		}
	}
}
