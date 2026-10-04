package frappetest_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

type resp struct {
	Status int
	Body   map[string]interface{}
	Raw    string
	Header http.Header
}

// fkDo sends one request; auth is applied via the mutate callback.
func fkDo(t *testing.T, s *frappetest.Site, method, path, body string, mutate func(*http.Request)) resp {
	t.Helper()
	req, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: string(raw), Header: res.Header}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

func fkKey(r *http.Request) {
	r.Header.Set("Authorization", "token "+frappetest.APIKey+":"+frappetest.APISecret)
}

func fkNames(t *testing.T, r resp) []string {
	t.Helper()
	data, _ := r.Body["data"].([]interface{})
	var out []string
	for _, d := range data {
		out = append(out, d.(map[string]interface{})["name"].(string))
	}
	return out
}

func fkList(t *testing.T, s *frappetest.Site, query url.Values) resp {
	t.Helper()
	return fkDo(t, s, "GET", "/api/resource/Item?"+query.Encode(), "", fkKey)
}

func fkSeed(t *testing.T) *frappetest.Site {
	s := frappetest.New(t)
	s.Add("Item",
		map[string]interface{}{"name": "a", "qty": 1, "color": "red"},
		map[string]interface{}{"name": "b", "qty": 5, "color": "blue"},
		map[string]interface{}{"name": "c", "qty": 10, "color": "red"},
	)
	return s
}

