# Contributing

Thanks for helping build Toolmux. This guide is for developers working on the
CLI, `toolmuxd`, provider integrations, docs, tests, and release tooling.

## Requirements

Install:

1. Go 1.26.3 or newer on the Go 1.26 line.
2. A C compiler such as `clang` or `gcc`, for cgo-enabled `toolmux` builds.
3. Docker, for the full Dockerfile-based linter pass.
4. `make`.
5. `cloudflared`, only if you are testing browser OAuth callbacks locally.

On macOS, local Keychain testing is easier if you build a stable signed binary:

```bash
CODESIGN_IDENTITY="Apple Development:" make dev-cli
```

Any stable local or Apple Developer signing identity can work. The important
part is that the rebuilt `bin/toolmux` keeps the same code-signing identity so
Keychain trust prompts can persist across builds.

## Setup

```bash
git clone https://github.com/fiam/toolmux.git
cd toolmux
make dev-cli
./bin/toolmux version
```

Run the default test suite:

```bash
make test
```

Run the full local quality pass:

```bash
make fmt-check
make vet
make test
make lint
```

`make lint` builds the `lint` target from the root `Dockerfile`. Contributors
do not need to install `staticcheck`, `golangci-lint`, `govulncheck`,
`gosec`, `gitleaks`, `actionlint`, or `yamllint` on the host. The Docker lint
pass also checks Go formatting and import order through `golangci-lint fmt`,
and enforces cyclomatic-complexity checks through `gocyclo`.

GitHub Actions runs these checks for pull requests and pushes to `main`, plus
race tests, fake-upstream integration tests, coverage generation, binary
builds, commit-message validation, and the generic `toolmuxd` container image
build. CI also runs a macOS GoReleaser snapshot release so the cgo-enabled
Darwin CLI artifacts and no-cgo Linux/Windows CLI artifacts are validated
before a release.
PR and Make-based local builds use the Make defaults: `toolmux` is compiled
with `CGO_ENABLED=1`, and `toolmuxd` is compiled with `CGO_ENABLED=0`.
The `release dry run` workflow also runs weekly and can be triggered manually;
it checks out latest `main`, builds native `toolmux` with cgo and `toolmuxd`
without cgo, runs the GoReleaser CLI snapshot path without publishing
artifacts or Homebrew updates, and validates the generic `toolmuxd` container
image separately on Linux.
Live-provider tests stay opt-in and are not part of default CI.

Imported remote MCP servers remain the preferred path when a provider already
ships an adequate MCP server. Do not add native provider registration stubs;
native providers should register only when their provider-owned specs,
handlers, fake upstreams, and tests are ready.

Do not add browser credential harvesting, cookie extraction, session-token
scraping, or provider-policy bypasses to make an MCP server easier to use. The
only current exception is Slack's explicit browser-session setup through
`toolmux add slack --workspace` or `--from-browser`, which must validate with
`auth.test` before storing credentials. For local or self-hosted MCP servers,
prefer OAuth, documented provider tokens, or explicit `toolmux mcp auth set`
flows that store credentials in the OS credential store.

MCP support is served by the CLI over stdio. Use `toolmux mcp serve` for
manual protocol testing and `toolmux mcp configure` to register the server with
Codex, Claude Code, or Gemini CLI. Interactive no-argument configuration shows
detected agents as checkboxes, shows how the target MCP server is currently
configured, preselects agents where Toolmux MCP is enabled, and removes the
Toolmux MCP server from agents that are unchecked. Use `toolmux mcp enable`
and `toolmux mcp disable` for non-interactive agent setup and teardown. MCP
tool profiles are stored in the general Toolmux config under the `mcp` key.
Global config is `~/.toolmux/config.yaml`; project config is
`.toolmux/config.yaml`. Inspect, initialize, and edit both with
`toolmux config`; manage profile entries with `toolmux mcp profile`.
Project config overrides global config for matching profile names and default
profile selection. Native and remote toolbox registrations live under the
top-level `toolboxes` key. The registered toolbox name is both the command
namespace and the credential account identity, so providers can be registered
multiple times with `--name`.

