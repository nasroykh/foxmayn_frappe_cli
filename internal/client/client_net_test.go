package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// TestGetListNonNilOnEmpty verifies GetList never returns a nil slice — so
// `list-docs --json` emits [] rather than null even for an empty or degenerate
// 2xx envelope (L13 / the removed M12).
func TestGetListNonNilOnEmpty(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":null}`, `{}`, `{"message":[]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))

		fc, err := New(context.Background(), &config.SiteConfig{URL: srv.URL, APIKey: "k", APISecret: "s"})
		if err != nil {
			srv.Close()
			t.Fatalf("New: %v", err)
		}
		rows, err := fc.GetList(context.Background(), "ToDo", ListOptions{})
		srv.Close()
		if err != nil {
			t.Fatalf("GetList(%s): %v", body, err)
		}
		if rows == nil {
			t.Errorf("GetList(%s) returned nil slice; want non-nil", body)
		}
		if b, _ := json.Marshal(rows); string(b) != "[]" {
			t.Errorf("GetList(%s) marshaled to %s, want []", body, b)
		}
	}
}

// TestNewNoCredentials verifies New errors rather than issuing anonymous
// requests when a site has no usable credentials (L22).
func TestNewNoCredentials(t *testing.T) {
	if _, err := New(context.Background(), &config.SiteConfig{URL: "http://127.0.0.1:1"}); err == nil {
		t.Error("New with no credentials: want error, got nil")
	}
}

// TestNumbersKeepPrecisionAndLiteral verifies responses decode numbers as
// json.Number: integers above 2^53 survive a round trip and the literal keeps
// Frappe's Int (no decimal point) vs Float/Currency (decimal point) shape.
func TestNumbersKeepPrecisionAndLiteral(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"name":"a","big":9007199254740993,"year":2025,"total":1500.0}}`))
	}))
	defer srv.Close()
	fc, err := New(context.Background(), &config.SiteConfig{URL: srv.URL, APIKey: "k", APISecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := fc.GetDoc(context.Background(), "ToDo", "a")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(doc); string(b) != `{"big":9007199254740993,"name":"a","total":1500.0,"year":2025}` {
		t.Errorf("round trip = %s", b)
	}
}

// TestTrailingDataRejected keeps the strictness json.Unmarshal had.
func TestTrailingDataRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"name":"a"}} {"x":1}`))
	}))
	defer srv.Close()
	fc, _ := New(context.Background(), &config.SiteConfig{URL: srv.URL, APIKey: "k", APISecret: "s"})
	if _, err := fc.GetDoc(context.Background(), "ToDo", "a"); err == nil {
		t.Error("want an error for trailing data")
	}
}
