package desktopinstall

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

const (
	maxRedirects    = 5
	downloadTimeout = 15 * time.Minute
	progressEvery   = 100 * time.Millisecond
)

// maxDownload caps the installer (a variable so tests can shrink it).
var maxDownload int64 = release.MaxArchiveBytes

// verified remembers the SHA-256 of every file Download checked, by cleaned
// path. Apply refuses a file that is not here and re-hashes the one it is
// about to run, so a swap between the check and the exec is caught.
var verified sync.Map // string -> [32]byte

// NewTempDir returns a fresh directory in the user's cache for one update
// (0700 on unix; per user on both systems). The caller RemoveAlls it.
func NewTempDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache directory: %w", err)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("creating the cache directory: %w", err)
	}
	dir, err := os.MkdirTemp(base, "ffd-update-*")
	if err != nil {
		return "", fmt.Errorf("creating a private directory: %w", err)
	}
	return dir, nil
}

// ExecRun is the RunFunc that starts a real process: argv only, never a shell.
func ExecRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			if len(msg) > 500 {
				msg = msg[:500]
			}
			return out, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, msg)
		}
		return out, fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return out, nil
}

// checkPlan refuses a plan that is not exactly what Latest builds: the
// download URLs come from constants, not from the caller.
func (in *Installer) checkPlan(p Plan) error {
	if !supported(in.GOOS) {
		return ErrUnsupported
	}
	v, ok := TagVersion(p.Tag)
	if !ok || v.Compare(p.Version) != 0 || v.Pre != "" {
		return fmt.Errorf("%w: tag %q does not match version %s", ErrAsset, p.Tag, p.Version)
	}
	asset, err := AssetName(in.GOOS, p.Version)
	if err != nil {
		return err
	}
	a, s, g := in.urls(p.Tag, asset)
	if p.Asset != asset || p.AssetURL != a || p.SumsURL != s || p.SigURL != g {
		return fmt.Errorf("%w: plan for %s does not match the release's file names", ErrAsset, p.Tag)
	}
	return nil
}

// Download fetches the plan's installer into dir and returns its path. The
// signed checksums come first (the signature, then the entry for the asset),
// so a bad release fails before the big download. The body is streamed to
// dir/<asset> through a size cap and a SHA-256, compared with the entry, and
// the file is hashed once more through a locked handle. Any failure removes
// the file and returns an error wrapping ErrVerify, ErrNetwork or the
// context's error. progress (may be nil) gets the bytes done and the total
// (0 when unknown), at most every 100 ms and once at the end.
func (in *Installer) Download(ctx context.Context, p Plan, dir string, progress func(done, total int64)) (_ string, err error) {
	if err := in.checkPlan(p); err != nil {
		return "", err
	}
	in.log("verifying the release signature")
	sums, err := release.SignedChecksums(ctx, p.SumsURL, p.SigURL)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		return "", fmt.Errorf("%w: %w", ErrVerify, err)
	}
	want, err := release.ChecksumFor(sums, p.Asset)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrVerify, err)
	}

	path := filepath.Join(dir, p.Asset)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", p.Asset, err)
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(path)
		}
	}()

	in.log("downloading " + p.Asset)
	c := client.NewHTTPClient(downloadTimeout).
		SetResponseBodyLimit(0). // the cap below is the limit
		SetRedirectPolicy(resty.RedirectPolicyFunc(in.checkRedirect))
	if in.Transport != nil {
		c.SetTransport(in.Transport)
	}
	resp, err := c.R().SetContext(ctx).SetDoNotParseResponse(true).Get(p.AssetURL)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		return "", fmt.Errorf("%w: downloading %s: %w", ErrNetwork, p.Asset, err)
	}
	body := resp.RawBody()
	defer body.Close()
	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("%w: downloading %s: HTTP %d", ErrNetwork, p.Asset, resp.StatusCode())
	}
	total := resp.RawResponse.ContentLength
	if total > maxDownload {
		return "", fmt.Errorf("%w: %s is %d MB, over the %d MB limit", ErrVerify, p.Asset, total>>20, maxDownload>>20)
	}

	h := sha256.New()
	pw := &progressWriter{fn: progress, total: max(total, 0)}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(body, maxDownload+1))
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		return "", fmt.Errorf("%w: downloading %s: %w", ErrNetwork, p.Asset, err)
	}
	switch {
	case n > maxDownload:
		return "", fmt.Errorf("%w: %s is over the %d MB limit", ErrVerify, p.Asset, maxDownload>>20)
	case total >= 0 && n != total:
		return "", fmt.Errorf("%w: %s ended after %d of %d bytes", ErrNetwork, p.Asset, n, total)
	}
	pw.flush()
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("writing %s: %w", p.Asset, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", p.Asset, err)
	}
	var got [32]byte
	h.Sum(got[:0])
	if got != want {
		return "", fmt.Errorf("%w: checksum mismatch for %s", ErrVerify, p.Asset)
	}

	// Once more from disk through a handle nobody can write through.
	in.log("verifying " + p.Asset)
	lf, err := openLocked(path)
	if err != nil {
		return "", fmt.Errorf("%w: reopening %s: %w", ErrVerify, p.Asset, err)
	}
	disk, herr := hashFile(lf)
	lf.Close()
	if herr != nil {
		return "", fmt.Errorf("%w: reading %s back: %w", ErrVerify, p.Asset, herr)
	}
	if disk != want {
		return "", fmt.Errorf("%w: %s changed on disk after the download", ErrVerify, p.Asset)
	}
	verified.Store(filepath.Clean(path), want)
	return path, nil
}

// checkRedirect is the download's redirect policy: https only, at most
// maxRedirects hops, and only to AllowedHosts.
func (in *Installer) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return &refusedRedirect{fmt.Sprintf("stopped after %d redirects", maxRedirects)}
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return &refusedRedirect{fmt.Sprintf("refused redirect to %s: not https", req.URL.Redacted())}
	}
	host := strings.ToLower(strings.TrimSuffix(req.URL.Hostname(), "."))
	for _, h := range in.AllowedHosts {
		if strings.EqualFold(strings.TrimSuffix(h, "."), host) {
			return nil
		}
	}
	return &refusedRedirect{fmt.Sprintf("refused redirect to %s: host is not allowed", req.URL.Redacted())}
}

type refusedRedirect struct{ msg string }

func (e *refusedRedirect) Error() string { return e.msg }

func hashFile(f *os.File) ([32]byte, error) {
	var sum [32]byte
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	h.Sum(sum[:0])
	return sum, nil
}

// progressWriter reports the running byte count, throttled.
type progressWriter struct {
	fn         func(done, total int64)
	done       int64
	total      int64
	lastReport time.Time
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.done += int64(len(b))
	if w.fn != nil && time.Since(w.lastReport) >= progressEvery {
		w.lastReport = time.Now()
		w.fn(w.done, w.total)
	}
	return len(b), nil
}

func (w *progressWriter) flush() {
	if w.fn != nil {
		w.fn(w.done, max(w.total, w.done))
	}
}
