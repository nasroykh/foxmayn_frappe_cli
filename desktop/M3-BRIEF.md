# Desktop V1 M3 brief (temporary)

Branch-only file. Delete it in the last commit before the PR is merged. It carries context from the local vault, which cloud sessions cannot read.

## Goal
Desktop V1 M3 = desktop 0.4.0 (beta): languages, accessibility, polish. Base: main 12cd841 (M0 ffc v1.19.0, M1 desktop 0.2.0, M2 desktop 0.3.0 all released).

Scope (vault plan): i18n infrastructure and French and Arabic catalogs for the whole app (not only the assistant), RTL, Arabic font, keyboard shortcuts, aria-live, focus handling, empty states and starter prompts, "Connect apps" rename (copy and docs; label already shipped), a feedback link, Vitest and Playwright with axe in CI.

Acceptance:
- every screen renders in the three languages with no clipped text in screenshots (light, dark, LTR, RTL);
- axe finds no serious or critical issue;
- the whole chat flow works with the keyboard only;
- a native Arabic speaker and a French speaker review the strings (human step by Nas; gates the release tag, not the merge).

Decisions already made (DV8: English, French, Arabic with RTL; DV13: "Assistant" and "Connect apps"; DV9 no telemetry). Nas approved every open-question recommendation below on 2026-10-10.

## Approved answers
1. Arabic font: Noto Sans Arabic (variable, `@fontsource-variable/noto-sans-arabic`, OFL-1.1), after Nunito in `--font-sans`; import only the wght axis; check built size.
2. Language: first run follows the OS (localStorage `ffd-language` > navigator fr/ar > en); switcher in Settings, command palette and onboarding page 1.
3. Latin digits in Arabic (`ar-u-nu-latn`).
4. Arabic: Modern Standard Arabic, ar-DZ date conventions.
5. Go errors: translatable `key` + args on `services.Error` for the common paths only; technical Detail stays English.
6. Model-facing prompts stay English, plus one line telling the model to reply in the user's language.
7. Starter prompts: 4 per preset, drafted by you, fill the composer only (never send). Nas edits later.
8. Feedback: GitHub issue form (`.github/ISSUE_TEMPLATE/desktop-feedback.yml`), "Send feedback" in the sidebar footer and the palette, prefilled version/os/lang. Keep the existing Settings > About "Report a problem".
9. Playwright in CI: ubuntu, Chromium only.
10. No internal rename (screen id `assistants`, `AssistantsService` stay). Rename the file to `connect-apps-screen.tsx` only.
11. 0.4.0: tagged directly (no rc), prerelease, `--latest=false`, only after Nas's OK and the native-speaker review. DO NOT tag or release.

# M3 plan: desktop 0.4.0. Base 12cd841. Paths under desktop/; F=desktop/frontend/src. UNVERIFIED = not checked in code/runs.
GOAL: whole app en/fr/ar + RTL + Arabic font, keyboard/screen-reader basics, empty states, starter prompts, Connect apps copy, feedback link, Playwright+axe in CI (vault Plan:130-134).

