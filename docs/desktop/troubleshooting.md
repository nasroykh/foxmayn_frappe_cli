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
| Install ffc first: assistants reach your sites through it. | Install the ffc helper. See [Using the app](using.md#the-ffc-helper). |
| The ffc helper does not answer / needs attention | ffc was found but does not run. **Install again** in Settings > ffc helper. |
| The claude command was not found. | Install Claude Code, or run the commands the app shows yourself. |
| The claude command is a Windows script the app does not run. | Claude Code was installed through npm. Run the commands the app shows in PowerShell 7. |
| The assistant's settings file is read-only. | Make the file writable, then connect again. |
| The assistant's settings file could not be read, so it was left as it is. | The file has a syntax error. Fix it by hand (the app never overwrites a file it cannot parse). |
| GitHub could not be reached / is limiting requests | The update check or ffc download failed. Try again later. |
| An assistant is "Connected" but does not see the site | Restart the assistant fully. For Claude Desktop, quit it from the tray or menu bar too. |
| ffc settings found in WSL | Informational: the app manages only the Windows config. |

For CLI-side problems (credentials, redirects, clock skew, TLS), run `ffc doctor` in a terminal. See [Troubleshooting](../troubleshooting.md).

Report a problem: Settings > About > **Report a problem**, or the [issue tracker](https://github.com/nasroykh/foxmayn_frappe_cli/issues).

## See also

- [Using the app](using.md)
- [Install](install.md)
- [Troubleshooting (CLI)](../troubleshooting.md)
