# Using Foxmayn Frappe Desktop

Add your sites, connect AI assistants, install the ffc helper and adjust settings.

Not an official Frappe product; not affiliated with Frappe Technologies.

The app has four screens in the sidebar: **Sites**, **Assistant**, **Connect apps** and **Settings**. Press Ctrl+K (Cmd+K on macOS) for the command palette: go to a screen, add a site, check a connection or switch the theme.

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

## Connect apps

The Connect apps screen shows Claude Desktop, Claude Code, Cursor, VS Code and Codex, whether each was found on this computer, and its status:

| Status | Meaning |
| --- | --- |
| Connected | The assistant has the app's entry with the settings shown. |
| Other settings | An entry exists but differs (another site, path or flags). **Update** replaces it. |
| Not connected | No entry. |
| Problem | The assistant's settings file could not be read. |

**Connect:** pick the **Assistant**, the **Site** ("Default site" follows whichever site is the default, or pin one site), and optionally **Read-only** (it can look at your data but cannot create, change or delete anything). The dialog shows the change it will make before writing; the assistant's settings file is backed up first. Then restart the assistant as the message says (for example, fully quit Claude Desktop, including from the tray or menu bar).

**Disconnect** removes the entry again; your sites stay saved.

The entry is named `frappe` and runs the installed ffc by its full path: the same as `ffc mcp install`. Which files are changed for each assistant: [MCP setup](../mcp/setup.md#where-each-clients-entry-goes). To use tool sets, policies or several sites in one server, use the [CLI](../mcp/running.md) or the [per-site policy](../mcp/safety.md#per-site-policy) in the config file; the app writes only the basic entry.

## Assistant

The Assistant screen is a chat inside the app. You ask a question about one of your sites, and the assistant answers from what it reads there through ffc's tools. It does not need a separate ffc install: the app runs the same ffc code itself. (**Connect apps**, which sets up other programs, still uses the installed ffc; see [The ffc helper](#the-ffc-helper).)

### Choose a model provider

The first time, the Assistant screen asks you to pick one. Options:

- **Anthropic**, **OpenAI** (GPT models) or **Google Gemini** (a Google AI Studio key): paste an API key.
- **OpenRouter**: **Sign in with OpenRouter** (below), or paste an API key.
- **Ollama** or **LM Studio**: models that run on this computer. The app looks for them at `http://localhost:11434/v1` and `http://localhost:1234/v1`. No key is needed.
- **Another server**: any server that speaks the OpenAI API, at an address you give. Use https, unless the server runs on this computer.

A pasted key is checked against the provider before it is saved. You can change providers, keys and the default model in Settings > Assistant. Anthropic, OpenAI, Gemini and OpenRouter always talk to their own hosts (the Gemini API, not Vertex AI), and the app never follows a redirect to another host. Changing the address of one of them removes its saved key, so a key is never sent to a host you did not give it to. The app does not read provider keys from environment variables such as `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.

**Sign in with OpenRouter.** Opens OpenRouter in your browser, where you allow a key for this app (labelled "Foxmayn Frappe Desktop"). The app receives it on a short-lived local address (`127.0.0.1`), checks it and saves it in the keychain; you never copy a key. It waits up to 5 minutes, and **Cancel sign-in** stops it. If the browser does not open, the page address is shown to open yourself. Pasting a key still works.

**Where keys live.** In the operating system's keychain (Windows Credential Manager, macOS Keychain), under "Foxmayn Frappe Desktop". The app window never gets a key back: it only shows the last four characters. Keys are not in the chat history or in any file the app writes.

### Ask a question

Click **New conversation** and pick the site, the provider and the model. Enter sends your message, Shift+Enter adds a line. Answers stream in, and each tool the assistant uses shows as a row (what it looked at, and whether it worked). The model sees your messages and the data its tools return, and that data goes to the provider you chose. With a local model it stays on this computer.

### Profiles

A profile sets what the assistant is for in a conversation: which tools it gets, its mode, a step limit and its instructions. Pick one in the profile picker when you start a conversation; a conversation can change it later. A profile can only narrow what the site's [policy](../mcp/safety.md#per-site-policy) allows, never widen it: anything the site refuses stays refused.

Five built-in presets:

| Preset | For |
| --- | --- |
| **Explore** | Looking things up. Read only. Where conversations go when their profile is deleted. |
| **Accounts helper** | Invoices, payments, journal entries. Asks before changes; adds the ERPNext tool set. |
| **Site admin** | Background jobs, the error log, the scheduler. Read only; adds the admin tool set. |
| **Data entry** | Creating and updating documents. Asks before changes; `delete_doc` and `bulk_delete` are hidden. |
| **Local model** | Small models on this computer. Read only, core tools, 15 steps per run. |

Presets cannot be changed. **Duplicate** one in Settings > Assistant > Profiles to edit the copy, or make a **New profile**. A profile has: mode, tool sets (none checked means core and lifecycle), tools to hide or the only tools allowed, DocTypes and methods to allow or refuse, whether server methods may be called, a step limit (1 to 100) and instructions. Deleting a profile moves its conversations to Explore.

- **Mode.** The stricter of the profile's mode and the conversation's switch applies. A profile set to read only shows a **Read only by profile** badge, and the switch cannot lift it.
- **Server methods** (`call_method`) are offered only when the profile allows them, the conversation asks before changes and the site's policy serves them.
- **Step limit.** After that many tool calls in one run the assistant pauses until you press **Continue** (25 without a profile).
- **What the model gets.** **Show what the model gets** lists the instructions and the tools the next answer starts with, and the step limit. The site's details (user, roles, versions) are added at the first answer.

### Site settings

Settings > Assistant > Site settings holds two things per site:

- **Instructions for this site.** Sent to the model in every conversation on the site, with the profile's own instructions.
- **Local models only.** Conversations on the site may use only a model server on this computer: Ollama, LM Studio or a custom server whose address is `localhost` or a loopback address. Hosted providers are refused. So are Ollama cloud models (names ending in `cloud`, such as `gpt-oss:120b-cloud`): the local Ollama passes them on to ollama.com. The rule is checked when you create a conversation, when you change its profile, before every run and before each model turn.

The settings follow a site that was renamed with `ffc` (same address, new name), as long as the old name is no longer in the ffc config; a rename never turns local only off. A conversation remembers the address its site had: if the site now points elsewhere, the run is refused and you start a new conversation.

Text that comes from the site (tool results, the site details added at the first answer) reaches the model inside blocks marked untrusted, with every `<` escaped, and the model is told to treat it as data. The stored history keeps the original text.

### Read only, or ask before changes

Each conversation has a mode:

| Mode | What the assistant can do |
| --- | --- |
| **Read only** (default) | Look at data. The tools that create, change or delete are not given to it at all. |
| **Ask before changes** | It may propose changes. Each one waits for your answer. |

You can switch the mode of a conversation while you chat.

### Approval cards

In **Ask before changes**, a change stops at a card that says **Approval needed**:

- **App card.** For creating or changing a document (and other writes ffc does not ask about itself). It shows the exact request: tool, site, DocTypes, document names and the arguments. For an update it also shows the field changes, computed from the document as it is now. If the document changed on the site before you approved, the update is refused instead of overwriting it.
- **ffc's own confirmation.** For deleting and cancelling, ffc asks itself, and its question is the card. There is exactly one card, not two.

**Approve** runs the change. **Decline** is safe: nothing is changed and the assistant is told you declined. Closing a card by stopping the run also declines it.

### Stop and Continue

**Stop** (or Esc) ends the run within about a second, closes open cards as declined and keeps what was already written in the chat. A change that was already running when you stopped may still have gone through on the site: check the document. After 25 tool calls in one run the assistant pauses so it does not go on forever; **Continue** lets it carry on.

### Limits that still apply

The assistant goes through the same checks as any connected app, from the site's [policy](../mcp/safety.md#per-site-policy): sensitive DocTypes stay read-only unless the site allows them, denied DocTypes and methods are refused, and a read-only site is read-only here too. The assistant cannot call arbitrary server methods (`call_method` is not offered). A long tool result is cut to 40 000 characters before the model sees it (the full text is kept in the history).

### Audit log

Every tool call is written to ffc's [audit log](../mcp/safety.md#audit-log) (`mcp-audit.jsonl` next to the config file) with the client name `foxmayn-desktop` and a `run_id`, which is the same for all the calls of one question. A change you approved through ffc's own confirmation shows `confirm_pending` followed by the result. A change you decline on an app card never reaches ffc, so it has no line.

### Cost

Each run shows its tokens (in, out, cached) and a cost, and the conversation shows a total. A cost comes from, in this order:

1. the cost the provider reports (OpenRouter);
2. the app's built-in price table for Anthropic, OpenAI and Gemini models, read from the providers' official pricing pages on 2026-10-09 (long-context prices and introductory prices with an end date are included). Prices change: the figure is an estimate as of that date;
3. otherwise **cost unknown**: the model is not in the table, and the app never guesses.

When any call of a conversation is unknown, its total reads **at least** the sum of the known ones. A model on this computer shows tokens only, with no cost. Conversations from 0.2.0 count as tokens only.

### Titles and renaming

After the first completed run, the app asks the conversation's own model for a one-line title (one extra call, no tools, through the same provider and the same local-only rule). If that fails, the first words of your message stay as the title. Rename a conversation with **Rename** in its header: a name you give is never replaced by an automatic title.

### Chat history

Conversations are stored on this computer only, in `assistant.db` (a SQLite file, readable only by you) in the app's data folder:

| System | Path |
| --- | --- |
| Windows | `%AppData%\Foxmayn Frappe Desktop\assistant.db` |
| macOS | `~/Library/Application Support/Foxmayn Frappe Desktop/assistant.db` |

Delete a conversation from the list to remove it. Provider names and addresses are stored there too, never keys.

- **Search.** The box above the list (Ctrl+Shift+F, Cmd+Shift+F on macOS) searches the messages, with highlighted snippets and filters for site, profile and date. It can search the archive instead of the other conversations.
- **Pin and archive.** **Pin to the top** keeps a conversation first in the list. **Archive** moves it out of the list without deleting it; **Move out of the archive** brings it back.
- **Retention.** Settings > Assistant > History: keep conversations **Forever** (the default), or delete those with no message for **90** or **30 days**. The sweep runs when the app starts and once a day, archived conversations included, and asks you to confirm the change first. Pinned conversations and ones being answered are always kept.
- **Not kept.** Turn off **Keep history** in a profile, and its conversations are marked "not kept" and removed the next time the app starts.

### Export and import

Open a conversation's menu and choose **Export as JSON** (everything, to import again) or **Export as Markdown** (to read). Secrets are redacted, passwords in addresses are removed, and no provider key is in the file; the export is refused if the keys cannot be scrubbed. The file is written in one step, so a failure leaves no half file.

**Import a conversation** (button above the list) reads a JSON export of up to 50 MB and adds it as a new conversation with new ids. A file is not trusted, even one you exported yourself:

- the conversation starts **read only**; choose a model before you continue it;
- its messages are never replayed to the model as turns of its own: they reach it as one block marked untrusted (`<imported_history untrusted="true">`, every `<` escaped), so instructions inside cannot pass as yours or the model's;
- the site details in the file are discarded and fetched fresh from the site;
- images are not in the file and become notes;
- its title is kept.

A file of another format or version, or one that does not hold together, is refused.

### Attachments

Drop files on the message box, click the paperclip, or paste an image, and they go with your next message. The app asks before it attaches dropped files. You can remove one before you send.

- Text, Markdown, log, CSV, TSV and JSON files are sent as text. An XLSX workbook is sent as CSV, one block per sheet.
- PNG, JPEG, WebP and GIF images are sent only to a model that reads images: Claude 3 and later; OpenAI GPT-4o, GPT-4.1, GPT-4.5, GPT-5 and the o1, o3 and o4 chat models (not mini previews, audio, realtime, search, Codex, image or embedding models); and Gemini 1.5 and later. OpenRouter and models on your computer get no images yet. If you switch a conversation to a model without images, earlier images reach it as a short note.
- At most 10 MB a file, 5 files a message, 200 000 characters of text a file and 400 000 a message. A pasted image may be up to 5 MB.
- PDF files cannot be attached yet (planned for a later release). A file whose content does not match its name, such as a renamed executable, is refused.

The model gets an attached file's text marked as untrusted data, like what it reads from the site, so instructions inside a file are not followed as yours. Attached text is not part of the history search. An export keeps the text of attached files but not the images; importing it turns each image into a note.

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