## Verified state
| Area | Finding (file:line) |
|---|---|
| i18n | F/i18n/index.ts:4-6 only en; lng "en" hardcoded :24; no detection/persistence; dir()+applyDocumentLanguage :8-30. en.json 338 lines, assistant only; 0 plural keys ({{count}} en.json:70,93). |
| Key test | F/test/i18n-keys.test.ts: only t(literal) exists in en.json. No fr/ar parity, no hardcoded check. |
| Hardcoded | 17/38 non-ui tsx have no t(): add-site-sheet (778 lines), sites-screen, connect-dialog, disconnect-dialog, site-dialogs, install-ffc-dialog, onboarding, page, copy-field, secret-input, wsl-alert, App, app-context. settings-screen 1 t() (tabs :60-63, About :408-414); app-header.tsx:39; lib/labels.ts:1-35 (timeAgo English); lib/errors.ts:38-50; ui: spinner:6, dialog:75, sheet:73, sidebar:275,287,290, toast:120. |
| Locale fmt | conversation-search.tsx:256, settings-screen.tsx:341 use browser locale; usage-line.tsx:14,30, chat-pane.tsx:440 use i18n.language. |
| Go strings | services.Error.Message English, ~91 sites (errors.go:29-100). Native dialogs: attachments.go:91-101 (ConfirmDroppedFiles), :172; history_export.go:451,539. Preset names profiles.go:96-118. No tray/menu. settings table store/history.go:83-97. Theme in localStorage app/theme.tsx:7-25 + public/theme-init.js. |
| Font | index.css:4,10 Nunito only, no Arabic glyphs. CSP font-src self: bundled fontsource needs no CSP change. noto-sans-arabic variable 5.3.0 OFL-1.1 (npm view). Arabic woff2 size UNVERIFIED (est 100-250 KB). |
| RTL | ui/direction.tsx DirectionProvider unused (main.tsx). ~30 physical tokens in 10 app files (app-header:65, page:59,82, connect-dialog:190, conversation-list:153,173,210, text-left x7 e.g. tool-row:55, markdown:86); ~150 in ui. Icons: add-site-sheet:558-603, onboarding:98,103, assistant-onboarding:115, ChevronRight x12. |
| Shortcuts | Ctrl+K App.tsx:71; Ctrl+Shift+F conversation-search.tsx:76; Esc stop chat-pane.tsx:189-196; Enter :596; Ctrl+B ui/sidebar.tsx:33. |
| a11y | live region chat-pane.tsx:550, announce :212-218; approval focus approval-card.tsx:42-44; refocus chat-pane:223. Missing: focus on screen change App.tsx:101-108; hover-only actions conversation-list.tsx:173,210. |
| Empty | assistant-screen:167,204; sites-screen:83; assistants-screen:78. Chat empty = one line chat-pane.tsx:488-490. |
| Rename | Label done (en.json:3,6; app-sidebar.tsx:24-25; 0.2.0.md:30). Copy left: page:90, add-site-sheet:541, assistants-screen:85,95,97, site-dialogs:141,218, sites-screen:159, mock:940,958; docs/desktop/README.md:10-11,27, using.md:3,11,21,45,210,218,220, troubleshooting.md:17, README.md:13,72, desktop/README.md:30. |
| Feedback | Report a problem -> REPO/issues settings-screen.tsx:42,412 (About only). No ISSUE_TEMPLATE. OpenWebsite any http(s) app.go:165-174. |
| CI | desktop.yml win+mac; last run 9/18 min (37988859078). Mock port 9245 strictPort vite.config.ts:41-45; params mock/backend.ts:70+. |

## One branch feat/desktop-m3: stage reviews locally, one push, one CI run
| Stage | Work | Files |
|---|---|---|
| S1 infra | Detect lang (localStorage ffd-language > navigator fr/ar > en), persist, pre-paint lang+dir in theme-init.js, DirectionProvider, switcher (Settings General, palette, onboarding p1), pseudo-locale in mock. Go AppService.SetLanguage + catalog for 4 native dialogs; Error.key+args, UI t(key,{defaultValue:message}). Regen bindings, sync backend.ts/backend-types.ts/mock (rule 11). | i18n/index.ts, theme-init.js, main.tsx, settings-screen, command-palette, onboarding, app.go, attachments.go, history_export.go, errors.go, new i18n.go, lib/errors.ts, bindings |
| S2 strings | Extract all hardcoded (rows above), Intl by i18n.language, plurals, preset names by id, Go keys on common paths (errors.go:80-99, assistant_loop.go:195-289, approval:94). fr.json, ar.json drafts. Assistants->apps copy + docs; file rename to connect-apps-screen.tsx, keep screen id. | Hardcoded/Rename files, locales, docs |
| S3 RTL+font | Logical props in app + used ui (shadcn migrate rtl UNVERIFIED available; else by hand); mirror arrows; dir=auto on bubbles/composer/data inputs; code+diff forced ltr; Noto Sans Arabic after Nunito in --font-sans. | index.css, package.json, ~10 app, ~15 ui, markdown.tsx, diff-view.tsx |
| S4 a11y+polish | Focus heading on screen change; Ctrl+N new chat, Ctrl+comma settings, Ctrl+/ shortcut sheet; keyboard-reachable conv actions; empty states (conversation-list:141, no results); 4 starter prompts per preset (fill composer only); Send feedback in sidebar footer + palette -> issues/new?template=desktop-feedback.yml prefilled version/os/lang. | App.tsx, app-sidebar, command-palette, chat-pane, conversation-list, shortcuts-dialog.tsx, .github/ISSUE_TEMPLATE |
| S5 tests+CI | Vitest parity (fr/ar keys = en, ar plural forms, no empty, same vars); Go test keys exist in en.json. Playwright 1.64 + @axe-core/playwright 4.13 on vite build --mode mock + preview (prod CSP). New ubuntu chromium job e2e, screenshots artifact, est +5-7 min parallel (UNVERIFIED). | playwright.config.ts, frontend/e2e/, package.json, desktop.yml |
| S6 docs | release-notes/0.4.0.md (desktop-release.yml:330), using.md, CLAUDE.md rule 14, handoff. | listed |
Separable if wanted: ISSUE_TEMPLATE; Playwright harness en-only. Default: in the batch.

