package frappetest

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// Files, as a v16 site serves them: POST /api/method/upload_file
// (multipart), frappe.client.attach_file (base64), GET /files/… (public, no
// login) and /private/files/… (a login; 403 HTML otherwise, also for a file
// that does not exist), frappe.core.api.file.get_max_file_size, and print
// output: download_pdf (application/pdf) and get_html_and_style.

const defaultMaxFileSize = 25 << 20

func (s *Site) registerFiles() {
	s.files = map[string][]byte{}
	s.maxFileSize = defaultMaxFileSize
	s.AddDocType("File", "file_name", "file_url", "is_private", "file_size", "attached_to_doctype",
		"attached_to_name", "attached_to_field", "folder", "is_folder", "content_hash")
	s.methods["frappe.core.api.file.get_max_file_size"] = func(*http.Request, map[string]interface{}) (interface{}, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.maxFileSize, nil
	}
	s.methods["frappe.client.attach_file"] = s.attachFile
	s.methods["frappe.www.printview.get_html_and_style"] = s.htmlAndStyle
}

// SetMaxFileSize sets the upload limit (System Settings max_file_size): a
// larger upload_file request is refused with 413, as werkzeug does.
func (s *Site) SetMaxFileSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxFileSize = n
}

// File returns the content stored at a file URL.
func (s *Site) File(fileURL string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[fileURL]
	return b, ok
}

// SetPrintHTML sets what get_html_and_style returns for every document.
func (s *Site) SetPrintHTML(html, style string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.printHTML, s.printStyle = html, style
}

// fileRoute serves the file routes; it reports false for other paths.
func (s *Site) fileRoute(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/files/"):
		s.serveFile(w, r, p, false)
	case strings.HasPrefix(p, "/private/files/"):
		s.serveFile(w, r, p, true)
	case p == "/api/method/upload_file":
		if s.authenticate(r) == "" {
			writeError(w, Permission("Not permitted"))
			return true
		}
		s.uploadFile(w, r)
	case p == "/api/method/frappe.utils.print_format.download_pdf":
		if s.authenticate(r) == "" {
			writeError(w, Permission("Not permitted"))
			return true
		}
		s.downloadPDF(w, r)
	default:
		return false
	}
	return true
}

func (s *Site) serveFile(w http.ResponseWriter, r *http.Request, p string, private bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	b, ok := s.files[p]
	s.mu.Unlock()
	if private && (!ok || s.authenticate(r) == "") {
		// Frappe answers a missing private file like a forbidden one.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<!doctype html>\n<html lang=en>\n<title>403 Forbidden</title>\n<h1>Forbidden</h1>\n<p>You don't have permission to access this file</p>\n")
		return
	}
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "<html><body><h1>404 Not Found</h1></body></html>")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if private {
		w.Header().Set("Content-Disposition", "inline; filename="+path.Base(p))
	}
	_, _ = w.Write(b)
}

// uploadFile is upload_file: multipart, the content in the part "file".
// Like Frappe it attaches to a document that does not exist, and it stores
// a public file unless is_private is set.
func (s *Site) uploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, Permission("Not permitted"))
		return
	}
	s.mu.Lock()
	max := s.maxFileSize
	s.mu.Unlock()
	if r.ContentLength > int64(max) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = io.WriteString(w, "<!doctype html>\n<html lang=en>\n<title>413 Request Entity Too Large</title>\n<h1>Request Entity Too Large</h1>\n<p>The data value transmitted exceeds the capacity limit.</p>\n")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, Validation("Invalid multipart body: "+err.Error()))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, &Error{http.StatusExpectationFailed, "MandatoryError", "Fields `file_name` or `file_url` must be set for File"})
		return
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	doc, ferr := s.newFile(hdr.Filename, content, r.FormValue("doctype"), r.FormValue("docname"),
		r.FormValue("fieldname"), r.FormValue("folder"), r.FormValue("is_private") == "1")
	if ferr != nil {
		writeError(w, ferr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": doc})
}

// attachFile is frappe.client.attach_file: the document must exist (it is
// read first), the content is base64 when decode_base64 is set.
func (s *Site) attachFile(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	dt, name := argString(args, "doctype"), argString(args, "docname")
	if _, ok := s.Doc(dt, name); !ok {
		return nil, NotFound(fmt.Sprintf("%s %s not found", dt, name))
	}
	data := []byte(argString(args, "filedata"))
	if truthy(args["decode_base64"]) {
		dec, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return nil, Validation("Incorrect padding")
		}
		data = dec
	}
	doc, err := s.newFile(argString(args, "filename"), data, dt, name, argString(args, "docfield"),
		argString(args, "folder"), truthy(args["is_private"]))
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func truthy(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case nil:
		return false
	}
	s := fmt.Sprint(v)
	return s != "" && s != "0" && s != "false"
}

// newFile stores content and creates its File document.
func (s *Site) newFile(fileName string, content []byte, dt, docname, field, folder string, private bool) (map[string]interface{}, *Error) {
	if fileName == "" {
		return nil, &Error{http.StatusExpectationFailed, "MandatoryError", "Fields `file_name` or `file_url` must be set for File"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(content) > s.maxFileSize {
		return nil, &Error{http.StatusExpectationFailed, "MaxFileSizeReachedError", "File size exceeded the maximum allowed size"}
	}
	prefix := "/files/"
	if private {
		prefix = "/private/files/"
	}
	fileName = strings.ReplaceAll(fileName, "/", "")
	u := prefix + fileName
	for i := 1; ; i++ {
		if old, taken := s.files[u]; !taken || string(old) == string(content) {
			break
		}
		ext := path.Ext(fileName)
		u = fmt.Sprintf("%s%s%d%s", prefix, strings.TrimSuffix(fileName, ext), i, ext)
	}
	s.files[u] = content
	if folder == "" {
		folder = "Home"
	}
	s.seq++
	name := fmt.Sprintf("%010x", s.seq)
	d := map[string]interface{}{
		"name": name, "file_name": fileName, "file_url": u, "is_private": boolNum(private),
		"file_size": len(content), "folder": folder, "is_folder": 0,
		"attached_to_doctype": nilIfEmpty(dt), "attached_to_name": nilIfEmpty(docname),
		"attached_to_field": nilIfEmpty(field),
	}
	doc := s.stamp("File", d, true)
	s.doctypes["File"][name] = doc
	return copyDoc(doc), nil
}

func boolNum(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// downloadPDF is frappe.utils.print_format.download_pdf: a GET answered
// with the PDF bytes, or the usual JSON error.
func (s *Site) downloadPDF(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dt, name := q.Get("doctype"), q.Get("name")
	if _, ok := s.Doc(dt, name); !ok {
		writeError(w, NotFound(fmt.Sprintf("%s %s not found", dt, name)))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s.pdf", strings.ReplaceAll(name, " ", "-")))
	_, _ = fmt.Fprintf(w, "%%PDF-1.4\n%% %s %s format=%s letterhead=%s no_letterhead=%s language=%s\n%%%%EOF\n",
		dt, name, q.Get("format"), q.Get("letterhead"), q.Get("no_letterhead"), q.Get("language"))
}

func (s *Site) htmlAndStyle(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	dt, name := argString(args, "doc"), argString(args, "name")
	if _, ok := s.Doc(dt, name); !ok {
		return nil, NotFound(fmt.Sprintf("%s %s not found", dt, name))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	html := s.printHTML
	if html == "" {
		html = fmt.Sprintf("<div class=\"print-format\"><h2>%s</h2><p>%s</p></div>", dt, name)
	}
	return map[string]interface{}{"html": html, "style": s.printStyle}, nil
}
