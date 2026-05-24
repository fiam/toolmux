# Self-Hosting toolmuxd

Last updated: 2026-05-24

`toolmuxd` is the open-source Toolmux server daemon. It exists so OAuth
providers that require confidential client secrets can still support a
browser-based OAuth flow while keeping provider tokens local to the user's
machine.

Self-hosting means you run your own `toolmuxd` and create your own provider
OAuth apps. Toolmux's hosted provider secrets are not included in this repo.

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
cloudflared tunnel create toolmux-dev
cloudflared tunnel route dns toolmux-dev auth-dev.example.com

TOOLMUX_TUNNEL_HOSTNAME=auth-dev.example.com \
  TOOLMUX_TUNNEL_NAME=toolmux-dev \
  make dev-server-tunnel
```

The script also supports dashboard-managed tunnels with
`TOOLMUX_CLOUDFLARED_TOKEN_FILE` or `TOOLMUX_CLOUDFLARED_TOKEN`. In that mode,
configure the Cloudflare public hostname service to point at
`http://127.0.0.1:8080`.

For real self-hosting, use a stable domain and deployment process instead of a
temporary Quick Tunnel.

## Run From Source

```bash
make build
bin/toolmuxd --addr :8080
```

Health check:

```bash
curl http://127.0.0.1:8080/healthz
```

Build info:

```bash
curl http://127.0.0.1:8080/build
curl -H 'Accept: text/plain' http://127.0.0.1:8080/build
```

## Run With Docker

Released `toolmuxd` images are published for Linux amd64 and arm64:

```bash
docker run --rm -p 8080:8080 ghcr.io/fiam/toolmuxd:<tag>
```

Build the generic image:

```bash
make build-toolmuxd-image
```

Run it locally:

```bash
docker run --rm -p 8080:8080 toolmuxd:dev
```

Health check:

```bash
curl http://127.0.0.1:8080/healthz
```

Build info:

```bash
curl http://127.0.0.1:8080/build
```

## HTTPS and Domains

OAuth providers generally require exact redirect URI matching. Put `toolmuxd`
behind HTTPS in production:

```text
https://auth.example.com/oauth/slack/callback
```

The reverse proxy or hosting platform should terminate TLS and forward requests
to `toolmuxd`.

## Secrets

Provider client secrets are deployment secrets, not source code.

Use environment variables, a secret manager, or your hosting platform's secret
facility:

```text
TOOLMUX_PUBLIC_URL=https://auth.example.com

SLACK_CLIENT_ID=...
SLACK_CLIENT_SECRET=...
SLACK_REDIRECT_URI=https://auth.example.com/oauth/slack/callback
SLACK_SCOPES=channels:read,groups:read,im:read,mpim:read,channels:history,groups:history,im:history,mpim:history,chat:write,reactions:write,files:read,users:read,usergroups:read,usergroups:write,search:read
```

Do not commit secrets, Cloudflare tunnel tokens, tunnel URLs, OAuth codes,
provider tokens, or local `.env` files.

The CLI uses hosted `https://api.toolmux.com` by default. For local development
or self-hosting, set:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com
```

## Token Custody

The initial design is local custody:

1. `toolmuxd` exchanges provider OAuth codes when a client secret is required.
2. `toolmuxd` keeps handoff token bundles only in short-lived process memory.
3. The CLI retrieves the token bundle once over HTTPS using a session secret.
4. The CLI stores provider tokens in the user's OS credential store.
5. `toolmuxd` deletes the handoff data.

No extra application-level handoff encryption is required for this MVP model
because the token bundle is transported over HTTPS and not written to durable
server storage.

Shared or durable handoff storage is out of MVP. If a future deployment needs
Redis, queues, files, or databases for handoff payloads, revisit the threat
model before implementation.

## Persistence

MVP `toolmuxd` should run as a single instance with an in-memory handoff store.
That is enough for local development and simple deployments.

Multi-instance deployments need sticky routing or shared handoff storage. Shared
handoff storage is intentionally deferred until there is a concrete operational
need and a threat model for it.

## Provider Setup

Self-hosters need their own provider OAuth apps:

1. Slack: Slack OAuth app with your callback URL and requested bot scopes.
2. Google: Google Cloud project with a web OAuth client and the Drive and
   Picker APIs enabled.

Remote MCP servers may use their own OAuth flows and do not require a native
provider app in `toolmuxd` unless Toolmux adds a provider-specific broker for
that service.

## Google Setup

Google is a Drive-only native provider. It uses brokered OAuth through
`toolmuxd`, a Google web OAuth client, Google Picker, and the non-sensitive
`drive.file` scope. The CLI stores the resulting token locally; `toolmuxd`
holds provider client configuration and short-lived OAuth/Picker handoff state
only.

### Cloud project

Create or choose one Google Cloud project for Toolmux. Enable the Google Drive
and Google Picker APIs in that project:

1. Open the Google Cloud console.
2. Select an existing project or create a new project for Toolmux.
3. Open APIs & Services > Library.
4. Search for Google Drive API and click Enable.
5. Search for Google Picker API and click Enable.

You can also enable both APIs with `gcloud`:

```bash
gcloud services enable drive.googleapis.com picker.googleapis.com \
  --project=my-google-project
