# Toolmux

Toolmux is a local-first Swiss-army knife for the CLI. It connects services
you use every day, exposes them through one consistent command surface, and
lets the same tools work for both humans and AI agents.

Use it to:

1. Connect SaaS services once, then operate them from the terminal.
2. Give agents a controlled MCP tool server backed by the same commands.
3. Import remote MCP servers and call their tools as normal CLI commands.
4. Keep provider tokens in your operating system credential store.
5. Use local policy and `--read-only` checks before credentials are read.

Toolmux has one command model with two presentation modes. Humans get readable
tables, color, Markdown rendering, pagers, browser opens, and interactive
selectors. Agents and scripts get stable JSON or YAML with no prompts,
spinners, ANSI escapes, pagers, or browser side effects.

## Current Status

Toolmux is early software. The native provider surface includes Notion and an
initial Slack command set, and Toolmux can also bridge imported remote MCP
servers into the CLI and into agent MCP sessions.

Active:

1. Notion native provider: OAuth connect, search, page reads/writes, links,
   page tree, databases, and data sources.
2. Slack native provider: OAuth connect, conversation listing, message send,
   and message search.
3. Remote MCP imports: register Streamable HTTP MCP servers, cache tools,
   authenticate with OAuth or bearer tokens, and call tools from CLI or agents.
4. Agent setup: configure Codex, Claude Code, and Gemini CLI to launch
   `toolmux mcp serve`.

Planned native providers:

1. Linear
2. Jira
3. Google Docs
4. Google Drive
5. Gmail

Until those native providers are implemented, use remote MCP servers where
available, such as Atlassian MCP for Jira-related workflows.

## Install

With Homebrew:

```bash
brew tap fiam/tap
brew install toolmux
toolmux version
```

Or build from source:

```bash
git clone https://github.com/fiam/toolmux.git
cd toolmux
make dev-cli
./bin/toolmux version
```

Released archives include the `toolmux` CLI for macOS, Linux, and Windows on
amd64 and arm64. The `toolmuxd` server daemon is released only as a Linux
amd64/arm64 container image at `ghcr.io/fiam/toolmuxd:<tag>`.

When building from source, use Go 1.26.3 or newer on the Go 1.26 line. Docker
is only required for the full linter pass and container image builds.

## First Run

Start by checking the CLI:

```bash
toolmux --help
toolmux version
```

Connect Notion:

```bash
toolmux connect notion
toolmux status notion
toolmux doctor notion
```

Connect Slack:

```bash
toolmux connect slack
toolmux status slack
toolmux doctor slack
```

The default OAuth broker is `https://api.toolmux.com`. The broker helps with
provider flows that require confidential client secrets, but long-lived
provider tokens are stored locally in your OS credential store.

For a local or self-hosted `toolmuxd`, point the CLI at your server:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com
toolmux connect notion
```

If you want to open the browser yourself:

```bash
toolmux connect notion --auth-url-only
```

Disconnect and remove the local token:

```bash
toolmux disconnect notion --yes
```

Self-hosting instructions are in [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).
Provider app setup notes are in [docs/providers/notion-app.md](docs/providers/notion-app.md)
and [docs/providers/slack-app.md](docs/providers/slack-app.md).

## Use It As A Human

Human output is the default:

```bash
toolmux notion search roadmap
toolmux notion page read "Roadmap"
toolmux notion page tree "Roadmap" --depth 3
toolmux slack conversations ls
toolmux slack message send --channel C123456 --text "deploy is done"
toolmux slack search --query "from:me deploy"
```

Toolmux uses terminal-friendly presentation when stdout is a TTY: tables,
colors, Markdown rendering, pagers, links, and interactive selection where a
command needs follow-up input.

Global output controls:

```bash
toolmux --color auto --pager auto notion page read "Roadmap"
toolmux --read-only notion page read "Roadmap"
```

Common global flags:

```text
--output table|json|yaml
--color auto|always|never
--pager auto|always|never
--profile <name>
--account <id-or-alias>
--policy <path>
--read-only
```

## Use It From Agents And Scripts

Use structured output whenever another program is reading the result:

```bash
toolmux --output json notion page links "Roadmap"
toolmux --output yaml status notion
```

Structured output is undecorated and stable: no ANSI escapes, prompts, pagers,
spinners, or browser opens. Interactive features are disabled automatically
when Toolmux is not attached to a terminal.

Use `--read-only` to deny commands with local or remote write effects:

```bash
toolmux --read-only --output json notion page read "Roadmap"
toolmux --read-only notion page content replace "Roadmap" --markdown "# New"
```

The first command can run. The second is blocked before provider credentials
are read.

## Use It As An MCP Server

Toolmux can expose implemented provider actions and imported remote MCP tools
over Model Context Protocol stdio:

```bash
toolmux mcp serve
```

The MCP server is generated from the same action metadata as the CLI, so tool
calls still pass through local policy checks, `--read-only`, profiles, account
selection, and provider auth.

When an agent lets you provide a command manually, use:

```bash
toolmux mcp serve \
  --mcp-profile notion-read \
  --tool 'notion.*' \
  --exclude-tool '*.delete'
