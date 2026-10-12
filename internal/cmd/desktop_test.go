package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/desktopinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

const (
	testBundle = "Foxmayn Frappe Desktop.app"
	testBundID = "com.foxmayn.frappedesktop"
	testExec   = "foxmayn-frappe-desktop"
)

// desktopRig is a fake GitHub (release list and files, signed with a
// generated key), a recorded macOS toolchain and a temp "Applications" folder.
type desktopRig struct {
	t      *testing.T
	srv    *httptest.Server
	seed   string
	mu     sync.Mutex
	hits   []string
	status int // list answer other than 200
	rels   []release.Release
	files  map[string][]byte

	calls  [][]string
	onRun  func(tool string) // called with the tool's base name, before it runs
	plist  map[string]string
	target desktopinstall.Target
	locErr error
}

// newDesktopRig starts the fake, swaps the release keys and the desktop
// seams, and points the cache folder at a temp dir. installed is the version
// of the app in the temp Applications folder ("" = not installed).
func newDesktopRig(t *testing.T, installed string) *desktopRig {
	t.Helper()
	pub, seed, err := relsig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	savedKeys := relsig.ReleaseKeys
	relsig.ReleaseKeys = []string{pub}
	t.Cleanup(func() { relsig.ReleaseKeys = savedKeys })

	r := &desktopRig{t: t, seed: seed, files: map[string][]byte{}, plist: map[string]string{
		"CFBundleIdentifier": testBundID,
		"CFBundleExecutable": testExec,
	}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)

	cache := t.TempDir()
	for _, k := range []string{"LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(k, cache)
	}

	apps := filepath.Join(t.TempDir(), "Applications")
	r.target = desktopinstall.Target{GOOS: "darwin", Path: filepath.Join(apps, testBundle)}
	if installed != "" {
		r.target.Installed, r.target.Version = true, installed
		r.write(filepath.Join(r.target.Path, "Contents", "marker"), "old")
	}

	savedGOOS, savedNew, savedLocate := desktopGOOS, newDesktopInstaller, desktopLocate
	t.Cleanup(func() { desktopGOOS, newDesktopInstaller, desktopLocate = savedGOOS, savedNew, savedLocate })
	desktopGOOS = "darwin"
	newDesktopInstaller = func(goos string) *desktopinstall.Installer {
		in := desktopinstall.New(goos)
		in.ReleasesURL = r.srv.URL + "/releases"
		in.DownloadBase = r.srv.URL + "/download/"
		in.AllowedHosts = []string{"127.0.0.1"}
		in.Run = r.run
		return in
	}
	desktopLocate = func(*desktopinstall.Installer, string) (desktopinstall.Target, error) {
		return r.target, r.locErr
	}
	return r
}

func (r *desktopRig) write(p, s string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *desktopRig) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.hits = append(r.hits, req.URL.Path)
	r.mu.Unlock()
	if req.URL.Path == "/releases" {
		if r.status != 0 {
			http.Error(w, `{"message":"limit"}`, r.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.rels)
		return
	}
	if b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/download/")]; ok {
		_, _ = w.Write(b)
		return
	}
	http.NotFound(w, req)
}

// downloads is how many requests asked for a file (not the list).
func (r *desktopRig) downloads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, h := range r.hits {
		if strings.HasPrefix(h, "/download/") {
			n++
		}
	}
	return n
}

// publish adds the desktop release tag with a signed checksums.txt.
func (r *desktopRig) publish(version string) {
	r.t.Helper()
	tag := "desktop-v" + version
	v, _ := desktopinstall.TagVersion(tag)
	name, _ := desktopinstall.AssetName("darwin", v)
	payload := []byte("disk image of " + version)
	sum := sha256.Sum256(payload)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	sig, err := relsig.Sign(r.seed, sums)
	if err != nil {
		r.t.Fatal(err)
	}
	r.files[tag+"/"+name] = payload
	r.files[tag+"/checksums.txt"] = sums
	r.files[tag+"/checksums.txt.sig"] = sig
	base := r.srv.URL + "/download/" + tag + "/"
	r.rels = append(r.rels, release.Release{TagName: tag, Assets: []release.Asset{
		{Name: name, BrowserDownloadURL: base + name},
		{Name: "checksums.txt", BrowserDownloadURL: base + "checksums.txt"},
		{Name: "checksums.txt.sig", BrowserDownloadURL: base + "checksums.txt.sig"},
	}})
	r.plist["CFBundleShortVersionString"] = version
}

