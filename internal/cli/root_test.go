package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
	_ "github.com/fiam/toolmux/internal/providers/all"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() == "" {
		t.Fatal("expected version output")
	}
}

func TestPolicyCatalog(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"policy", "catalog"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "toolbox.add") || !strings.Contains(rendered, "Remote") || !strings.Contains(rendered, "Local") {
		t.Fatalf("expected action effects in catalog, got %q", rendered)
	}
	if strings.Contains(rendered, "gmail.send") {
		t.Fatalf("catalog should not list unimplemented provider commands, got %q", rendered)
	}
}

func TestColorAlwaysColorsTableOutput(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--color", "always", "policy", "catalog"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("expected --color always to color table output, got %q", out.String())
	}
}

func TestUnimplementedProviderCommandsDoNotAppearInHelp(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, provider := range []string{"jira", "google"} {
		if strings.Contains(rendered, provider) {
			t.Fatalf("unimplemented provider command %q should not appear in help: %q", provider, rendered)
		}
	}
}

func TestRootHelpShowsMCPToolCallTimeout(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--mcp-tool-call-timeout") {
		t.Fatalf("expected MCP tool call timeout flag in help, got %q", out.String())
	}
}

func TestMCPToolCallTimeoutMustBePositive(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--mcp-tool-call-timeout", "0s", "version"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected timeout validation error")
	}
	if !strings.Contains(err.Error(), "--mcp-tool-call-timeout must be greater than 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadOnlyModeBlocksMutatingRootCommand(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--read-only", "add", "https://example.com/mcp", "--name", "demo", "--no-sync", "--global"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected read-only denial")
	}
	if !strings.Contains(err.Error(), "read-only mode blocks command toolbox.add") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoctorTableRunsCoreDiagnostics(t *testing.T) {
	t.Parallel()
	store := credentials.NewMemoryStore()
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"doctor"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "credential-store") || !strings.Contains(rendered, "toolboxes") {
		t.Fatalf("expected doctor diagnostics table, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("non-tty doctor output should not contain ANSI escape sequences: %q", rendered)
	}
}
