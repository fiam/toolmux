# Toolmux MCP-First Provider PRD

Last updated: 2026-05-24

## Summary

Toolmux is an open-source CLI that lets users connect and operate common SaaS
services from one command surface. The provider strategy is MCP-first: import
remote MCP servers when a provider already offers a usable MCP surface, and
build native integrations only for providers or workflows without an adequate
MCP path. The current product path is remote MCP catalog first; native
providers are deferred until a provider-specific workflow clearly needs one.

The first release optimizes for a simple connection experience without asking
users to create personal API keys or provider developer apps. Provider tokens
are stored locally by default; Toolmux does not provide cloud token storage in
the initial release. Google is exposed through one native Drive-focused
`google` toolbox, backed by brokered OAuth through `toolmuxd`, one local
credential bundle, and the non-sensitive `drive.file` scope unless a product
requirement explicitly justifies broader Google data access. Existing Drive
files are selected through the brokered Google Picker flow, with a normal
`google drive pick` action for one-off selection and `google drive selected ...`
actions for local selected-file cache management. Accessible files can be
copied into My Drive through `google drive files copy`.

## Goals

1. Let users connect supported native providers with a browser-based OAuth flow
   when native integration is justified.
2. Store long-lived provider credentials locally, protected by the user's operating system.
3. Provide a consistent command model across providers for auth, listing, reading, creating, and updating common resources.
4. Keep the hosted Toolmux server daemon, `toolmuxd`, open-source and minimized: its provider connection component may exchange/refresh tokens when provider client secrets are required, but it must not persist provider tokens.
5. Make provider capability and scope limits explicit so users understand why some actions require reauthorization or are deferred.
6. Keep Toolmux's production deployment infrastructure and provider secrets out of the OSS repo while publishing portable source and artifacts for CLI and server users.
7. Make imported remote MCP servers feel like first-class CLI namespaces with
   policy, `--read-only`, auth, cached schemas, and agent exposure.

## Non-Goals

1. No Toolmux-hosted token vault in the initial release.
2. No team-shared connections.
3. No scheduled cloud workflows or background jobs.
4. No native workspace bot automation by default.
5. No attempt to bypass provider OAuth policies or scrape browser/session
   tokens, except explicit Slack browser-session setup initiated by
   `toolmux add slack`.
6. No AWS Lambda, DNS, certificate, production secret, or hosted deployment infrastructure in the OSS repo.
7. No native Notion OAuth integration while Notion has a usable remote MCP
   path; do not ask users or hosted Toolmux operators to register a Notion
   public connection for native commands.
10. No browser credential harvesting, cookie extraction, or session-token
    scraping as an auth shortcut for MCP or native integrations, except the
    explicit Slack browser-session setup flow.

## Users

Primary users:

1. Developers who want to inspect and update SaaS resources from scripts and terminals.
2. Operators and support engineers who need fast cross-service lookups.
3. Technical founders and small teams who want a local-first automation tool before adopting team/cloud features.

Secondary users:

1. Security-minded users who prefer open-source auth infrastructure and local token custody.
2. Contributors who want to add providers through a stable integration interface.

## Repository and Deployment Model

The OSS repository contains:

1. `toolmux` CLI source.
2. `toolmuxd` server daemon source.
3. Generic self-hosting documentation.
4. Generic `toolmuxd` container build files.
5. Release automation for CLI binaries, Homebrew tap artifacts, and generic Linux server images.

The OSS repository must not contain:

1. Toolmux production AWS Lambda, API Gateway, ECR, DNS, certificate, or monitoring definitions.
2. Terraform, Pulumi, CDK, or deployment state for Toolmux's hosted infrastructure.
3. Provider OAuth client secrets.
4. Production abuse controls, billing internals, allowlists, or alerting destinations.

Toolmux's hosted deployment should live in a private infrastructure repo. That private repo may deploy `toolmuxd` to AWS Lambda, Lambda Function URLs, API Gateway, or another AWS entrypoint by consuming public release artifacts from this repo.

Self-hosters can run the OSS `toolmuxd`, but they must create their own provider OAuth apps and supply their own provider client ids and secrets.

