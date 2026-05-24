# AGENTS.md

This file defines repository expectations for coding agents working on Toolmux.
Keep it current whenever build, test, style, security, release, or workflow
requirements change.

## Maintenance

Agents must update this file when they:

1. Change the supported Go version or toolchain setup.
2. Add, remove, or rename important `make` targets or CI checks.
3. Add a new provider, auth mode, policy behavior, or test class.
4. Add, remove, rename, or change a supported native toolbox, remote MCP
   catalog entry, workflow template, or supported agent.
5. Change commit, release, linting, formatting, or security expectations.
6. Change the public OSS/private infrastructure repository boundary.
7. Change CLI output behavior, terminal UX, or machine-readable schemas.
8. Change public user behavior that should be reflected in `README.md`.
9. Change developer workflow that should be reflected in `CONTRIBUTING.md`.

Agents must keep `README.md`, `CONTRIBUTING.md`, and this file in sync:

1. `README.md` is for users. Update it for user-visible commands, provider
   status, supported toolboxes and tools, workflow templates, supported
   agents, auth flows, output modes, install instructions, and safety notes.
2. `CONTRIBUTING.md` is for developers. Update it for local setup, test/lint
   workflow, provider integration rules, PR expectations, and release tooling.
3. `AGENTS.md` is for coding agents. Update it for repository expectations
   that agents must follow while editing code or docs.
4. Do not add or rename commands, flags, providers, auth behavior, policy
   behavior, or quality gates without checking whether all three docs need an
   update.

When updating Go guidance, check the official Go release notes and release
history first:

1. https://go.dev/doc/go1.26
2. https://go.dev/doc/devel/release

## Go Version

Use the latest stable Go toolchain. As of 2026-05-07, `govulncheck` reports
Go 1.26.3 as the security-fix patch release for the Go 1.26 line.

Repository expectations:

1. Set new modules to `go 1.26`.
2. Pin CI to the latest Go 1.26 patch, then bump promptly when Go ships a new
   supported patch release.
3. Prefer modern standard-library APIs and idioms from current Go releases.
4. Do not enable experimental `GOEXPERIMENT` features in production builds
   unless the behavior is documented and intentionally gated.

Notable Go 1.26 features to keep in mind:

1. `new(expr)` can allocate and initialize pointer fields in one expression.
2. Generic constraints may self-reference when that simplifies type-safe APIs.
3. `go fix` now includes modernizers; run it deliberately during upgrades.
4. The Green Tea garbage collector is enabled by default.
5. Heap base randomization improves security on 64-bit platforms.
6. Prefer current standard-library APIs before adding dependencies.
7. `errors.AsType` can simplify type-safe error extraction.
8. `testing.T.ArtifactDir` can store integration-test artifacts.
9. `testing/cryptotest.SetGlobalRandom` supports deterministic crypto tests.
10. `testing.B.Loop` should be used for new benchmarks.

Notable Go 1.25 features still relevant to this codebase:

1. `testing/synctest` is available for deterministic concurrent tests.
2. `net/http.CrossOriginProtection` can help protect daemon browser endpoints.
3. `go vet` includes `waitgroup` and `hostport` analyzers.
4. `log/slog` includes newer helpers such as `GroupAttrs` and source support.

## Quality Gates

The codebase should have a strict quality setup from the first implementation
milestone. Expected local targets:

```bash
make fmt
make fmt-check
make help
make lint
make test
make test-race
make test-integration
make test-live
make build
make dev-cli
make build-toolmuxd-image
make coverage
make commitlint
make install-hooks
make dev-server-tunnel
```

`make lint` is Dockerfile-based and should not require contributors to install
`staticcheck`, `golangci-lint`, `govulncheck`, `gosec`, `gitleaks`,
`actionlint`, or `yamllint` on the host. It must enforce the configured
`golangci-lint` formatters, including `gci` import grouping as standard
library, third-party, then `github.com/fiam/toolmux` packages. Keep linter
versions pinned in the root `Dockerfile`.

`make test-live` must be skipped by default and require explicit environment
variables such as `TOOLMUX_LIVE_TESTS=1`.

Make-based builds compile `toolmux` with `CGO_ENABLED=1` and `toolmuxd` with
`CGO_ENABLED=0` by default through `TOOLMUX_CGO_ENABLED` and
`TOOLMUXD_CGO_ENABLED`. PR CI should use those Make defaults instead of
duplicating cgo settings in workflow environment variables. Keep C compiler and
header dependencies available for CLI build jobs whenever `toolmux` cgo
dependencies change. Keep `toolmuxd` and its container image pure-Go unless
the daemon's product requirements intentionally change.

