//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func TestMCPRemoteServerSyncAndTopLevelCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "mcp", "add", "linear", upstream.URL, "--global")
	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "linear create_issue")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected remote command policy check, got %q", policyOutput)
	}

	output := runRootForRemoteTest(t, env,
		"linear", "create_issue",
		"--title", "Bug",
		"--draft",
		"--labels", "backend",
		"--labels", "urgent",
	)
	if !strings.Contains(output, "called create_issue: Bug") {
		t.Fatalf("expected remote tool output, got %q", output)
	}
	if called["title"] != "Bug" || called["draft"] != true {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
	labels, ok := called["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "backend" || labels[1] != "urgent" {
		t.Fatalf("unexpected labels argument: %#v", called["labels"])
	}
}

func TestMCPBuiltinRemoteServersUseOAuthReadyEndpoints(t *testing.T) {
	server, ok := mcpBuiltinRemoteServers()["atlassian"]
	if !ok {
		t.Fatal("expected atlassian built-in MCP server")
	}
	if strings.TrimSpace(server.URL) == "" {
		t.Fatal("expected atlassian MCP endpoint")
	}
	metadataURL, err := wellKnownOAuthMetadataURL(server.URL, "oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(metadataURL) == "" {
		t.Fatal("expected atlassian protected resource metadata URL")
	}
	if os.Getenv("TOOLMUX_LIVE_TESTS") != "1" {
		t.Skip("set TOOLMUX_LIVE_TESTS=1 to check Atlassian protected resource metadata")
	}
	resp, err := http.Get(metadataURL) // #nosec G107 -- live test URL is derived from a built-in MCP endpoint.
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("expected atlassian protected resource metadata to return 2xx, got %d", resp.StatusCode)
	}
}

func TestMCPRemoteAddURLRequiresNameAndSupportsNameURLForm(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	output, err := runRootForRemoteTestError(t, env, "mcp", "add", upstream.URL, "--global")
	if err == nil {
		t.Fatalf("expected missing name error, got output:\n%s", output)
	}
	if !strings.Contains(err.Error(), "MCP server name is required when adding a URL") {
		t.Fatalf("unexpected missing name error: %v", err)
	}

	addOutput := runRootForRemoteTest(t, env, "mcp", "add", "custom", upstream.URL, "--global")
	for _, want := range []string{
		"registered global MCP server custom",
		"synced MCP server custom: 2 tools",
	} {
		if !strings.Contains(addOutput, want) {
			t.Fatalf("expected URL add output to contain %q, got:\n%s", want, addOutput)
		}
	}
	output = runRootForRemoteTest(t, env, "custom", "create_issue", "--title", "URL")
	if !strings.Contains(output, "called create_issue: URL") {
		t.Fatalf("expected custom remote tool output, got %q", output)
	}
}

