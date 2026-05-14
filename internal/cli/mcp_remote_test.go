//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

	catalogOutput := runRootForRemoteTest(t, env, "-o", "json", "catalog", "--mcp")
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

func TestMCPBuiltinRemoteCatalogIncludesHostedServers(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"airtable":    "https://mcp.airtable.com/mcp",
		"asana":       "https://mcp.asana.com/v2/mcp",
		"excalidraw":  "https://mcp.excalidraw.com/mcp",
		"figma":       "https://mcp.figma.com/mcp",
		"gainsight":   "https://mcp.staircase.ai/mcp",
		"github":      "https://api.githubcopilot.com/mcp/",
		"granola":     "https://mcp.granola.ai/mcp",
		"incident-io": "https://mcp.incident.io/mcp",
		"posthog":     "https://mcp.posthog.com/mcp",
		"sentry":      "https://mcp.sentry.dev/mcp",
		"stripe":      "https://mcp.stripe.com",
		"supabase":    "https://mcp.supabase.com/mcp",
		"vercel":      "https://mcp.vercel.com",
		"zoom":        "https://mcp.zoom.us/mcp/zoom/streamable",
		"zoominfo":    "https://mcp.zoominfo.com/mcp",
	}
	servers := mcpBuiltinRemoteServers()
	if _, ok := servers["incident"]; ok {
		t.Fatal("expected incident.io catalog key to be incident-io, not incident")
	}
	for name, url := range want {
		server, ok := servers[name]
		if !ok {
			t.Fatalf("expected built-in MCP server %q", name)
		}
		if server.URL != url || server.Transport != mcpRemoteTransportStreamableHTTP {
			t.Fatalf("expected %s to use %s over streamable HTTP, got %#v", name, url, server)
		}
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

func TestCatalogListsAndTogglesBuiltins(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	output := runRootForRemoteTest(t, env, "catalog")
	for _, want := range []string{
		"iterate",
		"mcp",
		"available",
		"notion",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected catalog output to contain %q, got:\n%s", want, output)
		}
	}

	enableOutput := runRootForRemoteTest(t, env, "catalog", "--enable", "iterate", "--global")
	if !strings.Contains(enableOutput, "enabled global MCP server iterate") {
		t.Fatalf("expected enable output, got %q", enableOutput)
	}
	jsonOutput := runRootForRemoteTest(t, env, "--output", "json", "catalog", "--mcp")
	var entries []toolboxCatalogEntry
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		t.Fatalf("decode catalog output: %v\n%s", err, jsonOutput)
	}
	var iterate toolboxCatalogEntry
	for _, entry := range entries {
		if entry.Name == "iterate" {
			iterate = entry
		}
	}
	if iterate.Type != "mcp" || !iterate.Registered || iterate.Status != "registered" || iterate.Scope != "global" {
		t.Fatalf("expected registered iterate catalog entry, got %#v", iterate)
	}
	if len(iterate.RegisteredNames) != 1 || iterate.RegisteredNames[0] != "iterate" {
		t.Fatalf("expected iterate registered name, got %#v", iterate.RegisteredNames)
	}

	disableOutput := runRootForRemoteTest(t, env, "catalog", "--disable", "iterate")
	if !strings.Contains(disableOutput, "disabled MCP server iterate") {
		t.Fatalf("expected disable output, got %q", disableOutput)
	}
	output = runRootForRemoteTest(t, env, "catalog", "--mcp")
	if !strings.Contains(output, "iterate") || !strings.Contains(output, "available") {
		t.Fatalf("expected iterate to be available after disable, got:\n%s", output)
	}

	notionOutput := runRootForRemoteTest(t, env, "catalog", "--enable", "notion", "--global")
	if !strings.Contains(notionOutput, "enabled global MCP server notion") {
		t.Fatalf("expected Notion MCP enable output, got %q", notionOutput)
	}
	jsonOutput = runRootForRemoteTest(t, env, "--output", "json", "catalog", "--mcp")
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		t.Fatalf("decode catalog output: %v\n%s", err, jsonOutput)
	}
	var notion toolboxCatalogEntry
	for _, entry := range entries {
		if entry.Name == "notion" {
			notion = entry
		}
	}
	if !notion.Registered || notion.Status != "registered" || len(notion.RegisteredNames) != 1 || notion.RegisteredNames[0] != "notion" {
		t.Fatalf("expected notion registered directly, got %#v", notion)
	}
	disableOutput = runRootForRemoteTest(t, env, "catalog", "--disable", "notion")
	if !strings.Contains(disableOutput, "disabled MCP server notion") {
		t.Fatalf("expected disable by catalog name, got %q", disableOutput)
	}

	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "catalog --enable iterate")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected catalog manage policy check, got %q", policyOutput)
	}

	mcpOutput := runRootForRemoteTest(t, env, "catalog", "--mcp")
	if strings.Contains(mcpOutput, "internal") {
		t.Fatalf("expected --mcp catalog output to omit internal entries, got:\n%s", mcpOutput)
	}
}

