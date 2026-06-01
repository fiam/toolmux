#!/usr/bin/env bash
# Record the README demo GIF with vhs.
#
# The demo runs the slack-recap workflow against a throwaway HOME wired to a
# stand-in agent, so the recording shows a realistic end-to-end workflow run
# with zero network access and without touching real credentials or org data.
# Re-run after changing docs/demo.tape, the stub agent, or the CLI's output.
#
# Requires: vhs (https://github.com/charmbracelet/vhs), go, python3.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if ! command -v vhs >/dev/null 2>&1; then
  echo "error: vhs is not installed (brew install vhs)" >&2
  exit 1
fi

demo_home="$repo_root/docs/.demo-home"
bin="$repo_root/bin/toolmux"

echo "building toolmux..."
go build -o "$bin" ./cmd/toolmux

echo "seeding demo state in $demo_home..."
rm -rf "$demo_home"
mkdir -p "$demo_home/.toolmux/workflows"

# Wire the workflow to a deterministic stand-in agent. The tape cd's into the
# demo HOME before running, so the relative "demo-agent.py" path resolves and no
# local filesystem path leaks into the recording.
# Register the slack toolbox so the workflow's `internal:slack` requirement is
# satisfied from config alone (no network, no credential lookup), letting the
# demo command stay clean without --no-setup.
cat > "$demo_home/.toolmux/config.yaml" <<'YAML'
version: 1
toolboxes:
    slack:
        type: internal
        provider: slack
workflows:
    default_agent: demo
    agents:
        demo:
            command: python3
            args:
                - demo-agent.py
YAML

cp "$repo_root/docs/demo-agent.py" "$demo_home/demo-agent.py"
cp "$repo_root/workflows/slack-recap.yaml" "$demo_home/.toolmux/workflows/slack-recap.yaml"

echo "recording with vhs..."
vhs docs/demo.tape

echo "done: docs/assets/toolmux-demo.gif"