func TestMCPRemoteCatalogListsAndTogglesBuiltins(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	output := runRootForRemoteTest(t, env, "mcp", "catalog")
	for _, want := range []string{
		"iterate",
		"available",
		"notion",
		"alias required",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected catalog output to contain %q, got:\n%s", want, output)
		}
	}

	enableOutput := runRootForRemoteTest(t, env, "mcp", "catalog", "--enable", "iterate", "--global")
	if !strings.Contains(enableOutput, "enabled global MCP server iterate") {
		t.Fatalf("expected enable output, got %q", enableOutput)
	}
	jsonOutput := runRootForRemoteTest(t, env, "--output", "json", "mcp", "catalog")
	var entries []mcpRemoteCatalogEntry
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		t.Fatalf("decode catalog output: %v\n%s", err, jsonOutput)
	}
	var iterate mcpRemoteCatalogEntry
	for _, entry := range entries {
		if entry.Name == "iterate" {
			iterate = entry
		}
	}
	if !iterate.Registered || iterate.Status != "registered" || iterate.Scope != "global" {
		t.Fatalf("expected registered iterate catalog entry, got %#v", iterate)
	}
	if len(iterate.RegisteredNames) != 1 || iterate.RegisteredNames[0] != "iterate" {
		t.Fatalf("expected iterate registered name, got %#v", iterate.RegisteredNames)
	}

	disableOutput := runRootForRemoteTest(t, env, "mcp", "catalog", "--disable", "iterate")
	if !strings.Contains(disableOutput, "disabled MCP server iterate") {
		t.Fatalf("expected disable output, got %q", disableOutput)
	}
	output = runRootForRemoteTest(t, env, "mcp", "catalog")
	if !strings.Contains(output, "iterate") || !strings.Contains(output, "available") {
		t.Fatalf("expected iterate to be available after disable, got:\n%s", output)
	}

	cmd := rootForRemoteTest(env)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"mcp", "catalog", "--enable", "notion"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `MCP server name "notion" conflicts`) {
		t.Fatalf("expected native conflict enabling notion, got %v", err)
	}

	aliasOutput := runRootForRemoteTest(t, env, "mcp", "catalog", "--enable", "notion=notion-mcp", "--global")
	if !strings.Contains(aliasOutput, "enabled global MCP server notion as notion-mcp") {
		t.Fatalf("expected alias enable output, got %q", aliasOutput)
	}
	jsonOutput = runRootForRemoteTest(t, env, "--output", "json", "mcp", "catalog")
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		t.Fatalf("decode catalog output: %v\n%s", err, jsonOutput)
	}
	var notion mcpRemoteCatalogEntry
	for _, entry := range entries {
		if entry.Name == "notion" {
			notion = entry
		}
	}
	if !notion.Registered || notion.Status != "registered" || len(notion.RegisteredNames) != 1 || notion.RegisteredNames[0] != "notion-mcp" {
		t.Fatalf("expected notion registered as alias, got %#v", notion)
	}
	disableOutput = runRootForRemoteTest(t, env, "mcp", "catalog", "--disable", "notion")
	if !strings.Contains(disableOutput, "disabled MCP server notion-mcp") {
		t.Fatalf("expected alias disable by catalog name, got %q", disableOutput)
	}

	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "mcp catalog --enable iterate")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected catalog manage policy check, got %q", policyOutput)
	}
}

func TestMCPRemoteListShowsToolsAndTree(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	addOutput := runRootForRemoteTest(t, env, "mcp", "add", "linear", upstream.URL)
	if !strings.Contains(addOutput, "registered project MCP server linear") {
		t.Fatalf("expected project registration output, got %q", addOutput)
	}

	listOutput := runRootForRemoteTest(t, env, "--color", "always", "mcp", "ls")
	for _, want := range []string{"linear", "synced", "project", "2"} {
		if !strings.Contains(listOutput, want) {
			t.Fatalf("expected mcp ls output to contain %q, got:\n%s", want, listOutput)
		}
	}
	if strings.Contains(listOutput, "local") {
		t.Fatalf("expected mcp ls output to use project scope, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "\x1b[") {
		t.Fatalf("expected mcp ls output to include color when forced, got:\n%s", listOutput)
	}

	jsonOutput := runRootForRemoteTest(t, env, "-o", "json", "mcp", "ls")
	var listItems []mcpRemoteListItem
	if err := json.Unmarshal([]byte(jsonOutput), &listItems); err != nil {
		t.Fatalf("decode mcp ls json output: %v\n%s", err, jsonOutput)
	}
	if len(listItems) != 1 || listItems[0].Name != "linear" || listItems[0].Scope != "project" || listItems[0].Status != "synced" {
		t.Fatalf("unexpected mcp ls json output: %+v", listItems)
	}

	toolOutput := runRootForRemoteTest(t, env, "mcp", "ls", "linear")
	for _, want := range []string{"create_issue", "calculate", "a*, b*, operation*", "title"} {
		if !strings.Contains(toolOutput, want) {
			t.Fatalf("expected mcp ls linear output to contain %q, got:\n%s", want, toolOutput)
		}
	}

	treeOutput := runRootForRemoteTest(t, env, "mcp", "ls", "-R")
	for _, want := range []string{"linear", "+-- calculate", "`-- create_issue"} {
		if !strings.Contains(treeOutput, want) {
			t.Fatalf("expected recursive mcp ls output to contain %q, got:\n%s", want, treeOutput)
		}
	}

	serverTreeOutput := runRootForRemoteTest(t, env, "mcp", "ls", "-R", "linear")
	for _, want := range []string{"linear", "+-- calculate", "`-- create_issue"} {
		if !strings.Contains(serverTreeOutput, want) {
			t.Fatalf("expected recursive mcp ls linear output to contain %q, got:\n%s", want, serverTreeOutput)
		}
	}
}

func TestMCPRemoteToolCommandsUseInputSchema(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "mcp", "add", "linear", upstream.URL, "--global")

	help := runRootForRemoteTest(t, env, "linear", "calculate", "-h")
	for _, want := range []string{
		"--a float",
		"--b float",
		"--operation string",
		"-v, --verbose",
		"toolmux schema linear calculate",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected help to contain %q, got:\n%s", want, help)
		}
	}
	if strings.Contains(help, "Input Schema:") || strings.Contains(help, `"required": [`) {
		t.Fatalf("expected help to omit full input schema, got:\n%s", help)
	}

	schemaOutput := runRootForRemoteTest(t, env, "schema", "linear", "calculate")
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaOutput), &schema); err != nil {
		t.Fatalf("decode schema output: %v\n%s", err, schemaOutput)
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["a"]; !ok || schema["type"] != "object" {
		t.Fatalf("unexpected schema output: %#v", schema)
	}
	dottedSchemaOutput := runRootForRemoteTest(t, env, "schema", "linear.calculate")
	if dottedSchemaOutput != schemaOutput {
		t.Fatalf("expected dotted schema lookup to match two-arg lookup:\n%s\n---\n%s", dottedSchemaOutput, schemaOutput)
	}
	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "schema linear.calculate")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected schema command policy check, got %q", policyOutput)
	}

	cmd := rootForRemoteTest(env)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"linear", "calculate", "--operation", "add"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing required MCP tool arguments: a, b") {
		t.Fatalf("expected missing required arguments error, got %v", err)
	}

	output := runRootForRemoteTest(t, env,
		"linear", "calculate",
		"--operation", "multiply",
		"--a", "6",
		"--b", "7",
	)
	if !strings.Contains(output, "called calculate: multiply") {
		t.Fatalf("expected remote calculate output, got %q", output)
	}
	if called["operation"] != "multiply" || called["a"] != float64(6) || called["b"] != float64(7) {
		t.Fatalf("unexpected calculate arguments: %#v", called)
	}
}

