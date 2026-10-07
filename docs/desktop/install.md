# Install Foxmayn Frappe Desktop

Install the desktop app on Windows or macOS, update it, and uninstall it.

Not an official Frappe product; not affiliated with Frappe Technologies.

## Install from a terminal (recommended)

One command downloads the newest desktop release, checks it, installs it and opens it. Use the same command to update.

```bash
# macOS (Terminal)
curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install-desktop.sh | sh
```

```powershell
# Windows (PowerShell or cmd.exe)
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install-desktop.ps1 | iex"
```

Why this is easier: beta builds are not yet signed by Apple or with a Windows code-signing certificate. A browser marks what it downloads, and macOS (Gatekeeper) or Windows (SmartScreen) then stops an unsigned app with a warning. A download made by `curl` or PowerShell is not marked, so the app opens normally. The scripts check the download themselves instead.

| | `install-desktop.sh` (macOS) | `install-desktop.ps1` (Windows) |
| --- | --- | --- |
| SHA-256 against `checksums.txt` | Yes | Yes |
| Signature of `checksums.txt` | Yes with OpenSSL 3 (`brew install openssl@3`); macOS's own LibreSSL cannot, and then the script says so | No (Windows has no built-in Ed25519 check) |
| Installs to | `/Applications`, or `~/Applications` when that is not writable | `%LOCALAPPDATA%\Programs\Foxmayn Frappe Desktop` (per user, no administrator rights) |
| An installed copy that is running | Quit first | Closed first |

Options (environment variables): `FFD_VERSION=0.1.1` installs that version instead of the newest; `FFD_NO_OPEN=1` does not open the app afterwards. On macOS, `FFC_SKIP_SIGNATURE=1` skips the signature check, at your own risk.

When a new version is out, the app's update notice (Settings > About) shows this command, pinned to that release, with a copy button.

## Download

Desktop releases are on the project's [Releases page](https://github.com/nasroykh/foxmayn_frappe_cli/releases), tagged `desktop-v<version>` (for example `desktop-v0.1.0`). They are separate from the CLI's `v<version>` releases. While the app is on 0.x, its releases are marked as pre-releases, and they are never GitHub's "latest" release, so look for the `desktop-v` tag.

| File | Platform |
| --- | --- |
| `foxmayn-frappe-desktop-<version>-windows-amd64-setup.exe` | Windows 10/11, x64 |
| `foxmayn-frappe-desktop-<version>-macos-universal.dmg` | macOS 12 or newer, Apple silicon and Intel |
| `checksums.txt`, `checksums.txt.sig` | Checksums and their signature |

Linux is not supported yet.

## Windows (manual download)

1. Run `foxmayn-frappe-desktop-<version>-windows-amd64-setup.exe`.
2. Beta builds are not code-signed, so SmartScreen warns about an unknown publisher. Click **More info**, then **Run anyway**.
3. The installer is per-user: it needs no administrator rights and installs to `%LOCALAPPDATA%\Programs\Foxmayn Frappe Desktop`. It needs the Microsoft Edge WebView2 runtime, which Windows 10 and 11 normally have.

**Uninstall:** Settings > Apps > Installed apps > Foxmayn Frappe Desktop > Uninstall. The installer includes an uninstaller.

## macOS (manual download)

1. Open the `.dmg` and drag the app to **Applications**.
2. Beta builds are not signed with an Apple Developer ID or notarized, so macOS blocks the first launch:
   - Open the app once (macOS refuses), then go to **System Settings > Privacy & Security** and click **Open Anyway**.
   - On macOS 14 and older, right-clicking the app and choosing **Open** also works.
   - If macOS says the app "is damaged and can't be opened", remove the download quarantine flag:

     ```bash
     xattr -dr com.apple.quarantine "/Applications/Foxmayn Frappe Desktop.app"
     ```

**Uninstall:** quit the app and move it from Applications to the Bin.

## Verify a download

From a checkout of the repository, check the signature of `checksums.txt`, then the file's checksum:

```bash
go run ./tools/relsign verify checksums.txt checksums.txt.sig
sha256sum -c --ignore-missing checksums.txt          # macOS: shasum -a 256 -c --ignore-missing checksums.txt
```

Or verify the build provenance with the GitHub CLI:

```bash
gh attestation verify foxmayn-frappe-desktop-0.1.0-macos-universal.dmg --repo nasroykh/foxmayn_frappe_cli
```

## What stays after uninstalling

The app shares its settings with the CLI. Uninstalling the app does not remove:

- `~/.config/ffc/config.yaml` (your sites and credentials) and the other files in `~/.config/ffc`;
- the `ffc` binary, if the app installed it (`%LOCALAPPDATA%\Programs\ffc` on Windows, `~/.local/bin/ffc` on macOS);
- assistant entries you connected. Disconnect them in the app first, or later with `ffc mcp uninstall --client <client>`.

To remove everything, see [Installation: uninstall](../getting-started/installation.md#uninstall).

## See also

- [Using the app](using.md)
- [Troubleshooting](troubleshooting.md)
- [Security](../security.md#release-signing)
