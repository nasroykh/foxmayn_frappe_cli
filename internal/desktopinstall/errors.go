package desktopinstall

import "errors"

// The failure classes callers map to their own messages and exit codes.
// Errors wrap one of them (errors.Is) and carry the English cause as text.
var (
	// ErrUnsupported: the OS is neither Windows nor macOS.
	ErrUnsupported = errors.New("self-install is only available for Windows and macOS")
	// ErrNotInstalledCopy: this is not an installed copy of the app (a
	// development build, a disk image, a translocated or renamed bundle).
	ErrNotInstalledCopy = errors.New("this is not an installed copy of the app")
	// ErrNotWritable: the install location cannot be written by this user.
	ErrNotWritable = errors.New("the install location is not writable")
	// ErrNotNewer: the newest release is not newer than the installed version.
	ErrNotNewer = errors.New("no newer release")
	// ErrNoRelease: GitHub lists no installable desktop release.
	ErrNoRelease = errors.New("no desktop release found")
	// ErrRateLimited: GitHub's API refused the request (403 or 429).
	ErrRateLimited = errors.New("GitHub rate limit reached")
	// ErrNetwork: no usable answer from GitHub (transport, status, body).
	ErrNetwork = errors.New("network error")
	// ErrAsset: the release lacks an expected file, or its URL is not the
	// one built from the repository and tag.
	ErrAsset = errors.New("release asset missing or unexpected")
	// ErrVerify: a signature, checksum or bundle check failed. Nothing was
	// installed.
	ErrVerify = errors.New("verification failed")
	// ErrInstall: the platform installer failed; the previous copy was
	// restored where possible.
	ErrInstall = errors.New("install failed")
)
