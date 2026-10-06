# Authentication

Choose how ffc signs in to each site (OAuth, API key, or username and password) and set it up.

## Which method to use

| | OAuth 2.0 | API key | Username and password |
| --- | --- | --- | --- |
| Best for | People at a desk | Scripts, CI, servers | Quick local testing |
| Setup on the site | None on Frappe v16 with dynamic client registration; otherwise one OAuth Client | Generate a key pair for the user | None |
| Needs a browser | Yes, once per setup | No | No |
| What is stored in `config.yaml` | Client ID (and secret, if any), access and refresh tokens | API key and secret | Your account password, in clear text |
| Two-factor authentication | Works (you sign in through the site) | Not involved | Not supported |
| Revocable without changing your password | Yes (`ffc site remove`, or an admin) | Yes (regenerate keys) | No |
| Non-interactive setup | Flags skip the prompts, but the browser login still needs a person | Yes | Yes |

The config file is written with mode 0600 in every case; ffc does not use the OS keychain. See [Security](../security.md#where-credentials-live).

## OAuth 2.0

```bash
ffc init --oauth                                   # or: ffc site add --oauth
ffc init --oauth --name prod --url https://erp.example.com
ffc site add --oauth --client-id 1a2b3c4d5e        # use an OAuth Client you created
```

ffc opens your browser on the site's login and consent page and waits up to 5 minutes for you to finish. It signs in as a public OAuth client (Authorization Code with PKCE, no client secret) with the redirect URI `http://127.0.0.1:<port>/callback` and the scopes `openid all`.

**Token refresh is automatic.** An expired access token is refreshed before any command that talks to the site. A long run (bulk commands, `list-docs --all`, `api --paginate`, the MCP server) that outlives the token refreshes it when the site rejects it and repeats that request once. Several ffc processes share one refresh. If the refresh fails, the command fails with exit 3 and a hint to run `ffc site add --oauth` again.

### Frappe v16: no setup

When the site offers dynamic client registration (**OAuth Settings → Enable Dynamic Client Registration**, with **Show Auth Server Metadata** on; both are on by default), ffc registers its own OAuth Client: named `ffc (<your computer's host name>)`, for this login's callback URL only, with no secret. The site shows its consent page on the first login.

- Each OAuth setup registers a new client. An administrator can delete old ones under **OAuth Client**.
- Newer Frappe releases allow 5 registrations per 10 minutes from one IP address; a 429 error says so.

### Frappe v15, or registration turned off

The wizard says why registration is not possible, prints the redirect URI, and asks for the ID (and secret, for a confidential client) of an OAuth Client you create once:

1. In Frappe, go to **Integrations → OAuth Client → New**.
2. Grant Type: **Authorization Code**. Scopes: `openid all`.
3. Redirect URI: the one ffc printed (`http://127.0.0.1:<port>/callback`).
4. Save and paste the client ID into ffc.

`--client-id ID` uses a given client and never registers one; its secret, if it has one, comes from `FFC_OAUTH_CLIENT_SECRET`. Without `--client-id` and without registration on the site, the non-interactive form (`--oauth --name --url`) is a usage error (exit 2).

Earlier ffc versions used `localhost` in the redirect URI. Update an OAuth Client registered that way to `127.0.0.1`.

### Signing out

`ffc site remove <name>` revokes the current token on the server first (best effort). Refresh tokens issued earlier for the same login stay valid until an administrator revokes them in the **OAuth Bearer Token** list.

## API key

```bash
ffc init --apikey
echo "$SECRET" | ffc init --name prod --url https://erp.example.com --api-key KEY --api-secret-stdin
FFC_API_SECRET="$SECRET" ffc site add --name ci --url https://erp.example.com --api-key KEY
```

Create the key pair in Frappe under **User → API Access → Generate Keys** (for your user, or a dedicated integration user with only the roles it needs). ffc checks the keys against the site before saving them.

In CI you can skip the config file entirely: see [Configuration: environment variables](configuration.md#environment-variables).

## Username and password

```bash
ffc init --password
echo "$PW" | ffc init --name dev --url http://localhost:8000 --username admin --password-stdin
```

ffc logs in with a session cookie at the start of each command and logs out when it ends. The password is stored in `config.yaml` (mode 0600).

- **Two-factor authentication is not supported.** An account with 2FA fails with a hint to use OAuth or an API key.
- On a site with **Deny Multiple Sessions** (System Settings), each ffc login can end your other sessions, such as the browser one. Use OAuth or an API key there.

## Secrets are never flag values

Non-interactive setup never takes a secret on the command line, where it would land in shell history and process lists. Pipe it with `--api-secret-stdin` / `--password-stdin` (one line; a terminal on stdin is refused), or set `FFC_API_SECRET` / `FFC_PASSWORD`.

## See also

- [Sites and settings](../cli/sites-and-settings.md)
- [Configuration](configuration.md)
- [Security](../security.md)
