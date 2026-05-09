# Toolmux Initial Provider Implementation Plan

Last updated: 2026-05-07

## Technical Direction

Use Go for both the CLI and `toolmuxd`, the Toolmux server daemon. Keep provider logic in one repository at first so the CLI and server share token schemas, provider metadata, and test fixtures.

Use the latest stable Go release. As of 2026-05-07, `govulncheck` reports Go
1.26.3 as the security-fix patch release for the Go 1.26 line. Set the module
to Go 1.26, pin CI to the latest Go 1.26 patch, and update `AGENTS.md`
whenever the project intentionally changes Go versions or toolchain
expectations.

Initial repository layout:

```text
cmd/toolmux/                  # CLI entrypoint
cmd/toolmuxd/                 # Toolmux server daemon entrypoint
Dockerfile                    # generic toolmuxd OCI image
internal/cli/                 # command tree and command helpers
internal/config/              # profiles and non-secret metadata
internal/output/              # table/json/yaml renderers
internal/policy/              # local command policy and RBAC engine
internal/credentials/         # domain credential store over OS keyrings
internal/oauth/               # PKCE, state, loopback, server handoff
internal/providers/           # provider registry and common interfaces
internal/providers/brokers/   # toolmuxd OAuth/token broker registry
internal/providers/notion/
internal/providers/notion/client/
internal/providers/notion/broker/
internal/providers/all/       # client provider side-effect imports
internal/providers/brokers/all/ # server broker side-effect imports
internal/providers/jira/
internal/providers/slack/
internal/providers/google/
internal/server/              # server HTTP handlers and provider exchanges
internal/testutil/            # fake OAuth server and provider fixtures
docs/
```

Recommended core dependencies:

1. `spf13/cobra` for CLI commands.
2. `gopkg.in/yaml.v3` for config and YAML output.
3. `charm.land/lipgloss/v2` for shared terminal styles, tables, hyperlinks,
   and graceful color degradation.
4. `github.com/muesli/termenv` for terminal capability and color-policy
   detection, including `NO_COLOR` and `CLICOLOR_FORCE` behavior.
5. `charm.land/glamour/v2` for rendering provider markdown
   descriptions in human table/detail output.
6. `github.com/99designs/keyring` behind an internal interface for OS
   credential stores.
7. `golang.org/x/oauth2` only where it matches provider behavior; wrap
   provider differences instead of leaking it into commands.
8. No separate full-screen TUI dependency for MVP; use regular commands with
   terminal-aware rendering and external `$PAGER` integration.
9. No separate encrypted-file vault dependency in the MVP; provider token
   bundles are stored directly in the user's OS credential store.
10. No extra handoff-encryption dependency for MVP; use HTTPS, one-time session
    secrets, and short-lived in-memory handoff. Shared or durable handoff
    storage is out of MVP and requires a separate threat model before
    implementation.
11. `goreleaser` and `cosign` for signed releases.

Recommended quality tooling:

1. `gofmt` and `go fmt ./...`.
2. `go vet ./...`, including modern analyzers from the current Go toolchain.
3. `go test -race ./...`.
4. `go test -coverprofile=coverage.out ./...`.
5. Dockerfile-based `make lint` with pinned `govulncheck`, `staticcheck`,
   `modernize`, `golangci-lint`, `gosec`, `gitleaks`, and `actionlint`
   versions, plus repository-wide YAML linting.
6. `commitlint` or equivalent conventional-commit validation.
7. `shellcheck` for shell scripts.
8. `yamllint` and Markdown linting for repo configuration and docs.

## Repository Boundary

This OSS repo owns portable product code and public artifacts:

1. `toolmux` CLI source and releases.
2. `toolmuxd` server source.
3. Generic `toolmuxd` container images.
4. Fake-provider tests and self-hosting docs.

Toolmux's hosted AWS/Lambda deployment belongs in a private infrastructure repo.
That private repo owns Lambda packaging, API Gateway or Function URL routing,
ECR promotion, provider secrets, DNS, certs, monitoring, abuse controls, and
deployment state.

The public repo should not grow Lambda-specific deployment code unless it is
generic and useful to self-hosters. Keep `toolmuxd` portable as an HTTP daemon.

## Architecture

### CLI Runtime

Every command receives an application context:

