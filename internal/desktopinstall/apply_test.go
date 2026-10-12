package desktopinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// downloaded runs the fake release through Download and returns the pieces
// Apply needs.
func downloaded(t *testing.T, goos string) (*Installer, string, Version) {
	t.Helper()
	f, in := newFake(t, goos)
	p := planFor(t, f, in, []byte("dmg or setup bytes"))
	path, err := in.Download(context.Background(), p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return in, path, p.Version
}

// macRig records every command and plays hdiutil, ditto, plutil and xattr
// against the real file system.
type macRig struct {
	t         *testing.T
	calls     [][]string
	fail      map[string]error  // "hdiutil attach", "ditto", "codesign", "plutil <key>", "xattr", "hdiutil detach"
	plist     map[string]string // value per plist key
	xattr     string
	mountLink string // when set, the mounted "app" is a symlink to it
}

func (r *macRig) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	key := name
	switch name {
	case "hdiutil":
		key += " " + args[0]
	case "plutil":
		key += " " + args[1]
	}
	if err, ok := r.fail[key]; ok {
		return []byte("simulated failure"), err
	}
	switch key {
	case "hdiutil attach":
		mnt := args[len(args)-2]
		app := filepath.Join(mnt, bundleApp)
		if r.mountLink != "" {
			if err := os.Symlink(r.mountLink, app); err != nil {
				r.t.Skipf("symlinks unavailable: %v", err)
			}
			return nil, nil
		}
		write(r.t, filepath.Join(app, "Contents", "marker"), "new")
	case "ditto":
		if err := os.CopyFS(args[1], os.DirFS(args[0])); err != nil {
			return nil, err
		}
	case "xattr":
		return []byte(r.xattr), nil
	default:
		if name == "plutil" {
			return []byte(r.plist[args[1]] + "\n"), nil
		}
	}
	return nil, nil
}

func newMacRig(t *testing.T) (*macRig, *Installer, string, Version) {
	in, asset, v := downloaded(t, "darwin")
	r := &macRig{t: t, fail: map[string]error{}, plist: map[string]string{
		"CFBundleIdentifier":         bundleID,
		"CFBundleShortVersionString": v.String(),
		"CFBundleExecutable":         bundleExec,
	}}
	in.Run = r.run
	return r, in, asset, v
}

func installedBundle(t *testing.T) Target {
	app := filepath.Join(t.TempDir(), "Applications", bundleApp)
	write(t, filepath.Join(app, "Contents", "marker"), "old")
	return Target{GOOS: "darwin", Path: app, Installed: true, Version: "0.9.0"}
}

func TestApplyDarwinOrderAndArgv(t *testing.T) {
	r, in, asset, v := newMacRig(t)
	tg := installedBundle(t)
	if err := in.Apply(context.Background(), tg, asset, v); err != nil {
		t.Fatal(err)
	}
	mnt := filepath.Join(filepath.Dir(asset), "mnt")
	app := filepath.Join(mnt, bundleApp)
	plist := filepath.Join(tg.Path+".new", "Contents", "Info.plist")
	want := [][]string{
		{"hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, asset},
		{"ditto", app, tg.Path + ".new"},
		{"codesign", "--verify", "--deep", "--strict", tg.Path + ".new"},
		{"plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plist},
		{"plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", plist},
		{"plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", plist},
		{"xattr", "-lr", tg.Path + ".new"},
		{"hdiutil", "detach", mnt},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls:\n got %q\nwant %q", r.calls, want)
	}
	if got := readFile(t, filepath.Join(tg.Path, "Contents", "marker")); got != "new" {
		t.Errorf("bundle = %q", got)
	}
	if got := readFile(t, filepath.Join(tg.Path+".old", "Contents", "marker")); got != "old" {
		t.Errorf("kept old bundle = %q", got)
	}
	if exists(tg.Path + ".new") {
		t.Error(".new left behind")
	}
	CleanupOld(tg)
	if exists(tg.Path + ".old") {
		t.Error("CleanupOld left .old")
	}
	if !exists(tg.Path) {
		t.Error("CleanupOld removed the new bundle")
	}
}

func TestApplyDarwinFailuresKeepTheOldBundle(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name   string
		setup  func(r *macRig)
		want   error
		detach bool // the image was mounted, so it must be unmounted
	}{
		{"attach", func(r *macRig) { r.fail["hdiutil attach"] = boom }, ErrInstall, false},
		{"ditto", func(r *macRig) { r.fail["ditto"] = boom }, ErrInstall, true},
		{"codesign", func(r *macRig) { r.fail["codesign"] = boom }, ErrVerify, true},
		{"plutil fails", func(r *macRig) { r.fail["plutil CFBundleIdentifier"] = boom }, ErrVerify, true},
		{"wrong bundle id", func(r *macRig) { r.plist["CFBundleIdentifier"] = "com.evil.app" }, ErrVerify, true},
		{"wrong version", func(r *macRig) { r.plist["CFBundleShortVersionString"] = "0.9.9" }, ErrVerify, true},
		{"wrong executable", func(r *macRig) { r.plist["CFBundleExecutable"] = "sh" }, ErrVerify, true},
		{"quarantined", func(r *macRig) { r.xattr = "com.apple.quarantine: 0083;..." }, ErrVerify, true},
		{"xattr fails", func(r *macRig) { r.fail["xattr"] = boom }, ErrVerify, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, in, asset, v := newMacRig(t)
			c.setup(r)
			tg := installedBundle(t)
			err := in.Apply(context.Background(), tg, asset, v)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if got := readFile(t, filepath.Join(tg.Path, "Contents", "marker")); got != "old" {
				t.Errorf("old bundle = %q", got)
			}
			if exists(tg.Path+".new") || exists(tg.Path+".old") {
				t.Error(".new or .old left behind")
			}
			last := r.calls[len(r.calls)-1]
			if gotDetach := last[0] == "hdiutil" && last[1] == "detach"; gotDetach != c.detach {
				t.Errorf("detach = %v, want %v (calls %q)", gotDetach, c.detach, r.calls)
			}
			// The command that failed is the last one before the detach: nothing ran after it.
			for _, call := range r.calls {
				if call[0] == "xattr" && c.name != "quarantined" && c.name != "xattr fails" {
					t.Errorf("xattr ran after an earlier failure: %q", r.calls)
				}
			}
		})
	}
}

