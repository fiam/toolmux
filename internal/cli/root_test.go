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
	if !strings.Contains(rendered, "mcp.add") || !strings.Contains(rendered, "Remote") || !strings.Contains(rendered, "Local") {
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

func TestReadOnlyModeBlocksMutatingRootCommand(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--read-only", "mcp", "add", "demo", "https://example.com/mcp", "--no-sync", "--global"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected read-only denial")
	}
	if !strings.Contains(err.Error(), "read-only mode blocks command mcp.add") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeErrorDoesNotPrintUsage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"object":"error","status":503,"code":"not_configured","message":"JIRA_CLIENT_ID is required"}`))
	}))
	defer server.Close()

	cmd := NewRootCommandWithDeps(Dependencies{
		HTTPClient:  server.Client(),
		ToolmuxdURL: server.URL,
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"connect", "jira"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected connect error")
	}
	if !strings.Contains(err.Error(), "JIRA_CLIENT_ID is required") {
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
		Provider:  "jira",
		AccountID: "default",
	}, credentials.OAuthTokens{
		AccessToken: "jira-access-token",
		TokenType:   "bearer",
		Scopes:      []string{"read:jira-work", "write:jira-work"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"status", "jira"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Provider") || !strings.Contains(rendered, "connected") || !strings.Contains(rendered, "write:jira-work") {
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
