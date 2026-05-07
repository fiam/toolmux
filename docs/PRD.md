# Supacli Initial Providers PRD

Last updated: 2026-05-07

## Summary

Supacli is an open-source CLI that lets users connect and operate common SaaS services from one command surface. The initial provider set is Notion, Jira, Slack, Linear, Google Docs, Google Drive, and Gmail. "Google driver" is treated as Google Drive.

The first release optimizes for a simple connection experience without asking users to create personal API keys or provider developer apps. Provider tokens are stored locally by default; Supacli does not provide cloud token storage in the initial release.

## Goals

1. Let users connect each initial provider with a browser-based OAuth flow.
2. Store long-lived provider credentials locally, protected by the user's operating system.
3. Provide a consistent command model across providers for auth, listing, reading, creating, and updating common resources.
4. Keep the hosted Supacli server daemon, `supaclid`, open-source and minimized: its OAuth broker component may exchange/refresh tokens when provider client secrets are required, but it must not persist provider tokens.
5. Make provider capability and scope limits explicit so users understand why some actions require reauthorization or are deferred.
6. Keep Supacli's production deployment infrastructure and provider secrets out of the OSS repo while publishing portable source and artifacts for CLI and server users.

## Non-Goals

1. No Supacli-hosted token vault in the initial release.
2. No team-shared connections.
3. No scheduled cloud workflows or background jobs.
4. No Slack bot/workspace automation as the default Slack experience.
5. No unrestricted Google Drive indexing/search in the initial release, because broad Drive scopes are restricted and increase verification/compliance burden.
6. No Gmail inbox search, message body reads, mailbox modification, forwarding, or admin settings in the initial release, because those scopes are restricted and increase verification/security-assessment burden.
7. No attempt to bypass provider OAuth policies or scrape browser/session tokens.
8. No AWS Lambda, DNS, certificate, production secret, or hosted deployment infrastructure in the OSS repo.

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

1. `supacli` CLI source.
2. `supaclid` server daemon source.
3. Generic self-hosting documentation.
4. Generic `supaclid` container build files.
5. Release automation for CLI binaries, Homebrew tap artifacts, and generic server images.

The OSS repository must not contain:

1. Supacli production AWS Lambda, API Gateway, ECR, DNS, certificate, or monitoring definitions.
2. Terraform, Pulumi, CDK, or deployment state for Supacli's hosted infrastructure.
3. Provider OAuth client secrets.
4. Production abuse controls, billing internals, allowlists, or alerting destinations.

Supacli's hosted deployment should live in a private infrastructure repo. That private repo may deploy `supaclid` to AWS Lambda, Lambda Function URLs, API Gateway, or another AWS entrypoint by consuming public release artifacts from this repo.

Self-hosters can run the OSS `supaclid`, but they must create their own provider OAuth apps and supply their own provider client ids and secrets.

## Product Principles

1. Local-first custody: provider refresh tokens stay on the user's machine unless the user later opts into a cloud vault.
2. Least privilege: each command requests only the scopes needed for that provider's MVP actions.
3. Explicit escalation: if a command requires missing scopes, Supacli explains the added scopes and starts reauthorization.
4. Portable output: every read/list command supports `--output table`, `--output json`, and `--output yaml`.
5. Scriptable defaults: commands fail with clear nonzero exits and structured errors in machine-readable output modes.
6. No token leakage: tokens, auth codes, refresh tokens, and `Authorization` headers are never printed or logged.
7. Policy before execution: every command and subcommand exposes authorization metadata and is checked against local policy before credential access or provider API calls.
8. Agent-first, human-friendly: structured command contracts remain the source of truth, with TTY-aware affordances layered on top for people.

## Command Policy and RBAC

Supacli should support local policy files so users and teams can restrict what the CLI is allowed to do from a given working directory. This is a local guardrail for developer workflows, automation, and shared repos. It is not a hard security boundary against a local user who can edit the policy file or run an older binary.

Policy discovery order:

1. Explicit `--policy <path>`.
2. `SUPACLI_POLICY=<path>`.
3. `.supacli/policy.yaml` in the current directory or parent directories.
4. No policy file means local interactive usage is allowed by default.

If multiple discovered policies apply, denies win over allows. A child-directory policy may add stricter rules, but it must not weaken a parent-directory deny.

Policy commands:

```bash
supacli policy init
supacli policy catalog
supacli policy check --command "jira issue create --project OPS --summary test"
supacli policy explain --command "gmail send --to user@example.com --subject test"
supacli policy doctor
```

