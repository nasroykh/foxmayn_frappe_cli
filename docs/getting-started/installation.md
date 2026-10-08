# Installation

Install, update and remove the `ffc` command line on Linux, macOS and Windows.

ffc is a single binary with no runtime dependencies. Releases are built for Linux, macOS and Windows on amd64 and arm64.

## Install script (recommended)

**Linux and macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.sh | sh
```

The script detects your OS and architecture, downloads the latest release, verifies it and installs `ffc` to `/usr/local/bin` when that directory is writable, otherwise to `~/.local/bin` (created if needed). If the target is not on your `PATH`, it prints the line to add to your shell profile.

**Windows** (from PowerShell or `cmd.exe`):

```powershell
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.ps1 | iex"
```

The script installs `ffc.exe` to `%LOCALAPPDATA%\Programs\ffc` and adds that folder to your user `PATH`. Open a new terminal afterwards. No administrator rights are needed.

### What the scripts verify

| Check | `install.sh` | `install.ps1` |
| --- | --- | --- |
| SHA-256 of the archive against `checksums.txt` | Yes (needs `sha256sum` or `shasum`) | Yes |
| Ed25519 signature of `checksums.txt` | Yes, when OpenSSL 3 is installed; otherwise it warns and relies on the checksum | No (Windows has no built-in Ed25519 verifier) |

macOS ships LibreSSL, not OpenSSL 3, so `install.sh` skips the signature check there unless you install OpenSSL 3 (for example with Homebrew). Every later [`ffc update`](../cli/update.md) checks the signature on all platforms.

`install.sh` honours two escape hatches, at your own risk: `FFC_SKIP_SIGNATURE=1` skips the signature check, and `FFC_SKIP_CHECKSUM=1` allows installing when no SHA-256 tool is found.

## Homebrew (macOS, Linux)

```bash
brew install nasroykh/tap/ffc
```

This installs from Foxmayn's own tap (not homebrew-core), with the shell completions and man pages. Homebrew checks the archive's SHA-256 from the tap; it does not check the release signature. The binary is not notarized yet, so the cask removes macOS's quarantine flag after installing, the same state a `curl` download is in. Update with `brew upgrade ffc`; `ffc update` leaves a Homebrew copy alone.

## Scoop (Windows)

```powershell
scoop bucket add foxmayn https://github.com/nasroykh/scoop-bucket
scoop install ffc
```

Scoop checks the archive's SHA-256 from the bucket, not the release signature. Update with `scoop update ffc`; `ffc update` leaves a Scoop copy alone.

## Manual download

1. Download the archive for your platform from the [Releases page](https://github.com/nasroykh/foxmayn_frappe_cli/releases): `ffc_<version>_<os>_<arch>.tar.gz` (Linux, macOS) or `.zip` (Windows), plus `checksums.txt` and `checksums.txt.sig`.
2. Check it. Either verify the build provenance with the GitHub CLI:

   ```bash
   gh attestation verify ffc_1.11.0_linux_amd64.tar.gz --repo nasroykh/foxmayn_frappe_cli
   ```

   or compare the SHA-256 with `checksums.txt` (`sha256sum -c --ignore-missing checksums.txt`). The signature check is described in [Security](../security.md#release-signing).
3. Extract it and put `ffc` (`ffc.exe`) in a directory on your `PATH`.

## From source

Needs Go (the version in `go.mod`).

```bash
go install github.com/nasroykh/foxmayn_frappe_cli/cmd/ffc@latest
```

A binary built this way reports version `dev` unless built with the release flags. See [Development](../development/README.md#build-from-source) for `make build` and `make install`.

## Install from the desktop app

Foxmayn Frappe Desktop can install ffc for you, with the same signature and checksum checks as `ffc update`. See [Desktop app: install the ffc helper](../desktop/using.md#the-ffc-helper).

## Check the installation

```bash
ffc --version
ffc --help
```

Next: [Quickstart](quickstart.md).

## Update

```bash
ffc update
```

This works for the install scripts, a manual download and `go install`. Homebrew and Scoop copies update through them (`brew upgrade ffc`, `scoop update ffc`); `ffc update` says so. Running the install script again also updates. See [update](../cli/update.md).

## Uninstall

1. Optional: remove MCP entries you added, for each client: `ffc mcp uninstall --client <client>`. Stop a detached MCP server with `ffc mcp stop`.
2. Optional: remove OAuth sites with `ffc site remove <name>`, which revokes their tokens on the server.
3. Delete the binary (Homebrew: `brew uninstall ffc`; Scoop: `scoop uninstall ffc`), or:
   - Linux/macOS: `rm "$(command -v ffc)"` (usually `/usr/local/bin/ffc` or `~/.local/bin/ffc`).
   - Windows: delete `%LOCALAPPDATA%\Programs\ffc` and remove it from your user `PATH` (Settings > System > About > Advanced system settings > Environment Variables).
4. Delete your settings and credentials: the `~/.config/ffc` directory (`%USERPROFILE%\.config\ffc` on Windows). It holds `config.yaml`, the MCP audit log and state files. The desktop app uses the same directory.
5. Delete the cache: `~/.cache/ffc` (Linux), `~/Library/Caches/ffc` (macOS) or `%LOCALAPPDATA%\ffc` (Windows).

## See also

- [Quickstart](quickstart.md)
- [Security](../security.md)
- [update](../cli/update.md)