## Product Principles

1. Local-first custody: provider refresh tokens stay on the user's machine unless the user later opts into a cloud vault.
2. Least privilege: each command requests only the scopes needed for that provider's MVP actions.
3. Explicit escalation: if a command requires missing scopes, Toolmux explains the added scopes and starts reauthorization.
4. Portable output: every read/list command supports `--output table`, `--output json`, and `--output yaml`.
5. Scriptable defaults: commands fail with clear nonzero exits and structured errors in machine-readable output modes.
6. No token leakage: tokens, auth codes, refresh tokens, and `Authorization` headers are never printed or logged.
7. Policy before execution: every command and subcommand exposes authorization metadata and is checked against local policy before credential access or provider API calls.
8. Agent-first, human-friendly: structured command contracts remain the source of truth, with TTY-aware affordances layered on top for people.

## Command Policy and RBAC

Toolmux should support local policy files so users and teams can restrict what the CLI is allowed to do from a given working directory. This is a local guardrail for developer workflows, automation, and shared repos. It is not a hard security boundary against a local user who can edit the policy file or run an older binary.

Policy discovery order:

1. Explicit `--policy <path>`.
2. `TOOLMUX_POLICY=<path>`.
3. `.toolmux/policy.yaml` in the current directory or parent directories.
4. No policy file means local interactive usage is allowed by default.

If multiple discovered policies apply, denies win over allows. A child-directory policy may add stricter rules, but it must not weaken a parent-directory deny.

Policy commands:

```bash
toolmux policy init
toolmux policy catalog
toolmux policy check --command "mcp ls"
toolmux policy explain --command "linear issue create --title Draft"
toolmux policy doctor
```

Each command must declare metadata that the policy engine can evaluate:

```text
command: linear.issue.create
provider: linear
resource: issue
action: create
remote_effect: write
local_effect: none
risk: issue-create
account: <resolved account id>
profile: <toolmux profile>
scopes: [issues:create]
args: provider-specific normalized arguments
```

Initial policy file format:

```yaml
version: 1
default: deny

roles:
  reader:
    allow:
      - provider: "*"
        remote_effects: ["read", "none"]
        local_effects: ["none"]
  operator:
    extends: ["reader"]
    allow:
      - provider: "linear"
        resources: ["issue"]
        actions: ["create"]
    deny:
      - risks: ["destructive"]

bindings:
  - role: operator
    profiles: ["default"]
    accounts: ["*@company.com"]
```

Policy evaluation requirements:

1. Evaluate policy before loading provider tokens.
2. Deny by default when a policy file sets `default: deny`.
3. Support provider, resource, action, command, remote effect, local effect,
   profile, account, risk, and normalized argument matching.
4. Return a clear denial reason and the policy rule that caused it.
5. Support `--policy` in all commands, including provider commands and auth commands.
6. Support machine-readable denial errors in JSON/YAML output.
7. Include a generated provider action catalog so users can write policies
   without reading source code. Provider command paths, argument constraints,
   flags, help, aliases, CLI commands, MCP tools, and REST routes should derive
   from one provider-owned action tree. Group nodes and leaf actions use the
   same metadata type, and upper layers walk that tree instead of maintaining
   separate group and command models. Root `add`, `remove`, `rm`, `status`,
   and `doctor` remain explicit CLI commands instead of generated provider
   subcommands.
8. Keep provider command execution provider-owned. Providers expose action
   handlers and return structured results; CLI, future MCP, and future REST
   adapters invoke those handlers and render or serialize the same result types
   instead of carrying provider-specific command logic.
9. Providers self-register through facet packages. A provider's
   `client` package owns CLI/API/MCP metadata, action handlers, API client,
   tests, and fake upstream integration tests. A provider's `broker` package
   owns server-side OAuth/token broker behavior for `toolmuxd` and registers
   through `internal/providers/brokers`. Binaries import the appropriate
   provider bundle for side effects instead of maintaining a hardcoded provider
   list in the registry or loading client provider code into the server.
10. Treat policy files as configuration, not secrets.
11. Avoid remote policy enforcement in the initial release; signed/team-managed policies can be added later.