func TestApplyDarwinFreshInstallAndDetachFallback(t *testing.T) {
	r, in, asset, v := newMacRig(t)
	r.fail["hdiutil detach"] = errors.New("busy")
	tg := Target{GOOS: "darwin", Path: filepath.Join(t.TempDir(), "new-dir", "Applications", bundleApp)}
	if err := in.Apply(context.Background(), tg, asset, v); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(tg.Path, "Contents", "marker")) != "new" {
		t.Error("fresh install missing")
	}
	if exists(tg.Path+".old") || exists(tg.Path+".new") {
		t.Error("leftovers after a fresh install")
	}
	// The plain detach failed in the rig, so the -force one is not tried:
	// it shares the key only by verb. Both must have been attempted.
	n := 0
	for _, c := range r.calls {
		if c[0] == "hdiutil" && c[1] == "detach" {
			n++
		}
	}
	if n != 1 && n != 2 {
		t.Errorf("detach attempts = %d", n)
	}
	var forced bool
	for _, c := range r.calls {
		forced = forced || strings.Contains(strings.Join(c, " "), "detach -force")
	}
	if !forced {
		t.Errorf("no -force detach after the plain one failed: %q", r.calls)
	}
}

func TestApplyDarwinRefusesSymlinks(t *testing.T) {
	t.Run("mounted app is a symlink", func(t *testing.T) {
		r, in, asset, v := newMacRig(t)
		r.mountLink = t.TempDir()
		tg := installedBundle(t)
		if err := in.Apply(context.Background(), tg, asset, v); !errors.Is(err, ErrVerify) {
			t.Errorf("err = %v", err)
		}
		if readFile(t, filepath.Join(tg.Path, "Contents", "marker")) != "old" {
			t.Error("old bundle changed")
		}
	})
	t.Run(".new is a symlink", func(t *testing.T) {
		_, in, asset, v := newMacRig(t)
		tg := installedBundle(t)
		victim := t.TempDir()
		write(t, filepath.Join(victim, "keep"), "x")
		if err := os.Symlink(victim, tg.Path+".new"); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := in.Apply(context.Background(), tg, asset, v); !errors.Is(err, ErrInstall) {
			t.Errorf("err = %v", err)
		}
		if readFile(t, filepath.Join(victim, "keep")) != "x" {
			t.Error("the symlink target was touched")
		}
	})
	t.Run("stale .new directory is replaced", func(t *testing.T) {
		_, in, asset, v := newMacRig(t)
		tg := installedBundle(t)
		write(t, filepath.Join(tg.Path+".new", "junk"), "stale")
		if err := in.Apply(context.Background(), tg, asset, v); err != nil {
			t.Fatal(err)
		}
		if exists(filepath.Join(tg.Path, "junk")) {
			t.Error("stale content ended up in the new bundle")
		}
	})
}

