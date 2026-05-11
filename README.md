<p align="center">
  <img src="docs/assets/toolmux-header.png" alt="Toolmux">
</p>

# Toolmux

Toolmux connects your terminal and local agents to the tools you already use,
with one command surface, local credential storage, and policy checks before
credentials are read.

Use Toolmux when you want to:

1. Work with SaaS tools from the command line.
2. Give coding agents controlled access to those same tools through MCP.
3. Import remote MCP servers and call their tools like normal CLI commands.
4. Keep provider tokens in your operating system credential store.
5. Use `--read-only` and local policy files to block writes before auth is
   loaded.

Toolmux is early software. Today it has an initial native Slack command set,
remote MCP imports, and agent setup for Codex, Claude Code, and Gemini CLI.

## Install

With Homebrew:

```bash
brew install fiam/tap/toolmux
toolmux version
```

Release archives for macOS, Linux, and Windows are available from
[GitHub Releases](https://github.com/fiam/toolmux/releases).

## Connect Services

Connect Slack:

```bash
toolmux connect slack
toolmux status slack
toolmux doctor slack
```

Disconnect and remove the local token:

```bash
toolmux disconnect slack --yes
```

Toolmux uses a hosted OAuth broker at `https://api.toolmux.com` by default for
provider flows that require confidential client secrets. Long-lived provider
tokens are handed back to your local CLI and stored in your OS credential
store.

To self-host the broker, point the CLI at your own `toolmuxd`:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com
toolmux connect slack
```

Self-hosting instructions are in [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).
Provider app setup notes are in [docs/providers/slack-app.md](docs/providers/slack-app.md).

## Slack

List visible conversations:

```bash
toolmux slack conversations ls
```

Send a message:

```bash
toolmux slack message send --channel C123456 --text "deploy is done"
```

Search messages when the Slack app was granted search access:

```bash
toolmux slack search --query "from:me deploy"
```

## Output For Humans And Scripts

Human output is the default. When stdout is a terminal, Toolmux can use tables,
colors, Markdown rendering, links, pagers, browser opens, and interactive
selectors.

Use structured output when another program is reading the result:

```bash
toolmux --output json slack search --query "deploy"
toolmux --output yaml status slack
```

JSON and YAML output are stable and undecorated: no ANSI escapes, prompts,
spinners, pagers, or browser side effects.

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

## Read-Only And Policy

Use `--read-only` to block commands with local or remote write effects before
provider credentials are read:

```bash
toolmux --read-only slack conversations ls
toolmux --read-only slack message send --channel C123456 --text "deploy"
```

The first command can run. The second is blocked.

For project-specific guardrails, create a local policy file:

```bash
toolmux policy init
toolmux policy catalog
toolmux policy check --command "slack conversations ls"
```

Policy discovery order:

1. `--policy <path>`
2. `TOOLMUX_POLICY=<path>`
3. `.toolmux/policy.yaml` in the current directory or a parent directory
4. No policy file means local usage is allowed by default

Policy files are local guardrails for projects and automation. They are not a
security boundary against a user who controls the machine or working
directory.

## Use Toolmux With Agents

Toolmux can expose provider actions and imported remote MCP tools over Model
Context Protocol stdio:

```bash
toolmux mcp serve
```

The MCP server uses the same action metadata as the CLI, so tool calls still
pass through local policy checks, `--read-only`, profiles, account selection,
and provider auth.

Configure supported local agent CLIs:

```bash
toolmux mcp configure
```

With no agent name, Toolmux autodetects supported installed CLIs. Interactive
runs show a checkbox selector, preselect agents where Toolmux MCP is already
enabled, and remove Toolmux from any configured agent you uncheck.

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

Limit which tools agents can see with MCP profiles:

```bash
toolmux mcp profile set slack-read \
  --tool 'slack.*' \
  --exclude-tool '*.send'

toolmux mcp profile default slack-read
toolmux mcp configure codex --mcp-profile slack-read --read-only
```

## Import Remote MCP Servers

Toolmux can import a remote Streamable HTTP MCP server, cache its tool
definitions, and expose those tools in two places:

1. Top-level CLI commands under the registered server name.
2. Proxied tools from `toolmux mcp serve`.

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

Use the Notion catalog entry for Notion work instead of a native Toolmux
integration:

```bash
toolmux mcp catalog --enable notion --global
toolmux mcp auth login notion
toolmux mcp sync notion
toolmux notion
```

Manage built-ins from the catalog:

```bash
toolmux mcp catalog
toolmux mcp catalog --enable cloudflare --global
toolmux mcp auth login cloudflare
toolmux mcp sync cloudflare
toolmux cloudflare
```

Register a custom endpoint:

```bash
toolmux mcp add linear-work https://mcp.linear.app/mcp --no-sync
toolmux mcp auth login linear-work
toolmux mcp sync linear-work
toolmux linear-work
```

The registered name becomes the command namespace. Registering `linear-work`
exposes CLI commands as `toolmux linear-work <tool-name>` and MCP tools as
`linear-work.<tool-name>`.

Rename or remove registered remotes:

```bash
toolmux mcp rename linear-work linear-prod
toolmux mcp remove linear-prod
```

Removing a remote also deletes stored auth for that server name in the active
Toolmux profile/account. If you already removed a server and want to clear a
stale token, use:

```bash
toolmux mcp auth remove <name>
```

Bearer-token auth is supported for servers that issue tokens outside a browser
OAuth flow:

```bash
printenv CLOUDFLARE_API_TOKEN | \
  toolmux mcp auth set cloudflare --bearer-token-stdin
```

Remote tool commands translate representable top-level JSON Schema properties
into flags. Use `--json` for nested objects or schemas that cannot be
expressed as flags. Use `toolmux schema <server> <tool>` or
`toolmux schema <server>.<tool>` to print the cached input schema.

## Token Custody

For native provider OAuth:

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

## Help

```bash
toolmux --help
toolmux <provider> --help
toolmux mcp --help
toolmux doctor
```

For developer setup, tests, architecture notes, and release workflow, see
[CONTRIBUTING.md](CONTRIBUTING.md).
