package desktopinstall

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

const testTag = "desktop-v0.10.0"

// planFor publishes testTag with payload and returns its plan.
func planFor(t *testing.T, f *fake, in *Installer, payload []byte) Plan {
	t.Helper()
	f.publish(testTag, payload)
	p, err := in.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func noFile(t *testing.T, dir string) {
	t.Helper()
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("files left in the download dir: %v", ents)
	}
}

func TestDownloadHappyPath(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			f, in := newFake(t, goos)
			payload := bytes.Repeat([]byte("installer bytes "), 5000)
			p := planFor(t, f, in, payload)
			dir := t.TempDir()
			var lastDone, lastTotal int64
			calls := 0
			path, err := in.Download(context.Background(), p, dir, func(done, total int64) {
				calls++
				lastDone, lastTotal = done, total
			})
			if err != nil {
				t.Fatal(err)
			}
			if path != filepath.Join(dir, p.Asset) {
				t.Errorf("path = %s", path)
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, payload) {
				t.Error("file content differs")
			}
			if calls == 0 || lastDone != int64(len(payload)) || lastTotal != int64(len(payload)) {
				t.Errorf("progress: %d calls, last %d/%d", calls, lastDone, lastTotal)
			}
			if runtime.GOOS != "windows" {
				if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
					t.Errorf("mode = %v", fi.Mode())
				}
			}
			if _, ok := verified.Load(filepath.Clean(path)); !ok {
				t.Error("the verified file was not recorded")
			}
		})
	}
}

func TestDownloadRefusals(t *testing.T) {
	payload := []byte("the real installer")
	otherPub, otherSeed, _ := relsig.GenerateKey()
	_ = otherPub
	sumsFor := func(name string, data []byte) []byte {
		s := sha256.Sum256(data)
		return []byte(hex.EncodeToString(s[:]) + "  " + name + "\n")
	}
	cases := []struct {
		name      string
		mutate    func(f *fake, p Plan)
		assetHits int // requests for the big file
	}{
		{"signed by another key", func(f *fake, p Plan) {
			sig, _ := relsig.Sign(otherSeed, f.files[testTag+"/checksums.txt"])
			f.files[testTag+"/checksums.txt.sig"] = sig
		}, 0},
		{"missing signature", func(f *fake, p Plan) { delete(f.files, testTag+"/checksums.txt.sig") }, 0},
		{"missing checksums", func(f *fake, p Plan) { delete(f.files, testTag+"/checksums.txt") }, 0},
		{"asset not in the sums", func(f *fake, p Plan) {
			sums := sumsFor("other-file.exe", payload)
			sig, _ := relsig.Sign(f.seed, sums)
			f.files[testTag+"/checksums.txt"], f.files[testTag+"/checksums.txt.sig"] = sums, sig
		}, 0},
		{"sums edited after signing", func(f *fake, p Plan) {
			f.files[testTag+"/checksums.txt"] = sumsFor(p.Asset, []byte("evil"))
		}, 0},
		{"bad hash", func(f *fake, p Plan) { f.files[testTag+"/"+p.Asset] = []byte("tampered installer") }, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, in := newFake(t, "windows")
			p := planFor(t, f, in, payload)
			c.mutate(f, p)
			dir := t.TempDir()
			_, err := in.Download(context.Background(), p, dir, nil)
			if !errors.Is(err, ErrVerify) {
				t.Fatalf("err = %v, want ErrVerify", err)
			}
			if n := f.count(p.Asset); n != c.assetHits {
				t.Errorf("the big file was requested %d times, want %d", n, c.assetHits)
			}
			noFile(t, dir)
		})
	}
}

func TestDownloadSizeCap(t *testing.T) {
	old := maxDownload
	maxDownload = 1024
	t.Cleanup(func() { maxDownload = old })

	t.Run("content-length", func(t *testing.T) {
		f, in := newFake(t, "windows")
		p := planFor(t, f, in, bytes.Repeat([]byte("x"), 5000))
		f.override[testTag+"/"+p.Asset] = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "5000")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
		}
		dir := t.TempDir()
		_, err := in.Download(context.Background(), p, dir, nil)
		if !errors.Is(err, ErrVerify) || !strings.Contains(err.Error(), "limit") {
			t.Errorf("err = %v", err)
		}
		noFile(t, dir)
	})
	t.Run("streamed", func(t *testing.T) {
		f, in := newFake(t, "windows")
		payload := bytes.Repeat([]byte("x"), 5000)
		p := planFor(t, f, in, payload)
		f.override[testTag+"/"+p.Asset] = func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 5; i++ { // chunked: no Content-Length to refuse upfront
				_, _ = w.Write(payload[:1000])
				w.(http.Flusher).Flush()
			}
		}
		dir := t.TempDir()
		_, err := in.Download(context.Background(), p, dir, nil)
		if !errors.Is(err, ErrVerify) || !strings.Contains(err.Error(), "limit") {
			t.Errorf("err = %v", err)
		}
		noFile(t, dir)
	})
}

