package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestFilePath(t *testing.T) {
	const site = "https://erp.example.com"
	for in, want := range map[string]string{
		"/files/a.png":                             "/files/a.png",
		"files/a.png":                              "/files/a.png",
		"/private/files/contract 1.pdf":            "/private/files/contract%201.pdf",
		"/private/files/contract%201.pdf":          "/private/files/contract%201.pdf",
		"https://erp.example.com/files/a.png":      "/files/a.png",
		"https://ERP.example.com:443/files/a.png":  "/files/a.png",
		"https://erp.example.com./private/files/x": "/private/files/x",
	} {
		got, err := FilePath(site, in)
		if err != nil || got != want {
			t.Errorf("FilePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		"https://evil.example.com/files/a.png":      "credentials must not go to another host",
		"http://erp.example.com/files/a.png":        "credentials must not go to another host", // no downgrade
		"https://erp.example.com:8443/files/a.png":  "credentials must not go to another host",
		"//evil.example.com/files/a.png":            "credentials must not go to another host",
		"https://u:p@erp.example.com/files/a.png":   "credentials must not go to another host",
		"https://erp.example.com.evil.com/files/a":  "credentials must not go to another host",
		"/api/method/frappe.ping":                   "expected /files/",
		"/files/../api/method/x":                    "no . or .. segments",
		"/private/files/%2e%2e/site_config.json":    "no . or .. segments",
		"/files/a.png?x=1":                          "no query",
		"/app/todo":                                 "expected /files/",
		"ftp://erp.example.com/files/a.png":         "credentials must not go to another host",
		"https://erp.example.com/private/files/a#b": "no query or fragment",
	} {
		if _, err := FilePath(site, in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("FilePath(%q) = %v; want an error with %q", in, err, want)
		}
	}
}

func fakeClient(t *testing.T, site *frappetest.Site) *FrappeClient {
	t.Helper()
	c, err := New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	c.r.SetRetryWaitTime(time.Millisecond).SetRetryMaxWaitTime(5 * time.Millisecond)
	return c
}

func TestUploadFile(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	c := fakeClient(t, site)
	ctx := context.Background()

	content := []byte("\x00\x01binary\xff")
	doc, err := c.UploadFile(ctx, FileUpload{Filename: "a.bin", Content: content, Doctype: "ToDo", Docname: "TD-1", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	if doc["file_url"] != "/private/files/a.bin" || doc["attached_to_name"] != "TD-1" || doc["folder"] != DefaultAttachFolder {
		t.Errorf("doc = %v", doc)
	}
	if got, _ := site.File("/private/files/a.bin"); !bytes.Equal(got, content) {
		t.Errorf("stored %q", got)
	}
	// The form as sent: multipart, is_private explicit (Frappe's default is
	// public), the content in the part "file".
	reqs := site.RequestsTo(http.MethodPost, "/api/method/upload_file")
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0].Header.Get("Content-Type"), "multipart/form-data") ||
		!strings.Contains(reqs[0].Body, `name="is_private"`) || !strings.Contains(reqs[0].Body, `name="file"; filename="a.bin"`) {
		t.Fatalf("requests = %+v", reqs)
	}

	// Public, with a field: attached_to_field is set (upload_file does not
	// set the document's field; the CLI does that).
	doc, err = c.UploadFile(ctx, FileUpload{Filename: "b.txt", Content: []byte("b"), Doctype: "ToDo", Docname: "TD-1", Field: "image", Folder: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	if doc["file_url"] != "/files/b.txt" || doc["attached_to_field"] != "image" || doc["folder"] != "Home" {
		t.Errorf("doc = %v", doc)
	}

	// A missing document is refused before the upload: upload_file itself
	// would create a File attached to nothing.
	_, err = c.UploadFile(ctx, FileUpload{Filename: "c.txt", Content: []byte("c"), Doctype: "ToDo", Docname: "nope"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("err = %v", err)
	}
	if n := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file")); n != 2 {
		t.Errorf("%d uploads sent, want 2", n)
	}
}

func TestUploadFileTooLargeAndNotRetried(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	site.SetMaxFileSize(100)
	c := fakeClient(t, site)
	if n := c.MaxFileSize(context.Background()); n != 100 {
		t.Errorf("MaxFileSize = %d", n)
	}
	_, err := c.UploadFile(context.Background(), FileUpload{Filename: "big", Content: make([]byte, 500), Doctype: "ToDo", Docname: "TD-1"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusRequestEntityTooLarge || !strings.Contains(err.Error(), "Max File Size") {
		t.Fatalf("err = %v", err)
	}

	// A 503 on an upload is not retried: a write never is.
	site.Handle("POST /api/method/upload_file", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	if _, err := c.UploadFile(context.Background(), FileUpload{Filename: "x", Content: []byte("x"), Doctype: "ToDo", Docname: "TD-1"}); err == nil {
		t.Fatal("want error")
	}
	if n := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file")); n != 2 {
		t.Errorf("%d upload requests, want 2 (one each, no retry)", n)
	}
}

func TestUploadDryRunHidesContent(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	c := fakeClient(t, site)
	ctx := WithDryRun(context.Background(), DryRunWrites)
	for _, call := range []func(FileUpload) error{
		func(u FileUpload) error { _, err := c.UploadFile(ctx, u); return err },
		func(u FileUpload) error { _, err := c.AttachFile(ctx, u); return err },
	} {
		err := call(FileUpload{Filename: "s.txt", Content: []byte("top-secret-content"), Doctype: "ToDo", Docname: "TD-1", Private: true})
		var plan *DryRunError
		if !errors.As(err, &plan) {
			t.Fatalf("err = %v", err)
		}
		b := planBodyText(plan)
		if strings.Contains(b, "top-secret") || strings.Contains(b, "dG9wLXNlY3JldC") || !strings.Contains(b, "s.txt, 18 bytes") {
			t.Errorf("plan body = %s", b)
		}
	}
	if n := len(site.RequestsTo(http.MethodPost, "/api/method/upload_file")) + len(site.RequestsTo(http.MethodPost, "/api/method/frappe.client.attach_file")); n != 0 {
		t.Errorf("%d writes sent in a dry run", n)
	}
}

func planBodyText(p *DryRunError) string {
	b, _ := json.Marshal(p.Requests)
	return string(b)
}

func TestAttachFile(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	c := fakeClient(t, site)
	content := []byte("\xff\xfe binary")
	doc, err := c.AttachFile(context.Background(), FileUpload{Filename: "x.bin", Content: content, Doctype: "ToDo", Docname: "TD-1", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := site.File(doc["file_url"].(string)); !bytes.Equal(got, content) || doc["file_url"] != "/private/files/x.bin" {
		t.Errorf("doc %v, stored %q", doc, got)
	}
	reqs := site.RequestsTo(http.MethodPost, "/api/method/frappe.client.attach_file")
	if len(reqs) != 1 || !strings.Contains(reqs[0].Body, `"decode_base64":1`) || !strings.Contains(reqs[0].Body, `"is_private":1`) {
		t.Errorf("request = %+v", reqs)
	}
	if _, err := c.AttachFile(context.Background(), FileUpload{Filename: "x", Content: []byte("x"), Doctype: "ToDo", Docname: "nope"}); err == nil {
		t.Error("attach to a missing document: want error")
	}
}

func TestAttachments(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"}, map[string]interface{}{"name": "TD-2"})
	c := fakeClient(t, site)
	ctx := context.Background()
	for _, n := range []string{"TD-1", "TD-2", "TD-1"} {
		if _, err := c.UploadFile(ctx, FileUpload{Filename: n + ".txt", Content: []byte(n + time.Now().String()), Doctype: "ToDo", Docname: n}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := c.Attachments(ctx, "ToDo", "TD-1", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["file_name"] != "TD-1.txt" || rows[0]["file_url"] == nil {
		t.Errorf("rows = %v", rows)
	}
}

func TestDownloadRetries(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			// Frappe v16's concurrency limit: 503 with Retry-After.
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"exc_type":"ServiceUnavailableError"}`)
		default:
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, "%PDF-1.4 ok")
		}
	}))
	defer srv.Close()
	c := newKeyClient(t, srv.URL)
	resp, err := c.PDF(context.Background(), "ToDo", "TD-1", PrintOptions{Format: "F", NoLetterhead: true, Language: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.Status != 200 || string(body) != "%PDF-1.4 ok" || !IsPDF(resp.Header) || n.Load() != 2 {
		t.Errorf("status %d body %q requests %d", resp.Status, body, n.Load())
	}

	// A long Retry-After, a 404 and a timeout are not retried.
	for name, h := range map[string]http.HandlerFunc{
		"retry-after 120": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusServiceUnavailable)
		},
		"404": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
	} {
		var m atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.Add(1); h(w, r) }))
		c := newKeyClient(t, srv.URL)
		resp, err := c.Download(context.Background(), "/files/x", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if m.Load() != 1 {
			t.Errorf("%s: %d requests, want 1", name, m.Load())
		}
		srv.Close()
	}

	// Always 503: three attempts, the last response returned.
	var k atomic.Int32
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv3.Close()
	c = newKeyClient(t, srv3.URL)
	resp, err = c.Download(context.Background(), "/files/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Status != http.StatusServiceUnavailable || k.Load() != 3 {
		t.Errorf("status %d after %d requests, want 503 after 3", resp.Status, k.Load())
	}

	// A timeout is not retried: one request.
	var slow atomic.Int32
	release := make(chan struct{})
	srvSlow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slow.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srvSlow.Close()
	defer close(release)
	c = newKeyClient(t, srvSlow.URL)
	c.raw.SetTimeout(100 * time.Millisecond)
	if resp, err := c.Download(context.Background(), "/files/x", nil); err == nil {
		_ = resp.Body.Close()
		t.Error("timeout: no error")
	}
	if slow.Load() != 1 {
		t.Errorf("timeout: %d requests, want 1", slow.Load())
	}

	// A connection reset is retried: three requests.
	var reset atomic.Int32
	srvReset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reset.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetLinger(0) // close with RST
		}
		_ = conn.Close()
	}))
	defer srvReset.Close()
	c = newKeyClient(t, srvReset.URL)
	if resp, err := c.Download(context.Background(), "/files/x", nil); err == nil {
		_ = resp.Body.Close()
		t.Error("reset: no error")
	}
	if reset.Load() != 3 {
		t.Errorf("reset: %d requests, want 3", reset.Load())
	}

	// Every retried response's body is closed, and the last one is the
	// caller's to close.
	var opened, closed atomic.Int32
	c = newKeyClient(t, srv3.URL)
	c.raw.SetTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err == nil {
			opened.Add(1)
			resp.Body = &closeCounter{ReadCloser: resp.Body, n: &closed}
		}
		return resp, err
	}))
	resp, err = c.Download(context.Background(), "/files/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 3 || closed.Load() != 2 {
		t.Errorf("before Close: %d bodies opened, %d closed; want 3 and 2", opened.Load(), closed.Load())
	}
	_ = resp.Body.Close()
	if closed.Load() != 3 {
		t.Errorf("after Close: %d of 3 bodies closed", closed.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeCounter struct {
	io.ReadCloser
	n *atomic.Int32
}

func (b *closeCounter) Close() error {
	b.n.Add(1)
	return b.ReadCloser.Close()
}

// The client timeout bounds the whole download, not each attempt.
func TestDownloadTimeoutBoundsAllAttempts(t *testing.T) {
	// A Retry-After that does not fit in what is left: the 503 is returned
	// at once instead of a wait the deadline would cut.
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newKeyClient(t, srv.URL)
	c.raw.SetTimeout(time.Second)
	start := time.Now()
	resp, err := c.Download(context.Background(), "/files/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Status != http.StatusServiceUnavailable || n.Load() != 1 || time.Since(start) > 900*time.Millisecond {
		t.Errorf("status %d after %d requests in %s", resp.Status, n.Load(), time.Since(start))
	}

	// Slow 503s: each attempt fits in the timeout, all of them do not.
	var m atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		m.Add(1)
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	c = newKeyClient(t, slow.URL)
	c.r.SetRetryCount(20)
	c.raw.SetTimeout(400 * time.Millisecond)
	start = time.Now()
	resp, err = c.Download(context.Background(), "/files/x", nil)
	if err == nil {
		_ = resp.Body.Close()
	}
	if el := time.Since(start); el > 700*time.Millisecond || m.Load() > 3 {
		t.Errorf("took %s and %d requests with a 400ms timeout", el, m.Load())
	}
}

func TestDownloadAuth(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	c := fakeClient(t, site)
	ctx := context.Background()
	if _, err := c.UploadFile(ctx, FileUpload{Filename: "p.txt", Content: []byte("private"), Doctype: "ToDo", Docname: "TD-1", Private: true}); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Download(ctx, "/private/files/p.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Status != 200 || string(b) != "private" {
		t.Errorf("status %d body %q", resp.Status, b)
	}
	if got := site.RequestsTo(http.MethodGet, "/private/files/p.txt"); len(got) != 1 || got[0].Header.Get("Authorization") == "" {
		t.Errorf("requests = %+v", got)
	}
	// Without credentials the site refuses it.
	r, err := http.Get(site.URL + "/private/files/p.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("anonymous: %d", r.StatusCode)
	}
	// A session site downloads with its cookie.
	sc, err := New(ctx, &config.SiteConfig{URL: site.URL, Username: frappetest.Username, Password: frappetest.Password})
	if err != nil {
		t.Fatal(err)
	}
	defer sc.CloseQuietly()
	resp, err = sc.Download(ctx, "/private/files/p.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Status != 200 {
		t.Errorf("session download: %d", resp.Status)
	}
}

func TestPrintHTML(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	c := fakeClient(t, site)
	res, err := c.PrintHTML(context.Background(), "ToDo", "TD-1", PrintOptions{Format: "Standard", NoLetterhead: true, Language: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := res["html"].(string); !strings.Contains(h, "TD-1") {
		t.Errorf("res = %v", res)
	}
	reqs := site.RequestsTo(http.MethodGet, "/api/method/frappe.www.printview.get_html_and_style")
	if len(reqs) != 1 || reqs[0].Query.Get("doc") != "ToDo" || reqs[0].Query.Get("print_format") != "Standard" ||
		reqs[0].Query.Get("no_letterhead") != "1" || reqs[0].Query.Get("_lang") != "fr" {
		t.Errorf("requests = %+v", reqs)
	}
	if _, err := c.PrintHTML(context.Background(), "ToDo", "nope", PrintOptions{}); err == nil {
		t.Error("missing document: want error")
	}
}

func TestDebugNeverTracesUploadContent(t *testing.T) {
	var buf bytes.Buffer
	oldOut, oldLevel := debugOut, Debug
	debugOut = func() io.Writer { return &buf }
	Debug = DebugBody
	defer func() { debugOut, Debug = oldOut, oldLevel }()
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	c := fakeClient(t, site)
	if _, err := c.UploadFile(context.Background(), FileUpload{Filename: "s.txt", Content: []byte("upload-secret-text"), Doctype: "ToDo", Docname: "TD-1"}); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "upload-secret-text") || !strings.Contains(out, "multipart upload") {
		t.Errorf("trace:\n%s", out)
	}
}
