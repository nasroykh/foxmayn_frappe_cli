# Claude Desktop extension

Install ffc in Claude Desktop with one file, without editing a config file.

ffc is not an official Frappe project and is not affiliated with Frappe Technologies.

## What it is

Every ffc release carries `ffc_<version>.mcpb`, an [MCP Bundle](https://github.com/modelcontextprotocol/mcpb): a zip that holds the ffc binary and a manifest. Claude Desktop reads it as an extension and starts `ffc mcp` for you. The tools, limits and safety rules are the ones in [Tools](tools.md) and [Safety](safety.md).

The bundle does not hold your sites or credentials. It uses the same config file as the command-line tool.

## Install

1. Set up at least one site with the command-line tool: `ffc init` (see [Setup](setup.md) for installing ffc itself).
2. Download `ffc_<version>.mcpb` from the [latest release](https://github.com/nasroykh/foxmayn_frappe_cli/releases/latest).
3. Open the file (double-click it), or in Claude Desktop go to Settings, Extensions and drag it in. Confirm the install.

To update, install the newer bundle over the old one.

## Settings

Claude Desktop shows these after the install, under the extension's settings:

| Setting | Meaning | Default |
| --- | --- | --- |
| Site | Site name from your ffc config. | Empty: the default site. |
| Config file | An ffc config file. | Empty: `~/.config/ffc/config.yaml`. |
| Read only | Expose only read tools. | Off. |

They map to `FFC_SITE`, `FFC_CONFIG` and `--read-only`. Per-site limits and confirmations in `config.yaml` apply as usual. Start with Read only on.

## Platforms

- macOS: one universal binary, Apple silicon and Intel.
- Windows: x64. It runs on Windows on Arm through emulation.
- Linux: no bundle, because Claude Desktop is not available there. Use `ffc mcp install` with your client instead.

## Not signed

The bundle and the binary inside are not signed or notarized, and the bundle is not in Anthropic's extension directory. Claude Desktop warns about an extension it cannot vouch for. Check the download against `checksums.txt` from the same release, which is signed (see [Security](../security.md)), or verify its build attestation with `gh attestation verify`.

## See also

- [MCP server](README.md)
- [Setup](setup.md): other clients, and `ffc mcp install`
