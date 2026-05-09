# Supacli

Supacli is a local-first CLI for working with SaaS tools from one
policy-aware command surface.

It is built for both people and agents:

1. Humans get readable tables, colors, markdown rendering, pagers, browser
   opens, and interactive selectors when running in a terminal.
2. Agents and scripts get stable `json` and `yaml` output with no prompts,
   spinners, pagers, browser opens, or ANSI escapes.

Supacli stores provider tokens in your operating system credential store by
default. The hosted or self-hosted `supaclid` server supports provider
connection flows that require confidential client secrets, but it does not
provide a cloud token vault in the initial design.

## Status

Supacli is early software. The first usable provider is Notion.

| Provider | Status | Notes |
| --- | --- | --- |
| Notion | Active | OAuth connect, search, page reads/writes, links, page tree, and data sources. |
| Linear | Planned | No command surface is registered yet. |
| Jira | Planned | No command surface is registered yet. |
| Slack | Planned | No command surface is registered yet. |
| Google Docs | Planned | No command surface is registered yet. |
| Google Drive | Planned | No command surface is registered yet. |
| Gmail | Planned | No command surface is registered yet. |

## Install

With Homebrew:

```bash
brew tap fiam/tap
brew install supacli
supacli version
```

Or build from source:

```bash
git clone https://github.com/fiam/supacli.git
cd supacli
make dev-cli
supacli version
```

Released archives include only the `supacli` CLI for macOS, Linux, and
Windows on amd64 and arm64. The `supaclid` server daemon is released only as a
Linux amd64/arm64 container image at `ghcr.io/fiam/supaclid:<tag>`. Use Go
1.26.3 or newer on the Go 1.26 line when building from source. Docker is only
required for the full linter pass and container image builds.

## Connect Notion

The CLI defaults to hosted `https://api.supacli.com` for brokered OAuth.

```bash
supacli connect notion
supacli status notion
supacli doctor notion
```

For a local or self-hosted `supaclid`, point the CLI at your server:

```bash
export SUPACLI_SUPACLID_URL=https://auth.example.com
supacli connect notion
```

To print the OAuth URL without waiting for completion:

```bash
supacli connect notion --auth-url-only
```

To disconnect and remove the local token:

```bash
supacli disconnect notion --yes
```

Self-hosting instructions are in [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).
Notion app setup notes are in
[docs/providers/notion-app.md](docs/providers/notion-app.md).
Self-hosted `supaclid` exposes `/healthz` for health checks and `/build` for
JSON or plaintext build metadata.

## Notion Examples

Search:

```bash
supacli notion search roadmap
supacli notion search --query tasks --type data_source
supacli notion search --limit 10 --sort edited --direction desc
```

Read pages:

```bash
supacli notion page read "Roadmap"
supacli notion page read --follow "Roadmap"
supacli notion page markdown "Roadmap"
supacli notion page links "Roadmap"
supacli notion page open "Roadmap"
```

Navigate page structure:

```bash
supacli notion page children "Roadmap"
supacli notion page tree "Roadmap" --depth 3
```

Create and update pages:

```bash
supacli notion page create \
  --parent-type workspace \
  --title "Meeting Notes" \
  --markdown "# Meeting Notes"

supacli notion page update "Meeting Notes" --title "Team Notes"
supacli notion page content insert "Team Notes" --markdown "## Followups"
supacli notion page content replace "Team Notes" \
  --markdown "# Replacement" \
  --yes
```

Inspect page export fidelity before automated edits:

```bash
supacli notion page doctor "Roadmap"
```

Work with data sources:

```bash
supacli notion data-source query <data-source-id>
supacli notion data-source schema <data-source-id>
supacli notion data-source row create <data-source-id> \
  --title "New Row"
supacli notion data-source row update <page-id> \
  --title "Updated Row"
```

## Output Modes

Human output is the default:

```bash
supacli notion page read "Roadmap"
```

Use structured output for agents and scripts:

```bash
supacli --output json notion page links "Roadmap"
supacli --output yaml status notion
```

Global output controls:

```bash
--output table|json|yaml
--color auto|always|never
--pager auto|always|never
--read-only
```

Interactive features are disabled automatically when Supacli is not attached
to a terminal.

## MCP

Supacli exposes implemented provider actions as Model Context Protocol tools
over stdio. The MCP server is generated from the same provider action metadata
as the CLI, and tool calls still pass through local policy checks,
`--read-only`, credential profiles, account selection, and provider auth.

```bash
supacli mcp serve
```

Use `mcp serve` directly when an agent lets you provide a command manually:

```bash
supacli mcp serve \
  --mcp-profile notion-read \
  --tool 'notion.*' \
  --exclude-tool '*.delete'
```

`mcp serve` accepts `--mcp-profile`, `--tool`, `--tool-regex`,
`--exclude-tool`, and `--exclude-tool-regex`.

### Agent Setup

Supacli can configure supported local agent CLIs automatically:

```bash
supacli mcp configure
```

Supported agent targets:

| Agent | Names | Scope support |
| --- | --- | --- |
| Codex | `codex` | Codex default MCP config |
| Claude Code | `claude`, `claude-code` | `local`, `user`, `project` |
| Gemini CLI | `gemini`, `gemini-cli` | `user`, `project` |

When no agent is named, `mcp configure` autodetects supported installed CLIs.
Interactive runs show detected agents as checkboxes, preselect agents where
Supacli MCP is configured and enabled, and show the detected command, scope, or
config path. Unchecking an already configured agent removes the Supacli MCP
server from that agent.

