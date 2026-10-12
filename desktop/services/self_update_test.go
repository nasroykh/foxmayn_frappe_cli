package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/desktopinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

// updFake is a fake GitHub (release list and files) plus an AppService that
// installs from it into a temp "installed" Windows exe, with Run and start
// recorded.
type updFake struct {
	t        *testing.T
	srv      *httptest.Server
	seed     string
	files    map[string][]byte
	override map[string]http.HandlerFunc
	rels     []release.Release
	status   int

	svc  *AppService
	host *fakeHost
	exe  string

	mu       sync.Mutex
	ran      [][]string
	started  [][]string
	runErr   error
	startErr error
}

func newUpdFake(t *testing.T, cur string) *updFake {
	t.Helper()
	pub, seed, err := relsig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	saved := relsig.ReleaseKeys
	relsig.ReleaseKeys = []string{pub}
	t.Cleanup(func() { relsig.ReleaseKeys = saved })

	f := &updFake{t: t, seed: seed, files: map[string][]byte{}, override: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)

	f.host = &fakeHost{}
	s := NewAppService(f.host, "/x/config.yaml", fakeLocator("windows", `C:\Users\me`, "", nil))
	s.goos = "windows"
	s.version = cur
	s.releasesURL = f.srv.URL + "/releases"
	in := desktopinstall.New("windows")
	in.ReleasesURL = f.srv.URL + "/releases"
	in.DownloadBase = f.srv.URL + "/download/"
	in.AllowedHosts = []string{"127.0.0.1"}
	in.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		f.mu.Lock()
		f.ran = append(f.ran, append([]string{name}, args...))
		err := f.runErr
		f.mu.Unlock()
		if err != nil {
			return nil, err
		}
		// The setup writes the new exe.
		return nil, os.WriteFile(f.exe, []byte("new"), 0o755)
	}
	f.exe = filepath.Join(t.TempDir(), "foxmayn-frappe-desktop.exe")
	if err := os.WriteFile(f.exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.selfUpdate = in
	s.exe = func() (string, error) { return f.exe, nil }
	s.locate = func(goos, exe string) (desktopinstall.Target, error) {
		return desktopinstall.Target{GOOS: "windows", Path: exe, Installed: true}, nil
	}
	s.tempDir = func() (string, error) { return os.MkdirTemp(t.TempDir(), "upd-") }
	s.start = func(name string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.started = append(f.started, append([]string{name}, args...))
		return f.startErr
	}
	f.svc = s
	return f
}