Each command must declare metadata that the policy engine can evaluate:

```text
command: gmail.send
provider: gmail
resource: message
action: send
effect: write
risk: external-send
account: <resolved account id>
profile: <supacli profile>
scopes: [https://www.googleapis.com/auth/gmail.send]
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
        actions: ["read", "list", "search", "status"]
  operator:
    extends: ["reader"]
    allow:
      - provider: "jira"
        resources: ["issue", "comment"]
        actions: ["create", "update"]
      - provider: "linear"
        resources: ["issue", "comment"]
        actions: ["create", "update"]
    deny:
      - provider: "gmail"
        actions: ["send"]
      - provider: "google-drive"
        actions: ["delete", "share"]

bindings:
  - role: operator
    profiles: ["default"]
    accounts: ["*@company.com"]
```

Policy evaluation requirements:

1. Evaluate policy before loading provider tokens.
2. Deny by default when a policy file sets `default: deny`.
3. Support provider, resource, action, command, profile, account, risk, and normalized argument matching.
4. Return a clear denial reason and the policy rule that caused it.
5. Support `--policy` in all commands, including provider commands and auth commands.
6. Support machine-readable denial errors in JSON/YAML output.
7. Include a generated command/action catalog so users can write policies without reading source code.
8. Treat policy files as configuration, not secrets.
9. Avoid remote policy enforcement in the initial release; signed/team-managed policies can be added later.

## Agent and Human UX

Supacli is primarily an agent-friendly CLI, but it should also feel fast, clear, and pleasant for humans. The product should expose one deterministic command model with two presentation modes:

```bash
supacli ... --output json     # agent/script contract
supacli ...                   # human default
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
6. Shell completion should cover static commands and dynamic provider values such as profiles, Jira projects, Linear teams, Slack channels, Notion aliases, and Google accounts.
7. Users should be able to define aliases for provider-specific ids so command lines remain readable.
8. Commands that return web resources should support `--open` to launch the provider URL in a browser.

Human-oriented examples:

```bash
supacli linear mine
supacli jira open PROJ-123
supacli notion find roadmap
supacli slack send '#ops' 'deploy is done'
supacli linear issue create --title "Fix login" --dry-run
supacli gmail send --to user@example.com --subject "Hi" --preview
```

Discovery commands:

```bash
supacli providers
supacli examples linear
supacli linear help workflows
supacli policy catalog
```

Alias commands:

```bash
supacli alias set jira.default OPS
supacli alias set slack.ops C123456
supacli alias ls
```

Optional later UX:

```bash
supacli browse
supacli linear issues
supacli connections
```

These TUI-style flows are not required for MVP, but the architecture should not block them.

## Connection UX

Baseline commands:

```bash
supacli connect notion
supacli connect jira
supacli connect slack
supacli connect linear
supacli connect google

