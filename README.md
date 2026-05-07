# Toolmux

Toolmux is a local-first mega CLI for connecting and operating SaaS services
from one command surface.

The initial provider set is:

1. Notion.
2. Jira.
3. Slack.
4. Linear.
5. Google Docs.
6. Google Drive.
7. Gmail.

The current scaffold includes:

1. `cmd/toolmux` for the CLI.
2. `cmd/toolmuxd` for the local server daemon.
3. A provider command catalog.
4. A local policy/RBAC engine.
5. A starter Linear integration package.
6. Starter tests, lint targets, and CI configuration.

Deployment model:

1. This OSS repo contains the CLI, `toolmuxd`, generic self-hosting docs, and
   generic server container build files.
2. Toolmux's hosted AWS/Lambda deployment belongs in a private infrastructure
   repo with provider secrets, DNS, monitoring, and deployment state.

See:

1. [Deployment Model](docs/DEPLOYMENT_MODEL.md)
2. [Self-Hosting toolmuxd](docs/SELF_HOSTING.md)

## Development

Use Go 1.26.3 or newer on the Go 1.26 line.

```bash
make fmt
make lint
make test
make test-integration
make build
make build-toolmuxd-image
```

`make lint` runs the pinned linter toolchain through the root Dockerfile, so
contributors only need Docker rather than local copies of every linter.

Provider commands are stubs for now, but they already pass through command
metadata and local policy authorization.

All current and future provider commands share the same output contract:
human-friendly terminal output by default, and stable agent/script output with
`--output json` or `--output yaml`. Provider implementations should return
structured results and let the shared output layer handle colors, tables,
markdown rendering, paging, and non-interactive behavior.

Linear is the first prepared provider because it supports native OAuth with
PKCE, targeted scopes, and local refresh without `toolmuxd`.
