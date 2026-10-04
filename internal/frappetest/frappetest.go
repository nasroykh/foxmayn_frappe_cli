// Package frappetest is an in-memory fake of the Frappe REST API for tests.
//
// It serves /api/resource (list, get, create, update, delete), the
// /api/method endpoints ffc uses (login, logout, get_logged_user, ping,
// get_count, query_report.run) and any method registered with HandleMethod.
// Errors use the shapes a real Frappe v15/v16 site returns (exc_type,
// _server_messages, exception), captured from a live site.
//
// Every request is recorded. Handle overrides one "METHOD /path" route, for
// error injection or for endpoints the fake does not model.
package frappetest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Credentials accepted by a new Site.
const (
	APIKey    = "test-key"
	APISecret = "test-secret"
	Username  = "Administrator"
	Password  = "admin"
	Token     = "test-oauth-token"
)

// standardFields exist on every document, so filtering or selecting them is
// always allowed.
var standardFields = map[string]bool{
	"name": true, "owner": true, "creation": true, "modified": true,
	"modified_by": true, "docstatus": true, "idx": true, "doctype": true,
}

// Request is one recorded HTTP request.
type Request struct {
	Method string
	Path   string // unescaped
	Query  url.Values
	Header http.Header
	Body   string
}

// MethodFunc handles a whitelisted method. args holds the query parameters
// (GET) or the JSON/form body (POST). The returned value becomes "message";
// an *Error is written with its status and Frappe error shape.
type MethodFunc func(r *http.Request, args map[string]interface{}) (interface{}, error)

// Site is a fake Frappe site. Create it with New.
type Site struct {
	URL string

	mu        sync.Mutex
	doctypes  map[string]map[string]map[string]interface{} // doctype → name → doc
	reports   map[string]map[string]interface{}
	methods   map[string]MethodFunc
	overrides map[string]http.Handler
	sessions  map[string]bool
	reqs      []Request
	seq       int
	logins    int
	logouts   int
	clock     time.Time
}

// New starts a fake site, closed when the test ends.
func New(t testing.TB) *Site {
	t.Helper()
	s := &Site{
		doctypes:  map[string]map[string]map[string]interface{}{},
		reports:   map[string]map[string]interface{}{},
		methods:   map[string]MethodFunc{},
		overrides: map[string]http.Handler{},
		sessions:  map[string]bool{},
		clock:     time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// AddDocType registers an empty DocType.
func (s *Site) AddDocType(doctype string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doctypes[doctype] == nil {
		s.doctypes[doctype] = map[string]map[string]interface{}{}
	}
}

// Add stores documents (registering the DocType). Each needs a "name"; the
// standard fields are filled in when missing.
func (s *Site) Add(doctype string, docs ...map[string]interface{}) {
	s.AddDocType(doctype)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range docs {
		name, _ := d["name"].(string)
		if name == "" {
			panic("frappetest.Add: document without a name")
		}
		s.doctypes[doctype][name] = s.stamp(doctype, copyDoc(d), true)
	}
}

// Doc returns a copy of a stored document.
func (s *Site) Doc(doctype, name string) (map[string]interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.doctypes[doctype][name]
	return copyDoc(d), ok
}

// Count returns the number of stored documents of a DocType.
func (s *Site) Count(doctype string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.doctypes[doctype])
}

// AddReport registers a query report; run returns result as "message".
func (s *Site) AddReport(name string, result map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports[name] = result
}

// HandleMethod registers a whitelisted method under /api/method/<name>.
func (s *Site) HandleMethod(name string, fn MethodFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[name] = fn
}

// Handle overrides one route, e.g. "DELETE /api/resource/ToDo/a". The path
// is matched unescaped and without the query string. Overrides skip auth.
func (s *Site) Handle(route string, h http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[route] = h
}

// Requests returns the recorded requests in order.
func (s *Site) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// RequestsTo returns the recorded requests for one method and path.
func (s *Site) RequestsTo(method, path string) []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// Logins and Logouts count successful /api/method/login and logout calls.
func (s *Site) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *Site) Logouts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logouts
}

// ─── errors ──────────────────────────────────────────────────────────────────

