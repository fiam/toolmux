# Slack App Setup

Toolmux Slack support uses a Slack OAuth v2 app with user scopes. The hosted or
self-hosted `toolmuxd` keeps the Slack client secret server-side, exchanges the
OAuth code, and hands the resulting user token bundle back to the local CLI for
OS credential-store storage.

## Current Commands

```bash
toolmux connect slack
toolmux slack conversations ls
toolmux slack message send --channel <id-or-name> --text "..."
toolmux slack search --query "from:me deploy"
```

`toolmux slack message send` is a write command and is blocked by
`--read-only`. `toolmux slack search` requires `search:read`; if a workspace or
app review does not grant that scope, use conversation listing and message send
without search.

## Manifest

The initial Slack app manifest should use user scopes, token rotation, and
hosted callback URLs:

```json
{
  "display_information": {
    "name": "Toolmux",
    "description": "Policy-aware Slack access for local CLI and agent workflows.",
    "background_color": "#1D9A8A",
    "long_description": "Toolmux connects Slack to local, policy-aware CLI and agent workflows. It lets each user authorize Slack permissions for listing visible conversations, sending messages when explicitly requested, and searching messages when the optional search permission is granted. Toolmux uses a hosted OAuth broker so Slack app credentials stay server-side while provider tokens are handed back to the user's local Toolmux client for local credential storage."
  },
  "oauth_config": {
    "redirect_urls": [
      "https://api.toolmux.com/oauth/slack/callback",
      "https://dev.albertogh.com/oauth/slack/callback"
    ],
    "scopes": {
      "user": [
        "channels:read",
        "chat:write",
        "groups:read",
        "im:read",
        "mpim:read",
        "search:read"
      ],
      "user_optional": [
        "chat:write",
        "groups:read",
        "im:read",
        "mpim:read",
        "search:read"
      ]
    },
    "pkce_enabled": false
  },
  "settings": {
    "org_deploy_enabled": false,
    "socket_mode_enabled": false,
    "token_rotation_enabled": true,
    "is_mcp_enabled": false
  }
}
```

For self-hosting, add your public callback URL to `redirect_urls`, then set:

```text
SLACK_CLIENT_ID=...
SLACK_CLIENT_SECRET=...
SLACK_REDIRECT_URI=https://auth.example.com/oauth/slack/callback
```

Do not commit Slack client secrets, OAuth codes, refresh tokens, or access
tokens.
