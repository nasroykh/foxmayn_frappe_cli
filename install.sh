#!/usr/bin/env sh
set -eu

REPO="nasroykh/foxmayn_frappe_cli"
BINARY="ffc"

# Ed25519 public keys that sign checksums.txt (base64, raw 32 bytes). Keep in
# sync with ReleaseKeys in internal/relsig/keys.go (TestInstallScriptInSync).
RELEASE_KEYS="5r/VTDFnWuvqWN2aMxp3Gn3KZOxbDvdkwgw/8gwV6Co= T7oC0UPkoBuTW54rFaVG8fgvzFvsp2m1VYZ24HDUts0="

# verify_signature checks checksums.txt.sig against checksums.txt with the
# domain prefix ffc signs (internal/relsig). It needs OpenSSL 3 for Ed25519
# (`pkeyutl -rawin`). Without it the install continues with the SHA-256 check
# only, with a warning. Returns non-zero only for a signature that is present
# and wrong, or missing when OpenSSL could have checked it.
verify_signature() {
  dir=$1
  if [ "${FFC_SKIP_SIGNATURE:-}" = "1" ]; then
    echo "Warning: skipping the release signature check (FFC_SKIP_SIGNATURE=1)." >&2
    return 0
  fi
  case "$(openssl version 2>/dev/null)" in
    "OpenSSL "[3-9]*) ;;
    *)
      echo "Warning: OpenSSL 3 not found; the release signature was not checked (SHA-256 only)." >&2
      echo "         Install OpenSSL 3 and re-run to check it. 'ffc update' always checks signatures." >&2
      return 0
      ;;
  esac
  if [ ! -s "$dir/checksums.txt.sig" ]; then
    echo "Error: the release has no checksums.txt.sig; refusing to install an unsigned release." >&2
    echo "Re-run with FFC_SKIP_SIGNATURE=1 to bypass at your own risk." >&2
    return 1
  fi
  printf 'ffc release checksums v1\n' > "$dir/signed-message"
  cat "$dir/checksums.txt" >> "$dir/signed-message"
  # openssl base64 -d exits 0 on bad input, so check the decoded size: an
  # Ed25519 signature is exactly 64 bytes.
  tr -d '\r\n' < "$dir/checksums.txt.sig" | openssl base64 -d -A > "$dir/sig.bin" 2>/dev/null || true
  if [ "$(wc -c < "$dir/sig.bin" | tr -d ' ')" != "64" ]; then
    echo "Error: checksums.txt.sig is not a valid Ed25519 signature." >&2
    return 1
  fi
  for key in $RELEASE_KEYS; do
    # SubjectPublicKeyInfo for Ed25519 = fixed 12-byte prefix + raw key.
    printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$key" > "$dir/release-key.pem"
    if openssl pkeyutl -verify -pubin -inkey "$dir/release-key.pem" -rawin \
      -in "$dir/signed-message" -sigfile "$dir/sig.bin" > /dev/null 2>&1; then
      echo "Release signature verified."
      return 0
    fi
  done
  echo "Error: checksums.txt.sig does not match any ffc release key. The download may have been tampered with." >&2
  return 1
}

# --- detect OS ---
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *)
    echo "Unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# --- detect arch ---
ARCH=$(uname -m)
case "$ARCH" in
  x86_64 | amd64) ARCH="amd64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# --- resolve latest tag ---
# The releases/latest redirect avoids the unauthenticated API's 60 req/h limit.
LATEST_URL=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" || true)
VERSION="${LATEST_URL##*/}"
case "$VERSION" in
  v[0-9]*) ;;
  *) VERSION="" ;;
esac

if [ -z "$VERSION" ]; then
  echo "Could not determine latest release version." >&2
  exit 1
fi

echo "Installing ffc ${VERSION} (${OS}/${ARCH})..."

ARCHIVE="ffc_${VERSION#v}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# --- download archive + checksums + signature ---
curl -fsSL "$URL" -o "$TMP/$ARCHIVE"
curl -fsSL "$CHECKSUM_URL" -o "$TMP/checksums.txt"
# An HTTP error such as 404 (curl exit 22) means no signature was published,
# which verify_signature reports; any other failure is a download error.
SIG_STATUS=0
curl -fsSL "${CHECKSUM_URL}.sig" -o "$TMP/checksums.txt.sig" || SIG_STATUS=$?
if [ "$SIG_STATUS" -ne 0 ] && [ "$SIG_STATUS" -ne 22 ]; then
  echo "Error: could not download checksums.txt.sig (curl exit $SIG_STATUS)." >&2
  exit 1
fi

# --- verify that checksums.txt was signed by an ffc release key ---
verify_signature "$TMP" || exit 1

# --- verify checksum ---
# grep -F: the archive name contains '.', which are regex metacharacters.
cd "$TMP"
if command -v sha256sum > /dev/null 2>&1; then
  grep -F "$ARCHIVE" checksums.txt | sha256sum -c -
elif command -v shasum > /dev/null 2>&1; then
  grep -F "$ARCHIVE" checksums.txt | shasum -a 256 -c -
elif [ "${FFC_SKIP_CHECKSUM:-}" = "1" ]; then
  echo "Warning: no sha256 tool found; skipping checksum verification (FFC_SKIP_CHECKSUM=1)." >&2
else
  echo "Error: no sha256 tool (sha256sum or shasum) found; cannot verify the download." >&2
  echo "Install one, or re-run with FFC_SKIP_CHECKSUM=1 to bypass at your own risk." >&2
  exit 1
fi
cd - > /dev/null

# --- extract ---
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

# --- install ---
INSTALL_DIR=""
if [ -w "/usr/local/bin" ]; then
  INSTALL_DIR="/usr/local/bin"
elif [ -d "$HOME/.local/bin" ]; then
  INSTALL_DIR="$HOME/.local/bin"
else
  mkdir -p "$HOME/.local/bin"
  INSTALL_DIR="$HOME/.local/bin"
fi

mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

echo "Installed to $INSTALL_DIR/$BINARY"
echo "Optional provenance check (needs gh): download $ARCHIVE from $URL and run: gh attestation verify $ARCHIVE --repo $REPO"
echo "Run 'ffc --help' to get started. Use 'ffc init' to configure your first site."

# warn if install dir is not in PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "Note: $INSTALL_DIR is not in your PATH."
    echo "Add this to your shell profile:"
    echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
    ;;
esac