```bash
supacli mcp configure codex claude gemini
```

For scripts, use explicit non-interactive commands. With no agent arguments,
`enable` and `disable` autodetect supported installed CLIs.

```bash
supacli mcp enable codex claude
supacli mcp disable gemini
```

`mcp configure` is the interactive manager. `mcp enable` adds or replaces the
Supacli MCP server in the selected agents. `mcp disable` removes the Supacli
MCP server from the selected agents.

Claude Code and Gemini CLI default to user-scoped MCP config for consistency.
Use `--scope project` for project-scoped agent config, or `--claude-scope
local` when you want Claude Code's private local project scope.

`supacli mcp configure` and `supacli mcp enable` accept these configuration
options:

- `--command`: executable path written into agent MCP config; defaults to
  `supacli`.
- `--server-name`: MCP server name written into agent config; defaults to
  `supacli`, or `supacli-<profile>` when `--mcp-profile` is set.
- `--scope`: common agent config scope for Claude Code and Gemini CLI;
  defaults to `user`; accepts `user` or `project`.
- `--claude-scope`: Claude Code-specific override; accepts `local`, `user`, or
  `project`.
- `--gemini-scope`: Gemini CLI-specific override; accepts `user` or `project`.
- `--dry-run`: print the agent CLI commands without running them.
- `--mcp-profile`, `--tool`, `--tool-regex`, `--exclude-tool`, and
  `--exclude-tool-regex`: configure the launched server's tool selection.

Global Supacli flags passed to `mcp configure` or `mcp enable` are also written
into the launched `supacli mcp serve` command when relevant:

- `--profile`
- `--account`
- `--policy`
- `--read-only`

`supacli mcp disable` removes the Supacli MCP server from the selected agents.
It accepts `--server-name`, `--mcp-profile`, and `--dry-run`. Claude Code and
Gemini CLI removal checks all scopes that Supacli supports for those agents, so
disabling cleans up stale local, user, and project entries.

Examples:

```bash
supacli mcp enable codex \
  --command /opt/supacli/bin/supacli \
  --mcp-profile notion-read \
  --read-only \
  --dry-run

supacli mcp enable claude gemini \
  --scope project \
  --tool 'notion.page.*' \
  --exclude-tool '*.delete'

supacli mcp disable claude gemini --mcp-profile notion-read
```

### MCP Profiles

Use MCP profiles to expose only selected tools. Supacli stores profiles in the
general Supacli config: global config is
`$XDG_CONFIG_HOME/supacli/config.yaml` or the platform user config directory,
and project config is `.supacli/config.yaml`. Project config overrides global
config with the same profile name or default profile, like Git config layering.

```yaml
version: 1
mcp:
  default_profile: notion-read
  profiles:
    notion-read:
      tools:
        - "notion.*"
      exclude_tools:
        - "*.create"
        - "*.update"
        - "*.delete"
      tool_regex:
        - "^notion\\.page\\."
      exclude_tool_regex:
        - "\\.delete$"
```

```bash
supacli mcp profile set notion-read \
  --tool 'notion.*' \
  --exclude-tool '*.create' \
  --exclude-tool '*.update' \
  --exclude-tool '*.delete'

supacli mcp configure codex --mcp-profile notion-read
```

Profile commands:

```bash
supacli mcp profile set <name>
supacli mcp profile default <name>
supacli mcp profile ls
supacli mcp profile show <name>
```

Set a default profile so `supacli mcp serve` uses it even when no
`--mcp-profile` is passed:

```bash
supacli mcp profile default notion-read
```

Use `--global` to write global Supacli config. Without `--global`, profile
commands write project-local config. `--local` is also accepted for explicit
project writes.

Filters support shell-style globs through `--tool` and `--exclude-tool`, and
regular expressions through `--tool-regex` and `--exclude-tool-regex`. You can
also pass filters directly during configuration:

```bash
supacli mcp configure claude gemini \
  --mcp-profile notion-pages \
  --tool 'notion.page.*' \
  --exclude-tool '*.delete'
```

## Local Policy

Supacli can enforce local command policy before it reads provider credentials
or calls provider APIs.

Create a starter policy:

```bash
supacli policy init
```

Inspect available policy-aware commands:

```bash
supacli policy catalog
```

The catalog lists implemented provider actions and their remote/local effects.
Use `--read-only` to block actions with remote or local write effects.

Check a command:

```bash
supacli policy check --command "notion page read Roadmap"
```

Policy discovery order:

1. `--policy <path>`
2. `SUPACLI_POLICY=<path>`
3. `.supacli/policy.yaml` in the current directory or a parent directory
4. No policy file means local usage is allowed by default

Policy files are local guardrails for projects and automation. They are not a
security boundary against a user who controls the machine or working directory.

## Token Custody

Supacli uses local token custody:

1. `supaclid` starts a browser OAuth flow.
2. The provider redirects back to `supaclid`.
3. `supaclid` exchanges the code when a client secret is required.
4. The CLI retrieves the token bundle once over HTTPS.
5. The CLI stores provider tokens in the OS credential store.
6. `supaclid` keeps only short-lived handoff data in process memory.

This keeps long-lived provider tokens on your machine instead of in Supacli's
hosted service.

## Self-Hosting

You can run your own `supaclid` with your own provider OAuth apps and secrets.
See [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).

The public repository contains portable source, generic container builds, fake
upstream tests, and self-hosting docs. Supacli's hosted deployment
infrastructure and provider secrets are intentionally outside this repository.

## Development

Contributor setup and project conventions are in
[CONTRIBUTING.md](CONTRIBUTING.md).
