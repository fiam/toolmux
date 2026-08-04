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
	if err == nil || !strings.Contains(err.Error(), "toolmux rename status <new-name>") {
		t.Fatalf("expected rename guidance, got %v", err)
	}

	out := runRootForRemoteTest(t, env, "mcp", "rename", "status", "status2")
	if !strings.Contains(out, "renamed MCP server status to status2") {
		t.Fatalf("expected rename output, got %q", out)
	}
}

func TestToolboxRenameMovesRemoteAuthCacheAndLabel(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	server := mcpRemoteServer{URL: "https://mcp.notion.com/mcp", Transport: mcpRemoteTransportStreamableHTTP}
	config := toolmuxConfigFile{
		Version: 1,
		Toolboxes: map[string]toolboxConfig{
			"notion": withToolboxLabel(toolboxConfigFromMCPRemoteServer(server, "notion"), "Work workspace"),
		},
	}
	if err := writeToolmuxConfigFile(env.Config, config); err != nil {
		t.Fatal(err)
	}
	oldRef := mcpRemoteCredentialRef(&options{profile: "default"}, "notion")
	if err := env.Store.SaveOAuthTokens(context.Background(), oldRef, credentials.OAuthTokens{
		AccessToken: "notion-token",
		Extra:       map[string]string{"mcp_server": "notion"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPRemoteCache(env.CacheDir, "notion", mcpRemoteCache{
		Name: "notion",
		URL:  server.URL,
		Tools: []mcpRemoteTool{
			{Name: "notion-get-users"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "rename", "notion", "notion-work")
	if !strings.Contains(output, "renamed toolbox notion to notion-work") {
		t.Fatalf("unexpected rename output: %q", output)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := config.Toolboxes["notion"]; exists {
		t.Fatal("old toolbox config still exists")
	}
	if got := config.Toolboxes["notion-work"].Label; got != "Work workspace" {
		t.Fatalf("renamed toolbox label = %q", got)
	}
	newRef := mcpRemoteCredentialRef(&options{profile: "default"}, "notion-work")
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), newRef)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "notion-token" || tokens.Extra["mcp_server"] != "notion-work" {
		t.Fatalf("unexpected renamed tokens: %#v", tokens)
	}
	if _, err := env.Store.LoadOAuthTokens(context.Background(), oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("old stored auth still exists: %v", err)
	}
	if cache, ok, err := readMCPRemoteCacheIfExists(env.CacheDir, "notion-work"); err != nil || !ok || cache.Name != "notion-work" {
		t.Fatalf("renamed cache missing: ok=%v err=%v", ok, err)
	}
}

func TestToolboxRenameKeepsAuthAndCacheForOtherConfigScope(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	server := mcpRemoteServer{URL: "https://mcp.notion.com/mcp", Transport: mcpRemoteTransportStreamableHTTP}
	globalConfig := toolmuxConfigFile{Version: 1, Toolboxes: map[string]toolboxConfig{
		"notion": toolboxConfigFromMCPRemoteServer(server, "notion"),
	}}
	if err := writeToolmuxConfigFile(env.Config, globalConfig); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	projectPath := filepath.Join(projectDir, toolmuxConfigRelPath)
	if err := writeToolmuxConfigFile(projectPath, globalConfig); err != nil {
		t.Fatal(err)
	}
	oldRef := mcpRemoteCredentialRef(&options{profile: "default"}, "notion")
	if err := env.Store.SaveOAuthTokens(context.Background(), oldRef, credentials.OAuthTokens{AccessToken: "notion-token"}); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPRemoteCache(env.CacheDir, "notion", mcpRemoteCache{Name: "notion", URL: server.URL}); err != nil {
		t.Fatal(err)
	}

	runRootForRemoteTest(t, env, "rename", "notion", "notion-work", "--project")
	if _, err := env.Store.LoadOAuthTokens(context.Background(), oldRef); err != nil {
		t.Fatalf("global registration lost shared auth: %v", err)
	}
	newRef := mcpRemoteCredentialRef(&options{profile: "default"}, "notion-work")
	if _, err := env.Store.LoadOAuthTokens(context.Background(), newRef); err != nil {
		t.Fatalf("renamed project registration missing copied auth: %v", err)
	}
	if _, ok, err := readMCPRemoteCacheIfExists(env.CacheDir, "notion"); err != nil || !ok {
		t.Fatalf("global registration lost shared cache: ok=%v err=%v", ok, err)
	}
	if cache, ok, err := readMCPRemoteCacheIfExists(env.CacheDir, "notion-work"); err != nil || !ok || cache.Name != "notion-work" {
		t.Fatalf("project registration missing copied cache: cache=%#v ok=%v err=%v", cache, ok, err)
	}
}

func TestToolboxRenameMovesNativeAuthAndLabel(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	config := toolmuxConfigFile{
		Version: 1,
		Toolboxes: map[string]toolboxConfig{
			"slack-work": {Type: toolboxTypeInternal, Provider: "slack", Label: "Work workspace"},
		},
	}
	if err := writeToolmuxConfigFile(env.Config, config); err != nil {
		t.Fatal(err)
	}
	oldRef := credentials.ConnectionRef{Profile: "default", Provider: "slack", AccountID: "slack-work"}
	if err := env.Store.SaveOAuthTokens(context.Background(), oldRef, credentials.OAuthTokens{AccessToken: "slack-token"}); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "rename", "slack-work", "slack-personal", "--label", "Personal workspace")
	if !strings.Contains(output, "renamed toolbox slack-work to slack-personal") {
		t.Fatalf("unexpected rename output: %q", output)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Toolboxes["slack-personal"].Label; got != "Personal workspace" {
		t.Fatalf("renamed native toolbox label = %q", got)
	}
	newRef := credentials.ConnectionRef{Profile: "default", Provider: "slack", AccountID: "slack-personal"}
	if _, err := env.Store.LoadOAuthTokens(context.Background(), newRef); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Store.LoadOAuthTokens(context.Background(), oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("old native stored auth still exists: %v", err)
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