`make dev-cli` builds `./bin/toolmux` for local interactive testing. On macOS,
set `CODESIGN_IDENTITY` to a stable local signing identity so the target signs
the binary after every rebuild and Keychain "Always Allow" decisions can persist
across development builds.

CI should run at least:

1. `golangci-lint fmt --diff` or an equivalent format check covering `gofmt`,
   `goimports`, and `gci`.
2. `go vet ./...`.
3. Dockerfile-based `make lint`, including `staticcheck`, `golangci-lint`,
   `gocyclo`, `modernize`, `paralleltest`, `govulncheck`, `gosec`, `gitleaks`,
   `actionlint`, and repository-wide YAML linting.
4. `go test ./...`.
5. `go test -race ./...`.
6. Deterministic fake-upstream integration tests.
7. Markdown, YAML, and shell-script linting where applicable.
8. Commit-message validation.
9. Generic `toolmuxd` container image build validation.
10. Coverage generation with `make coverage`.
11. GoReleaser snapshot release validation for the CLI archive matrix on
    macOS, covering cgo-enabled Darwin artifacts and no-cgo Linux/Windows
    artifacts.
12. A non-publishing release dry run against latest `main` through
    `goreleaser release --snapshot --clean --skip=ko`, plus Linux validation
    of the generic `toolmuxd` container image.

## Releases

Release automation uses release-please and GoReleaser.

1. `release-please-config.json` and `.release-please-manifest.json` control
   release PRs, changelog generation, GitHub releases, and tags.
2. `.goreleaser.yaml` controls CLI archives for `toolmux` and the Ko-built
   `toolmuxd` container image.
3. PR, local Make builds, and GoReleaser releases use split cgo settings:
   `toolmux` is cgo-enabled where native platform integrations require it,
   Darwin release artifacts are built on macOS with cgo, Linux/Windows CLI
   artifacts are built without cgo, and `toolmuxd` remains pure-Go with
   `CGO_ENABLED=0`.
4. `toolmux` release archives must cover macOS, Linux, and Windows on amd64
   and arm64 unless release support is intentionally changed and documented.
5. `toolmuxd` must not be released as a binary archive. Release it only as a
   Linux amd64/arm64 image at `ghcr.io/fiam/toolmuxd:<tag>`.
6. The release workflow publishes a Homebrew cask to `fiam/homebrew-tap`.
7. The release dry-run workflow must stay read-only, check out latest `main`,
   skip Ko publishing in the GoReleaser snapshot, and must not log in to GHCR,
   publish GitHub release artifacts, or update Homebrew.
8. Keep the Homebrew cask binary stanza aligned with released binary names.
9. `HOMEBREW_TAP_GITHUB_TOKEN` must have contents write access to
   `fiam/homebrew-tap`.
10. Use `RELEASE_PLEASE_TOKEN` when release-please PRs need to trigger CI under
   branch protection; otherwise the workflow falls back to `GITHUB_TOKEN`.

## Integration Tests

Provider integrations must be tested against fake upstream servers in CI.
Do not rely on live SaaS providers as the main correctness signal.

Fake upstreams should emulate:

1. OAuth success, denial, callback errors, expired state, and PKCE failures.
2. Token refresh, refresh rotation, revocation, and missing scopes.
3. Provider API success, pagination, permission denied, rate limits, malformed
   responses, empty responses, 5xx errors, and retries.
4. Slack, Linear, and other behavior needed by implemented native commands.
5. Remote MCP OAuth protected-resource metadata, authorization-server metadata,
   dynamic client registration, loopback callbacks, resource parameters, PKCE,
   and refresh behavior.
6. toolmuxd local-custody handoff without storing plaintext provider tokens.

Live-provider tests may exist for smoke coverage, but they must be opt-in,
isolated from default CI, and must never record real tokens in fixtures.

All Go tests should call `t.Parallel()` unless there is a specific, documented
reason they cannot. Avoid process-global state in tests; inject dependencies
instead of using `t.Setenv`, `os.Setenv`, or working-directory changes in
parallel tests.

Provider integration tests that exercise the `toolmux` command surface should
live with the provider package, usually as external tests such as
`internal/providers/<provider>/client` package `client_test`. Use
`internal/testutil/toolmuxtest` for command execution helpers instead of
creating provider-specific `runToolmux` wrappers.

