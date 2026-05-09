package supaclitest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fiam/supacli/internal/cli"
	_ "github.com/fiam/supacli/internal/providers/all"
)

type Result struct {
	Output string
	Err    error
}

func Run(t testing.TB, deps cli.Dependencies, args ...string) string {
	t.Helper()
	result := RunResult(t, deps, args...)
	if result.Err != nil {
		t.Fatalf("supacli %s failed: %v\noutput:\n%s", strings.Join(args, " "), result.Err, result.Output)
	}
	return result.Output
}

func RunResult(t testing.TB, deps cli.Dependencies, args ...string) Result {
	t.Helper()
	cmd := cli.NewRootCommandWithDeps(deps)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return Result{
		Output: out.String(),
		Err:    err,
	}
}

func AssertContains(t testing.TB, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("expected output to contain %q, got %q", want, output)
	}
}
