# Desktop troubleshooting

Messages Foxmayn Frappe Desktop may show, and what to do about them.

| Message | What to do |
| --- | --- |
| Windows protected your PC (SmartScreen) | Beta builds are unsigned: **More info**, then **Run anyway**. See [Install](install.md#windows). |
| macOS: the app cannot be opened, or "is damaged" | Use **Open Anyway** in Privacy & Security, or remove the quarantine flag. See [Install](install.md#macos). |
| The site could not be reached. Check the address and your internet connection. | Check the address in a browser. Behind a VPN or proxy, make sure this computer can reach the site. |
| The site did not accept these sign-in details. | Re-enter the API key and secret, or the password. Accounts with two-factor authentication must use browser sign-in or an API key. |
| The site answered with an error (<status>). | A server-side problem. Try `ffc doctor` in a terminal for details. |
| This site can't set up browser sign-in for the app by itself. | Frappe v15, or app registration is turned off. Use **Advanced: use your own OAuth client**, or an API key. See [Using the app](using.md#add-a-site). |
| The sign-in took too long. | Finish the browser sign-in within 5 minutes, then try again. |
| Your browser did not open | Copy the sign-in link the app shows into your browser. |
| Your sign-in has expired. Sign in again to keep using this site. | Add the site again under the same name and tick **Replace the saved site called <name>**. |
| The ffc config file could not be read. Fix or remove it, then try again. | `config.yaml` has a syntax error, probably from a manual edit. Fix it (Settings > General > Open folder), or move it away and add your sites again. |
| Install ffc first: apps reach your sites through it. | Install the ffc helper. See [Using the app](using.md#the-ffc-helper). |
| The ffc helper does not answer / needs attention | ffc was found but does not run. **Install again** in Settings > ffc helper. |
| The claude command was not found. | Install Claude Code, or run the commands the app shows yourself. |
| The claude command is a Windows script the app does not run. | Claude Code was installed through npm. Run the commands the app shows in PowerShell 7. |
| The app's settings file is read-only. | Make the file writable, then connect again. |
| The app's settings file could not be read, so it was left as it is. | The file has a syntax error. Fix it by hand (the app never overwrites a file it cannot parse). |
| GitHub could not be reached / is limiting requests | The update check or ffc download failed. Try again later. |
| This copy was not installed by the app's installer ... so it cannot update itself. | A development build, an app run from a disk image or moved out of its folder. Install the app with the installer or the script, then update from there; until then use the command in Settings > About. |
| This app's folder cannot be changed by your user, so it cannot update itself. | The app lives where your user cannot write (for example `/Applications` owned by another user). Run the update command from Settings > About, or move the app to `~/Applications`. |
| The download did not pass its safety checks, so nothing was installed. | The signature or checksum of the update did not match. Your current version was kept. Try again later or download it from the release page. |
| The release does not have the expected files. / No release of the app was found on GitHub. | The release is incomplete or not published yet. Try again later. |
| The update could not be installed. Your current version was kept. | The installer or the copy step failed. Run the command from Settings > About in a terminal to see the error. |
| The update is installed, but the app could not restart itself. | Close the app and open it again; it is already the new version. |
| An update is already running. / The update was cancelled. | Wait for the running update, or start it again. Nothing was installed after a cancel. |
| An app is "Connected" but does not see the site | Restart the app fully. For Claude Desktop, quit it from the tray or menu bar too. |
| ffc settings found in WSL | Informational: the app manages only the Windows config. |

For CLI-side problems (credentials, redirects, clock skew, TLS), run `ffc doctor` in a terminal. See [Troubleshooting](../troubleshooting.md).

Report a problem: Settings > About > **Report a problem**, or the [issue tracker](https://github.com/nasroykh/foxmayn_frappe_cli/issues).

## See also

- [Using the app](using.md)
- [Install](install.md)
- [Troubleshooting (CLI)](../troubleshooting.md)
