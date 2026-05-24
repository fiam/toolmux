//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
)

func TestConfigShowMergesGlobalAndProject(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	projectDir := t.TempDir()
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
	projectConfigPath := filepath.Join(projectDir, toolmuxConfigRelPath)

	if err := writeToolmuxConfigFile(env.Config, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			DefaultProfile: "global-default",
			Profiles: map[string]mcpProfileConfig{
				"global-only": {Tools: []string{"slack.*"}},
				"overlap":     {Tools: []string{"global.*"}},
			},
			Servers: map[string]mcpRemoteServer{
				"linear": {URL: "https://global.example/mcp"},
			},
		},
		Workflows: workflowConfig{
			DefaultAgent: "global-agent",
			Agents: map[string]workflowAgentConfig{
				"global-agent": {Command: "codex"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeToolmuxConfigFile(projectConfigPath, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			DefaultProfile: "project-default",
			Profiles: map[string]mcpProfileConfig{
				"project-only": {Tools: []string{"google.*"}},
				"overlap":      {Tools: []string{"project.*"}},
			},
			Servers: map[string]mcpRemoteServer{
				"notion": {URL: "https://project.example/mcp"},
			},
		},
		Workflows: workflowConfig{
			DefaultAgent: "project-agent",
			Agents: map[string]workflowAgentConfig{
				"project-agent": {Command: "claude"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	merged := decodeConfigOutput(t, runRootForRemoteTest(t, env, "--output", "json", "config", "show"))
	if merged.MCP.DefaultProfile != "project-default" {
		t.Fatalf("expected project default profile, got %#v", merged.MCP.DefaultProfile)
	}
	if !slices.Equal(merged.MCP.Profiles["overlap"].Tools, []string{"project.*"}) {
		t.Fatalf("expected project profile override, got %#v", merged.MCP.Profiles["overlap"])
	}
	if _, ok := merged.MCP.Profiles["global-only"]; !ok {
		t.Fatalf("expected global profile to remain, got %#v", merged.MCP.Profiles)
	}
	if _, ok := merged.MCP.Profiles["project-only"]; !ok {
		t.Fatalf("expected project profile, got %#v", merged.MCP.Profiles)
	}
	if toolbox, ok := merged.Toolboxes["linear"]; !ok || toolbox.Type != toolboxTypeMCP {
		t.Fatalf("expected global toolbox, got %#v", merged.Toolboxes)
	}
	if toolbox, ok := merged.Toolboxes["notion"]; !ok || toolbox.Type != toolboxTypeMCP {
		t.Fatalf("expected project toolbox, got %#v", merged.Toolboxes)
	}
	if merged.Workflows.DefaultAgent != "project-agent" {
		t.Fatalf("expected project workflow default, got %#v", merged.Workflows.DefaultAgent)
	}
	if _, ok := merged.Workflows.Agents["global-agent"]; !ok {
		t.Fatalf("expected global workflow agent, got %#v", merged.Workflows.Agents)
	}
	if _, ok := merged.Workflows.Agents["project-agent"]; !ok {
		t.Fatalf("expected project workflow agent, got %#v", merged.Workflows.Agents)
	}

	global := decodeConfigOutput(t, runRootForRemoteTest(t, env, "--output", "json", "config", "show", "--global"))
	if global.MCP.DefaultProfile != "global-default" {
		t.Fatalf("expected global-only config, got %#v", global.MCP.DefaultProfile)
	}
	if _, ok := global.MCP.Profiles["project-only"]; ok {
		t.Fatalf("global config included project profile: %#v", global.MCP.Profiles)
	}

	project := decodeConfigOutput(t, runRootForRemoteTest(t, env, "--output", "json", "config", "show", "--project"))
	if project.MCP.DefaultProfile != "project-default" {
		t.Fatalf("expected project-only config, got %#v", project.MCP.DefaultProfile)
	}
	if _, ok := project.MCP.Profiles["global-only"]; ok {
		t.Fatalf("project config included global profile: %#v", project.MCP.Profiles)
	}
}

func TestConfigPathsShowsGlobalAndProject(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	projectDir := t.TempDir()
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
	projectConfigPath := filepath.Join(projectDir, toolmuxConfigRelPath)

	paths := decodeConfigPathsOutput(t, runRootForRemoteTest(t, env, "--output", "json", "config", "paths"))
	global, project := configPathByScope(t, paths, "global"), configPathByScope(t, paths, "project")
	if global.Path != env.Config || global.Exists || global.Active {
		t.Fatalf("unexpected global path before init: %#v", global)
	}
	if project.Path != projectConfigPath || project.Exists || project.Active {
		t.Fatalf("unexpected project path before init: %#v", project)
	}

	if err := writeToolmuxConfigFile(env.Config, minimalToolmuxConfig()); err != nil {
		t.Fatal(err)
	}
	if err := writeToolmuxConfigFile(projectConfigPath, minimalToolmuxConfig()); err != nil {
		t.Fatal(err)
	}
	paths = decodeConfigPathsOutput(t, runRootForRemoteTest(t, env, "--output", "json", "config", "paths"))
	global, project = configPathByScope(t, paths, "global"), configPathByScope(t, paths, "project")
	if !global.Exists || !global.Active {
		t.Fatalf("expected active global config, got %#v", global)
	}
	if !project.Exists || !project.Active {
		t.Fatalf("expected active project config, got %#v", project)
	}
}

func TestConfigInitCreatesConfigAndRefusesOverwrite(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	output := runRootForRemoteTest(t, env, "config", "init", "--global")
	if !strings.Contains(output, "initialized global config") {
		t.Fatalf("expected init output, got %q", output)
	}
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if config.Version != 1 {
		t.Fatalf("expected version 1 config, got %#v", config)
	}
	_, err = runRootForRemoteTestError(t, env, "config", "init", "--global")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite error, got %v", err)
	}
	runRootForRemoteTest(t, env, "config", "init", "--global", "--force")

	projectDir := t.TempDir()
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
	runRootForRemoteTest(t, env, "config", "init", "--project")
	if _, err := os.Stat(filepath.Join(projectDir, toolmuxConfigRelPath)); err != nil {
		t.Fatalf("expected project config: %v", err)
	}
}

func TestConfigProjectWriteIgnoresGlobalConfigInParent(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	if err := writeToolmuxConfigFile(env.Config, minimalToolmuxConfig()); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(env.Home, "repo")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)

	output := runRootForRemoteTest(t, env, "config", "init", "--project")
	if !strings.Contains(output, "initialized project config") {
		t.Fatalf("expected project init output, got %q", output)
	}
	projectConfigPath := filepath.Join(projectDir, toolmuxConfigRelPath)
	if _, err := os.Stat(projectConfigPath); err != nil {
		t.Fatalf("expected project config at %s: %v", projectConfigPath, err)
	}

	paths := decodeConfigPathsOutput(t, runRootForRemoteTest(t, env, "--output", "json", "config", "paths"))
	project := configPathByScope(t, paths, "project")
	if project.Path != projectConfigPath || !project.Exists || !project.Active {
		t.Fatalf("expected active project config below home, got %#v", project)
	}
}

func TestConfigShowProjectRequiresExistingProjectConfig(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	projectDir := t.TempDir()
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
	_, err := runRootForRemoteTestError(t, env, "config", "show", "--project")
	if err == nil || !strings.Contains(err.Error(), "no project config found") {
		t.Fatalf("expected missing project config error, got %v", err)
	}
}

func TestConfigEditRequiresEditor(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials: credentials.NewMemoryStore(),
		Env: func(name string) string {
			if name == "TOOLMUX_MCP_CACHE_DIR" {
				return env.CacheDir
			}
			return os.Getenv(name)
		},
	})
	cmd.SetArgs([]string{"config", "edit", "--global"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "VISUAL or EDITOR") {
		t.Fatalf("expected missing editor error, got %v", err)
	}
}

func TestConfigCommandsStayOutsidePolicy(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	_, err := runRootForRemoteTestError(t, env, "policy", "check", "--command", "config show")
	if err == nil || !strings.Contains(err.Error(), "no command spec found") {
		t.Fatalf("expected config command outside policy, got %v", err)
	}
}

func decodeConfigOutput(t *testing.T, output string) toolmuxConfigFile {
	t.Helper()
	var config toolmuxConfigFile
	if err := json.Unmarshal([]byte(output), &config); err != nil {
		t.Fatalf("decode config output: %v\n%s", err, output)
	}
	return config
}

func decodeConfigPathsOutput(t *testing.T, output string) []configPathItem {
	t.Helper()
	var paths []configPathItem
	if err := json.Unmarshal([]byte(output), &paths); err != nil {
		t.Fatalf("decode config paths output: %v\n%s", err, output)
	}
	return paths
}

func configPathByScope(t *testing.T, paths []configPathItem, scope string) configPathItem {
	t.Helper()
	for _, item := range paths {
		if item.Scope == scope {
			return item
		}
	}
	t.Fatalf("missing %s config path in %#v", scope, paths)
	return configPathItem{}
}
