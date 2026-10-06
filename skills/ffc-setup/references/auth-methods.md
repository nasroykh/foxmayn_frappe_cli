# Auth methods and setup flags

`ffc init` creates the config file (first site); `ffc site add` adds a site to an existing one. Both take the same flags.

| Flag | Meaning |
| --- | --- |
| `--oauth` / `--apikey` / `--password` | skip the method menu |
| `--name NAME` | site name (non-interactive) |
| `--url URL` | site URL (non-interactive). A bare host gets `https://`; any path is dropped (the site root is used) |
| `--api-key KEY` | API key; secret from `--api-secret-stdin` or `FFC_API_SECRET` |
| `--api-secret-stdin` | read the secret from stdin (one line) |
| `--username USER` | username or email; password from `--password-stdin` or `FFC_PASSWORD` |
| `--password-stdin` | read the password from stdin (one line) |
| `--client-id ID` | OAuth: use this OAuth Client, never register one; its secret (if any) from `FFC_OAUTH_CLIENT_SECRET` |
| `--force` | replace an existing site or config without asking |

Without a terminal, pass `--name`, `--url` and one credential set. The secret is never a flag value, so it never lands in shell history or process lists. Credentials are verified against the site before the config is written; a failed check writes nothing.

## OAuth (browser login, Authorization Code + PKCE)

- Only tokens are stored (`oauth_client_id`, `access_token`, `refresh_token`, `token_expiry`). Expired access tokens refresh automatically before a command and once on a 401 mid-run.
- Frappe v16 with OAuth Settings > Enable Dynamic Client Registration (on by default): ffc registers its own public OAuth Client. Nothing to prepare.
- Frappe v15, or registration off: the wizard explains and asks for the ID of an OAuth Client the user creates (Integrations > OAuth Client > New) with the redirect URI it prints, `http://127.0.0.1:<port>/callback` (not `localhost`). Non-interactive `--oauth --name N --url U` then needs `--client-id`, else exit 2.
- `--oauth --name --url` skips the prompts but still opens a browser, so it is not for headless CI. Use an API key there.
- `ffc site remove` revokes the current OAuth token first (best effort). Refresh tokens issued earlier for the same login stay valid until an administrator revokes them.
- A refresh failure warns on stderr ("refreshing the OAuth token ... failed"); fix it with `ffc site add --oauth --name SAME --url URL --force` or `ffc init --oauth`.

## API key + secret

- Generate on the site: User > API Access > Generate Keys (the secret is shown once).
- Sent as `Authorization: token key:secret`. Best for scripts, CI and long-running MCP servers.
- `FFC_API_KEY` + `FFC_API_SECRET` (as a pair) override the stored credentials of the selected site at run time; see config.md.

## Username + password

- Logs in on every run and logs out at the end. The password is stored in the config file in clear text (file mode 0600); prefer OAuth or an API key.
- Frappe two-factor authentication is not supported: ffc stops with an error pointing to OAuth or an API key.

## Site names

Keep their case and may contain dots (`Prod`, `erp.example.com`). `--site` and `default_site` match exactly first, then a unique case-insensitive match.
