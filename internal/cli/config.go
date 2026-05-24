package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
)

type toolmuxConfigSource struct {
	Scope  string            `json:"scope" yaml:"scope"`
	Path   string            `json:"path" yaml:"path"`
	config toolmuxConfigFile `json:"-" yaml:"-"`
}

type configPathItem struct {
	Scope  string `json:"scope" yaml:"scope"`
	Path   string `json:"path" yaml:"path"`
	Exists bool   `json:"exists" yaml:"exists"`
	Active bool   `json:"active" yaml:"active"`
}

func configCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and manage Toolmux config files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown config command %q", args[0])
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(configShowCommand(opts))
	cmd.AddCommand(configPathsCommand(opts))
	cmd.AddCommand(configInitCommand(opts))
	cmd.AddCommand(configEditCommand(opts))
	return cmd
}

func configShowCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print Toolmux config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := configForScope(scope, opts.workDir)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, config, nil)
		},
	}
	addConfigReadScopeFlags(cmd, &scope)
	return cmd
}

func configPathsCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "paths",
		Aliases: []string{"path"},
		Short:   "Print Toolmux config file paths",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := configPathItems(opts.workDir)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, paths, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(paths))
				for _, item := range paths {
					rows = append(rows, []string{
						item.Scope,
						item.Path,
						formatBool(item.Exists),
						formatBool(item.Active),
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Scope", "Path", "Exists", "Active"},
					Rows:    rows,
				})
			})
		},
	}
}

func configInitCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a Toolmux config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, scopeName, err := toolmuxConfigWritePath(scope, opts.workDir)
			if err != nil {
				return err
			}
			if !force {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("%s config already exists at %s; pass --force to overwrite", scopeName, path)
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			if err := writeToolmuxConfigFile(path, minimalToolmuxConfig()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "initialized %s config in %s\n", scopeName, path)
			return nil
		},
	}
	addConfigWriteScopeFlags(cmd, &scope)
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}

func configEditCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open a Toolmux config file in $VISUAL or $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _, err := toolmuxConfigWritePath(scope, opts.workDir)
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err := writeToolmuxConfigFile(path, minimalToolmuxConfig()); err != nil {
					return err
				}
			}
			editor := strings.TrimSpace(firstNonEmpty(opts.env("VISUAL"), opts.env("EDITOR")))
			if editor == "" {
				return fmt.Errorf("VISUAL or EDITOR must be set to edit config")
			}
			return runConfigEditor(commandContext(cmd), cmd, editor, path)
		},
	}
	addConfigWriteScopeFlags(cmd, &scope)
	return cmd
}

func addConfigReadScopeFlags(cmd *cobra.Command, scope *mcpProfileScopeOptions) {
	cmd.Flags().BoolVar(&scope.Global, "global", false, "print only the user config")
	cmd.Flags().BoolVar(&scope.Project, "project", false, "print only the project config")
}

func addConfigWriteScopeFlags(cmd *cobra.Command, scope *mcpProfileScopeOptions) {
	cmd.Flags().BoolVar(&scope.Global, "global", false, "write the user config")
	cmd.Flags().BoolVar(&scope.Project, "project", false, "write the project config")
}

func configForScope(scope mcpProfileScopeOptions, startDir string) (toolmuxConfigFile, error) {
	if scope.Global && scope.Project {
		return toolmuxConfigFile{}, fmt.Errorf("use only one of --global or --project")
	}
	if scope.Global {
		path, err := globalToolmuxConfigPath()
		if err != nil {
			return toolmuxConfigFile{}, err
		}
		config, ok, err := readToolmuxConfigFileIfExists(path)
		if err != nil {
			return toolmuxConfigFile{}, err
		}
		if !ok {
			return minimalToolmuxConfig(), nil
		}
		return config, nil
	}
	if scope.Project {
		path, ok, err := discoverToolmuxConfigFile(startDir)
		if err != nil {
			return toolmuxConfigFile{}, err
		}
		globalPath, globalErr := globalToolmuxConfigPath()
		if globalErr != nil {
			return toolmuxConfigFile{}, globalErr
		}
		if !ok || sameFilesystemPath(globalPath, path) {
			return toolmuxConfigFile{}, fmt.Errorf("no project config found; create one with `toolmux config init --project`")
		}
		return readToolmuxConfigFile(path)
	}
	return effectiveToolmuxConfig(startDir)
}

