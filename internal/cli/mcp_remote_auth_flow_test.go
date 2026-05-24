//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func TestMCPRemoteServerOAuthLoginAndRefresh(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteOAuthServer(t, &called)
	defer upstream.Close()

	_, policyErr := runRootForRemoteTestError(t, env, "policy", "check", "--command", "mcp auth login linear")
	if policyErr == nil || !strings.Contains(policyErr.Error(), "no command spec found") {
		t.Fatalf("expected OAuth auth command outside policy, got %v", policyErr)
	}
	addOutput := runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	for _, want := range []string{
		"registered global toolbox linear",
		"MCP server linear requires auth; starting OAuth login",
		"stored OAuth token for MCP server linear",
		"synced toolbox linear: 1 tools",
	} {
		if !strings.Contains(addOutput, want) {
			t.Fatalf("expected OAuth add output to contain %q, got:\n%s", want, addOutput)
		}
	}
	authStatus := runRootForRemoteTest(t, env, "mcp", "auth", "status", "linear")
	if !strings.Contains(authStatus, "stored OAuth token") {
		t.Fatalf("expected stored OAuth status, got %q", authStatus)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := configMCPRemoteServer(config, "linear")
	if !ok {
		t.Fatal("expected linear server config")
	}
	if server.AuthRequired == nil || !*server.AuthRequired {
		t.Fatalf("expected OAuth server to record auth_required true, got %#v", server)
	}

	output := runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "OAuth")
	if !strings.Contains(output, "called create_issue: OAuth") {
		t.Fatalf("expected OAuth remote tool output, got %q", output)
	}

	tokens, err := env.Store.LoadOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, "linear"))
	if err != nil {
		t.Fatal(err)
	}
	tokens.ExpiresAt = time.Now().Add(-time.Hour)
	if err := env.Store.SaveOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, "linear"), tokens); err != nil {
		t.Fatal(err)
	}
	output = runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "Refreshed")
	if !strings.Contains(output, "called create_issue: Refreshed") {
		t.Fatalf("expected refreshed OAuth remote tool output, got %q", output)
	}
	if called["title"] != "Refreshed" {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
}

func TestMCPRemoteAddDoesNotRegisterWhenOAuthLoginFails(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	upstream := newFakeMCPRemoteOAuthRequiredWithoutMetadataServer(t)
	defer upstream.Close()

	output, err := runRootForRemoteTestError(t, env, "add", upstream.URL, "--name", "linear", "--global")
	if err == nil {
		t.Fatalf("expected OAuth add failure, got output:\n%s", output)
	}
	if strings.Contains(output, "registered global toolbox linear") {
		t.Fatalf("expected failed OAuth add not to print registration, got:\n%s", output)
	}
	if !strings.Contains(output, "MCP server linear requires auth; starting OAuth login") {
		t.Fatalf("expected OAuth login attempt, got:\n%s", output)
	}
	for _, want := range []string{
		"initial sync failed for MCP server linear",
		"OAuth login failed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if _, ok, lookupErr := lookupMCPRemoteServer("linear", ""); lookupErr != nil {
		t.Fatal(lookupErr)
	} else if ok {
		t.Fatal("expected failed OAuth add not to register MCP server")
	}
}
