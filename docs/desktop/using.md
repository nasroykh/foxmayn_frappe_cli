# Using Foxmayn Frappe Desktop

Add your sites, connect AI assistants, install the ffc helper and adjust settings.

Not an official Frappe product; not affiliated with Frappe Technologies.

The app has three screens in the sidebar: **Sites**, **Assistants** and **Settings**. Press Ctrl+K (Cmd+K on macOS) for the command palette: go to a screen, add a site, check a connection or switch the theme.

## First run

When there is no ffc config yet, a short welcome tour explains sites and assistants. Its last page offers **Add your first site**, or **Look around first**. If ffc is not installed, the app also shows **One more thing: install the ffc helper**; see [The ffc helper](#the-ffc-helper).

## Sites

The Sites screen lists every site in `~/.config/ffc/config.yaml`, including ones added with the `ffc` command line. Each row shows the address, a **Default** badge, and the last check ("Connected as <user>" or "Last check failed"). Plain `http://` addresses get a warning: the connection is not encrypted.

### Add a site

Click **Add a site** (sidebar or Sites screen). Four steps:

1. **Site address and name.** Enter the address (for example `erp.example.com`). The name is suggested from it; it is how ffc and the assistants refer to the site. To replace a saved site with the same name, tick **Replace the saved site called <name>**.
2. **Choose how to sign in:**
   - **Sign in with your browser** (recommended). The app never sees your password, and it works with two-factor authentication.
   - **Use an API key.**
   - **Use your username and password.** Saved on this computer; accounts with two-factor authentication cannot use it.
3. **Sign in.**
   - Browser: the app opens the site's sign-in page and waits up to 5 minutes. If the browser does not open, copy the sign-in link instead.
   - API key: paste the **API key** and **API secret** (in Frappe: My Settings > API Access), then **Check and save**.
   - Username and password: enter them and click **Sign in**.
4. **Done.** Optionally **Use as the default site**, or go straight to **Connect an assistant**.

The app checks the credentials before saving anything.

**Browser sign-in on Frappe v15 (or with app registration turned off).** On Frappe v16 the app registers itself with the site automatically. Older versions do not allow that, so the app asks for your own OAuth client: open **Advanced: use your own OAuth client**, create an OAuth Client in Frappe (Grant Type "Authorization Code", Response Type "Code", with the redirect URI the app shows), and paste its **Client ID** (and **Client secret**, if it has one). Or use an API key instead. Details: [Authentication](../getting-started/authentication.md#oauth-20).

### Manage a site

Each site's menu has:

| Action | What it does |
| --- | --- |
| **Check connection** | Signs in and shows who you are signed in as. An expired browser sign-in is renewed first; if the site no longer accepts it, sign in again. |
| **Make default** | The site used when no site is named (by ffc and by assistants set to "Default site"). |
| **Connect an assistant** | Opens the connect dialog with this site selected. |
| **Rename…** | Changes the site's name. Assistants pinned to the old name stop working until you reconnect them. |
| **Change address…** | Moves the site to a new address after checking the saved credentials against it. Not available for browser sign-in sites: remove and add them again. |
| **Remove…** | Removes the site. For a browser sign-in site, the app first revokes its token on the site (best effort, 10 seconds). |

## Assistants

The Assistants screen shows Claude Desktop, Claude Code, Cursor, VS Code and Codex, whether each was found on this computer, and its status:

| Status | Meaning |
| --- | --- |
| Connected | The assistant has the app's entry with the settings shown. |
| Other settings | An entry exists but differs (another site, path or flags). **Update** replaces it. |
| Not connected | No entry. |
| Problem | The assistant's settings file could not be read. |

**Connect:** pick the **Assistant**, the **Site** ("Default site" follows whichever site is the default, or pin one site), and optionally **Read-only** (it can look at your data but cannot create, change or delete anything). The dialog shows the change it will make before writing; the assistant's settings file is backed up first. Then restart the assistant as the message says (for example, fully quit Claude Desktop, including from the tray or menu bar).

**Disconnect** removes the entry again; your sites stay saved.

The entry is named `frappe` and runs the installed ffc by its full path: the same as `ffc mcp install`. Which files are changed for each assistant: [MCP setup](../mcp/setup.md#where-each-clients-entry-goes). To use tool sets, policies or several sites in one server, use the [CLI](../mcp/running.md) or the [per-site policy](../mcp/safety.md#per-site-policy) in the config file; the app writes only the basic entry.

## The ffc helper

Assistants reach your sites through the `ffc` command line, so it must be installed. The sidebar footer shows its version, or "ffc helper missing".

**Install ffc** (in the alert, or Settings > ffc helper) asks first, then:

1. fetches the newest ffc release from GitHub;
2. verifies the Ed25519 signature of its checksums and the SHA-256 of the archive, exactly as `ffc update` does, and refuses anything unverified;
3. installs it for your user only:
   - Windows: `%LOCALAPPDATA%\Programs\ffc\ffc.exe`, added to your user PATH.
   - macOS: `~/.local/bin/ffc`. If that folder is not on your shell's PATH, the app shows the line to add to `~/.zprofile`. Assistants use the full path, so they work either way.

The app finds an existing ffc on PATH or where the install scripts put it. When a newer ffc release is out, the app says so (see [Updates](#updates)) and **Update** replaces the ffc it found, where it is, with the same checks. A development build of ffc (version `dev`), a program that does not answer like ffc, or a wrapper script (such as `ffc.cmd`) is never replaced: the app installs a release in its own folder instead. An ffc that Homebrew, Scoop or winget installed is left to that package manager: the app neither replaces it nor installs a second copy, and shows its update command (such as `brew upgrade ffc`). You can also run `ffc update` in a terminal. After an update, restart your connected assistants so they start the new ffc. The ffc helper tab also shows the install script command to run by hand.

## Settings

| Tab | Contents |
| --- | --- |
| General | **Theme** (Light, Dark, System). **Settings file**: the path of `config.yaml`, with **Open folder**. |
| ffc helper | Installed version and path, **Install ffc** / **Update to X** / **Install again**, **Look again**, **Open folder**. |
| About | Version, **Check for updates**, **Source code**, **Report a problem**. |

Number and date formats are CLI settings: `ffc config`.

The app uses `~/.config/ffc/config.yaml`, or the file `FFC_CONFIG` names, as the CLI does. With another file, assistant connections get `--config <path>` so `ffc mcp` reads the same file. On macOS an app opened from Finder or the Dock does not see variables set in a shell profile: run `launchctl setenv FFC_CONFIG /path/to/config.yaml` (until the next login), or start the app from a terminal with `open -a "Foxmayn Frappe Desktop"`.

## Updates

At start, at most once a day, the app asks GitHub for the newest `desktop-v` release. When one is newer, it shows a notice with a **Download** button (opens the release page) and a dot on Settings. Settings > About > **Check for updates** checks at any time. The app does not update itself: download and run the new installer. Development builds (version `0.0.0-dev`) never show the notice. The same check compares the installed ffc with the newest ffc release (the one `ffc update` would install): when it is newer, a notice offers **Update**, the sidebar shows "ffc X available", and Settings > ffc helper shows **Update to X**. The check cannot be turned off.

## WSL (Windows)

If a running WSL distribution has its own ffc settings, the app says so. The app manages only the Windows config; the two are separate and not synced.

## See also

- [Install](install.md)
- [Troubleshooting](troubleshooting.md)
- [MCP safety](../mcp/safety.md)