Slack native-provider tests must exercise all supported auth models against
fake upstreams: explicit token+cookie storage, user-owned OAuth, brokered
OAuth through `toolmuxd`, token refresh, and representative Web API commands.
Do not use live Slack workspaces as the default correctness signal.

Google native-provider tests must exercise the preferred unified `google`
namespace and the `google drive` command group against fake upstreams while
sharing one local `google` OAuth credential bundle. Keep Google auth on
Google's non-sensitive `drive.file` scope unless product requirements
explicitly justify broader access. Cover brokered OAuth through `toolmuxd`,
toolmuxd token exchange, local missing-scope failures before Google API calls,
refresh-token preservation, and representative Drive API commands. Cover
`toolmux google drive selected add/list/remove`,
`toolmux google drive files copy`, `toolmux google drive pick`, and
`toolmux google drive available` through fake brokered Picker flows without
using live Google. The brokered Picker flow must use Google's
`trigger_onepick=true` flow, request only `drive.file`, and keep hosted Google
client secrets and Picker configuration out of CLI output. Do not add a local
Google Picker fallback unless product requirements explicitly change.

Tests that need a real toolmuxd instance should use
`internal/testutil/toolmuxdtest` instead of constructing `server.NewHandler`
or `httptest.Server` directly. Provider-specific fake upstream behavior should
stay with the provider test fixtures.

## CLI Output

Toolmux has one command surface for humans and agents. Do not add a separate
provider-specific TUI, and do not let provider commands hand-roll ANSI styles,
pagers, prompts, or table layouts.
Native provider behavior must be exposed through provider-owned action specs so
the CLI and MCP surfaces stay aligned. Keep provider handlers in provider
client packages; the CLI root may only handle policy for provider tool
execution, context construction, invocation, and shared rendering.

Provider commands must return structured results and route all presentation
through `internal/output`. Human table output may use shared styles, color,
hyperlinks, markdown rendering, compact tables, progress spinners, and pagers
when stdout is a TTY. Provider-owned long-running work should report status
through `actions.ProgressReporter` instead of printing terminal UI directly.
JSON/YAML output must stay stable and undecorated: no ANSI escape sequences, no
pagers, no prompts, no progress animation, and no browser opens.

MCP support is exposed through `toolmux mcp serve` over stdio. The MCP server
must write only valid JSON-RPC messages to stdout; diagnostics belong on
stderr. Native MCP tools must be generated from provider-owned `actions.Spec`
metadata and must run the same policy and `--read-only` checks before provider
credentials are read. Native provider tools should be listed only when that
provider has stored auth for the active Toolmux profile. Do not add separate
MCP-only provider command trees for native providers.

`toolmux mcp configure` manages supported agent CLIs: Codex, Claude Code, and
Gemini CLI. With no agent argument it autodetects installed supported CLIs; with
arguments it accepts known agent names and aliases such as `claude-code` and
`gemini-cli`. Interactive no-argument runs must show a checkbox selector and
preselect agents where Toolmux MCP is configured and enabled. The selector must
show how each existing Toolmux MCP server is configured, and unchecking a
configured agent must remove the Toolmux MCP server from that agent. It
configures agents to launch `toolmux mcp serve`. Use `toolmux mcp enable` and
`toolmux mcp disable` for non-interactive agent setup and teardown. Keep common
agent scope behavior consistent across agents where their CLIs support the same
scope.

MCP tool profiles are non-secret configuration under the general Toolmux config
`mcp` key. Global config lives at `~/.toolmux/config.yaml`; project config
lives in `.toolmux/config.yaml`. Inspect, initialize, and edit both with
`toolmux config`; manage profile entries through `toolmux mcp profile`.
Project config overrides global config for matching profile names and default
profile selection, similar to Git config layering. The root `toolmux config`
command is a CLI-only management surface and must never be listed as an MCP
tool.
Profiles select tools with shell-style globs (`--tool`, `--exclude-tool`) and
regular expressions (`--tool-regex`, `--exclude-tool-regex`). Keep profile docs
and tests in sync when changing selection behavior.