supacli connections ls
supacli connections doctor
supacli disconnect notion
```

`google-docs` and `google-drive` may be supported as aliases, but they should create or update the same underlying Google connection.
`gmail` may also be supported as a connection alias, but it should create or update the same underlying Google connection.

Connection success output should show:

1. Provider name.
2. Connected account/workspace/site.
3. Granted scopes in readable language.
4. Local storage mode.
5. Suggested first command.

Connection metadata may be stored in config files, but token material must be
stored directly in the user's OS credential store.

## Auth Model

Supacli supports two auth classes in the initial release.

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

1. Linear.
2. Google Docs/Drive/Gmail through Google desktop OAuth.
3. Slack user-token mode when using Slack PKCE-compatible desktop redirect flows.

### supaclid Local-Custody OAuth

Used when the provider requires a confidential client secret for token exchange or refresh.

Flow:

```text
CLI generates one-time handoff id and session secret
CLI opens auth.supacli.dev
Provider redirects to supaclid
supaclid exchanges code using provider client secret
supaclid stores token bundle in short-lived in-memory handoff
CLI retrieves bundle once over HTTPS and stores it locally
supaclid deletes the handoff material
```

Initial providers:

1. Notion.
2. Jira.
3. Slack bot/workspace-install mode, if enabled after user-token MVP.

supaclid must not store provider access tokens or refresh tokens in durable storage.

## Local Credential Storage

Supacli stores non-secret metadata separately from secrets.

```text
~/.config/supacli/config.yaml        # profiles, selected account ids, display names
OS credential store                  # OAuth token bundles and refresh metadata
```

OS credential store targets:

1. macOS Keychain.
2. Windows Credential Manager / DPAPI-backed credential APIs.
3. Linux Secret Service via GNOME Keyring or KWallet.

Supacli stores one OS credential item per provider/service connection:

```text
service: supacli
key: profile/<profile>/provider/<provider>/service/<service>/account/<account-id>/oauth
value: versioned OAuth token bundle JSON
```

The keyring value contains provider access tokens, refresh tokens, expiry
timestamps, token type, scopes, and small provider-specific secret OAuth fields.
Display names, provider account ids, selected accounts, and other non-secret
metadata remain in config files so connection listing does not depend on
keyring enumeration.

## Provider MVPs

### Notion

Auth:

1. supaclid OAuth through a Notion public connection.
2. Token refresh uses supaclid because Notion requires client credentials for refresh.
3. User grants access to selected pages/databases in Notion.

MVP commands:

```bash
supacli notion search --query "roadmap"
supacli notion page get <page-id>
supacli notion page create --parent <page-id> --title "..."
supacli notion database query <database-id>
```

Out of scope for MVP:

1. Full workspace crawling beyond pages/databases selected in Notion's permission flow.
2. Complex block editing UI.
3. Database schema migrations.

### Jira

Auth:

1. supaclid-backed Atlassian OAuth 2.0 3LO.
2. Store `cloudId`, site URL, user account id, scopes, access token, and rotating refresh token locally.
3. Refresh uses supaclid because Atlassian requires the app client secret.

Candidate scopes:

1. `offline_access`.
2. `read:jira-work`.
3. `read:jira-user`.
4. `write:jira-work` only when create/comment/transition commands are enabled.

MVP commands:

```bash
supacli jira sites ls
supacli jira issues list --jql "assignee = currentUser() ORDER BY updated DESC"
supacli jira issue get PROJ-123
supacli jira issue create --project PROJ --type Task --summary "..."
supacli jira comment add PROJ-123 --body "..."
```

Out of scope for MVP:

1. Jira Data Center OAuth variants.
2. Jira administration APIs.
3. Bulk issue mutation.

### Slack

Auth:

1. Default MVP path is user-token OAuth with PKCE where possible.
2. Slack bot scopes are not the default because Slack desktop redirects with PKCE are not allowed to request bot scopes.
3. Slack bot/workspace install can be added through supaclid as a separate `--mode bot` path.

Candidate user scopes:

1. `chat:write` for posting as the connected user.
2. `channels:read`, `groups:read`, `im:read`, and `mpim:read` for listing visible conversations, subject to Slack approval rules.
3. `search:read` for user-level message search if accepted by the app review and target workspaces.

MVP commands:

```bash
supacli slack conversations ls
supacli slack message send --channel <id-or-name> --text "..."
supacli slack search --query "from:me deploy"
```

Out of scope for MVP:

1. Slack Events API ingestion.
2. Socket Mode.
3. Bot mentions and automations.
4. Admin, Audit Logs, Legal Holds, or SCIM APIs.

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
supacli linear issues list --team <key>
supacli linear issue get <issue-id-or-key>
supacli linear issue create --team <key> --title "..."
supacli linear comment add <issue-id-or-key> --body "..."
```

Out of scope for MVP:

1. Admin endpoints.
2. Customer management.
3. Webhooks.
4. Agent actor authorization.

### Google Docs

Auth:

1. Native Google desktop OAuth.
2. Use narrow scopes first.
3. Prefer `https://www.googleapis.com/auth/drive.file` for files created, opened, or explicitly selected for Supacli.

MVP commands:

```bash
supacli google docs create --title "..."
supacli google docs get <document-id>
supacli google docs export <document-id> --format markdown
supacli google docs append <document-id> --text "..."
```

Scope strategy:

1. Start with `drive.file` for Supacli-created or user-selected docs.
2. Add `documents.readonly` or `documents` only if the command requires direct Docs API access not covered by `drive.file`.
3. Defer broad all-docs search until Google verification requirements are understood and accepted.

Out of scope for MVP:

1. Full account-wide Docs search.
2. Comments/suggestions.
3. Rich collaborative editing features.

### Google Drive

Auth:

1. Native Google desktop OAuth.
2. Prefer non-sensitive `drive.file` for least privilege.
3. Avoid `drive` and `drive.readonly` in MVP because they are restricted scopes.

MVP commands:

```bash
supacli google drive upload <path> --parent <folder-id>
supacli google drive download <file-id> --output <path>
supacli google drive ls --created-by supacli
supacli google drive folder create --name "..."
```

Scope strategy:

