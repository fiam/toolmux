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

Toolmux is early software. Today it focuses on remote MCP imports, agent setup
for Codex, Claude Code, and Gemini CLI, and a native Slack command set for
internal workflows.

## Install

With Homebrew:

```bash
brew install --cask fiam/tap/toolmux
toolmux version
```

Release archives for macOS, Linux, and Windows are available from
[GitHub Releases](https://github.com/fiam/toolmux/releases).

## Add Toolboxes

Import a supported remote MCP server from the catalog:

```bash
toolmux mcp catalog
toolmux add grafana
toolmux mcp auth login grafana
toolmux mcp sync grafana
toolmux grafana
```

Try the public no-auth Iterate mock server:

```bash
toolmux add iterate
toolmux iterate mock_echo --message hello
```

Remote MCP server definitions and cached tool metadata are non-secret config.
OAuth tokens, bearer tokens, refresh tokens, dynamic client secrets, manually
supplied client secrets, and auth codes are stored only in the OS credential
store or transient process memory.

Toolmux uses a hosted OAuth broker at `https://api.toolmux.com` by default for
native provider flows that require confidential client secrets. To self-host
the broker, point the CLI at your own `toolmuxd`:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com
```

Self-hosting instructions are in [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).

## Native Slack

Slack is available as a native provider under `toolmux slack`. It supports
these auth models:

1. Browser session extraction through embedded `slackauth`.
2. User-supplied token plus an optional explicit cookie header.
3. A user-owned Slack OAuth app with a local loopback callback.
4. Brokered OAuth through `toolmuxd`.

Use browser session auth for a workspace:

```bash
toolmux add slack --workspace acme
```

`--workspace` is required for Slack browser-session auth. Use the Slack
workspace subdomain, for example `acme` from `https://acme.slack.com`.

By default, Toolmux asks `slackauth` to pick the best local engine. You can
select one explicitly:

```bash
toolmux add slack --workspace acme --from-browser webview
toolmux add slack --workspace acme --from-browser chrome
```

Store a user-supplied token and cookie:

```bash
toolmux add slack \
  --token-env SLACK_TOKEN \
  --cookie-env SLACK_COOKIE
```

Browser extraction runs only when explicitly requested through
`toolmux add slack --workspace` or `--from-browser`. The resulting token and
cookie use the same storage path as manually supplied credentials.
`toolmux add slack` validates Slack credentials with `auth.test` before storing
them, records the returned workspace URL, and uses that workspace-specific API
base for later Slack calls.

Authorize with your own Slack OAuth app:

```bash
toolmux add slack \
  --auth oauth \
  --client-id "$SLACK_CLIENT_ID" \
  --client-secret-env SLACK_CLIENT_SECRET \
  --scope channels:read,chat:write,im:write,search:read
```

Authorize through the Toolmux broker:

```bash
toolmux add slack --auth broker --scope channels:read,chat:write,im:write,search:read
```

Omit `--scope` to use Toolmux's Slack defaults, which cover channel and DM
history, opening DMs, posting, reactions, attachment reads, user search, user
groups, and Slack search.

The Slack broker facet in `toolmuxd` uses these environment variables:

```text
SLACK_CLIENT_ID
SLACK_CLIENT_SECRET
SLACK_AUTH_URL
SLACK_TOKEN_URL
SLACK_REVOKE_URL
SLACK_REDIRECT_URI
SLACK_SCOPES
```

Common Slack commands:

```bash
toolmux slack auth_test
toolmux slack channels_list --channel_types public_channel,private_channel
toolmux slack conversations_history --channel_id C123456 --oldest 1710000000.000000 --limit 50
toolmux slack conversations_search_messages --search_query "from:@alice roadmap"
toolmux slack conversations_open --user_id U123456
toolmux slack conversations_add_message --channel_id C123456 --text "Build is green" --dry-run
toolmux slack conversations_add_message --channel_id C123456 --text "Build is green"
toolmux status slack
toolmux remove slack
```

Native Slack command names use Slack MCP-style and Slack Web API method names:
`auth_test`, `conversations_history`, `conversations_replies`,
`conversations_add_message`, `conversations_open`, `reactions_add`, `reactions_remove`,
`attachment_get_data`, `conversations_search_messages`,
`conversations_unreads`, `conversations_mark`, `channels_list`,
`usergroups_list`, `usergroups_me`, `usergroups_create`,
`usergroups_update`, `usergroups_users_update`, and `users_search`.

## Workflows

Workflows are local YAML files that render an agent prompt and run it through a
configured local agent command. Global workflows live in
`~/.toolmux/workflows`; project workflows live in `.toolmux/workflows`.

List workflow templates and create the Slack recap workflow:

```bash
toolmux workflow templates
toolmux workflow init slack-recap --template slack-recap --project
```

Template names in `toolmux workflow templates` are fetched from this
repository on GitHub. You can also create workflows from
`github:owner/repo/path[@ref]` or a direct YAML URL:

```bash
toolmux workflow init team-recap --template github:acme/workflows/slack-recap.yaml@main
toolmux workflow init custom --template https://example.com/workflow.yaml
```

Workflow prompts are inline Go `text/template` strings. Inputs without defaults
are required:

```bash
toolmux workflow render slack-recap --input since="yesterday 18:00"
```

Run a workflow with an explicit agent:

```bash
toolmux workflow run slack-recap \
  --agent codex \
  --input since="yesterday 18:00"
```

If a workflow, `--agent`, or `workflows.default_agent` does not provide an
agent, `workflow run` opens an agent selector in an interactive terminal and
fails in non-interactive runs. Agent commands may include `{{ .prompt }}` in
the command or args; otherwise Toolmux appends the rendered prompt as the final
argument. The `--agent` value can include arguments, such as
`--agent "codex --yolo"`.

Set a default workflow agent:

```bash
toolmux workflow config set default-agent codex
```

In an interactive terminal, omit the agent to choose from detected local agents:

```bash
toolmux workflow config set default-agent
```

Workflows declare required toolboxes with compact requirements such as
`internal:slack`, `catalog:linear`, or a remote MCP URL. Missing required
toolboxes are added automatically during `workflow init` and `workflow run`
unless `--no-setup` is passed.

The built-in Slack recap template uses `internal:slack`. If no
`delivery_channel` input is set, the prompt tells the agent to DM the recap to
itself by opening a Slack IM conversation first, then sending to that DM channel
ID.

## Output For Humans And Scripts

Human output is the default. When stdout is a terminal, Toolmux can use tables,
colors, Markdown rendering, links, pagers, browser opens, progress spinners,
and interactive selectors. Long auth flows show status while Toolmux waits for
browser approval, callback receipt, token exchange, and credential validation.

Use structured output when another program is reading the result:

```bash
toolmux --output json mcp ls -R
toolmux --output yaml mcp catalog
```

JSON and YAML output are stable and undecorated: no ANSI escapes, prompts,
spinners, pagers, or browser side effects.

Common global flags:

```text
--output table|json|yaml
--color auto|always|never
--pager auto|always|never
--profile <name>
--policy <path>
--read-only
```

## Read-Only And Policy

Use `--read-only` to block commands with local or remote write effects before
provider credentials are read:

```bash
toolmux --read-only mcp ls -R
toolmux --read-only add https://example.com/mcp --name demo --no-sync
```

The first command can run. The second is blocked because it writes config.

For project-specific guardrails, create a local policy file:

```bash
toolmux policy init
toolmux policy catalog
toolmux policy check --command "mcp ls"
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

Toolmux can expose imported remote MCP tools over Model Context Protocol stdio:

```bash
toolmux mcp serve
```

The MCP server uses the same action metadata as the CLI, so tool calls still
pass through local policy checks, `--read-only`, profiles, and stored auth.

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
toolmux mcp profile set readonly \
  --tool 'grafana.*' \
  --exclude-tool '*.send'

toolmux mcp profile default readonly
toolmux mcp configure codex --mcp-profile readonly --read-only
```

## Import Remote MCP Servers

Toolmux can import a remote Streamable HTTP MCP server, cache its tool
definitions, and expose those tools in two places:

1. Top-level CLI commands under the registered server name.
2. Proxied tools from `toolmux mcp serve`.

Try the public no-auth Iterate mock server:

```bash
toolmux add iterate
toolmux iterate mock_echo --message hello
toolmux iterate mock_calculate --operation add --a 2 --b 3
toolmux mcp schema iterate mock_calculate
```

Built-in remote MCP catalog names:

```text
atlassian
cloudflare
grafana
iterate
linear
miro
notion
```

Use the Notion catalog entry for Notion work instead of a native Toolmux
integration:

```bash
toolmux add notion
toolmux mcp auth login notion
toolmux mcp sync notion
toolmux notion
```

Manage built-ins from the catalog:

```bash
toolmux mcp catalog
toolmux add cloudflare
toolmux mcp auth login cloudflare
toolmux mcp sync cloudflare
toolmux cloudflare
```

Grafana Cloud uses hosted OAuth. The browser flow may ask for your Grafana
Cloud stack URL before consent:

```bash
toolmux add grafana
toolmux mcp auth login grafana
toolmux mcp sync grafana
toolmux grafana
```

Register a custom endpoint:

```bash
toolmux add https://mcp.linear.app/mcp --name linear-work --no-sync
toolmux mcp auth login linear-work
toolmux mcp sync linear-work
toolmux linear-work
```

The registered name becomes the command namespace. When `--name` is omitted for
an MCP URL, Toolmux derives a default name from the URL host, such as `linear`
for `https://mcp.linear.app/mcp`. MCP config stores the resolved URL, not the
catalog shorthand. Registering `linear-work` exposes CLI commands as
`toolmux linear-work <tool-name>` and MCP tools as `linear-work.<tool-name>`.

Show registered toolboxes and their auth state:

```bash
toolmux status
toolmux status linear-work
```

For repeated non-secret tool arguments, configure defaults on the registered
remote. Defaults apply only to tools whose input schema has that argument, and
explicit `--json` values or flags override them. The Atlassian catalog entry
will suggest setting `cloudId` when it is missing:

```bash
toolmux mcp defaults set atlassian cloudId <cloud-id>
toolmux mcp defaults ls atlassian
toolmux atlassian <tool-name>
```

Remote MCP config writes default to the global Toolmux config. Add `--project`
when you intentionally want a project-local server, profile, or default
argument.

Global Toolmux config is `~/.toolmux/config.yaml`. Project config remains
`.toolmux/config.yaml` in the current project tree.

In an interactive terminal, remote MCP command help and tool listings keep
upstream descriptions compact and lightly styled so the command list stays
scannable. Use full descriptions when you need the original upstream text:

```bash
toolmux linear-work --full-help
toolmux mcp ls linear-work --full-descriptions
toolmux mcp ls -R --full-descriptions
```

Use `-v`/`--verbose` on `toolmux add`, `toolmux mcp sync`, or a remote tool
command to print redacted Streamable HTTP requests and responses for debugging.

Non-interactive output and JSON/YAML output keep the full cached metadata for
agents and scripts.

Rename or remove registered remotes:

```bash
toolmux mcp rename linear-work linear-prod
toolmux remove linear-prod
```

Removing a remote also deletes stored auth for that server name in the active
Toolmux profile. If you already removed a server and want to clear a
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
expressed as flags. Use `toolmux mcp schema <server> <tool>` or
`toolmux mcp schema <server>.<tool>` to print the cached input schema.

## Token Custody

For native provider OAuth integrations:

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
toolmux <remote> --help
toolmux mcp --help
toolmux doctor
```

For developer setup, tests, architecture notes, and release workflow, see
[CONTRIBUTING.md](CONTRIBUTING.md).
