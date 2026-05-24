package cli

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers"
)

type nativeToolboxEntry struct {
	Name     string             `json:"name" yaml:"name"`
	Scope    string             `json:"scope" yaml:"scope"`
	Scopes   []string           `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path     string             `json:"path" yaml:"path"`
	Provider providers.Provider `json:"-" yaml:"-"`
}

func (config toolboxConfig) mcpRemoteServer() mcpRemoteServer {
	server := mcpRemoteServer{
		URL:              config.URL,
		Command:          config.Command,
		Args:             append([]string(nil), config.Args...),
		Transport:        config.Transport,
		AuthRequired:     config.AuthRequired,
		DefaultArguments: maps.Clone(config.DefaultArguments),
	}
	if config.AuthRequired != nil {
		authRequired := *config.AuthRequired
		server.AuthRequired = &authRequired
	}
	return server
}

func toolboxConfigFromMCPRemoteServer(server mcpRemoteServer, catalog string) toolboxConfig {
	server = normalizeMCPRemoteServer(server)
	config := toolboxConfig{
		Type:             toolboxTypeMCP,
		Catalog:          strings.TrimSpace(catalog),
		URL:              server.URL,
		Command:          server.Command,
		Args:             append([]string(nil), server.Args...),
		Transport:        server.Transport,
		AuthRequired:     server.AuthRequired,
		DefaultArguments: maps.Clone(server.DefaultArguments),
	}
	if server.AuthRequired != nil {
		authRequired := *server.AuthRequired
		config.AuthRequired = &authRequired
	}
	return config
}

func normalizeToolboxConfig(config *toolmuxConfigFile) {
	if config.Toolboxes == nil {
		config.Toolboxes = map[string]toolboxConfig{}
	}
	for name, server := range config.MCP.Servers {
		if _, exists := config.Toolboxes[name]; exists {
			continue
		}
		config.Toolboxes[name] = toolboxConfigFromMCPRemoteServer(server, "")
	}
	config.MCP.Servers = map[string]mcpRemoteServer{}
}

func configMCPRemoteServer(config toolmuxConfigFile, name string) (mcpRemoteServer, bool) {
	if toolbox, ok := config.Toolboxes[name]; ok && toolbox.Type == toolboxTypeMCP {
		return normalizeMCPRemoteServer(toolbox.mcpRemoteServer()), true
	}
	server, ok := config.MCP.Servers[name]
	if !ok {
		return mcpRemoteServer{}, false
	}
	return normalizeMCPRemoteServer(server), true
}

func setConfigMCPRemoteServer(config *toolmuxConfigFile, name string, server mcpRemoteServer) {
	if config.Toolboxes == nil {
		config.Toolboxes = map[string]toolboxConfig{}
	}
	catalog := ""
	if existing, ok := config.Toolboxes[name]; ok && existing.Type == toolboxTypeMCP {
		catalog = existing.Catalog
	}
	config.Toolboxes[name] = toolboxConfigFromMCPRemoteServer(server, catalog)
	delete(config.MCP.Servers, name)
}

func deleteConfigMCPRemoteServer(config *toolmuxConfigFile, name string) {
	delete(config.Toolboxes, name)
	delete(config.MCP.Servers, name)
}

func renameConfigMCPRemoteServer(config *toolmuxConfigFile, oldName, newName string, server mcpRemoteServer) {
	catalog := ""
	if existing, ok := config.Toolboxes[oldName]; ok && existing.Type == toolboxTypeMCP {
		catalog = existing.Catalog
	}
	deleteConfigMCPRemoteServer(config, oldName)
	if config.Toolboxes == nil {
		config.Toolboxes = map[string]toolboxConfig{}
	}
	config.Toolboxes[newName] = toolboxConfigFromMCPRemoteServer(server, catalog)
}

func setConfigNativeToolbox(config *toolmuxConfigFile, name string, provider providers.Provider) {
	if config.Toolboxes == nil {
		config.Toolboxes = map[string]toolboxConfig{}
	}
	config.Toolboxes[name] = toolboxConfig{
		Type:     toolboxTypeInternal,
		Provider: provider.ID,
	}
}

func effectiveNativeToolboxEntries(startDir string) ([]nativeToolboxEntry, error) {
	globalPath := ""
	if startDir == "" {
		var err error
		globalPath, err = globalToolmuxConfigPath()
		if err != nil {
			return nil, err
		}
	}
	return effectiveNativeToolboxEntriesFromPaths(startDir, globalPath)
}

func effectiveNativeToolboxEntriesFromPaths(startDir, globalPath string) ([]nativeToolboxEntry, error) {
	sources, err := loadMCPProfileSources(startDir, globalPath)
	if err != nil {
		return nil, err
	}
	byName := map[string]nativeToolboxEntry{}
	for _, source := range sources {
		names := make([]string, 0, len(source.config.Toolboxes))
		for name := range source.config.Toolboxes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			toolbox := source.config.Toolboxes[name]
			if toolbox.Type != toolboxTypeInternal {
				continue
			}
			cleanName, err := cleanMCPRemoteName(name)
			if err != nil {
				return nil, fmt.Errorf("invalid internal toolbox name in %s: %w", source.Path, err)
			}
			providerName := strings.TrimSpace(toolbox.Provider)
			if providerName == "" {
				providerName = cleanName
			}
			provider, ok := providers.Lookup(providerName)
			if !ok {
				return nil, fmt.Errorf("internal toolbox %q in %s references unknown provider %q", cleanName, source.Path, providerName)
			}
			existing, exists := byName[cleanName]
			scopes := []string{source.Scope}
			if exists {
				scopes = appendScope(existing.Scopes, source.Scope)
			}
			byName[cleanName] = nativeToolboxEntry{
				Name:     cleanName,
				Scope:    source.Scope,
				Scopes:   scopes,
				Path:     source.Path,
				Provider: provider,
			}
		}
	}
	entries := make([]nativeToolboxEntry, 0, len(byName))
	for _, entry := range byName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func lookupNativeToolboxEntry(name, startDir string) (nativeToolboxEntry, bool, error) {
	entries, err := effectiveNativeToolboxEntries(startDir)
	if err != nil {
		return nativeToolboxEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return nativeToolboxEntry{}, false, nil
}

func nativeToolboxTree(entry nativeToolboxEntry) actions.Spec {
	tree := entry.Provider.Tree
	tree.Segment = entry.Name
	return tree
}

func nativeToolboxActionSpecs(entry nativeToolboxEntry) []actions.Spec {
	return actions.LeafSpecs(actions.ProviderName(entry.Name), nativeToolboxTree(entry))
}

func nativeToolboxHandlerID(entry nativeToolboxEntry, spec actions.Spec) string {
	local := strings.TrimPrefix(spec.ID, entry.Name+".")
	return entry.Provider.ID + "." + local
}
