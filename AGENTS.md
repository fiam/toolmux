# AGENTS.md

This file defines repository expectations for coding agents working on Supacli.
Keep it current whenever build, test, style, security, release, or workflow
requirements change.

## Maintenance

Agents must update this file when they:

1. Change the supported Go version or toolchain setup.
2. Add, remove, or rename important `make` targets or CI checks.
3. Add a new provider, auth mode, policy behavior, or test class.
4. Change commit, release, linting, formatting, or security expectations.

When updating Go guidance, check the official Go release notes and release
history first:

1. https://go.dev/doc/go1.26
2. https://go.dev/doc/devel/release

## Go Version

Use the latest stable Go toolchain. As of 2026-05-06, the official Go release
history shows Go 1.26.2 as the latest patch release on the Go 1.26 line.

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
6. `crypto/hpke` is available for HPKE designs, including future handoff work.
7. `errors.AsType` can simplify type-safe error extraction.
8. `testing.T.ArtifactDir` can store integration-test artifacts.
9. `testing/cryptotest.SetGlobalRandom` supports deterministic crypto tests.
10. `testing.B.Loop` should be used for new benchmarks.

Notable Go 1.25 features still relevant to this codebase:

1. `testing/synctest` is available for deterministic concurrent tests.
2. `net/http.CrossOriginProtection` can help protect broker browser endpoints.
3. `go vet` includes `waitgroup` and `hostport` analyzers.
4. `log/slog` includes newer helpers such as `GroupAttrs` and source support.

## Quality Gates

The codebase should have a strict quality setup from the first implementation
milestone. Expected local targets:

```bash
make fmt
make fmt-check
make lint
make test
make test-race
make test-integration
make test-live
make build
make coverage
make commitlint
```

`make test-live` must be skipped by default and require explicit environment
variables such as `SUPACLI_LIVE_TESTS=1`.

CI should run at least:

1. `go fmt` or an equivalent format check.
2. `go vet ./...`.
3. `staticcheck ./...`.
4. `golangci-lint run`.
5. `go test ./...`.
6. `go test -race ./...`.
7. Deterministic fake-upstream integration tests.
8. `govulncheck ./...`.
9. `gosec ./...` or equivalent security linting.
10. Secret scanning with `gitleaks` or equivalent.
11. Markdown, YAML, and shell-script linting where applicable.
12. Commit-message validation.

## Integration Tests

Provider integrations must be tested against fake upstream servers in CI.
Do not rely on live SaaS providers as the main correctness signal.

Fake upstreams should emulate:

1. OAuth success, denial, callback errors, expired state, and PKCE failures.
2. Token refresh, refresh rotation, revocation, and missing scopes.
3. Provider API success, pagination, permission denied, rate limits, malformed
   responses, empty responses, 5xx errors, and retries.
4. Notion, Jira, Slack, Linear, Google Docs, Google Drive, and Gmail behavior
   needed by implemented commands.
5. Brokered local-custody handoff without storing plaintext provider tokens.

Live-provider tests may exist for smoke coverage, but they must be opt-in,
isolated from default CI, and must never record real tokens in fixtures.

## Security

Provider tokens, auth codes, refresh tokens, one-time handoff secrets, and
`Authorization` headers must never appear in logs, fixtures, command output,
crash reports, telemetry, or committed files.

Policy checks must run before vault reads, token refresh, or provider API calls.
Every executable command and alias needs a command spec for policy evaluation.

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

1. Keep the subject line at or below 50 characters.
2. Wrap body lines at 72 characters.
3. Use a blank line between the subject and body.
4. Explain why in the body when the change is not obvious.
5. Use `!` or a `BREAKING CHANGE:` footer for breaking changes.

Examples:

```text
feat(policy): add command catalog

Add command metadata so local policy checks can run before provider
credentials are loaded from the vault.
```
