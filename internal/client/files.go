package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// Files: upload (multipart upload_file), attach (frappe.client.attach_file,
// base64), the File rows attached to a document, downloads of /files and
// /private/files, and print output (PDF, HTML).

// DefaultAttachFolder is the File folder the desk files attachments in.
const DefaultAttachFolder = "Home/Attachments"

// FileUpload is a file to attach to a document.
type FileUpload struct {
	Filename string
	Content  []byte
	Doctype  string
	Docname  string
	Field    string // Attach field to set to the file URL; "" for none
	Folder   string // "" is DefaultAttachFolder
	Private  bool
}

func (u FileUpload) folder() string {
	if u.Folder == "" {
		return DefaultAttachFolder
	}
	return u.Folder
}

// planFields describes an upload in a dry-run plan: the form fields and the
// file's name, size and type, never its content.
func (u FileUpload) planFields(contentField string) map[string]interface{} {
	m := map[string]interface{}{
		"doctype":    u.Doctype,
		"docname":    u.Docname,
		"folder":     u.folder(),
		"is_private": boolInt(u.Private),
		contentField: fmt.Sprintf("(%s, %d bytes, not shown)", u.Filename, len(u.Content)),
	}
	if u.Field != "" {
		m["fieldname"] = u.Field
	}
	return m
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// contentType is the MIME type of a file name, for the multipart part.
// Frappe guesses the type from the name itself; this only labels the part.
func contentType(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// MaxFileSize returns the largest upload the site accepts, in bytes
// (frappe.core.api.file.get_max_file_size: System Settings max_file_size,
// else the site config's, else 25 MiB). It is 0 when the site does not say
// (older versions without the method): the server still enforces its limit.
func (c *FrappeClient) MaxFileSize(ctx context.Context) int64 {
	var res struct {
		Message json.Number `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/method/frappe.core.api.file.get_max_file_size", nil, nil, nil, &res); err != nil {
		return 0
	}
	n, err := res.Message.Int64()
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// FileTooLarge is the error for an upload over the site's limit, found
// before anything is sent.
func FileTooLarge(name string, n, limit int64) error {
	return &StateError{Message: fmt.Sprintf("%s is %s, over the site's upload limit of %s (System Settings > Max File Size)",
		name, size(n), size(limit))}
}

// fileHints are the hints for upload and attach errors.
func fileHints(doctype, name string) map[int]string {
	h := docHints(doctype, name, "write")
	h[http.StatusRequestEntityTooLarge] = "file larger than the site accepts (413): the limit is System Settings > Max File Size"
	return h
}

// UploadFile attaches u to its document through POST
// /api/method/upload_file (multipart, the field "file"), as the desk does,
// and returns the File document. Like every write it is never retried. The
// request is repeated only after a session re-login or token refresh, when
// the site refused it before it ran; the form is rebuilt from u.Content
// then, so it is sent intact.
func (c *FrappeClient) UploadFile(ctx context.Context, u FileUpload) (map[string]interface{}, error) {
	form := map[string]string{
		"doctype":    u.Doctype,
		"docname":    u.Docname,
		"folder":     u.folder(),
		"is_private": strconv.Itoa(boolInt(u.Private)), // Frappe's default is public
	}
	if u.Field != "" {
		form["fieldname"] = u.Field
	}
	// upload_file accepts a document that does not exist (the desk uploads
	// to unsaved forms) and creates a File attached to nothing: check first.
	if _, err := c.GetDoc(ctx, u.Doctype, u.Docname); err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, c.r, http.MethodPost, "/api/method/upload_file", func(r *resty.Request) {
		r.SetMultipartFormData(form)
		r.SetMultipartField("file", u.Filename, contentType(u.Filename), bytes.NewReader(u.Content))
	})
	if err != nil {
		var plan *DryRunError
		if errors.As(err, &plan) && len(plan.Requests) == 1 {
			plan.Requests[0].Body = u.planFields("file")
		}
		return nil, err
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, fileHints(u.Doctype, u.Docname))
	}
	var res struct {
		Message map[string]interface{} `json:"message"`
	}
	if err := decodeJSON(resp, &res); err != nil {
		return nil, err
	}
	if res.Message == nil {
		return nil, fmt.Errorf("unexpected response: upload_file returned no File document")
	}
	return res.Message, nil
}

// AttachFile attaches u through frappe.client.attach_file, which takes the
// content base64-encoded in a JSON body. It needs only read access to the
// document (and create on File); with Field set, the document is saved,
// which needs write.
func (c *FrappeClient) AttachFile(ctx context.Context, u FileUpload) (map[string]interface{}, error) {
	args := map[string]interface{}{
		"doctype":       u.Doctype,
		"docname":       u.Docname,
		"filename":      u.Filename,
		"filedata":      base64.StdEncoding.EncodeToString(u.Content),
		"decode_base64": 1,
		"is_private":    boolInt(u.Private), // Frappe's default is public
		"folder":        u.folder(),
	}
	if u.Field != "" {
		args["docfield"] = u.Field
	}
	var res struct {
		Message map[string]interface{} `json:"message"`
	}
	err := c.do(ctx, http.MethodPost, "/api/method/frappe.client.attach_file", args, nil, fileHints(u.Doctype, u.Docname), &res)
	if err != nil {
		var plan *DryRunError
		if errors.As(err, &plan) && len(plan.Requests) == 1 {
			plan.Requests[0].Body = u.planFields("filedata")
		}
		return nil, err
	}
	if res.Message == nil {
		return nil, fmt.Errorf("unexpected response: attach_file returned no File document")
	}
	return res.Message, nil
}

// AttachmentFields are the File fields Attachments returns.
var AttachmentFields = []string{"name", "file_name", "file_url", "is_private", "file_size", "attached_to_field", "folder", "creation"}

// AttachmentFilters selects the File documents attached to a document.
func AttachmentFilters(doctype, name string) string {
	b, _ := json.Marshal([][]string{
		{"attached_to_doctype", "=", doctype},
		{"attached_to_name", "=", name},
	})
	return string(b)
}

// Attachments lists the File documents attached to a document, oldest
// first. limit is a ListOptions.Limit.
func (c *FrappeClient) Attachments(ctx context.Context, doctype, name string, limit int) ([]map[string]interface{}, error) {
	return c.GetList(ctx, "File", ListOptions{
		Fields:  AttachmentFields,
		Filters: AttachmentFilters(doctype, name),
		Limit:   limit,
		OrderBy: "creation asc",
	})
}

// FilePath checks a file URL for Download: a site-relative /files/… or
// /private/files/… path, or a full URL of the site itself (same scheme,
// host and port). A URL of any other host is refused, so the site's
// credentials never leave it. It returns the escaped path.
func FilePath(siteURL, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid file URL %q: %w", raw, err)
	}
	if u.Scheme != "" || u.Host != "" || strings.HasPrefix(raw, "//") {
		site, err := url.Parse(siteURL)
		if err != nil {
			return "", fmt.Errorf("invalid site URL: %w", err)
		}
		if u.User != nil || !strings.EqualFold(u.Scheme, site.Scheme) || !sameHost(u, site) {
			return "", fmt.Errorf("refusing %q: not a URL of the site %s, and the site's credentials must not go to another host", u.Redacted(), site.Redacted())
		}
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasPrefix(p, "/files/") && !strings.HasPrefix(p, "/private/files/") {
		return "", fmt.Errorf("invalid file URL %q: expected /files/… or /private/files/… (a File's file_url)", raw)
	}
	unescaped, err := url.PathUnescape(p)
	if err != nil {
		return "", fmt.Errorf("invalid file URL %q: %w", raw, err)
	}
	for _, seg := range strings.Split(unescaped, "/") {
		if seg == ".." || seg == "." {
			return "", fmt.Errorf("invalid file URL %q: no . or .. segments", raw)
		}
	}
	if u.RawQuery != "" || u.Fragment != "" {
		// Frappe serves files by path; a query would be ignored at best.
		return "", fmt.Errorf("invalid file URL %q: no query or fragment", raw)
	}
	return p, nil
}

// sameHost compares host and port, filling in the scheme's default port.
func sameHost(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			return "443"
		case "http":
			return "80"
		}
		return ""
	}
	return strings.EqualFold(strings.TrimSuffix(a.Hostname(), "."), strings.TrimSuffix(b.Hostname(), ".")) &&
		port(a) == port(b) && a.Hostname() != ""
}

// URL returns the site URL the client talks to, without a trailing slash.
func (c *FrappeClient) URL() string { return c.baseURL }

// Download sends a GET for path (see SitePath, FilePath) with the site's
// credentials and returns the response unread, like Raw, so a body of any
// size can be streamed to a file. Unlike Raw it follows the GET retry
// policy: a 429/502/503/504 (with a Retry-After of at most 10 s) or a
// connection error is retried, a timeout never.
func (c *FrappeClient) Download(ctx context.Context, path string, query url.Values) (*RawResponse, error) {
	req := RawRequest{Method: http.MethodGet, Path: path, Query: query}
	wait := c.r.RetryWaitTime
	for attempt := 0; ; attempt++ {
		resp, err := c.Raw(ctx, req)
		last := attempt >= c.r.RetryCount
		var reason string
		var delay time.Duration
		switch {
		case err != nil:
			var te *TransportError
			if last || !errors.As(err, &te) || !retryableTransport(te.Err) {
				return nil, err
			}
			reason = "transport error"
		case last || !retryableStatus(resp.Status, resp.Header):
			return resp, nil
		default:
			reason = fmt.Sprintf("HTTP %d", resp.Status)
			delay = retryAfterDelay(resp.Header)
			_ = resp.Body.Close()
		}
		if delay == 0 {
			delay = min(wait<<attempt, c.r.RetryMaxWaitTime)
		}
		if Debug != DebugOff {
			debugWrite(fmt.Sprintf("debug: retrying GET %s after %s (attempt %d)", redactURL(c.baseURL+path), reason, attempt+2))
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, &TransportError{Err: ctx.Err()}
		case <-t.C:
		}
	}
}

// PrintOptions select how a document is printed.
type PrintOptions struct {
	Format       string // Print Format; "" for the DocType's default
	Letterhead   string // Letter Head; "" for the default
	NoLetterhead bool
	Language     string // "" for the user's language
}

// PDF requests frappe.utils.print_format.download_pdf for a document and
// returns the response unread. The caller must check the status and that
// the body is a PDF (see IsPDF) before saving it.
func (c *FrappeClient) PDF(ctx context.Context, doctype, name string, o PrintOptions) (*RawResponse, error) {
	q := url.Values{"doctype": {doctype}, "name": {name}}
	if o.Format != "" {
		q.Set("format", o.Format)
	}
	if o.Letterhead != "" {
		q.Set("letterhead", o.Letterhead)
	}
	if o.NoLetterhead {
		q.Set("no_letterhead", "1")
	}
	if o.Language != "" {
		q.Set("language", o.Language)
	}
	return c.Download(ctx, "/api/method/frappe.utils.print_format.download_pdf", q)
}

// IsPDF reports whether a response's Content-Type says PDF.
func IsPDF(h http.Header) bool {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && mt == "application/pdf"
}

// PrintHTML renders a document with a print format through
// frappe.www.printview.get_html_and_style, which needs read access to the
// document and returns {"html", "style"}. It is a GET: the method only
// renders (the controller's before_print hook runs, as for any print).
func (c *FrappeClient) PrintHTML(ctx context.Context, doctype, name string, o PrintOptions) (map[string]interface{}, error) {
	args := map[string]interface{}{"doc": doctype, "name": name}
	if o.Format != "" {
		args["print_format"] = o.Format
	}
	if o.Letterhead != "" {
		args["letterhead"] = o.Letterhead
	}
	if o.NoLetterhead {
		args["no_letterhead"] = 1
	}
	qp, err := QueryArgs(args)
	if err != nil {
		return nil, err
	}
	if o.Language != "" {
		// get_html_and_style has no language argument; Frappe reads _lang
		// from the request for any method.
		qp["_lang"] = o.Language
	}
	var res struct {
		Message map[string]interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/method/frappe.www.printview.get_html_and_style", nil, qp, docHints(doctype, name, "read"), &res); err != nil {
		return nil, err
	}
	if res.Message == nil {
		return nil, fmt.Errorf("unexpected response: get_html_and_style returned nothing")
	}
	return res.Message, nil
}
