package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/fiam/supacli/internal/actions"
	"github.com/fiam/supacli/internal/providers"
	"github.com/fiam/supacli/internal/version"
)

const mcpProtocolVersion = "2025-06-18"
const mcpProfilesRelPath = ".supacli/mcp-profiles.yaml"
const mcpDefaultProfileName = "default"

type mcpToolSelection struct {
	Profile          string
	Tools            []string
	ToolRegex        []string
	ExcludeTools     []string
	ExcludeToolRegex []string
}

type mcpConfigureOptions struct {
	mcpToolSelection
	Command     string
	ServerName  string
	ClaudeScope string
	GeminiScope string
	DryRun      bool
}

type mcpProfileScopeOptions struct {
	Global bool
	Local  bool
}

func mcpCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve and configure MCP tools",
	}
	cmd.AddCommand(mcpServeCommand(opts))
	cmd.AddCommand(mcpConfigureCommand(opts))
	cmd.AddCommand(mcpProfileCommand(opts))
	return cmd
}

func mcpServeCommand(opts *options) *cobra.Command {
	var selection mcpToolSelection
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve Supacli provider actions over MCP stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveMCPToolSelection(selection)
			if err != nil {
				return err
			}
			selector, err := newMCPToolSelector(resolved)
			if err != nil {
				return err
			}
			server := mcpServer{
				opts:     opts,
				cmd:      cmd,
				selector: selector,
			}
			return server.run(commandContext(cmd), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	addMCPToolSelectionFlags(cmd, &selection)
	return cmd
}

func mcpConfigureCommand(opts *options) *cobra.Command {
	configure := mcpConfigureOptions{
		Command:     "supacli",
		ClaudeScope: "local",
		GeminiScope: "project",
	}
	cmd := &cobra.Command{
		Use:   "configure [agent...]",
		Short: "Configure installed agents to use Supacli MCP",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveMCPToolSelection(configure.mcpToolSelection)
			if err != nil {
				return err
			}
			if _, err := newMCPToolSelector(resolved); err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpConfigureSpec(), args); err != nil {
				return err
			}
			runtime := mcpAgentRuntime{
				lookPath: exec.LookPath,
				run:      runMCPAgentCommand,
			}
			results, err := configureMCPAgents(commandContext(cmd), runtime, opts, configure, args)
			if err != nil {
				return err
			}
			for _, result := range results {
				if configure.DryRun {
					for _, command := range result.Commands {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", result.Agent, command.String())
					}
					continue
				}
				detail := ""
				if result.Scope != "" {
					detail = " (" + result.Scope + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "configured %s MCP server %s%s\n", result.Agent, result.ServerName, detail)
			}
			return nil
		},
	}
	addMCPToolSelectionFlags(cmd, &configure.mcpToolSelection)
	cmd.Flags().StringVar(&configure.Command, "command", "supacli", "supacli executable configured for agent MCP launches")
	cmd.Flags().StringVar(&configure.ServerName, "server-name", "", "MCP server name configured in agents")
	cmd.Flags().StringVar(&configure.ClaudeScope, "claude-scope", "local", "Claude Code MCP scope: local, user, or project")
	cmd.Flags().StringVar(&configure.GeminiScope, "gemini-scope", "project", "Gemini CLI MCP scope: user or project")
	cmd.Flags().BoolVar(&configure.DryRun, "dry-run", false, "print agent configuration commands without running them")
	return cmd
}

func mcpProfileCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage MCP tool profiles",
	}

	var setSelection mcpToolSelection
	var setScope mcpProfileScopeOptions
	set := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or update an MCP tool profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}
			setSelection.Profile = name
			if _, err := newMCPToolSelector(setSelection); err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpProfileSetSpec(), args); err != nil {
				return err
			}
			configPath, scope, err := mcpProfileWritePath(setScope)
			if err != nil {
				return err
			}
			config, err := readMCPProfileFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if config.Profiles == nil {
				config.Profiles = map[string]mcpProfileConfig{}
			}
			config.Version = 1
			config.Profiles[name] = mcpProfileConfigFromSelection(setSelection)
			if err := writeMCPProfileFile(configPath, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved %s MCP profile %s in %s\n", scope, name, configPath)
			return nil
		},
	}
	addMCPToolPatternFlags(set, &setSelection)
	addMCPProfileScopeFlags(set, &setScope)
	cmd.AddCommand(set)

	var defaultScope mcpProfileScopeOptions
	defaultCommand := &cobra.Command{
		Use:   "default <name>",
		Short: "Set the default MCP tool profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}
			if err := authorize(cmd, opts, mcpProfileDefaultSpec(), args); err != nil {
				return err
			}
			configPath, scope, err := mcpProfileWritePath(defaultScope)
			if err != nil {
				return err
			}
			config, err := readMCPProfileFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if config.Profiles == nil {
				config.Profiles = map[string]mcpProfileConfig{}
			}
			if scope == "global" {
				if _, ok := config.Profiles[name]; !ok {
					return fmt.Errorf("MCP profile %q not found in %s", name, configPath)
				}
			} else {
				if _, ok := config.Profiles[name]; !ok {
					if _, ok, err := lookupMCPProfile(name, ""); err != nil {
						return err
					} else if !ok {
						return fmt.Errorf("MCP profile %q not found", name)
					}
				}
			}
			config.Version = 1
			config.DefaultProfile = name
			if err := writeMCPProfileFile(configPath, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set %s default MCP profile to %s in %s\n", scope, name, configPath)
			return nil
		},
	}
	addMCPProfileScopeFlags(defaultCommand, &defaultScope)
	cmd.AddCommand(defaultCommand)

	cmd.AddCommand(&cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List MCP tool profiles",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := effectiveMCPProfileEntries("")
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no MCP profiles configured")
				return nil
			}
			for _, entry := range entries {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", entry.Name, strings.Join(entry.Scopes, ","))
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show an MCP tool profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			entry, ok, err := lookupMCPProfile(name, "")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP profile %q not found", name)
			}
			return writeValue(cmd, opts, entry, nil)
		},
	})

	return cmd
}

