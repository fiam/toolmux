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

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
)

type mcpTestResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *mcpError       `json:"error"`
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
		"--mcp-profile", "ops read",
		"--tool", "linear.*",
		"--exclude-tool", "*.send",
		"--scope", "project",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"codex: codex mcp add toolmux-ops-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'ops read' --tool 'linear.*' --exclude-tool '*.send'",
		"claude: claude mcp add --scope project --transport stdio toolmux-ops-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'ops read' --tool 'linear.*' --exclude-tool '*.send'",
		"gemini: gemini mcp add --scope project --transport stdio toolmux-ops-read /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'ops read' --tool 'linear.*' --exclude-tool '*.send'",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestMCPToolUsesActionDescription(t *testing.T) {
	t.Parallel()

	spec := actions.Command("demo.search", "search",
		actions.Short("Search demo"),
		actions.Description("Search demo records visible to the current user."),
		actions.RBAC("record", actions.VerbSearch, actions.EffectRead),
	)
	tool := mcpToolFromSpec(spec)
	if tool.Description != "Search demo records visible to the current user." {
		t.Fatalf("expected MCP tool description from action metadata, got %q", tool.Description)
	}
}

func TestMCPToolsListShowsOnlyRegisteredNativeTools(t *testing.T) {
	t.Parallel()

	store := credentials.NewMemoryStore()
	workDir := t.TempDir()
	if err := writeToolmuxConfigFile(filepath.Join(workDir, toolmuxConfigRelPath), toolmuxConfigFile{
		Version: 1,
		Toolboxes: map[string]toolboxConfig{
			"google": {Type: toolboxTypeInternal, Provider: "google"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "google",
		AccountID: "google",
	}, credentials.OAuthTokens{
		AccessToken: "google-access",
		TokenType:   "Bearer",
		Scopes:      []string{"https://www.googleapis.com/auth/drive.file"},
	}); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store, WorkDir: workDir})
	out := &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"))
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"mcp", "serve"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var response mcpTestResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &response); err != nil {
		t.Fatalf("decode MCP response: %v\n%s", err, out.String())
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode tools/list result: %v\n%s", err, string(response.Result))
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "google.drive.available") {
		t.Fatalf("expected registered Google tool in MCP tools/list, got %#v", names)
	}
	if slices.Contains(names, "slack.auth_test") {
		t.Fatalf("expected unregistered Slack tool to be hidden from MCP tools/list, got %#v", names)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "toolmux.config") || strings.HasPrefix(name, "config.") {
			t.Fatalf("config command must not be exposed as an MCP tool, got %#v", names)
		}
	}
}

func TestMCPConfigureDryRunIncludesMCPToolCallTimeout(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"--mcp-tool-call-timeout", "2m",
		"mcp", "configure", "codex",
		"--dry-run",
		"--command", "/opt/toolmux/bin/toolmux",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "codex: codex mcp add toolmux -- /opt/toolmux/bin/toolmux mcp serve --mcp-tool-call-timeout 2m0s"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, out.String())
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
		"--mcp-profile", "ops read",
		"--tool", "linear.*",
		"--scope", "project",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"codex: codex mcp add toolmux-ops-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'ops read' --tool 'linear.*'",
		"claude: claude mcp add --scope project --transport stdio toolmux-ops-read -- /opt/toolmux/bin/toolmux mcp serve --mcp-profile 'ops read' --tool 'linear.*'",
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
		"--mcp-profile", "ops read",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{
		"claude: claude mcp remove --scope local toolmux-ops-read",
		"claude: claude mcp remove --scope user toolmux-ops-read",
		"claude: claude mcp remove --scope project toolmux-ops-read",
		"gemini: gemini mcp remove --scope user toolmux-ops-read",
		"gemini: gemini mcp remove --scope project toolmux-ops-read",
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
		Tools:        []string{"linear.*"},
		ExcludeTools: []string{"*.send"},
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
	listSpec := actions.Spec{ID: "linear.issue.list", Path: []string{"linear", "issue", "list"}}
	sendSpec := actions.Spec{ID: "linear.message.send", Path: []string{"linear", "message", "send"}}
	if !selector.matches(listSpec) || selector.matches(sendSpec) {
		t.Fatalf("profile filters were not applied")
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
				"global": {Tools: []string{"linear.*"}},
				"shared": {Tools: []string{"linear.*"}},
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
				"project": {Tools: []string{"linear.issue.*"}},
				"shared":  {Tools: []string{"linear.project.*"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveMCPToolSelectionFromPaths(mcpToolSelection{}, dir, globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile != "project" || !slices.Equal(resolved.Tools, []string{"linear.issue.*"}) {
		t.Fatalf("expected project default profile, got %+v", resolved)
	}

	resolved, err = resolveMCPToolSelectionFromPaths(mcpToolSelection{Profile: "shared"}, dir, globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resolved.Tools, []string{"linear.project.*"}) {
		t.Fatalf("expected project profile to override global profile, got %+v", resolved)
	}

	entries, err := effectiveMCPProfileEntriesFromPaths(dir, globalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "shared" && !slices.Equal(entry.Scopes, []string{"global", "project"}) {
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
	opts := &options{profile: "work", readOnly: true}
	configure := mcpConfigureOptions{
		mcpToolSelection: mcpToolSelection{
			Profile:      "readonly",
			Tools:        []string{"linear.*"},
			ExcludeTools: []string{"*.send"},
		},
	}
	fmt.Println(strings.Join(mcpConfiguredServeArgs(opts, configure), " "))
	// Output: mcp serve --profile work --read-only --mcp-profile readonly --tool linear.* --exclude-tool *.send
}
