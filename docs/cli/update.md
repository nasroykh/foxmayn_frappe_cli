# update

Update ffc in place to the latest signed release. A copy installed by a package manager is updated through that manager instead.

```bash
ffc update           # check, show the new version, ask, install
ffc update --check   # only say whether an update is available
ffc update --yes     # install without asking
```

| Flag | Description |
| --- | --- |
| `--check` | Only check; install nothing. |
| `-y, --yes` | Skip the confirmation. |

## What it verifies

`ffc update` installs a release only when:

1. its `checksums.txt` carries a valid Ed25519 signature (`checksums.txt.sig`) from a release key built into ffc, and
2. the downloaded archive matches its SHA-256 checksum.

Anything else is refused. Someone who can replace the release files cannot also forge the signature. Versions before 1.6.1 did not check signatures, so the first update from such a version relies on the checksum alone. Details: [Security](../security.md#release-signing).

The newest release is the newest non-draft, non-prerelease release tagged `v<number>...`; desktop app releases (`desktop-v...`) are never picked.

## Where it installs

ffc replaces the running binary in its current location. On Windows the running `ffc.exe` is moved aside first, so updating works even while a detached MCP server runs. If ffc lives in a directory you cannot write to (for example `/usr/local/bin`), it says so; rerun with the needed rights (`sudo ffc update`).

Entries written by `ffc mcp install` keep working: they point at the same path.

## Package managers

A package manager keeps track of the version it installed, so `ffc update` does not replace its copy. It still checks, then stops with the command to run:

| Installed with | ffc lives under | Update with |
| --- | --- | --- |
| Homebrew | `<prefix>/Cellar/ffc/` or `<prefix>/Caskroom/ffc/` | `brew upgrade ffc` |
| Scoop | `<scoop root>/apps/ffc/` (also `$SCOOP`, `$SCOOP_GLOBAL`) | `scoop update ffc` |
| winget | `...\WinGet\Packages\...` or `...\WinGet\Links\` | `winget upgrade ffc` |

`ffc update --check` works as usual, and the daily notice and `ffc doctor` name the same command.

## Automatic update check

At most once a day, ffc checks GitHub in the background and prints a one-line notice on stderr when a newer version exists:

```text
Update available: v1.10.0 → v1.11.0  (run: ffc update)
```

For a package manager's copy, the notice names its command instead (see above).

The check is skipped for `update`, `mcp`, `completion` and `help`. A failing network costs at most one attempt per day. To turn it off, set `FFC_NO_UPDATE_CHECK` to any value.

## See also

- [Installation](../getting-started/installation.md)
- [Security](../security.md)
- [doctor](doctor.md)
