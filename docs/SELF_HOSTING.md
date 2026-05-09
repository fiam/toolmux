# Self-Hosting supaclid

Last updated: 2026-05-09

`supaclid` is the open-source Supacli server daemon. It exists so OAuth
providers that require confidential client secrets can still support a
browser-based "connect" flow while keeping provider tokens local to the user's
machine.

Self-hosting means you run your own `supaclid` and create your own provider
OAuth apps. Supacli's hosted provider secrets are not included in this repo.

## Requirements

1. Go 1.26 for building from source, or Docker for the container image.
2. A public HTTPS URL for OAuth callbacks.
3. Provider OAuth apps for the providers you want to support.
4. Deployment secrets for provider client ids and client secrets.

For local testing with a temporary `trycloudflare.com` hostname, use:

```bash
make dev-server-tunnel
```

If you have a Cloudflare account and a domain on Cloudflare, use a stable
tunnel hostname so OAuth redirect URIs do not change every run:

```bash
cloudflared tunnel login
cloudflared tunnel create supacli-dev
cloudflared tunnel route dns supacli-dev auth-dev.example.com

SUPACLI_TUNNEL_HOSTNAME=auth-dev.example.com \
  SUPACLI_TUNNEL_NAME=supacli-dev \
  make dev-server-tunnel
```

The script also supports dashboard-managed tunnels with
`SUPACLI_CLOUDFLARED_TOKEN_FILE` or `SUPACLI_CLOUDFLARED_TOKEN`. In that mode,
configure the Cloudflare public hostname service to point at
`http://127.0.0.1:8080`.

For real self-hosting, use a stable domain and deployment process instead of a
temporary Quick Tunnel.

## Run From Source

```bash
make build
bin/supaclid --addr :8080
```

Health check:

```bash
curl http://127.0.0.1:8080/healthz
```

## Run With Docker

Released `supaclid` images are published for Linux amd64 and arm64:

```bash
docker run --rm -p 8080:8080 ghcr.io/fiam/supaclid:<tag>
```

Build the generic image:

```bash
make build-supaclid-image
```

Run it locally:

```bash
docker run --rm -p 8080:8080 supaclid:dev
```

Health check:

```bash
curl http://127.0.0.1:8080/healthz
```

## HTTPS and Domains

OAuth providers generally require exact redirect URI matching. Put `supaclid`
behind HTTPS in production:

```text
https://auth.example.com/oauth/notion/callback
https://auth.example.com/oauth/jira/callback
https://auth.example.com/oauth/slack/callback
```

The reverse proxy or hosting platform should terminate TLS and forward requests
to `supaclid`.

## Secrets

Provider client secrets are deployment secrets, not source code.

Use environment variables, a secret manager, or your hosting platform's secret
facility:

```text
SUPACLI_PUBLIC_URL=https://auth.example.com

NOTION_CLIENT_ID=...
NOTION_CLIENT_SECRET=...
NOTION_REDIRECT_URI=https://auth.example.com/oauth/notion/callback

ATLASSIAN_CLIENT_ID=...
ATLASSIAN_CLIENT_SECRET=...
ATLASSIAN_REDIRECT_URI=https://auth.example.com/oauth/jira/callback

SLACK_CLIENT_ID=...
SLACK_CLIENT_SECRET=...
SLACK_REDIRECT_URI=https://auth.example.com/oauth/slack/callback
```

Do not commit secrets, Cloudflare tunnel tokens, tunnel URLs, OAuth codes,
provider tokens, or local `.env` files.

The CLI uses hosted `https://api.supacli.com` by default. For local development
or self-hosting, set:

```bash
export SUPACLI_SUPACLID_URL=https://auth.example.com
```

## Token Custody

The initial design is local custody:

1. `supaclid` exchanges provider OAuth codes when a client secret is required.
2. `supaclid` keeps handoff token bundles only in short-lived process memory.
3. The CLI retrieves the token bundle once over HTTPS using a session secret.
4. The CLI stores provider tokens in the user's OS credential store.
5. `supaclid` deletes the handoff data.

No extra application-level handoff encryption is required for this MVP model
because the token bundle is transported over HTTPS and not written to durable
server storage.

Shared or durable handoff storage is out of MVP. If a future deployment needs
Redis, queues, files, or databases for handoff payloads, revisit the threat
model before implementation.

## Persistence

MVP `supaclid` should run as a single instance with an in-memory handoff store.
That is enough for local development and simple deployments.

Multi-instance deployments need sticky routing or shared handoff storage. Shared
handoff storage is intentionally deferred until there is a concrete operational
need and a threat model for it.

## Provider Setup

Self-hosters need their own provider OAuth apps:

1. Notion: public connection with your callback URL.
2. Jira: Atlassian OAuth 2.0 3LO app with your callback URL.
3. Slack: Slack OAuth app with your callback URL.

See provider-specific docs under `docs/providers/`.