func TestApplyRefusesAFileDownloadDidNotVerify(t *testing.T) {
	in, asset, v := downloaded(t, "windows")
	in.Run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("ran an unverified installer")
		return nil, nil
	}
	exe := filepath.Join(t.TempDir(), "app.exe")

	// A file nobody downloaded.
	other := filepath.Join(t.TempDir(), filepath.Base(asset))
	write(t, other, "evil")
	if err := in.Apply(context.Background(), Target{GOOS: "windows", Path: exe}, other, v); !errors.Is(err, ErrVerify) {
		t.Errorf("unknown file: %v", err)
	}
	// A verified file that changed afterwards.
	write(t, asset, "swapped")
	if err := in.Apply(context.Background(), Target{GOOS: "windows", Path: exe}, asset, v); !errors.Is(err, ErrVerify) {
		t.Errorf("swapped file: %v", err)
	}
	if exists(exe) {
		t.Error("exe created")
	}
}

func TestApplyWindows(t *testing.T) {
	setupRun := func(t *testing.T, tg Target, asset string, write2 func() error) (RunFunc, *[][]string) {
		var calls [][]string
		return func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{name}, args...))
			if name != asset {
				t.Errorf("ran %q, want %q", name, asset)
			}
			if tg.Installed {
				if exists(tg.Path) {
					t.Error("the old exe was still in place when the setup ran")
				}
				if readFile(t, tg.Path+".old") != "old" {
					t.Error("the old exe was not moved aside")
				}
			}
			return nil, write2()
		}, &calls
	}
	newTarget := func(t *testing.T, installed bool) Target {
		exe := filepath.Join(t.TempDir(), "Programs", "Foxmayn Frappe Desktop", "foxmayn-frappe-desktop.exe")
		if installed {
			write(t, exe, "old")
		}
		return Target{GOOS: "windows", Path: exe, Installed: installed}
	}

	t.Run("update", func(t *testing.T) {
		in, asset, v := downloaded(t, "windows")
		tg := newTarget(t, true)
		write(t, tg.Path+".old", "stale old")
		run, calls := setupRun(t, tg, asset, func() error { return os.WriteFile(tg.Path, []byte("new"), 0o755) })
		in.Run = run
		if err := in.Apply(context.Background(), tg, asset, v); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*calls, [][]string{{asset, "/S"}}) {
			t.Errorf("calls = %q", *calls)
		}
		if readFile(t, tg.Path) != "new" || readFile(t, tg.Path+".old") != "old" {
			t.Errorf("exe=%q old=%q", readFile(t, tg.Path), readFile(t, tg.Path+".old"))
		}
		CleanupOld(tg)
		if exists(tg.Path + ".old") {
			t.Error("CleanupOld left .old")
		}
	})
	t.Run("fresh install", func(t *testing.T) {
		in, asset, v := downloaded(t, "windows")
		tg := newTarget(t, false)
		run, _ := setupRun(t, tg, asset, func() error { return os.WriteFile(tg.Path, []byte("new"), 0o755) })
		in.Run = run
		if err := in.Apply(context.Background(), tg, asset, v); err != nil {
			t.Fatal(err)
		}
		if readFile(t, tg.Path) != "new" || exists(tg.Path+".old") {
			t.Error("fresh install wrong")
		}
	})
	t.Run("setup fails, exe restored", func(t *testing.T) {
		in, asset, v := downloaded(t, "windows")
		tg := newTarget(t, true)
		run, _ := setupRun(t, tg, asset, func() error { return errors.New("exit status 2") })
		in.Run = run
		err := in.Apply(context.Background(), tg, asset, v)
		if !errors.Is(err, ErrInstall) {
			t.Fatalf("err = %v", err)
		}
		if readFile(t, tg.Path) != "old" || exists(tg.Path+".old") {
			t.Errorf("exe = %q, .old exists = %v", readFile(t, tg.Path), exists(tg.Path+".old"))
		}
	})
	t.Run("setup succeeds but writes no exe, exe restored", func(t *testing.T) {
		in, asset, v := downloaded(t, "windows")
		tg := newTarget(t, true)
		run, _ := setupRun(t, tg, asset, func() error { return nil })
		in.Run = run
		if err := in.Apply(context.Background(), tg, asset, v); !errors.Is(err, ErrInstall) {
			t.Fatalf("err = %v", err)
		}
		if readFile(t, tg.Path) != "old" {
			t.Errorf("exe = %q", readFile(t, tg.Path))
		}
	})
	t.Run("cancelled setup", func(t *testing.T) {
		in, asset, v := downloaded(t, "windows")
		tg := newTarget(t, true)
		ctx, cancel := context.WithCancel(context.Background())
		in.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cancel()
			return nil, ctx.Err()
		}
		err := in.Apply(ctx, tg, asset, v)
		if !errors.Is(err, ErrInstall) || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if readFile(t, tg.Path) != "old" {
			t.Errorf("exe = %q", readFile(t, tg.Path))
		}
	})
}
