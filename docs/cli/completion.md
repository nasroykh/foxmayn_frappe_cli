# Completion

Tab-complete commands, flags, site names, DocTypes, fields and report names in bash, zsh, fish and PowerShell.

## Install

The release archives already hold the scripts, in `completions/` (`ffc.bash`, `ffc.zsh`, `ffc.fish`, `ffc.ps1`), next to the man pages in `manpages/`. Copy one to the place below instead of generating it, if you prefer.

| Shell | This session | Every session |
| --- | --- | --- |
| bash (needs the `bash-completion` package) | `source <(ffc completion bash)` | Linux: `ffc completion bash > /etc/bash_completion.d/ffc`; macOS: `ffc completion bash > $(brew --prefix)/etc/bash_completion.d/ffc` |
| zsh | `source <(ffc completion zsh)` | Linux: `ffc completion zsh > "${fpath[1]}/_ffc"`; macOS: `ffc completion zsh > $(brew --prefix)/share/zsh/site-functions/_ffc` |
| fish | `ffc completion fish \| source` | `ffc completion fish > ~/.config/fish/completions/ffc.fish` |
| PowerShell | `ffc completion powershell \| Out-String \| Invoke-Expression` | Add that line to your PowerShell profile. |

For zsh, if completion is not enabled yet, run once: `echo "autoload -U compinit; compinit" >> ~/.zshrc`. Start a new shell afterwards. Every subcommand takes `--no-descriptions` to leave out the descriptions.

## What completes

- Site names: `--site`, `site use/remove/rename/edit`, `config set --default-site`, `mcp --sites`.
- DocTypes: `-d/--doctype` on every command, `cache warm --doctypes`, `mcp --allow-doctypes/--deny-doctypes`.
- Fields: `--fields` (the last item of a comma-separated list).
- Report names: `run-report -n`.
- Fixed values: `--output`, `--number-format`, `--date-format`, `can --perm`, `mcp --toolsets/--confirm/--allow-tools`, `mcp install/uninstall --client`, `--debug`.

Document names (`-n/--name`) are never completed.

## Fill the cache first

Completion reads only the config file and the [local cache](schema-and-cache.md#the-local-cache). It never sends a request or signs in, so with no fresh cache entry it offers nothing. Fill the cache once per site:

```bash
ffc cache warm --doctypes "Sales Invoice,Customer"
```

DocType and report lists stay fresh for 24 hours, schemas (for field completion) for one hour.

## See also

- [Schema and cache](schema-and-cache.md)
- [CLI reference](README.md)
