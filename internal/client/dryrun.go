package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-resty/resty/v2"
)

// DryRunScope says which requests a dry run holds back.
type DryRunScope int

const (
	// DryRunWrites holds back every request except GET and HEAD, so a
	// command can still read what it needs (the document to submit, a
	// DocType's meta) before the write it would make.
	DryRunWrites DryRunScope = iota + 1
	// DryRunAll holds back every request, for commands whose request is
	// arbitrary (call-method, api): a GET to a method can write too.
	DryRunAll
)

type dryRunKey struct{}

// WithDryRun returns a context under which the client sends no request in
// scope: it returns a *DryRunError describing the request instead.
func WithDryRun(ctx context.Context, scope DryRunScope) context.Context {
	return context.WithValue(ctx, dryRunKey{}, scope)
}

// IsDryRun reports whether ctx carries a dry run.
func IsDryRun(ctx context.Context) bool {
	_, ok := ctx.Value(dryRunKey{}).(DryRunScope)
	return ok
}

func holdBack(ctx context.Context, method string) bool {
	scope, _ := ctx.Value(dryRunKey{}).(DryRunScope)
	switch scope {
	case DryRunAll:
		return true
	case DryRunWrites:
		return method != http.MethodGet && method != http.MethodHead
	}
	return false
}

// PlannedRequest is a request a dry run did not send. Secrets in the URL,
// the headers and the body are redacted.
type PlannedRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// Headers are the ones set for this request (ffc api -H, Content-Type),
	// not the credentials every request carries.
	Headers map[string]string `json:"headers,omitempty"`
	Body    interface{}       `json:"body,omitempty"`
	// Changes is filled in by commands that can tell what a write would
	// change (update-doc): field → {"from": current, "to": new}.
	Changes map[string]interface{} `json:"changes,omitempty"`
}

// DryRunError carries the requests a dry run held back. It is returned as
// an error so every caller stops at the first write, exactly where the real
// run would have changed something.
type DryRunError struct {
	Requests []PlannedRequest
}

func (e *DryRunError) Error() string {
	if len(e.Requests) == 1 {
		return fmt.Sprintf("dry run: %s %s not sent", e.Requests[0].Method, e.Requests[0].URL)
	}
	return fmt.Sprintf("dry run: %d requests not sent", len(e.Requests))
}

// plan describes req, built for method and path on r, as a held-back
// request.
func plan(r *resty.Client, req *resty.Request, method, path string) *DryRunError {
	u := strings.TrimRight(r.BaseURL, "/") + path
	if q := req.QueryParam.Encode(); q != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + q
	}
	var headers map[string]string
	for k := range req.Header {
		if headers == nil {
			headers = map[string]string{}
		}
		headers[k] = redactHeader(k, strings.Join(req.Header.Values(k), ", "))
	}
	return &DryRunError{Requests: []PlannedRequest{{
		Method:  method,
		URL:     redactURL(u),
		Headers: headers,
		Body:    planBody(req.Body, req.Header.Get("Content-Type")),
	}}}
}

// planBody is a request body as data: JSON as its value, other text as a
// string, binary as a size; secrets redacted.
func planBody(body interface{}, contentType string) interface{} {
	var raw []byte
	switch b := body.(type) {
	case nil:
		return nil
	case []byte:
		raw = b
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			return fmt.Sprintf("(%T body)", b)
		}
	}
	switch {
	case len(raw) == 0:
		return nil
	case !utf8.Valid(raw):
		return fmt.Sprintf("(binary, %s)", size(int64(len(raw))))
	case strings.Contains(contentType, "x-www-form-urlencoded"):
		return redactForm(string(raw))
	}
	if v, ok := parseJSONValue(raw); ok {
		r, _ := redactValue(v)
		return r
	}
	return redactJSONText(string(raw))
}