// run plays hdiutil, ditto, codesign, plutil and xattr against the real
// file system and records every call.
func (r *desktopRig) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if path.Dir(name) != "/usr/bin" {
		r.t.Errorf("%s is not run by absolute path under /usr/bin", name)
	}
	name = path.Base(name)
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	if r.onRun != nil {
		r.onRun(name)
	}
	if name != "hdiutil" || args[0] != "detach" { // the unmount is deliberately uncancellable
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	switch name {
	case "hdiutil":
		if args[0] == "attach" {
			r.write(filepath.Join(args[len(args)-2], testBundle, "Contents", "marker"), "new")
		}
	case "ditto":
		if err := os.CopyFS(args[1], os.DirFS(args[0])); err != nil {
			return nil, err
		}
	case "plutil":
		return []byte(r.plist[args[1]] + "\n"), nil
	}
	return nil, nil
}

func (r *desktopRig) marker() string {
	b, err := os.ReadFile(filepath.Join(r.target.Path, "Contents", "marker"))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// tempDirs counts the private download folders left in the cache dir.
func tempDirs(t *testing.T) int {
	t.Helper()
	root, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && strings.HasPrefix(d.Name(), "ffd-update-") {
			n++
		}
		return nil
	})
	return n
}

func jsonObj(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, s)
	}
	return m
}

func TestDesktopRefusedOnLinuxBeforeAnyRequest(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	desktopGOOS = "linux"
	newDesktopInstaller = func(string) *desktopinstall.Installer {
		t.Fatal("installer built on linux")
		return nil
	}
	for _, args := range [][]string{{"desktop", "status"}, {"desktop", "install", "--yes"}} {
		res := runFFC(t, "", "", args...)
		if res.Code != exitUsage {
			t.Errorf("%v: exit %d, want 2; err %v", args, res.Code, res.Err)
		}
		if !strings.Contains(res.Stderr, "only available for Windows and macOS") {
			t.Errorf("%v: stderr %q", args, res.Stderr)
		}
	}
	if len(r.hits) != 0 {
		t.Errorf("requests on linux: %v", r.hits)
	}
}

func TestDesktopStatus(t *testing.T) {
	cases := []struct {
		name, installed, latest string
		wantInstalled, wantUpd  bool
	}{
		{"update", "0.9.0", "0.10.0", true, true},
		{"up to date", "0.10.0", "0.10.0", true, false},
		{"newer than release", "0.11.0", "0.10.0", true, false},
		{"not installed", "", "0.10.0", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newDesktopRig(t, c.installed)
			r.publish(c.latest)
			res := runFFC(t, "", "", "desktop", "status", "--json")
			if res.Code != 0 {
				t.Fatalf("exit %d: %v %s", res.Code, res.Err, res.Stderr)
			}
			m := jsonObj(t, res.Stdout)
			if m["installed"] != c.wantInstalled || m["update_available"] != c.wantUpd ||
				m["installed_version"] != c.installed || m["latest"] != c.latest ||
				m["path"] != r.target.Path ||
				!strings.Contains(fmt.Sprint(m["release_url"]), "desktop-v"+c.latest) {
				t.Errorf("status = %v", m)
			}
			if r.downloads() != 0 {
				t.Errorf("status downloaded files: %v", r.hits)
			}
		})
	}
}

