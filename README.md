# Supacli

Supacli is a local-first mega CLI for connecting and operating SaaS services
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

1. `cmd/supacli` for the CLI.
2. `cmd/supaclid` for the local server daemon.
3. A provider command catalog.
4. A local policy/RBAC engine.
5. A starter Linear integration package.
6. Starter tests, lint targets, and CI configuration.

Deployment model:

1. This OSS repo contains the CLI, `supaclid`, generic self-hosting docs, and
   generic server container build files.
2. Supacli's hosted AWS/Lambda deployment belongs in a private infrastructure
   repo with provider secrets, DNS, monitoring, and deployment state.

See:

1. [Deployment Model](docs/DEPLOYMENT_MODEL.md)
2. [Self-Hosting supaclid](docs/SELF_HOSTING.md)

## Development

Use Go 1.26.3 or newer on the Go 1.26 line.

```bash
make fmt
make test
make test-integration
make build
make build-supaclid-image
```

Provider commands are stubs for now, but they already pass through command
metadata and local policy authorization.

Linear is the first prepared provider because it supports native OAuth with
PKCE, targeted scopes, and local refresh without `supaclid`.
