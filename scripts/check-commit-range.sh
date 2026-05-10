#!/usr/bin/env bash
set -euo pipefail

if (($# == 0)); then
  echo "usage: scripts/check-commit-range.sh <rev-list-arg>..." >&2
  exit 2
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
commits="$(git rev-list --reverse "$@")"

while IFS= read -r sha; do
  if [[ -z "$sha" ]]; then
    continue
  fi
  echo "Checking commit ${sha}"
  git log -1 --format=%B "$sha" | "${script_dir}/check-commit-message.sh"
done <<<"$commits"