func fkEq(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFilters(t *testing.T) {
	s := fkSeed(t)
	cases := []struct {
		name, filters string
		want          []string
	}{
		{"object eq", `{"color":"red"}`, []string{"a", "c"}},
		{"object op pair", `{"qty":[">",1]}`, []string{"b", "c"}},
		{"list 3", `[["color","=","blue"]]`, []string{"b"}},
		{"list 4", `[["Item","color","=","blue"]]`, []string{"b"}},
		{"ne", `[["color","!=","red"]]`, []string{"b"}},
		{"like", `[["name","like","%b%"]]`, []string{"b"}},
		{"like prefix", `{"color":["like","re%"]}`, []string{"a", "c"}},
		{"in", `[["name","in",["a","c"]]]`, []string{"a", "c"}},
		{"gt", `[["qty",">",4]]`, []string{"b", "c"}},
		{"lt", `[["qty","<",5]]`, []string{"a"}},
		{"and", `[["color","=","red"],["qty",">",1]]`, []string{"c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := fkList(t, s, url.Values{"filters": {c.filters}, "order_by": {"name asc"}})
			if r.Status != 200 {
				t.Fatalf("status %d: %s", r.Status, r.Raw)
			}
			fkEq(t, fkNames(t, r), c.want)
		})
	}
}

func TestUnknownFieldIsDataError(t *testing.T) {
	s := fkSeed(t)
	r := fkList(t, s, url.Values{"filters": {`{"nope":"x"}`}})
	if r.Status != 417 || r.Body["exc_type"] != "DataError" {
		t.Fatalf("filter: %d %s", r.Status, r.Raw)
	}
	r = fkList(t, s, url.Values{"fields": {`["name","nope"]`}})
	if r.Status != 417 || r.Body["exc_type"] != "DataError" {
		t.Fatalf("fields: %d %s", r.Status, r.Raw)
	}
}

func TestInvalidFilterShape(t *testing.T) {
	s := fkSeed(t)
	r := fkList(t, s, url.Values{"filters": {`[["color"]]`}})
	if r.Status != 417 || r.Body["exc_type"] != "ValidationError" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestLimitsAndStart(t *testing.T) {
	s := frappetest.New(t)
	for i := 0; i < 25; i++ {
		s.Add("Item", map[string]interface{}{"name": fmt.Sprintf("n%02d", i)})
	}
	if n := len(fkNames(t, fkList(t, s, url.Values{}))); n != 20 {
		t.Fatalf("default limit gave %d rows, want 20", n)
	}
	if n := len(fkNames(t, fkList(t, s, url.Values{"limit_page_length": {"0"}}))); n != 25 {
		t.Fatalf("limit 0 gave %d rows, want 25", n)
	}
	r := fkList(t, s, url.Values{"limit_page_length": {"2"}, "limit_start": {"3"}, "order_by": {"name asc"}})
	fkEq(t, fkNames(t, r), []string{"n03", "n04"})
	r = fkList(t, s, url.Values{"limit_start": {"100"}})
	if n := len(fkNames(t, r)); n != 0 {
		t.Fatalf("start past the end gave %d rows", n)
	}
}

func TestOrderBy(t *testing.T) {
	s := fkSeed(t)
	fkEq(t, fkNames(t, fkList(t, s, url.Values{"order_by": {"qty asc"}})), []string{"a", "b", "c"})
	fkEq(t, fkNames(t, fkList(t, s, url.Values{"order_by": {"qty desc"}})), []string{"c", "b", "a"})
	fkEq(t, fkNames(t, fkList(t, s, url.Values{"order_by": {"name desc"}})), []string{"c", "b", "a"})
	// default: modified desc, the last written first
	fkEq(t, fkNames(t, fkList(t, s, url.Values{})), []string{"c", "b", "a"})
	r := fkList(t, s, url.Values{"order_by": {"qty sideways"}})
	if r.Status != 417 {
		t.Fatalf("bad order_by: %d", r.Status)
	}
}

func TestFieldsSelection(t *testing.T) {
	s := fkSeed(t)
	r := fkList(t, s, url.Values{"fields": {`["name","qty"]`}, "order_by": {"name asc"}})
	row := r.Body["data"].([]interface{})[0].(map[string]interface{})
	if len(row) != 2 || row["qty"] != float64(1) {
		t.Fatalf("row %v", row)
	}
	r = fkList(t, s, url.Values{"fields": {`["*"]`}})
	row = r.Body["data"].([]interface{})[0].(map[string]interface{})
	if row["doctype"] != "Item" || row["color"] == nil {
		t.Fatalf("star row %v", row)
	}
}

func TestCreateAutoNameAndDuplicate(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("Note")
	r := fkDo(t, s, "POST", "/api/resource/Note", `{"title":"x"}`, fkKey)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	name := r.Body["data"].(map[string]interface{})["name"]
	if name != "Note-0001" {
		t.Fatalf("auto name %v", name)
	}
	r = fkDo(t, s, "POST", "/api/resource/Note", `{"name":"fixed"}`, fkKey)
	if r.Status != 200 {
		t.Fatalf("%d", r.Status)
	}
	r = fkDo(t, s, "POST", "/api/resource/Note", `{"name":"fixed"}`, fkKey)
	if r.Status != 409 || r.Body["exc_type"] != "DuplicateEntryError" {
		t.Fatalf("duplicate: %d %s", r.Status, r.Raw)
	}
	if s.Count("Note") != 2 {
		t.Fatalf("count %d", s.Count("Note"))
	}
	if d, ok := s.Doc("Note", "fixed"); !ok || d["owner"] != frappetest.Username {
		t.Fatalf("doc %v %v", d, ok)
	}
	r = fkDo(t, s, "POST", "/api/resource/Note", `not json`, fkKey)
	if r.Status != 417 {
		t.Fatalf("bad body: %d", r.Status)
	}
}

func TestUpdateMerge(t *testing.T) {
	s := fkSeed(t)
	before, _ := s.Doc("Item", "a")
	r := fkDo(t, s, "PUT", "/api/resource/Item/a", `{"qty":7,"name":"zzz"}`, fkKey)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	d, _ := s.Doc("Item", "a")
	if d["qty"].(json.Number).String() != "7" || d["color"] != "red" || d["name"] != "a" {
		t.Fatalf("merged doc %v", d)
	}
	if d["modified"] == before["modified"] {
		t.Fatal("modified not bumped")
	}
	if _, ok := s.Doc("Item", "zzz"); ok {
		t.Fatal("update renamed the document")
	}
	r = fkDo(t, s, "PUT", "/api/resource/Item/missing", `{"qty":1}`, fkKey)
	if r.Status != 404 {
		t.Fatalf("update missing: %d", r.Status)
	}
}

func TestGetAndDelete(t *testing.T) {
	s := fkSeed(t)
	r := fkDo(t, s, "GET", "/api/resource/Item/b", "", fkKey)
	if r.Status != 200 || r.Body["data"].(map[string]interface{})["color"] != "blue" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	r = fkDo(t, s, "DELETE", "/api/resource/Item/b", "", fkKey)
	if r.Status != 202 {
		t.Fatalf("delete: %d", r.Status)
	}
	r = fkDo(t, s, "DELETE", "/api/resource/Item/b", "", fkKey)
	if r.Status != 404 || r.Body["exc_type"] != "DoesNotExistError" {
		t.Fatalf("second delete: %d %s", r.Status, r.Raw)
	}
	r = fkDo(t, s, "GET", "/api/resource/Item/b", "", fkKey)
	if r.Status != 404 {
		t.Fatalf("get deleted: %d", r.Status)
	}
	r = fkDo(t, s, "GET", "/api/resource/Nothing", "", fkKey)
	if r.Status != 404 {
		t.Fatalf("unknown doctype: %d", r.Status)
	}
	if _, ok := s.Doc("Item", "b"); ok {
		t.Fatal("still stored")
	}
}

func TestAuth(t *testing.T) {
	s := fkSeed(t)
	path := "/api/resource/Item/a"
	if r := fkDo(t, s, "GET", path, "", fkKey); r.Status != 200 {
		t.Fatalf("api key: %d", r.Status)
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+frappetest.Token) }
	if r := fkDo(t, s, "GET", path, "", bearer); r.Status != 200 {
		t.Fatalf("bearer: %d", r.Status)
	}
	wrong := func(r *http.Request) { r.Header.Set("Authorization", "token test-key:wrong") }
	if r := fkDo(t, s, "GET", path, "", wrong); r.Status != 403 || r.Body["exc_type"] != "PermissionError" {
		t.Fatalf("wrong secret: %d %s", r.Status, r.Raw)
	}
	if r := fkDo(t, s, "GET", path, "", nil); r.Status != 403 {
		t.Fatalf("guest: %d", r.Status)
	}
	badSid := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "sid", Value: "nope"}) }
	if r := fkDo(t, s, "GET", path, "", badSid); r.Status != 403 {
		t.Fatalf("bad sid: %d", r.Status)
	}
}

