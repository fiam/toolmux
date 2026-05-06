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
2. `cmd/auth-broker` for the minimal OAuth broker.
3. A provider command catalog.
4. A local policy/RBAC engine.
5. Starter tests, lint targets, and CI configuration.

## Development

Use Go 1.26.2 or newer on the Go 1.26 line.

```bash
make fmt
make test
make test-integration
make build
```

Provider commands are stubs for now, but they already pass through command
metadata and local policy authorization.