```text
AppContext
  ConfigStore
  PolicyEngine
  ProviderActionCatalog
  CredentialStore
  ProviderRegistry
  OutputRenderer
  HTTPClientFactory
  Logger
```

Provider commands must not read tokens directly. They first authorize the
provider action against local policy, then request an authorized client:

```text
Command -> PolicyEngine.Authorize(action metadata) -> Provider.AuthClient(profile, account, scopes) -> token refresh if needed -> API call
```

Policy authorization must happen before credential reads, token refresh, or provider API calls.

### Terminal Output Layer

Toolmux should have one command surface for humans and agents. Provider commands
return structured results; the shared output layer decides how to render those
results for the current terminal and selected output format.

Initial package shape:

```text
internal/output/
  renderer.go       # table/json/yaml dispatch and result contracts
  terminal.go       # TTY, width, color, and environment detection
  styles.go         # shared semantic colors and status badges
  table.go          # compact human tables
  pager.go          # external $PAGER integration
  markdown.go       # terminal markdown rendering
  errors.go         # human and machine-readable error rendering
```

Output options:

```go
type Options struct {
    Format      Format      // table, json, yaml
    ColorPolicy ColorPolicy // auto, always, never
    PagerPolicy PagerPolicy // auto, always, never
    Interactive bool
    Width       int
    Quiet       bool
}
```

Rules:

1. Providers must not write precolored strings, invoke pagers, prompt directly,
   or create provider-specific table renderers.
2. JSON/YAML output is the automation contract: stable schemas, no ANSI escape
   sequences, no pagers, no progress animation, and no implicit prompts.
3. Human table output can use `lipgloss` styles, `termenv` capability detection,
   `glamour` markdown rendering, semantic badges, and terminal hyperlinks.
4. Pager support should call the user's `$PAGER` when output is long and stdout
   is a TTY. JSON/YAML should not page unless the user explicitly requests it.
5. `--color auto` must respect `NO_COLOR`, `CLICOLOR=0`,
   `CLICOLOR_FORCE=1`, and `TERM=dumb`.
6. Snapshot tests should fix terminal width and color policy so table output is
   deterministic.
7. Do not add a Bubble Tea-style app for MVP provider workflows. If
   interactive browsing is added later, it must still share provider action
   specs, policy checks, credential access, and output contracts with normal
   commands.

### Command Catalog and Policy Engine

Every command and subcommand declares a stable command spec:

```go
type CommandSpec struct {
    ID        string
    Provider  string
    Resource  string
    Action    string
    Effect    string
    Risk      []string
    Scopes    []string
}
```

Runtime authorization evaluates the command spec plus normalized invocation data:

```go
type Invocation struct {
    Spec       CommandSpec
    Profile    string
    Account    string
    Args       map[string]any
    OutputMode string
    WorkingDir string
}
```

Policy discovery order:

1. Explicit `--policy <path>`.
2. `TOOLMUX_POLICY=<path>`.
3. `.toolmux/policy.yaml` in the current directory or parent directories.
4. No policy file means allow local interactive usage.

Policy semantics:

1. YAML policy v1 is declarative; do not embed a general-purpose scripting language in MVP.
2. Deny rules override allow rules.
3. Multiple discovered policies are merged with deny-overrides semantics.
4. A child-directory policy may add stricter rules, but it must not weaken a parent-directory deny.
5. `default: deny` requires an explicit allow for the command.
6. `default: allow` permits commands unless denied.
7. Denials include the matched rule id/path and a short reason.

Initial policy commands:

```bash
toolmux policy init
toolmux policy catalog
toolmux policy check --command "notion page read Roadmap"
toolmux policy explain --command "notion page create --title Draft"
toolmux policy doctor
```

The provider action catalog should be generated by walking registered provider
`actions.Spec` trees so docs, completions, policy validation, CLI commands, MCP
tools, and REST routes cannot drift from implemented commands. Group nodes and
leaf actions use the same metadata type. Root `connect`, `disconnect`,
`status [provider...]`, and `doctor [provider...]` remain explicit CLI commands
because their human interaction and diagnostics are cross-provider.

### Provider Interface

The provider catalog wires provider metadata and behavior into every surface:

```go
type Provider struct {
    ID                 string
    DisplayName        string
    AuthMode           string
    Aliases            []string
    Tree               actions.Spec
    Handlers           map[string]actions.Handler
    Diagnostics        actions.DiagnosticsFunc
    DefaultPermissions func() []string
    BaseURLEnv         string
    DefaultBaseURL     string
    APIVersionEnv      string
    DefaultAPIVersion  string
}
```

