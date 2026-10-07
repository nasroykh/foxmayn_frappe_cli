#!/usr/bin/env sh
# Installs Foxmayn Frappe Desktop on macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install-desktop.sh | sh
#
# It downloads the newest desktop release (FFD_VERSION=0.1.2 picks one),
# verifies checksums.txt.sig (with OpenSSL 3) and the SHA-256 of the .dmg,
# copies the app to /Applications (~/Applications when that is not writable)
# and opens it. FFD_NO_OPEN=1 skips opening it.
#
# The app is not notarized by Apple yet. A browser download is quarantined,
# and macOS then refuses to open it; a download by curl is not, so the app
# opens normally. That is why this script exists: it checks the download
# itself instead.
set -eu

REPO="nasroykh/foxmayn_frappe_cli"
APP="Foxmayn Frappe Desktop.app"
PROCESS="foxmayn-frappe-desktop"

# Ed25519 public keys that sign checksums.txt (base64, raw 32 bytes). Keep in
# sync with ReleaseKeys in internal/relsig/keys.go (TestInstallScriptInSync).
RELEASE_KEYS="5r/VTDFnWuvqWN2aMxp3Gn3KZOxbDvdkwgw/8gwV6Co= T7oC0UPkoBuTW54rFaVG8fgvzFvsp2m1VYZ24HDUts0="

# verify_signature checks checksums.txt.sig against checksums.txt with the
# domain prefix ffc signs (internal/relsig), as install.sh does. It needs
# OpenSSL 3 for Ed25519 (`pkeyutl -rawin`); macOS ships LibreSSL, so without
# Homebrew's openssl@3 the install continues with the SHA-256 check only.
verify_signature() {
  dir=$1
  if [ "${FFC_SKIP_SIGNATURE:-}" = "1" ]; then
    echo "Warning: skipping the release signature check (FFC_SKIP_SIGNATURE=1)." >&2
    return 0
  fi
  ssl=""
  for c in openssl /opt/homebrew/opt/openssl@3/bin/openssl /usr/local/opt/openssl@3/bin/openssl; do
    case "$("$c" version 2>/dev/null)" in
      "OpenSSL "[3-9]*) ssl=$c; break ;;
    esac
  done
  if [ -z "$ssl" ]; then
    echo "Note: OpenSSL 3 not found; the release signature was not checked (SHA-256 only)." >&2
    echo "      'brew install openssl@3' and re-run to check it too." >&2
    return 0
  fi
  if [ ! -s "$dir/checksums.txt.sig" ]; then
    echo "Error: the release has no checksums.txt.sig; refusing to install an unsigned release." >&2
    echo "Re-run with FFC_SKIP_SIGNATURE=1 to bypass at your own risk." >&2
    return 1
  fi
  printf 'ffc release checksums v1\n' > "$dir/signed-message"
  cat "$dir/checksums.txt" >> "$dir/signed-message"
  # openssl base64 -d exits 0 on bad input, so check the decoded size: an
  # Ed25519 signature is exactly 64 bytes.
  tr -d '\r\n' < "$dir/checksums.txt.sig" | "$ssl" base64 -d -A > "$dir/sig.bin" 2>/dev/null || true
  if [ "$(wc -c < "$dir/sig.bin" | tr -d ' ')" != "64" ]; then
    echo "Error: checksums.txt.sig is not a valid Ed25519 signature." >&2
    return 1
  fi
  for key in $RELEASE_KEYS; do
    # SubjectPublicKeyInfo for Ed25519 = fixed 12-byte prefix + raw key.
    printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$key" > "$dir/release-key.pem"
    if "$ssl" pkeyutl -verify -pubin -inkey "$dir/release-key.pem" -rawin \
      -in "$dir/signed-message" -sigfile "$dir/sig.bin" > /dev/null 2>&1; then
      echo "Release signature verified."
      return 0
    fi
  done
  echo "Error: checksums.txt.sig does not match any ffc release key. The download may have been tampered with." >&2
  return 1
}

if [ "$(uname -s)" != "Darwin" ]; then
  echo "This installs the macOS app. On Windows use install-desktop.ps1; Linux has no desktop app yet." >&2
  exit 1
fi