func TestLoginLogout(t *testing.T) {
	s := fkSeed(t)
	r := fkDo(t, s, "POST", "/api/method/login", `{"usr":"Administrator","pwd":"nope"}`, nil)
	if r.Status != 401 || s.Logins() != 0 {
		t.Fatalf("bad login: %d logins=%d", r.Status, s.Logins())
	}
	r = fkDo(t, s, "POST", "/api/method/login", `{"usr":"Administrator","pwd":"admin"}`, nil)
	if r.Status != 200 || s.Logins() != 1 {
		t.Fatalf("login: %d logins=%d", r.Status, s.Logins())
	}
	var sid string
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == "sid" {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("no sid cookie")
	}
	withSid := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "sid", Value: sid}) }
	if r := fkDo(t, s, "GET", "/api/resource/Item/a", "", withSid); r.Status != 200 {
		t.Fatalf("sid auth: %d", r.Status)
	}
	if r := fkDo(t, s, "GET", "/api/method/logout", "", withSid); r.Status != 403 || s.Logouts() != 0 {
		t.Fatalf("GET logout: %d logouts=%d", r.Status, s.Logouts())
	}
	if r := fkDo(t, s, "POST", "/api/method/logout", "", withSid); r.Status != 200 || s.Logouts() != 1 {
		t.Fatalf("logout: %d logouts=%d", r.Status, s.Logouts())
	}
	if r := fkDo(t, s, "GET", "/api/resource/Item/a", "", withSid); r.Status != 403 {
		t.Fatalf("sid after logout: %d", r.Status)
	}
	// form-encoded login works too
	r = fkDo(t, s, "POST", "/api/method/login", "usr=Administrator&pwd=admin", func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if r.Status != 200 || s.Logins() != 2 {
		t.Fatalf("form login: %d logins=%d", r.Status, s.Logins())
	}
}

func TestHandleMethod(t *testing.T) {
	s := frappetest.New(t)
	s.HandleMethod("my.app.echo", func(r *http.Request, args map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"method": r.Method, "x": args["x"]}, nil
	})
	s.HandleMethod("my.app.fail", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Validation("bad input")
	})
	s.HandleMethod("my.app.boom", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, fmt.Errorf("kaput")
	})
	r := fkDo(t, s, "POST", "/api/method/my.app.echo", `{"x":"1"}`, fkKey)
	msg := r.Body["message"].(map[string]interface{})
	if r.Status != 200 || msg["method"] != "POST" || msg["x"] != "1" {
		t.Fatalf("post: %d %s", r.Status, r.Raw)
	}
	r = fkDo(t, s, "GET", "/api/method/my.app.echo?x=2", "", fkKey)
	if msg := r.Body["message"].(map[string]interface{}); msg["method"] != "GET" || msg["x"] != "2" {
		t.Fatalf("get: %s", r.Raw)
	}
	r = fkDo(t, s, "POST", "/api/method/my.app.fail", "", fkKey)
	if r.Status != 417 || r.Body["exc_type"] != "ValidationError" || !strings.Contains(r.Raw, "bad input") {
		t.Fatalf("fail: %d %s", r.Status, r.Raw)
	}
	r = fkDo(t, s, "POST", "/api/method/my.app.boom", "", fkKey)
	if r.Status != 500 {
		t.Fatalf("boom: %d", r.Status)
	}
	r = fkDo(t, s, "POST", "/api/method/unknown.method", "", fkKey)
	if r.Status != 417 {
		t.Fatalf("unknown method: %d", r.Status)
	}
	r = fkDo(t, s, "POST", "/api/method/my.app.echo", "", nil)
	if r.Status != 403 {
		t.Fatalf("guest method: %d", r.Status)
	}
}