func mcpConfigureSpec() actions.Spec {
	return actions.Command("supacli.mcp.configure", "configure",
		actions.Use("mcp configure [agent...]"),
		actions.Short("Configure agent MCP settings"),
		actions.RBAC("agent_config", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("agent-config"),
	)
}

func mcpProfileSetSpec() actions.Spec {
	return actions.Command("supacli.mcp.profile.set", "set",
		actions.Use("mcp profile set <name>"),
		actions.Short("Create or update MCP tool profile"),
		actions.RBAC("mcp_profile", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("agent-config"),
		actions.ExactArgs(1),
	)
}

func mcpProfileDefaultSpec() actions.Spec {
	return actions.Command("supacli.mcp.profile.default", "default",
		actions.Use("mcp profile default <name>"),
		actions.Short("Set default MCP tool profile"),
		actions.RBAC("mcp_profile", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("agent-config"),
		actions.ExactArgs(1),
	)
}

func addMCPToolSelectionFlags(cmd *cobra.Command, selection *mcpToolSelection) {
	cmd.Flags().StringVar(&selection.Profile, "mcp-profile", "", "name for this MCP tool profile")
	addMCPToolPatternFlags(cmd, selection)
}

func addMCPToolPatternFlags(cmd *cobra.Command, selection *mcpToolSelection) {
	cmd.Flags().StringArrayVar(&selection.Tools, "tool", nil, "shell-style tool glob to include, such as notion.*")
	cmd.Flags().StringArrayVar(&selection.ToolRegex, "tool-regex", nil, "regular expression for tool IDs to include")
	cmd.Flags().StringArrayVar(&selection.ExcludeTools, "exclude-tool", nil, "shell-style tool glob to exclude")
	cmd.Flags().StringArrayVar(&selection.ExcludeToolRegex, "exclude-tool-regex", nil, "regular expression for tool IDs to exclude")
}

func addMCPProfileScopeFlags(cmd *cobra.Command, scope *mcpProfileScopeOptions) {
	cmd.Flags().BoolVar(&scope.Global, "global", false, "write the profile to the user config")
	cmd.Flags().BoolVar(&scope.Local, "local", false, "write the profile to the project config")
}

type mcpToolSelector struct {
	includeGlobs []string
	includeRegex []*regexp.Regexp
	excludeGlobs []string
	excludeRegex []*regexp.Regexp
	includeAll   bool
	profile      string
}

func newMCPToolSelector(selection mcpToolSelection) (mcpToolSelector, error) {
	selector := mcpToolSelector{
		includeGlobs: compactStrings(selection.Tools),
		excludeGlobs: compactStrings(selection.ExcludeTools),
		profile:      strings.TrimSpace(selection.Profile),
	}
	for _, pattern := range append(selector.includeGlobs, selector.excludeGlobs...) {
		if _, err := path.Match(pattern, "supacli.test"); err != nil {
			return mcpToolSelector{}, fmt.Errorf("invalid MCP tool glob %q: %w", pattern, err)
		}
	}
	for _, pattern := range compactStrings(selection.ToolRegex) {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return mcpToolSelector{}, fmt.Errorf("invalid MCP tool regex %q: %w", pattern, err)
		}
		selector.includeRegex = append(selector.includeRegex, compiled)
	}
	for _, pattern := range compactStrings(selection.ExcludeToolRegex) {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return mcpToolSelector{}, fmt.Errorf("invalid MCP exclude regex %q: %w", pattern, err)
		}
		selector.excludeRegex = append(selector.excludeRegex, compiled)
	}
	selector.includeAll = len(selector.includeGlobs) == 0 && len(selector.includeRegex) == 0
	return selector, nil
}

func (selector mcpToolSelector) matches(spec actions.Spec) bool {
	targets := mcpToolTargets(spec)
	if selector.excluded(targets) {
		return false
	}
	if selector.includeAll {
		return true
	}
	return selector.included(targets)
}

func (selector mcpToolSelector) included(targets []string) bool {
	for _, target := range targets {
		for _, glob := range selector.includeGlobs {
			if matched, _ := path.Match(glob, target); matched {
				return true
			}
		}
		for _, regex := range selector.includeRegex {
			if regex.MatchString(target) {
				return true
			}
		}
	}
	return false
}

func (selector mcpToolSelector) excluded(targets []string) bool {
	for _, target := range targets {
		for _, glob := range selector.excludeGlobs {
			if matched, _ := path.Match(glob, target); matched {
				return true
			}
		}
		for _, regex := range selector.excludeRegex {
			if regex.MatchString(target) {
				return true
			}
		}
	}
	return false
}

func mcpToolTargets(spec actions.Spec) []string {
	pathWithDots := strings.Join(spec.Path, ".")
	pathWithSpaces := strings.Join(spec.Path, " ")
	targets := []string{spec.ID}
	if pathWithDots != "" && pathWithDots != spec.ID {
		targets = append(targets, pathWithDots)
	}
	if pathWithSpaces != "" {
		targets = append(targets, pathWithSpaces)
	}
	return targets
}

func mcpToolSelectionArgs(selection mcpToolSelection) []string {
	var args []string
	if profile := strings.TrimSpace(selection.Profile); profile != "" {
		args = append(args, "--mcp-profile", profile)
	}
	for _, value := range compactStrings(selection.Tools) {
		args = append(args, "--tool", value)
	}
	for _, value := range compactStrings(selection.ToolRegex) {
		args = append(args, "--tool-regex", value)
	}
	for _, value := range compactStrings(selection.ExcludeTools) {
		args = append(args, "--exclude-tool", value)
	}
	for _, value := range compactStrings(selection.ExcludeToolRegex) {
		args = append(args, "--exclude-tool-regex", value)
	}
	return args
}

func compactStrings(values []string) []string {
	compact := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			compact = append(compact, value)
		}
	}
	return compact
}

