//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func TestMCPRemoteServerSyncAndTopLevelCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global")
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

func TestMCPStdioServerAddAndExposeTools(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	addArgs := append([]string{"add", "--name", "localmcp", "--global", "--"}, mcpRemoteStdioHelperCommand()...)
	addOutput := runRootForRemoteTest(t, env, addArgs...)
	for _, want := range []string{
		"registered global toolbox localmcp",
		"synced toolbox localmcp: 2 tools",
	} {
		if !strings.Contains(addOutput, want) {
			t.Fatalf("expected stdio add output to contain %q, got:\n%s", want, addOutput)
		}
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	server := config.MCP.Servers["localmcp"]
	if server.Transport != mcpRemoteTransportStdio || server.Command != os.Args[0] || len(server.Args) == 0 {
		t.Fatalf("expected stdio command config, got %#v", server)
	}
	if server.AuthRequired == nil || *server.AuthRequired {
		t.Fatalf("expected stdio server to record auth_required false, got %#v", server.AuthRequired)
	}

	output := runRootForRemoteTest(t, env, "localmcp", "create_issue", "--title", "Stdio")
	if !strings.Contains(output, "stdio create_issue: Stdio") {
		t.Fatalf("expected stdio tool output, got %q", output)
	}

	status := runRootForRemoteTest(t, env, "status", "localmcp")
	if !strings.Contains(status, "stdio-mcp") || !strings.Contains(status, os.Args[0]) {
		t.Fatalf("expected stdio status, got:\n%s", status)
	}

	callOutput := runRootForRemoteTestWithInput(t, env,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"localmcp.create_issue","arguments":{"title":"From MCP"}}}`,
		"mcp", "serve",
	)
	result := decodeMCPCallResult(t, callOutput)
	if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "From MCP") {
		t.Fatalf("unexpected stdio MCP call result: %+v", result)
	}
}

func TestMCPRemoteDefaultArgumentsApplyToToolCalls(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteCloudServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "jira")
	setOutput := runRootForRemoteTest(t, env, "mcp", "defaults", "set", "jira", "cloudId", "cloud-123")
	if !strings.Contains(setOutput, "set default argument cloudId") {
		t.Fatalf("expected defaults set output, got %q", setOutput)
	}
	listOutput := runRootForRemoteTest(t, env, "mcp", "defaults", "ls", "jira")
	if !strings.Contains(listOutput, "cloudId") || !strings.Contains(listOutput, "cloud-123") {
		t.Fatalf("expected defaults list output to include cloudId, got %q", listOutput)
	}

	output := runRootForRemoteTest(t, env, "jira", "search", "--jql", "project = DEMO")
	if !strings.Contains(output, "called search: cloud-123") {
		t.Fatalf("expected default cloudId output, got %q", output)
	}
	if called["cloudId"] != "cloud-123" || called["jql"] != "project = DEMO" {
		t.Fatalf("unexpected defaulted arguments: %#v", called)
	}

	output = runRootForRemoteTest(t, env, "jira", "search", "--jql", "project = DEMO", "--cloudId", "cloud-456")
	if !strings.Contains(output, "called search: cloud-456") {
		t.Fatalf("expected overridden cloudId output, got %q", output)
	}
	if called["cloudId"] != "cloud-456" {
		t.Fatalf("expected explicit cloudId to override default: %#v", called)
	}

	removeOutput := runRootForRemoteTest(t, env, "mcp", "defaults", "rm", "jira", "cloudId")
	if !strings.Contains(removeOutput, "removed default arguments") {
		t.Fatalf("expected defaults remove output, got %q", removeOutput)
	}
	_, err := runRootForRemoteTestError(t, env, "jira", "search", "--jql", "project = DEMO")
	if err == nil || !strings.Contains(err.Error(), "missing required MCP tool arguments: cloudId") {
		t.Fatalf("expected missing cloudId after removing default, got %v", err)
	}
}

func TestMCPRemoteDefaultArgumentHintComesFromCatalogMetadata(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	atlassian := mcpBuiltinRemoteServers()["atlassian"]
	writeRemoteTestConfig(t, env, map[string]mcpRemoteServer{
		"atlassian": atlassian,
	})
	if err := writeMCPRemoteCache(env.CacheDir, "atlassian", mcpRemoteCache{
		Name:      "atlassian",
		URL:       atlassian.URL,
		Transport: atlassian.Transport,
		Tools:     []mcpRemoteTool{fakeMCPCloudSearchRemoteTool()},
		SyncedAt:  time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	catalogOutput := runRootForRemoteTest(t, env, "-o", "json", "list", "--mcp")
	if !strings.Contains(catalogOutput, "default_argument_hints") || !strings.Contains(catalogOutput, "cloudId") {
		t.Fatalf("expected catalog output to include cloudId default hint, got %q", catalogOutput)
	}
	_, err := runRootForRemoteTestError(t, env, "atlassian", "search", "--jql", "project = DEMO")
	if err == nil {
		t.Fatal("expected missing cloudId error")
	}
	want := "toolmux mcp defaults set atlassian cloudId <cloud-id>"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected default cloudId hint %q, got %v", want, err)
	}
}

func TestToolboxAddURLDerivesNameAndSupportsCustomName(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	derivedOutput := runRootForRemoteTest(t, env, "add", "https://mcp.linear.app/mcp", "--no-sync", "--global")
	if !strings.Contains(derivedOutput, "registered global toolbox linear") {
		t.Fatalf("expected URL-derived registration output, got:\n%s", derivedOutput)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.MCP.Servers["linear"].URL; got != "https://mcp.linear.app/mcp" {
		t.Fatalf("expected exact URL to be stored, got %q", got)
	}

	addOutput := runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "custom", "--global")
	for _, want := range []string{
		"registered global toolbox custom",
		"synced toolbox custom: 2 tools",
	} {
		if !strings.Contains(addOutput, want) {
			t.Fatalf("expected URL add output to contain %q, got:\n%s", want, addOutput)
		}
	}
	output := runRootForRemoteTest(t, env, "custom", "create_issue", "--title", "URL")
	if !strings.Contains(output, "called create_issue: URL") {
		t.Fatalf("expected custom remote tool output, got %q", output)
	}
}

func TestToolboxAddCatalogNameStoresResolvedURL(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	addOutput := runRootForRemoteTest(t, env, "add", "linear", "--name", "linear-work", "--no-sync", "--global")
	if !strings.Contains(addOutput, "registered global toolbox linear-work") {
		t.Fatalf("expected catalog add output, got:\n%s", addOutput)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.MCP.Servers["linear-work"].URL; got != mcpBuiltinRemoteServers()["linear"].URL {
		t.Fatalf("expected catalog add to store resolved URL, got %q", got)
	}
}

func TestToolboxAddInfersStdioCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	addOutput := runRootForRemoteTest(t, env, "add", "npx", "foo/bar", "--no-sync", "--global")
	if !strings.Contains(addOutput, "registered global toolbox bar") {
		t.Fatalf("expected inferred stdio add output, got:\n%s", addOutput)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	server := config.MCP.Servers["bar"]
	if server.Transport != mcpRemoteTransportStdio || server.Command != "npx" {
		t.Fatalf("expected inferred stdio command config, got %#v", server)
	}
	if len(server.Args) != 1 || server.Args[0] != "foo/bar" {
		t.Fatalf("expected inferred stdio args, got %#v", server.Args)
	}
}

func TestToolboxAddStdioDisambiguatesCatalogName(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	_, err := runRootForRemoteTestError(t, env, "add", "linear", "foo", "--no-sync", "--global")
	if err == nil || !strings.Contains(err.Error(), "pass --stdio") {
		t.Fatalf("expected catalog command ambiguity error, got %v", err)
	}

	addOutput := runRootForRemoteTest(t, env, "add", "--stdio", "linear", "foo", "--name", "linearcmd", "--no-sync", "--global")
	if !strings.Contains(addOutput, "registered global toolbox linearcmd") {
		t.Fatalf("expected stdio-disambiguated add output, got:\n%s", addOutput)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	server := config.MCP.Servers["linearcmd"]
	if server.Transport != mcpRemoteTransportStdio || server.Command != "linear" {
		t.Fatalf("expected stdio command config, got %#v", server)
	}
	if len(server.Args) != 1 || server.Args[0] != "foo" {
		t.Fatalf("expected stdio command arg, got %#v", server.Args)
	}
}

func TestDefaultMCPRemoteNameFromURL(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://mcp.linear.app/mcp":         "linear",
		"https://linear.app/mcp":             "linear",
		"https://api.slack.com/mcp":          "slack",
		"https://mcp.example.co.uk/mcp":      "example",
		"https://team-tools.example.com/mcp": "example",
	}
	for raw, want := range tests {
		got, err := defaultMCPRemoteNameFromURL(raw)
		if err != nil {
			t.Fatalf("defaultMCPRemoteNameFromURL(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("defaultMCPRemoteNameFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDefaultMCPRemoteNameFromCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		command string
		args    []string
		want    string
	}{
		"npx package": {
			command: "npx",
			args:    []string{"-y", "@upstash/context7-mcp"},
			want:    "context7",
		},
		"npx slash package": {
			command: "npx",
			args:    []string{"foo/bar"},
			want:    "bar",
		},
		"docker image": {
			command: "docker",
			args:    []string{"run", "-i", "--rm", "ghcr.io/acme/browser-tools-mcp:latest"},
			want:    "browser-tools",
		},
		"plain command": {
			command: "/opt/bin/my-mcp-server",
			want:    "my",
		},
	}
	for name, test := range tests {
		got, err := defaultMCPRemoteNameFromCommand(test.command, test.args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != test.want {
			t.Fatalf("%s: got %q, want %q", name, got, test.want)
		}
	}
}

func TestMCPRemoteToolCommandsUseInputSchema(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global")

	serverHelp := runRootForRemoteTest(t, env, "linear")
	for _, want := range []string{"Imported remote MCP server linear", "create_issue", "calculate"} {
		if !strings.Contains(serverHelp, want) {
			t.Fatalf("expected remote server help to contain %q, got:\n%s", want, serverHelp)
		}
	}
	if strings.Contains(serverHelp, "has no command") {
		t.Fatalf("expected remote server without a tool to show help, got:\n%s", serverHelp)
	}

	missingOutput, missingErr := runRootForRemoteTestError(t, env, "linear", "missing_tool")
	if missingErr == nil {
		t.Fatalf("expected missing remote tool error, got output:\n%s", missingOutput)
	}
	if !strings.Contains(missingErr.Error(), `MCP server "linear" has no command "missing_tool"`) {
		t.Fatalf("unexpected missing remote tool error: %v", missingErr)
	}

	help := runRootForRemoteTest(t, env, "linear", "calculate", "-h")
	for _, want := range []string{
		"--a float",
		"--b float",
		"--operation string",
		"-v, --verbose",
		"toolmux mcp schema linear calculate",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected help to contain %q, got:\n%s", want, help)
		}
	}
	if strings.Contains(help, "Input Schema:") || strings.Contains(help, `"required": [`) {
		t.Fatalf("expected help to omit full input schema, got:\n%s", help)
	}

	schemaOutput := runRootForRemoteTest(t, env, "mcp", "schema", "linear", "calculate")
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaOutput), &schema); err != nil {
		t.Fatalf("decode schema output: %v\n%s", err, schemaOutput)
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["a"]; !ok || schema["type"] != "object" {
		t.Fatalf("unexpected schema output: %#v", schema)
	}
	dottedSchemaOutput := runRootForRemoteTest(t, env, "mcp", "schema", "linear.calculate")
	if dottedSchemaOutput != schemaOutput {
		t.Fatalf("expected dotted schema lookup to match two-arg lookup:\n%s\n---\n%s", dottedSchemaOutput, schemaOutput)
	}
	_, policyErr := runRootForRemoteTestError(t, env, "policy", "check", "--command", "mcp schema linear.calculate")
	if policyErr == nil || !strings.Contains(policyErr.Error(), "no command spec found") {
		t.Fatalf("expected schema command outside policy, got %v", policyErr)
	}

	_, rootSchemaErr := runRootForRemoteTestError(t, env, "schema", "linear.calculate")
	if rootSchemaErr == nil || !strings.Contains(rootSchemaErr.Error(), "unknown command") {
		t.Fatalf("expected root schema command to be unavailable, got %v", rootSchemaErr)
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

func TestMCPRemoteCompactDescriptionUsesFirstLine(t *testing.T) {
	t.Parallel()

	description := "  First line with   extra spacing.  \n\nSecond line with details that should remain available elsewhere."
	if got := mcpRemoteCompactDescription(description); got != "First line with extra spacing." {
		t.Fatalf("unexpected compact description %q", got)
	}

	markdown := "## Overview\n\nCreate pages in a Notion database from markdown."
	if got := mcpRemoteCompactDescription(markdown); got != "Create pages in a Notion database from markdown." {
		t.Fatalf("unexpected markdown compact description %q", got)
	}

	colonList := "Perform a search over:\n\n- pages\n- databases\n- data sources\n- comments"
	if got := mcpRemoteCompactDescription(colonList); got != "Perform a search over pages, databases, data sources." {
		t.Fatalf("unexpected colon-list compact description %q", got)
	}

	long := strings.Repeat("word ", 40)
	got := mcpRemoteCompactDescription(long)
	if len(got) > mcpRemoteCompactDescriptionLimit+3 || !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated compact description, got %q", got)
	}
}

func TestMCPRemoteToolForServePreservesCachedMetadata(t *testing.T) {
	t.Parallel()

	served := mcpRemoteToolForServe("grafana.query_prometheus", mcpRemoteTool{
		Name:        "query_prometheus",
		Title:       "Query Prometheus",
		Description: "Execute PromQL queries.",
		InputSchema: map[string]any{
			"type": "object",
		},
		OutputSchema: map[string]any{
			"type": "object",
		},
		Annotations: map[string]any{
			"readOnlyHint": true,
		},
		Icons: []map[string]any{{
			"src":      "https://example.com/icon.png",
			"mimeType": "image/png",
		}},
		Execution: map[string]any{
			"taskSupport": "optional",
		},
		Meta: map[string]any{
			"upstream": "grafana",
		},
	})

	for _, key := range []string{"name", "title", "description", "inputSchema", "outputSchema", "annotations", "icons", "execution", "_meta"} {
		if _, ok := served[key]; !ok {
			t.Fatalf("expected served remote tool to include %q: %#v", key, served)
		}
	}
	if served["name"] != "grafana.query_prometheus" || served["title"] != "Query Prometheus" {
		t.Fatalf("unexpected served remote tool metadata: %#v", served)
	}
}

func TestMCPRemoteToolForServeMarksDefaultArgumentsOptional(t *testing.T) {
	t.Parallel()

	served := mcpRemoteToolForServeWithDefaults("atlassian.search", fakeMCPCloudSearchRemoteTool(), map[string]any{
		"cloudId": "cloud-123",
	})
	schema, ok := served["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("expected input schema map, got %#v", served["inputSchema"])
	}
	required := mcpRemoteRequiredSet(schema)
	if required["cloudId"] {
		t.Fatalf("expected cloudId to be optional in served schema: %#v", schema["required"])
	}
	if !required["jql"] {
		t.Fatalf("expected jql to remain required: %#v", schema["required"])
	}
	properties := mcpRemoteSchemaProperties(schema)
	cloudID, ok := properties["cloudId"].(map[string]any)
	if !ok || cloudID["default"] != "cloud-123" {
		t.Fatalf("expected cloudId default in served schema: %#v", properties["cloudId"])
	}
}

func TestMCPRemoteCallToolResultPreservesStructuredAndNonTextContent(t *testing.T) {
	t.Parallel()

	var result mcpCallToolResult
	raw := []byte(`{"content":[{"type":"image","data":"abc","mimeType":"image/png","annotations":{"audience":["user"]}}],"structuredContent":{"ok":true},"_meta":{"trace":"1"}}`)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !mcpRemoteCallToolResultHasPayload(result) {
		t.Fatal("expected remote call result payload to be recognized")
	}
	if result.StructuredContent == nil || result.Content[0].Data != "abc" || result.Content[0].MimeType != "image/png" {
		t.Fatalf("unexpected decoded call result: %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"structuredContent"`, `"mimeType":"image/png"`, `"_meta"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("expected encoded call result to contain %q, got %s", want, encoded)
		}
	}
}

func TestRenderMCPRemoteRootCompactHelpUsesColorAndCompactDescriptions(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	opts := &options{output: "table", color: "always"}
	cmd := &cobra.Command{
		Use:   "notion",
		Short: "Imported remote MCP server notion",
	}
	cmd.SetOut(&out)
	cmd.Flags().Bool("full-help", false, "show full upstream MCP tool descriptions")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "notion-create-pages",
			Short: "## Overview\n\nCreate pages in a Notion database from markdown.\n\nAdditional details for agents.",
			Run:   func(cmd *cobra.Command, args []string) {},
		},
		&cobra.Command{
			Use:   "notion-search",
			Short: "Perform a search over:\n\n- pages\n- databases\n- data sources\n- comments",
			Run:   func(cmd *cobra.Command, args []string) {},
		},
	)

	renderMCPRemoteRootCompactHelp(cmd, opts)

	help := out.String()
	for _, want := range []string{
		"\x1b[",
		"Available Commands:",
		"notion-create-pages",
		"Create pages in a Notion database from markdown.",
		"Perform a search over pages, databases, data sources.",
		`Use "notion --full-help" for full upstream descriptions.`,
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected compact help to contain %q, got:\n%s", want, help)
		}
	}
	for _, unwanted := range []string{"## Overview", "Additional details for agents.", "comments"} {
		if strings.Contains(help, unwanted) {
			t.Fatalf("expected compact help to omit %q, got:\n%s", unwanted, help)
		}
	}
}

func TestMCPRemoteServerExposesCachedToolsOverMCPServe(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global")

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

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global", "--no-sync")
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
