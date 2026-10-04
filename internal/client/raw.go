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
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", nil, fmt.Errorf("invalid query in %q: %w", p, err)
	}
	return u.EscapedPath(), q, nil
}

// authHeaders are set by the client from the site config; a request may not
// replace them.
var authHeaders = []string{"Authorization", "Cookie"}

// Raw sends req with the site's credentials and returns the response without
// decoding it or checking its status, so any endpoint (desk methods, file
// downloads) can be used. The body is streamed: the 128 MiB cap of the other
// methods does not apply, and the request is not retried.
func (c *FrappeClient) Raw(ctx context.Context, req RawRequest) (*RawResponse, error) {
	for _, h := range authHeaders {
		if req.Header.Get(h) != "" {
			return nil, fmt.Errorf("the %s header is set from the site config and cannot be overridden", h)
		}
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
