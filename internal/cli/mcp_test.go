package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
	_ "github.com/fiam/toolmux/internal/providers/all"
)

type mcpTestResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *mcpError       `json:"error"`
}

func TestMCPToolsListUsesProfileFilters(t *testing.T) {
	t.Parallel()

	output := runMCPServe(t,
		[]string{"mcp", "serve", "--tool", "notion.page.*", "--exclude-tool", "*.delete"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	)
	response := decodeMCPTestResponse(t, output)
	if response.Error != nil {
		t.Fatalf("unexpected MCP error: %+v", response.Error)
	}
	var result struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"notion.page.read", "notion.page.create"} {
		if !slices.Contains(names, want) {
			t.Fatalf("expected MCP tools to include %q, got %v", want, names)
		}
	}
	for _, unwanted := range []string{"notion.search", "notion.page.delete"} {
		if slices.Contains(names, unwanted) {
			t.Fatalf("expected MCP tools to exclude %q, got %v", unwanted, names)
		}
	}
}

func TestMCPToolCallReturnsStructuredText(t *testing.T) {
	t.Parallel()

	output := runMCPServe(t,
		[]string{"mcp", "serve"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notion.page.create","arguments":{"parent-type":"workspace","title":"Draft","markdown":"# Draft","dry-run":true}}}`,
	)
	result := decodeMCPCallResult(t, output)
	if result.IsError {
		t.Fatalf("unexpected MCP tool error: %+v", result.Content)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("unexpected MCP content: %+v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, `"dry_run": true`) || !strings.Contains(text, `"action": "notion.page.create"`) {
		t.Fatalf("expected dry-run JSON text, got %q", text)
	}
}

func TestMCPToolCallHonorsReadOnlyMode(t *testing.T) {
	t.Parallel()

	output := runMCPServe(t,
		[]string{"--read-only", "mcp", "serve"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notion.page.create","arguments":{"parent-type":"workspace","title":"Draft","markdown":"# Draft","dry-run":true}}}`,
	)
	result := decodeMCPCallResult(t, output)
	if !result.IsError {
		t.Fatalf("expected MCP tool error, got %+v", result)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "read-only mode blocks command notion.page.create") {
		t.Fatalf("expected read-only denial, got %+v", result.Content)
	}
}

func TestMCPConfigureDryRunSupportsKnownAgents(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"mcp", "configure", "codex", "claude-code", "gemini-cli",
		"--dry-run",
		"--command", "/opt/toolmux/bin/toolmux",
		"--mcp-profile", "notion read",
		"--tool", "notion.page.*",
		"--exclude-tool", "*.delete",
		"--scope", "project",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"codex: codex mcp add toolmux-notion-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'notion read' --tool 'notion.page.*' --exclude-tool '*.delete'",
		"claude: claude mcp add --scope project --transport stdio toolmux-notion-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'notion read' --tool 'notion.page.*' --exclude-tool '*.delete'",
		"gemini: gemini mcp add --scope project --transport stdio toolmux-notion-read /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'notion read' --tool 'notion.page.*' --exclude-tool '*.delete'",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestMCPEnableDryRunSupportsKnownAgents(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"mcp", "enable", "codex", "claude-code",
		"--dry-run",
		"--command", "/opt/toolmux/bin/toolmux",
		"--mcp-profile", "notion read",
		"--tool", "notion.*",
		"--scope", "project",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"codex: codex mcp add toolmux-notion-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'notion read' --tool 'notion.*'",
		"claude: claude mcp add --scope project --transport stdio toolmux-notion-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'notion read' --tool 'notion.*'",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestMCPDisableDryRunSupportsKnownAgents(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"mcp", "disable", "claude-code", "gemini-cli",
		"--dry-run",
		"--mcp-profile", "notion read",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"claude: claude mcp remove --scope local toolmux-notion-read",
		"claude: claude mcp remove --scope user toolmux-notion-read",
		"claude: claude mcp remove --scope project toolmux-notion-read",
		"gemini: gemini mcp remove --scope user toolmux-notion-read",
		"gemini: gemini mcp remove --scope project toolmux-notion-read",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestMCPProfileSetWritesLocalProfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	profilePath := filepath.Join(dir, ".toolmux", "config.yaml")
	profile := mcpProfileConfigFromSelection(mcpToolSelection{
		Tools:        []string{"notion.page.*"},
		ExcludeTools: []string{"*.delete"},
	})
	if err := writeToolmuxConfigFile(profilePath, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			Profiles: map[string]mcpProfileConfig{"readonly": profile},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveMCPToolSelectionFromPaths(mcpToolSelection{Profile: "readonly"}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	selector, err := newMCPToolSelector(resolved)
	if err != nil {
		t.Fatal(err)
	}
	server := mcpServer{selector: selector}
	names := make([]string, 0, len(server.mcpSpecs()))
	for _, spec := range server.mcpSpecs() {
		names = append(names, spec.ID)
	}
	if !slices.Contains(names, "notion.page.read") || slices.Contains(names, "notion.page.delete") {
		t.Fatalf("profile filters were not applied: %v", names)
	}
}

func TestMCPProfilesLayerGlobalAndProjectDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeToolmuxConfigFile(globalPath, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			DefaultProfile: "global",
			Profiles: map[string]mcpProfileConfig{
				"global": {Tools: []string{"notion.*"}},
				"shared": {Tools: []string{"notion.*"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeToolmuxConfigFile(filepath.Join(dir, ".toolmux", "config.yaml"), toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			DefaultProfile: "project",
			Profiles: map[string]mcpProfileConfig{
				"project": {Tools: []string{"notion.page.*"}},
				"shared":  {Tools: []string{"notion.data_source.*"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveMCPToolSelectionFromPaths(mcpToolSelection{}, dir, globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile != "project" || !slices.Equal(resolved.Tools, []string{"notion.page.*"}) {
		t.Fatalf("expected project default profile, got %+v", resolved)
	}

	resolved, err = resolveMCPToolSelectionFromPaths(mcpToolSelection{Profile: "shared"}, dir, globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resolved.Tools, []string{"notion.data_source.*"}) {
		t.Fatalf("expected project profile to override global profile, got %+v", resolved)
	}

	entries, err := effectiveMCPProfileEntriesFromPaths(dir, globalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "shared" && !slices.Equal(entry.Scopes, []string{"global", "local"}) {
			t.Fatalf("expected shared profile to report both scopes, got %+v", entry)
		}
	}
}

func TestMCPConfiguredDefaultMustExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := writeToolmuxConfigFile(filepath.Join(dir, ".toolmux", "config.yaml"), toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			DefaultProfile: "missing",
			Profiles:       map[string]mcpProfileConfig{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := resolveMCPToolSelectionFromPaths(mcpToolSelection{}, dir, "")
	if err == nil || !strings.Contains(err.Error(), `default MCP profile "missing" not found`) {
		t.Fatalf("expected missing default profile error, got %v", err)
	}
}

func TestSelectMCPAgentsAutodetectsSupportedCLIs(t *testing.T) {
	t.Parallel()

	runtime := mcpAgentRuntime{
		lookPath: func(name string) (string, error) {
			switch name {
			case "codex", "gemini":
				return "/bin/" + name, nil
			default:
				return "", errors.New("not found")
			}
		},
	}
	agents, err := selectMCPAgents(runtime, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(agents, []string{"codex", "gemini"}) {
		t.Fatalf("unexpected agents: %v", agents)
	}
}

func TestSelectedEnabledMCPAgentsKeepsSupportedOrder(t *testing.T) {
	t.Parallel()

	statuses := map[string]mcpAgentStatus{
		"codex":  {Configured: true, Enabled: true},
		"claude": {Configured: true, Enabled: false},
		"gemini": {Configured: true, Enabled: true},
	}
	selected := selectedEnabledMCPAgents(statuses, []string{"codex", "claude", "gemini"})
	if !slices.Equal(selected, []string{"codex", "gemini"}) {
		t.Fatalf("unexpected selected agents: %v", selected)
	}
}

func TestRemoveMCPAgentsRemovesSupportedScopes(t *testing.T) {
	t.Parallel()

	var ran []string
	runtime := mcpAgentRuntime{
		run: func(ctx context.Context, name string, args []string) error {
			ran = append(ran, name+" "+strings.Join(args, " "))
			return nil
		},
	}
	results, err := removeMCPAgents(context.Background(), runtime, mcpConfigureOptions{}, []string{"claude", "gemini"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected removal results for two agents, got %+v", results)
	}
	for _, want := range []string{
		"claude mcp remove --scope local toolmux",
		"claude mcp remove --scope user toolmux",
		"claude mcp remove --scope project toolmux",
		"gemini mcp remove --scope user toolmux",
		"gemini mcp remove --scope project toolmux",
	} {
		if !slices.Contains(ran, want) {
			t.Fatalf("expected removal command %q, got %v", want, ran)
		}
	}
}

func TestGeminiMCPConfigDetectsConfiguredServer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"toolmux":{"command":"toolmux"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mcpConfigHasServer(path, "toolmux") {
		t.Fatal("expected toolmux MCP server to be detected")
	}
	if mcpConfigHasServer(path, "other") {
		t.Fatal("unexpected MCP server detection")
	}
}

func runMCPServe(t *testing.T, args []string, request string) string {
	t.Helper()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(request + "\n"))
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func decodeMCPCallResult(t *testing.T, output string) mcpCallToolResult {
	t.Helper()
	response := decodeMCPTestResponse(t, output)
	if response.Error != nil {
		t.Fatalf("unexpected MCP error: %+v", response.Error)
	}
	var result mcpCallToolResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeMCPTestResponse(t *testing.T, output string) mcpTestResponse {
	t.Helper()
	var response mcpTestResponse
	decoder := json.NewDecoder(strings.NewReader(output))
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode MCP response from %q: %v", output, err)
	}
	if response.JSONRPC != "2.0" {
		t.Fatalf("unexpected JSON-RPC version %q", response.JSONRPC)
	}
	var extra mcpTestResponse
	err := decoder.Decode(&extra)
	if err == nil {
		t.Fatalf("expected one MCP response, got multiple in %q", output)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected trailing MCP response data in %q", output)
	}
	return response
}

func TestMCPAgentConfigureCommandsRejectInvalidScopes(t *testing.T) {
	t.Parallel()

	_, err := mcpAgentConfigureCommands("gemini", "toolmux", mcpConfigureOptions{GeminiScope: "local"}, []string{"mcp", "serve"})
	if err == nil || !strings.Contains(err.Error(), "--gemini-scope") {
		t.Fatalf("expected Gemini scope error, got %v", err)
	}
	_, err = mcpAgentConfigureCommands("claude", "toolmux", mcpConfigureOptions{ClaudeScope: "global"}, []string{"mcp", "serve"})
	if err == nil || !strings.Contains(err.Error(), "--claude-scope") {
		t.Fatalf("expected Claude scope error, got %v", err)
	}
	_, err = mcpAgentConfigureCommands("gemini", "toolmux", mcpConfigureOptions{AgentScope: "local"}, []string{"mcp", "serve"})
	if err == nil || !strings.Contains(err.Error(), "--scope") {
		t.Fatalf("expected common scope error, got %v", err)
	}
}

func Example_mcpConfiguredServeArgs() {
	opts := &options{profile: "work", account: "default", readOnly: true}
	configure := mcpConfigureOptions{
		mcpToolSelection: mcpToolSelection{
			Profile:      "readonly",
			Tools:        []string{"notion.*"},
			ExcludeTools: []string{"*.delete"},
		},
	}
	fmt.Println(strings.Join(mcpConfiguredServeArgs(opts, configure), " "))
	// Output: mcp serve --profile work --account default --read-only --mcp-profile readonly --tool notion.* --exclude-tool *.delete
}
