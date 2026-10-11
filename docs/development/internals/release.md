# Release internals

Moved out of the root CLAUDE.md so it loads only when the work touches this area. Notes for contributors and AI agents; the root file keeps the rules that apply everywhere. Desktop release notes live in `desktop/CLAUDE.md`.

## Release (CLI)

Releases are automated via GoReleaser + GitHub Actions (`.github/workflows/release.yml`).

To cut a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions will cross-compile for linux/darwin/windows × amd64/arm64, create a GitHub Release, upload the tarballs, and generate `checksums.txt` and its Ed25519 signature `checksums.txt.sig`. Each archive holds the binary, README.md, LICENSE, `completions/` (ffc.bash, ffc.zsh, ffc.fish, ffc.ps1) and `manpages/` (one section-1 page per command), written by the `before` hook `go run ./tools/gendocs . <version> <commit time>` (gitignored; the commit time dates the pages, since a global `env` entry would see `.CommitTimestamp` as zero).

Next to the archives the release uploads one Claude Desktop extension, `ffc_<version>.mcpb` (MCP Bundle manifest 0.3; docs/mcp/bundle.md): a zip of manifest.json, icon.png, `server/ffc` (macOS universal) and `server/ffc.exe` (windows amd64), packed deterministically by `go run ./tools/mcpb` from the post hook of the `universal_binaries` entry (`replace: false`; that pipe runs after every build, so the Windows binary exists). The archive lists `ids: [ffc]` so no darwin_all archive reaches the cask or scoop. `checksum` and `release` take `./dist/*.mcpb` as `extra_files`, so the signed checksums.txt covers it, and release.yml attests `dist/*.mcpb`. Optional user_config keys need a `default` (an unset key without one reaches the process as the literal `${user_config.x}`). No Linux bundle.

**MCP Registry.** `server.json` (schema 2025-12-11, name `io.github.nasroykh/ffc`, description at most 100 characters) lists that bundle as its one `mcpb` package. `.github/workflows/mcp-registry.yml` fills in `version`, `identifier` and `fileSha256` (from the release's checksums.txt after `relsign verify`) and runs mcp-publisher (pinned version and SHA-256) with `login github-oidc`. It runs after a successful Release of a final tag only when the repository variable `MCP_REGISTRY_PUBLISH` is `true`, or by hand with a tag. Published versions cannot be changed or removed; `mcp-publisher validate` checks a file without publishing.

**Release signing.** GoReleaser's `signs` step runs `go run ./tools/relsign sign` with the `FFC_RELEASE_SIGNING_KEY` repository secret (base64 Ed25519 seed). `ffc update` verifies the signature against `internal/relsig/keys.go` and refuses a release without a valid one. relsign refuses a secret that does not match `ReleaseKeys`, and `TestReleaseKeysConfigured` fails while `ReleaseKeys` is empty. To rotate: generate a key with `go run ./tools/relsign keygen <file>`, add its public key to `ReleaseKeys` (and `RELEASE_KEYS` in install.sh) next to the old one, ship a release signed with the old key, then switch the secret. Never commit a private key.

**Backup key.** `ReleaseKeys` also holds an offline backup key (second entry) whose private half is not in CI. If the CI key is lost or leaked, switch `FFC_RELEASE_SIGNING_KEY` to the backup key (released binaries since v1.6.3 already trust it), remove the compromised key from `ReleaseKeys` and install.sh in that release, and generate a new offline backup.


End users install with:

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.sh | sh

# Windows (PowerShell or cmd.exe)
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.ps1 | iex"
```

**Key files:**
- `.goreleaser.yaml` — build matrix, archive naming and contents (gendocs hook), checksum config, `homebrew_casks` (tap nasroykh/homebrew-tap; post-install hook strips the quarantine flag, since the binary is not notarized) and `scoops` (bucket nasroykh/scoop-bucket), both `skip_upload: auto` (no rc) and pushed with the `TAP_GITHUB_TOKEN` secret (fine-grained, Contents read/write on those two repos only; the release fails without it)
- `.github/workflows/release.yml` — triggers on `v*` tags; runs `go mod tidy -diff`, vet and tests, then GoReleaser and a build-provenance attestation. Actions are pinned to commit SHAs.
- `.github/workflows/ci.yml` — gofmt, tidy check, vet, race tests, staticcheck, govulncheck, vet and tests on windows-latest (no -race), and a cross-build on every push/PR
- `install.sh` — Linux/macOS: detects OS/arch, downloads tarball, verifies SHA256, installs to `/usr/local/bin` or `~/.local/bin`
- `install.ps1` — Windows: detects arch, downloads zip, verifies SHA256, installs to `%LOCALAPPDATA%\Programs\ffc`, adds to user PATH