func TestMCPRemoteToolVerbosePrintsHTTPTrace(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServerWithBearer(t, &called, "secret-token")
	defer upstream.Close()

	runRootForRemoteTest(t, env, "mcp", "add", "linear", upstream.URL, "--global", "--no-sync")
	runRootForRemoteTestWithInput(t, env, "secret-token", "mcp", "auth", "set", "linear", "--bearer-token-stdin")
	runRootForRemoteTest(t, env, "mcp", "sync", "linear")

	output := runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "Trace", "-v")
	for _, want := range []string{
		"----- MCP HTTP request -----",
		"----- MCP HTTP response -----",
		"Authorization: <redacted>",
		`"method":"tools/call"`,
		"called create_issue: Trace",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected verbose output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("verbose output leaked bearer token:\n%s", output)
	}
}

func TestMCPRemoteServerExposesCachedToolsOverMCPServe(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "mcp", "add", "linear", upstream.URL, "--global")

	listOutput := runRootForRemoteTestWithInput(t, env,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		"mcp", "serve",
	)
	response := decodeMCPTestResponse(t, listOutput)
	if response.Error != nil {
		t.Fatalf("unexpected MCP error: %+v", response.Error)
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &list); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tool := range list.Tools {
		if tool.Name == "linear.create_issue" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected linear.create_issue in tools/list, got %+v", list.Tools)
	}

	callOutput := runRootForRemoteTestWithInput(t, env,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"linear.create_issue","arguments":{"title":"From MCP"}}}`,
		"mcp", "serve",
	)
	result := decodeMCPCallResult(t, callOutput)
	if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "From MCP") {
		t.Fatalf("unexpected remote MCP call result: %+v", result)
	}
}

func TestMCPRemoteServerUsesStoredBearerToken(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServerWithBearer(t, &called, "secret-token")
	defer upstream.Close()

	runRootForRemoteTest(t, env, "mcp", "add", "linear", upstream.URL, "--global", "--no-sync")
	runRootForRemoteTestWithInput(t, env, "secret-token", "mcp", "auth", "set", "linear", "--bearer-token-stdin")
	authStatus := runRootForRemoteTest(t, env, "mcp", "auth", "status", "linear")
	if !strings.Contains(authStatus, "stored bearer token") {
		t.Fatalf("expected stored auth status, got %q", authStatus)
	}
	runRootForRemoteTest(t, env, "mcp", "sync", "linear")

	output := runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "Authenticated")
	if !strings.Contains(output, "called create_issue: Authenticated") {
		t.Fatalf("expected authenticated remote tool output, got %q", output)
	}
	if called["title"] != "Authenticated" {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
}

func TestMCPRemoteRemoveMultipleServersUsesProjectFlag(t *testing.T) {
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

	help := runRootForRemoteTest(t, env, "mcp", "rm", "--help")
	if !strings.Contains(help, "--project") {
		t.Fatalf("expected remove help to include --project, got:\n%s", help)
	}
	if strings.Contains(help, "--local") {
		t.Fatalf("expected remove help not to include --local, got:\n%s", help)
	}

	output := runRootForRemoteTest(t, env, "mcp", "rm", "miro", "linear", "--project")
	for _, want := range []string{
		"removed MCP server miro",
		"removed MCP server linear",
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
}

func TestMCPRemoteServerOAuthLoginAndRefresh(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteOAuthServer(t, &called)
	defer upstream.Close()

	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "mcp auth login linear")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected OAuth auth policy check, got %q", policyOutput)
	}
	addOutput := runRootForRemoteOAuthTest(t, env, upstream.Client(), "mcp", "add", "linear", upstream.URL+"/mcp", "--global")
	for _, want := range []string{
		"registered global MCP server linear",
		"MCP server linear requires auth; starting OAuth login",
		"stored OAuth token for MCP server linear",
		"synced MCP server linear: 1 tools",
	} {
		if !strings.Contains(addOutput, want) {
			t.Fatalf("expected OAuth add output to contain %q, got:\n%s", want, addOutput)
		}
	}
	authStatus := runRootForRemoteTest(t, env, "mcp", "auth", "status", "linear")
	if !strings.Contains(authStatus, "stored OAuth token") {
		t.Fatalf("expected stored OAuth status, got %q", authStatus)
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

	output, err := runRootForRemoteTestError(t, env, "mcp", "add", "linear", upstream.URL, "--global")
	if err == nil {
		t.Fatalf("expected OAuth add failure, got output:\n%s", output)
	}
	if strings.Contains(output, "registered global MCP server linear") {
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

func TestMCPRemoteServerSupportsSSEAndSessionID(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteSSESessionServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "mcp", "add", "iterate", upstream.URL, "--global")

	output := runRootForRemoteTest(t, env, "iterate", "create_issue", "--title", "SSE")
	if !strings.Contains(output, "called create_issue: SSE") {
		t.Fatalf("expected SSE remote tool output, got %q", output)
	}
	if called["title"] != "SSE" {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
}

func TestMCPRemoteServerRefreshesStaleCacheOnCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	var toolsListCalls int
	upstream := newFakeMCPRemoteServerWithToolListCounter(t, &called, &toolsListCalls)
	defer upstream.Close()
	writeRemoteTestConfig(t, env, map[string]mcpRemoteServer{
		"linear": {URL: upstream.URL, Transport: mcpRemoteTransportStreamableHTTP},
	})
	if err := writeMCPRemoteCache(env.CacheDir, "linear", mcpRemoteCache{
		Name:      "linear",
		URL:       upstream.URL,
		Transport: mcpRemoteTransportStreamableHTTP,
		Tools: []mcpRemoteTool{{
			Name:        "create_issue",
			Description: "Stale create issue",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
		}},
		SyncedAt: time.Now().Add(-25 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "Refreshed")
	if !strings.Contains(output, "called create_issue: Refreshed") {
		t.Fatalf("expected remote tool output, got %q", output)
	}
	if toolsListCalls == 0 {
		t.Fatal("expected stale cache to be refreshed before command execution")
	}
	if called["title"] != "Refreshed" {
		t.Fatalf("unexpected remote arguments: %#v", called)
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
	cmd.SetArgs([]string{"mcp", "add", "notion", upstream.URL, "--global"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `MCP server name "notion" conflicts`) {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestMCPRemoteServerCommandSurfaceIsFlat(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	cmd := rootForRemoteTest(env)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"mcp", "server", "add", "iterate"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected legacy mcp server command path to fail")
	}
}

func TestMCPRemoteServerStartupConflictPrintsRenameCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	writeRemoteTestConfig(t, env, map[string]mcpRemoteServer{
		"notion": {URL: "https://example.com/mcp", Transport: mcpRemoteTransportStreamableHTTP},
	})

	cmd := rootForRemoteTest(env)
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "toolmux mcp rename notion <new-name>") {
		t.Fatalf("expected rename guidance, got %v", err)
	}

	out := runRootForRemoteTest(t, env, "mcp", "rename", "notion", "notion2")
	if !strings.Contains(out, "renamed MCP server notion to notion2") {
		t.Fatalf("expected rename output, got %q", out)
	}
}

type mcpRemoteTestEnv struct {
	Home     string
	Config   string
	CacheDir string
	Store    *credentials.MemoryStore
}

func newMCPRemoteTestEnv(t *testing.T) mcpRemoteTestEnv {
	t.Helper()
	home := t.TempDir()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TOOLMUX_MCP_CACHE_DIR", cacheDir)
	config, err := globalToolmuxConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return mcpRemoteTestEnv{Home: home, Config: config, CacheDir: cacheDir, Store: credentials.NewMemoryStore()}
}

func rootForRemoteTest(env mcpRemoteTestEnv) *cobra.Command {
	return NewRootCommandWithDeps(Dependencies{
		Credentials: env.Store,
		Env: func(name string) string {
			if name == "TOOLMUX_MCP_CACHE_DIR" {
				return env.CacheDir
			}
			return os.Getenv(name)
		},
	})
}

func runRootForRemoteTest(t *testing.T, env mcpRemoteTestEnv, args ...string) string {
	t.Helper()
	return runRootForRemoteTestWithInput(t, env, "", args...)
}

func runRootForRemoteTestWithInput(t *testing.T, env mcpRemoteTestEnv, input string, args ...string) string {
	t.Helper()
	cmd := rootForRemoteTest(env)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	if input != "" {
		cmd.SetIn(strings.NewReader(input + "\n"))
	}
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func runRootForRemoteTestError(t *testing.T, env mcpRemoteTestEnv, args ...string) (string, error) {
	t.Helper()
	cmd := rootForRemoteTest(env)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func runRootForRemoteOAuthTest(t *testing.T, env mcpRemoteTestEnv, client *http.Client, args ...string) string {
	t.Helper()
	cmd := rootForRemoteTest(env)
	out := newOAuthFollowerWriter(t, client)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out.wait()
	return out.String()
}

type oauthFollowerWriter struct {
	t      *testing.T
	client *http.Client

	mu   sync.Mutex
	buf  bytes.Buffer
	once sync.Once
	done chan error
}

func newOAuthFollowerWriter(t *testing.T, client *http.Client) *oauthFollowerWriter {
	t.Helper()
	if client == nil {
		client = http.DefaultClient
	}
	return &oauthFollowerWriter{t: t, client: client, done: make(chan error, 1)}
}

func (w *oauthFollowerWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buf.Write(data)
	text := w.buf.String()
	authURL := firstHTTPURL(text)
	w.mu.Unlock()
	if authURL != "" {
		w.once.Do(func() {
			go func() {
				resp, err := w.client.Get(authURL)
				if err != nil {
					w.done <- err
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				err = resp.Body.Close()
				if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
					err = fmt.Errorf("authorization URL returned status %d", resp.StatusCode)
				}
				w.done <- err
			}()
		})
	}
	return len(data), nil
}

func (w *oauthFollowerWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *oauthFollowerWriter) wait() {
	w.t.Helper()
	select {
	case err := <-w.done:
		if err != nil {
			w.t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		w.t.Fatal("timed out following OAuth authorization URL")
	}
}

func firstHTTPURL(text string) string {
	for field := range strings.FieldsSeq(text) {
		field = strings.TrimRight(field, ".,)")
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			return field
		}
	}
	return ""
}

func writeRemoteTestConfig(t *testing.T, env mcpRemoteTestEnv, servers map[string]mcpRemoteServer) {
	t.Helper()
	if err := writeToolmuxConfigFile(env.Config, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			Servers: servers,
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func newFakeMCPRemoteServer(t *testing.T, called *map[string]any) *httptest.Server {
	t.Helper()
	return newFakeMCPRemoteServerWithBearer(t, called, "")
}

func newFakeMCPRemoteServerWithBearer(t *testing.T, called *map[string]any, bearerToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if bearerToken != "" && r.Header.Get("Authorization") != "Bearer "+bearerToken {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"serverInfo": map[string]any{
						"name": "fake-linear",
					},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool(), fakeMCPCalculateTool()},
				},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode call params: %v", err)
			}
			if called != nil {
				*called = params.Arguments
			}
			text := ""
			switch params.Name {
			case "create_issue":
				text = "called create_issue: " + params.Arguments["title"].(string)
			case "calculate":
				text = "called calculate: " + params.Arguments["operation"].(string)
			default:
				t.Fatalf("unexpected tool name %q", params.Name)
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: text,
					}},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcpError{Code: -32601, Message: "method not found"},
			})
		}
	}))
}

func newFakeMCPRemoteOAuthRequiredWithoutMetadataServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Bearer realm="OAuth", error="invalid_token"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_token"}`)
	}))
}

