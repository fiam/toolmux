package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
