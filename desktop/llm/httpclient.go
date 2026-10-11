package llm

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// responseHeaderTimeout bounds the wait for a provider's response headers
// (the value the OpenAI SDK's own default client uses). A stream's body is
// bounded by the request context only.
const responseHeaderTimeout = 10 * time.Minute

// MaxResponseBytes caps the body of any provider response, a stream included:
// the SDK decoders and io.ReadAll would otherwise buffer whatever a server
// sends. 64 MiB is far above a long streamed answer.
const MaxResponseBytes = 64 << 20

// ErrResponseTooLarge is the read error once a body passes the cap.
var ErrResponseTooLarge = errors.New("provider response is too large")

type capTransport struct {
	base http.RoundTripper
	max  int64
}

func (t capTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &capBody{rc: resp.Body, left: t.max}
	return resp, nil
}

type capBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *capBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrResponseTooLarge
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - int(-b.left), ErrResponseTooLarge
	}
	return n, err
}

func (b *capBody) Close() error { return b.rc.Close() }

// NoRedirectClient returns a copy of h, or a new client when h is nil, that
// follows no redirect: a 3xx comes back as the response. Adapters send the
// API key in a header, and Go copies headers other than Authorization and
// Cookie to a redirect target on another host.
func NoRedirectClient(h *http.Client) *http.Client {
	c := &http.Client{}
	switch {
	case h != nil:
		cp := *h
		c = &cp
	default:
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			t = t.Clone()
			t.ResponseHeaderTimeout = responseHeaderTimeout
			c.Transport = t
		}
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = capTransport{base: base, max: MaxResponseBytes}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