```

`mcp serve` accepts `--mcp-profile`, `--tool`, `--tool-regex`,
`--exclude-tool`, and `--exclude-tool-regex`.

## Configure Local Agents

Toolmux can configure supported agent CLIs to launch `toolmux mcp serve`:

```bash
toolmux mcp configure
```

With no agent name, Toolmux autodetects supported installed CLIs. Interactive
runs show a checkbox selector, preselect agents where Toolmux MCP is already
enabled, and remove the Toolmux MCP server from any configured agent you
uncheck.

Supported agent targets:

| Agent | Names | Scope support |
| --- | --- | --- |
| Codex | `codex` | Codex default MCP config |
| Claude Code | `claude`, `claude-code` | `local`, `user`, `project` |
| Gemini CLI | `gemini`, `gemini-cli` | `user`, `project` |

Configure specific agents:

```bash
toolmux mcp configure codex claude gemini
```

For scripts, use explicit enable and disable commands:

```bash
toolmux mcp enable codex claude
toolmux mcp disable gemini
```

Examples:

```bash
toolmux mcp enable codex \
  --command /opt/toolmux/bin/toolmux \
  --mcp-profile notion-read \
  --read-only \
  --dry-run

toolmux mcp enable claude gemini \
  --scope project \
  --tool 'notion.page.*' \
  --exclude-tool '*.delete'

toolmux mcp disable claude gemini --mcp-profile notion-read
```

`mcp configure` and `mcp enable` can write relevant global Toolmux flags into
the launched MCP command, including `--profile`, `--account`, `--policy`, and
`--read-only`.

## Import Remote MCP Servers

Toolmux can import a remote MCP server, cache its tool definitions, and expose
those tools in two places:

1. As top-level CLI commands under the registered server name.
2. As proxied tools from `toolmux mcp serve`.

Try the public no-auth Iterate mock server:

```bash
toolmux mcp add iterate
toolmux iterate mock_echo --message hello
toolmux iterate mock_calculate --operation add --a 2 --b 3
toolmux schema iterate mock_calculate
```

Built-in remote MCP catalog names:

```text
atlassian
cloudflare
iterate
linear
miro
notion
```

`atlassian` uses Atlassian's OAuth-capable
`https://mcp.atlassian.com/v1/mcp/authv2` endpoint. `iterate` points at
`https://mock.iterate.com/no-auth` and is useful for smoke tests.

Register a custom Streamable HTTP MCP endpoint:

```bash
toolmux mcp add linear2 https://mcp.linear.app/mcp --no-sync
toolmux mcp auth login linear2
toolmux mcp sync linear2
toolmux mcp ls
toolmux mcp ls linear2
toolmux mcp ls -R
toolmux mcp show linear2
```

Manage built-ins from the catalog:

```bash
toolmux mcp catalog
toolmux mcp catalog --enable iterate --global --sync
toolmux mcp catalog --enable notion=notion-mcp --global
toolmux mcp catalog --manage
```

Rename or remove registered remotes:

```bash
toolmux mcp rename linear2 linear-work
toolmux mcp remove linear-work miro
```

Removing a remote also deletes stored auth for that server name in the active
Toolmux profile/account.

The registered name becomes the command namespace. Registering `linear2`
exposes CLI commands as `toolmux linear2 <tool-name>` and MCP tools as
`linear2.<tool-name>`.

Toolmux rejects imported remote names that collide with native commands. If a
future Toolmux version adds a native command that collides with an imported
remote MCP server, startup fails with a rename command such as:

```bash
toolmux mcp rename linear <new-name>
```

`mcp add` syncs tools immediately by default. If the first sync returns an
auth-required response and no auth is stored for that server name, Toolmux
starts MCP OAuth, stores auth, retries sync, and writes the server config only
after auth and sync succeed. If login is cancelled or fails, no server entry is
written.

Use `--no-sync` when you want to register first and authenticate later with
`toolmux mcp auth login <name>` or `toolmux mcp auth set <name>`.

Remote tool commands translate representable top-level JSON Schema properties
into flags. Use `--json` for nested objects or schemas that cannot be expressed
as flags. Use `toolmux schema <server> <tool>` or
`toolmux schema <server>.<tool>` to print the cached input schema. Remote tool
commands also accept `-v`/`--verbose` for raw MCP HTTP tracing on stderr with
authorization headers redacted.

## Authenticate Remote MCP

OAuth auth uses MCP protected-resource metadata discovery,
authorization-server metadata, PKCE, the OAuth `resource` parameter, and
dynamic client registration when the server advertises it:

```bash
toolmux mcp auth login cloudflare
toolmux mcp auth status cloudflare
toolmux mcp auth remove cloudflare
```

`toolmux mcp auth remove <name>` can also clean up stale auth after the remote
server entry has already been removed.

If dynamic client registration is not available, provide a client from the MCP
server operator:

```bash
toolmux mcp auth login myserver \
  --client-id "$MCP_CLIENT_ID" \
  --scope tools.read
```

Bearer-token auth is supported for servers that issue tokens outside a browser
OAuth flow:

```bash
printenv CLOUDFLARE_API_TOKEN | \
  toolmux mcp auth set cloudflare --bearer-token-stdin
```

Stored auth is applied to `sync`, CLI remote tool calls, and proxied
`mcp serve` tool calls after policy checks.

## Notion Workflows

Search:

```bash
toolmux notion search roadmap
toolmux notion search --query tasks --type data_source
toolmux notion search --limit 10 --sort edited --direction desc
```

Read and navigate pages:

```bash
toolmux notion page read "Roadmap"
toolmux notion page read --follow "Roadmap"
toolmux notion page markdown "Roadmap"
toolmux notion page links "Roadmap"
toolmux notion page open "Roadmap"
toolmux notion page children "Roadmap"
toolmux notion page tree "Roadmap" --depth 3
```

Create and update pages:

```bash
toolmux notion page create \
  --parent-type workspace \
  --title "Meeting Notes" \
  --markdown "# Meeting Notes"

toolmux notion page update "Meeting Notes" --title "Team Notes"
toolmux notion page content insert "Team Notes" --markdown "## Followups"
toolmux notion page content replace "Team Notes" \
  --markdown "# Replacement" \
  --yes
```

Inspect page export fidelity before automated edits:

```bash
toolmux notion page doctor "Roadmap"
```

Work with data sources:

```bash
toolmux notion data-source query <data-source-id>
toolmux notion data-source schema <data-source-id>
toolmux notion data-source row create <data-source-id> \
  --title "New Row"
toolmux notion data-source row update <page-id> \
  --title "Updated Row"
```

## MCP Profiles

Use MCP profiles to expose only selected tools to agents. Profiles live in the
general Toolmux config under the `mcp` key.

Global config uses `$XDG_CONFIG_HOME/toolmux/config.yaml` or the platform user
config directory. Project config uses `.toolmux/config.yaml`. Project config
overrides global config for matching profile names and default profile
selection, similar to Git config layering.

Example config:

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

Create and use a profile:

```bash
toolmux mcp profile set notion-read \
  --tool 'notion.*' \
  --exclude-tool '*.create' \
  --exclude-tool '*.update' \
  --exclude-tool '*.delete'

toolmux mcp profile default notion-read
toolmux mcp configure codex --mcp-profile notion-read
```

Profile commands:

```bash
toolmux mcp profile set <name>
toolmux mcp profile default <name>
toolmux mcp profile ls
toolmux mcp profile show <name>
```

Use `--global` to write global Toolmux config. Without `--global`, profile
commands write project config. Use `--project` when you want to make the
project scope explicit.

Filters support shell-style globs through `--tool` and `--exclude-tool`, and
regular expressions through `--tool-regex` and `--exclude-tool-regex`.

## Local Policy

Toolmux can enforce local command policy before it reads provider credentials
or calls provider APIs.

Create a starter policy:

```bash
toolmux policy init
```

Inspect available policy-aware commands:

```bash
toolmux policy catalog
```

The catalog lists implemented provider actions, Toolmux MCP management
commands, cached imported MCP tools, and their remote/local effects.

Check a command:

```bash
toolmux policy check --command "notion page read Roadmap"
toolmux policy check --command "iterate mock_echo"
```

Policy discovery order:

1. `--policy <path>`
2. `TOOLMUX_POLICY=<path>`
3. `.toolmux/policy.yaml` in the current directory or a parent directory
4. No policy file means local usage is allowed by default

Policy files are local guardrails for projects and automation. They are not a
security boundary against a user who controls the machine or working directory.

## Token Custody

Toolmux uses local token custody:

1. `toolmuxd` starts a browser OAuth flow.
2. The provider redirects back to `toolmuxd`.
3. `toolmuxd` exchanges the code when a client secret is required.
4. The CLI retrieves the token bundle once over HTTPS.
5. The CLI stores provider tokens in the OS credential store.
6. `toolmuxd` keeps only short-lived handoff data in process memory.

Remote MCP server definitions and cached tool metadata are non-secret config.
Bearer tokens, OAuth tokens, refresh tokens, dynamic client secrets, manually
supplied client secrets, and auth codes are stored only in the OS credential
store or transient process memory.

## Self-Hosting

You can run your own `toolmuxd` with your own provider OAuth apps and secrets.
See [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).

Self-hosted `toolmuxd` exposes unauthenticated operational endpoints for
deployment checks:

```text
GET /healthz
GET /build
```

These responses must not include secrets, provider configuration, tokens, or
deployment-specific infrastructure details.

The public repository contains portable source, generic container builds, fake
upstream tests, and self-hosting docs. Toolmux's hosted deployment
infrastructure and provider secrets are intentionally outside this repository.

## Development

Contributor setup and project conventions are in
[CONTRIBUTING.md](CONTRIBUTING.md).
