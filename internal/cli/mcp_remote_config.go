package cli

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func cleanMCPRemoteName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "", fmt.Errorf("MCP server name is required")
	}
	if !mcpRemoteNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid MCP server name %q: use lowercase letters, digits, hyphens, or underscores, starting with a letter", name)
	}
	return name, nil
}

func defaultMCPRemoteNameFromURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid MCP server URL %q: %w", raw, err)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("MCP server URL must include a host")
	}
	labels := strings.Split(host, ".")
	labels = slices.DeleteFunc(labels, func(label string) bool {
		return label == "" || label == "mcp"
	})
	if len(labels) == 0 {
		return "", fmt.Errorf("could not derive a toolbox name from %q; pass --name", raw)
	}
	name := labels[0]
	if len(labels) >= 2 {
		name = labels[len(labels)-2]
	}
	if len(labels) >= 3 && len(labels[len(labels)-1]) == 2 {
		switch labels[len(labels)-2] {
		case "ac", "co", "com", "edu", "gov", "net", "org":
			name = labels[len(labels)-3]
		}
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	name = strings.Trim(name, "-_")
	if name == "" {
		return "", fmt.Errorf("could not derive a toolbox name from %q; pass --name", raw)
	}
	if _, err := cleanMCPRemoteName(name); err != nil {
		return "", fmt.Errorf("could not derive a valid toolbox name from %q; pass --name", raw)
	}
	return name, nil
}

func validateMCPRemoteURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid MCP server URL %q: %w", raw, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("MCP server URL must use https or http")
	}
	if parsed.Host == "" {
		return fmt.Errorf("MCP server URL must include a host")
	}
	return nil
}

func validateMCPRemoteServer(server mcpRemoteServer) error {
	server = normalizeMCPRemoteServer(server)
	switch server.Transport {
	case mcpRemoteTransportStreamableHTTP:
		if strings.TrimSpace(server.Command) != "" || len(server.Args) > 0 {
			return fmt.Errorf("streamable HTTP MCP servers cannot include command arguments")
		}
		return validateMCPRemoteURL(server.URL)
	case mcpRemoteTransportStdio:
		if strings.TrimSpace(server.URL) != "" {
			return fmt.Errorf("stdio MCP servers cannot include a URL")
		}
		_, _, err := cleanMCPRemoteCommand(append([]string{server.Command}, server.Args...))
		return err
	default:
		return validateMCPRemoteTransport(server.Transport)
	}
}