func TestMCPRemoteListShowsToolsAndTree(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServer(t, &called)
	defer upstream.Close()

	addOutput := runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear")
	if !strings.Contains(addOutput, "registered global toolbox linear") {
		t.Fatalf("expected global registration output, got %q", addOutput)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if config.MCP.Servers["linear"].AuthRequired == nil || *config.MCP.Servers["linear"].AuthRequired {
		t.Fatalf("expected no-auth server to record auth_required false, got %#v", config.MCP.Servers["linear"])
	}

	listOutput := runRootForRemoteTest(t, env, "--color", "always", "mcp", "ls")
	for _, want := range []string{"linear", "synced", "global", "2"} {
		if !strings.Contains(listOutput, want) {
			t.Fatalf("expected mcp ls output to contain %q, got:\n%s", want, listOutput)
		}
	}
	if strings.Contains(listOutput, "local") {
		t.Fatalf("expected mcp ls output to use normalized scope labels, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "\x1b[") {
		t.Fatalf("expected mcp ls output to include color when forced, got:\n%s", listOutput)
	}

	jsonOutput := runRootForRemoteTest(t, env, "-o", "json", "mcp", "ls")
	var listItems []mcpRemoteListItem
	if err := json.Unmarshal([]byte(jsonOutput), &listItems); err != nil {
		t.Fatalf("decode mcp ls json output: %v\n%s", err, jsonOutput)
	}
	if len(listItems) != 1 || listItems[0].Name != "linear" || listItems[0].Scope != "global" || listItems[0].Status != "synced" {
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

func TestStatusTableShowsRemoteMCPToolboxesAndAuth(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServerWithBearer(t, &called, "secret-token")
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global", "--no-sync")
	runRootForRemoteTestWithInput(t, env, "secret-token", "mcp", "auth", "set", "linear", "--bearer-token-stdin")
	runRootForRemoteTest(t, env, "mcp", "sync", "linear")

	rendered := runRootForRemoteTest(t, env, "status", "linear")
	for _, want := range []string{"Toolbox", "remote-mcp", "connected", "bearer", "linear", "2"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected status table to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("non-tty status output should not contain ANSI escape sequences: %q", rendered)
	}

	jsonOutput := runRootForRemoteTest(t, env, "-o", "json", "status")
	var statuses []toolboxStatusItem
	if err := json.Unmarshal([]byte(jsonOutput), &statuses); err != nil {
		t.Fatalf("decode status json output: %v\n%s", err, jsonOutput)
	}
	if len(statuses) != 1 || statuses[0].Name != "linear" || statuses[0].Auth != "bearer" || statuses[0].Status != "connected" {
		t.Fatalf("unexpected status json output: %+v", statuses)
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
	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "mcp schema linear.calculate")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected schema command policy check, got %q", policyOutput)
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

func TestSyncMCPRemoteServerRejectsMissingToolsArray(t *testing.T) {
	t.Parallel()

	var initialized atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"serverInfo":      map[string]any{"name": "null-tools"},
				},
			})
		case "notifications/initialized":
			initialized.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized.Load() {
				t.Fatal("tools/list called before notifications/initialized")
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  nil,
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	_, err := syncMCPRemoteServer(context.Background(), upstream.Client(), mcpRemoteServerEntry{
		Name: "null-tools",
		Server: mcpRemoteServer{
			URL:       upstream.URL,
			Transport: mcpRemoteTransportStreamableHTTP,
		},
	}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "tools/list returned no tools array") {
		t.Fatalf("expected missing tools array error, got %v", err)
	}
}

func TestMCPRemoteToolVerbosePrintsHTTPTrace(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServerWithBearer(t, &called, "secret-token")
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global", "--no-sync")
	runRootForRemoteTestWithInput(t, env, "secret-token", "mcp", "auth", "set", "linear", "--bearer-token-stdin")
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if config.MCP.Servers["linear"].AuthRequired == nil || !*config.MCP.Servers["linear"].AuthRequired {
		t.Fatalf("expected bearer auth set to record auth_required true, got %#v", config.MCP.Servers["linear"])
	}
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

func TestMCPRemoteAddVerbosePrintsHTTPTraceAndUsesLatestClientMetadata(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var sawInitialize bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Protocol-Version") != mcpRemoteClientProtocolVersion {
			t.Fatalf("expected MCP protocol header %q, got %q", mcpRemoteClientProtocolVersion, r.Header.Get("Mcp-Protocol-Version"))
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string         `json:"protocolVersion"`
				ClientInfo      map[string]any `json:"clientInfo"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode initialize params: %v", err)
			}
			if params.ProtocolVersion != mcpRemoteClientProtocolVersion {
				t.Fatalf("expected initialize protocol %q, got %q", mcpRemoteClientProtocolVersion, params.ProtocolVersion)
			}
			if params.ClientInfo["title"] != "Toolmux" || params.ClientInfo["websiteUrl"] != "https://github.com/fiam/toolmux" {
				t.Fatalf("unexpected clientInfo: %#v", params.ClientInfo)
			}
			icons, _ := params.ClientInfo["icons"].([]any)
			if len(icons) != 1 {
				t.Fatalf("expected clientInfo icon, got %#v", params.ClientInfo["icons"])
			}
			sawInitialize = true
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpRemoteClientProtocolVersion,
					"serverInfo":      map[string]any{"name": "metadata-test"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !sawInitialize {
				t.Fatal("tools/list called before initialize")
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	output := runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "debug", "--global", "-v")
	for _, want := range []string{
		"----- MCP HTTP request -----",
		"Mcp-Protocol-Version: " + mcpRemoteClientProtocolVersion,
		`"protocolVersion":"` + mcpRemoteClientProtocolVersion + `"`,
		`"title":"Toolmux"`,
		`"method":"tools/list"`,
		"synced toolbox debug: 1 tools",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected add verbose output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestSyncMCPRemoteServerFollowsToolsListPagination(t *testing.T) {
	t.Parallel()

	var initialized atomic.Bool
	var cursors []*string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"protocolVersion": mcpRemoteClientProtocolVersion,
					"serverInfo":      map[string]any{"name": "paged-tools"},
				},
			})
		case "notifications/initialized":
			initialized.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized.Load() {
				t.Fatal("tools/list called before notifications/initialized")
			}
			var params struct {
				Cursor *string `json:"cursor"`
			}
			if len(req.Params) > 0 {
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Fatalf("decode tools/list params: %v", err)
				}
			}
			cursors = append(cursors, params.Cursor)
			if len(cursors) == 1 {
				_ = json.NewEncoder(w).Encode(mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"tools":      []map[string]any{fakeMCPCreateIssueTool()},
						"nextCursor": "",
					},
				})
				return
			}
			if params.Cursor == nil || *params.Cursor != "" {
				t.Fatalf("expected empty-string cursor on second page, got %#v", params.Cursor)
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCalculateTool()},
				},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	cache, err := syncMCPRemoteServer(context.Background(), upstream.Client(), mcpRemoteServerEntry{
		Name: "paged",
		Server: mcpRemoteServer{
			URL:       upstream.URL,
			Transport: mcpRemoteTransportStreamableHTTP,
		},
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Tools) != 2 || cache.Tools[0].Name != "calculate" || cache.Tools[1].Name != "create_issue" {
		t.Fatalf("unexpected paginated tools: %#v", cache.Tools)
	}
	if len(cursors) != 2 || cursors[0] != nil || cursors[1] == nil || *cursors[1] != "" {
		t.Fatalf("unexpected cursor sequence: %#v", cursors)
	}
	var raw struct {
		Tools []mcpRemoteTool   `json:"tools"`
		Pages []json.RawMessage `json:"pages"`
	}
	if err := json.Unmarshal(cache.Raw["tools_list"], &raw); err != nil {
		t.Fatalf("decode cached tools_list raw: %v", err)
	}
	if len(raw.Tools) != 2 || len(raw.Pages) != 2 {
		t.Fatalf("expected aggregated raw tools and pages, got %#v", raw)
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

func TestMCPRemoteServerOAuthLoginAndRefresh(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteOAuthServer(t, &called)
	defer upstream.Close()

	policyOutput := runRootForRemoteTest(t, env, "policy", "check", "--command", "mcp auth login linear")
	if !strings.Contains(policyOutput, "allowed") {
		t.Fatalf("expected OAuth auth policy check, got %q", policyOutput)
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
	if config.MCP.Servers["linear"].AuthRequired == nil || !*config.MCP.Servers["linear"].AuthRequired {
		t.Fatalf("expected OAuth server to record auth_required true, got %#v", config.MCP.Servers["linear"])
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

func TestMCPRemoteServerSupportsSSEAndSessionID(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteSSESessionServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "iterate", "--global")

	output := runRootForRemoteTest(t, env, "iterate", "create_issue", "--title", "SSE")
	if !strings.Contains(output, "called create_issue: SSE") {
		t.Fatalf("expected SSE remote tool output, got %q", output)
	}
	if called["title"] != "SSE" {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
}

func TestReadMCPRemoteSSEResponseSkipsNotifications(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
		``,
		`event: message`,
		`data: {"jsonrpc":"2.0","id":99,"result":{"ignored":true}}`,
		``,
		`event: message`,
		`data: {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"query_prometheus","inputSchema":{"type":"object"}}]}}`,
		``,
	}, "\n")
	message, err := readMCPRemoteSSEResponse(strings.NewReader(stream), json.RawMessage("1"))
	if err != nil {
		t.Fatal(err)
	}
	var response mcpResponse
	if err := json.Unmarshal(message, &response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.ID, json.RawMessage("1")) {
		t.Fatalf("expected response id 1, got %s", response.ID)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "query_prometheus") {
		t.Fatalf("expected final tools/list response, got %s", data)
	}
}

func TestReadMCPRemoteSSEResponseDefaultTimeoutIsSixtySeconds(t *testing.T) {
	t.Parallel()

	if mcpRemoteSSEIdleTimeout != time.Minute {
		t.Fatalf("expected default SSE idle timeout to be 60s, got %s", mcpRemoteSSEIdleTimeout)
	}
}

func TestReadMCPRemoteSSEResponseReturnsBeforeStreamCloses(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	type result struct {
		message []byte
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := readMCPRemoteSSEResponse(reader, json.RawMessage("1"), 2*time.Second)
		resultCh <- result{message: message, err: err}
	}()

	_, err := io.WriteString(writer, strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
		``,
	}, "\n")+"\n")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	_, err = io.WriteString(writer, strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"query_prometheus","inputSchema":{"type":"object"}}]}}`,
		``,
	}, "\n")+"\n")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !strings.Contains(string(result.message), "query_prometheus") {
			t.Fatalf("expected final tools/list response, got %s", result.message)
		}
	case <-time.After(750 * time.Millisecond):
		t.Fatal("timed out waiting for matching SSE response before stream close")
	}
}