Each provider has small shared core metadata plus optional facet packages. The
`client` facet owns command trees, action handlers, API clients, diagnostics,
fake upstream command tests, and provider-specific client tests. Surface adapters
such as Cobra, future MCP, and future REST walk the same tree, evaluate policy,
construct an `actions.Context`, invoke the provider handler, and render or
serialize the returned typed result. Do not put provider command execution in
`internal/cli`.

Provider facets self-register:

```go
func init() {
    providers.Register(Descriptor())
}
```

`init()` must only register static descriptors. It must not read environment
variables, touch files, call the network, load credentials, start goroutines, or
log. The `internal/providers/all` package blank-imports enabled client facets,
and `toolmux` plus command test harnesses import that bundle for side effects.
The `internal/providers/brokers/all` package blank-imports enabled broker
facets, and `toolmuxd` imports that bundle for side effects. Adding a provider
should require adding provider packages and one import in the relevant bundle,
not editing the CLI or server surfaces.

`AuthMode` values:

1. `native_pkce`.
2. `brokered_local_custody`.
3. `manual_token`, hidden behind an explicit unsupported/dev flag.

### Local Credential Store

Store provider token bundles directly in the user's OS credential store through
a domain-specific interface. Do not expose random key-value operations to
provider code.

Non-secret connection metadata:

```text
~/.config/toolmux/config.yaml
  profiles, selected account ids, display names, scopes, and provider metadata
```

OS credential store item:

```text
service: toolmux
key: profile/<profile>/provider/<provider>/service/<service>/account/<account-id>/oauth
value: versioned OAuth token bundle JSON
```

Credential interface:

```go
type Store interface {
    SaveOAuthTokens(ctx context.Context, ref ConnectionRef, tokens OAuthTokens) error
    LoadOAuthTokens(ctx context.Context, ref ConnectionRef) (OAuthTokens, error)
    DeleteOAuthTokens(ctx context.Context, ref ConnectionRef) error
    Doctor(ctx context.Context) Diagnostics
}
```

Rules:

1. Use `github.com/99designs/keyring` behind `internal/credentials`.
2. Store only minimal OAuth token bundles and refresh metadata in keyring items.
3. Store display names, provider account ids, selected accounts, and scopes in non-secret config.
4. Do not rely on keyring enumeration for connection listing; config is the index.
5. Disable plaintext fallback by default.
6. Add `toolmux doctor` for keyring availability diagnostics.

### Native PKCE Flow

Used for Linear, Google Docs/Drive/Gmail, and Slack user-token mode when configured.

Implementation:

1. Generate `state`, `code_verifier`, and S256 `code_challenge`.
2. Bind an HTTP listener to `127.0.0.1` on an ephemeral port.
3. Open the system browser.
4. Validate callback `state`.
5. Exchange code for tokens.
6. Persist token bundle locally.
7. Close listener immediately.

The callback HTML should contain no token data.

### toolmuxd-Backed Local-Custody Flow

Used for Notion, Jira, and Slack bot/workspace mode.

toolmuxd OAuth endpoints:

```text
POST /v1/oauth/sessions
GET  /v1/oauth/sessions/{session_id}
GET  /oauth/{provider}/start
GET  /oauth/{provider}/callback
POST /v1/oauth/{provider}/refresh
POST /v1/oauth/{provider}/revoke
GET  /healthz
```

Session creation:

```json
{
  "provider": "notion",
  "mode": "default",
  "requested_scopes": [],
  "cli_public_key": "...",
  "session_secret_hash": "...",
  "return_hint": "poll"
}
```

Session response:

```json
{
  "session_id": "...",
  "auth_url": "https://api.toolmux.com/oauth/notion/start?session_id=...",
  "expires_at": "2026-05-06T12:01:00Z"
}
```

Callback handling:

1. Validate server session and OAuth `state`.
2. Exchange code using deployment-only provider client secret.
3. Normalize provider token response.
4. Store the token bundle in a short-lived in-memory handoff with TTL <= 120 seconds.
5. Return a browser page telling the user to return to the terminal.
6. Shared or durable handoff storage is out of MVP.

CLI polling:

1. Poll `GET /v1/oauth/sessions/{session_id}` with the session secret.
2. Receive token bundle once over HTTPS.
3. Store in the local OS credential store.

Refresh for toolmuxd-backed providers:

1. CLI sends the current refresh token to toolmuxd over HTTPS.
2. toolmuxd calls provider token endpoint with its client secret.
3. toolmuxd returns new token bundle in the HTTP response.
4. toolmuxd does not persist the token.
5. CLI atomically replaces the old local token.

This is not zero-trust: toolmuxd sees tokens in memory. It is local-custody: toolmuxd does not retain tokens.

## Milestones

### M0 - Repo and Tooling

Deliverables:

1. Initialize Go module with `go 1.26`.
2. Add `cmd/toolmux` and `cmd/toolmuxd`.
3. Add `make test`, `make lint`, and `make build`.
4. Add `make test-integration` for deterministic fake-upstream tests.
5. Add `make test-live` for opt-in live-provider smoke tests.
6. Add GitHub Actions for tests, race tests, integration tests,
   Dockerfile-based linting, docs linting, and commit-message validation.
7. Add GoReleaser config with signed artifacts.
8. Add repository-level `AGENTS.md` with Go version, testing, linting, and commit-message rules.
9. Add generic `toolmuxd` OCI image build config.
10. Add public self-hosting docs that do not assume Toolmux's AWS account.

Acceptance criteria:

1. `toolmux --help` works.
2. `toolmuxd --help` works.
3. CI passes without provider secrets.
4. CI runs fake-upstream integration tests without network calls to real providers.
5. Invalid conventional commits are rejected by local tooling or CI.
6. `make build-toolmuxd-image` builds a generic server image when Docker is available.

### M1 - CLI Core

Deliverables:

1. Config profiles.
2. Local credential store over OS keyrings.
3. Output renderers for table, JSON, and YAML.
4. Provider registry.
5. Command catalog and local policy engine.
6. Base auth commands:

```bash
toolmux connect <provider>
toolmux disconnect <provider>
toolmux status [provider...]
toolmux doctor [provider...]
toolmux connections ls
```

7. Base policy commands:

```bash
toolmux policy init
toolmux policy catalog
toolmux policy check
toolmux policy explain
toolmux policy doctor
```

Acceptance criteria:

1. Credential-store tests cover create, read, update, delete, missing
   credentials, corrupt payloads, and backend diagnostics.
2. Table/JSON/YAML snapshots are stable at fixed terminal widths.
3. Output tests prove JSON/YAML disable ANSI color, pagers, prompts, spinners,
   browser opens, and hyperlinks unless those values are literal data fields.
4. Terminal tests cover `NO_COLOR`, `CLICOLOR=0`, `CLICOLOR_FORCE=1`,
   `TERM=dumb`, non-TTY stdout, and explicit `--color`/`--pager` overrides.
5. Missing keyring produces actionable diagnostics.
6. Policy tests cover allow, deny, default-deny, default-allow, parent/child
   merge behavior, malformed policy files, and machine-readable denial output.
7. A policy denial happens before credential-store access in a provider command
   test.
8. Linter configuration is strict enough to catch unchecked errors, shadowing
   mistakes, leaked goroutines in tests, context misuse, unsafe formatting of
   host/port pairs, and insecure crypto defaults.

### M2 - toolmuxd with Notion

Why second:

Notion validates the toolmuxd-backed local-custody model and has explicit page/database access constraints users need to understand.

Deliverables:

1. toolmuxd session API.
2. toolmuxd provider secret config via environment variables.
3. Short-lived, single-use, in-memory token handoff.
4. Notion provider in CLI.
5. Notion toolmuxd exchange and refresh handlers.
6. Notion commands:

```bash
toolmux notion search
toolmux status notion
toolmux notion page get
toolmux notion page markdown
toolmux notion page create
toolmux notion page update
toolmux notion page content insert
toolmux notion page content replace
toolmux notion page content update
toolmux notion page delete
toolmux notion page restore
toolmux notion page move
toolmux notion data-source query
toolmux notion database data-sources
```

Acceptance criteria:

1. toolmuxd durable store contains no plaintext token fields.
2. Handoff payload is single-use and expires.
3. Token refresh path uses toolmuxd and updates local rotating tokens if Notion returns replacements.
4. Notion "missing page access" errors suggest sharing the page/database with the Toolmux connection.
5. Notion provider action specs include command paths, argument constraints,
   flags, remote/local effects, and scopes for generated surfaces and policy
   evaluation.