func effectiveToolmuxConfig(startDir string) (toolmuxConfigFile, error) {
	sources, err := loadToolmuxConfigSources(startDir)
	if err != nil {
		return toolmuxConfigFile{}, err
	}
	config := minimalToolmuxConfig()
	for _, source := range sources {
		config = mergeToolmuxConfig(config, source.config)
	}
	return config, nil
}

func loadToolmuxConfigSources(startDir string) ([]toolmuxConfigSource, error) {
	globalPath, err := globalToolmuxConfigPath()
	if err != nil {
		return nil, err
	}
	var sources []toolmuxConfigSource
	if config, ok, err := readToolmuxConfigFileIfExists(globalPath); err != nil {
		return nil, err
	} else if ok {
		sources = append(sources, toolmuxConfigSource{Scope: "global", Path: globalPath, config: config})
	}
	if projectPath, ok, err := discoverToolmuxConfigFile(startDir); err != nil {
		return nil, err
	} else if ok && !sameFilesystemPath(globalPath, projectPath) {
		config, err := readToolmuxConfigFile(projectPath)
		if err != nil {
			return nil, err
		}
		sources = append(sources, toolmuxConfigSource{Scope: "project", Path: projectPath, config: config})
	}
	return sources, nil
}

func configPathItems(startDir string) ([]configPathItem, error) {
	globalPath, err := globalToolmuxConfigPath()
	if err != nil {
		return nil, err
	}
	globalExists, err := fileExists(globalPath)
	if err != nil {
		return nil, err
	}
	projectPath, projectExists, err := discoverToolmuxConfigFile(startDir)
	if err != nil {
		return nil, err
	}
	if projectExists && sameFilesystemPath(globalPath, projectPath) {
		projectPath = ""
		projectExists = false
	}
	if !projectExists {
		projectPath, err = projectConfigCandidatePath(startDir)
		if err != nil {
			return nil, err
		}
	}
	return []configPathItem{
		{
			Scope:  "global",
			Path:   globalPath,
			Exists: globalExists,
			Active: globalExists,
		},
		{
			Scope:  "project",
			Path:   projectPath,
			Exists: projectExists,
			Active: projectExists && !sameFilesystemPath(globalPath, projectPath),
		},
	}, nil
}

func projectConfigCandidatePath(startDir string) (string, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, toolmuxConfigRelPath), nil
}

func fileExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

func minimalToolmuxConfig() toolmuxConfigFile {
	return toolmuxConfigFile{Version: 1}
}

func mergeToolmuxConfig(base, overlay toolmuxConfigFile) toolmuxConfigFile {
	merged := cloneToolmuxConfig(base)
	if merged.Version == 0 {
		merged.Version = 1
	}
	if overlay.MCP.DefaultProfile != "" {
		merged.MCP.DefaultProfile = overlay.MCP.DefaultProfile
	}
	if len(overlay.Toolboxes) > 0 {
		if merged.Toolboxes == nil {
			merged.Toolboxes = map[string]toolboxConfig{}
		}
		for name, toolbox := range overlay.Toolboxes {
			merged.Toolboxes[name] = cloneToolboxConfig(toolbox)
		}
	}
	if len(overlay.MCP.Profiles) > 0 {
		if merged.MCP.Profiles == nil {
			merged.MCP.Profiles = map[string]mcpProfileConfig{}
		}
		for name, profile := range overlay.MCP.Profiles {
			merged.MCP.Profiles[name] = cloneMCPProfileConfig(profile)
		}
	}
	if len(overlay.MCP.Servers) > 0 {
		if merged.MCP.Servers == nil {
			merged.MCP.Servers = map[string]mcpRemoteServer{}
		}
		for name, server := range overlay.MCP.Servers {
			merged.MCP.Servers[name] = cloneMCPRemoteServer(server)
		}
	}
	if overlay.Workflows.DefaultAgent != "" {
		merged.Workflows.DefaultAgent = overlay.Workflows.DefaultAgent
	}
	if len(overlay.Workflows.Agents) > 0 {
		if merged.Workflows.Agents == nil {
			merged.Workflows.Agents = map[string]workflowAgentConfig{}
		}
		for name, agent := range overlay.Workflows.Agents {
			merged.Workflows.Agents[name] = cloneWorkflowAgentConfig(agent)
		}
	}
	return merged
}