func TestHandleOverride(t *testing.T) {
	s := fkSeed(t)
	s.Handle("DELETE /api/resource/Item/a", frappetest.ErrorHandler(frappetest.LinkExists("linked")))
	s.Handle("GET /api/resource/Item/b", frappetest.HTMLPage(502))
	r := fkDo(t, s, "DELETE", "/api/resource/Item/a", "", nil) // overrides skip auth
	if r.Status != 417 || r.Body["exc_type"] != "LinkExistsError" {
		t.Fatalf("override: %d %s", r.Status, r.Raw)
	}
	if _, ok := s.Doc("Item", "a"); !ok {
		t.Fatal("override did not replace the handler")
	}
	r = fkDo(t, s, "GET", "/api/resource/Item/b", "", fkKey)
	if r.Status != 502 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") || !strings.Contains(r.Raw, "<html>") {
		t.Fatalf("html: %d %s", r.Status, r.Raw)
	}
}

func TestErrorShape(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("GET /x", frappetest.ErrorHandler(frappetest.NotFound("gone")))
	s.Handle("GET /y", frappetest.ErrorHandler(frappetest.Permission("no")))
	r := fkDo(t, s, "GET", "/x", "", nil)
	if r.Status != 404 || r.Body["exception"] != nil || r.Body["exc_type"] != "DoesNotExistError" {
		t.Fatalf("404 shape: %s", r.Raw)
	}
	sm, _ := r.Body["_server_messages"].(string)
	if !strings.Contains(sm, "gone") {
		t.Fatalf("_server_messages %q", sm)
	}
	r = fkDo(t, s, "GET", "/y", "", nil)
	if r.Status != 403 || r.Body["exception"] == nil || r.Body["exc"] == nil {
		t.Fatalf("403 shape: %s", r.Raw)
	}
}

func TestQueryReport(t *testing.T) {
	s := frappetest.New(t)
	s.AddReport("Sales", map[string]interface{}{
		"columns": []interface{}{"A:Data"},
		"result":  []interface{}{[]interface{}{"x"}},
	})
	r := fkDo(t, s, "POST", "/api/method/frappe.desk.query_report.run", `{"report_name":"Sales","filters":"{}"}`, fkKey)
	msg, _ := r.Body["message"].(map[string]interface{})
	if r.Status != 200 || msg["columns"] == nil {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	r = fkDo(t, s, "POST", "/api/method/frappe.desk.query_report.run", `{"report_name":"Missing"}`, fkKey)
	if r.Status != 404 || r.Body["exc_type"] != "DoesNotExistError" {
		t.Fatalf("missing: %d %s", r.Status, r.Raw)
	}
}

func TestGetCountAndPing(t *testing.T) {
	s := fkSeed(t)
	r := fkDo(t, s, "POST", "/api/method/frappe.client.get_count", `{"doctype":"Item","filters":"{\"color\":\"red\"}"}`, fkKey)
	if r.Body["message"] != float64(2) {
		t.Fatalf("count: %s", r.Raw)
	}
	r = fkDo(t, s, "POST", "/api/method/frappe.client.get_count", `{"doctype":"Item"}`, fkKey)
	if r.Body["message"] != float64(3) {
		t.Fatalf("count all: %s", r.Raw)
	}
	r = fkDo(t, s, "GET", "/api/method/frappe.ping", "", nil)
	if r.Status != 200 || r.Body["message"] != "pong" {
		t.Fatalf("ping: %s", r.Raw)
	}
	r = fkDo(t, s, "GET", "/api/method/frappe.auth.get_logged_user", "", fkKey)
	if r.Body["message"] != frappetest.Username {
		t.Fatalf("logged user: %s", r.Raw)
	}
}

func TestRecording(t *testing.T) {
	s := fkSeed(t)
	fkDo(t, s, "GET", "/api/resource/Item/a?x=1", "", fkKey)
	fkDo(t, s, "POST", "/api/resource/Item", `{"name":"q"}`, fkKey)
	got := s.RequestsTo("GET", "/api/resource/Item/a")
	if len(got) != 1 || got[0].Query.Get("x") != "1" || got[0].Header.Get("Authorization") == "" {
		t.Fatalf("recorded %+v", got)
	}
	if reqs := s.Requests(); len(reqs) != 2 || reqs[1].Body != `{"name":"q"}` {
		t.Fatalf("requests %+v", reqs)
	}
}
