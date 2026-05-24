package cli

import (
	"bytes"
	"context"
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
	if !strings.Contains(rendered, "google.drive.available") || !strings.Contains(rendered, "Remote") || !strings.Contains(rendered, "Local") {
		t.Fatalf("expected tool action effects in catalog, got %q", rendered)
	}
	for _, rootCommand := range []string{"toolbox.add", "mcp.ls", "toolmux.config"} {
		if strings.Contains(rendered, rootCommand) {
			t.Fatalf("policy catalog should not list root command %q, got %q", rootCommand, rendered)
		}
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
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, provider := range []string{"jira", "google" + "-docs", "google" + "-drive"} {
		if strings.Contains(rendered, provider) {
			t.Fatalf("unimplemented provider command %q should not appear in help: %q", provider, rendered)
		}
	}
}

func TestRootHelpShowsOnlyRegisteredNativeCommands(t *testing.T) {
	t.Parallel()

	store := credentials.NewMemoryStore()
	if err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "google",
		AccountID: "default",
	}, credentials.OAuthTokens{
		AccessToken: "google-access",
		TokenType:   "Bearer",
		Scopes:      []string{"https://www.googleapis.com/auth/drive.file"},
	}); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "google") {
		t.Fatalf("expected registered google command in help, got %q", rendered)
	}
	if strings.Contains(rendered, "slack") {
		t.Fatalf("expected unregistered slack command to be hidden from help, got %q", rendered)
	}
}

func TestRootHelpShowsMCPToolCallTimeout(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
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

func TestOpenBrowserCommandUsesToolmuxBrowserOnDarwin(t *testing.T) {
	t.Parallel()
	command, args := openBrowserCommand("darwin", "Google Chrome", "", "https://example.com/picker")
	if command != "open" {
		t.Fatalf("expected open command, got %q", command)
	}
	want := []string{"-a", "Google Chrome", "https://example.com/picker"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected args %#v, got %#v", want, args)
	}
}

func TestOpenBrowserCommandUsesBrowserCommand(t *testing.T) {
	t.Parallel()
	command, args := openBrowserCommand("linux", "", "firefox --new-window %s", "https://example.com/picker")
	if command != "firefox" {
		t.Fatalf("expected firefox command, got %q", command)
	}
	want := []string{"--new-window", "https://example.com/picker"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected args %#v, got %#v", want, args)
	}
}

func TestOpenBrowserCommandDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		goosName    string
		wantCommand string
		wantArgs    []string
	}{
		{name: "darwin", goosName: "darwin", wantCommand: "open", wantArgs: []string{"https://example.com/picker"}},
		{name: "windows", goosName: "windows", wantCommand: "rundll32", wantArgs: []string{"url.dll,FileProtocolHandler", "https://example.com/picker"}},
		{name: "linux", goosName: "linux", wantCommand: "xdg-open", wantArgs: []string{"https://example.com/picker"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			command, args := openBrowserCommand(tt.goosName, "", "", "https://example.com/picker")
			if command != tt.wantCommand {
				t.Fatalf("expected command %q, got %q", tt.wantCommand, command)
			}
			if strings.Join(args, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Fatalf("expected args %#v, got %#v", tt.wantArgs, args)
			}
		})
	}
}

func TestReadOnlyModeBlocksMutatingToolCommand(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--read-only", "slack", "conversations_add_message", "--channel_id", "C123", "--text", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected read-only denial")
	}
	if !strings.Contains(err.Error(), "read-only mode blocks command slack.conversations_add_message") {
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
