# Toolmux

Toolmux is a local-first CLI for working with SaaS tools from one
policy-aware command surface.

It is built for both people and agents:

1. Humans get readable tables, colors, markdown rendering, pagers, browser
   opens, and interactive selectors when running in a terminal.
2. Agents and scripts get stable `json` and `yaml` output with no prompts,
   spinners, pagers, browser opens, or ANSI escapes.

Toolmux stores provider tokens in your operating system credential store by
default. The hosted or self-hosted `toolmuxd` server brokers OAuth for
providers that require confidential client secrets, but it does not provide a
cloud token vault in the initial design.

## Status

Toolmux is early software. The first usable provider is Notion.

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
brew install toolmux
toolmux version
```

Or build from source:

```bash
git clone https://github.com/fiam/toolmux.git
cd toolmux
make dev-cli
toolmux version
```

Released archives include `toolmux` and `toolmuxd` binaries for macOS, Linux,
and Windows on amd64 and arm64. Use Go 1.26.3 or newer on the Go 1.26 line
when building from source. Docker is only required for the full linter pass and
container image builds.

## Connect Notion

The CLI defaults to hosted `https://api.toolmux.com` for brokered OAuth.

```bash
toolmux connect notion
toolmux status notion
toolmux doctor notion
```

For a local or self-hosted `toolmuxd`, point the CLI at your server:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com
toolmux connect notion
```

To print the OAuth URL without waiting for completion:

```bash
toolmux connect notion --auth-url-only
```

To disconnect and remove the local token:

```bash
toolmux disconnect notion --yes
```

Self-hosting instructions are in [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).
Notion app setup notes are in
[docs/providers/notion-app.md](docs/providers/notion-app.md).

## Notion Examples

Search:

```bash
toolmux notion search roadmap
toolmux notion search --query tasks --type data_source
toolmux notion search --limit 10 --sort edited --direction desc
```

Read pages:

```bash
toolmux notion page read "Roadmap"
toolmux notion page read --follow "Roadmap"
toolmux notion page markdown "Roadmap"
toolmux notion page links "Roadmap"
toolmux notion page open "Roadmap"
```

Navigate page structure:

```bash
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

## Output Modes

Human output is the default:

```bash
toolmux notion page read "Roadmap"
```

Use structured output for agents and scripts:

```bash
toolmux --output json notion page links "Roadmap"
toolmux --output yaml status notion
```

Global output controls:

```bash
--output table|json|yaml
--color auto|always|never
--pager auto|always|never
--read-only
```

Interactive features are disabled automatically when Toolmux is not attached
to a terminal.

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

The catalog lists implemented provider actions and their remote/local effects.
Use `--read-only` to block actions with remote or local write effects.

Check a command:

```bash
toolmux policy check --command "notion page read Roadmap"
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

This keeps long-lived provider tokens on your machine instead of in Toolmux's
hosted service.

## Self-Hosting

You can run your own `toolmuxd` with your own provider OAuth apps and secrets.
See [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md).

The public repository contains portable source, generic container builds, fake
upstream tests, and self-hosting docs. Toolmux's hosted deployment
infrastructure and provider secrets are intentionally outside this repository.

## Development

Contributor setup and project conventions are in
[CONTRIBUTING.md](CONTRIBUTING.md).
