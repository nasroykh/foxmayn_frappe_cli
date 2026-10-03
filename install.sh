#!/usr/bin/env sh
set -eu

REPO="nasroykh/foxmayn_frappe_cli"
BINARY="ffc"

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

# --- download archive + checksums ---
curl -fsSL "$URL" -o "$TMP/$ARCHIVE"
curl -fsSL "$CHECKSUM_URL" -o "$TMP/checksums.txt"

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