func TestDownloadInterruptedAndCancelled(t *testing.T) {
	t.Run("connection dropped mid-body", func(t *testing.T) {
		f, in := newFake(t, "windows")
		p := planFor(t, f, in, bytes.Repeat([]byte("y"), 10000))
		f.override[testTag+"/"+p.Asset] = func(w http.ResponseWriter, r *http.Request) {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			bw := bufio.NewWriter(buf)
			fmt.Fprintf(bw, "HTTP/1.1 200 OK\r\nContent-Length: 10000\r\n\r\n%s", strings.Repeat("y", 100))
			bw.Flush()
			conn.Close()
		}
		dir := t.TempDir()
		_, err := in.Download(context.Background(), p, dir, nil)
		if !errors.Is(err, ErrNetwork) {
			t.Errorf("err = %v, want ErrNetwork", err)
		}
		noFile(t, dir)
	})
	t.Run("cancelled", func(t *testing.T) {
		f, in := newFake(t, "windows")
		p := planFor(t, f, in, bytes.Repeat([]byte("z"), 10000))
		f.override[testTag+"/"+p.Asset] = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10000")
			_, _ = w.Write([]byte(strings.Repeat("z", 100)))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir := t.TempDir()
		_, err := in.Download(ctx, p, dir, func(done, total int64) { cancel() })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		noFile(t, dir)
	})
}

func TestDownloadRedirectPolicy(t *testing.T) {
	payload := []byte("redirected installer")
	setup := func(t *testing.T, target func(f *fake, key string) string) (*Installer, Plan, string) {
		f, in := newFake(t, "windows")
		p := planFor(t, f, in, payload)
		key := testTag + "/" + p.Asset
		real := f.files[key]
		f.files[testTag+"/cdn-copy"] = real
		f.override[key] = func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target(f, r.URL.RawQuery), http.StatusFound)
		}
		return in, p, t.TempDir()
	}

	t.Run("allowed https host", func(t *testing.T) {
		in, p, dir := setup(t, func(f *fake, _ string) string { return f.tls.URL + "/download/" + testTag + "/cdn-copy" })
		path, err := in.Download(context.Background(), p, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, payload) {
			t.Error("content differs")
		}
	})
	t.Run("http", func(t *testing.T) {
		in, p, dir := setup(t, func(f *fake, _ string) string { return f.srv.URL + "/download/" + testTag + "/cdn-copy" })
		_, err := in.Download(context.Background(), p, dir, nil)
		if !errors.Is(err, ErrNetwork) || !strings.Contains(err.Error(), "not https") {
			t.Errorf("err = %v", err)
		}
		noFile(t, dir)
	})
	t.Run("host not listed", func(t *testing.T) {
		in, p, dir := setup(t, func(f *fake, _ string) string {
			return strings.Replace(f.tls.URL, "127.0.0.1", "localhost", 1) + "/download/" + testTag + "/cdn-copy"
		})
		_, err := in.Download(context.Background(), p, dir, nil)
		if !errors.Is(err, ErrNetwork) || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("err = %v", err)
		}
		noFile(t, dir)
	})
	t.Run("too many hops", func(t *testing.T) {
		in, p, dir := setup(t, func(f *fake, q string) string {
			n, _ := strconv.Atoi(strings.TrimPrefix(q, "n="))
			return f.tls.URL + "/download/" + testTag + "/" + p0(f) + "?n=" + strconv.Itoa(n+1)
		})
		_, err := in.Download(context.Background(), p, dir, nil)
		if !errors.Is(err, ErrNetwork) || !strings.Contains(err.Error(), "redirects") {
			t.Errorf("err = %v", err)
		}
		noFile(t, dir)
	})
}

// p0 is the asset name of the fake's only release.
func p0(f *fake) string {
	v, _ := TagVersion(testTag)
	n, _ := AssetName(f.goos, v)
	return n
}

func TestDownloadRefusesAForeignPlan(t *testing.T) {
	f, in := newFake(t, "windows")
	good := planFor(t, f, in, []byte("p"))
	mut := []func(p *Plan){
		func(p *Plan) { p.AssetURL = "https://evil.example/" + p.Asset },
		func(p *Plan) { p.SumsURL = "https://evil.example/checksums.txt" },
		func(p *Plan) { p.SigURL = strings.Replace(p.SigURL, "checksums.txt.sig", "other.sig", 1) },
		func(p *Plan) { p.Asset = "foxmayn-frappe-desktop-0.10.0-windows-amd64-setup.exe.bak" },
		func(p *Plan) { p.Tag = "desktop-v0.9.0" },
		func(p *Plan) { p.Tag = "v0.10.0" },
	}
	before := len(f.hits)
	for i, m := range mut {
		p := good
		m(&p)
		if _, err := in.Download(context.Background(), p, t.TempDir(), nil); !errors.Is(err, ErrAsset) {
			t.Errorf("mutation %d: err = %v, want ErrAsset", i, err)
		}
	}
	if len(f.hits) != before {
		t.Error("a refused plan still made requests")
	}
}

func TestNewTempDir(t *testing.T) {
	dir, err := NewTempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("%v %v", fi, err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if !strings.HasPrefix(filepath.Base(dir), "ffd-update-") {
		t.Errorf("name = %s", dir)
	}
}