func TestDesktopStatusFormats(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	if res := runFFC(t, "", "", "desktop", "status", "--output", "yaml"); res.Code != 0 ||
		!strings.Contains(res.Stdout, "update_available: true") || !strings.Contains(res.Stdout, "latest: 0.10.0") {
		t.Errorf("yaml: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	if res := runFFC(t, "", "", "desktop", "status", "--jq", ".latest"); res.Code != 0 || strings.TrimSpace(res.Stdout) != "0.10.0" {
		t.Errorf("jq: exit %d %q", res.Code, res.Stdout)
	}
	if res := runFFC(t, "", "", "desktop", "status"); res.Code != 0 || !strings.Contains(res.Stdout, "0.10.0") {
		t.Errorf("table: exit %d\n%s", res.Code, res.Stdout)
	}
}

func TestDesktopInstallDryRunDownloadsNothing(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	res := runFFC(t, "", "", "desktop", "install", "--dry-run", "--json")
	if res.Code != 0 {
		t.Fatalf("exit %d: %v %s", res.Code, res.Err, res.Stderr)
	}
	m := jsonObj(t, res.Stdout)
	if m["action"] != "update" || m["from"] != "0.9.0" || m["to"] != "0.10.0" || m["dry_run"] != true ||
		m["asset"] != "foxmayn-frappe-desktop-0.10.0-macos-universal.dmg" || m["path"] != r.target.Path {
		t.Errorf("plan = %v", m)
	}
	if r.downloads() != 0 || len(r.calls) != 0 || r.marker() != "old" {
		t.Errorf("dry run touched things: hits %v calls %v marker %q", r.hits, r.calls, r.marker())
	}
}

func TestDesktopInstallWithoutYesAndNoTerminal(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	res := runFFC(t, "", "", "desktop", "install")
	if res.Code != exitUsage || !strings.Contains(res.Stderr, "--yes") {
		t.Errorf("exit %d, stderr %q", res.Code, res.Stderr)
	}
	if r.downloads() != 0 || r.marker() != "old" {
		t.Errorf("downloaded or changed without confirmation: %v %q", r.hits, r.marker())
	}
}

func TestDesktopInstallYesUpdates(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.9.5")
	r.publish("0.10.0")
	res := runFFC(t, "", "", "desktop", "install", "--yes", "--json")
	if res.Code != 0 {
		t.Fatalf("exit %d: %v\n%s", res.Code, res.Err, res.Stderr)
	}
	m := jsonObj(t, res.Stdout)
	if m["action"] != "update" || m["version"] != "0.10.0" || m["path"] != r.target.Path || m["restart_needed"] != true {
		t.Errorf("result = %v", m)
	}
	if !strings.Contains(res.Stderr, "restart it") {
		t.Errorf("no restart hint on stderr: %q", res.Stderr)
	}
	if got := r.marker(); got != "new" {
		t.Errorf("bundle marker = %q, want new", got)
	}
	var order []string
	for _, c := range r.calls {
		order = append(order, c[0])
	}
	if got := strings.Join(order, " "); !strings.HasPrefix(got, "hdiutil ditto codesign plutil") ||
		!strings.HasSuffix(got, "hdiutil") {
		t.Errorf("command order = %s", got)
	}
	if n := tempDirs(t); n != 0 {
		t.Errorf("%d private download folders left behind", n)
	}
}

func TestDesktopInstallFresh(t *testing.T) {
	r := newDesktopRig(t, "")
	r.publish("0.10.0")
	res := runFFC(t, "", "", "desktop", "install", "--yes", "--json")
	if res.Code != 0 {
		t.Fatalf("exit %d: %v\n%s", res.Code, res.Err, res.Stderr)
	}
	m := jsonObj(t, res.Stdout)
	if m["action"] != "install" || m["version"] != "0.10.0" || m["restart_needed"] != false {
		t.Errorf("result = %v", m)
	}
	if r.marker() != "new" {
		t.Errorf("bundle marker = %q", r.marker())
	}
	if strings.Contains(res.Stderr, "restart it") {
		t.Errorf("restart hint after a fresh install: %q", res.Stderr)
	}
}

func TestDesktopInstallUpToDateDownloadsNothing(t *testing.T) {
	for _, installed := range []string{"0.10.0", "0.11.0"} {
		r := newDesktopRig(t, installed)
		r.publish("0.10.0")
		res := runFFC(t, "", "", "desktop", "install", "--yes", "--json")
		if res.Code != 0 {
			t.Fatalf("%s: exit %d: %v", installed, res.Code, res.Err)
		}
		m := jsonObj(t, res.Stdout)
		if m["action"] != "none" || m["version"] != installed || m["restart_needed"] != false {
			t.Errorf("%s: result = %v", installed, m)
		}
		if r.downloads() != 0 || len(r.calls) != 0 || r.marker() != "old" {
			t.Errorf("%s: touched things: %v %v %q", installed, r.hits, r.calls, r.marker())
		}
	}
}

func TestDesktopInstallBadSignatureInstallsNothing(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	r.files["desktop-v0.10.0/checksums.txt.sig"] = []byte("not a signature")
	res := runFFC(t, "", "", "desktop", "install", "--yes")
	if res.Code != exitGeneric {
		t.Errorf("exit %d, want 1; err %v", res.Code, res.Err)
	}
	if !errors.Is(res.Err, desktopinstall.ErrVerify) {
		t.Errorf("err = %v, want ErrVerify", res.Err)
	}
	if len(r.calls) != 0 || r.marker() != "old" || fileExists(r.target.Path+".old") || fileExists(r.target.Path+".new") {
		t.Errorf("something was applied: calls %v marker %q", r.calls, r.marker())
	}
	if n := tempDirs(t); n != 0 {
		t.Errorf("%d private download folders left behind", n)
	}
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestDesktopRateLimitedIsNetworkExit(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.status = http.StatusTooManyRequests
	for _, args := range [][]string{{"desktop", "status"}, {"desktop", "install", "--yes"}} {
		res := runFFC(t, "", "", args...)
		if res.Code != exitNetwork {
			t.Errorf("%v: exit %d, want 7; err %v", args, res.Code, res.Err)
		}
	}
}

func TestDesktopUnwritableIsPermissionExit(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	r.locErr = fmt.Errorf("%w: /Applications", desktopinstall.ErrNotWritable)
	res := runFFC(t, "", "", "desktop", "install", "--yes")
	if res.Code != exitPermission {
		t.Errorf("exit %d, want 5; err %v", res.Code, res.Err)
	}
	if len(r.hits) != 0 {
		t.Errorf("requests before the location check: %v", r.hits)
	}
}

func TestDesktopErrorJSONShape(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.status = http.StatusTooManyRequests
	res := runFFC(t, "", "", "desktop", "status", "--json")
	if res.Code != exitNetwork {
		t.Fatalf("exit %d", res.Code)
	}
	var out errorJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stderr)), &out); err != nil {
		t.Fatalf("stderr is not the JSON error shape: %v\n%s", err, res.Stderr)
	}
	if out.Error.ExitCode != exitNetwork || out.Error.Code == "" || !strings.Contains(out.Error.Message, "rate limit") {
		t.Errorf("error = %+v", out.Error)
	}
}

