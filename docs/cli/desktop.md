# desktop

Check for, install and update the [Foxmayn Frappe Desktop](../desktop/README.md) app from the command line. Windows and macOS only: on any other system the command exits with a usage error (2) before it contacts anything.

```bash
ffc desktop status                 # installed version, newest release, is there an update
ffc desktop install                # install or update (asks first)
ffc desktop install --yes          # no question
ffc desktop install --dry-run      # show what would be installed, download nothing
```

## desktop status

Shows whether the app is installed, its version and path, and the newest desktop release. It exits 0 whether or not an update exists; scripts read `update_available`.

| Field | Meaning |
| --- | --- |
| `installed` | The app is installed for this user. |
| `installed_version` | Its version; empty when not installed or the system did not say. |
| `path` | The app: the `.app` bundle on macOS, the exe on Windows. When not installed, where an install would go. |
| `latest` | The newest installable desktop release, without a `v`. |
| `update_available` | `latest` is newer than `installed_version` (also true when the installed version is unknown). False when not installed. |
| `release_url` | The release page on GitHub. |

```bash
ffc desktop status --jq .update_available
ffc desktop status --output yaml
```

## desktop install

Installs the newest desktop release, or updates an installed copy to it.

| Flag | Description |
| --- | --- |
| `-y, --yes` | Skip the confirmation. Without a terminal, `--yes` is required. |
| `--dry-run` | Print the plan (`action`, `from`, `to`, `asset`, `path`, `dry_run`) and exit 0. Only the release list is read; nothing is downloaded or changed. |

The result (`--json`, `--output` and `--jq` work as everywhere) has `action` (`install`, `update`, or `none` when the installed version is already the newest), `version`, `path` and `restart_needed`.

- **Windows:** the per-user setup runs silently and installs into `%LOCALAPPDATA%\Programs\Foxmayn Frappe Desktop`. A running copy is moved aside first, so it keeps running until restarted.
- **macOS:** the app is copied from the disk image. An installed copy is updated where it is (`/Applications` or `~/Applications`); a first install goes to `/Applications`, or to `~/Applications` when `/Applications` is not writable by you. The old copy stays in place until the new one has passed its checks.
- It never downgrades and never reinstalls the same version: when the installed version is the newest, it says so and exits 0 without downloading.
- It never quits or opens the app. After an update, ffc prints "restart it" on stderr and `restart_needed` is true; restart the app to use the new version.
- A pre-1.0 release that GitHub marks as a "prerelease" is installable; one with a `-rc` or `-beta` suffix never is.

### What it verifies

The same checks the app runs when it updates itself:

1. `checksums.txt` carries a valid Ed25519 signature (`checksums.txt.sig`) from a release key built into ffc.
2. The download matches its SHA-256 checksum, again after it is written to disk.
3. The download URLs are the ones built from the repository and the tag, and a redirect goes only to GitHub's own download hosts over https.
4. macOS: the copied bundle passes `codesign --verify`, has the expected bundle id and version, and carries no quarantine flag.

Any failure installs nothing (exit 1). The download goes into a private folder that is removed afterwards.

## Exit codes

| Code | When |
| --- | --- |
| 0 | Done, up to date, or `status` / `--dry-run` answered. |
| 1 | Verification, asset, install or "no release" failure; not an installed copy of the app. |
| 2 | Not Windows or macOS; no `--yes` and no terminal; bad flag. |
| 5 | The install location is not writable by you. |
| 7 | GitHub unreachable or rate limited (the API allows 60 requests an hour per address). |
| 130 | Interrupted. |

See [Exit codes](exit-codes.md) for the JSON error shape.

## See also

- [Desktop app](../desktop/README.md)
- [update](update.md): the same for ffc itself
- [Security](../security.md#release-signing)