1. Use `drive.file` for files Supacli creates or the user explicitly selects.
2. Use `drive.appdata` only for app configuration data if needed.
3. Defer all-drive search/listing until restricted-scope verification and possible security assessment are handled.

Out of scope for MVP:

1. Whole-Drive indexing.
2. Shared drive administration.
3. Permission management beyond files Supacli creates.

### Gmail

Auth:

1. Native Google desktop OAuth through the shared Google connection.
2. Use Gmail scopes only when a Gmail command requires them.
3. Avoid restricted Gmail scopes in MVP.

Candidate scopes:

1. `https://www.googleapis.com/auth/gmail.labels` for listing and editing labels. This is non-sensitive.
2. `https://www.googleapis.com/auth/gmail.send` for sending email. This is sensitive and requires OAuth verification.
3. Avoid `gmail.readonly`, `gmail.metadata`, `gmail.modify`, `gmail.compose`, `gmail.settings.basic`, and `gmail.settings.sharing` in MVP because they are restricted.

MVP commands:

```bash
supacli gmail labels ls
supacli gmail labels create --name "..."
supacli gmail send --to user@example.com --subject "..." --body "..."
```

Scope strategy:

1. Start with `gmail.labels` for read/write label commands.
2. Request `gmail.send` only when the user runs a send command or explicitly enables Gmail send support.
3. Defer message listing, search, body reads, attachment reads, drafts, mailbox modification, forwarding, and settings until restricted-scope verification and security-assessment requirements are accepted.

Out of scope for MVP:

1. Inbox/message search.
2. Message body or attachment reads.
3. Draft management.
4. Labeling or archiving messages.
5. Gmail settings, delegates, forwarding, filters, or admin actions.

## Cross-Provider Requirements

All providers must support:

1. `connect`, `disconnect`, `status`, and `doctor`.
2. Local credential storage.
3. Token refresh before expiry.
4. Remote revocation where supported.
5. Structured errors with provider error code, HTTP status, and retry hint.
6. `--output table|json|yaml`.
7. `--profile <name>` for multiple identities.
8. `--account <id-or-alias>` when multiple workspaces/sites are connected.
9. Command metadata for policy evaluation.
10. Local policy enforcement before token access and provider API calls.
11. TTY-aware behavior: interactive prompts, spinners, browser opens, and paging only happen in interactive contexts or when explicitly requested.
12. Human-friendly table output and stable JSON/YAML output for the same command.
13. Preview or dry-run support for risky writes where the provider API allows safe preview.
14. Shell completion hooks for commands, providers, profiles, aliases, and provider-specific ids where feasible.
15. Open-in-browser support for commands that return provider URLs.

## Security Requirements

1. No provider token may be written to server logs, CLI logs, analytics, crash reports, command history, or plaintext config.
2. supaclid handoff records must expire within 120 seconds and be single-use.
3. supaclid handoff payloads may be returned over HTTPS without extra application-level encryption when they are held in short-lived process memory. Shared or durable handoff storage is out of MVP and requires a separate threat model before implementation.
4. OAuth `state` must be high entropy and validated on every flow.
5. PKCE must use S256 where supported.
6. Loopback listeners must bind to `127.0.0.1` and close immediately after callback.
7. Token refresh must atomically replace rotating refresh tokens.
8. `disconnect` must revoke remote tokens where the provider exposes a revocation endpoint, then delete local credentials.
9. The OSS server repo must include secret scanning and clear documentation that deployment operators must provide provider client secrets out of band.
10. Local policy denies must be checked before provider tokens are read from the OS credential store.

## Quality Requirements

Supacli should treat correctness and maintainability as product requirements because the CLI will touch high-value SaaS accounts and local credentials.

Required quality gates:

1. Use the latest stable Go toolchain and keep the version guidance in `AGENTS.md` current.
2. Run an extensive linter suite in CI, including formatting, vet analyzers, static analysis, security scanning, vulnerability checks, dependency checks, and commit-message checks.
3. Emulate upstream providers in integration tests instead of relying only on unit tests or live SaaS sandboxes.
4. Test OAuth, refresh, revocation, policy denial, provider API errors, rate limits, pagination, and malformed responses against fake upstream servers.
5. Keep live-provider tests separate from deterministic CI tests and require explicit opt-in credentials for them.
6. Ensure every provider command has unit tests for command metadata, policy checks, output rendering, and error mapping.
7. Ensure `supaclid` has integration tests proving that plaintext provider tokens are never written to durable storage or logs.
8. Require conventional commits with 50-character subject lines and 72-character wrapped body lines so release automation and changelog generation remain reliable.