func TestDesktopErrorMapping(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{desktopinstall.ErrUnsupported, exitUsage},
		{fmt.Errorf("x: %w", desktopinstall.ErrNetwork), exitNetwork},
		{fmt.Errorf("x: %w", desktopinstall.ErrRateLimited), exitNetwork},
		{fmt.Errorf("x: %w", desktopinstall.ErrNotWritable), exitPermission},
		{fmt.Errorf("x: %w", desktopinstall.ErrVerify), exitGeneric},
		{fmt.Errorf("x: %w", desktopinstall.ErrAsset), exitGeneric},
		{fmt.Errorf("x: %w", desktopinstall.ErrInstall), exitGeneric},
		{desktopinstall.ErrNoRelease, exitGeneric},
		{fmt.Errorf("x: %w", desktopinstall.ErrNotInstalledCopy), exitGeneric},
		{fmt.Errorf("x: %w", context.Canceled), exitInterrupted},
		{errAborted, classifyCode(errAborted)},
	} {
		if got, _ := classify(desktopError(c.err)); got != c.want {
			t.Errorf("%v -> %d, want %d", c.err, got, c.want)
		}
	}
}

func classifyCode(err error) int { c, _ := classify(err); return c }

// Ctrl-C while the install runs (after the download) must not stop it: the
// copy and the swap finish, as the app's own update does.
func TestDesktopInstallApplyIgnoresCancel(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.onRun = func(tool string) {
		if tool == "ditto" {
			cancel()
		}
	}
	res := runFFCCtx(t, ctx, "", "", "desktop", "install", "--yes", "--json")
	if res.Code != 0 {
		t.Fatalf("exit %d: %v\n%s", res.Code, res.Err, res.Stderr)
	}
	if got := r.marker(); got != "new" {
		t.Errorf("bundle marker = %q, want new (the install was cut short)", got)
	}
}

