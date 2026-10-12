package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

func TestList(t *testing.T) {
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`[
 {"tag_name":"desktop-v0.2.0","draft":true,"html_url":"https://github.com/x/releases/tag/desktop-v0.2.0","published_at":"2026-10-01T10:00:00Z"},
 {"tag_name":"v1.0.0","prerelease":true}]`))
	}))
	defer srv.Close()

	rels, err := List(context.Background(), srv.URL, time.Second)
	if err != nil || len(rels) != 2 {
		t.Fatalf("%v %v", rels, err)
	}
	// Drafts and every kind of tag are kept: the caller picks.
	if !rels[0].Draft || rels[0].HTMLURL != "https://github.com/x/releases/tag/desktop-v0.2.0" || rels[0].PublishedAt != "2026-10-01T10:00:00Z" {
		t.Errorf("first release = %+v", rels[0])
	}
	// Latest still skips them.
	if _, err := Latest(context.Background(), srv.URL, time.Second); err == nil || !strings.Contains(err.Error(), "no ffc release") {
		t.Errorf("Latest: %v", err)
	}

	for _, code := range []int{403, 429, 500} {
		status = code
		_, err := List(context.Background(), srv.URL, time.Second)
		var se *StatusError
		if !errors.As(err, &se) || se.Code != code || !strings.Contains(err.Error(), "HTTP") {
			t.Errorf("status %d: %v", code, err)
		}
		if _, err := Latest(context.Background(), srv.URL, time.Second); !errors.As(err, &se) {
			t.Errorf("Latest status %d: %v", code, err)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sum := sha256.Sum256([]byte("a"))
	body := []byte("# comment\n" + hex.EncodeToString(sum[:]) + "  a.zip\nnot a line\n" + strings.Repeat("0", 64) + "  b.zip\n")
	got, err := ChecksumFor(body, "a.zip")
	if err != nil || got != sum {
		t.Errorf("a.zip: %x %v", got, err)
	}
	if _, err := ChecksumFor(body, "c.zip"); err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Errorf("missing: %v", err)
	}
	if _, err := ChecksumFor([]byte("zz  a.zip\n"), "a.zip"); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("malformed: %v", err)
	}
	// A second entry for the same name is refused, whatever its sum.
	line := hex.EncodeToString(sum[:]) + "  a.zip\n"
	for _, dup := range []string{line + line, line + strings.Repeat("1", 64) + "  a.zip\n"} {
		if _, err := ChecksumFor([]byte(dup), "a.zip"); err == nil || !strings.Contains(err.Error(), "more than one") {
			t.Errorf("duplicate: %v", err)
		}
	}
	// A duplicate of another file does not matter.
	if _, err := ChecksumFor([]byte(line+line), "b.zip"); err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Errorf("other file: %v", err)
	}
}

func TestSignedChecksums(t *testing.T) {
	pub, seed, _ := relsig.GenerateKey()
	saved := relsig.ReleaseKeys
	relsig.ReleaseKeys = []string{pub}
	t.Cleanup(func() { relsig.ReleaseKeys = saved })
	sums := []byte(strings.Repeat("1", 64) + "  a.zip\n")
	sig, _ := relsig.Sign(seed, sums)
	files := map[string][]byte{"/sums": sums, "/sig": sig}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	got, err := SignedChecksums(context.Background(), srv.URL+"/sums", srv.URL+"/sig")
	if err != nil || string(got) != string(sums) {
		t.Fatalf("%q %v", got, err)
	}
	files["/sums"] = []byte(strings.Repeat("2", 64) + "  a.zip\n")
	if _, err := SignedChecksums(context.Background(), srv.URL+"/sums", srv.URL+"/sig"); err == nil || !strings.Contains(err.Error(), "signature check failed") {
		t.Errorf("edited sums: %v", err)
	}
	if _, err := SignedChecksums(context.Background(), "", srv.URL+"/sig"); err == nil {
		t.Error("empty sums URL accepted")
	}
	if _, err := SignedChecksums(context.Background(), srv.URL+"/sums", ""); err == nil {
		t.Error("empty sig URL accepted")
	}
}
