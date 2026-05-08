#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SERVER_ADDR="${SUPACLI_SERVER_ADDR:-127.0.0.1:8080}"
LOCAL_URL="${SUPACLI_LOCAL_SERVER_URL:-http://${SERVER_ADDR}}"
STATE_DIR="${SUPACLI_DEV_STATE_DIR:-.supacli}"
ENV_FILE="${SUPACLI_DEV_ENV:-${STATE_DIR}/server-tunnel.env}"
CLOUDFLARED_LOG="${SUPACLI_CLOUDFLARED_LOG:-${STATE_DIR}/cloudflared.log}"
SERVER_LOG="${SUPACLI_SERVER_LOG:-${STATE_DIR}/server.log}"
CLOUDFLARED_CONFIG="${SUPACLI_CLOUDFLARED_CONFIG:-${STATE_DIR}/cloudflared.yaml}"

TUNNEL_HOSTNAME="${SUPACLI_TUNNEL_HOSTNAME:-}"
TUNNEL_NAME="${SUPACLI_TUNNEL_NAME:-supacli-dev}"
TUNNEL_ROUTE_DNS="${SUPACLI_TUNNEL_ROUTE_DNS:-0}"
CLOUDFLARED_CREDENTIALS_FILE="${SUPACLI_CLOUDFLARED_CREDENTIALS_FILE:-}"
CLOUDFLARED_TOKEN="${SUPACLI_CLOUDFLARED_TOKEN:-}"
CLOUDFLARED_TOKEN_FILE="${SUPACLI_CLOUDFLARED_TOKEN_FILE:-}"

SERVER_PID=""
TUNNEL_PID=""
PUBLIC_URL=""
TUNNEL_MODE="quick"

