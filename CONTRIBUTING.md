# Contributing

Thanks for helping build Supacli. This guide is for developers working on the
CLI, `supaclid`, provider integrations, docs, tests, and release tooling.

## Requirements

Install:

1. Go 1.26.3 or newer on the Go 1.26 line.
2. Docker, for the full Dockerfile-based linter pass.
3. `make`.
4. `cloudflared`, only if you are testing browser OAuth callbacks locally.

On macOS, local Keychain testing is easier if you build a stable signed binary:

```bash
CODESIGN_IDENTITY="Apple Development:" make dev-cli
```

Any stable local or Apple Developer signing identity can work. The important
part is that the rebuilt `bin/supacli` keeps the same code-signing identity so
Keychain trust prompts can persist across builds.

## Setup

```bash
git clone https://github.com/fiam/supacli.git
cd supacli
make dev-cli
./bin/supacli version
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
pass also checks Go formatting and import order through `golangci-lint fmt`.

GitHub Actions runs these checks for pull requests and pushes to `main`, plus
race tests, fake-upstream integration tests, coverage generation, binary
builds, commit-message validation, and the generic `supaclid` container image
build. CI also runs a GoReleaser snapshot release so the CLI archive matrix and
Ko-built `supaclid` image manifest are validated before a release.
Live-provider tests stay opt-in and are not part of default CI.

## Common Targets

```bash
make help
make dev-cli
make build
make build-supaclid-image
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
make dev-server-tunnel
```

`make test-live` is opt-in. It must not run live provider tests unless
`SUPACLI_LIVE_TESTS=1` and the required provider credentials are present.

## Releases

Releases are managed by release-please and GoReleaser.

1. Push conventional commits to `main`.
2. The `release` workflow opens or updates a release-please PR.
3. Merge the release PR to create the GitHub release and tag.
4. GoReleaser builds `supacli` archives for macOS, Linux, and Windows on amd64
   and arm64.
5. GoReleaser publishes a Ko-built `supaclid` Linux image for amd64 and arm64
   to `ghcr.io/fiam/supaclid:<tag>`.
6. GoReleaser uploads CLI release artifacts and checksums to GitHub Releases.
7. GoReleaser publishes the `supacli` Homebrew formula to
   `fiam/homebrew-tap`.

Required repository secrets:

1. `HOMEBREW_TAP_GITHUB_TOKEN`: token with contents write access to
   `fiam/homebrew-tap`.
2. `RELEASE_PLEASE_TOKEN`: optional token for release-please PRs. Configure it
   when release PRs need to trigger CI under branch protection; otherwise the
   workflow falls back to `GITHUB_TOKEN`.

## Development Workflow

1. Keep changes narrowly scoped.
2. Prefer existing package patterns over new abstractions.
3. Add or update policy metadata before a command can read credentials.
4. Run policy checks before token reads or provider API calls.
5. Keep human output in `internal/output`.
6. Keep JSON/YAML output stable and free of ANSI escapes.
7. Add fake-upstream tests for provider behavior.
8. Do not rely on live SaaS providers for default CI correctness.
9. Add `t.Parallel()` to Go tests unless a specific shared-state constraint
   prevents it.
10. Keep imports grouped as standard library, third-party packages, then
   `github.com/fiam/supacli` packages.

Provider integration tests that exercise real `supacli` commands should live
with the provider package and use `internal/testutil/supaclitest` for shared
command-running helpers.

Tests that need supaclid should use `internal/testutil/supaclidtest`, with
provider-specific fake upstream behavior kept in provider fixtures.

Provider commands should be useful for both humans and agents. If a command
adds a prompt, browser open, pager, spinner, or selector, it must be gated on
interactive terminal use and must not affect JSON/YAML output.

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

## Local OAuth Testing

For Notion or other brokered OAuth flows, run a local server tunnel:

```bash
make dev-server-tunnel
```

For a stable Cloudflare hostname:

```bash
cloudflared tunnel login
cloudflared tunnel create supacli-dev
cloudflared tunnel route dns supacli-dev auth-dev.example.com

SUPACLI_TUNNEL_HOSTNAME=auth-dev.example.com \
  SUPACLI_TUNNEL_NAME=supacli-dev \
  make dev-server-tunnel
```

Then point the CLI at that server:

```bash
export SUPACLI_SUPACLID_URL=https://auth-dev.example.com
```

Never commit tunnel URLs, Cloudflare tunnel tokens, provider client secrets,
OAuth codes, provider tokens, `.env`, `.envrc`, or local credential material.

## Policy and RBAC

Every executable command and alias needs policy metadata. Policy must be
evaluated before:

1. Loading provider tokens from the credential store.
2. Refreshing tokens.
3. Calling provider APIs.
4. Opening browser flows for provider auth.

Use these commands while developing:

```bash
./bin/supacli policy catalog
./bin/supacli policy check --command "notion page read Roadmap"
./bin/supacli policy doctor
```

Provider command metadata is data-driven. Root `status [provider...]` and
`doctor [provider...]` are explicit commands with provider-aware policy checks,
so do not add provider-specific status or doctor subcommands.

Provider command paths, args, flags, group help, aliases, and leaf help belong
in a provider-owned `actions.Spec` tree. Use one spec type for both groups and
leaf actions, then let the Cobra, MCP, REST, policy, and catalog layers walk
that tree instead of maintaining separate command models.

Provider command execution belongs with the provider too. Add client action
handlers under `internal/providers/<provider>/client`, expose them through the
provider catalog, and return typed results. The CLI root should only evaluate
policy, construct an action context, invoke the handler, and render results
through shared `internal/actions` and `internal/output` interfaces. Do not add
provider-specific Cobra files under `internal/cli`.

Provider facets self-register through package `init()` functions. Keep those
functions pure and static: no env reads, I/O, network calls, credentials,
goroutines, or logging. Add client provider facets to
`internal/providers/all`; add supaclid OAuth/token broker facets to
`internal/providers/brokers/all`. `supacli`, `supaclid`, and test harnesses
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

Example:

```text
feat(notion): add page links command

Expose page links as stable structured output so agents can inspect
navigation targets without using the interactive follow menu.
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
9. `make build-supaclid-image`, when Docker is available
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