## Agent and Human UX

Toolmux is primarily an agent-friendly CLI, but it should also feel fast, clear, and pleasant for humans. The product should expose one deterministic command model with two presentation modes:

```bash
toolmux ... --output json     # agent/script contract
toolmux ...                   # human default
```

The structured interface is the source of truth. Human-friendly behavior must not make agent output unstable or ambiguous.

Agent UX requirements:

1. JSON/YAML output must be deterministic, schema-friendly, and quiet.
2. Machine-readable errors must include provider, command id, error code, HTTP status when available, retry hint, and policy rule when relevant.
3. Commands must not prompt, open browsers, page output, or render spinners when stdin/stdout are non-interactive unless explicitly requested.
4. Every command must support nonzero exit codes that distinguish usage errors, policy denial, auth failures, provider failures, and partial success where relevant.

Human UX requirements:

1. Default output should use concise tables, readable summaries, color when supported, hyperlinks when supported, and clear empty states.
2. Interactive prompts, selectors, confirmations, progress indicators, and browser opens are allowed only when attached to a TTY.
3. Risky commands should support `--dry-run`, `--preview`, or `--confirm` patterns before mutation.
4. Error messages should explain what failed, why it likely failed, the policy/provider detail behind it, and the exact command to retry or inspect.
5. Common workflows may have ergonomic shortcuts that map to canonical commands.
6. Shell completion should cover static commands and dynamic provider values
   such as profiles, Slack channels, Linear teams, and remote MCP server
   names/tool names.
7. Users should be able to define aliases for provider-specific ids so command lines remain readable.
8. Commands that return web resources should support `--open` to launch the provider URL in a browser.

Terminal presentation contract:

1. Toolmux should not ship a separate full-screen TUI for provider workflows in
   the MVP. The normal command surface must serve both humans and agents.
2. Every current and future provider command must use shared output renderers
   instead of provider-specific ANSI strings, paging, prompts, or table
   formatting.
3. Global output controls must include `--output table|json|yaml`,
   `--color auto|always|never`, `--pager auto|always|never`, `--no-pager`,
   `--no-input`, and `--quiet`.
4. Color behavior must honor `NO_COLOR`, `CLICOLOR=0`, `CLICOLOR_FORCE=1`,
   and `TERM=dumb` when `--color auto` is active.
5. Pager behavior must honor `$PAGER`, fall back to direct output when no pager
   is available, preserve ANSI color when paging human output, and never page
   JSON/YAML unless explicitly requested.
6. Human table output may use color, semantic badges, hyperlinks, aligned
   columns, markdown rendering, and compact summaries when stdout is a TTY.
7. Agent output must be undecorated: no ANSI escape sequences, no hyperlinks
   beyond literal field values, no spinners, no prompts, no browser opens, and
   no pager.
8. Non-interactive stdin/stdout must disable prompts, browser opens, paging,
   progress animation, and implicit color unless the user explicitly overrides
   the behavior.
9. Human errors should include a concise summary, likely cause, provider or
   policy detail, retry guidance, and the equivalent inspect command when one
   exists.
10. Machine-readable errors must keep a stable schema for automation and policy
   reporting.

Human-oriented examples:

```bash
toolmux slack channels_list
toolmux slack conversations_search_messages --search_query "from:@alice roadmap"
toolmux add notion
toolmux notion
toolmux slack conversations_add_message --channel_id C123456 --text "Build is green" --dry-run
```

Discovery commands:

```bash
toolmux policy catalog
toolmux mcp schema <server.tool>
```

Optional later UX:

```bash
toolmux browse
toolmux linear issues
toolmux status
```

These optional flows are not required for MVP. If added later, they should remain
part of the same command surface and output contract rather than becoming a
separate application.

## Toolbox UX

Baseline commands:

```bash
toolmux add notion

toolmux status
toolmux status notion
toolmux doctor
toolmux remove notion
```

Remote MCP add and auth success output should show:

1. Toolbox name.
2. Backend kind and source URL.
3. Auth mode and granted scopes when known.
4. Local storage mode.
5. Suggested first command.