# --- resolve the version ---
# Desktop releases are never GitHub's "latest" (that is the CLI), so read the
# release list and take the newest desktop-v<X.Y.Z> (no -rc suffix).
VERSION="${FFD_VERSION:-}"
VERSION="${VERSION#desktop-v}"
VERSION="${VERSION#v}"
if [ -z "$VERSION" ]; then
  LIST=$(curl -fsSL -H "Accept: application/vnd.github+json" "https://api.github.com/repos/${REPO}/releases?per_page=100" || true)
  # One field per line whether GitHub indents the JSON or not.
  VERSION=$(printf '%s\n' "$LIST" | tr ',' '\n' | sed -n 's/.*"tag_name": *"desktop-v\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)".*/\1/p' | head -n 1)
fi
case "$VERSION" in
  [0-9]*) ;;
  *)
    echo "Could not find the latest desktop release (GitHub may be limiting requests)." >&2
    echo "Pick one from https://github.com/${REPO}/releases and re-run with FFD_VERSION=<version>." >&2
    exit 1
    ;;
esac

TAG="desktop-v${VERSION}"
DMG="foxmayn-frappe-desktop-${VERSION}-macos-universal.dmg"
BASE="https://github.com/${REPO}/releases/download/${TAG}"
echo "Installing Foxmayn Frappe Desktop ${VERSION}..."

TMP=$(mktemp -d)
MNT="$TMP/mnt"
cleanup() {
  if [ -d "$MNT" ]; then hdiutil detach -quiet "$MNT" 2>/dev/null || true; fi
  rm -rf "$TMP"
}
trap cleanup EXIT

# --- download dmg + checksums + signature ---
curl -fsSL "$BASE/$DMG" -o "$TMP/$DMG"
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt"
SIG_STATUS=0
curl -fsSL "$BASE/checksums.txt.sig" -o "$TMP/checksums.txt.sig" || SIG_STATUS=$?
if [ "$SIG_STATUS" -ne 0 ] && [ "$SIG_STATUS" -ne 22 ]; then
  echo "Error: could not download checksums.txt.sig (curl exit $SIG_STATUS)." >&2
  exit 1
fi

# --- verify ---
verify_signature "$TMP" || exit 1
# grep -F: the file name contains '.', a regex metacharacter.
LINE=$(grep -F "  $DMG" "$TMP/checksums.txt" || true)
if [ -z "$LINE" ]; then
  echo "Error: $DMG is not listed in checksums.txt." >&2
  exit 1
fi
(cd "$TMP" && printf '%s\n' "$LINE" | shasum -a 256 -c -) || {
  echo "Error: the SHA-256 of $DMG does not match checksums.txt." >&2
  exit 1
}

# --- unpack ---
mkdir -p "$MNT"
hdiutil attach -quiet -nobrowse -readonly -noautoopen -mountpoint "$MNT" "$TMP/$DMG"
if [ ! -d "$MNT/$APP" ]; then
  echo "Error: $APP not found in $DMG." >&2
  exit 1
fi

# --- choose the folder ---
DEST="/Applications"
if [ -e "$DEST/$APP" ] && [ ! -w "$DEST/$APP" ]; then
  echo "Error: $DEST/$APP exists and is not writable by you. Remove it (or run as its owner) and re-run." >&2
  exit 1
fi
if [ ! -w "$DEST" ]; then
  DEST="$HOME/Applications"
  mkdir -p "$DEST"
fi

# --- quit an installed copy that runs (not a development build), then replace it ---
if pgrep -f "/$APP/Contents/MacOS/$PROCESS" > /dev/null 2>&1; then
  echo "Quitting the running app..."
  osascript -e 'tell application "Foxmayn Frappe Desktop" to quit' > /dev/null 2>&1 || true
  i=0
  while pgrep -f "/$APP/Contents/MacOS/$PROCESS" > /dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -gt 20 ]; then
      echo "Error: Foxmayn Frappe Desktop is still running. Quit it and re-run." >&2
      exit 1
    fi
    sleep 0.5
  done
fi

rm -rf "$DEST/$APP.new"
ditto "$MNT/$APP" "$DEST/$APP.new"
codesign --verify --deep --strict "$DEST/$APP.new" 2>/dev/null || {
  rm -rf "$DEST/$APP.new"
  echo "Error: the app's code signature does not verify; not installed." >&2
  exit 1
}
rm -rf "$DEST/$APP"
mv "$DEST/$APP.new" "$DEST/$APP"

echo "Installed to $DEST/$APP"
echo "Optional provenance check (needs gh): gh release download $TAG -R $REPO -p '$DMG' && gh attestation verify '$DMG' --repo $REPO"

if [ "${FFD_NO_OPEN:-}" != "1" ]; then
  open "$DEST/$APP"
fi