Workflow definitions are non-secret YAML. Global workflows live under
`~/.toolmux/workflows`; project workflows live under `.toolmux/workflows`.
Template catalog entries must point at YAML files committed under `workflows/`
and loaded from GitHub, not hardcoded as Go workflow structs. Workflow prompts
are inline Go `text/template` strings. Missing template inputs without defaults
must fail clearly. A workflow can declare an agent, but `workflow run` must also
support `--agent` and `workflows.default_agent`; if no agent is available,
interactive runs should prompt for a detected or configured local agent and
non-interactive runs must fail. `toolmux workflow config set default-agent`
with no agent argument should open the same selector interactively. Agent
command definitions may include `{{ .prompt }}`; otherwise Toolmux appends the
rendered prompt as an extra argument. Workflows may declare required toolboxes
with compact values such as `internal:slack`, `catalog:linear`, or a remote MCP
URL, and missing requirements should be added automatically during
`workflow init` and `workflow run` unless `--no-setup` is passed.

Imported MCP servers are also managed under the general Toolmux `mcp`
config key through `toolmux mcp`. Server definitions and cached
`tools/list` metadata are non-secret; bearer tokens, OAuth tokens, refresh
tokens, dynamic client secrets, manually supplied client secrets, and auth codes
must live only in the credential store or transient process memory. Remote tool
commands are generated from
cached remote metadata under the registered server name, and they must run
policy and `--read-only` checks before stored auth is read or remote HTTP calls
are made. Remote server default arguments are non-secret config, must apply
only to matching top-level tool schema properties, and must be overridden by
explicit `--json` or flag arguments. MCP config write commands must default to
global config; use `--project` only for explicit project-local writes. Server
config should record `auth_required` after sync or auth setup when the
requirement is known. Streamable HTTP clients must handle JSON and
`text/event-stream` responses and preserve `Mcp-Session-Id` headers for
sessionful remote servers. Remote MCP `tools/call` response inactivity timeout
defaults to 60 seconds and is controlled by the top-level
`--mcp-tool-call-timeout` flag for both CLI remote commands and
`toolmux mcp serve`.
Root MCP management commands such as `mcp ls`, `mcp show`, `mcp sync`,
`mcp auth`, `mcp defaults`, and `mcp schema` are CLI maintenance surfaces and
do not need policy metadata because they are not tools.
Command-backed MCP servers use the `stdio` transport and are added with
`toolmux add <command> [args...]` when the input is not a URL, catalog entry,
or native toolbox. Use `--name` to override the derived namespace, `--stdio` or
`--transport stdio` only to disambiguate a command name that matches a catalog
or native toolbox, and `--` before command-owned flags. Stdio command
definitions are non-secret config, inherit the Toolmux process environment, do
not use Toolmux-managed MCP OAuth or bearer tokens. Treat stdio tool calls as
both remote-write and local-write for policy because the configured command can
touch local
files, caches, containers, browsers, or network services.
`toolmux mcp auth login` must use MCP protected-resource metadata discovery,
authorization-server metadata, PKCE, the OAuth `resource` parameter, and dynamic
client registration when advertised; keep `--client-id` available for servers
without registration. Top-level `toolmux add` registers remote MCP toolboxes
from a built-in catalog name or MCP URL and syncs tools by default. When the
first sync returns an auth-required response and no auth is stored, it must
start MCP OAuth, store auth, retry sync, and only then write the server config.
Failed or cancelled OAuth must not leave a registered server behind. Keep
`--no-sync` available for registration without auth or sync. Custom URL adds
must use `toolmux add <url>` with `--name` when the derived name is not desired
or would collide. Top-level `toolmux remove` and `rm` remove registered
toolboxes and must delete stored auth for removed remote MCP server names in
the active Toolmux profile. `toolmux add`, `toolmux mcp sync`, and
remote tool commands must support `-v`/`--verbose` redacted HTTP tracing for
debugging. `toolmux mcp auth remove` must still delete matching stored auth
after the server entry has already been removed.
Stale remote caches should refresh
opportunistically after about 24 hours without making existing cached
commands unusable when a refresh attempt fails. `toolmux list` must list
all built-in toolboxes, include a toolbox type column, and support `--mcp` and
`--internal` filters. MCP catalog entries must be listed whether or not they
are registered, support scriptable `--enable`/`--disable`, and provide
interactive `--manage` checkbox toggling for built-ins. Catalog
enablement must allow `--enable <catalog-name>=<registered-name>` so built-ins
can be registered under a non-conflicting command namespace.
Add remote MCP catalog entries only for documented hosted Streamable HTTP MCP
endpoints that can be added and authenticated through the server's own OAuth
flow without users creating their own OAuth app first. Keep built-in remote MCP
catalog data in `internal/cli/mcp_remote_catalog.yaml`, include a
`display_name` for every entry, and keep the user-facing catalog summary in
`README.md` current.
`toolmux mcp ls` must use shared table styling for human output, display only
`project` or `global` scope labels, support `mcp ls <name>` for one server's
cached tools, and support `mcp ls -R` for a tree of all registered servers and
their cached tools. Running a registered remote namespace such as
`toolmux linear` without a tool must show command help with available cached
tools. Interactive human output must compact remote MCP tool descriptions and
may use shared color tones for command names, arguments, and secondary text.
Keep full upstream descriptions available through non-interactive output,
JSON/YAML, `toolmux <server> --full-help`, and the `--full-descriptions` flag
on `toolmux mcp ls`.