func cloneToolmuxConfig(config toolmuxConfigFile) toolmuxConfigFile {
	clone := toolmuxConfigFile{Version: config.Version}
	if len(config.Toolboxes) > 0 {
		clone.Toolboxes = make(map[string]toolboxConfig, len(config.Toolboxes))
		for name, toolbox := range config.Toolboxes {
			clone.Toolboxes[name] = cloneToolboxConfig(toolbox)
		}
	}
	clone.MCP.DefaultProfile = config.MCP.DefaultProfile
	if len(config.MCP.Profiles) > 0 {
		clone.MCP.Profiles = make(map[string]mcpProfileConfig, len(config.MCP.Profiles))
		for name, profile := range config.MCP.Profiles {
			clone.MCP.Profiles[name] = cloneMCPProfileConfig(profile)
		}
	}
	if len(config.MCP.Servers) > 0 {
		clone.MCP.Servers = make(map[string]mcpRemoteServer, len(config.MCP.Servers))
		for name, server := range config.MCP.Servers {
			clone.MCP.Servers[name] = cloneMCPRemoteServer(server)
		}
	}
	clone.Workflows.DefaultAgent = config.Workflows.DefaultAgent
	if len(config.Workflows.Agents) > 0 {
		clone.Workflows.Agents = make(map[string]workflowAgentConfig, len(config.Workflows.Agents))
		for name, agent := range config.Workflows.Agents {
			clone.Workflows.Agents[name] = cloneWorkflowAgentConfig(agent)
		}
	}
	return clone
}

func cloneToolboxConfig(toolbox toolboxConfig) toolboxConfig {
	clone := toolboxConfig{
		Type:             toolbox.Type,
		Provider:         toolbox.Provider,
		Catalog:          toolbox.Catalog,
		URL:              toolbox.URL,
		Command:          toolbox.Command,
		Args:             append([]string(nil), toolbox.Args...),
		Transport:        toolbox.Transport,
		AuthRequired:     toolbox.AuthRequired,
		DefaultArguments: maps.Clone(toolbox.DefaultArguments),
	}
	if toolbox.AuthRequired != nil {
		authRequired := *toolbox.AuthRequired
		clone.AuthRequired = &authRequired
	}
	return clone
}

func cloneMCPProfileConfig(profile mcpProfileConfig) mcpProfileConfig {
	return mcpProfileConfig{
		Tools:            append([]string(nil), profile.Tools...),
		ToolRegex:        append([]string(nil), profile.ToolRegex...),
		ExcludeTools:     append([]string(nil), profile.ExcludeTools...),
		ExcludeToolRegex: append([]string(nil), profile.ExcludeToolRegex...),
	}
}

func cloneMCPRemoteServer(server mcpRemoteServer) mcpRemoteServer {
	clone := mcpRemoteServer{
		URL:          server.URL,
		Command:      server.Command,
		Args:         append([]string(nil), server.Args...),
		Transport:    server.Transport,
		AuthRequired: server.AuthRequired,
	}
	if server.AuthRequired != nil {
		authRequired := *server.AuthRequired
		clone.AuthRequired = &authRequired
	}
	if len(server.DefaultArguments) > 0 {
		clone.DefaultArguments = maps.Clone(server.DefaultArguments)
	}
	return clone
}

func cloneWorkflowAgentConfig(agent workflowAgentConfig) workflowAgentConfig {
	return workflowAgentConfig{
		Command: agent.Command,
		Args:    append([]string(nil), agent.Args...),
	}
}

func runConfigEditor(ctx context.Context, cmd *cobra.Command, editor, path string) error {
	editorArgs := strings.Fields(editor)
	if len(editorArgs) == 0 {
		return fmt.Errorf("VISUAL or EDITOR must name an executable")
	}
	commandArgs := append(append([]string(nil), editorArgs[1:]...), path)
	// #nosec G204 -- editor comes from explicit VISUAL/EDITOR configuration.
	edit := exec.CommandContext(ctx, editorArgs[0], commandArgs...)
	edit.Stdin = cmd.InOrStdin()
	edit.Stdout = cmd.OutOrStdout()
	edit.Stderr = cmd.ErrOrStderr()
	return edit.Run()
}

func formatBool(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
