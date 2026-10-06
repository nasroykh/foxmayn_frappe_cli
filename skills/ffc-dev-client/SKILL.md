---
name: ffc-dev-client
description: Change the ffc HTTP client, auth and config layer safely - internal/client (FrappeClient, do/send, hints and error classes, retries, redirects, raw and streamed requests, dry runs, debug redaction), API key / session / OAuth auth and token refresh, OAuth registration and revocation (internal/sitesetup), and config reads and writes (internal/config - config.Edit, locks, env overrides). Use it whenever you add a Frappe API call, touch authentication or login, change how requests are sent or retried, or write to config.yaml, and whenever Frappe answers in an unexpected shape. Load ffc-dev first.
---

# Client, auth and config internals

These layers carry the security boundary (credentials never reach another host) and the exit-code contract. CLAUDE.md "Common Pitfalls" holds the detailed Frappe evidence; read the bullet for the area you touch.

## Adding a Frappe API call

```go
// client.go style: one method per Frappe call, ctx first, hints for 401/403/404.
func (c *FrappeClient) CreateDoc(ctx context.Context, doctype string, data map[string]interface{}) (map[string]interface{}, error) {
	hints := readHints(doctype)
	hints[http.StatusForbidden] = fmt.Sprintf("permission denied (403): your user may not have create access to %s", doctype)
	var env dataEnvelope
	if err := c.do(ctx, http.MethodPost, resourcePath(doctype), data, nil, hints, &env); err != nil {
		return nil, c.missingDocType(ctx, doctype, err)
	}
	return env.doc() // a 2xx without "data" is an error, not an empty doc
}
```

- Everything goes through `c.do(ctx, method, path, body, query, hints, out)` → `send`: auth header per request, session relogin, OAuth refresh on 401, dry-run hold-back, debug trace, error conversion. Do not add a second request path.
- `readHints(doctype)` / `docHints(doctype, name, access)` give actionable 401/403/404 messages; `docMethod` makes a 404 from a whitelisted method name the document, not the method.
- Return the typed errors `classify` understands: `*APIError` (status + `exc_type`), `*AuthError`, `*TransportError`, `*StateError` (wrong document state, exit 6), `*RedirectError`. Messages come from `frappeErrorResponse.userMessage()`; non-JSON bodies are cut to 300 runes and sanitised.
- A Frappe quirk you rely on gets a test against the fake and, when the fake cannot prove it, a contract test (ffc-dev-testing).

Frappe response shapes that trip people up: [references/frappe-api-quirks.md](references/frappe-api-quirks.md).

## Transport rules (http.go, raw.go, files.go)

- Only GET (and HEAD) follow redirects; a redirected write fails with the target URL. Never https → http, never another host while `Authorization` or `Cookie` is set.
- Retries: GET only, on 429/502/503/504 and transport errors; never after timeout, cancel, refused redirect or TLS error; Retry-After honoured up to 10 s. **Never add retries to writes.**
- `Raw` (used by `ffc api`, downloads) is streamed with no retries and no 128 MiB cap; `SitePath` refuses absolute and `//host` URLs, `CheckHeaders` refuses `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name`, `X-Forwarded-Host`. Keep both checks in the client.
- Streamed downloads run the GET retry policy in their own loop (resty's retries leak a streamed body). Multipart uploads build a fresh streamed body per attempt inside `send` so a relogin replays it intact.
- Non-site HTTP (GitHub releases) uses `client.NewHTTPClient(timeout)`, never `resty.New()` (its logger prints `WARN RESTY` lines).
- Anything that logs requests or plans uses the redaction helpers in debug.go (`TestDebugNeverPrintsSecrets` covers all auth methods). Printed URLs go through `redactedURL`.

## Auth

| Method | Header | Picked when (in order) |
| --- | --- | --- |
| OAuth | `Authorization: Bearer <token>` (attached per request in `send`) | `cfg.AccessToken != ""` (`IsOAuth`) |
| API key | `Authorization: token key:secret` | `APIKey` + `APISecret` |
| Password | `Cookie: sid=...` after a login inside `client.New` | `IsSessionAuth()` (username and password) |

- `client.New` is fallible (session sites log in). Commands get clients from `callSite`/`newClient` (internal/cmd/site_client.go): `loadSite` = `loadSiteConfig` (no network) + OAuth refresh under the config lock; `newSiteClient` adds the in-run `tokenRefresher`.
- On a 401 an OAuth client probes `frappe.auth.get_logged_user` with the same token: only a refused token triggers one single-flight refresh and one repeat (writes included). A 401 raised by the method itself is returned, never retried. Definitive refresh failures (token endpoint 400/401/403, no refresh token) are remembered per token.
- Session sites log in once per invocation and must log out (`CloseQuietly`), or every run leaves a live session. Long-lived clients relogin on 401/403 only after checking the session is really gone. Never persist a `sid`. 2FA is not supported (`LoginPassword` returns an error pointing to OAuth or an API key).

## OAuth setup and revocation (internal/sitesetup)

- Prompt-free and cobra-free (the desktop app uses it); `internal/cmd` keeps forms, spinners and messages.
- PKCE callback server binds `127.0.0.1` (redirect URI `http://127.0.0.1:<port>/callback`, never `localhost`), accepts exactly one result, answers duplicates with 409, always closes on abort.
- `ResolveOAuthApp`: `--client-id` as is; else dynamic registration when `/.well-known/oauth-authorization-server` has `registration_endpoint` (Frappe v16): one public client, exactly this run's redirect URI, scope `openid all`, never twice per run; else `*NoRegistrationError` (wizard prompts for a client id; non-interactive setup turns it into a usage error naming `--client-id`). Posts only to the configured site's host.
- `site remove` calls `RevokeToken` (refresh token, `client_id` in the form, no Authorization header) before taking the config lock, bounded by 10 s, warning only on failure.

## Config (internal/config)

- **All writes** go through `config.Edit(path, func(f *config.File) error {...})` or `config.Overwrite` for a fresh file: lock (`config.yaml.lock`), re-read, edit the `yaml.Node` tree (comments and key order survive), atomic write at 0600, symlinks followed. Return `config.ErrUnchanged` to skip. Network calls while holding the lock stay within `config.MaxLockHold` (30 s). Helpers on `File`: `Get`, `Set`, `HasSite`, `SiteNames`, `Site`, `PutSite`, `RemoveSite`, `RenameSite`, `SetSiteURL`, `SetSiteTokens`.
- Site writes from commands go through `siteStore(path)` (`sitesetup.Store`), which also drops the site's cache (`sitecache.Drop`).
- Reads: `config.Load(site, path)` (env overrides applied) or `config.Read(path)`; `resolveCfgPath()` for the path. `Config`/`SiteConfig` have `yaml` tags only; `SiteConfig.Name` is runtime-only, the exact YAML key.
- Site names keep case and dots; matching is exact, then unique case-insensitive. Do not reintroduce viper (it lowercased keys and split on dots).
- Env: `FFC_API_KEY`+`FFC_API_SECRET` only as a pair (replace stored credentials); `FFC_URL` only with the pair, error alone when different; no config file → the three vars define the site. `applyEnvOverrides` and `PutSite` must keep a site's `mcp` block.
