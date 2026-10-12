# Desktop clean-machine test

A manual test of a desktop release on a machine that has never run ffc or the app: a new Windows user account or virtual machine, or a fresh macOS user. Run it for each release candidate before 1.0, and for any release that changes the installer, the install scripts or signing. CI cannot cover it: the runners already have Go, Node, WebView2 and a warm cache.

Write down the release tag, the OS version and build, and the date. Note each step as pass or fail; for a failure, keep the exact message and a screenshot.

## Before you start

- [ ] No `~/.config/ffc` folder, no `ffc` on `PATH`.
- [ ] Windows: no `%LOCALAPPDATA%\Programs\Foxmayn Frappe Desktop` and no `%APPDATA%\Foxmayn Frappe Desktop`. macOS: no app in `/Applications` or `~/Applications`, and no `~/Library/Application Support/Foxmayn Frappe Desktop`.
- [ ] No "Foxmayn Frappe Desktop" entries in Windows Credential Manager or the macOS keychain.
- [ ] A disposable test site, never a production one, and a model provider key you can revoke afterwards.

## Install

Test both ways on at least one machine per OS.

Terminal install ([Install](../desktop/install.md#install-from-a-terminal-recommended)):

- [ ] The command from the release notes, pinned to the tag, installs and opens the app.
- [ ] Windows: no SmartScreen prompt; the app is in `%LOCALAPPDATA%\Programs\Foxmayn Frappe Desktop` and in the Start menu.
- [ ] macOS: no Gatekeeper prompt. With OpenSSL 3 installed the script reports that the signature was checked; without it, it says that it could not check.
- [ ] Run the command again while the app is open. Windows closes and reopens the app. macOS asks you to quit it first.

Manual download:

- [ ] `checksums.txt.sig` verifies with `go run ./tools/relsign verify`, and the file's SHA-256 matches `checksums.txt`.
- [ ] Windows: SmartScreen shows "More info" and then "Run anyway". The installer needs no administrator rights. WebView2 is present or gets installed.
- [ ] macOS: the first launch is blocked, and "Open Anyway" in Privacy & Security opens the app. If macOS reports the app as damaged, the `xattr` line in the install notes fixes it.

## First run

- [ ] The app opens in the OS language when that is English, French or Arabic. Arabic is right to left.
- [ ] Settings > About shows the release version, not `0.0.0-dev`.
- [ ] The ffc helper alert offers **Install ffc**. It asks first, then installs to `%LOCALAPPDATA%\Programs\ffc` (Windows) or `~/.local/bin/ffc` (macOS). A new terminal finds `ffc --version`.

## Sites

- [ ] Add the test site with an API key. `~/.config/ffc/config.yaml` is created; on macOS it has mode 0600 and its folder 0700.
- [ ] Add a site with a username and password, and one with OAuth (the browser opens; the callback lands on `127.0.0.1`).
- [ ] Change a site's address to `https://` on the same host: it saves without asking for the sign-in. Change it to another host: the dialog asks for the API key and secret (or username and password) again, and nothing is saved until they check out.
- [ ] Add a site again under an existing name (Replace). The app asks first, with Cancel as the default.
- [ ] Safety rules: a change that opens something lists what it opens and asks first; a narrowing change saves without asking. Remove a site that has rules: the question says the rules go with it.
- [ ] Turning Local models only off asks first.

## Assistant

- [ ] Add a provider key. It shows up in Credential Manager or the keychain, never in `config.yaml`.
- [ ] A read question answers without an approval card.
- [ ] `update_doc` shows a card with a field diff. Approve it, and the change is on the site.
- [ ] Declining a card changes nothing on the site.
- [ ] A `restore_doc` card names the DocType it brings back.
- [ ] Attach an XLSX, a DOCX, a PDF and an image. Each is read or refused with a clear message.
- [ ] Drop a file on the message box: it is attached. Drop it on macOS too (the drop path changed in 0.9.0).

## MCP clients

- [ ] Connect an installed AI client (for example Claude Desktop). It lists the ffc tools and answers a read.
- [ ] Disconnect it. The client's config no longer has the entry.

## Activity

- [ ] Activity lists the Assistant's calls and the MCP client's calls, and the filters work.

## Update

- [ ] Install the previous release, then run the new release's terminal command. Sites, conversations and provider keys are kept.
- [ ] Settings > About shows the update notice with the pinned command when a newer release exists.

## Uninstall

- [ ] Windows: Settings > Apps removes the app folder and its shortcuts. macOS: moving the app to the Bin removes it.
- [ ] These stay, as [What stays after uninstalling](../desktop/install.md#what-stays-after-uninstalling) says: `~/.config/ffc`, the `ffc` binary, connected client entries, provider keys in the keychain, and the conversation store (`assistant.db` in `%APPDATA%\Foxmayn Frappe Desktop` or `~/Library/Application Support/Foxmayn Frappe Desktop`).
- [ ] Reinstalling after the uninstall finds the sites and conversations again.

## After the test

- [ ] Revoke the provider key and the test site's API keys.
- [ ] File each failure as an issue, with the release tag, the OS and the step.
