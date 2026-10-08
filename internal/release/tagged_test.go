package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTagged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tags/v1.2.0":
			fmt.Fprint(w, `{"tag_name":"v1.2.0","assets":[{"name":"checksums.txt"}]}`)
		case "/tags/v1.3.0-rc1":
			fmt.Fprint(w, `{"tag_name":"v1.3.0-rc1","prerelease":true}`)
		case "/tags/v1.4.0":
			fmt.Fprint(w, `{"tag_name":"v1.4.0","draft":true}`)
		case "/tags/v1.5.0":
			http.Error(w, `{"message":"rate limited"}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	for _, tag := range []string{"v1.2.0", "v1.3.0-rc1"} {
		rel, err := Tagged(ctx, srv.URL, tag, 5*time.Second)
		if err != nil || rel.TagName != tag {
			t.Errorf("Tagged(%s) = %+v, %v", tag, rel, err)
		}
	}
	for tag, want := range map[string]string{
		"v1.1.0":         "no ffc release v1.1.0",
		"v1.4.0":         "no ffc release v1.4.0", // a draft
		"v1.5.0":         "HTTP 403",
		"desktop-v0.1.2": "not an ffc release tag",
	} {
		if _, err := Tagged(ctx, srv.URL, tag, 5*time.Second); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Tagged(%s) error = %v, want %q", tag, err, want)
		}
	}
}
