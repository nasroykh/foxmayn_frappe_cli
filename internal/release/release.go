// Package release finds, downloads and verifies ffc releases on GitHub. It
// never prompts and never touches an installed binary: `ffc update` and the
// desktop app's installer decide where the verified binary goes.
package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

// ReleasesURL lists the repository's newest releases. The repository also
// publishes desktop app releases, so GitHub's releases/latest may be one of
// those; Latest picks the newest ffc release from this list instead.
const ReleasesURL = "https://api.github.com/repos/nasroykh/foxmayn_frappe_cli/releases?per_page=100"

const (
	// MaxArchiveBytes caps the downloaded archive and MaxBinaryBytes one
	// extracted file, so a hostile or corrupt archive cannot exhaust memory (D20).
	MaxArchiveBytes   = 200 << 20
	MaxBinaryBytes    = 200 << 20
	maxChecksumsBytes = 1 << 20
	checksumsName     = "checksums.txt"
)

// Release is the part of GitHub's release JSON that ffc reads.
type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Target is the archive of one platform plus the files that vouch for it.
type Target struct {
	Tag          string
	GOOS         string
	AssetName    string
	DownloadURL  string
	ChecksumsURL string
	SignatureURL string
}

// Latest returns the newest ffc release in the list at url (ReleasesURL
// outside tests): the first one, in GitHub's newest-first order, that is
// neither a draft nor a prerelease and whose tag is "v<digit>…" (the CLI's
// tags; desktop releases use another prefix). It is what releases/latest
// gave before desktop releases existed.
func Latest(ctx context.Context, url string, timeout time.Duration) (*Release, error) {
	var rels []Release
	resp, err := client.NewHTTPClient(timeout).R().
		SetContext(ctx).
		SetResult(&rels).
		SetHeader("Accept", "application/vnd.github+json").
		Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching release info: %w", err)
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode())
	}
	for i := range rels {
		if r := &rels[i]; !r.Draft && !r.Prerelease && isCLITag(r.TagName) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no ffc release found on GitHub")
}

// isCLITag reports whether tag is an ffc CLI release tag: "v" then a digit.
func isCLITag(tag string) bool {
	return len(tag) > 1 && tag[0] == 'v' && tag[1] >= '0' && tag[1] <= '9'
}

// AssetName returns the GoReleaser archive name of a platform. GoReleaser
// strips the leading "v" from the tag for .Version.
//
// Example: "v0.2.0", "linux", "amd64" → "ffc_0.2.0_linux_amd64.tar.gz"
func AssetName(tag, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("ffc_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), goos, goarch, ext)
}

// BinaryName is the ffc binary's file name inside the archive.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "ffc.exe"
	}
	return "ffc"
}

// Target picks the archive for goos/goarch and the checksums file and
// signature next to it. A missing checksums file or signature is left empty
// here and refused by Verify.
func (r *Release) Target(goos, goarch string) (Target, error) {
	t := Target{Tag: r.TagName, GOOS: goos, AssetName: AssetName(r.TagName, goos, goarch)}
	for _, a := range r.Assets {
		switch a.Name {
		case t.AssetName:
			t.DownloadURL = a.BrowserDownloadURL
		case checksumsName:
			t.ChecksumsURL = a.BrowserDownloadURL
		case relsig.SignatureName:
			t.SignatureURL = a.BrowserDownloadURL
		}
	}
	if t.DownloadURL == "" {
		return t, fmt.Errorf("no asset found for %s/%s (expected %q)", goos, goarch, t.AssetName)
	}
	return t, nil
}

// Download fetches the target's archive, verifies it against the release's
// signed checksums.txt and returns the ffc binary inside it. Any failure
// returns no binary (fail closed).
func Download(ctx context.Context, t Target) ([]byte, error) {
	archive, err := Fetch(ctx, t.DownloadURL, 5*time.Minute, MaxArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("downloading: %w", err)
	}
	// TLS alone does not protect against a compromised release (H1), and the
	// signature keeps someone who can replace release assets from also
	// replacing checksums.txt (D19).
	if err := Verify(ctx, archive, t.ChecksumsURL, t.SignatureURL, t.AssetName); err != nil {
		return nil, err
	}
	bin, err := Extract(archive, t.GOOS)
	if err != nil {
		return nil, fmt.Errorf("extracting binary: %w", err)
	}
	return bin, nil
}

// Fetch GETs url and returns at most max bytes, failing when the body is
// larger. The context cancels the transfer (D18, D20).
func Fetch(ctx context.Context, url string, timeout time.Duration, max int64) ([]byte, error) {
	resp, err := client.NewHTTPClient(timeout).R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.RawBody().Close()
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	data, err := io.ReadAll(io.LimitReader(resp.RawBody(), max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("response exceeds %d MB limit", max>>20)
	}
	return data, nil
}

// Verify checks the release's checksums.txt against its Ed25519 signature
// (relsig.ReleaseKeys), then compares the SHA-256 of archive with the entry
// for assetName (H1, D19). The archive name carries the version, so a signed
// checksums.txt from an older release cannot vouch for this one.
func Verify(ctx context.Context, archive []byte, checksumsURL, sigURL, assetName string) error {
	if checksumsURL == "" {
		return fmt.Errorf("release has no checksums.txt — refusing to install an unverified binary")
	}
	if sigURL == "" {
		return fmt.Errorf("release has no %s — refusing to install an unverified binary", relsig.SignatureName)
	}
	body, err := Fetch(ctx, checksumsURL, 30*time.Second, maxChecksumsBytes)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	sig, err := Fetch(ctx, sigURL, 30*time.Second, relsig.MaxSignatureBytes)
	if err != nil {
		return fmt.Errorf("fetching checksums signature: %w", err)
	}
	if err := relsig.Verify(relsig.ReleaseKeys, body, sig); err != nil {
		return fmt.Errorf("checksums.txt signature check failed — refusing to install: %w", err)
	}

	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(body), "\n") {
		// checksums.txt lines are "<hex-sha256>  <filename>".
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			if !strings.EqualFold(fields[0], got) {
				return fmt.Errorf("checksum mismatch for %s:\n  expected %s\n  got      %s", assetName, fields[0], got)
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s in checksums.txt", assetName)
}

// Extract returns the ffc binary from a release archive of goos (a zip on
// Windows, a tar.gz elsewhere).
func Extract(archive []byte, goos string) ([]byte, error) {
	if goos == "windows" {
		return ExtractFromZip(archive, BinaryName(goos))
	}
	return ExtractFromTarGz(archive, BinaryName(goos))
}

// ExtractFromTarGz returns the regular file called name from a tar.gz.
func ExtractFromTarGz(data []byte, name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decompressing gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == name {
			return readBinaryEntry(tr, name)
		}
	}
	return nil, fmt.Errorf("%q not found in archive", name)
}

// ExtractFromZip returns the regular file called name from a zip.
func ExtractFromZip(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}
	for _, f := range r.File {
		if f.Mode().IsRegular() && filepath.Base(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return readBinaryEntry(rc, name)
		}
	}
	return nil, fmt.Errorf("%q not found in zip", name)
}

// readBinaryEntry reads one archive entry, rejecting empty or oversized data so
// a corrupt archive can never replace ffc with a 0-byte or huge file (D20).
func readBinaryEntry(r io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBinaryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxBinaryBytes {
		return nil, fmt.Errorf("%q exceeds %d MB limit", name, MaxBinaryBytes>>20)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%q in archive is empty", name)
	}
	return data, nil
}