func TestReadMCPRemoteSSEResponseTimesOutWhenStreamGoesIdle(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := readMCPRemoteSSEResponse(reader, json.RawMessage("1"), 100*time.Millisecond)
		errCh <- err
	}()

	_, err := io.WriteString(writer, strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
		``,
	}, "\n")+"\n")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected idle timeout error")
		}
		if !strings.Contains(err.Error(), "timed out waiting for response message") {
			t.Fatalf("expected idle timeout error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle SSE response error")
	}
}

func TestRemoteMCPToolCommandUsesConfiguredCallTimeout(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	upstream := newFakeMCPRemoteIdleToolCallServer(t)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global")
	_, err := runRootForRemoteTestError(t, env,
		"--mcp-tool-call-timeout", "100ms",
		"linear", "create_issue",
		"--title", "Slow",
	)
	if err == nil {
		t.Fatal("expected MCP tool call timeout error")
	}
	if !strings.Contains(err.Error(), "timed out waiting for response message after 100ms of inactivity") {
		t.Fatalf("expected configured timeout in error, got %v", err)
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

func TestMCPRemoteStdioHelper(t *testing.T) {
	if !mcpRemoteStdioHelperRequested() {
		return
	}
	os.Exit(runFakeMCPStdioServer())
}

func mcpRemoteStdioHelperCommand() []string {
	return []string{os.Args[0], "-test.run=^TestMCPRemoteStdioHelper$", "--", "--toolmux-test-mcp-stdio"}
}

func mcpRemoteStdioHelperRequested() bool {
	return slices.Contains(os.Args, "--toolmux-test-mcp-stdio")
}

func runFakeMCPStdioServer() int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			return 2
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"serverInfo": map[string]any{
					"name": "fake-stdio",
				},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{fakeMCPCreateIssueTool(), fakeMCPCalculateTool()},
			}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				fmt.Fprintf(os.Stderr, "decode call params: %v\n", err)
				return 2
			}
			switch params.Name {
			case "create_issue":
				result = mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "stdio create_issue: " + fmt.Sprint(params.Arguments["title"]),
					}},
				}
			case "calculate":
				result = mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "stdio calculate: " + fmt.Sprint(params.Arguments["operation"]),
					}},
				}
			default:
				result = mcpCallToolResult{
					IsError: true,
					Content: []mcpContent{{
						Type: "text",
						Text: "unexpected tool " + params.Name,
					}},
				}
			}
		default:
			result = nil
		}
		if err := encoder.Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result}); err != nil {
			fmt.Fprintf(os.Stderr, "encode response: %v\n", err)
			return 2
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scan stdin: %v\n", err)
		return 2
	}
	return 0
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

