package llm

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoRedirectClientCapsTheBody(t *testing.T) {
	big := strings.Repeat("x", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, big) }))
	defer srv.Close()
	read := func(max int64) ([]byte, error) {
		c := NoRedirectClient(nil)
		c.Transport = capTransport{base: c.Transport.(capTransport).base, max: max}
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	if b, err := read(100); err != nil || len(b) != 100 {
		t.Fatalf("at the cap: %d bytes, %v", len(b), err)
	}
	if b, err := read(99); !errors.Is(err, ErrResponseTooLarge) || len(b) != 99 {
		t.Fatalf("over the cap: %d bytes, %v", len(b), err)
	}
	resp, err := NoRedirectClient(nil).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, err := io.ReadAll(resp.Body); err != nil || len(b) != 100 {
		t.Fatalf("default cap: %d bytes, %v", len(b), err)
	}
}