Toolbox metadata may be stored in config files, but token material must be
stored directly in the user's OS credential store.

## Auth Model

Toolmux supports two auth classes in the initial release.

### Native Local OAuth

Used when the provider supports public/native OAuth with PKCE and a loopback redirect.

Flow:

```text
CLI generates state + PKCE verifier
CLI opens the browser
Provider redirects to 127.0.0.1 callback
CLI exchanges code for tokens
CLI stores tokens locally
```

Initial providers:

1. Slack native commands for internal workflows.
2. Remote MCP catalog entries for providers with adequate MCP servers.

### toolmuxd Local-Custody OAuth

Used when the provider requires a confidential client secret for token exchange or refresh.

Flow:

```text
CLI generates one-time handoff id and session secret
CLI opens api.toolmux.com
Provider redirects to toolmuxd
toolmuxd exchanges code using provider client secret
toolmuxd stores token bundle in short-lived in-memory handoff
CLI retrieves bundle once over HTTPS and stores it locally
toolmuxd deletes the handoff material
```

Initial native provider support:

1. Slack for internal workflows.

toolmuxd must not store provider access tokens or refresh tokens in durable storage.

## Local Credential Storage

Toolmux stores non-secret metadata separately from secrets.

```text
~/.config/toolmux/config.yaml        # profiles, selected account ids, display names
OS credential store                  # OAuth token bundles and refresh metadata
```

OS credential store targets:

1. macOS Keychain.
2. Windows Credential Manager / DPAPI-backed credential APIs.
3. Linux Secret Service via GNOME Keyring or KWallet.

Toolmux stores one OS credential item per provider/service connection:

```text
service: toolmux
key: profile/<profile>/provider/<provider>/service/<service>/account/<account-id>/oauth
value: versioned OAuth token bundle JSON
```

The keyring value contains provider access tokens, refresh tokens, expiry
timestamps, token type, scopes, and small provider-specific secret OAuth fields.
Display names, provider account ids, selected accounts, and other non-secret
metadata remain in config files so connection listing does not depend on
keyring enumeration.

## Provider MVPs

### Remote MCP Providers

Auth:

1. Remote MCP servers are registered under the general Toolmux `mcp` config
   key and authenticate with MCP OAuth, PKCE, dynamic client registration when
   advertised, or externally issued bearer tokens.
2. Server definitions and cached `tools/list` metadata are non-secret config.
   OAuth tokens, refresh tokens, bearer tokens, dynamic client secrets, and
   auth codes live only in the OS credential store or transient process
   memory.
3. Imported tool commands run policy and `--read-only` checks before stored
   auth is read or a remote HTTP call is made.

MVP commands:

```bash
toolmux list
toolmux add notion
toolmux mcp auth login notion
toolmux mcp sync notion
toolmux notion
toolmux mcp schema notion <tool>
```

Out of scope for MVP:

1. Native provider-specific Notion commands.
2. Bypassing provider admin approval, OAuth policy, or workspace governance.
3. Scraping browser sessions, local browser storage, or copying tokens out of
   provider-owned clients, except Slack's explicit `toolmux add slack`
   browser-session setup.

### Slack

Auth:

1. Explicit browser-session setup through embedded slackauth.
2. Explicit user-supplied token plus optional cookie header.
3. User-owned Slack OAuth app with local loopback callback.
4. toolmuxd-backed Slack OAuth for hosted or self-hosted broker flows.
5. Store scopes, team metadata, access token, refresh token, and refresh
   metadata locally.

Candidate scopes:

1. `channels:read`, `groups:read`, `im:read`, and `mpim:read`.
2. `channels:history`, `groups:history`, `im:history`, and `mpim:history`.
3. `search:read`.
4. `chat:write` only when send commands are enabled.

MVP commands:

```bash
toolmux add slack --workspace acme
toolmux add slack --token-env SLACK_TOKEN --cookie-env SLACK_COOKIE
toolmux add slack --auth oauth --client-id "$SLACK_CLIENT_ID"
toolmux add slack --auth broker
toolmux slack channels_list
toolmux slack conversations_history --channel_id C123456 --oldest 1710000000.000000
toolmux slack conversations_search_messages --search_query "from:@alice roadmap"
toolmux slack conversations_add_message --channel_id C123456 --text "Build is green"
```