// The download is the part that Ctrl-C may stop, and then nothing is applied.
func TestDesktopInstallCancelledBeforeApplyInstallsNothing(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := runFFCCtx(t, ctx, "", "", "desktop", "install", "--yes", "--json")
	if res.Code == 0 || len(r.calls) != 0 || r.marker() != "old" {
		t.Errorf("exit %d, calls %v, marker %q", res.Code, r.calls, r.marker())
	}
}

// An installed copy whose version cannot be read is never replaced silently:
// the newest release might be older than it.
func TestDesktopInstallUnknownInstalledVersion(t *testing.T) {
	for _, ver := range []string{"", "banana"} {
		t.Run("version "+ver, func(t *testing.T) {
			r := newDesktopRig(t, "0.11.0")
			r.target.Version = ver
			r.publish("0.10.0")

			res := runFFC(t, "", "", "desktop", "install", "--yes")
			if res.Code != exitGeneric || !strings.Contains(res.Stderr, "could not read") || !strings.Contains(res.Stderr, "--force") ||
				!strings.Contains(res.Stderr, "0.10.0") {
				t.Errorf("exit %d, stderr %q", res.Code, res.Stderr)
			}
			if r.downloads() != 0 || len(r.calls) != 0 || r.marker() != "old" {
				t.Errorf("touched things: %v %v %q", r.hits, r.calls, r.marker())
			}
			// --dry-run tells the truth too: the real run would refuse.
			if res := runFFC(t, "", "", "desktop", "install", "--dry-run"); res.Code != exitGeneric {
				t.Errorf("dry run without --force: exit %d", res.Code)
			}

			res = runFFC(t, "", "", "desktop", "install", "--force", "--dry-run", "--json")
			if res.Code != 0 {
				t.Fatalf("dry run: exit %d: %v %s", res.Code, res.Err, res.Stderr)
			}
			if m := jsonObj(t, res.Stdout); m["from"] != "unknown" || m["to"] != "0.10.0" || m["action"] != "update" {
				t.Errorf("plan = %v", m)
			}
			if len(r.calls) != 0 {
				t.Errorf("dry run ran %v", r.calls)
			}

			res = runFFC(t, "", "", "desktop", "install", "--force", "--yes", "--json")
			if res.Code != 0 {
				t.Fatalf("forced: exit %d: %v %s", res.Code, res.Err, res.Stderr)
			}
			if m := jsonObj(t, res.Stdout); m["action"] != "update" || m["version"] != "0.10.0" {
				t.Errorf("result = %v", m)
			}
			if r.marker() != "new" {
				t.Errorf("marker = %q", r.marker())
			}
		})
	}
}

// The CLI leaves the previous bundle as .old (the app may be running from
// it); Apply replaces what an earlier update left.
func TestDesktopInstallKeepsPreviousCopyAsOld(t *testing.T) {
	r := newDesktopRig(t, "0.9.0")
	r.publish("0.10.0")
	r.write(filepath.Join(r.target.Path+".old", "Contents", "marker"), "stale")
	if res := runFFC(t, "", "", "desktop", "install", "--yes"); res.Code != 0 {
		t.Fatalf("exit %d: %v %s", res.Code, res.Err, res.Stderr)
	}
	b, err := os.ReadFile(filepath.Join(r.target.Path+".old", "Contents", "marker"))
	if err != nil || string(b) != "old" {
		t.Errorf(".old = %q, %v; want the replaced copy", b, err)
	}
	if fileExists(r.target.Path + ".new") {
		t.Error(".new left behind")
	}
}

// status follows the same pick rule as install: a flagged prerelease with a
// plain number is not offered to a stable 1.x install.
func TestDesktopStatusUsesTheInstallPickRule(t *testing.T) {
	r := newDesktopRig(t, "1.0.0")
	r.publish("1.1.0")
	r.publish("1.2.0")
	r.rels[1].Prerelease = true
	res := runFFC(t, "", "", "desktop", "status", "--json")
	if res.Code != 0 {
		t.Fatalf("exit %d: %v", res.Code, res.Err)
	}
	if m := jsonObj(t, res.Stdout); m["latest"] != "1.1.0" || m["update_available"] != true {
		t.Errorf("status = %v", m)
	}
}
