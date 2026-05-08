# Supacli

Supacli is a local-first CLI for working with SaaS tools from one
policy-aware command surface.

It is built for both people and agents:

1. Humans get readable tables, colors, markdown rendering, pagers, browser
   opens, and interactive selectors when running in a terminal.
2. Agents and scripts get stable `json` and `yaml` output with no prompts,
   spinners, pagers, browser opens, or ANSI escapes.

Supacli stores provider tokens in your operating system credential store by
default. The hosted or self-hosted `supaclid` server brokers OAuth for
providers that require confidential client secrets, but it does not provide a
cloud token vault in the initial design.

## Status

Supacli is early software. The first usable provider is Notion.

| Provider | Status | Notes |
| --- | --- | --- |
| Notion | Active | OAuth connect, search, page reads/writes, links, page tree, and data sources. |
| Linear | Planned | Provider metadata and early integration work exist. |
| Jira | Planned | Command catalog exists. |
| Slack | Planned | Command catalog exists. |
| Google Docs | Planned | Command catalog exists. |
| Google Drive | Planned | Command catalog exists. |
| Gmail | Planned | Command catalog exists. |

## Install

For now, build from source:

```bash
git clone https://github.com/fiam/supacli.git
cd supacli
make dev-cli
supacli version
```

Use Go 1.26.3 or newer on the Go 1.26 line. Docker is only required for the
full linter pass and container image builds.

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
```

Interactive features are disabled automatically when Supacli is not attached
to a terminal.

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
