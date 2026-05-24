//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func TestToolboxRemoveMultipleServersUsesProjectFlag(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	projectPath := filepath.Join(env.Home, ".toolmux", "config.yaml")
	if err := writeToolmuxConfigFile(projectPath, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			Servers: map[string]mcpRemoteServer{
				"linear": {URL: "https://mcp.linear.app/mcp", Transport: mcpRemoteTransportStreamableHTTP},
				"miro":   {URL: "https://mcp.miro.com/", Transport: mcpRemoteTransportStreamableHTTP},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"linear", "miro"} {
		if err := env.Store.SaveOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, name), credentials.OAuthTokens{
			AccessToken: "token-" + name,
			TokenType:   "Bearer",
		}); err != nil {
			t.Fatal(err)
		}
	}

	help := runRootForRemoteTest(t, env, "rm", "--help")
	if !strings.Contains(help, "--project") {
		t.Fatalf("expected remove help to include --project, got:\n%s", help)
	}
	if strings.Contains(help, "--local") {
		t.Fatalf("expected remove help not to include --local, got:\n%s", help)
	}

	output := runRootForRemoteTest(t, env, "rm", "miro", "linear", "--project")
	for _, want := range []string{
		"removed toolbox miro",
		"removed toolbox linear",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected remove output to contain %q, got:\n%s", want, output)
		}
	}
	config, err := readToolmuxConfigFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.MCP.Servers) != 0 {
		t.Fatalf("expected all project MCP servers removed, got %#v", config.MCP.Servers)
	}
	for _, name := range []string{"linear", "miro"} {
		_, err := env.Store.LoadOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, name))
		if !errors.Is(err, credentials.ErrNotFound) {
			t.Fatalf("expected stored auth for %s to be removed, got %v", name, err)
		}
	}

	if err := env.Store.SaveOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, "linear"), credentials.OAuthTokens{
		AccessToken: "stale-token",
		TokenType:   "Bearer",
	}); err != nil {
		t.Fatal(err)
	}
	authRemoveOutput := runRootForRemoteTest(t, env, "mcp", "auth", "rm", "linear")
	if !strings.Contains(authRemoveOutput, "removed stored auth for MCP server linear") {
		t.Fatalf("expected stale auth removal output, got %q", authRemoveOutput)
	}
	_, err = env.Store.LoadOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, "linear"))
	if !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("expected stale stored auth to be removed, got %v", err)
	}
}

func TestMCPRemoteServerRegistrationRejectsNativeCommandCollision(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	upstream := newFakeMCPRemoteServer(t, nil)
	defer upstream.Close()

	cmd := rootForRemoteTest(env)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"add", upstream.URL, "--name", "status", "--global"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `MCP server name "status" conflicts`) {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestMCPRemoteServerStartupConflictPrintsRenameCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	writeRemoteTestConfig(t, env, map[string]mcpRemoteServer{
		"status": {URL: "https://example.com/mcp", Transport: mcpRemoteTransportStreamableHTTP},
	})

	cmd := rootForRemoteTest(env)
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "toolmux mcp rename status <new-name>") {
		t.Fatalf("expected rename guidance, got %v", err)
	}

	out := runRootForRemoteTest(t, env, "mcp", "rename", "status", "status2")
	if !strings.Contains(out, "renamed MCP server status to status2") {
		t.Fatalf("expected rename output, got %q", out)
	}
}

func TestMCPRemoteServerNotionDoesNotConflictWithoutNativeCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	writeRemoteTestConfig(t, env, map[string]mcpRemoteServer{
		"notion": {URL: "https://mcp.notion.com/mcp", Transport: mcpRemoteTransportStreamableHTTP},
	})

	output := runRootForRemoteTest(t, env, "--help")
	if !strings.Contains(output, "notion") {
		t.Fatalf("expected imported notion command in help, got %q", output)
	}

	cmd := rootForRemoteTest(env)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected imported notion server not to conflict with native commands: %v", err)
	}
}
