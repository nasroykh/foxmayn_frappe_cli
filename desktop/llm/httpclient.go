package llm

import (
	"net/http"
	"time"
)

// responseHeaderTimeout bounds the wait for a provider's response headers
// (the value the OpenAI SDK's own default client uses). A stream's body is
// bounded by the request context only.
const responseHeaderTimeout = 10 * time.Minute

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
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
