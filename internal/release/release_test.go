package release

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

func TestAssetAndBinaryNames(t *testing.T) {
	if got := AssetName("v1.11.0", "windows", "arm64"); got != "ffc_1.11.0_windows_arm64.zip" {
		t.Errorf("windows asset = %q", got)
	}
	if got := AssetName("1.11.0", "darwin", "arm64"); got != "ffc_1.11.0_darwin_arm64.tar.gz" {
		t.Errorf("darwin asset = %q", got)
	}
	if BinaryName("windows") != "ffc.exe" || BinaryName("darwin") != "ffc" {
		t.Error("binary names")
	}
}

func TestTarget(t *testing.T) {
	rel := &Release{TagName: "v2.0.0", Assets: []Asset{
		{Name: "ffc_2.0.0_windows_amd64.zip", BrowserDownloadURL: "https://x/zip"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://x/sums"},
		{Name: relsig.SignatureName, BrowserDownloadURL: "https://x/sig"},
	}}
	tg, err := rel.Target("windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if tg.DownloadURL != "https://x/zip" || tg.ChecksumsURL != "https://x/sums" || tg.SignatureURL != "https://x/sig" || tg.Tag != "v2.0.0" {
		t.Errorf("target = %+v", tg)
	}
	if _, err := rel.Target("darwin", "arm64"); err == nil || !strings.Contains(err.Error(), "ffc_2.0.0_darwin_arm64.tar.gz") {
		t.Errorf("missing platform: %v", err)
	}
}

// fakeRelease serves a signed release with one Windows archive.
func fakeRelease(t *testing.T, bin []byte, signed bool) (*httptest.Server, map[string][]byte) {
	t.Helper()
	pub, seed, err := relsig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	saved := relsig.ReleaseKeys
	relsig.ReleaseKeys = []string{pub}
	t.Cleanup(func() { relsig.ReleaseKeys = saved })

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ffc.exe")
	_, _ = w.Write(bin)
	_ = zw.Close()
	archive := buf.Bytes()
	const asset = "ffc_3.0.0_windows_amd64.zip"
	sum := sha256.Sum256(archive)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	sig, _ := relsig.Sign(seed, sums)

	files := map[string][]byte{"/" + asset: archive, "/checksums.txt": sums}
	if signed {
		files["/checksums.txt.sig"] = sig
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			rel := Release{TagName: "v3.0.0"}
			for name := range files {
				rel.Assets = append(rel.Assets, Asset{Name: name[1:], BrowserDownloadURL: "http://" + r.Host + name})
			}
			// Newest first, as GitHub lists them: a desktop release, a
			// draft and a prerelease come before the ffc release.
			rels := []Release{
				{TagName: "desktop-v0.2.0"},
				{TagName: "v3.1.0", Draft: true},
				{TagName: "v3.1.0-rc1", Prerelease: true},
				rel,
				{TagName: "v2.9.0"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rels)
			return
		}
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, files
}

func TestLatestAndDownload(t *testing.T) {
	srv, _ := fakeRelease(t, []byte("EXE"), true)
	ctx := context.Background()
	rel, err := Latest(ctx, srv.URL+"/latest", 5*time.Second)
	if err != nil || rel.TagName != "v3.0.0" {
		t.Fatalf("Latest = %+v, %v", rel, err)
	}
	tg, err := rel.Target("windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := Download(ctx, tg)
	if err != nil || string(bin) != "EXE" {
		t.Fatalf("Download = %q, %v", bin, err)
	}
}

func TestDownloadFailsClosed(t *testing.T) {
	ctx := context.Background()

	// No signature asset: refused, no binary.
	srv, _ := fakeRelease(t, []byte("EXE"), false)
	rel, err := Latest(ctx, srv.URL+"/latest", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tg, _ := rel.Target("windows", "amd64")
	if bin, err := Download(ctx, tg); err == nil || bin != nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("unsigned release: %q, %v", bin, err)
	}

	// Archive swapped after signing: refused.
	srv, files := fakeRelease(t, []byte("EXE"), true)
	files["/ffc_3.0.0_windows_amd64.zip"] = []byte("evil")
	rel, _ = Latest(ctx, srv.URL+"/latest", 5*time.Second)
	tg, _ = rel.Target("windows", "amd64")
	if bin, err := Download(ctx, tg); err == nil || bin != nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("swapped archive: %q, %v", bin, err)
	}

	// A cancelled context stops the download.
	srv, _ = fakeRelease(t, []byte("EXE"), true)
	rel, _ = Latest(ctx, srv.URL+"/latest", 5*time.Second)
	tg, _ = rel.Target("windows", "amd64")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if bin, err := Download(cctx, tg); err == nil || bin != nil {
		t.Errorf("cancelled: %q, %v", bin, err)
	}
}

func TestLatestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/empty" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.URL.Path == "/desktop-only" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"tag_name":"desktop-v0.1.0"},{"tag_name":"vnext"}]`))
			return
		}
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := Latest(context.Background(), srv.URL+"/x", 5*time.Second); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("403: %v", err)
	}
	for _, path := range []string{"/empty", "/desktop-only"} {
		if _, err := Latest(context.Background(), srv.URL+path, 5*time.Second); err == nil || !strings.Contains(err.Error(), "no ffc release") {
			t.Errorf("%s: %v", path, err)
		}
	}
}