Workflow definitions are non-secret YAML. Global workflows live under
`~/.toolmux/workflows`; project workflows live under `.toolmux/workflows`.
Templates listed by `toolmux workflow templates` must be committed as YAML
files under `workflows/` and loaded through GitHub template sources, not
hardcoded in Go. Workflow prompts are inline Go `text/template` strings.
Workflows may declare required toolboxes with compact values such as
`internal:slack`, `catalog:linear`, or a remote MCP URL. Missing requirements
should be added automatically during `workflow init` and `workflow run` unless
the caller passes `--no-setup`. If a workflow run has no selected agent,
interactive sessions should prompt for a configured or detected local agent;
non-interactive sessions should fail. The no-agent form of
`toolmux workflow config set default-agent` should also support an interactive
selector.

Imported MCP servers are stored as `toolboxes` entries, with non-secret server
definitions in config and cached tool metadata in the user cache directory.
Use top-level `toolmux add` to register native toolboxes, remote MCP toolboxes
from a catalog name or URL, or command-backed stdio toolboxes with
`toolmux add <command> [args...]`; use `--name` to choose the registered
namespace/account, `--stdio` only to disambiguate a command name that matches a
catalog or native toolbox, and `--` before command-owned flags; use
`toolmux mcp sync`,
`toolmux mcp rename`, `toolmux mcp ls`, `toolmux mcp show`,
`toolmux list`, and `toolmux mcp defaults` for MCP-specific server
maintenance. Default arguments are non-secret config values applied only
to remote tool schemas with matching top-level properties; explicit tool
arguments override configured defaults. MCP config write commands default to
the global config; require `--project` for project-local writes. Server config
should record `auth_required` after sync or auth setup when the requirement is
known. Native toolbox help and MCP `tools/list` output should include only
native providers registered in the merged Toolmux config. Stdio MCP
toolboxes do not use Toolmux-managed MCP OAuth or bearer-token
auth; configure auth through the command environment or command arguments. Use
`toolmux mcp auth login` for MCP OAuth with PKCE and dynamic client
registration, and `toolmux mcp auth set` for externally issued bearer tokens.
`toolmux remove` and its `rm` alias should accept one or more toolbox names.
Removing a remote MCP toolbox should also delete stored auth for that server
name in the active Toolmux profile.
`toolmux mcp auth remove` should still delete matching stored auth when the
server entry has already been removed.
`toolmux mcp ls` should use the shared table renderer for human output,
display only `project` or `global` scope labels, support `mcp ls <name>` for
cached tools on one server, and support `mcp ls -R` for a tree of registered
servers and cached tools. Running a registered remote namespace such as
`toolmux linear` without a tool should show help with available cached tools.
Interactive human output should compact remote MCP tool descriptions and may
use shared color tones for command names, arguments, and secondary text. Keep
full upstream descriptions available through non-interactive output,
JSON/YAML, `toolmux <server> --full-help`, and the `--full-descriptions` flag
on `toolmux mcp ls`.
Do not store bearer tokens, OAuth tokens, refresh tokens, dynamic client
secrets, client secrets, auth codes, or authorization headers in YAML config or
test fixtures. Remote Streamable HTTP support must handle both JSON and
`text/event-stream` responses and preserve `Mcp-Session-Id` headers for
sessionful servers. `toolmux add` syncs MCP tools by default; when the first
sync gets an auth-required response and no auth is stored, it should start MCP
OAuth, store auth, retry sync, and only then write the server config. Failed or
cancelled OAuth must not leave a registered server behind. Keep `--no-sync`
working for users who want registration without auth or sync. Custom URL adds
must use `toolmux add <url>` with `--name` when the derived name is not desired
or would collide. OAuth tests should use fake upstreams for protected-resource
metadata, authorization-server metadata, dynamic registration, loopback
callbacks, PKCE, token exchange, and refresh. Stale caches should refresh
opportunistically after about 24 hours without breaking use of an existing cache
when refresh fails. Remote tool commands
should translate representable top-level input-schema properties into flags,
keep help focused on command usage, expose full schemas through the top-level
`toolmux mcp schema` command, and support `-v`/`--verbose` HTTP tracing with
credential headers redacted. `toolmux add` and `toolmux mcp sync` should
support the same redacted tracing for sync-time debugging.
`toolmux list` must list all built-in toolboxes, include a toolbox type
column, and support `--mcp` and `--internal` filters. MCP catalog entries must
be listed regardless of registration state and support scriptable
`--enable`/`--disable` plus interactive `--manage` toggling. Catalog
enablement must allow
`--enable <catalog-name>=<registered-name>` so built-ins can be registered
under a non-conflicting command namespace.
Add remote MCP catalog entries only for documented hosted Streamable HTTP MCP
endpoints that can be added and authenticated through the server's own OAuth
flow without users creating their own OAuth app first. Keep built-in remote MCP
catalog data in `internal/cli/mcp_remote_catalog.yaml`, include a
`display_name` for every entry, and keep the user-facing catalog summary in
`README.md` current.

