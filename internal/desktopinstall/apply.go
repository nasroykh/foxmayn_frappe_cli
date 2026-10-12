package desktopinstall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	setupTimeout = 5 * time.Minute
	detachTime   = 30 * time.Second
)

// Apply installs the file Download returned (asset, the path) as version v
// over t. Every external program runs through in.Run with an argument
// vector. The previous copy is moved aside and only removed by CleanupOld,
// so a failure restores it: a nil error means the new copy is in place and
// was checked.
//
// macOS: mount the image read-only, copy the bundle next to the old one,
// check its signature, bundle id, version and quarantine flag, swap it in
// and unmount. Windows: move the exe aside (allowed while it runs), run the
// per-user setup silently and hold the setup file locked until it exits.
func (in *Installer) Apply(ctx context.Context, t Target, asset string, v Version) error {
	if !supported(in.GOOS) {
		return ErrUnsupported
	}
	if t.GOOS != in.GOOS || t.Path == "" {
		return fmt.Errorf("%w: target %q does not match %s", ErrInstall, t.Path, in.GOOS)
	}
	want, ok := verified.Load(filepath.Clean(asset))
	name, _ := AssetName(in.GOOS, v)
	if !ok || filepath.Base(asset) != name {
		return fmt.Errorf("%w: %s was not downloaded and verified by this package", ErrVerify, asset)
	}

	// Hash the file once more through a handle that stays open for the whole
	// install: on Windows nobody can write to or swap it meanwhile.
	lf, err := openLocked(asset)
	if err != nil {
		return fmt.Errorf("%w: opening %s: %w", ErrVerify, asset, err)
	}
	defer lf.Close()
	got, err := hashFile(lf)
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", ErrVerify, asset, err)
	}
	if got != want.([32]byte) {
		return fmt.Errorf("%w: %s changed after the download", ErrVerify, asset)
	}
	defer verified.Delete(filepath.Clean(asset))

	if in.GOOS == "darwin" {
		return in.applyDarwin(ctx, t, asset, v)
	}
	return in.applyWindows(ctx, t, asset)
}

func (in *Installer) applyDarwin(ctx context.Context, t Target, asset string, v Version) (err error) {
	run := in.run()
	mnt := filepath.Join(filepath.Dir(asset), "mnt")
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return fmt.Errorf("%w: creating the mount point: %w", ErrInstall, err)
	}

	in.log("mounting the disk image")
	if _, err := run(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, asset); err != nil {
		return fmt.Errorf("%w: mounting the disk image: %w", ErrInstall, err)
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachTime)
		defer cancel()
		if _, derr := run(dctx, "hdiutil", "detach", mnt); derr != nil {
			if _, derr = run(dctx, "hdiutil", "detach", "-force", mnt); derr != nil {
				in.log("unmounting the disk image failed: " + derr.Error())
			}
		}
	}()

	src := filepath.Join(mnt, bundleApp)
	if fi, serr := os.Lstat(src); serr != nil {
		return fmt.Errorf("%w: the disk image has no %s: %w", ErrVerify, bundleApp, serr)
	} else if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%w: %s in the disk image is not a plain bundle", ErrVerify, bundleApp)
	}

	newPath := t.Path + ".new"
	if fi, serr := os.Lstat(newPath); serr == nil {
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symlink", ErrInstall, newPath)
		}
		if rerr := os.RemoveAll(newPath); rerr != nil {
			return fmt.Errorf("%w: removing the leftover %s: %w", ErrInstall, newPath, rerr)
		}
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		return fmt.Errorf("%w: creating %s: %w", ErrInstall, filepath.Dir(t.Path), err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(newPath)
		}
	}()

	in.log("copying the app")
	if _, err := run(ctx, "ditto", src, newPath); err != nil {
		return fmt.Errorf("%w: copying the app: %w", ErrInstall, err)
	}

	in.log("checking the app")
	if _, err := run(ctx, "codesign", "--verify", "--deep", "--strict", newPath); err != nil {
		return fmt.Errorf("%w: code signature: %w", ErrVerify, err)
	}
	for _, c := range []struct{ key, want string }{
		{"CFBundleIdentifier", bundleID},
		{"CFBundleShortVersionString", v.String()},
		{"CFBundleExecutable", bundleExec},
	} {
		got, perr := in.plistValue(ctx, newPath, c.key)
		if perr != nil {
			return fmt.Errorf("%w: reading %s: %w", ErrVerify, c.key, perr)
		}
		if got != c.want {
			return fmt.Errorf("%w: %s is %q, expected %q", ErrVerify, c.key, got, c.want)
		}
	}
	// Go writes no quarantine flag; one here means the file came some other
	// way, so refuse it rather than strip a mark Gatekeeper would act on.
	out, err := run(ctx, "xattr", "-lr", newPath)
	if err != nil {
		return fmt.Errorf("%w: reading extended attributes: %w", ErrVerify, err)
	}
	if strings.Contains(string(out), "com.apple.quarantine") {
		return fmt.Errorf("%w: the new app carries the com.apple.quarantine flag", ErrVerify)
	}

	in.log("replacing the app")
	oldPath := t.Path + ".old"
	hadOld := false
	if _, serr := os.Lstat(t.Path); serr == nil {
		hadOld = true
		if rerr := os.RemoveAll(oldPath); rerr != nil {
			return fmt.Errorf("%w: removing the leftover %s: %w", ErrInstall, oldPath, rerr)
		}
		if rerr := os.Rename(t.Path, oldPath); rerr != nil {
			return fmt.Errorf("%w: moving the old app aside: %w", ErrInstall, rerr)
		}
	}
	if rerr := os.Rename(newPath, t.Path); rerr != nil {
		if hadOld {
			if berr := os.Rename(oldPath, t.Path); berr != nil {
				rerr = fmt.Errorf("%w (and restoring the old app failed: %v)", rerr, berr)
			}
		}
		return fmt.Errorf("%w: putting the new app in place: %w", ErrInstall, rerr)
	}
	return nil
}