Slack native command names use Slack MCP-style and Slack Web API method names:
`auth_test`, `conversations_history`, `conversations_replies`,
`conversations_add_message`, `reactions_add`, `reactions_remove`,
`attachment_get_data`, `conversations_search_messages`,
`conversations_unreads`, `conversations_mark`, `channels_list`,
`usergroups_list`, `usergroups_me`, `usergroups_create`,
`usergroups_update`, `usergroups_users_update`, and `users_search`.

Out of scope for MVP:

1. Browser cookie harvesting or Slack session extraction.
2. Slack administration APIs.
3. Bulk message mutation.

### Linear

Auth:

1. Native OAuth with PKCE.
2. Refresh can be local because Linear allows refresh using `client_id` when the token was generated with PKCE.

Candidate scopes:

1. `read`.
2. `issues:create`.
3. `comments:create`.
4. Avoid `admin`.

MVP commands:

```bash
toolmux linear issues list --team <key>
toolmux linear issue get <issue-id-or-key>
toolmux linear issue create --team <key> --title "..."
toolmux linear comment add <issue-id-or-key> --body "..."
```

Out of scope for MVP:

1. Admin endpoints.
2. Customer management.
3. Webhooks.
4. Agent actor authorization.

## Cross-Provider Requirements

All provider-like toolboxes must support:

1. Registration through `toolmux add` where applicable, removal through
   `toolmux remove`, global `status [toolbox...]`, and global `doctor` for
   core diagnostics.
2. Local credential storage.
3. Token refresh before expiry.
4. Remote revocation where supported.
5. Structured errors with provider error code, HTTP status, and retry hint.
6. `--output table|json|yaml`.
7. `--profile <name>` for multiple identities.
8. Command metadata for policy evaluation.
9. Local policy enforcement before token access and provider API calls.
10. TTY-aware behavior: interactive prompts, spinners, browser opens, and paging only happen in interactive contexts or when explicitly requested.
11. `status` output must show registered toolbox state, backend kind, stored
    auth type, tool count, and source URL when available.
12. `doctor` output must run core diagnostics plus remote MCP checks and include
    remediation when a check fails or warns.
13. Human-friendly table output and stable JSON/YAML output for the same command.
14. Shared terminal presentation through `internal/output`; providers return
    structured view models and never hand-roll colors, paging, prompts, or ad
    hoc table layouts.
15. Markdown-producing commands should render Markdown through
    `charm.land/glamour/v2` for interactive human table output, while
    JSON/YAML and non-interactive output remain undecorated and stable.
16. Stable JSON/YAML schemas for automation, even when human table columns are
    provider-specific or optimized for terminal width.
17. Preview or dry-run support for risky writes where the provider API allows safe preview.
18. Shell completion hooks for commands, providers, profiles, aliases, and provider-specific ids where feasible.
19. Open-in-browser support for commands that return provider URLs.

## Security Requirements

1. No provider token may be written to server logs, CLI logs, analytics, crash reports, command history, or plaintext config.
2. toolmuxd handoff records must expire within 120 seconds and be single-use.
3. toolmuxd handoff payloads may be returned over HTTPS without extra application-level encryption when they are held in short-lived process memory. Shared or durable handoff storage is out of MVP and requires a separate threat model before implementation.
4. OAuth `state` must be high entropy and validated on every flow.
5. PKCE must use S256 where supported.
6. Loopback listeners must bind to `127.0.0.1` and close immediately after callback.
7. Token refresh must atomically replace rotating refresh tokens.
8. Removing a toolbox must delete local credentials, and remote revocation
   should happen where an integration exposes a supported revocation endpoint.
9. The OSS server repo must include secret scanning and clear documentation that deployment operators must provide provider client secrets out of band.
10. Local policy denies must be checked before provider tokens are read from the OS credential store.
11. Browser cookies, local browser databases, workspace session tokens, and
    provider web-app bearer tokens are credential material. Slack browser
    session extraction is allowed only through explicit `toolmux add slack`
    browser auth and must validate with `auth.test` before storing credentials.

