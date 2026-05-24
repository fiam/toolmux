package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type toolmuxConfigFile struct {
	Version   int                      `json:"version" yaml:"version"`
	Toolboxes map[string]toolboxConfig `json:"toolboxes,omitempty" yaml:"toolboxes,omitempty"`
	MCP       mcpConfig                `json:"mcp,omitzero" yaml:"mcp,omitempty"`
	Workflows workflowConfig           `json:"workflows,omitzero" yaml:"workflows,omitempty"`
}

type mcpConfig struct {
	DefaultProfile string                      `json:"default_profile,omitempty" yaml:"default_profile,omitempty"`
	Profiles       map[string]mcpProfileConfig `json:"profiles,omitempty" yaml:"profiles,omitempty"`
	Servers        map[string]mcpRemoteServer  `json:"servers,omitempty" yaml:"servers,omitempty"`
}

type toolboxConfig struct {
	Type             string         `json:"type" yaml:"type"`
	Provider         string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Catalog          string         `json:"catalog,omitempty" yaml:"catalog,omitempty"`
	URL              string         `json:"url,omitempty" yaml:"url,omitempty"`
	Command          string         `json:"command,omitempty" yaml:"command,omitempty"`
	Args             []string       `json:"args,omitempty" yaml:"args,omitempty"`
	Transport        string         `json:"transport,omitempty" yaml:"transport,omitempty"`
	AuthRequired     *bool          `json:"auth_required,omitempty" yaml:"auth_required,omitempty"`
	DefaultArguments map[string]any `json:"default_arguments,omitempty" yaml:"default_arguments,omitempty"`
}

const (
	toolboxTypeInternal = "internal"
	toolboxTypeMCP      = "mcp"
)

type mcpProfileConfig struct {
	Tools            []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	ToolRegex        []string `json:"tool_regex,omitempty" yaml:"tool_regex,omitempty"`
	ExcludeTools     []string `json:"exclude_tools,omitempty" yaml:"exclude_tools,omitempty"`
	ExcludeToolRegex []string `json:"exclude_tool_regex,omitempty" yaml:"exclude_tool_regex,omitempty"`
}

type workflowConfig struct {
	DefaultAgent string                         `json:"default_agent,omitempty" yaml:"default_agent,omitempty"`
	Agents       map[string]workflowAgentConfig `json:"agents,omitempty" yaml:"agents,omitempty"`
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
	globalPath, err := globalToolmuxConfigPath()
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
			return mcpToolSelection{}, fmt.Errorf("MCP profile %q not found; create it with `toolmux mcp profile set %s`", name, name)
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
		if source.config.MCP.DefaultProfile != "" {
			config.DefaultProfile = source.config.MCP.DefaultProfile
		}
	}
	return config, nil
}

func (config mcpEffectiveProfileConfig) lookup(name string) (mcpProfileEntry, bool) {
	var found mcpProfileEntry
	ok := false
	for _, source := range config.Sources {
		profile, exists := source.config.MCP.Profiles[name]
		if !exists {
			continue
		}
		scopes := []string{source.Scope}
		if ok {
			scopes = appendScope(found.Scopes, source.Scope)
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
	return toolmuxConfigWritePath(scope, "")
}

func toolmuxConfigWritePath(scope mcpProfileScopeOptions, startDir string) (string, string, error) {
	if scope.Global && scope.Project {
		return "", "", fmt.Errorf("use only one of --global or --project")
	}
	if scope.Project {
		globalPath, err := globalToolmuxConfigPath()
		if err != nil {
			return "", "", err
		}
		if path, ok, err := discoverToolmuxConfigFile(startDir); err != nil {
			return "", "", err
		} else if ok && !sameFilesystemPath(globalPath, path) {
			return path, "project", nil
		}
		if startDir != "" {
			return filepath.Join(startDir, toolmuxConfigRelPath), "project", nil
		}
		return toolmuxConfigRelPath, "project", nil
	}
	path, err := globalToolmuxConfigPath()
	return path, "global", err
}

func lookupMCPProfile(name, startDir string) (mcpProfileEntry, bool, error) {
	globalPath, err := globalToolmuxConfigPath()
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
	globalPath, err := globalToolmuxConfigPath()
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
		names := make([]string, 0, len(source.config.MCP.Profiles))
		for name := range source.config.MCP.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			profile := source.config.MCP.Profiles[name]
			existing, exists := byName[name]
			scopes := []string{source.Scope}
			if exists {
				scopes = appendScope(existing.Scopes, source.Scope)
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
	config toolmuxConfigFile
}

func loadMCPProfileSources(startDir, globalPath string) ([]mcpProfileSource, error) {
	var sources []mcpProfileSource
	if globalPath != "" {
		if config, ok, err := readToolmuxConfigFileIfExists(globalPath); err != nil {
			return nil, err
		} else if ok {
			sources = append(sources, mcpProfileSource{Scope: "global", Path: globalPath, config: config})
		}
	}
	if localPath, ok, err := discoverToolmuxConfigFile(startDir); err != nil {
		return nil, err
	} else if ok {
		if hasMCPProfileSourcePath(sources, localPath) {
			return sources, nil
		}
		config, err := readToolmuxConfigFile(localPath)
		if err != nil {
			return nil, err
		}
		sources = append(sources, mcpProfileSource{Scope: "project", Path: localPath, config: config})
	}
	return sources, nil
}

func globalToolmuxConfigPath() (string, error) {
	dir, err := toolmuxHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func toolmuxHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".toolmux"), nil
}

func hasMCPProfileSourcePath(sources []mcpProfileSource, path string) bool {
	for _, source := range sources {
		if sameFilesystemPath(source.Path, path) {
			return true
		}
	}
	return false
}

func sameFilesystemPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func appendScope(scopes []string, scope string) []string {
	if slices.Contains(scopes, scope) {
		return scopes
	}
	return append(scopes, scope)
}

func discoverToolmuxConfigFile(startDir string) (string, bool, error) {
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
		candidate := filepath.Join(dir, toolmuxConfigRelPath)
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

func readToolmuxConfigFile(path string) (toolmuxConfigFile, error) {
	// #nosec G304 -- Toolmux config paths are explicit local configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return toolmuxConfigFile{}, err
	}
	var config toolmuxConfigFile
	if err := yaml.Unmarshal(data, &config); err != nil {
		return toolmuxConfigFile{}, err
	}
	if config.Version == 0 {
		config.Version = 1
	}
	if config.MCP.Profiles == nil {
		config.MCP.Profiles = map[string]mcpProfileConfig{}
	}
	if config.MCP.Servers == nil {
		config.MCP.Servers = map[string]mcpRemoteServer{}
	}
	if config.Toolboxes == nil {
		config.Toolboxes = map[string]toolboxConfig{}
	}
	normalizeToolboxConfig(&config)
	return config, nil
}

func readToolmuxConfigFileIfExists(path string) (toolmuxConfigFile, bool, error) {
	config, err := readToolmuxConfigFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return toolmuxConfigFile{}, false, nil
		}
		return toolmuxConfigFile{}, false, err
	}
	return config, true, nil
}

func writeToolmuxConfigFile(path string, config toolmuxConfigFile) error {
	if config.Version == 0 {
		config.Version = 1
	}
	if config.MCP.Profiles == nil {
		config.MCP.Profiles = map[string]mcpProfileConfig{}
	}
	if config.MCP.Servers == nil {
		config.MCP.Servers = map[string]mcpRemoteServer{}
	}
	normalizeToolboxConfig(&config)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	// #nosec G306 -- Toolmux config is non-secret local tool configuration.
	return os.WriteFile(path, data, 0o644)
}
