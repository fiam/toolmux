package cli

import (
	"bytes"
	_ "embed"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

func mcpBuiltinRemoteServers() map[string]mcpRemoteServer {
	catalog := mcpBuiltinRemoteCatalog()
	servers := make(map[string]mcpRemoteServer, len(catalog))
	for name, definition := range catalog {
		servers[name] = definition.Server
	}
	return servers
}

func mcpBuiltinRemoteCatalog() map[string]mcpRemoteCatalogDefinition {
	mcpBuiltinRemoteCatalogOnce.Do(func() {
		mcpBuiltinRemoteCatalogValue, mcpBuiltinRemoteCatalogErr = parseMCPBuiltinRemoteCatalog(mcpBuiltinRemoteCatalogYAML)
	})
	if mcpBuiltinRemoteCatalogErr != nil {
		panic(mcpBuiltinRemoteCatalogErr)
	}
	return cloneMCPRemoteCatalog(mcpBuiltinRemoteCatalogValue)
}

func parseMCPBuiltinRemoteCatalog(data []byte) (map[string]mcpRemoteCatalogDefinition, error) {
	var file struct {
		Servers map[string]mcpRemoteCatalogDefinition `yaml:"servers"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse built-in MCP remote catalog: %w", err)
	}
	if len(file.Servers) == 0 {
		return nil, fmt.Errorf("built-in MCP remote catalog has no servers")
	}
	catalog := make(map[string]mcpRemoteCatalogDefinition, len(file.Servers))
	for name, definition := range file.Servers {
		clean, err := cleanMCPRemoteName(name)
		if err != nil {
			return nil, fmt.Errorf("invalid built-in MCP remote catalog name %q: %w", name, err)
		}
		if clean != name {
			return nil, fmt.Errorf("invalid built-in MCP remote catalog name %q: must be normalized as %q", name, clean)
		}
		definition.Server = normalizeMCPRemoteServer(definition.Server)
		definition.DisplayName = strings.TrimSpace(definition.DisplayName)
		if definition.DisplayName == "" {
			return nil, fmt.Errorf("built-in MCP remote catalog server %q is missing display_name", name)
		}
		if err := validateMCPRemoteServer(definition.Server); err != nil {
			return nil, fmt.Errorf("invalid built-in MCP remote catalog server %q: %w", name, err)
		}
		catalog[name] = definition
	}
	return catalog, nil
}

func cloneMCPRemoteCatalog(src map[string]mcpRemoteCatalogDefinition) map[string]mcpRemoteCatalogDefinition {
	dst := make(map[string]mcpRemoteCatalogDefinition, len(src))
	for name, definition := range src {
		definition.Server.Args = slices.Clone(definition.Server.Args)
		definition.Server.DefaultArguments = maps.Clone(definition.Server.DefaultArguments)
		definition.DefaultArgumentHints = slices.Clone(definition.DefaultArgumentHints)
		dst[name] = definition
	}
	return dst
}