## Common Targets

```bash
make help
make dev-cli
make build
make build-toolmuxd-image
make fmt
make fmt-check
make vet
make test
make test-race
make test-integration
make test-live
make lint
make coverage
make commitlint
make install-hooks
make dev-server-tunnel
```

`make test-live` is opt-in. It must not run live provider tests unless
`TOOLMUX_LIVE_TESTS=1` and the required provider credentials are present.

## Releases

Releases are managed by release-please and GoReleaser.

1. Push conventional commits to `main`.
2. The `release` workflow opens or updates a release-please PR.
3. Merge the release PR to create the GitHub release and tag.
4. GoReleaser builds `toolmux` archives for macOS, Linux, and Windows on amd64
   and arm64. macOS CLI artifacts are cgo-enabled so Slack browser-session
   auth can call Cocoa/WKWebView.
5. GoReleaser publishes a Ko-built `toolmuxd` Linux image for amd64 and arm64
   to `ghcr.io/fiam/toolmuxd:<tag>`.
6. GoReleaser uploads CLI release artifacts and checksums to GitHub Releases.
7. GoReleaser publishes the `toolmux` Homebrew cask to
   `fiam/homebrew-tap`.

For a no-publish release rehearsal, run the `release dry run` workflow. It
checks out latest `main`, runs `goreleaser check`, builds native `toolmux` with
cgo and `toolmuxd` without cgo, and runs
`goreleaser release --snapshot --clean --skip=ko` with read-only repository
permissions. It does not log in to GHCR, require the Homebrew tap token, or
publish release artifacts.

PR, local Make builds, and GoReleaser releases use split cgo settings:
`toolmux` is cgo-enabled where native platform integrations require it,
Darwin release artifacts are built on macOS with cgo, Linux/Windows CLI
artifacts are built without cgo, and `toolmuxd` remains pure-Go with
`CGO_ENABLED=0`.

Required repository secrets:

1. `HOMEBREW_TAP_GITHUB_TOKEN`: token with contents write access to
   `fiam/homebrew-tap`.
2. `RELEASE_PLEASE_TOKEN`: optional token for release-please PRs. Configure it
   when release PRs need to trigger CI under branch protection; otherwise the
   workflow falls back to `GITHUB_TOKEN`.

## Development Workflow

1. Keep changes narrowly scoped.
2. Prefer existing package patterns over new abstractions.
3. Add or update provider action metadata before exposing a tool.
4. Run policy checks before tool token reads or provider API calls.
5. Keep human output in `internal/output`.
6. Keep JSON/YAML output stable and free of ANSI escapes.
7. Add fake-upstream tests for provider behavior.
8. Do not rely on live SaaS providers for default CI correctness.
9. Add `t.Parallel()` to Go tests unless a specific shared-state constraint
   prevents it.
10. Keep imports grouped as standard library, third-party packages, then
   `github.com/fiam/toolmux` packages.

Provider integration tests that exercise real `toolmux` commands should live
with the provider package and use `internal/testutil/toolmuxtest` for shared
command-running helpers.

Tests that need toolmuxd should use `internal/testutil/toolmuxdtest`, with
provider-specific fake upstream behavior kept in provider fixtures.

Provider commands should be useful for both humans and agents. If a command
adds a prompt, browser open, pager, spinner, or selector, it must be gated on
interactive terminal use and must not affect JSON/YAML output.
Long-running provider work should report progress through the shared
`actions.ProgressReporter` surface instead of printing provider-owned terminal
UI directly.