func (f *updFake) serve(w http.ResponseWriter, r *http.Request) {
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

// publish adds a signed release holding payload; it returns the asset name.
func (f *updFake) publish(version string, payload []byte) string {
	f.t.Helper()
	tag := "desktop-v" + version
	v, _ := desktopinstall.ParseVersion(version)
	name, _ := desktopinstall.AssetName("windows", v)
	sum := sha256.Sum256(payload)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	sig, err := relsig.Sign(f.seed, sums)
	if err != nil {
		f.t.Fatal(err)
	}
	f.files[tag+"/"+name] = payload
	f.files[tag+"/checksums.txt"] = sums
	f.files[tag+"/checksums.txt.sig"] = sig
	b := f.srv.URL + "/download/" + tag + "/"
	f.rels = append(f.rels, release.Release{TagName: tag, Assets: []release.Asset{
		{Name: name, BrowserDownloadURL: b + name},
		{Name: "checksums.txt", BrowserDownloadURL: b + "checksums.txt"},
		{Name: "checksums.txt.sig", BrowserDownloadURL: b + "checksums.txt.sig"},
	}})
	return name
}

func (f *updFake) steps() []string {
	var out []string
	for _, d := range f.host.named(EventUpdateProgress) {
		step := d.(UpdateProgress).Step
		if len(out) == 0 || out[len(out)-1] != step {
			out = append(out, step)
		}
	}
	return out
}

func wantUpdateError(t *testing.T, err error, code, key string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code || e.Key != key {
		t.Fatalf("err = %v, want %s/%s", err, code, key)
	}
	if _, ok := errorKeys[key]; !ok {
		t.Fatalf("%s is not an error key", key)
	}
	return e
}

func TestInstallUpdateSuccess(t *testing.T) {
	f := newUpdFake(t, "0.1.0")
	f.publish("0.1.0", []byte("old release"))
	asset := f.publish("0.2.0", []byte(strings.Repeat("setup bytes ", 1000)))
	// GitHub lists newest first; the pick must not depend on it.
	f.rels[0], f.rels[1] = f.rels[1], f.rels[0]

	res, err := f.svc.InstallUpdate(context.Background())
	if err != nil || res.Version != "0.2.0" {
		t.Fatalf("got %+v, %v", res, err)
	}
	if len(f.ran) != 1 || filepath.Base(f.ran[0][0]) != asset || f.ran[0][1] != "/S" {
		t.Errorf("setup run = %v", f.ran)
	}
	// The new copy is started at the path LocateSelf gave, nothing else.
	want := []string{f.exe, "--after-update", strconv.Itoa(os.Getpid())}
	if len(f.started) != 1 || strings.Join(f.started[0], "|") != strings.Join(want, "|") {
		t.Errorf("started = %v, want %v", f.started, want)
	}
	if f.host.quits != 1 {
		t.Errorf("quits = %d", f.host.quits)
	}
	steps := strings.Join(f.steps(), ",")
	if steps != "checking,downloading,verifying,installing,restarting" {
		t.Errorf("steps = %s", steps)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "new" {
		t.Errorf("exe = %q", b)
	}
}

func TestInstallUpdateRelaunchDarwin(t *testing.T) {
	f := newUpdFake(t, "0.1.0")
	if err := f.svc.relaunch(desktopinstall.Target{GOOS: "darwin", Path: "/Applications/Foxmayn Frappe Desktop.app"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/open", "-n", "/Applications/Foxmayn Frappe Desktop.app", "--args", "--after-update", strconv.Itoa(os.Getpid())}
	if strings.Join(f.started[0], "|") != strings.Join(want, "|") {
		t.Errorf("started = %v", f.started)
	}
}

func TestInstallUpdateBusy(t *testing.T) {
	f := newUpdFake(t, "0.1.0")
	f.svc.updateMu.Lock()
	defer f.svc.updateMu.Unlock()
	_, err := f.svc.InstallUpdate(context.Background())
	wantUpdateError(t, err, CodeUnavailable, "update.busy")
}

func TestInstallUpdateFailuresInstallNothing(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *updFake)
		code  string
		key   string
	}{
		{"not newer", func(f *updFake) { f.publish("0.1.0", []byte("x")) }, CodeUnavailable, "update.notNewer"},
		{"no release", func(f *updFake) {}, CodeFailed, "update.noRelease"},
		{"rate limited", func(f *updFake) { f.status = 429 }, CodeUnavailable, "update.rateLimited"},
		{"server error", func(f *updFake) { f.status = 500 }, CodeNetwork, "update.network"},
		{"bad signature", func(f *updFake) {
			f.publish("0.2.0", []byte("x"))
			f.files["desktop-v0.2.0/checksums.txt.sig"] = []byte("not a signature")
		}, CodeFailed, "update.verify"},
		{"bad hash", func(f *updFake) {
			name := f.publish("0.2.0", []byte("x"))
			f.files["desktop-v0.2.0/"+name] = []byte("tampered")
		}, CodeFailed, "update.verify"},
		{"not installed copy", func(f *updFake) {
			f.publish("0.2.0", []byte("x"))
			f.svc.locate = func(string, string) (desktopinstall.Target, error) {
				return desktopinstall.Target{}, desktopinstall.ErrNotInstalledCopy
			}
		}, CodeUnavailable, "update.notInstalled"},
		{"not writable", func(f *updFake) {
			f.publish("0.2.0", []byte("x"))
			f.svc.locate = func(string, string) (desktopinstall.Target, error) {
				return desktopinstall.Target{}, desktopinstall.ErrNotWritable
			}
		}, CodeUnavailable, "update.notWritable"},
		{"dev build", func(f *updFake) { f.publish("0.2.0", []byte("x")); f.svc.version = "0.0.0-dev" }, CodeUnavailable, "update.notInstalled"},
		{"installer fails", func(f *updFake) { f.publish("0.2.0", []byte("x")); f.runErr = errors.New("exit status 2") }, CodeFailed, "update.install"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newUpdFake(t, "0.1.0")
			c.setup(f)
			_, err := f.svc.InstallUpdate(context.Background())
			e := wantUpdateError(t, err, c.code, c.key)
			if e.Message != errorKeys[c.key] {
				t.Errorf("message = %q", e.Message)
			}
			if len(f.started) != 0 || f.host.quits != 0 {
				t.Errorf("restarted after a failure: started %v quits %d", f.started, f.host.quits)
			}
			if b, _ := os.ReadFile(f.exe); string(b) != "old" {
				t.Errorf("exe = %q, want the old copy kept", b)
			}
		})
	}
}

func TestInstallUpdateCancelBeforeInstall(t *testing.T) {
	f := newUpdFake(t, "0.1.0")
	name := f.publish("0.2.0", []byte("x"))
	started := make(chan struct{})
	f.override["desktop-v0.2.0/"+name] = func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.InstallUpdate(ctx)
		done <- err
	}()
	<-started
	cancel()
	wantUpdateError(t, <-done, CodeCancelled, "update.cancelled")
	if len(f.ran) != 0 || len(f.started) != 0 || f.host.quits != 0 {
		t.Errorf("ran %v started %v quits %d", f.ran, f.started, f.host.quits)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "old" {
		t.Errorf("exe = %q", b)
	}
}

func TestInstallUpdateRelaunchFailureKeepsApp(t *testing.T) {
	f := newUpdFake(t, "0.1.0")
	f.publish("0.2.0", []byte("x"))
	f.startErr = errors.New("cannot start")
	res, err := f.svc.InstallUpdate(context.Background())
	wantUpdateError(t, err, CodeFailed, "update.relaunch")
	if res.Version != "0.2.0" || f.host.quits != 0 {
		t.Errorf("res %+v quits %d: the app must stay open when the new copy did not start", res, f.host.quits)
	}
}

func TestInstallUpdateTakesNoInput(t *testing.T) {
	// The bound method has nothing but ctx: no URL, path or version from the
	// web view can steer it.
	var _ func(context.Context) (UpdateInstall, error) = (*AppService)(nil).InstallUpdate
}

func TestActiveRunsWithoutStore(t *testing.T) {
	if n := NewAssistantService(&fakeHost{}, "/x/config.yaml").ActiveRuns(); n != 0 {
		t.Errorf("ActiveRuns = %d", n)
	}
}