Use `charm.land/glamour/v2` for terminal Markdown rendering. Render Markdown
only for interactive human table output; keep non-TTY, JSON, and YAML output
plain and stable for agents.

Toolbox status is owned by the root `status [toolbox...]` command, which should
report registered toolbox state, backend kind, stored auth type, tool count,
scope, and source URL when available. Do not add provider-specific
`status` subcommands. The root status command is CLI-only management and does
not need policy metadata.

Diagnostics are owned by the root `doctor` command. Do not add
provider-specific `doctor` subcommands. `doctor` should run active core and
remote MCP diagnostics with actionable remediation as CLI-only management.

When adding or changing a provider, update the PRD or implementation docs if the
provider needs new output fields, error fields, aliases, shell completions,
human table columns, or policy metadata.

Prefer imported remote MCP servers over native provider integrations when a
provider already exposes an adequate MCP server. Add native integrations only
for providers or workflows that do not have a usable MCP path, or when a native
surface is explicitly justified by product requirements.

Do not implement browser credential harvesting, cookie extraction, session-token
scraping, or provider-policy bypasses. The only current exception is Slack's
explicit browser-session setup through `toolmux add slack --workspace` or
`--from-browser`, which must validate with `auth.test` before storing
credentials. If Toolmux needs to support a local or self-hosted MCP server that
accepts tokens, require explicit user-supplied credentials through the OS
credential store, `mcp auth set`, OAuth, or the server's own documented setup
flow.

## Hosted Broker

The CLI defaults to `https://api.toolmux.com` for brokered OAuth flows. Use
`TOOLMUX_TOOLMUXD_URL` for local development and self-hosted deployments. Do not
add provider-specific server URL flags unless the product contract changes.

`toolmuxd` exposes unauthenticated operational endpoints for deployment checks:
`GET /healthz` returns JSON health status, and `GET /build` returns Toolmux
server daemon build metadata as JSON by default or plaintext when requested
with `Accept: text/plain` or `?format=text`. Do not include secrets,
environment values, provider configuration, tokens, or deployment-specific
infrastructure details in these responses.

## Local Provider Harnesses

`make dev-server-tunnel` starts the local server and exposes it through
Cloudflare Tunnel for OAuth callback testing. It uses a temporary Quick Tunnel
by default. Set `TOOLMUX_TUNNEL_HOSTNAME` to use a stable Cloudflare account
tunnel, either with a locally-managed tunnel name or a dashboard-managed tunnel
token. It writes local, ignored environment hints to
`.toolmux/server-tunnel.env`.

Do not commit tunnel URLs, Cloudflare tunnel tokens, provider client secrets,
OAuth codes, or generated token material.

## Security

Provider tokens, auth codes, refresh tokens, one-time handoff secrets, and
`Authorization` headers must never appear in logs, fixtures, command output,
crash reports, telemetry, or committed files.

Browser cookies, local browser databases, workspace session tokens, and
provider web-app bearer tokens are also credential material. Do not read,
extract, transform, sync, print, or store them unless the provider-specific
flow is explicitly supported by `toolmux add` and requested by the user.
Slack browser-session auth must stay confined to the `internal/slackauth`
package and the `toolmux add slack` flow. Slack auth setup and legacy
credential migration must use `auth.test` to validate credentials and store the
returned workspace URL for workspace-specific API calls.
Slack tools that use undocumented web-session endpoints must be read-only,
prefixed with `experimental_`, documented as experimental in CLI and MCP
descriptions, and tested against fake upstreams. Prefer documented Slack Web
API methods when they work.
Expose Slack identity through provider-owned read-only actions such as
`slack.auth_test`; do not add special self-DM shortcuts when existing Slack
message commands can address a user or configured conversation.