6. CLI defaults to `https://api.toolmux.com` for toolmuxd and supports
   `TOOLMUX_TOOLMUXD_URL` for local development and self-hosting.

### M3 - Jira

Deliverables:

1. Atlassian OAuth 3LO toolmuxd exchange.
2. Atlassian toolmuxd refresh with rotating refresh token handling.
3. `accessible-resources` lookup and local `cloudId` storage.
4. Jira provider commands:

```bash
toolmux jira sites ls
toolmux jira issues list
toolmux jira issue get
toolmux jira issue create
toolmux jira comment add
```

Acceptance criteria:

1. Multiple Atlassian sites can be represented under one connection.
2. The user can choose a default Jira site per profile.
3. Refresh token rotation is atomic.
4. Permission errors distinguish missing OAuth scope from missing Jira project permission.
5. Jira create/comment commands can be denied by resource, action, project key, or account.

### M4 - Google Docs, Google Drive, and Gmail

Deliverables:

1. Google desktop OAuth provider.
2. Scope escalation support.
3. Shared Google connection used by Docs, Drive, and Gmail command groups.
4. Google Docs commands:

```bash
toolmux google docs create
toolmux google docs get
toolmux google docs export
toolmux google docs append
```

5. Google Drive commands:

```bash
toolmux google drive upload
toolmux google drive download
toolmux google drive ls --created-by toolmux
toolmux google drive folder create
```

6. Gmail commands:

```bash
toolmux gmail labels ls
toolmux gmail labels create
toolmux gmail send
```

Acceptance criteria:

1. MVP uses narrow Google scopes first, with `drive.file` preferred for Docs/Drive and `gmail.labels` preferred for Gmail label commands.
2. Commands that require broader scopes fail with a clear explanation instead of silently over-requesting.
3. Google refresh tokens are saved long-term and refreshed locally.
4. Broad Drive search is gated as non-MVP unless verification work is complete.
5. Gmail message search/read/modify commands are gated as non-MVP unless restricted-scope verification and security-assessment requirements are complete.
6. Gmail send requests `gmail.send` only when the user runs a send command or explicitly enables Gmail send support.
7. Gmail send can be denied by recipient domain, account, command risk, or action.

### M5 - Slack User Mode

Deliverables:

1. Slack PKCE user-token connection mode.
2. Slack token rotation support.
3. Slack commands:

```bash
toolmux slack conversations ls
toolmux slack message send
toolmux slack search
```

Acceptance criteria:

1. Slack user-token mode does not request bot scopes.
2. Token rotation handles 12-hour access tokens and refresh-token replacement.
3. Commands clearly tell users when workspace admin approval is required.
4. Slack bot/workspace mode remains explicitly out of MVP or behind `toolmux connect slack --mode bot`.
5. Slack send/search commands include distinct policy actions.

### M6 - Hardening and Beta

Deliverables:

1. Provider contract tests using fake OAuth and fake API servers.
2. End-to-end tests for native OAuth and toolmuxd-backed handoff.
3. Threat model document.
4. Privacy policy text for hosted toolmuxd.
5. Signed release artifacts.
6. Install docs for macOS, Linux, and Windows.

Acceptance criteria:

1. Automated tests assert that token-like fields are redacted from logs.
2. toolmuxd endpoints pass tests for TTL, replay prevention, state validation, and missing session handling.
3. Release checksums and signatures are generated in CI.
4. At least one clean-machine install test is run per OS.

## Provider Configuration

toolmuxd deployment environment variables:

```text
TOOLMUX_PUBLIC_URL=https://api.toolmux.com
TOOLMUX_REDIS_URL=redis://...

NOTION_CLIENT_ID=...
NOTION_CLIENT_SECRET=...
NOTION_REDIRECT_URI=https://api.toolmux.com/oauth/notion/callback

ATLASSIAN_CLIENT_ID=...
ATLASSIAN_CLIENT_SECRET=...
ATLASSIAN_REDIRECT_URI=https://api.toolmux.com/oauth/jira/callback

SLACK_CLIENT_ID=...
SLACK_CLIENT_SECRET=...
SLACK_REDIRECT_URI=https://api.toolmux.com/oauth/slack/callback
```

