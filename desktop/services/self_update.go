package services

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/desktopinstall"
)

// UpdateInstall is the result of InstallUpdate. When it returns, the new copy
// was started and the app is quitting.
type UpdateInstall struct {
	Version string `json:"version"`
}

// afterUpdateFlag is the argument the relaunched app receives (main).
const afterUpdateFlag = "--after-update"

// InstallUpdate replaces this copy of the app with the newest release and
// restarts it. It takes no input from the web view: the release is the one
// internal/desktopinstall finds for this version, the signed checksums and
// the file's SHA-256 are verified before anything is installed, and the
// program started afterwards is the path LocateSelf returned for this very
// copy. The UI has already asked the person (and warned about a running
// assistant or unsent text). Steps arrive as "update:progress" events.
//
// Cancelling ctx stops it until the install begins; from then on it runs to
// the end, because stopping half way is the one thing that could leave the
// app broken. Any failure leaves the current copy as it was. On success the
// call returns after the new copy was started, and the app quits.
func (s *AppService) InstallUpdate(ctx context.Context) (UpdateInstall, error) {
	if !s.updateMu.TryLock() {
		return UpdateInstall{}, keyed(CodeUnavailable, "update.busy", nil)
	}
	defer s.updateMu.Unlock()

	step := func(name string) { s.host.Emit(EventUpdateProgress, UpdateProgress{Step: name}) }
	fail := func(err error) (UpdateInstall, error) {
		return UpdateInstall{}, updateError(ctx, err)
	}

	step("checking")
	cur, ok := releaseVersion(s.version)
	if !ok {
		return fail(desktopinstall.ErrNotInstalledCopy)
	}
	exe, err := s.exe()
	if err != nil {
		return fail(errors.Join(desktopinstall.ErrNotInstalledCopy, err))
	}
	target, err := s.locate(s.goos, exe)
	if err != nil {
		return fail(err)
	}
	plan, err := s.selfUpdate.Find(ctx, cur)
	if err != nil {
		return fail(err)
	}

	dir, err := s.tempDir()
	if err != nil {
		return fail(errors.Join(desktopinstall.ErrInstall, err))
	}
	defer os.RemoveAll(dir)

	var verifying atomic.Bool
	step("downloading")
	file, err := s.selfUpdate.Download(ctx, plan, dir, func(done, total int64) {
		if total > 0 && done >= total {
			if verifying.CompareAndSwap(false, true) {
				step("verifying")
			}
			return
		}
		s.host.Emit(EventUpdateProgress, UpdateProgress{Step: "downloading", Done: done, Total: total})
	})
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	// Point of no return: the install and the restart do not stop for a
	// cancelled call.
	bg := context.WithoutCancel(ctx)
	step("installing")
	if err := s.selfUpdate.Apply(bg, target, file, plan.Version); err != nil {
		// Not a cancellation, whatever the caller did meanwhile.
		return UpdateInstall{}, updateError(bg, err)
	}

	res := UpdateInstall{Version: plan.Version.String()}
	step("restarting")
	if err := s.relaunch(target); err != nil {
		return res, keyed(CodeFailed, "update.relaunch", err)
	}
	s.host.Quit()
	return res, nil
}

// relaunch starts the updated copy at the path LocateSelf gave, telling it
// which process to wait for. On macOS `open -n` starts a new instance of the
// bundle even while this one still runs.
func (s *AppService) relaunch(t desktopinstall.Target) error {
	pid := strconv.Itoa(os.Getpid())
	if t.GOOS == "darwin" {
		return s.start("/usr/bin/open", "-n", t.Path, "--args", afterUpdateFlag, pid)
	}
	return s.start(t.Path, afterUpdateFlag, pid)
}

// startDetached starts a program and does not wait for it.
func startDetached(name string, args ...string) error {
	c := exec.Command(name, args...)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// updateError turns a failure of the self-update into a keyed Error; Detail
// keeps the English cause.
func updateError(ctx context.Context, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	// Literal keys: TestErrorKeysUsed reads them from the syntax tree.
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return keyed(CodeCancelled, "update.cancelled", err)
	case errors.Is(err, desktopinstall.ErrUnsupported):
		return keyed(CodeUnavailable, "update.unsupported", err)
	case errors.Is(err, desktopinstall.ErrNotInstalledCopy):
		return keyed(CodeUnavailable, "update.notInstalled", err)
	case errors.Is(err, desktopinstall.ErrNotWritable):
		return keyed(CodeUnavailable, "update.notWritable", err)
	case errors.Is(err, desktopinstall.ErrNotNewer):
		return keyed(CodeUnavailable, "update.notNewer", err)
	case errors.Is(err, desktopinstall.ErrRateLimited):
		return keyed(CodeUnavailable, "update.rateLimited", err)
	case errors.Is(err, desktopinstall.ErrNetwork):
		return keyed(CodeNetwork, "update.network", err)
	case errors.Is(err, desktopinstall.ErrNoRelease):
		return keyed(CodeFailed, "update.noRelease", err)
	case errors.Is(err, desktopinstall.ErrAsset):
		return keyed(CodeFailed, "update.asset", err)
	case errors.Is(err, desktopinstall.ErrVerify):
		return keyed(CodeFailed, "update.verify", err)
	}
	return keyed(CodeFailed, "update.install", err)
}
