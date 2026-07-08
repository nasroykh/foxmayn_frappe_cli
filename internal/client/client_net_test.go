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