## Success Metrics

MVP success:

1. A user can connect all seven providers without manually creating API keys.
2. A user can run at least one read command and one write/create command for each provider, subject to provider-scope limits.
3. 95% of successful OAuth flows complete in under 90 seconds after browser approval.
4. Token refresh happens without user interaction for supported providers.
5. Provider tokens are absent from server durable storage and logs in automated tests.
6. The CLI can be installed as a signed binary on macOS, Linux, and Windows.
7. A policy file can block write/destructive commands across all initial providers with consistent denial output.
8. CI blocks unformatted code, failing linters, broken fake-provider integration tests, detected vulnerabilities, token leaks, and invalid commit messages.
9. Non-interactive command runs never hang on prompts and always produce stable machine-readable output when `--output json` or `--output yaml` is used.
10. Human default output is readable enough that users can complete common read/create/update flows without consulting raw JSON.
11. The OSS repo publishes generic CLI/server artifacts without exposing Supacli production infrastructure or provider secrets.

## Risks

1. Google verification can block or delay broad Docs/Drive/Gmail features.
2. Slack PKCE user-token capabilities may not cover desired bot/workspace workflows.
3. supaclid availability affects Notion/Jira refresh flows even though tokens are local.
4. Provider OAuth policies can change and may require re-review.
5. Local keychains behave differently in headless Linux and CI environments.
6. Local policy files are useful guardrails but can be bypassed by users who control their machine or working directory.
7. Private deployment code can drift from public `supaclid` behavior unless artifact versions and compatibility checks are enforced.

## Open Questions

1. Should Slack MVP be user-token only, or should supaclid-backed bot install be included in the first public beta?
2. Should Google Docs and Drive be separate top-level commands or grouped under `supacli google`?
3. Should Gmail commands be top-level as `supacli gmail`, grouped under `supacli google gmail`, or both?
4. Should Notion write commands require an explicit `--parent` every time, or should users define a default workspace/page alias?
5. Should Jira write commands be enabled in MVP or gated behind a second auth scope escalation?
6. Should the default generated policy be `default: deny` for repos and `default: allow` for personal shells?
7. Which human shortcuts should ship in MVP versus being added after the canonical commands are stable?
8. Should aliases be stored per profile, per provider account, or both?
9. What are the preferred hosted `supaclid` domains for production, staging, and local development?
10. Should hosted Supacli deploy `supaclid` to Lambda as a container image, a wrapped generic image, or a Lambda-specific private image?

## Source References

1. Notion public OAuth authorization: https://developers.notion.com/guides/get-started/authorization
2. Atlassian Jira OAuth 2.0 3LO: https://developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/
3. Atlassian Jira OAuth scopes: https://developer.atlassian.com/cloud/jira/platform/scopes-for-oauth-2-3LO-and-forge-apps/
4. Slack OAuth v2: https://docs.slack.dev/authentication/installing-with-oauth/
5. Slack PKCE: https://docs.slack.dev/authentication/using-pkce/
6. Slack token rotation: https://docs.slack.dev/authentication/using-token-rotation/
7. Linear OAuth 2.0: https://linear.app/developers/oauth-2-0-authentication
8. Linear GraphQL API: https://linear.app/developers/graphql
9. Google desktop OAuth: https://developers.google.com/identity/protocols/oauth2/native-app
10. Google Docs API scopes: https://developers.google.com/workspace/docs/api/auth
11. Google Drive API scopes: https://developers.google.com/workspace/drive/api/guides/api-specific-auth
12. Gmail API scopes: https://developers.google.com/workspace/gmail/api/auth/scopes
13. Gmail sending guide: https://developers.google.com/gmail/api/guides/sending
14. Google Workspace API user data and developer policy: https://developers.google.com/gmail/api/policy
15. Go 1.26 release notes: https://go.dev/doc/go1.26
16. Go release history: https://go.dev/doc/devel/release
17. Conventional Commits 1.0.0: https://www.conventionalcommits.org/en/v1.0.0/
18. AWS Lambda container images: https://docs.aws.amazon.com/lambda/latest/dg/go-image.html
19. AWS Lambda Function URLs: https://docs.aws.amazon.com/lambda/latest/dg/urls-configuration.html
20. AWS Secrets Manager with Lambda: https://docs.aws.amazon.com/lambda/latest/dg/with-secrets-manager.html
21. 99designs Go keyring package: https://pkg.go.dev/github.com/99designs/keyring
