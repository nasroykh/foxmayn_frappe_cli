//go:build darwin

package desktopinstall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyRealDiskImage builds a tiny ad-hoc signed app, packs it into a
// real .dmg, serves it through the fake release and runs the real Apply
// (hdiutil, ditto, codesign, plutil, xattr) into a temp dir. UNVERIFIED on CI
// until the macOS runner has run it.
func TestApplyRealDiskImage(t *testing.T) {
	for _, tool := range []string{"hdiutil", "ditto", "codesign", "plutil", "xattr"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	const version = "0.10.0"
	src := t.TempDir()
	app := filepath.Join(src, bundleApp)
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary to use as the executable")
	}
	bin, err := os.ReadFile(truePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, bundleExec), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>` + bundleID + `</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
<key>CFBundleExecutable</key><string>` + bundleExec + `</string>
<key>CFBundleName</key><string>Foxmayn Frappe Desktop</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "-s", "-", "--force", "--deep", app).CombinedOutput(); err != nil {
		t.Skipf("cannot ad-hoc sign: %v: %s", err, out)
	}
	dmg := filepath.Join(t.TempDir(), "image.dmg")
	if out, err := exec.Command("hdiutil", "create", "-srcfolder", src, "-format", "UDZO", "-volname", "Foxmayn", dmg).CombinedOutput(); err != nil {
		t.Skipf("cannot create a disk image: %v: %s", err, out)
	}
	payload, err := os.ReadFile(dmg)
	if err != nil {
		t.Fatal(err)
	}

	f, in := newFake(t, "darwin")
	p := planFor(t, f, in, payload)
	path, err := in.Download(context.Background(), p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tg := Target{GOOS: "darwin", Path: filepath.Join(t.TempDir(), "Applications", bundleApp)}
	if err := in.Apply(context.Background(), tg, path, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tg.Path, "Contents", "MacOS", bundleExec)); err != nil {
		t.Errorf("installed app has no executable: %v", err)
	}
	out, err := exec.Command("xattr", "-lr", tg.Path).CombinedOutput()
	if err != nil || strings.Contains(string(out), "com.apple.quarantine") {
		t.Errorf("quarantine flag on the installed app (or xattr failed: %v): %s", err, out)
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", tg.Path).CombinedOutput(); err != nil {
		t.Errorf("installed app does not verify: %v: %s", err, out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("downloaded image removed by Apply: %v", err)
	}
}
