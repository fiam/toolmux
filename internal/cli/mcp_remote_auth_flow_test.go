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

	_, policyErr := runRootForRemoteTestError(t, env, "policy", "check", "--command", "auth login linear")
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
	authStatus := runRootForRemoteTest(t, env, "auth", "status", "linear")
	if !strings.Contains(authStatus, "OAuth auth stored") {
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

func TestMCPRemoteAuthRefreshCommandRefreshesExpiredOAuth(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteOAuthServer(t, &called)
	defer upstream.Close()

	runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	ref := mcpRemoteCredentialRef(&options{profile: "default"}, "linear")
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tokens.ExpiresAt = time.Now().Add(-time.Hour)
	if err := env.Store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "auth", "refresh", "linear")
	for _, want := range []string{"linear", "refreshed", "OAuth token refreshed and validated"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected refresh output to contain %q, got:\n%s", want, output)
		}
	}
	tokens, err = env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "oauth-access-2" {
		t.Fatalf("expected refreshed access token to be stored, got %q", tokens.AccessToken)
	}
}

func TestMCPRemoteAuthRefreshReauthsWhenRefreshMetadataMissing(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream, fixture := newFakeMCPRemoteOAuthServerWithRefreshExpectation(t, &called, false)
	defer upstream.Close()

	runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	ref := mcpRemoteCredentialRef(&options{profile: "default"}, "linear")
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tokens.ExpiresAt = time.Now().Add(-time.Hour)
	tokens.RefreshToken = ""
	delete(tokens.Extra, "token_endpoint")
	delete(tokens.Extra, "client_id")
	if err := env.Store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteOAuthTest(t, env, upstream.Client(), "auth", "refresh", "linear")
	if !strings.Contains(output, "OAuth re-authorized because stored OAuth token is missing refresh metadata") {
		t.Fatalf("expected refresh to re-authorize missing metadata, got:\n%s", output)
	}
	if fixture.refreshCount != 0 {
		t.Fatalf("expected re-auth without refresh-token flow, got %d refreshes", fixture.refreshCount)
	}
	tokens, err = env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken == "" || tokens.Extra["token_endpoint"] == "" || tokens.Extra["client_id"] == "" {
		t.Fatalf("expected re-auth to store refresh metadata, got %#v", tokens)
	}
}

func TestMCPRemoteCommandDoesNotImplicitlyReauthWithoutInteractiveTerminal(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream, fixture := newFakeMCPRemoteOAuthServerWithRefreshExpectation(t, &called, false)
	defer upstream.Close()

	runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	ref := mcpRemoteCredentialRef(&options{profile: "default"}, "linear")
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tokens.ExpiresAt = time.Now().Add(-time.Hour)
	tokens.RefreshToken = ""
	delete(tokens.Extra, "token_endpoint")
	delete(tokens.Extra, "client_id")
	if err := env.Store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}

	output, err := runRootForRemoteTestError(t, env, "linear", "create_issue", "--title", "Reauthed")
	if err == nil {
		t.Fatalf("expected non-interactive command to require explicit auth login, got output:\n%s", output)
	}
	if !strings.Contains(err.Error(), "stored OAuth token for toolbox linear is expired and missing refresh metadata") {
		t.Fatalf("expected missing metadata error, got %v", err)
	}
	if fixture.refreshCount != 0 {
		t.Fatalf("expected no refresh-token flow, got %d refreshes", fixture.refreshCount)
	}
	if called != nil {
		t.Fatalf("expected remote tool not to be called, got %#v", called)
	}
}

func TestMCPRemoteSyncRefreshesStoredOAuthAfterUnauthorized(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream, fixture := newFakeMCPRemoteOAuthServerWithRefreshExpectation(t, &called, true)
	defer upstream.Close()

	runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	fixture.accessTokens["oauth-access-1"] = false

	output := runRootForRemoteTest(t, env, "mcp", "sync", "linear")
	if !strings.Contains(output, "synced MCP server linear: 1 tools") {
		t.Fatalf("expected sync to refresh OAuth and succeed, got:\n%s", output)
	}
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), mcpRemoteCredentialRef(&options{profile: "default"}, "linear"))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "oauth-access-2" {
		t.Fatalf("expected refreshed access token to be stored, got %q", tokens.AccessToken)
	}
}

func TestDoctorFixRefreshesOAuthAndRepairsMissingCache(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteOAuthServer(t, &called)
	defer upstream.Close()

	runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	ref := mcpRemoteCredentialRef(&options{profile: "default"}, "linear")
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tokens.ExpiresAt = time.Now().Add(-time.Hour)
	if err := env.Store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}
	if err := removeMCPRemoteCache(env.CacheDir, "linear"); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "doctor", "--fix")
	for _, want := range []string{"linear", "toolbox-cache", "1 cached tools", "OAuth auth stored"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected doctor --fix output to contain %q, got:\n%s", want, output)
		}
	}
	tokens, err = env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "oauth-access-2" {
		t.Fatalf("expected doctor --fix to store refreshed access token, got %q", tokens.AccessToken)
	}
}

func TestDoctorDoesNotRefreshOAuthWithoutFix(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream, fixture := newFakeMCPRemoteOAuthServerWithRefreshExpectation(t, &called, false)
	defer upstream.Close()

	runRootForRemoteOAuthTest(t, env, upstream.Client(), "add", upstream.URL+"/mcp", "--name", "linear", "--global")
	ref := mcpRemoteCredentialRef(&options{profile: "default"}, "linear")
	tokens, err := env.Store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tokens.ExpiresAt = time.Now().Add(-time.Hour)
	if err := env.Store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "doctor")
	if !strings.Contains(output, "OAuth auth stored") {
		t.Fatalf("expected doctor output to show stored OAuth, got:\n%s", output)
	}
	if fixture.refreshCount != 0 {
		t.Fatalf("expected doctor without --fix not to refresh OAuth, got %d refreshes", fixture.refreshCount)
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