CLI configuration:

```yaml
default_profile: default
server_url: https://api.toolmux.com
profiles:
  default:
    output: table
    defaults:
      jira_site: null
      google_account: null
      gmail_account: null
      slack_workspace: null
```

## Testing Strategy

Toolmux needs deterministic integration tests that emulate provider behavior. Live-provider tests are useful, but they must not be the primary signal because they are flaky, slow, rate-limited, and require secrets.

Unit tests:

1. PKCE generation and validation.
2. OAuth state creation and verification.
3. Vault encryption, migration, locking, and corruption handling.
4. Provider response parsing.
5. Output rendering.
6. Policy parsing, role inheritance, glob matching, deny-overrides behavior, and denial rendering.

Integration tests:

1. Fake provider OAuth server for native flow.
2. Fake toolmuxd-backed provider with client-secret exchange.
3. In-memory handoff TTL and single-use behavior.
4. Token refresh rotation races.
5. Policy denial before credential lookup for representative read, write, send, and disconnect commands.
6. Fake Notion, Jira, Slack, Google Docs, Google Drive, and Gmail servers.
7. Provider fixtures for success, expired token, revoked token, missing scope, permission denied, rate limit, pagination, malformed JSON, empty response, and 5xx retry behavior.
8. OAuth callback and toolmuxd handoff tests that run fully offline.
9. Contract tests ensuring provider action specs, command args, flags, required
   scopes, and policy actions match implemented commands.

Manual provider tests:

1. One sandbox workspace/account for each provider.
2. Provider secrets stored only in CI secret store or local `.env`, never committed.
3. Record test checklists, not real HTTP fixtures containing tokens.
4. Live tests must be skipped by default and require explicit environment variables such as `TOOLMUX_LIVE_TESTS=1`.

Security tests:

1. Secret scanner in CI.
2. Log redaction tests for `Authorization`, `access_token`, `refresh_token`, `code`, and provider-specific token prefixes.
3. Static checks for accidental token fields in toolmuxd persistence types.
4. Replay tests for handoff sessions.
5. Policy bypass tests for aliases and nested subcommands, ensuring every executable command has a command spec.
6. toolmuxd persistence tests that fail if token-shaped fields are written to files, databases, telemetry, or logs.

Lint and quality gates:

1. Formatting: `gofmt`, `go fmt`, Markdown lint, YAML lint.
2. Correctness: `go vet`, Dockerfile-based static analysis, race tests.
3. Security: Dockerfile-based vulnerability, security, and secret scanning.
4. Compatibility: cross-platform build checks for macOS, Linux, and Windows.
5. Release hygiene: SBOM generation, signed artifacts, checksums, provenance.
6. Git hygiene: Conventional Commits with subject lines at or below 50 characters and body lines wrapped at 72 characters.

## Release Plan

1. Publish alpha builds for contributors with Notion first.
2. Add Jira and Google Workspace commands behind explicit beta labels.
3. Add Slack user mode after PKCE app configuration is validated.
4. Publish signed `toolmux` binaries and Homebrew tap updates.
5. Publish generic `toolmuxd` container images.
6. Sign every binary, checksum file, and server image.
7. Publish SBOM and provenance with releases.
8. Document provider app review status and known scope limitations per release.

## Operational Notes

Hosted toolmuxd is required for a zero-manual-key experience for Notion and Jira. Self-hosters can run toolmuxd, but they must create their own provider OAuth apps and supply client secrets. The server code can be open source; provider client secrets cannot.

toolmuxd should be deployable without Postgres for the initial release. A single-node in-memory handoff store is sufficient for MVP and local development. Redis or another shared handoff store should wait until we have a separate threat model and operational reason for multi-instance handoff storage.

Toolmux's production AWS/Lambda deployment should live outside this OSS repo.
The private infrastructure repo should consume public `toolmuxd` release images
or binaries and adapt them for Lambda, API Gateway, Function URLs, Secrets
Manager, DNS, and monitoring.

## Initial Build Order

Recommended exact order:

1. M0 repo/tooling.
2. M1 CLI core and credentials.
3. M2 toolmuxd and Notion.
4. M3 Jira.
5. M4 Google Docs/Drive/Gmail.
6. M5 Slack user mode.
7. M6 hardening and beta.

This order proves the hardest foundation early: toolmuxd-backed local-custody
OAuth.
