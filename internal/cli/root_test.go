package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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
	if !strings.Contains(rendered, "slack.message.send") || !strings.Contains(rendered, "Remote") || !strings.Contains(rendered, "Local") {
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

func TestProviderHelpComesFromActionMetadata(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"slack", "conversations", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Read Slack conversations") || !strings.Contains(rendered, "Usage:") {
		t.Fatalf("expected generated provider help from metadata, got %q", rendered)
	}
}

func TestProviderCommandFlagsComeFromActionMetadata(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"slack", "conversations", "ls", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"--include-archived", "--limit", "--team", "--types"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected generated provider help to contain %q, got %q", want, rendered)
		}
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

func TestReadOnlyModeBlocksMutatingProviderCommand(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--read-only", "slack", "message", "send", "--channel", "C123456", "--text", "Draft", "--dry-run"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected read-only denial")
	}
	if !strings.Contains(err.Error(), "read-only mode blocks command slack.message.send") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeErrorDoesNotPrintUsage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"object":"error","status":503,"code":"not_configured","message":"SLACK_CLIENT_ID is required"}`))
	}))
	defer server.Close()

	cmd := NewRootCommandWithDeps(Dependencies{
		HTTPClient:  server.Client(),
		ToolmuxdURL: server.URL,
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"connect", "slack"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected connect error")
	}
	if !strings.Contains(err.Error(), "SLACK_CLIENT_ID is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Fatalf("runtime error printed usage:\n%s", out.String())
	}
}

func TestStatusTableShowsProviderPermissions(t *testing.T) {
	t.Parallel()
	store := credentials.NewMemoryStore()
	err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "default",
	}, credentials.OAuthTokens{
		AccessToken: "slack-access-token",
		TokenType:   "bearer",
		Scopes:      []string{"channels:read", "chat:write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"status", "slack"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Provider") || !strings.Contains(rendered, "connected") || !strings.Contains(rendered, "chat:write") {
		t.Fatalf("expected connected status table with permissions, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("non-tty table output should not contain ANSI escape sequences: %q", rendered)
	}
}

func TestDoctorTableRunsCoreAndProviderDiagnostics(t *testing.T) {
	t.Parallel()
	store := credentials.NewMemoryStore()
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"doctor", "jira"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "credential-store") || !strings.Contains(rendered, "not connected") {
		t.Fatalf("expected doctor diagnostics table, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("non-tty doctor output should not contain ANSI escape sequences: %q", rendered)
	}
}

func TestProviderDryRunDoesNotRequireCredentials(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: credentials.NewMemoryStore()})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"slack", "message", "send",
		"--channel", "C123456",
		"--text", "preview",
		"--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "slack.message.send") {
		t.Fatalf("expected dry-run output, got %q", out.String())
	}
}