type mcpProfileFile struct {
	Version        int                         `json:"version" yaml:"version"`
	DefaultProfile string                      `json:"default_profile,omitempty" yaml:"default_profile,omitempty"`
	Profiles       map[string]mcpProfileConfig `json:"profiles" yaml:"profiles"`
}

type mcpProfileConfig struct {
	Tools            []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	ToolRegex        []string `json:"tool_regex,omitempty" yaml:"tool_regex,omitempty"`
	ExcludeTools     []string `json:"exclude_tools,omitempty" yaml:"exclude_tools,omitempty"`
	ExcludeToolRegex []string `json:"exclude_tool_regex,omitempty" yaml:"exclude_tool_regex,omitempty"`
}

type mcpProfileEntry struct {
	Name    string           `json:"name" yaml:"name"`
	Scope   string           `json:"scope" yaml:"scope"`
	Scopes  []string         `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path    string           `json:"path" yaml:"path"`
	Profile mcpProfileConfig `json:"profile" yaml:"profile"`
}

type mcpEffectiveProfileConfig struct {
	DefaultProfile string             `json:"default_profile,omitempty" yaml:"default_profile,omitempty"`
	Sources        []mcpProfileSource `json:"sources,omitempty" yaml:"sources,omitempty"`
}

func resolveMCPToolSelection(selection mcpToolSelection) (mcpToolSelection, error) {
	return resolveMCPToolSelectionFromDir(selection, "")
}

func resolveMCPToolSelectionFromDir(selection mcpToolSelection, startDir string) (mcpToolSelection, error) {
	globalPath, err := globalMCPProfilePath()
	if err != nil {
		return mcpToolSelection{}, err
	}
	return resolveMCPToolSelectionFromPaths(selection, startDir, globalPath)
}

func resolveMCPToolSelectionFromPaths(selection mcpToolSelection, startDir, globalPath string) (mcpToolSelection, error) {
	config, err := effectiveMCPProfileConfig(startDir, globalPath)
	if err != nil {
		return mcpToolSelection{}, err
	}
	name := strings.TrimSpace(selection.Profile)
	explicit := name != ""
	configuredDefault := false
	if name == "" {
		if hasInlineMCPToolSelection(selection) {
			return selection, nil
		}
		if config.DefaultProfile != "" {
			name = config.DefaultProfile
			configuredDefault = true
		} else {
			name = mcpDefaultProfileName
		}
	}
	entry, ok := config.lookup(name)
	if !ok {
		if explicit && hasInlineMCPToolSelection(selection) {
			return selection, nil
		}
		if explicit {
			return mcpToolSelection{}, fmt.Errorf("MCP profile %q not found; create it with `supacli mcp profile set %s`", name, name)
		}
		if configuredDefault {
			return mcpToolSelection{}, fmt.Errorf("default MCP profile %q not found", name)
		}
		return selection, nil
	}
	resolved := entry.Profile.selection(name)
	resolved.Tools = append(resolved.Tools, selection.Tools...)
	resolved.ToolRegex = append(resolved.ToolRegex, selection.ToolRegex...)
	resolved.ExcludeTools = append(resolved.ExcludeTools, selection.ExcludeTools...)
	resolved.ExcludeToolRegex = append(resolved.ExcludeToolRegex, selection.ExcludeToolRegex...)
	return resolved, nil
}

func effectiveMCPProfileConfig(startDir, globalPath string) (mcpEffectiveProfileConfig, error) {
	sources, err := loadMCPProfileSources(startDir, globalPath)
	if err != nil {
		return mcpEffectiveProfileConfig{}, err
	}
	config := mcpEffectiveProfileConfig{Sources: sources}
	for _, source := range sources {
		if source.config.DefaultProfile != "" {
			config.DefaultProfile = source.config.DefaultProfile
		}
	}
	return config, nil
}

func (config mcpEffectiveProfileConfig) lookup(name string) (mcpProfileEntry, bool) {
	var found mcpProfileEntry
	ok := false
	for _, source := range config.Sources {
		profile, exists := source.config.Profiles[name]
		if !exists {
			continue
		}
		scopes := []string{source.Scope}
		if ok {
			scopes = append(found.Scopes, source.Scope)
		}
		found = mcpProfileEntry{
			Name:    name,
			Scope:   source.Scope,
			Scopes:  scopes,
			Path:    source.Path,
			Profile: profile,
		}
		ok = true
	}
	return found, ok
}

func hasInlineMCPToolSelection(selection mcpToolSelection) bool {
	return len(compactStrings(selection.Tools)) > 0 ||
		len(compactStrings(selection.ToolRegex)) > 0 ||
		len(compactStrings(selection.ExcludeTools)) > 0 ||
		len(compactStrings(selection.ExcludeToolRegex)) > 0
}

func (profile mcpProfileConfig) selection(name string) mcpToolSelection {
	return mcpToolSelection{
		Profile:          name,
		Tools:            append([]string(nil), profile.Tools...),
		ToolRegex:        append([]string(nil), profile.ToolRegex...),
		ExcludeTools:     append([]string(nil), profile.ExcludeTools...),
		ExcludeToolRegex: append([]string(nil), profile.ExcludeToolRegex...),
	}
}

func mcpProfileConfigFromSelection(selection mcpToolSelection) mcpProfileConfig {
	return mcpProfileConfig{
		Tools:            compactStrings(selection.Tools),
		ToolRegex:        compactStrings(selection.ToolRegex),
		ExcludeTools:     compactStrings(selection.ExcludeTools),
		ExcludeToolRegex: compactStrings(selection.ExcludeToolRegex),
	}
}

func mcpProfileWritePath(scope mcpProfileScopeOptions) (string, string, error) {
	if scope.Global && scope.Local {
		return "", "", fmt.Errorf("use only one of --global or --local")
	}
	if scope.Global {
		path, err := globalMCPProfilePath()
		return path, "global", err
	}
	return filepath.Join(".supacli", "mcp-profiles.yaml"), "local", nil
}

func lookupMCPProfile(name, startDir string) (mcpProfileEntry, bool, error) {
	globalPath, err := globalMCPProfilePath()
	if err != nil {
		return mcpProfileEntry{}, false, err
	}
	return lookupMCPProfileFromPaths(name, startDir, globalPath)
}

func lookupMCPProfileFromPaths(name, startDir, globalPath string) (mcpProfileEntry, bool, error) {
	entries, err := effectiveMCPProfileEntriesFromPaths(startDir, globalPath)
	if err != nil {
		return mcpProfileEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return mcpProfileEntry{}, false, nil
}

func effectiveMCPProfileEntries(startDir string) ([]mcpProfileEntry, error) {
	globalPath, err := globalMCPProfilePath()
	if err != nil {
		return nil, err
	}
	return effectiveMCPProfileEntriesFromPaths(startDir, globalPath)
}

func effectiveMCPProfileEntriesFromPaths(startDir, globalPath string) ([]mcpProfileEntry, error) {
	sources, err := loadMCPProfileSources(startDir, globalPath)
	if err != nil {
		return nil, err
	}
	byName := map[string]mcpProfileEntry{}
	for _, source := range sources {
		names := make([]string, 0, len(source.config.Profiles))
		for name := range source.config.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			profile := source.config.Profiles[name]
			existing, exists := byName[name]
			scopes := []string{source.Scope}
			if exists {
				scopes = append(existing.Scopes, source.Scope)
			}
			byName[name] = mcpProfileEntry{
				Name:    name,
				Scope:   source.Scope,
				Scopes:  scopes,
				Path:    source.Path,
				Profile: profile,
			}
		}
	}
	entries := make([]mcpProfileEntry, 0, len(byName))
	for _, entry := range byName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

type mcpProfileSource struct {
	Scope  string `json:"scope" yaml:"scope"`
	Path   string `json:"path" yaml:"path"`
	config mcpProfileFile
}

func loadMCPProfileSources(startDir, globalPath string) ([]mcpProfileSource, error) {
	var sources []mcpProfileSource
	if globalPath != "" {
		if config, ok, err := readMCPProfileFileIfExists(globalPath); err != nil {
			return nil, err
		} else if ok {
			sources = append(sources, mcpProfileSource{Scope: "global", Path: globalPath, config: config})
		}
	}
	if localPath, ok, err := discoverMCPProfileFile(startDir); err != nil {
		return nil, err
	} else if ok {
		config, err := readMCPProfileFile(localPath)
		if err != nil {
			return nil, err
		}
		sources = append(sources, mcpProfileSource{Scope: "local", Path: localPath, config: config})
	}
	return sources, nil
}

func globalMCPProfilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "supacli", "mcp-profiles.yaml"), nil
}

func discoverMCPProfileFile(startDir string) (string, bool, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", false, err
		}
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false, err
	}
	for {
		candidate := filepath.Join(dir, mcpProfilesRelPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false, nil
}

func readMCPProfileFile(path string) (mcpProfileFile, error) {
	// #nosec G304 -- MCP profile paths are explicit local configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return mcpProfileFile{}, err
	}
	var config mcpProfileFile
	if err := yaml.Unmarshal(data, &config); err != nil {
		return mcpProfileFile{}, err
	}
	if config.Version == 0 {
		config.Version = 1
	}
	if config.Profiles == nil {
		config.Profiles = map[string]mcpProfileConfig{}
	}
	return config, nil
}

func readMCPProfileFileIfExists(path string) (mcpProfileFile, bool, error) {
	config, err := readMCPProfileFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mcpProfileFile{}, false, nil
		}
		return mcpProfileFile{}, false, err
	}
	return config, true, nil
}

func writeMCPProfileFile(path string, config mcpProfileFile) error {
	if config.Version == 0 {
		config.Version = 1
	}
	if config.Profiles == nil {
		config.Profiles = map[string]mcpProfileConfig{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	// #nosec G306 -- MCP profiles are non-secret tool selection config.
	return os.WriteFile(path, data, 0o644)
}

type mcpServer struct {
	opts     *options
	cmd      *cobra.Command
	selector mcpToolSelector
}

func (server mcpServer) run(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		response, ok := server.handleMessage(ctx, line)
		if !ok {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (err mcpError) Error() string {
	return err.Message
}

func (server mcpServer) handleMessage(ctx context.Context, line []byte) (mcpResponse, bool) {
	var request mcpRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &mcpError{Code: -32700, Message: "parse error"},
		}, true
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return server.errorResponse(request, -32600, "invalid request"), true
	}
	result, err := server.handleRequest(ctx, request)
	if len(request.ID) == 0 {
		return mcpResponse{}, false
	}
	if err != nil {
		var rpcErr mcpError
		if errors.As(err, &rpcErr) {
			return mcpResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcErr}, true
		}
		return server.errorResponse(request, -32603, err.Error()), true
	}
	return mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result}, true
}

func (server mcpServer) handleRequest(ctx context.Context, request mcpRequest) (any, error) {
	switch request.Method {
	case "initialize":
		return server.initializeResult(request.Params), nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return server.toolsListResult(), nil
	case "tools/call":
		var params mcpCallToolParams
		if err := decodeMCPParams(request.Params, &params); err != nil {
			return nil, mcpError{Code: -32602, Message: err.Error()}
		}
		return server.callTool(ctx, params)
	default:
		return nil, mcpError{Code: -32601, Message: "method not found: " + request.Method}
	}
}

func (server mcpServer) errorResponse(request mcpRequest, code int, message string) mcpResponse {
	id := request.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpError{Code: code, Message: message},
	}
}

type mcpInitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

func (server mcpServer) initializeResult(params json.RawMessage) map[string]any {
	protocolVersion := mcpProtocolVersion
	var initParams mcpInitializeParams
	if err := decodeMCPParams(params, &initParams); err == nil && initParams.ProtocolVersion != "" {
		protocolVersion = initParams.ProtocolVersion
	}
	serverName := "supacli"
	if server.selector.profile != "" {
		serverName += "-" + sanitizeMCPName(server.selector.profile)
	}
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": version.Version,
		},
	}
}

func (server mcpServer) toolsListResult() map[string]any {
	specs := server.mcpSpecs()
	tools := make([]mcpTool, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, mcpToolFromSpec(spec))
	}
	return map[string]any{
		"tools": tools,
	}
}

func (server mcpServer) mcpSpecs() []actions.Spec {
	specs := providers.CommandSpecs()
	specs = slices.DeleteFunc(specs, func(spec actions.Spec) bool {
		return !server.selector.matches(spec)
	})
	return specs
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema mcpInputSchema `json:"inputSchema"`
}

type mcpInputSchema struct {
	Type                 string                 `json:"type"`
	Properties           map[string]mcpProperty `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties bool                   `json:"additionalProperties"`
}