// Error is a Frappe exception. Returned from a MethodFunc or written with
// ErrorHandler, it produces the real response shape.
type Error struct {
	Status  int
	ExcType string // e.g. "ValidationError"
	Message string
}

func (e *Error) Error() string { return e.ExcType + ": " + e.Message }

// Common Frappe exceptions.
func NotFound(msg string) *Error { return &Error{http.StatusNotFound, "DoesNotExistError", msg} }
func Validation(msg string) *Error {
	return &Error{http.StatusExpectationFailed, "ValidationError", msg}
}
func DataError(msg string) *Error  { return &Error{http.StatusExpectationFailed, "DataError", msg} }
func Permission(msg string) *Error { return &Error{http.StatusForbidden, "PermissionError", msg} }
func Duplicate(msg string) *Error  { return &Error{http.StatusConflict, "DuplicateEntryError", msg} }
func LinkExists(msg string) *Error {
	return &Error{http.StatusExpectationFailed, "LinkExistsError", msg}
}

// ErrorHandler writes e on every request; use it with Handle.
func ErrorHandler(e *Error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeError(w, e) })
}

// HTMLPage answers with an HTML page, as a login proxy or a misconfigured
// reverse proxy would.
func HTMLPage(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><head><title>Login</title></head><body><h1>Login</h1><form></form></body></html>")
	})
}

func writeError(w http.ResponseWriter, e *Error) {
	msg, _ := json.Marshal(map[string]interface{}{
		"message": e.Message, "as_table": false, "title": "Message",
		"indicator": "red", "raise_exception": 1,
	})
	serverMessages, _ := json.Marshal([]string{string(msg)})
	body := map[string]interface{}{
		"exc_type":         e.ExcType,
		"_server_messages": string(serverMessages),
	}
	// Real sites add the exception and a traceback except for 404s.
	if e.Status != http.StatusNotFound {
		body["exception"] = "frappe.exceptions." + e.ExcType + ": " + e.Message
		tb, _ := json.Marshal([]string{"Traceback (most recent call last):\n  File \"apps/frappe/frappe/app.py\", line 158, in application\nfrappe.exceptions." + e.ExcType})
		body["exc"] = string(tb)
	}
	writeJSON(w, e.Status, body)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ─── routing ─────────────────────────────────────────────────────────────────

func (s *Site) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), string(body)})
	override := s.overrides[r.Method+" "+r.URL.Path]
	s.mu.Unlock()
	if override != nil {
		override.ServeHTTP(w, r)
		return
	}

	path := r.URL.Path
	switch {
	case path == "/api/method/login":
		s.login(w, r, body)
		return
	case path == "/api/method/frappe.ping":
		writeJSON(w, http.StatusOK, map[string]string{"message": "pong"})
		return
	}

	user := s.authenticate(r)
	if user == "" {
		writeError(w, Permission("User Guest does not have doctype access via role permission"))
		return
	}

	switch {
	case path == "/api/method/logout":
		s.logout(w, r)
	case strings.HasPrefix(path, "/api/resource/"):
		s.resource(w, r, body, user)
	case strings.HasPrefix(path, "/api/method/"):
		s.method(w, r, strings.TrimPrefix(path, "/api/method/"), body, user)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// authenticate returns the user a request is authenticated as, or "".
func (s *Site) authenticate(r *http.Request) string {
	switch auth := r.Header.Get("Authorization"); {
	case auth == "token "+APIKey+":"+APISecret:
		return Username
	case auth == "Bearer "+Token:
		return Username
	}
	if c, err := r.Cookie("sid"); err == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sessions[c.Value] {
			return Username
		}
	}
	return ""
}

func (s *Site) login(w http.ResponseWriter, r *http.Request, body []byte) {
	args := parseArgs(r, body)
	if args["usr"] != Username || args["pwd"] != Password {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Invalid Login. Try again.", "exc_type": "AuthenticationError"})
		return
	}
	s.mu.Lock()
	s.logins++
	sid := fmt.Sprintf("sid-%d", s.logins)
	s.sessions[sid] = true
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: sid, Path: "/", HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]string{"message": "Logged In", "home_page": "/app", "full_name": "Administrator"})
}

