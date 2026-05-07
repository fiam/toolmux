# Notion App Setup

Last updated: 2026-05-07

Supacli needs a Notion public connection for the zero-manual-token user flow.
Do not create an internal connection for the product OAuth path; internal
connections produce a static token for one workspace and do not give users the
browser-based install flow we want.

## Create the Dev Connection

1. Open the Notion developer portal:
   https://www.notion.so/profile/integrations
2. In the Build section, choose `Public connections`.
3. Click `Create new connection`.
4. Use these settings for local development:

```text
Connection name: Supacli Dev
Development workspace: <your test workspace>
Installation scope: Selected workspaces only
```

Use `Selected workspaces only` for the development connection. Notion says the
installation scope is set at creation time and cannot be changed later. When we
are ready for a public hosted release, create a separate production connection
with `Any workspace`.

## Redirect URIs

Add the redirect URI that `supaclid` will expose:

```text
https://auth.supacli.dev/oauth/notion/callback
```

For local development, add one of these too:

```text
https://<your-tunnel-domain>/oauth/notion/callback
http://localhost:8080/oauth/notion/callback
```

Prefer a stable HTTPS tunnel for local development until we verify Notion's
current localhost behavior in the dashboard. The redirect URI used in the
authorization URL must match one of the connection's configured redirect URIs.

## Local Cloudflare Tunnel Harness

Supacli includes a local server harness that starts `supaclid` and exposes
it through a Cloudflare Quick Tunnel:

```bash
make dev-server-tunnel
```

The harness runs:

```text
supaclid --addr 127.0.0.1:8080
cloudflared tunnel --url http://127.0.0.1:8080
```

It prints the generated `https://*.trycloudflare.com` URL and the exact Notion
redirect URI to paste into the Notion connection dashboard:

```text
https://<generated>.trycloudflare.com/oauth/notion/callback
```

It also writes local, ignored environment hints to:

```text
.supacli/server-tunnel.env
```

Quick Tunnel URLs are temporary and usually change each run. If the URL changes,
update the Notion connection's redirect URI before testing another OAuth flow.

## Capabilities

For the MVP Notion commands, request the smallest useful capability set:

```text
Read content
Insert content
No user information
```

Why:

1. `notion search`, `notion page get`, and `notion database query` need read
   access to selected pages/databases.
2. `notion page create` needs insert access.
3. We do not need update content, comments, or user emails for the first pass.

If we later add page updates, block edits, comments, or richer identity display,
we can add capabilities and require users to reauthorize.

## Secrets to Capture

After creation, open the connection's `Configuration` tab and copy:

```text
NOTION_CLIENT_ID=<OAuth client id>
NOTION_CLIENT_SECRET=<OAuth client secret>
NOTION_AUTH_URL=<connection authorization URL>
NOTION_REDIRECT_URI=<chosen redirect URI>
```

Keep these in local shell secrets, `.env.local`, or deployment secrets. Do not
commit them and do not paste the client secret into chat.

supaclid uses the client id and client secret to exchange Notion authorization
codes and refresh tokens. The CLI still stores the resulting provider tokens
locally; supaclid must not persist Notion tokens.

## Authorization Behavior to Expect

When a user connects Notion:

1. Supacli sends them to the Notion authorization URL.
2. Notion displays the requested capabilities.
3. The user chooses pages/databases through Notion's page picker.
4. Notion redirects to the configured redirect URI with a temporary `code`.
5. supaclid exchanges the code for a token using HTTP Basic
   authentication with the Notion client id and client secret.
6. The token response includes workspace metadata that the CLI can store
   locally with the token bundle.

Only the authorizing user can use that public connection authorization. If
multiple members in a Notion workspace want Supacli access, each member needs to
complete the OAuth flow.

## Production Connection Later

For production, create a separate connection:

```text
Connection name: Supacli
Installation scope: Any workspace
Redirect URI: https://auth.supacli.dev/oauth/notion/callback
```

Marketplace listing details are separate from creating a public connection. We
do not need a Marketplace listing for the initial OAuth implementation.

## References

1. Public connections:
   https://developers.notion.com/guides/get-started/public-connections
2. Authorization:
   https://developers.notion.com/guides/get-started/authorization
3. Connection capabilities:
   https://developers.notion.com/reference/capabilities
4. Cloudflare Quick Tunnels:
   https://try.cloudflare.com/
5. Cloudflare TryCloudflare notes:
   https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/
