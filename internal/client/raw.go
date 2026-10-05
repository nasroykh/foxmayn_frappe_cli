package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-resty/resty/v2"
)

// RawRequest is a request to any path of the site, for `ffc api`.
type RawRequest struct {
	Method string
	Path   string     // site-relative path, without a query string (see SitePath)
	Query  url.Values // may be nil
	Body   []byte     // nil: no body
	Header http.Header
}

// RawResponse is an undecoded response. The caller must close Body.
type RawResponse struct {
	Status int
	Proto  string
	Header http.Header
	Body   io.ReadCloser
}

// FixedHeaderError is a raw request that tries to set a header the client
// controls.
type FixedHeaderError struct{ Header string }

func (e *FixedHeaderError) Error() string {
	return fmt.Sprintf("the %s header is set from the site config and cannot be overridden", e.Header)
}

// CheckHeaders returns a *FixedHeaderError when h sets a header Raw refuses.
func CheckHeaders(h http.Header) error {
	for _, k := range fixedHeaders {
		if _, ok := h[http.CanonicalHeaderKey(k)]; ok {
			return &FixedHeaderError{k}
		}
	}
	return nil
}

// SitePath splits a user-supplied path into its site-relative path and query.
// Only paths on the configured site are accepted: an absolute URL or a
// protocol-relative "//host" would send the credentials elsewhere.
func SitePath(p string) (string, url.Values, error) {
	u, err := url.Parse(p)
	if err != nil {
		return "", nil, fmt.Errorf("invalid path %q: %w", p, err)
	}
	if u.Scheme != "" || u.Host != "" || u.User != nil || strings.HasPrefix(p, "//") {
		return "", nil, fmt.Errorf("invalid path %q: only paths on the site are allowed, not URLs", p)
	}
	if !strings.HasPrefix(p, "/") {
		// Parse again with the slash, so escapes such as %2F are kept.
		if u, err = url.Parse("/" + p); err != nil {
			return "", nil, fmt.Errorf("invalid path %q: %w", p, err)
		}
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", nil, fmt.Errorf("invalid query in %q: %w", p, err)
	}
	return u.EscapedPath(), q, nil
}

// fixedHeaders may not be set on a raw request: the credentials come from
// the site config, and the others pick the site on a multi-tenant bench or
// proxy, which would send those credentials to another site.
var fixedHeaders = []string{"Authorization", "Cookie", "Host", "X-Frappe-Site-Name", "X-Forwarded-Host"}

// Raw sends req with the site's credentials and returns the response without
// decoding it or checking its status, so any endpoint (desk methods, file
// downloads) can be used. The response body is streamed: the 128 MiB cap of
// the other methods does not apply, and the request is not retried on a 429
// or 5xx. It is repeated once only after a session re-login or an OAuth
// token refresh (see send): the site refused it before it ran, and the
// request body is a byte slice, so it is sent again intact. A streamed
// request body (an io.Reader) could not be replayed; keep Body a []byte.
func (c *FrappeClient) Raw(ctx context.Context, req RawRequest) (*RawResponse, error) {
	if err := CheckHeaders(req.Header); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(req.Path, "/") || strings.HasPrefix(req.Path, "//") {
		return nil, fmt.Errorf("invalid path %q: use SitePath", req.Path)
	}
	target := req.Path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}
	resp, err := c.send(ctx, c.raw, req.Method, target, func(r *resty.Request) {
		r.SetDoNotParseResponse(true)
		for k, vs := range req.Header {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
		if req.Body != nil {
			r.SetBody(req.Body)
		}
	})
	if err != nil {
		return nil, err
	}
	return &RawResponse{
		Status: resp.StatusCode(),
		Proto:  resp.RawResponse.Proto,
		Header: resp.Header(),
		Body:   resp.RawBody(),
	}, nil
}
