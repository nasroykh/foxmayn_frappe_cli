# Install Foxmayn Frappe Desktop

Download and install the desktop app on Windows or macOS, get past the warnings for unsigned beta builds, and uninstall it.

Not an official Frappe product; not affiliated with Frappe Technologies.

## Download

Desktop releases are on the project's [Releases page](https://github.com/nasroykh/foxmayn_frappe_cli/releases), tagged `desktop-v<version>` (for example `desktop-v0.1.0`). They are separate from the CLI's `v<version>` releases. While the app is on 0.x, its releases are marked as pre-releases, and they are never GitHub's "latest" release, so look for the `desktop-v` tag.

| File | Platform |
| --- | --- |
| `foxmayn-frappe-desktop-<version>-windows-amd64-setup.exe` | Windows 10/11, x64 |
| `foxmayn-frappe-desktop-<version>-macos-universal.dmg` | macOS 12 or newer, Apple silicon and Intel |
| `checksums.txt`, `checksums.txt.sig` | Checksums and their signature |

Linux is not supported yet.

> As of 2026-10-06 no desktop release has been published yet. Until one is, build the app from source: [Development: desktop app](../development/desktop.md).

## Windows

1. Run `foxmayn-frappe-desktop-<version>-windows-amd64-setup.exe`.
2. Beta builds are not code-signed, so SmartScreen warns about an unknown publisher. Click **More info**, then **Run anyway**.
3. The installer is per-user: it needs no administrator rights and installs to `%LOCALAPPDATA%\Programs\Foxmayn Frappe Desktop`. It needs the Microsoft Edge WebView2 runtime, which Windows 10 and 11 normally have.

**Uninstall:** Settings > Apps > Installed apps > Foxmayn Frappe Desktop > Uninstall. The installer includes an uninstaller.

## macOS

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
