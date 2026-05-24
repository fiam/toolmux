//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

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
		"airtable":     "https://mcp.airtable.com/mcp",
		"asana":        "https://mcp.asana.com/v2/mcp",
		"atlassian":    "https://mcp.atlassian.com/v1/mcp/authv2",
		"cloudflare":   "https://mcp.cloudflare.com/mcp",
		"datadog":      "https://mcp.datadoghq.com/api/unstable/mcp-server/mcp",
		"excalidraw":   "https://mcp.excalidraw.com/mcp",
		"figma":        "https://mcp.figma.com/mcp",
		"gainsight":    "https://mcp.staircase.ai/mcp",
		"github":       "https://api.githubcopilot.com/mcp/",
		"grafana":      "https://mcp.grafana.com/mcp",
		"granola":      "https://mcp.granola.ai/mcp",
		"incident-io":  "https://mcp.incident.io/mcp",
		"linear":       "https://mcp.linear.app/mcp",
		"miro":         "https://mcp.miro.com/",
		"neon":         "https://mcp.neon.tech/mcp",
		"notion":       "https://mcp.notion.com/mcp",
		"pagerduty":    "https://mcp.pagerduty.com/mcp",
		"pagerduty-eu": "https://mcp.eu.pagerduty.com/mcp",
		"posthog":      "https://mcp.posthog.com/mcp",
		"sentry":       "https://mcp.sentry.dev/mcp",
		"stripe":       "https://mcp.stripe.com",
		"supabase":     "https://mcp.supabase.com/mcp",
		"vercel":       "https://mcp.vercel.com",
		"zoom":         "https://mcp.zoom.us/mcp/zoom/streamable",
		"zoominfo":     "https://mcp.zoominfo.com/mcp",
	}
	servers := mcpBuiltinRemoteServers()
	if _, ok := servers["incident"]; ok {
		t.Fatal("expected incident.io catalog key to be incident-io, not incident")
	}
	if _, ok := servers["iterate"]; ok {
		t.Fatal("expected test-only iterate server not to be in the built-in catalog")
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
	catalog := mcpBuiltinRemoteCatalog()
	for name, definition := range catalog {
		if strings.TrimSpace(definition.DisplayName) == "" {
			t.Fatalf("expected built-in MCP server %q to have a display name", name)
		}
	}
}

func TestCatalogListsAndTogglesBuiltins(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	output := runRootForRemoteTest(t, env, "list")
	for _, want := range []string{
		"mcp",
		"available",
		"linear",
		"notion",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected catalog output to contain %q, got:\n%s", want, output)
		}
	}

	enableOutput := runRootForRemoteTest(t, env, "list", "--enable", "linear", "--global")
	if !strings.Contains(enableOutput, "enabled global MCP server linear") {
		t.Fatalf("expected enable output, got %q", enableOutput)
	}
	jsonOutput := runRootForRemoteTest(t, env, "--output", "json", "list", "--mcp")
	var entries []toolboxCatalogEntry
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		t.Fatalf("decode catalog output: %v\n%s", err, jsonOutput)
	}
	var linear toolboxCatalogEntry
	for _, entry := range entries {
		if entry.Name == "linear" {
			linear = entry
		}
	}
	if linear.Type != "mcp" || !linear.Registered || linear.Status != "not_synced" || linear.Scope != "global" {
		t.Fatalf("expected registered linear catalog entry, got %#v", linear)
	}
	if linear.DisplayName != "Linear" {
		t.Fatalf("expected catalog display name, got %#v", linear)
	}
	if len(linear.RegisteredNames) != 1 || linear.RegisteredNames[0] != "linear" {
		t.Fatalf("expected linear registered name, got %#v", linear.RegisteredNames)
	}

	disableOutput := runRootForRemoteTest(t, env, "list", "--disable", "linear")
	if !strings.Contains(disableOutput, "disabled MCP server linear") {
		t.Fatalf("expected disable output, got %q", disableOutput)
	}
	output = runRootForRemoteTest(t, env, "list", "--mcp")
	if !strings.Contains(output, "linear") || !strings.Contains(output, "available") {
		t.Fatalf("expected linear to be available after disable, got:\n%s", output)
	}

	notionOutput := runRootForRemoteTest(t, env, "list", "--enable", "notion", "--global")
	if !strings.Contains(notionOutput, "enabled global MCP server notion") {
		t.Fatalf("expected Notion MCP enable output, got %q", notionOutput)
	}
	jsonOutput = runRootForRemoteTest(t, env, "--output", "json", "list", "--mcp")
	if err := json.Unmarshal([]byte(jsonOutput), &entries); err != nil {
		t.Fatalf("decode catalog output: %v\n%s", err, jsonOutput)
	}
	var notion toolboxCatalogEntry
	for _, entry := range entries {
		if entry.Name == "notion" {
			notion = entry
		}
	}
	if !notion.Registered || notion.Status != "not_synced" || len(notion.RegisteredNames) != 1 || notion.RegisteredNames[0] != "notion" {
		t.Fatalf("expected notion registered directly, got %#v", notion)
	}
	disableOutput = runRootForRemoteTest(t, env, "list", "--disable", "notion")
	if !strings.Contains(disableOutput, "disabled MCP server notion") {
		t.Fatalf("expected disable by catalog name, got %q", disableOutput)
	}

	_, policyErr := runRootForRemoteTestError(t, env, "policy", "check", "--command", "list --enable linear")
	if policyErr == nil || !strings.Contains(policyErr.Error(), "no command spec found") {
		t.Fatalf("expected catalog management outside policy, got %v", policyErr)
	}

	mcpOutput := runRootForRemoteTest(t, env, "list", "--mcp")
	if strings.Contains(mcpOutput, "internal") {
		t.Fatalf("expected --mcp list output to omit internal entries, got:\n%s", mcpOutput)
	}
}

func TestToolboxListAliasAndRemovedLegacyCatalogNames(t *testing.T) {
	env := newMCPRemoteTestEnv(t)

	aliasOutput := runRootForRemoteTest(t, env, "ls", "--mcp")
	if !strings.Contains(aliasOutput, "linear") {
		t.Fatalf("expected ls alias output to contain linear, got:\n%s", aliasOutput)
	}

	for _, command := range []string{"catalog", "available"} {
		output, err := runRootForRemoteTestError(t, env, command)
		if err == nil {
			t.Fatalf("expected legacy %s command to fail, got:\n%s", command, output)
		}
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
	server, ok := configMCPRemoteServer(config, "linear")
	if !ok {
		t.Fatal("expected linear server config")
	}
	if server.AuthRequired == nil || *server.AuthRequired {
		t.Fatalf("expected no-auth server to record auth_required false, got %#v", server)
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