func newFakeMCPRemoteCloudServer(t *testing.T, called *map[string]any) *httptest.Server {
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
					"serverInfo":      map[string]any{"name": "fake-cloud"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCloudSearchTool()},
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
			if params.Name != "search" {
				t.Fatalf("unexpected tool name %q", params.Name)
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
						Text: "called search: " + params.Arguments["cloudId"].(string),
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

func newFakeMCPRemoteIdleToolCallServer(t *testing.T) *httptest.Server {
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
					"serverInfo":      map[string]any{"name": "fake-idle"},
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
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\n")
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`)
			fmt.Fprint(w, "\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
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

func fakeMCPCloudSearchTool() map[string]any {
	return map[string]any{
		"name":        "search",
		"description": "Search cloud resources",
		"inputSchema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"cloudId", "jql"},
			"properties": map[string]any{
				"cloudId": map[string]any{
					"type":        "string",
					"description": "Cloud site ID",
				},
				"jql": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
			},
		},
	}
}

func fakeMCPCloudSearchRemoteTool() mcpRemoteTool {
	tool := fakeMCPCloudSearchTool()
	return mcpRemoteTool{
		Name:        tool["name"].(string),
		Description: tool["description"].(string),
		InputSchema: tool["inputSchema"].(map[string]any),
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
