#!/usr/bin/env python3
"""Stub workflow agent used only to record the README demo GIF.

A real Toolmux workflow hands the rendered prompt to a local agent (Codex,
Claude Code, ...) which calls the live Slack tools. For a deterministic,
offline recording this stand-in ignores the prompt, pauses briefly so the
workflow spinner is visible, and prints a canned recap to stdout (which the
workflow surfaces as the result).
"""
import sys
import time

RECAP = """\
Slack recap - last 24h

#eng-releases
  - v2.4 cut and deploying to staging; prod rollout tomorrow 9am (decision)
  - Blocker: flaky integration test on CI - Dana is investigating
  - Asked: who owns the changelog this cycle?

#design
  - New onboarding flow approved; handoff to eng on Thursday
  - Needs final empty-state copy by EOD (asked: @sam)

#general
  - All-hands moved to Friday 10am PT

Posted this recap to your Slack DM.
"""


def main():
    # Pretend to do work so the workflow spinner animates in the capture.
    time.sleep(2.0)
    sys.stdout.write(RECAP)
    sys.stdout.flush()


if __name__ == "__main__":
    main()
