# Releasing

How CLI and desktop releases are cut, signed and published. Maintainers only.

The two products release independently from the same repository, told apart by tag prefix:

| Product | Tag | Workflow | GitHub "latest"? |
| --- | --- | --- | --- |
| ffc CLI | `vX.Y.Z` (`vX.Y.Z-rc1` for a pre-release) | `.github/workflows/release.yml` (GoReleaser) | Yes, for final releases |
| Desktop app | `desktop-vX.Y.Z` | `.github/workflows/desktop-release.yml` | **Never** |

## CLI release

```bash
git tag v1.12.0
git push origin v1.12.0
```

`release.yml` then:

1. checks `go mod tidy -diff`, runs `go vet` and the tests, and fails the release on any error;
2. resolves the previous final tag for the changelog (so an rc and a final tag on the same commit work);
3. runs GoReleaser: CGO-free builds for linux, darwin and windows on amd64 and arm64, archives `ffc_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), `checksums.txt`;
4. signs `checksums.txt` with `go run ./tools/relsign sign` using the `FFC_RELEASE_SIGNING_KEY` secret, producing `checksums.txt.sig`;
5. publishes the GitHub release (a tag with a suffix such as `-rc1` becomes a pre-release, so it never becomes "latest");
6. attests build provenance for every archive and both checksum files.

The changelog leaves out `docs:`, `test:` and `chore:` commits and merge commits.

### Signing keys

`internal/relsig/keys.go` lists the trusted public keys, and `install.sh` and `install-desktop.sh` repeat them (a test keeps them in sync). There are two: the CI key, and an offline backup key whose private half is not in CI.

- `relsign` refuses a signing secret that does not match a trusted key, so a release cannot publish an unverifiable signature.
- **Rotate:** generate a key with `go run ./tools/relsign keygen <file>`, add its public key to `ReleaseKeys`, `install.sh` and `install-desktop.sh` next to the old one, ship a release signed with the old key, then switch the secret.
- **Compromised CI key:** switch the secret to the backup key (binaries since v1.6.3 trust it), remove the compromised key from `ReleaseKeys`, `install.sh` and `install-desktop.sh` in that release, and create a new offline backup.
- Never commit a private key.

### Which release users get

`ffc update`, the background update check and the desktop app's ffc installer pick the newest non-draft, non-prerelease release whose tag is `v<digit>...`, from the releases list, so desktop releases are skipped. `install.sh` and `install.ps1` read GitHub's "latest" release, as do ffc versions up to v1.11.0. That is why a desktop release must never be marked latest.

## Desktop release

1. Write `desktop/release-notes/X.Y.Z.md`. Without it, a default "unsigned beta" text is used.
2. Tag and push:

   ```bash
   git tag desktop-v0.1.0
   git push origin desktop-v0.1.0
   ```

`desktop-release.yml` then builds a per-user NSIS installer on Windows (`wails3 package INSTALL_SCOPE=user`) and a universal (arm64 + amd64) `.dmg` on macOS, with the version from the tag stamped into the app. It writes `checksums.txt` for both, signs it with `relsign`, attests provenance, and creates the release with `--latest=false`, plus `--prerelease` for 0.x versions or a version with a `-` suffix.

Assets: `foxmayn-frappe-desktop-<version>-windows-amd64-setup.exe`, `foxmayn-frappe-desktop-<version>-macos-universal.dmg`, `checksums.txt`, `checksums.txt.sig`.

`install-desktop.sh` and `install-desktop.ps1` (repository root) install the newest `desktop-v<X.Y.Z>` release (no `-` suffix) by these asset names, so renaming an asset breaks them. The app's update notice runs them from the new release's tag (`desktopInstallCommand`), so tag a commit that has them. Put the two one-liners in the release notes (the default notes have them).

A pull request that touches the workflow or `desktop/build/`, and a manual run, build and package the same way as a dry run: workflow artifacts only, version `0.0.0-dev`, never a release.

**Code signing.** Apple Developer ID signing and notarization and Windows Authenticode signing are wired up but run only when the signing secrets exist. As of 2026-10-06 they have never run, so builds are unsigned. The secrets are `APPLE_CERTIFICATE_P12_BASE64`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`, `WINDOWS_CERTIFICATE_PFX_BASE64` and `WINDOWS_CERTIFICATE_PASSWORD`.

## See also

- [Security: release signing](../security.md#release-signing)
- [Testing](testing.md)
- [Desktop app development](desktop.md)
