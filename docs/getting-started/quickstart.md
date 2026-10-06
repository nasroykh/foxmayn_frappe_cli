# Quickstart

Connect ffc to a Frappe site and run your first commands in five minutes.

You need ffc installed ([Installation](installation.md)) and an account on a Frappe or ERPNext site (Frappe v15 or newer is what ffc is tested against).

## 1. Add your site

```bash
ffc init
```

The wizard asks for a site name (a short label such as `prod`), the site URL and how to sign in:

- **OAuth** (browser login): best for people. On Frappe v16 it usually needs no setup on the site.
- **API key**: best for scripts and CI. Create one in Frappe under **User → API Access → Generate Keys**.
- **Username and password**: simplest, but stores your password in the config file.

[Authentication](authentication.md) explains the trade-offs. Without a terminal, pass everything as flags:

```bash
echo "$SECRET" | ffc init --name prod --url https://erp.example.com --api-key KEY --api-secret-stdin
```

## 2. Check the connection

```bash
ffc ping
ffc whoami
```

`ping` reports the response time and the user you are signed in as. If anything fails, run `ffc doctor`.

## 3. Read data

```bash
ffc list-doctypes --module Selling
ffc list-docs -d Customer --fields name,customer_name,territory --limit 5
ffc get-doc -d Customer -n "CUST-0001"
ffc search acme -d Customer                # find a document name from its title
ffc get-schema -d "Sales Invoice"          # what fields does a DocType have?
```

## 4. Write data

```bash
ffc create-doc -d ToDo --data '{"description":"Try ffc"}' --json --keys name
ffc update-doc -d ToDo -n <name> --data '{"status":"Closed"}' --dry-run   # preview
ffc update-doc -d ToDo -n <name> --data '{"status":"Closed"}'
ffc delete-doc -d ToDo -n <name>                                         # asks first
```

## 5. Use it in scripts

```bash
ffc list-docs -d "Sales Invoice" --filters '{"status":"Unpaid"}' --all --output csv --fields name,customer,grand_total > unpaid.csv
ffc count-docs -d ToDo --filters '{"status":"Open"}'
ffc list-docs -d ToDo --jq '.[].name'
```

Exit codes tell scripts what went wrong ([Exit codes](../cli/exit-codes.md)), and ffc never waits for input when there is no terminal.

## 6. Connect an AI assistant (optional)

```bash
ffc mcp install --client claude-desktop --site prod --read-only
```

Restart the assistant, and it can read your site through ffc. See [MCP server](../mcp/README.md). Prefer a graphical app? [Foxmayn Frappe Desktop](../desktop/README.md) does the same with buttons.

## See also

- [CLI reference](../cli/README.md)
- [Configuration](configuration.md)
- [Troubleshooting](../troubleshooting.md)