func (in *Installer) applyWindows(ctx context.Context, t Target, setup string) error {
	exe := t.Path
	oldPath := exe + ".old"
	hadOld := false
	if _, serr := os.Lstat(exe); serr == nil {
		hadOld = true
		if rerr := os.Remove(oldPath); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			oldPath = fmt.Sprintf("%s.old-%d", exe, time.Now().UnixNano())
		}
		// A running exe can be renamed, not overwritten.
		if rerr := os.Rename(exe, oldPath); rerr != nil {
			return fmt.Errorf("%w: moving the old app aside: %w", ErrInstall, rerr)
		}
	} else if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		return fmt.Errorf("%w: creating %s: %w", ErrInstall, filepath.Dir(exe), err)
	}

	in.log("running the installer")
	sctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	_, err := in.run()(sctx, setup, "/S")
	if err == nil {
		if fi, serr := os.Stat(exe); serr != nil || !fi.Mode().IsRegular() {
			err = fmt.Errorf("the installer finished but %s is missing", exe)
		}
	}
	if err != nil {
		if hadOld {
			if _, serr := os.Lstat(exe); serr != nil {
				if berr := os.Rename(oldPath, exe); berr != nil {
					err = fmt.Errorf("%w (and restoring the old app failed: %v)", err, berr)
				}
			}
		}
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("%w: %w", ErrInstall, errors.Join(cerr, err))
		}
		return fmt.Errorf("%w: %w", ErrInstall, err)
	}
	return nil
}

// CleanupOld removes the previous copy and any half-copied one that Apply
// left beside t. It is best effort and never fails; call it once the new
// copy runs (or later, from the new copy).
func CleanupOld(t Target) {
	if t.Path == "" {
		return
	}
	os.RemoveAll(t.Path + ".new")
	os.RemoveAll(t.Path + ".old")
	if t.GOOS == "windows" {
		if m, err := filepath.Glob(escapeGlob(t.Path) + ".old-*"); err == nil {
			for _, p := range m {
				os.RemoveAll(p)
			}
		}
	}
}

// escapeGlob quotes the glob metacharacters of a literal path.
func escapeGlob(p string) string {
	if filepath.Separator == '\\' {
		// Backslash is the separator, not an escape; bracket the specials.
		return strings.NewReplacer("*", "[*]", "?", "[?]", "[", "[[]").Replace(p)
	}
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`).Replace(p)
}