## Quality Requirements

Toolmux should treat correctness and maintainability as product requirements because the CLI will touch high-value SaaS accounts and local credentials.

Required quality gates:

1. Use the latest stable Go toolchain and keep the version guidance in `AGENTS.md` current.
2. Run an extensive linter suite in CI, including formatting, vet analyzers, static analysis, security scanning, vulnerability checks, dependency checks, and commit-message checks.
3. Emulate upstream providers in integration tests instead of relying only on unit tests or live SaaS sandboxes.
4. Test OAuth, refresh, revocation, policy denial, provider API errors, rate limits, pagination, and malformed responses against fake upstream servers.
5. Keep live-provider tests separate from deterministic CI tests and require explicit opt-in credentials for them.
6. Ensure every provider command has unit tests for command metadata, policy checks, output rendering, and error mapping.
7. Ensure `toolmuxd` has integration tests proving that plaintext provider tokens are never written to durable storage or logs.
8. Require conventional commits with 50-character subject lines and 72-character wrapped body lines so release automation and changelog generation remain reliable.

## Success Metrics

MVP success:

1. A user can add supported toolboxes without manually creating provider API
   keys when those providers support OAuth or external token issuance.
2. A user can run at least one read command and one write/create command for
   each implemented native provider or imported MCP server, subject to
   provider-scope limits.
3. 95% of successful OAuth flows complete in under 90 seconds after browser approval.
4. Token refresh happens without user interaction for supported providers.
5. Provider tokens are absent from server durable storage and logs in automated tests.
6. The CLI can be installed as a signed binary on macOS, Linux, and Windows.
7. A policy file can block write/destructive commands across native and
   imported MCP commands with consistent denial output.
8. CI blocks unformatted code, failing linters, broken fake-provider integration tests, detected vulnerabilities, token leaks, and invalid commit messages.
9. Non-interactive command runs never hang on prompts and always produce stable machine-readable output when `--output json` or `--output yaml` is used.
10. Human default output is readable enough that users can complete common read/create/update flows without consulting raw JSON.
11. The OSS repo publishes generic CLI archives and server images without exposing Toolmux production infrastructure or provider secrets.

## Risks

1. Native provider OAuth scope reviews can block or delay useful workflows.
2. toolmuxd availability affects brokered native refresh flows even though tokens are local.
3. Provider OAuth policies can change and may require re-review.
4. Local keychains behave differently in headless Linux and CI environments.
5. Local policy files are useful guardrails but can be bypassed by users who control their machine or working directory.
6. Private deployment code can drift from public `toolmuxd` behavior unless artifact versions and compatibility checks are enforced.

## Open Questions

1. Which providers with MCP support still need a native fallback, and what
   product gap justifies that work?
2. Should the default generated policy be `default: deny` for repos and `default: allow` for personal shells?
3. Which human shortcuts should ship in MVP versus being added after the canonical commands are stable?
4. Should aliases be stored per profile, per provider account, or both?
5. What are the preferred hosted `toolmuxd` domains for production, staging, and local development?
6. Should hosted Toolmux deploy `toolmuxd` to Lambda as a container image, a wrapped generic image, or a Lambda-specific private image?

## Source References

1. Linear OAuth 2.0: https://linear.app/developers/oauth-2-0-authentication
2. Linear GraphQL API: https://linear.app/developers/graphql
3. Slack API methods: https://api.slack.com/methods
4. Go 1.26 release notes: https://go.dev/doc/go1.26
5. Go release history: https://go.dev/doc/devel/release
6. Conventional Commits 1.0.0: https://www.conventionalcommits.org/en/v1.0.0/
7. AWS Lambda container images: https://docs.aws.amazon.com/lambda/latest/dg/go-image.html
8. AWS Lambda Function URLs: https://docs.aws.amazon.com/lambda/latest/dg/urls-configuration.html
9. AWS Secrets Manager with Lambda: https://docs.aws.amazon.com/lambda/latest/dg/with-secrets-manager.html
20. 99designs Go keyring package: https://pkg.go.dev/github.com/99designs/keyring