```

`gcloud` cannot create the regular Google Auth Platform OAuth client that Drive
Picker requires. `gcloud iam oauth-clients` creates IAM OAuth clients for Google
Cloud access, and `gcloud iap oauth-clients` creates clients locked to
Identity-Aware Proxy.

### OAuth consent

Configure OAuth consent for the same project:

1. Open Google Auth Platform > Branding or APIs & Services > OAuth consent
   screen.
2. Enter the app name, user support email, and developer contact email.
3. Choose Internal for a Google Workspace-only deployment, or External for
   general Google accounts.
4. Open Google Auth Platform > Audience and add your Google account as a test
   user while the app is in testing mode.
5. Open Google Auth Platform > Data Access or OAuth consent screen > Scopes.
6. Add this scope:

```text
https://www.googleapis.com/auth/drive.file
```

`drive.file` lets Toolmux create files and access files that the user explicitly
opens or selects for the app. It does not grant blanket Drive access. Toolmux's
Drive provider intentionally does not request broader Drive or Docs scopes.

### Web OAuth client

Create an OAuth client:

1. Open Google Auth Platform > Clients or APIs & Services > Credentials.
2. Click Create client or Create credentials > OAuth client ID.
3. Choose Web application as the application type.
4. Name the client, for example `Toolmux Broker`.
5. Add authorized redirect URIs for the broker:

```text
https://auth.example.com/oauth/google/callback
```

Use your actual `toolmuxd` public origin. For the hosted Toolmux broker, use
the hosted origin instead. For local tunnel testing, use the current tunnel URL.
The same callback handles both normal brokered OAuth and brokered Google Picker
callbacks; Toolmux dispatches them by OAuth `state`.

6. Click Create.
7. Copy the client ID and client secret.

Set these `toolmuxd` environment variables:

```bash
export TOOLMUX_PUBLIC_URL=https://auth.example.com
export GOOGLE_CLIENT_ID=...
export GOOGLE_CLIENT_SECRET=...
export GOOGLE_SCOPES=https://www.googleapis.com/auth/drive.file
```

Optional endpoint and redirect overrides for fake upstreams, sovereign
deployments, or tests:

```bash
export GOOGLE_AUTH_URL=...
export GOOGLE_TOKEN_URL=...
export GOOGLE_REVOKE_URL=...
export GOOGLE_REDIRECT_URI=https://auth.example.com/oauth/google/callback
```

Point the CLI at the broker when self-hosting:

```bash
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com
```

### Verify locally

Build or install the CLI, then select a Drive file:

```bash
toolmux google drive selected add
toolmux google drive selected list
toolmux google drive available
```

`toolmux google drive selected add` is the normal first-run command for file
access. It opens the brokered Google Picker flow, saves selected file IDs
locally, and stores the OAuth token. Run `toolmux add google` only when you want
Drive API access before selecting files.

If the app is still in Google testing mode, the signed-in Google account must be
listed as a test user.

### Drive Picker

`toolmux google drive pick` and `toolmux google drive selected add` open Google
Picker through a short-lived `toolmuxd` session. Google returns
`picked_file_ids` to the broker callback, `toolmuxd` exchanges the returned
authorization code, and the CLI polls until the selected file IDs are ready.
By default this uses the same `/oauth/google/callback` redirect URI as normal
Google brokered OAuth. Set `GOOGLE_PICKER_REDIRECT_URI` only if you intentionally
register a separate Picker callback such as
`https://auth.example.com/oauth/google/picker/callback`.

The brokered Picker flow is limited to `drive.file` and must not be combined
with broader scopes.

Picker needs only the same `drive.file` grant used by the default Google
toolboxes. The selected file ID is usable by Toolmux because the user
explicitly opened that file for the app. `toolmux google drive selected add`
saves selected file IDs locally; `toolmux google drive selected list` shows that
cache; `toolmux google drive selected remove <file-id>` removes a cached ID.
`toolmux google drive files copy <file-id-or-url>` copies an accessible source
file into My Drive, defaulting the destination parent to `root`. With
`drive.file`, shared source files must first be selected through Picker unless
Toolmux created or opened them before. Removing a cached ID is a Toolmux-local
operation; users should revoke app access from their Google account when they
need Google to forget the app-level grant.

### Manual smoke test

After configuring the CLI:

```bash
toolmux google drive selected add
toolmux google drive selected list
toolmux google drive files copy <file-id-or-url>
toolmux google drive pick
toolmux google drive available
```

Add `toolmux add google` only when you want to test Drive API authorization
before selecting files.

If OAuth fails before reaching Google, check `TOOLMUX_TOOLMUXD_URL`,
`TOOLMUX_PUBLIC_URL`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, the authorized
redirect URIs, and the OAuth consent screen test users. Set `TOOLMUX_BROWSER`
to launch OAuth and Picker pages in a specific browser; on macOS, use an app
name such as `Google Chrome`.

### Local `.envrc`

`.envrc` is ignored by this repository. With `direnv`, a local self-hosted
Google setup can look like this:

```bash
export GOOGLE_CLIENT_ID=...
export GOOGLE_CLIENT_SECRET=...
export GOOGLE_SCOPES=https://www.googleapis.com/auth/drive.file
export TOOLMUX_PUBLIC_URL=https://auth.example.com
export TOOLMUX_TOOLMUXD_URL=https://auth.example.com

export TOOLMUX_BROWSER="Google Chrome" # optional Picker/OAuth browser override
```

Run `direnv allow` after editing `.envrc`. Do not commit `.envrc`, OAuth client
secrets, OAuth codes, or provider tokens.