func isMCPRemoteURLArgument(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

func validateMCPRemoteTransport(transport string) error {
	switch strings.TrimSpace(transport) {
	case "", mcpRemoteTransportStreamableHTTP, mcpRemoteTransportStdio:
		return nil
	default:
		return fmt.Errorf("unsupported MCP transport %q", transport)
	}
}

func ensureMCPRemoteNameAvailable(root *cobra.Command, name string) error {
	if err := ensureToolboxNameAvailable(root, name); err != nil {
		return fmt.Errorf("MCP server name %q conflicts with an existing Toolmux command; choose a different name", name)
	}
	return nil
}

func ensureToolboxNameAvailable(root *cobra.Command, name string) error {
	if root != nil && rootCommandHasName(root, name) {
		return fmt.Errorf("toolbox name %q conflicts with an existing Toolmux command; choose a different name", name)
	}
	return nil
}

func rootCommandHasName(root *cobra.Command, name string) bool {
	for _, command := range root.Commands() {
		if command.Name() == name {
			return true
		}
		if slices.Contains(command.Aliases, name) {
			return true
		}
	}
	return false
}

func rootNativeCommandHasName(root *cobra.Command, name string) bool {
	if root == nil {
		return false
	}
	for _, command := range root.Commands() {
		if command.Annotations[mcpRemoteServerAnnotation] != "" {
			continue
		}
		if command.Name() == name {
			return true
		}
		if slices.Contains(command.Aliases, name) {
			return true
		}
	}
	return false
}

func effectiveMCPRemoteServerEntries(startDir string) ([]mcpRemoteServerEntry, error) {
	globalPath := ""
	if startDir == "" {
		var err error
		globalPath, err = globalToolmuxConfigPath()
		if err != nil {
			return nil, err
		}
	}
	return effectiveMCPRemoteServerEntriesFromPaths(startDir, globalPath)
}

func effectiveMCPRemoteServerEntriesFromPaths(startDir, globalPath string) ([]mcpRemoteServerEntry, error) {
	sources, err := loadMCPProfileSources(startDir, globalPath)
	if err != nil {
		return nil, err
	}
	byName := map[string]mcpRemoteServerEntry{}
	for _, source := range sources {
		names := make([]string, 0, len(source.config.MCP.Servers)+len(source.config.Toolboxes))
		for name := range source.config.MCP.Servers {
			names = append(names, name)
		}
		for name, toolbox := range source.config.Toolboxes {
			if toolbox.Type == toolboxTypeMCP && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			server, ok := configMCPRemoteServer(source.config, name)
			if !ok {
				continue
			}
			existing, exists := byName[name]
			scopes := []string{source.Scope}
			if exists {
				scopes = appendScope(existing.Scopes, source.Scope)
			}
			byName[name] = mcpRemoteServerEntry{
				Name:   name,
				Scope:  source.Scope,
				Scopes: scopes,
				Path:   source.Path,
				Server: server,
			}
		}
	}
	entries := make([]mcpRemoteServerEntry, 0, len(byName))
	for _, entry := range byName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func lookupMCPRemoteServer(name, startDir string) (mcpRemoteServerEntry, bool, error) {
	entries, err := effectiveMCPRemoteServerEntries(startDir)
	if err != nil {
		return mcpRemoteServerEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return mcpRemoteServerEntry{}, false, nil
}

func normalizeMCPRemoteServer(server mcpRemoteServer) mcpRemoteServer {
	server.URL = strings.TrimSpace(server.URL)
	server.Command = strings.TrimSpace(server.Command)
	server.Transport = strings.TrimSpace(server.Transport)
	if server.Transport == "" {
		server.Transport = mcpRemoteTransportStreamableHTTP
	}
	return server
}

func writeMCPRemoteAuthRequired(entry mcpRemoteServerEntry, required bool) error {
	config, err := readToolmuxConfigFile(entry.Path)
	if err != nil {
		return err
	}
	server, exists := configMCPRemoteServer(config, entry.Name)
	if !exists {
		return fmt.Errorf("MCP server %q is not registered in %s", entry.Name, entry.Path)
	}
	server.AuthRequired = new(required)
	setConfigMCPRemoteServer(&config, entry.Name, server)
	return writeToolmuxConfigFile(entry.Path, config)
}

func mcpRemoteCacheDir(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "toolmux", "mcp-remotes"), nil
}

func mcpRemoteCachePath(configured, name string) (string, error) {
	dir, err := mcpRemoteCacheDir(configured)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

func readMCPRemoteCacheIfExists(configuredDir, name string) (mcpRemoteCache, bool, error) {
	path, err := mcpRemoteCachePath(configuredDir, name)
	if err != nil {
		return mcpRemoteCache{}, false, err
	}
	// #nosec G304 -- MCP cache paths are derived from local Toolmux config.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mcpRemoteCache{}, false, nil
		}
		return mcpRemoteCache{}, false, err
	}
	var cache mcpRemoteCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return mcpRemoteCache{}, false, err
	}
	return cache, true, nil
}

func writeMCPRemoteCache(configuredDir, name string, cache mcpRemoteCache) error {
	path, err := mcpRemoteCachePath(configuredDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	cache.Version = mcpRemoteCacheVersion
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	// #nosec G306 -- MCP cache contains non-secret remote tool metadata.
	return os.WriteFile(path, data, 0o644)
}

func renameMCPRemoteCache(configuredDir, oldName, newName string) error {
	oldPath, err := mcpRemoteCachePath(configuredDir, oldName)
	if err != nil {
		return err
	}
	newPath, err := mcpRemoteCachePath(configuredDir, newName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o750); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeMCPRemoteCache(configuredDir, name string) error {
	path, err := mcpRemoteCachePath(configuredDir, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
