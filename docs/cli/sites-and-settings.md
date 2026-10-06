# Sites and settings

Create the config, manage the sites in it, change display settings and check the connection: `init`, `site`, `config`, `ping`.

## Create the config: `ffc init`

```bash
ffc init             # menu: OAuth, API key, or username/password
ffc init --oauth     # straight to the OAuth browser login
ffc init --apikey    # straight to the API key form
ffc init --password  # straight to the username/password form
```

`init` writes `~/.config/ffc/config.yaml` (or the path given with `--config`). It checks the credentials against the site before writing. A URL without a scheme gets `https://`.

Non-interactive (no terminal needed): pass `--name`, `--url` and one credential set. The secret is never a flag value.

```bash
echo "$SECRET" | ffc init --name prod --url https://erp.example.com --api-key KEY --api-secret-stdin
FFC_API_SECRET="$SECRET" ffc init --name prod --url erp.example.com --api-key KEY --force
echo "$PW" | ffc init --name dev --url http://localhost:8000 --username admin --password-stdin
ffc init --oauth --name prod --url https://erp.example.com   # no prompts, still a browser login
```

| Flag | Description |
| --- | --- |
| `--oauth` / `--apikey` / `--password` | Pick the sign-in method (mutually exclusive). |
| `--name` | Site name (non-interactive setup). |
| `--url` | Site URL (non-interactive setup). |
| `--api-key` | API key; the secret comes from `--api-secret-stdin` or `FFC_API_SECRET`. |
| `--api-secret-stdin` | Read the API secret from stdin (one line). |
| `--username` | Username or email; the password comes from `--password-stdin` or `FFC_PASSWORD`. |
| `--password-stdin` | Read the password from stdin (one line). |
| `--client-id` | OAuth: use this OAuth Client instead of registering one. Its secret, if any, comes from `FFC_OAUTH_CLIENT_SECRET`. |
| `--force` | Replace an existing config (for `init`) without asking. |

Which method to choose, and how OAuth client registration works: [Authentication](../getting-started/authentication.md).

## Manage sites: `ffc site`

```bash
ffc site list                       # name, URL, sign-in method, default
ffc site add                        # add a site (menu to choose the sign-in method)
ffc site add --oauth                # add a site through the OAuth browser login
ffc site use [NAME]                 # set the default site (menu when NAME is omitted)
ffc site remove [NAME] [--yes]      # remove a site (menu when NAME is omitted)
ffc site rename OLD NEW             # rename; default_site follows if OLD was the default
ffc site edit NAME --url URL        # change the URL; credentials are re-checked first
```

### `site add`

Takes the same flags as `init` (`--oauth`, `--apikey`, `--password`, `--name`, `--url`, `--api-key`, `--api-secret-stdin`, `--username`, `--password-stdin`, `--client-id`, `--force`). Replacing an existing site needs `--force`.

```bash
echo "$SECRET" | ffc site add --name staging --url https://staging.example.com --api-key KEY --api-secret-stdin
echo "$PW" | ffc site add --name dev --url http://localhost:8000 --username admin --password-stdin --force
ffc site add --oauth --client-id 1a2b3c4d5e    # use an OAuth Client you created
```

### `site remove`

Without `--yes` it asks for confirmation. With no terminal, a missing `--yes` is a usage error (exit 2) and the config is left unchanged.

For an OAuth site, ffc first revokes the current token on the server (its refresh token and the access token issued with it). This is best effort and limited to 10 seconds: if the site cannot be reached or refuses, ffc prints a warning and removes the site anyway. Refresh tokens issued earlier for the same login stay valid until an administrator revokes them (Frappe keeps each refresh as a new OAuth Bearer Token record). The OAuth Client itself stays on the site.

### `site edit`

Changes only the URL. The stored credentials are checked against the new URL first; a failing check leaves the config unchanged. An OAuth site cannot be moved this way, because its client and tokens belong to the old server: run `ffc site add --oauth --force` with the new URL instead.

### `site rename`

Keeps the site's position and comments in the config file.

Removing, renaming or editing a site, and adding a site over an existing name, also deletes that site's [local cache](schema-and-cache.md#the-local-cache).

## Display settings: `ffc config`

```bash
ffc config                                   # interactive settings screen
ffc config get                               # all settings as a table
ffc config get --json                        # or --yaml / -y
ffc config set --default-site prod
ffc config set --number-format us --date-format dd/mm/yyyy
```

| Setting | Flag | Values |
| --- | --- | --- |
| Default site | `--default-site` | Any configured site name. |
| Number format | `--number-format` | `french` (default, `1 000 000,00`), `us` (`1,000,000.00`), `german` (`1.000.000,00`), `plain` (`1000000.00`). |
| Date format | `--date-format` | `yyyy-mm-dd` (default), `dd-mm-yyyy`, `dd/mm/yyyy`, `mm/dd/yyyy`. |

The formats apply to tables only. JSON, CSV and other machine formats print values as the site sent them.

## Check the connection: `ffc ping`

```bash
ffc ping
ffc ping --site dev --json
```

`ping` calls `frappe.ping` and reports the response time. Because `frappe.ping` answers without credentials, `ping` also asks who the credentials belong to and prints the user. Credentials the site rejects, or that it treats as Guest, fail with exit code 3.

To check only that a site answers, without credentials, use `curl -fsS https://<site>/api/method/frappe.ping`. For a full check of the setup, use [`ffc doctor`](doctor.md).

## See also

- [Authentication](../getting-started/authentication.md)
- [Configuration](../getting-started/configuration.md)
- [doctor](doctor.md)
