package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeWorkflowAgent writes a tiny POSIX shell script to a temp file and
// returns a discoverer that hands it back as the only candidate. The script
// body has access to:
//   - $PROMPT       — the prompt the engine passed as the final positional arg
//   - $TOOLMUX_FAKE_STEP — incremented across invocations of the same test
//
// The body should print whatever the test wants the agent to emit on stdout.
func fakeWorkflowAgent(t *testing.T, body string) func(WorkflowConfigSnapshot) []WorkflowAgentCandidate {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-agent.sh")
	counter := filepath.Join(dir, "step")
	contents := "#!/bin/sh\n" +
		"set -e\n" +
		"PROMPT=\"$1\"\n" +
		"COUNTER_FILE=\"" + counter + "\"\n" +
		"if [ -f \"$COUNTER_FILE\" ]; then\n" +
		"  TOOLMUX_FAKE_STEP=$(($(cat \"$COUNTER_FILE\") + 1))\n" +
		"else\n" +
		"  TOOLMUX_FAKE_STEP=1\n" +
		"fi\n" +
		"printf '%s' \"$TOOLMUX_FAKE_STEP\" > \"$COUNTER_FILE\"\n" +
		"export TOOLMUX_FAKE_STEP\n" +
		body + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return func(WorkflowConfigSnapshot) []WorkflowAgentCandidate {
		return []WorkflowAgentCandidate{{
			Name:    "fake",
			Label:   "Fake agent",
			Command: "/bin/sh",
			Args:    []string{script},
		}}
	}
}