## Tests per acceptance
| Criterion | Test |
|---|---|
| 3 langs, no clipping, light/dark/LTR/RTL | e2e/screens.spec.ts ~25 states x en/fr/ar x light/dark. Fail if overflow hidden and scrollWidth>clientWidth+1 unless .truncate/data-clip-ok. Pseudo-locale run flags hardcoded text. Screenshots artifact for manual look; no pixel baselines. |
| axe | AxeBuilder tags wcag2a/2aa/21aa per state; fail on serious/critical. |
| Keyboard-only chat | e2e/keyboard.spec.ts keyboard only: palette, Ctrl+N, site/profile, send, Decline focused, Tab Approve, Esc stop, Ctrl+Shift+F; focus ring asserted; rerun ar. |
| Native review | Human, gates tag not merge. |

## Open questions (rec, confidence)
1 Arabic font: Noto Sans Arabic variable; alt Cairo/Readex Pro. (med-high)
2 Default OS lang; switcher Settings+palette+onboarding. (high)
3 Latin digits in Arabic (ar-u-nu-latn). (high; WebView2 ar default UNVERIFIED)
4 MSA, ar-DZ date conventions. (med)
5 Go errors: keys for common paths, Detail English. (med)
6 Model-facing prompts stay English + reply-in-user-language line (assistant_loop.go:42-49). (high)
7 Starter prompts: 4/preset, we draft, Nas edits. (med)
8 Feedback via GitHub issue form; users may lack accounts. (med-high)
9 Playwright CI: ubuntu chromium only, WebKit skipped. (med-high)
10 No internal rename (screen id, AssistantsService). (high)
11 0.4.0 direct tag, prerelease, not latest, after review. (high)

## Risks
- Missed strings (dynamic keys escape regex; pseudo-locale is the net).
- WebView2 accelerators (Ctrl+N/P/F/R) may beat preventDefault; main.go sets none; test in real app.
- shadcn ui RTL edits: sidebar.tsx:292, message-scroller.tsx:100 need visual check.
- Bidi with Latin site data; code/diff must stay LTR.
- Binary size from Arabic font; import wght axis only.
- Arabic 6 plural forms; parity test must not demand forms en lacks.
- Go dialogs use OS lang until SetLanguage.
- Port 9245 strictPort clash locally.
- Existing vitest asserts English: pin tests to en.

## Standing rules
- No AI attribution anywhere (commits, PR text, code comments, files). Messages read as if Nas wrote them. Conventional commit titles.
- Never commit `.agents/` or `skills-lock.json`.
- Confirm with Nas before outward-facing actions: releases, tags, new repos, paid services. Do not merge; open the PR and stop (Nas merges reviewed green PRs himself, squash).
- Batch: one branch `feat/desktop-m3`, one local stage review per stage, commit per stage. Push at the end of S5 (or when a CI check is needed), not per stage.
- Agents by family alias only (sonnet, opus, fable), no haiku, effort at most high. Opus reviewer for cross-file architecture.
- CLAUDE.md has one lone CR: edit it with Python `newline=""` and assert the CR count stays 1. Read the "Desktop Assistant (0.3.0)" bullet (rules 1-13) first; add rule 14 for M3.
- Bindings: after any Go service surface change run `wails3 generate bindings -clean=true -ts -i` and update `lib/backend.ts`, `lib/backend-types.ts`, `mock/backend.ts`. desktop.yml fails on a diff.
- Windows CRLF noise on go.mod, go.sum, bindings: use `git diff --ignore-cr-at-eol` (not an issue on Linux).
- No cgo locally for `-race` (macOS CI only). `main` and `TestMain` must call `cmd.RunJQChildIfRequested()` first.
- Never run contract tests against acme or compta.
- A running vite dev server locks node_modules; mock UI port 9245 is strictPort.
- Gates: `cd desktop/frontend && npm ci && npm run build && npm test`; `cd desktop && go vet ./... && go test ./...`.
- Verify claims in this plan against the code before relying on them: line numbers drift, UNVERIFIED items are unchecked (Arabic font size, `shadcn migrate rtl` availability, WebView2 accelerators Ctrl+N/P/F/R, e2e CI time).
- Cloud sandbox cannot run the real Wails window or WebView2: the WebView2 accelerator risk and the native dialogs need Nas's manual test on Windows. Say so in the PR body as a test checklist.