Policy checks must run before tool credential reads, token refresh, or provider
API calls. Only executable tools need policy metadata: native provider leaf
actions and synthetic remote MCP tool commands. For provider commands, add
data-driven action specs with both `remote_effect` and `local_effect`; do not
register placeholder specs for providers that are not implemented yet.

Remote MCP management commands do not need policy metadata. Synthetic remote
MCP tool commands do need policy metadata. Imported remote MCP server names
must not collide with native top-level commands or aliases. If a newly added
native command collides with an existing imported MCP server, startup must fail
with an actionable error that prints `toolmux mcp rename <old-name> <new-name>`.
Synthetic remote MCP tool commands must generate flags for representable
top-level input-schema properties, keep help focused on command usage, expose
full schemas through the `toolmux mcp schema` command, and provide
`-v`/`--verbose` HTTP tracing on stderr with authorization and cookie headers
redacted.

Provider command paths, argument constraints, flags, group help, aliases, and
leaf help must come from a provider-owned `actions.Spec` tree. Use the same
type for group nodes and leaf actions, and let upper layers walk the tree
instead of maintaining a parallel group model. Do not hardcode provider command
trees or provider command flags in the Cobra root layer. Root management
surfaces such as `add`, `remove`, `rm`, `list`, `status`, `doctor`, `config`,
`policy`, `workflow`, and `mcp` are code-driven CLI-only command surfaces.
Use `actions.Short` for compact command listings and `actions.Description` for
detailed long help and MCP `tools/list` descriptions. Keep provider
descriptions concrete enough for agents to understand identifiers, timestamp
formats, pagination, write effects, and provider-specific restrictions.

Provider command behavior must also live with the provider's client package, not
in `internal/cli`. Register provider-owned `actions.Handler` functions through
the provider catalog, return structured results, and implement shared
renderable interfaces from `internal/actions` when human table output needs
tables, Markdown, text, browser opens, or follow-up interactions. The Cobra
layer may walk metadata, evaluate policy for tool execution, invoke handlers,
and render shared results; it must not contain provider-specific command
implementations.

Provider facets self-register. Use `internal/providers/<provider>/client` for
CLI/API/MCP action metadata, handlers, diagnostics, and API clients; use
`internal/providers/<provider>/broker` for toolmuxd OAuth/token broker support.
Facet packages should expose `Descriptor()` or equivalent static constructors
and call registry functions from `init()`. Keep `init()` limited to static
registration: no env reads, filesystem access, network calls, goroutines,
credentials, or logging. Add client providers to `internal/providers/all` and
broker providers to `internal/providers/brokers/all`; binaries and test
harnesses import the appropriate bundle for side effects.
If separate native command namespaces share one provider OAuth grant, set the
provider registry's shared credential provider and keep scope merging/checking
inside the provider client package, not in `internal/cli`.

Broker facets register through `internal/providers/brokers`. Keep
`internal/server` generic: it may use broker descriptors and OAuth interfaces,
but it must not import provider client packages.

## Repository Boundary

This OSS repo contains portable product source and generic artifacts:

1. `toolmux` CLI source.
2. `toolmuxd` server daemon source.
3. Generic `toolmuxd` container build files.
4. Fake-provider tests and public self-hosting docs.

This repo must not contain Toolmux's hosted deployment infrastructure:

1. AWS Lambda, API Gateway, ECR, DNS, or certificate definitions.
2. Terraform, Pulumi, CDK, or deployment state for Toolmux production.
3. Provider OAuth client secrets.
4. Production monitoring, abuse-control, billing, or alerting internals.

Hosted Toolmux deployment work belongs in a private infrastructure repo. Keep
public docs generic enough for self-hosters who bring their own provider OAuth
apps and secrets.

## Commits

Use Conventional Commits:

```text
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

Allowed common types include `feat`, `fix`, `docs`, `test`, `refactor`,
`perf`, `build`, `ci`, `chore`, and `revert`.

Commit message rules:

1. Keep the subject line at or below 72 characters.
2. Wrap body lines at 72 characters.
3. Use a blank line between the subject and body.
4. Explain why in the body when the change is not obvious.
5. Use `!` or a `BREAKING CHANGE:` footer for breaking changes.

Run `make install-hooks` in local clones to install the repository
`commit-msg` and `pre-push` hooks. The hooks reuse the same commit-message
validation script as CI.

Examples:

```text
feat(policy): add command catalog

Add command metadata so local policy checks can run before provider
credentials are loaded from the OS credential store.
```