MCP tools are generated from the same provider action specs as Cobra commands.
Use `actions.Short` for compact command lists and `actions.Description` for
detailed agent-facing MCP tool descriptions and long CLI help. Do not add
separate MCP-only provider command trees. If a provider action is not safe or
useful for agents, control exposure with MCP profiles or policy rather than
forking provider metadata.

Imported remote MCP tools are the exception: they are generated from cached
remote `tools/list` metadata and exposed under the registered server name.
Their synthetic action specs must still run policy and `--read-only` checks
before stored auth is read or remote HTTP calls are made.
Remote MCP `tools/call` response inactivity timeout defaults to 60 seconds and
is controlled by the top-level `--mcp-tool-call-timeout` flag for both CLI
remote commands and `toolmux mcp serve`.

## Documentation Expectations

Update docs in the same change when behavior changes:

1. Update `README.md` for user-visible commands, auth behavior, output modes,
   installation steps, or provider status.
2. Update `CONTRIBUTING.md` for developer workflow, tests, linting, release,
   local environment, or architecture expectations.
3. Update `AGENTS.md` when agent instructions, required checks, or repository
   conventions change.
4. Update `docs/PRD.md` or `docs/IMPLEMENTATION_PLAN.md` when product scope or
   planned provider behavior changes.
5. Update provider docs under `docs/providers/` when OAuth setup or live
   provider configuration changes.

Do not leave README examples pointing at commands that no longer exist.

## Provider Integrations

When adding or expanding a provider:

1. Add provider action specs with path, args, flags, provider, resource,
   action, remote effect, local effect, risk, and scopes.
2. Add client code that uses structured request and response types.
3. Avoid `map[string]any` for server/client JSON when a stable struct is
   practical.
4. Use the OS credential store through the shared credentials interface.
5. Keep provider tokens, auth codes, refresh tokens, and `Authorization`
   headers out of logs and fixtures.
6. Add fake-upstream behavior for success, pagination, malformed responses,
   permission errors, rate limits, and server failures.
7. Add integration tests that run without live provider credentials.
8. Keep live tests optional and skipped by default.

For commands that mutate or delete data, include `--dry-run` where useful and
require explicit confirmation for destructive or broad replacement actions.

Slack is the first native provider command set. Its client facet lives under
`internal/providers/slack/client`, browser-session extraction lives under
`internal/slackauth`, shared Slack HTTP/OAuth helpers live under
`internal/providers/slack/slackapi`, and the toolmuxd broker facet lives under
`internal/providers/slack/broker`. Slack tests must cover browser extraction
flag routing, direct token+cookie auth, user-owned OAuth, brokered OAuth, token
refresh, `toolmux add slack` flags, `--name`-based account identity, add-time
`auth.test` validation failures,
workspace URL enrichment, `toolmux remove slack`, and representative Web API
commands against fake upstream servers. Slack tools that use undocumented
web-session endpoints must be read-only, prefixed with `experimental_`,
documented as experimental in both CLI and MCP descriptions, and covered by
fake-upstream tests.

For Slack broker testing, configure fake or local upstream endpoints through
`brokers.Config` in tests instead of environment variables. For deployed
`toolmuxd`, use `SLACK_CLIENT_ID`, `SLACK_CLIENT_SECRET`, optional endpoint
overrides, and `SLACK_SCOPES`.

Google uses the native command namespace `google`, with a `drive` command
group. Google stores one local OAuth bundle per registered toolbox name under
the `google` credential provider. Its client facet lives under
`internal/providers/google/client`, and
shared Google REST/OAuth helpers live under
`internal/providers/google/googleapi`. Google tests must cover brokered OAuth
through `toolmuxd`, the default non-sensitive `drive.file`
scope, local scope checks before API calls, refresh-token preservation, and
representative Drive API commands against fake upstream servers. Google tests
must cover `toolmux google drive selected add/list/remove`,
`toolmux google drive files copy`, `toolmux google drive pick`, and
`toolmux google drive available` through fake brokered Picker flows without
using live Google. Brokered Picker tests must assert `trigger_onepick=true`, a
single `drive.file` scope, returned `picked_file_ids`, token exchange in
`toolmuxd`, and no CLI-side Picker API key. Configure Google broker credentials
on `toolmuxd`.

## Local OAuth Testing

For brokered OAuth flows, run a local server tunnel:

```bash
make dev-server-tunnel
```

For a stable Cloudflare hostname:

```bash
cloudflared tunnel login
cloudflared tunnel create toolmux-dev
cloudflared tunnel route dns toolmux-dev auth-dev.example.com

TOOLMUX_TUNNEL_HOSTNAME=auth-dev.example.com \
  TOOLMUX_TUNNEL_NAME=toolmux-dev \
  make dev-server-tunnel
```

Then point the CLI at that server:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth-dev.example.com
```

Never commit tunnel URLs, Cloudflare tunnel tokens, provider client secrets,
OAuth codes, provider tokens, `.env`, `.envrc`, or local credential material.

## Policy and RBAC

Only executable tools need policy metadata: native provider leaf actions and
synthetic remote MCP tool commands. Root management commands such as `config`,
`list`, `status`, `doctor`, `add`, `remove`, `workflow`, and MCP setup/auth
commands are ordinary CLI maintenance surfaces and should not appear in
`policy catalog`.

Policy must be evaluated before tool execution does any of the following:

1. Loading provider tokens from the credential store.
2. Refreshing tokens.
3. Calling provider APIs.

Use these commands while developing:

```bash
./bin/toolmux policy catalog
./bin/toolmux policy check --command "google drive available"
./bin/toolmux policy check --command "linear create_issue"
./bin/toolmux policy doctor
```

Provider command metadata is data-driven. Root `status [toolbox...]` reports
registered toolbox state and auth, while root `doctor` runs core Toolmux and
remote MCP diagnostics. Do not add provider-specific status or doctor
subcommands.

Provider command paths, args, flags, group help, aliases, and leaf help belong
in a provider-owned `actions.Spec` tree. Use one spec type for both groups and
leaf actions, then let the Cobra, MCP, REST, policy, and catalog layers walk
that tree instead of maintaining separate command models.

Provider command execution belongs with the provider too. Add client action
handlers under `internal/providers/<provider>/client`, expose them through the
provider catalog, and return typed results. The CLI root should only evaluate
policy for provider tool actions, construct an action context, invoke the
handler, and render results through shared `internal/actions` and
`internal/output` interfaces. Do not add provider-specific Cobra files under
`internal/cli`.

Provider facets self-register through package `init()` functions. Keep those
functions pure and static: no env reads, I/O, network calls, credentials,
goroutines, or logging. Add client provider facets to
`internal/providers/all`; add toolmuxd OAuth/token broker facets to
`internal/providers/brokers/all`. `toolmux`, `toolmuxd`, and test harnesses
should import only the bundle they need for side effects.

Server-side OAuth/token broker implementations should register descriptors in
`internal/providers/brokers` from `internal/providers/<provider>/broker`.
`internal/server` should depend on that broker registry, not on provider client
packages.

## Commit Messages

Use Conventional Commits:

```text
<type>[optional scope]: <description>

[optional body]
```

Rules enforced by `make commitlint`:

1. Subject line at or below 72 characters.
2. Body lines wrapped at 72 characters.
3. Blank line between subject and body.
4. Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`,
   `ci`, `chore`, and `revert`.

Run `make install-hooks` once in a local clone to configure repository Git
hooks. The `commit-msg` hook checks each new commit message, and the
`pre-push` hook checks the outgoing commit range before it reaches CI.

Example:

```text
feat(mcp): add remote tool filtering

Expose cached remote tool filters so agents can inspect a smaller command
surface without parsing human table output.
```

## Pull Request Checklist

Before opening a PR, check:

1. `make fmt-check`
2. `make vet`
3. `make test`
4. `make test-race`
5. `make test-integration`
6. `make build`
7. `make coverage`
8. GoReleaser snapshot build, through CI
9. `make build-toolmuxd-image`, when Docker is available
10. `make lint`, when Docker is available
11. `make commitlint`, after creating commits
12. README/CONTRIBUTING/AGENTS/docs updates for behavior changes

If you cannot run a check, call that out in the PR with the reason.

## Security

Do not commit secrets or generated credential material. If you accidentally
print or commit a token, revoke it immediately and tell maintainers.

Security-sensitive changes should preserve local token custody and should not
add durable server-side provider token storage without an explicit product and
threat-model update.