type mcpProperty struct {
	Type        any    `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Items       any    `json:"items,omitempty"`
	MinItems    *int   `json:"minItems,omitempty"`
	MaxItems    *int   `json:"maxItems,omitempty"`
	Default     any    `json:"default,omitempty"`
}

func mcpToolFromSpec(spec actions.Spec) mcpTool {
	schema := mcpInputSchema{
		Type:                 "object",
		Properties:           map[string]mcpProperty{},
		AdditionalProperties: false,
	}
	if spec.Args.Min > 0 || spec.Args.Max != 0 {
		property := mcpProperty{
			Type:        "array",
			Description: "Positional arguments for `supacli " + strings.Join(spec.Path, " ") + "`.",
			Items:       map[string]string{"type": "string"},
		}
		if spec.Args.Min > 0 {
			minimum := spec.Args.Min
			property.MinItems = &minimum
			schema.Required = append(schema.Required, "args")
		}
		if spec.Args.Max >= 0 {
			maximum := spec.Args.Max
			property.MaxItems = &maximum
		}
		schema.Properties["args"] = property
	}
	for _, flag := range spec.Flags {
		schema.Properties[flag.Name] = mcpPropertyFromFlag(flag)
	}
	sort.Strings(schema.Required)
	description := spec.Short
	if description == "" {
		description = actionShort(spec)
	}
	return mcpTool{
		Name:        spec.ID,
		Description: description,
		InputSchema: schema,
	}
}

func mcpPropertyFromFlag(flag actions.Flag) mcpProperty {
	property := mcpProperty{Description: flag.Usage}
	switch flag.Type {
	case actions.FlagBool:
		property.Type = "boolean"
		property.Default = flag.DefaultBool
	case actions.FlagInt:
		property.Type = "integer"
		property.Default = flag.DefaultInt
	case actions.FlagString:
		property.Type = "string"
		if flag.Default != "" {
			property.Default = flag.Default
		}
	case actions.FlagStringSlice:
		property.Type = "array"
		property.Items = map[string]string{"type": "string"}
		if len(flag.DefaultString) > 0 {
			property.Default = append([]string(nil), flag.DefaultString...)
		}
	default:
		property.Type = "string"
	}
	return property
}

type mcpCallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type mcpCallToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (server mcpServer) callTool(ctx context.Context, params mcpCallToolParams) (mcpCallToolResult, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "tool name is required"}
	}
	spec, ok := server.lookupMCPTool(name)
	if !ok {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "unknown MCP tool " + name}
	}
	arguments, err := decodeMCPToolArguments(params.Arguments, spec)
	if err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	if err := validateMCPArgs(spec, arguments.args); err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	if err := authorize(server.cmd, server.opts, spec, arguments.args); err != nil {
		return mcpErrorToolResult(err), nil
	}
	provider, ok := providers.Lookup(spec.Provider)
	if !ok {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "unknown provider " + spec.Provider + " for " + spec.ID}
	}
	handler, ok := providers.ActionHandler(provider, spec.ID)
	if !ok {
		return mcpErrorToolResult(fmt.Errorf("%s is not implemented", spec.ID)), nil
	}
	store, err := server.opts.credentials()
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	execCtx := actionExecutionContext(ctx, server.opts, store, provider)
	execCtx.Interactive = false
	result, err := handler(execCtx, actions.Invocation{
		Spec:  spec,
		Args:  arguments.args,
		Flags: arguments.flags,
	})
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	text, err := mcpTextResult(result)
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	return mcpTextToolResult(text), nil
}

func (server mcpServer) lookupMCPTool(name string) (actions.Spec, bool) {
	for _, spec := range server.mcpSpecs() {
		if spec.ID == name {
			return spec, true
		}
	}
	return actions.Spec{}, false
}

type mcpToolArguments struct {
	args  []string
	flags map[string]any
}

func decodeMCPToolArguments(raw json.RawMessage, spec actions.Spec) (mcpToolArguments, error) {
	var decoded map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		decoded = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(raw, &decoded); err != nil {
		return mcpToolArguments{}, fmt.Errorf("tool arguments must be an object")
	}
	args, err := decodeMCPPositionalArgs(decoded["args"])
	if err != nil {
		return mcpToolArguments{}, err
	}
	flags := make(map[string]any, len(spec.Flags))
	for _, flag := range spec.Flags {
		value, err := decodeMCPFlagValue(decoded[flag.Name], flag)
		if err != nil {
			return mcpToolArguments{}, err
		}
		flags[flag.Name] = value
	}
	return mcpToolArguments{args: args, flags: flags}, nil
}

func decodeMCPPositionalArgs(raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	return nil, fmt.Errorf("args must be a string or an array of strings")
}

func decodeMCPFlagValue(raw json.RawMessage, flag actions.Flag) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return mcpDefaultFlagValue(flag), nil
	}
	switch flag.Type {
	case actions.FlagBool:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s must be a boolean", flag.Name)
		}
		return value, nil
	case actions.FlagInt:
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s must be an integer", flag.Name)
		}
		return value, nil
	case actions.FlagString:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s must be a string", flag.Name)
		}
		return value, nil
	case actions.FlagStringSlice:
		var values []string
		if err := json.Unmarshal(raw, &values); err == nil {
			return values, nil
		}
		var single string
		if err := json.Unmarshal(raw, &single); err == nil {
			return []string{single}, nil
		}
		return nil, fmt.Errorf("%s must be a string or an array of strings", flag.Name)
	default:
		return nil, fmt.Errorf("%s has unsupported flag type %q", flag.Name, flag.Type)
	}
}

func mcpDefaultFlagValue(flag actions.Flag) any {
	switch flag.Type {
	case actions.FlagBool:
		return flag.DefaultBool
	case actions.FlagInt:
		return flag.DefaultInt
	case actions.FlagString:
		return flag.Default
	case actions.FlagStringSlice:
		return append([]string(nil), flag.DefaultString...)
	default:
		return nil
	}
}

func validateMCPArgs(spec actions.Spec, args []string) error {
	if len(args) < spec.Args.Min {
		return fmt.Errorf("%s requires at least %d positional argument(s)", spec.ID, spec.Args.Min)
	}
	if spec.Args.Max >= 0 && len(args) > spec.Args.Max {
		return fmt.Errorf("%s accepts at most %d positional argument(s)", spec.ID, spec.Args.Max)
	}
	return nil
}

func mcpErrorToolResult(err error) mcpCallToolResult {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "tool failed"
	}
	return mcpCallToolResult{
		IsError: true,
		Content: []mcpContent{{
			Type: "text",
			Text: message,
		}},
	}
}

func mcpTextToolResult(text string) mcpCallToolResult {
	return mcpCallToolResult{
		Content: []mcpContent{{
			Type: "text",
			Text: text,
		}},
	}
}

func mcpTextResult(result any) (string, error) {
	if result == nil {
		return "", nil
	}
	if markdown, ok := result.(actions.MarkdownRenderable); ok {
		text := markdown.MarkdownSource()
		if truncated, unknown := markdown.MarkdownTruncated(); truncated {
			text = strings.TrimRight(text, "\n") + "\n\ntruncated: " + strconv.Itoa(unknown) + " unknown blocks"
		}
		return text, nil
	}
	if text, ok := result.(actions.TextRenderable); ok {
		return text.Text(), nil
	}
	if opener, ok := result.(actions.BrowserOpenRenderable); ok && opener.BrowserURL() != "" {
		return opener.BrowserURL(), nil
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeMCPParams(raw json.RawMessage, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid params")
	}
	return nil
}

type mcpAgentRuntime struct {
	lookPath func(string) (string, error)
	run      func(context.Context, string, []string) error
}

type mcpAgentResult struct {
	Agent      string
	ServerName string
	Scope      string
	Commands   []mcpExternalCommand
}

type mcpExternalCommand struct {
	Name string
	Args []string
}

func (command mcpExternalCommand) String() string {
	values := append([]string{command.Name}, command.Args...)
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellQuote(value))
	}
	return strings.Join(quoted, " ")
}

func configureMCPAgents(ctx context.Context, runtime mcpAgentRuntime, opts *options, configure mcpConfigureOptions, names []string) ([]mcpAgentResult, error) {
	selected, err := selectMCPAgents(runtime, names, configure.DryRun)
	if err != nil {
		return nil, err
	}
	serverName := mcpConfiguredServerName(configure)
	serveArgs := mcpConfiguredServeArgs(opts, configure)
	results := make([]mcpAgentResult, 0, len(selected))
	for _, agent := range selected {
		result, err := mcpAgentConfigureCommands(agent, serverName, configure, serveArgs)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		if configure.DryRun {
			continue
		}
		for i, command := range result.Commands {
			if i == 0 {
				_ = runtime.run(ctx, command.Name, command.Args)
				continue
			}
			if err := runtime.run(ctx, command.Name, command.Args); err != nil {
				return nil, err
			}
		}
	}
	return results, nil
}

func selectMCPAgents(runtime mcpAgentRuntime, names []string, dryRun bool) ([]string, error) {
	if len(names) == 0 {
		var detected []string
		for _, agent := range supportedMCPAgents() {
			definition, _ := mcpAgentDefinition(agent)
			if _, err := runtime.lookPath(definition.command); err == nil {
				detected = append(detected, agent)
			}
		}
		if len(detected) == 0 {
			return nil, fmt.Errorf("no supported MCP agents detected; pass one of: %s", strings.Join(supportedMCPAgents(), ", "))
		}
		return detected, nil
	}
	var selected []string
	seen := map[string]bool{}
	for _, name := range names {
		agent, ok := canonicalMCPAgent(name)
		if !ok {
			return nil, fmt.Errorf("unknown MCP agent %q; supported agents: %s", name, strings.Join(supportedMCPAgents(), ", "))
		}
		if seen[agent] {
			continue
		}
		if !dryRun {
			definition, _ := mcpAgentDefinition(agent)
			if _, err := runtime.lookPath(definition.command); err != nil {
				return nil, fmt.Errorf("%s CLI not found in PATH", definition.command)
			}
		}
		selected = append(selected, agent)
		seen[agent] = true
	}
	return selected, nil
}

type mcpAgentDefinitionValue struct {
	command string
}

func supportedMCPAgents() []string {
	return []string{"codex", "claude", "gemini"}
}

func canonicalMCPAgent(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "codex":
		return "codex", true
	case "claude", "claude-code":
		return "claude", true
	case "gemini", "gemini-cli":
		return "gemini", true
	default:
		return "", false
	}
}

func mcpAgentDefinition(agent string) (mcpAgentDefinitionValue, bool) {
	switch agent {
	case "codex":
		return mcpAgentDefinitionValue{command: "codex"}, true
	case "claude":
		return mcpAgentDefinitionValue{command: "claude"}, true
	case "gemini":
		return mcpAgentDefinitionValue{command: "gemini"}, true
	default:
		return mcpAgentDefinitionValue{}, false
	}
}

func mcpAgentConfigureCommands(agent, serverName string, configure mcpConfigureOptions, serveArgs []string) (mcpAgentResult, error) {
	switch agent {
	case "codex":
		return mcpAgentResult{
			Agent:      "codex",
			ServerName: serverName,
			Commands: []mcpExternalCommand{
				{Name: "codex", Args: []string{"mcp", "remove", serverName}},
				{Name: "codex", Args: append([]string{"mcp", "add", serverName, "--", configure.Command}, serveArgs...)},
			},
		}, nil
	case "claude":
		scope := strings.TrimSpace(configure.ClaudeScope)
		if scope == "" {
			scope = "local"
		}
		if scope != "local" && scope != "user" && scope != "project" {
			return mcpAgentResult{}, fmt.Errorf("--claude-scope must be local, user, or project")
		}
		return mcpAgentResult{
			Agent:      "claude",
			ServerName: serverName,
			Scope:      "scope=" + scope,
			Commands: []mcpExternalCommand{
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", scope, serverName}},
				{Name: "claude", Args: append([]string{"mcp", "add", "--scope", scope, "--transport", "stdio", serverName, "--", configure.Command}, serveArgs...)},
			},
		}, nil
	case "gemini":
		scope := strings.TrimSpace(configure.GeminiScope)
		if scope == "" {
			scope = "project"
		}
		if scope != "user" && scope != "project" {
			return mcpAgentResult{}, fmt.Errorf("--gemini-scope must be user or project")
		}
		return mcpAgentResult{
			Agent:      "gemini",
			ServerName: serverName,
			Scope:      "scope=" + scope,
			Commands: []mcpExternalCommand{
				{Name: "gemini", Args: []string{"mcp", "remove", "--scope", scope, serverName}},
				{Name: "gemini", Args: append([]string{"mcp", "add", "--scope", scope, "--transport", "stdio", serverName, configure.Command}, serveArgs...)},
			},
		}, nil
	default:
		return mcpAgentResult{}, fmt.Errorf("unknown MCP agent %q", agent)
	}
}

func mcpConfiguredServerName(configure mcpConfigureOptions) string {
	if name := strings.TrimSpace(configure.ServerName); name != "" {
		return name
	}
	if profile := sanitizeMCPName(configure.Profile); profile != "" {
		return "supacli-" + profile
	}
	return "supacli"
}

func sanitizeMCPName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if allowed {
			builder.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func mcpConfiguredServeArgs(opts *options, configure mcpConfigureOptions) []string {
	args := []string{"mcp", "serve"}
	if opts.profile != "" && opts.profile != "default" {
		args = append(args, "--profile", opts.profile)
	}
	if opts.account != "" {
		args = append(args, "--account", opts.account)
	}
	if opts.policy != "" {
		args = append(args, "--policy", opts.policy)
	}
	if opts.readOnly {
		args = append(args, "--read-only")
	}
	args = append(args, mcpToolSelectionArgs(configure.mcpToolSelection)...)
	return args
}

func runMCPAgentCommand(ctx context.Context, name string, args []string) error {
	// #nosec G204 -- agent commands are selected from supported local CLIs.
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, detail)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		if r >= 'a' && r <= 'z' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			return false
		}
		if r >= '0' && r <= '9' {
			return false
		}
		return !strings.ContainsRune("-_./:=,+", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
