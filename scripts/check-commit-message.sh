#!/usr/bin/env bash
set -euo pipefail

message="$(cat)"
subject="$(printf '%s\n' "$message" | sed -n '1p')"

subject_re='^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9._-]+\))?!?: .+'

if [[ ! "$subject" =~ $subject_re ]]; then
  echo "invalid commit subject: $subject" >&2
  echo "expected: <type>[optional scope]: <description>" >&2
  exit 1
fi

if ((${#subject} > 72)); then
  echo "commit subject is ${#subject} chars; max is 72" >&2
  exit 1
fi

line_no=0
while IFS= read -r line || [[ -n "$line" ]]; do
  line_no=$((line_no + 1))
  if ((line_no == 1)); then
    continue
  fi
  if ((${#line} > 72)); then
    echo "commit body line $line_no is ${#line} chars; max is 72" >&2
    exit 1
  fi
done <<<"$message"
