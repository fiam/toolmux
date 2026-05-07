#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SERVER_ADDR="${TOOLMUX_SERVER_ADDR:-127.0.0.1:8080}"
LOCAL_URL="${TOOLMUX_LOCAL_SERVER_URL:-http://${SERVER_ADDR}}"
STATE_DIR="${TOOLMUX_DEV_STATE_DIR:-.toolmux}"
ENV_FILE="${TOOLMUX_DEV_ENV:-${STATE_DIR}/server-tunnel.env}"
CLOUDFLARED_LOG="${TOOLMUX_CLOUDFLARED_LOG:-${STATE_DIR}/cloudflared.log}"
SERVER_LOG="${TOOLMUX_SERVER_LOG:-${STATE_DIR}/server.log}"

SERVER_PID=""
TUNNEL_PID=""

cleanup() {
  if [[ -n "$TUNNEL_PID" ]] && kill -0 "$TUNNEL_PID" 2>/dev/null; then
    kill "$TUNNEL_PID" 2>/dev/null || true
  fi
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    return 1
  fi
}

need go
need curl
if ! need cloudflared; then
  cat >&2 <<'EOF'

Install cloudflared, then rerun:

  brew install cloudflared

Cloudflare Quick Tunnels do not require a Cloudflare account.
EOF
  exit 1
fi

if [[ -f "${HOME}/.cloudflared/config.yaml" ]]; then
  cat >&2 <<'EOF'
warning: ~/.cloudflared/config.yaml exists.
Cloudflare documents that TryCloudflare quick tunnels are not supported when
that config file is present. If tunnel startup fails, temporarily move it.
EOF
fi

mkdir -p "$STATE_DIR" bin
: >"$CLOUDFLARED_LOG"
: >"$SERVER_LOG"

echo "building toolmuxd..."
go build -o bin/toolmuxd ./cmd/toolmuxd

echo "starting local server on ${SERVER_ADDR}..."
bin/toolmuxd --addr "$SERVER_ADDR" >"$SERVER_LOG" 2>&1 &
SERVER_PID="$!"

echo "waiting for ${LOCAL_URL}/healthz..."
for _ in {1..80}; do
  if curl -fsS "${LOCAL_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "local server exited early; log follows:" >&2
    cat "$SERVER_LOG" >&2
    exit 1
  fi
  sleep 0.25
done

if ! curl -fsS "${LOCAL_URL}/healthz" >/dev/null 2>&1; then
  echo "local server did not become healthy; log follows:" >&2
  cat "$SERVER_LOG" >&2
  exit 1
fi

echo "starting Cloudflare quick tunnel for ${LOCAL_URL}..."
cloudflared tunnel --url "$LOCAL_URL" >"$CLOUDFLARED_LOG" 2>&1 &
TUNNEL_PID="$!"

TUNNEL_URL=""
for _ in {1..120}; do
  if ! kill -0 "$TUNNEL_PID" 2>/dev/null; then
    echo "cloudflared exited early; log follows:" >&2
    cat "$CLOUDFLARED_LOG" >&2
    exit 1
  fi
  TUNNEL_URL="$(grep -Eo 'https://[[:alnum:]-]+\.trycloudflare\.com' "$CLOUDFLARED_LOG" | head -n 1 || true)"
  if [[ -n "$TUNNEL_URL" ]]; then
    break
  fi
  sleep 0.5
done

if [[ -z "$TUNNEL_URL" ]]; then
  echo "timed out waiting for Cloudflare tunnel URL; log follows:" >&2
  cat "$CLOUDFLARED_LOG" >&2
  exit 1
fi

cat >"$ENV_FILE" <<EOF
TOOLMUX_LOCAL_SERVER_URL=${LOCAL_URL}
TOOLMUX_SERVER_URL=${TUNNEL_URL}
EOF

cat <<EOF

Toolmux local server tunnel is running.

Local server:
  ${LOCAL_URL}

Public tunnel:
  ${TUNNEL_URL}

OAuth callback template:
  ${TUNNEL_URL}/oauth/<provider>/callback

For Notion, add this redirect URI in the Notion connection dashboard:
  ${TUNNEL_URL}/oauth/notion/callback

Wrote local environment hints to:
  ${ENV_FILE}

Logs:
  ${SERVER_LOG}
  ${CLOUDFLARED_LOG}

Press Ctrl+C to stop the server and tunnel.

EOF

tail -n +1 -f "$CLOUDFLARED_LOG" &
TAIL_PID="$!"
wait "$TUNNEL_PID" || true
kill "$TAIL_PID" 2>/dev/null || true
