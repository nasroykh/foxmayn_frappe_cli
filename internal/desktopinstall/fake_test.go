package desktopinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

// fake is an in-memory GitHub: the release list at /releases and the files
// of every published tag at /download/<tag>/<name>. A second, TLS server
// stands in for the CDN a download is redirected to.
type fake struct {
	t        *testing.T
	srv      *httptest.Server
	tls      *httptest.Server
	seed     string
	goos     string
	status   int // list answer other than 200
	rels     []release.Release
	files    map[string][]byte           // "<tag>/<name>"
	override map[string]http.HandlerFunc // "<tag>/<name>"
	mu       sync.Mutex
	hits     []string
}

// newFake starts the servers and swaps the release keys for a generated one.
func newFake(t *testing.T, goos string) (*fake, *Installer) {
	t.Helper()
	pub, seed, err := relsig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	saved := relsig.ReleaseKeys
	relsig.ReleaseKeys = []string{pub}
	t.Cleanup(func() { relsig.ReleaseKeys = saved })

	f := &fake{t: t, seed: seed, goos: goos, files: map[string][]byte{}, override: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.tls = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	t.Cleanup(f.tls.Close)

	in := New(goos)
	in.ReleasesURL = f.srv.URL + "/releases"
	in.DownloadBase = f.srv.URL + "/download/"
	in.AllowedHosts = []string{"127.0.0.1"}
	in.Transport = f.tls.Client().Transport // trusts the TLS server, speaks plain http too
	return f, in
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r.URL.Path)
	f.mu.Unlock()
	if r.URL.Path == "/releases" {
		if f.status != 0 {
			http.Error(w, `{"message":"no"}`, f.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.rels)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/download/")
	if h, ok := f.override[key]; ok {
		h(w, r)
		return
	}
	if b, ok := f.files[key]; ok {
		_, _ = w.Write(b)
		return
	}
	http.NotFound(w, r)
}

// count is how many requests hit a path containing sub.
func (f *fake) count(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		if strings.Contains(h, sub) {
			n++
		}
	}
	return n
}

// publish adds a release (newest first is the caller's order) with the
// platform asset holding payload and a correctly signed checksums.txt. It
// returns the asset name.
func (f *fake) publish(tag string, payload []byte, flags ...string) string {
	f.t.Helper()
	v, ok := TagVersion(tag)
	if !ok {
		f.t.Fatalf("bad tag %s", tag)
	}
	name, _ := AssetName(f.goos, v)
	sum := sha256.Sum256(payload)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	sig, err := relsig.Sign(f.seed, sums)
	if err != nil {
		f.t.Fatal(err)
	}
	f.files[tag+"/"+name] = payload
	f.files[tag+"/checksums.txt"] = sums
	f.files[tag+"/checksums.txt.sig"] = sig
	r := release.Release{TagName: tag, Assets: f.assets(tag, name)}
	for _, fl := range flags {
		switch fl {
		case "draft":
			r.Draft = true
		case "prerelease":
			r.Prerelease = true
		}
	}
	f.rels = append(f.rels, r)
	return name
}

func (f *fake) assets(tag, name string) []release.Asset {
	b := f.srv.URL + "/download/" + tag + "/"
	return []release.Asset{
		{Name: name, BrowserDownloadURL: b + name},
		{Name: "checksums.txt", BrowserDownloadURL: b + "checksums.txt"},
		{Name: "checksums.txt.sig", BrowserDownloadURL: b + "checksums.txt.sig"},
	}
}