func newFakeMCPRemoteOAuthServer(t *testing.T, called *map[string]any) *httptest.Server {
	t.Helper()
	type authCode struct {
		ClientID      string
		RedirectURI   string
		Resource      string
		CodeChallenge string
	}
	var serverURL string
	codes := map[string]authCode{}
	accessTokens := map[string]bool{
		"oauth-access-1": true,
		"oauth-access-2": true,
	}
	refreshCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpURL := serverURL + "/mcp"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              mcpURL,
				"authorization_servers": []string{serverURL},
				"scopes_supported":      []string{"tools.read"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                serverURL,
				"authorization_endpoint":                serverURL + "/authorize",
				"token_endpoint":                        serverURL + "/token",
				"registration_endpoint":                 serverURL + "/register",
				"response_types_supported":              []string{"code"},
				"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
				"code_challenge_methods_supported":      []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/register":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode registration: %v", err)
			}
			redirects, _ := req["redirect_uris"].([]any)
			if len(redirects) != 1 || redirects[0] == "" {
				t.Fatalf("unexpected redirect_uris in registration: %#v", req)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": "toolmux-test-client",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/authorize":
			query := r.URL.Query()
			if query.Get("client_id") != "toolmux-test-client" {
				t.Fatalf("unexpected client_id %q", query.Get("client_id"))
			}
			if query.Get("resource") != mcpURL {
				t.Fatalf("unexpected resource %q", query.Get("resource"))
			}
			if scope := query.Get("scope"); scope != "" && scope != "tools.read" {
				t.Fatalf("unexpected scope %q", query.Get("scope"))
			}
			if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
				t.Fatalf("missing PKCE challenge: %s", r.URL.RawQuery)
			}
			code := "code-1"
			codes[code] = authCode{
				ClientID:      query.Get("client_id"),
				RedirectURI:   query.Get("redirect_uri"),
				Resource:      query.Get("resource"),
				CodeChallenge: query.Get("code_challenge"),
			}
			redirect, err := url.Parse(query.Get("redirect_uri"))
			if err != nil {
				t.Fatalf("parse redirect URI: %v", err)
			}
			redirectQuery := redirect.Query()
			redirectQuery.Set("code", code)
			redirectQuery.Set("state", query.Get("state"))
			redirect.RawQuery = redirectQuery.Encode()
			http.Redirect(w, r, redirect.String(), http.StatusFound)
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				code := r.Form.Get("code")
				issued, ok := codes[code]
				if !ok {
					t.Fatalf("unexpected code %q", code)
				}
				if r.Form.Get("client_id") != issued.ClientID || r.Form.Get("redirect_uri") != issued.RedirectURI || r.Form.Get("resource") != issued.Resource {
					t.Fatalf("unexpected token request form: %#v", r.Form)
				}
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != issued.CodeChallenge {
					t.Fatalf("unexpected PKCE verifier challenge %q, want %q", got, issued.CodeChallenge)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "oauth-access-1",
					"refresh_token": "oauth-refresh-1",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"scope":         "tools.read",
				})
			case "refresh_token":
				if r.Form.Get("client_id") != "toolmux-test-client" || r.Form.Get("refresh_token") != "oauth-refresh-1" || r.Form.Get("resource") != mcpURL {
					t.Fatalf("unexpected refresh request form: %#v", r.Form)
				}
				refreshCount++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "oauth-access-2",
					"refresh_token": "oauth-refresh-2",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"scope":         "tools.read",
				})
			default:
				t.Fatalf("unexpected grant_type %q", r.Form.Get("grant_type"))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !accessTokens[token] {
				w.Header().Add("WWW-Authenticate", `Bearer resource_metadata="`+serverURL+`/.well-known/oauth-protected-resource/mcp"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var req mcpRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			switch req.Method {
			case "initialize":
				_ = json.NewEncoder(w).Encode(mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"protocolVersion": mcpProtocolVersion,
						"serverInfo": map[string]any{
							"name": "fake-oauth-linear",
						},
					},
				})
			case "tools/list":
				_ = json.NewEncoder(w).Encode(mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"tools": []map[string]any{fakeMCPCreateIssueTool()},
					},
				})
			case "tools/call":
				var params struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Fatalf("decode call params: %v", err)
				}
				if called != nil {
					*called = params.Arguments
				}
				_ = json.NewEncoder(w).Encode(mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: mcpCallToolResult{
						Content: []mcpContent{{
							Type: "text",
							Text: "called create_issue: " + params.Arguments["title"].(string),
						}},
					},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			default:
				_ = json.NewEncoder(w).Encode(mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &mcpError{Code: -32601, Message: "method not found"},
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	t.Cleanup(func() {
		if refreshCount == 0 {
			t.Error("expected OAuth refresh token flow to be exercised")
		}
	})
	return server
}

func newFakeMCPRemoteServerWithToolListCounter(t *testing.T, called *map[string]any, toolsListCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"serverInfo": map[string]any{
						"name": "fake-linear",
					},
				},
			})
		case "tools/list":
			(*toolsListCalls)++
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode call params: %v", err)
			}
			if called != nil {
				*called = params.Arguments
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "called create_issue: " + params.Arguments["title"].(string),
					}},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcpError{Code: -32601, Message: "method not found"},
			})
		}
	}))
}

func newFakeMCPRemoteSSESessionServer(t *testing.T, called *map[string]any) *httptest.Server {
	t.Helper()
	sessionCounter := 0
	sessions := map[string]bool{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			sessionCounter++
			sessionID := fmt.Sprintf("session-%d", sessionCounter)
			sessions[sessionID] = true
			w.Header().Set("Mcp-Session-Id", sessionID)
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"serverInfo": map[string]any{
						"name": "fake-sse",
					},
				},
			})
		case "notifications/initialized":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		case "tools/call":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode call params: %v", err)
			}
			if params.Name != "create_issue" {
				t.Fatalf("unexpected tool name %q", params.Name)
			}
			if called != nil {
				*called = params.Arguments
			}
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "called create_issue: " + params.Arguments["title"].(string),
					}},
				},
			})
		default:
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcpError{Code: -32601, Message: "method not found"},
			})
		}
	}))
}

func fakeMCPCreateIssueTool() map[string]any {
	return map[string]any{
		"name":        "create_issue",
		"description": "Create an issue",
		"inputSchema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Issue title",
				},
				"draft": map[string]any{
					"type":        "boolean",
					"description": "Create as draft",
				},
				"labels": map[string]any{
					"type":        "array",
					"description": "Labels",
					"items":       map[string]any{"type": "string"},
				},
			},
		},
	}
}

func fakeMCPCalculateTool() map[string]any {
	return map[string]any{
		"name":        "calculate",
		"description": "Performs basic arithmetic operations",
		"inputSchema": map[string]any{
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"operation", "a", "b"},
			"properties": map[string]any{
				"a": map[string]any{
					"type":        "number",
					"description": "First operand",
				},
				"b": map[string]any{
					"type":        "number",
					"description": "Second operand",
				},
				"operation": map[string]any{
					"type":        "string",
					"description": "Operation to perform",
					"enum":        []string{"add", "subtract", "multiply", "divide"},
				},
			},
		},
	}
}

func writeFakeMCPSSE(t *testing.T, w http.ResponseWriter, response mcpResponse) {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
}