func (s *Site) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, Permission("Not permitted"))
		return
	}
	s.mu.Lock()
	if c, err := r.Cookie("sid"); err == nil {
		delete(s.sessions, c.Value)
	}
	s.logouts++
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{})
}

// ─── /api/method ─────────────────────────────────────────────────────────────

func (s *Site) method(w http.ResponseWriter, r *http.Request, name string, body []byte, user string) {
	args := parseArgs(r, body)
	var (
		result interface{}
		err    error
	)
	s.mu.Lock()
	fn := s.methods[name]
	s.mu.Unlock()
	switch {
	case fn != nil:
		result, err = fn(r, args)
	case name == "frappe.auth.get_logged_user":
		result = user
	case name == "frappe.client.get_count":
		result, err = s.getCount(args)
	case name == "frappe.desk.query_report.run":
		result, err = s.runReport(args)
	default:
		err = Validation(fmt.Sprintf("Failed to get method for command %s with No module named '%s'", name, name))
	}
	if err != nil {
		if fe, ok := err.(*Error); ok {
			writeError(w, fe)
			return
		}
		writeError(w, &Error{http.StatusInternalServerError, "Exception", err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": result})
}

func (s *Site) getCount(args map[string]interface{}) (interface{}, error) {
	dt, _ := args["doctype"].(string)
	rows, err := s.query(dt, args["filters"])
	if err != nil {
		return nil, err
	}
	return len(rows), nil
}

func (s *Site) runReport(args map[string]interface{}) (interface{}, error) {
	name, _ := args["report_name"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.reports[name]
	if !ok {
		return nil, NotFound(fmt.Sprintf("Report %s not found", name))
	}
	return res, nil
}

// parseArgs reads method arguments: the query string, then a JSON or form
// body on top.
func parseArgs(r *http.Request, body []byte) map[string]interface{} {
	args := map[string]interface{}{}
	for k, v := range r.URL.Query() {
		args[k] = v[0]
	}
	if len(body) == 0 {
		return args
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]interface{}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if dec.Decode(&m) == nil {
			for k, v := range m {
				args[k] = v
			}
		}
		return args
	}
	if form, err := url.ParseQuery(string(body)); err == nil {
		for k, v := range form {
			args[k] = v[0]
		}
	}
	return args
}

// ─── /api/resource ───────────────────────────────────────────────────────────

func (s *Site) resource(w http.ResponseWriter, r *http.Request, body []byte, user string) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/resource/"), "/", 2)
	doctype := parts[0]
	s.mu.Lock()
	_, known := s.doctypes[doctype]
	s.mu.Unlock()
	if !known {
		writeError(w, NotFound(fmt.Sprintf("DocType %s not found", doctype)))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.list(w, r, doctype)
		case http.MethodPost:
			s.create(w, doctype, body, user)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	name := parts[1]
	switch r.Method {
	case http.MethodGet:
		if d, ok := s.Doc(doctype, name); ok {
			writeJSON(w, http.StatusOK, map[string]interface{}{"data": d})
			return
		}
		writeError(w, NotFound(fmt.Sprintf("%s %s not found", doctype, name)))
	case http.MethodPut:
		s.update(w, doctype, name, body, user)
	case http.MethodDelete:
		s.mu.Lock()
		_, ok := s.doctypes[doctype][name]
		delete(s.doctypes[doctype], name)
		s.mu.Unlock()
		if !ok {
			writeError(w, NotFound(fmt.Sprintf("%s %s not found", doctype, name)))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "ok"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func decodeBody(body []byte) (map[string]interface{}, error) {
	var m map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, Validation("Invalid request body")
	}
	return m, nil
}

func (s *Site) create(w http.ResponseWriter, doctype string, body []byte, user string) {
	d, err := decodeBody(body)
	if err != nil {
		writeError(w, err.(*Error))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name, _ := d["name"].(string)
	if name == "" {
		s.seq++
		name = fmt.Sprintf("%s-%04d", strings.ReplaceAll(doctype, " ", "-"), s.seq)
	}
	if _, exists := s.doctypes[doctype][name]; exists {
		writeError(w, Duplicate(fmt.Sprintf("%s %s already exists", doctype, name)))
		return
	}
	d["name"] = name
	d["owner"] = user
	doc := s.stamp(doctype, d, true)
	s.doctypes[doctype][name] = doc
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": copyDoc(doc)})
}

func (s *Site) update(w http.ResponseWriter, doctype, name string, body []byte, user string) {
	patch, err := decodeBody(body)
	if err != nil {
		writeError(w, err.(*Error))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.doctypes[doctype][name]
	if !ok {
		writeError(w, NotFound(fmt.Sprintf("%s %s not found", doctype, name)))
		return
	}
	for k, v := range patch {
		if k != "name" {
			doc[k] = v
		}
	}
	doc["modified_by"] = user
	s.stamp(doctype, doc, false)
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": copyDoc(doc)})
}

// stamp fills the standard fields; the caller holds s.mu. Every write moves
// the clock one second, so "modified desc" ordering is deterministic.
func (s *Site) stamp(doctype string, d map[string]interface{}, isNew bool) map[string]interface{} {
	s.clock = s.clock.Add(time.Second)
	ts := s.clock.Format("2006-01-02 15:04:05.000000")
	d["doctype"] = doctype
	d["modified"] = ts
	if isNew {
		setDefault(d, "creation", ts)
		setDefault(d, "owner", Username)
		setDefault(d, "modified_by", Username)
		setDefault(d, "docstatus", json.Number("0"))
		setDefault(d, "idx", json.Number("0"))
	}
	return d
}

func setDefault(d map[string]interface{}, k string, v interface{}) {
	if _, ok := d[k]; !ok {
		d[k] = v
	}
}

func (s *Site) list(w http.ResponseWriter, r *http.Request, doctype string) {
	q := r.URL.Query()
	fields := []string{"name"}
	if f := q.Get("fields"); f != "" {
		if err := json.Unmarshal([]byte(f), &fields); err != nil {
			writeError(w, Validation("fields must be a JSON list"))
			return
		}
	}
	var filters interface{}
	if f := q.Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &filters); err != nil {
			writeError(w, Validation("filters must be JSON"))
			return
		}
	}
	rows, err := s.query(doctype, filters)
	if err != nil {
		writeError(w, err.(*Error))
		return
	}
	for _, f := range fields {
		if f != "*" && !s.knownField(doctype, f) {
			writeError(w, DataError("Field not permitted in query: "+f))
			return
		}
	}
	if err := sortRows(rows, q.Get("order_by")); err != nil {
		writeError(w, err.(*Error))
		return
	}

	start, _ := strconv.Atoi(q.Get("limit_start"))
	limit := 20
	if v := q.Get("limit_page_length"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	if start > len(rows) {
		start = len(rows)
	}
	rows = rows[start:]
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}

	out := make([]map[string]interface{}, 0, len(rows))
	for _, d := range rows {
		if len(fields) == 1 && fields[0] == "*" {
			out = append(out, d)
			continue
		}
		row := map[string]interface{}{}
		for _, f := range fields {
			row[f] = d[f]
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": out})
}

// knownField reports whether any document of the DocType has the field (the
// fake has no schema).
func (s *Site) knownField(doctype, field string) bool {
	if standardFields[field] {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.doctypes[doctype] {
		if _, ok := d[field]; ok {
			return true
		}
	}
	return false
}

// query returns copies of the documents matching filters: an object
// {"field": value | [op, value]} or a list of [field, op, value] or
// [doctype, field, op, value]. It is also used by get_count, whose filters
// may arrive JSON-encoded.
func (s *Site) query(doctype string, filters interface{}) ([]map[string]interface{}, error) {
	if str, ok := filters.(string); ok && str != "" {
		var v interface{}
		if err := json.Unmarshal([]byte(str), &v); err != nil {
			return nil, Validation("filters must be JSON")
		}
		filters = v
	}
	conds, err := parseFilters(filters)
	if err != nil {
		return nil, err
	}
	for _, c := range conds {
		if !s.knownField(doctype, c.field) {
			return nil, DataError("Field not permitted in query: " + c.field)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]interface{}
	for _, d := range s.doctypes[doctype] {
		match := true
		for _, c := range conds {
			if !c.match(d[c.field]) {
				match = false
				break
			}
		}
		if match {
			out = append(out, copyDoc(d))
		}
	}
	return out, nil
}

type cond struct {
	field, op string
	value     interface{}
}

func parseFilters(f interface{}) ([]cond, error) {
	var out []cond
	switch v := f.(type) {
	case nil:
	case map[string]interface{}:
		for field, val := range v {
			if pair, ok := val.([]interface{}); ok && len(pair) == 2 {
				op, _ := pair[0].(string)
				out = append(out, cond{field, op, pair[1]})
				continue
			}
			out = append(out, cond{field, "=", val})
		}
	case []interface{}:
		for _, item := range v {
			parts, ok := item.([]interface{})
			if ok && len(parts) == 4 {
				parts = parts[1:]
			}
			if !ok || len(parts) != 3 {
				return nil, Validation(fmt.Sprintf("invalid filter: %v", item))
			}
			field, _ := parts[0].(string)
			op, _ := parts[1].(string)
			out = append(out, cond{field, op, parts[2]})
		}
	default:
		return nil, Validation("filters must be an object or a list")
	}
	return out, nil
}

func (c cond) match(v interface{}) bool {
	a := fmt.Sprint(v)
	switch strings.ToLower(c.op) {
	case "=":
		return a == fmt.Sprint(c.value)
	case "!=":
		return a != fmt.Sprint(c.value)
	case "like":
		return likeMatch(a, fmt.Sprint(c.value))
	case "not like":
		return !likeMatch(a, fmt.Sprint(c.value))
	case "in", "not in":
		in := false
		for _, x := range toList(c.value) {
			if a == fmt.Sprint(x) {
				in = true
			}
		}
		return in == (strings.ToLower(c.op) == "in")
	case ">", "<", ">=", "<=":
		cmp := compare(v, c.value)
		switch c.op {
		case ">":
			return cmp > 0
		case "<":
			return cmp < 0
		case ">=":
			return cmp >= 0
		default:
			return cmp <= 0
		}
	}
	return false
}

func toList(v interface{}) []interface{} {
	switch x := v.(type) {
	case []interface{}:
		return x
	case string:
		var out []interface{}
		for _, p := range strings.Split(x, ",") {
			out = append(out, strings.TrimSpace(p))
		}
		return out
	}
	return []interface{}{v}
}

// likeMatch implements SQL LIKE with % wildcards, case-insensitively (as
// MariaDB's default collation does).
func likeMatch(s, pattern string) bool {
	s, pattern = strings.ToLower(s), strings.ToLower(pattern)
	parts := strings.Split(pattern, "%")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i, p := range parts[1:] {
		if i == len(parts)-2 {
			return strings.HasSuffix(s, p)
		}
		idx := strings.Index(s, p)
		if idx < 0 {
			return false
		}
		s = s[idx+len(p):]
	}
	return s == ""
}

// compare orders two values numerically when both are numbers, otherwise as
// strings.
func compare(a, b interface{}) int {
	fa, aok := number(a)
	fb, bok := number(b)
	if aok && bok {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func number(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

// sortRows applies "field [asc|desc]" (default "modified desc").
func sortRows(rows []map[string]interface{}, orderBy string) error {
	if orderBy == "" {
		orderBy = "modified desc"
	}
	f := strings.Fields(orderBy)
	if len(f) == 0 || len(f) > 2 {
		return Validation("unsupported order_by: " + orderBy)
	}
	field, desc := strings.Trim(f[0], "`"), false
	if len(f) == 2 {
		switch strings.ToLower(f[1]) {
		case "desc":
			desc = true
		case "asc":
		default:
			return Validation("unsupported order_by: " + orderBy)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		c := compare(rows[i][field], rows[j][field])
		if c == 0 {
			c = strings.Compare(fmt.Sprint(rows[i]["name"]), fmt.Sprint(rows[j]["name"]))
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
	return nil
}

func copyDoc(d map[string]interface{}) map[string]interface{} {
	if d == nil {
		return nil
	}
	b, _ := json.Marshal(d)
	var out map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	_ = dec.Decode(&out)
	return out
}