usage() {
  cat <<'EOF'
Run supaclid locally and expose it through Cloudflare Tunnel.

Quick tunnel mode, no Cloudflare account required:

  make dev-server-tunnel

Stable locally-managed tunnel mode:

  cloudflared tunnel login
  cloudflared tunnel create supacli-dev
  cloudflared tunnel route dns supacli-dev auth-dev.example.com

  SUPACLI_TUNNEL_HOSTNAME=auth-dev.example.com \
    SUPACLI_TUNNEL_NAME=supacli-dev \
    make dev-server-tunnel

Stable dashboard-managed token mode:

  1. Create a tunnel and public hostname in the Cloudflare dashboard.
  2. Point the public hostname service at http://127.0.0.1:8080.
  3. Run with a token file or token environment variable:

  SUPACLI_TUNNEL_HOSTNAME=auth-dev.example.com \
    SUPACLI_CLOUDFLARED_TOKEN_FILE=.supacli/cloudflared-token \
    make dev-server-tunnel

Environment knobs:

  SUPACLI_SERVER_ADDR                    local bind address, default 127.0.0.1:8080
  SUPACLI_TUNNEL_HOSTNAME                stable public hostname, enables stable mode
  SUPACLI_TUNNEL_NAME                    locally-managed tunnel name, default supacli-dev
  SUPACLI_TUNNEL_ROUTE_DNS=1             run cloudflared tunnel route dns before run
  SUPACLI_CLOUDFLARED_CREDENTIALS_FILE   locally-managed tunnel credentials JSON
  SUPACLI_CLOUDFLARED_TOKEN_FILE         dashboard-managed tunnel token file
  SUPACLI_CLOUDFLARED_TOKEN              dashboard-managed tunnel token

The script writes Supacli environment hints to .supacli/server-tunnel.env.
It never writes Cloudflare tunnel tokens.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

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

Quick Tunnels do not require a Cloudflare account. Stable hostnames require a
Cloudflare account, a tunnel, and a DNS/public hostname route.
EOF
  exit 1
fi

if [[ -n "$CLOUDFLARED_TOKEN" && -n "$CLOUDFLARED_TOKEN_FILE" ]]; then
  echo "set only one of SUPACLI_CLOUDFLARED_TOKEN or SUPACLI_CLOUDFLARED_TOKEN_FILE" >&2
  exit 1
fi

if [[ -n "$TUNNEL_HOSTNAME" ]]; then
  PUBLIC_URL="https://${TUNNEL_HOSTNAME}"
  if [[ -n "$CLOUDFLARED_TOKEN" || -n "$CLOUDFLARED_TOKEN_FILE" ]]; then
    TUNNEL_MODE="token"
  else
    TUNNEL_MODE="named"
  fi
elif [[ -f "${HOME}/.cloudflared/config.yaml" ]]; then
  cat >&2 <<'EOF'
warning: ~/.cloudflared/config.yaml exists.
Cloudflare documents that TryCloudflare quick tunnels are not supported when
that config file is present. Set SUPACLI_TUNNEL_HOSTNAME for stable tunnel mode
or temporarily move the config file if quick tunnel startup fails.
EOF
fi

mkdir -p "$STATE_DIR" bin
: >"$CLOUDFLARED_LOG"
: >"$SERVER_LOG"

write_env_file() {
  cat >"$ENV_FILE" <<EOF
SUPACLI_LOCAL_SERVER_URL=${LOCAL_URL}
SUPACLI_PUBLIC_URL=${PUBLIC_URL}
SUPACLI_SUPACLID_URL=${PUBLIC_URL}
NOTION_REDIRECT_URI=${PUBLIC_URL}/oauth/notion/callback
EOF
}

wait_for_local_server() {
  echo "waiting for ${LOCAL_URL}/healthz..."
  for _ in {1..80}; do
    if curl -fsS "${LOCAL_URL}/healthz" >/dev/null 2>&1; then
      return
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "local server exited early; log follows:" >&2
      cat "$SERVER_LOG" >&2
      exit 1
    fi
    sleep 0.25
  done

  echo "local server did not become healthy; log follows:" >&2
  cat "$SERVER_LOG" >&2
  exit 1
}

wait_for_public_tunnel() {
  echo "waiting for ${PUBLIC_URL}/healthz through Cloudflare..."
  for _ in {1..120}; do
    if ! kill -0 "$TUNNEL_PID" 2>/dev/null; then
      echo "cloudflared exited early; log follows:" >&2
      cat "$CLOUDFLARED_LOG" >&2
      exit 1
    fi
    if curl -fsS "${PUBLIC_URL}/healthz" >/dev/null 2>&1; then
      return
    fi
    sleep 0.5
  done

  echo "timed out waiting for public tunnel health; log follows:" >&2
  cat "$CLOUDFLARED_LOG" >&2
  exit 1
}

start_quick_tunnel() {
  echo "starting Cloudflare quick tunnel for ${LOCAL_URL}..."
  cloudflared tunnel --url "$LOCAL_URL" >"$CLOUDFLARED_LOG" 2>&1 &
  TUNNEL_PID="$!"

  for _ in {1..120}; do
    if ! kill -0 "$TUNNEL_PID" 2>/dev/null; then
      echo "cloudflared exited early; log follows:" >&2
      cat "$CLOUDFLARED_LOG" >&2
      exit 1
    fi
    PUBLIC_URL="$(grep -Eo 'https://[[:alnum:]-]+\.trycloudflare\.com' "$CLOUDFLARED_LOG" | head -n 1 || true)"
    if [[ -n "$PUBLIC_URL" ]]; then
      break
    fi
    sleep 0.5
  done

  if [[ -z "$PUBLIC_URL" ]]; then
    echo "timed out waiting for Cloudflare tunnel URL; log follows:" >&2
    cat "$CLOUDFLARED_LOG" >&2
    exit 1
  fi
}

write_named_tunnel_config() {
  {
    printf 'tunnel: %s\n' "$TUNNEL_NAME"
    if [[ -n "$CLOUDFLARED_CREDENTIALS_FILE" ]]; then
      printf 'credentials-file: %s\n' "$CLOUDFLARED_CREDENTIALS_FILE"
    fi
    printf '\ningress:\n'
    printf '  - hostname: %s\n' "$TUNNEL_HOSTNAME"
    printf '    service: %s\n' "$LOCAL_URL"
    printf '  - service: http_status:404\n'
  } >"$CLOUDFLARED_CONFIG"
}

start_named_tunnel() {
  write_named_tunnel_config
  echo "wrote Cloudflare tunnel config to ${CLOUDFLARED_CONFIG}"
  cloudflared tunnel --config "$CLOUDFLARED_CONFIG" ingress validate

  if [[ "$TUNNEL_ROUTE_DNS" == "1" ]]; then
    echo "routing ${TUNNEL_HOSTNAME} to Cloudflare tunnel ${TUNNEL_NAME}..."
    if ! cloudflared tunnel route dns "$TUNNEL_NAME" "$TUNNEL_HOSTNAME"; then
      echo "warning: DNS route command failed; continuing in case the route already exists" >&2
    fi
  fi

  echo "starting Cloudflare named tunnel ${TUNNEL_NAME} for ${PUBLIC_URL}..."
  if [[ -n "$CLOUDFLARED_CREDENTIALS_FILE" ]]; then
    cloudflared tunnel --config "$CLOUDFLARED_CONFIG" run \
      --credentials-file "$CLOUDFLARED_CREDENTIALS_FILE" \
      "$TUNNEL_NAME" >"$CLOUDFLARED_LOG" 2>&1 &
  else
    cloudflared tunnel --config "$CLOUDFLARED_CONFIG" run \
      "$TUNNEL_NAME" >"$CLOUDFLARED_LOG" 2>&1 &
  fi
  TUNNEL_PID="$!"
}

start_token_tunnel() {
  echo "starting Cloudflare dashboard-managed tunnel for ${PUBLIC_URL}..."
  if [[ -n "$CLOUDFLARED_TOKEN_FILE" ]]; then
    cloudflared tunnel run --token-file "$CLOUDFLARED_TOKEN_FILE" >"$CLOUDFLARED_LOG" 2>&1 &
  else
    TUNNEL_TOKEN="$CLOUDFLARED_TOKEN" cloudflared tunnel run >"$CLOUDFLARED_LOG" 2>&1 &
  fi
  TUNNEL_PID="$!"
}

echo "building supaclid..."
go build -o bin/supaclid ./cmd/supaclid

echo "starting local server on ${SERVER_ADDR}..."
if [[ -n "$PUBLIC_URL" ]]; then
  SUPACLI_PUBLIC_URL="$PUBLIC_URL" bin/supaclid --addr "$SERVER_ADDR" >"$SERVER_LOG" 2>&1 &
else
  bin/supaclid --addr "$SERVER_ADDR" >"$SERVER_LOG" 2>&1 &
fi
SERVER_PID="$!"

wait_for_local_server

case "$TUNNEL_MODE" in
quick)
  start_quick_tunnel
  ;;
named)
  start_named_tunnel
  ;;
token)
  start_token_tunnel
  ;;
*)
  echo "unknown tunnel mode: ${TUNNEL_MODE}" >&2
  exit 1
  ;;
esac

wait_for_public_tunnel
write_env_file

cat <<EOF

Supacli local server tunnel is running.

Mode:
  ${TUNNEL_MODE}

Local server:
  ${LOCAL_URL}

Public tunnel:
  ${PUBLIC_URL}

OAuth callback template:
  ${PUBLIC_URL}/oauth/<provider>/callback

Supacli environment:
  export SUPACLI_SUPACLID_URL=${PUBLIC_URL}
  export SUPACLI_PUBLIC_URL=${PUBLIC_URL}
  export NOTION_REDIRECT_URI=${PUBLIC_URL}/oauth/notion/callback

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
