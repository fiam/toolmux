package cli

import (
	_ "embed"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func mcpRemoteCatalogDisplayName(name, displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || displayName == name {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, displayName)
}

func mcpRemoteCatalogEntries(root *cobra.Command, opts *options) ([]mcpRemoteCatalogEntry, error) {
	registered, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil, err
	}
	builtins := mcpBuiltinRemoteCatalog()
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]mcpRemoteCatalogEntry, 0, len(names))
	for _, name := range names {
		definition := builtins[name]
		server := normalizeMCPRemoteServer(definition.Server)
		entry := mcpRemoteCatalogEntry{
			Name:                 name,
			DisplayName:          definition.DisplayName,
			Status:               "available",
			Registered:           false,
			URL:                  server.URL,
			Transport:            server.Transport,
			DefaultArgumentHints: definition.DefaultArgumentHints,
		}
		matches := matchingMCPRemoteCatalogRegistrations(name, server, registered)
		if len(matches) > 0 {
			registeredEntry := matches[0]
			entry.Status = "registered"
			entry.Registered = true
			entry.RegisteredNames = make([]string, 0, len(matches))
			scopes := map[string]bool{}
			for _, match := range matches {
				entry.RegisteredNames = append(entry.RegisteredNames, match.Name)
				for _, scope := range match.Scopes {
					scopes[scope] = true
				}
			}
			sort.Strings(entry.RegisteredNames)
			entry.Scope = registeredEntry.Scope
			for scope := range scopes {
				entry.Scopes = append(entry.Scopes, scope)
			}
			sort.Strings(entry.Scopes)
			entry.Path = registeredEntry.Path
			entry.URL = registeredEntry.Server.URL
			entry.Transport = registeredEntry.Server.Transport
			entry.MissingDefaultArguments = mcpRemoteMissingDefaultArgumentNames(registeredEntry, definition.DefaultArgumentHints)
			if cache, ok, _ := readMCPRemoteCacheIfExists(opts.mcpCacheDir, registeredEntry.Name); ok {
				tools := len(cache.Tools)
				entry.Tools = &tools
			}
		} else if rootNativeCommandHasName(root, name) {
			entry.Status = "alias_required"
			entry.Reason = fmt.Sprintf("native command conflict; use --enable %s=<name>", name)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func matchingMCPRemoteCatalogRegistrations(catalogName string, server mcpRemoteServer, registered []mcpRemoteServerEntry) []mcpRemoteServerEntry {
	var matches []mcpRemoteServerEntry
	for _, entry := range registered {
		if entry.Name == catalogName || sameMCPRemoteServer(entry.Server, server) {
			matches = append(matches, entry)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})
	return matches
}

func sameMCPRemoteServer(left, right mcpRemoteServer) bool {
	left = normalizeMCPRemoteServer(left)
	right = normalizeMCPRemoteServer(right)
	if left.Transport != right.Transport {
		return false
	}
	if left.Transport == mcpRemoteTransportStdio {
		return left.Command == right.Command && slices.Equal(left.Args, right.Args)
	}
	return strings.TrimRight(left.URL, "/") == strings.TrimRight(right.URL, "/")
}

func mcpRemoteServerSource(server mcpRemoteServer) string {
	server = normalizeMCPRemoteServer(server)
	if server.Transport == mcpRemoteTransportStdio {
		return mcpRemoteCommandDisplay(server)
	}
	return server.URL
}

func mcpRemoteKind(server mcpRemoteServer) string {
	if normalizeMCPRemoteServer(server).Transport == mcpRemoteTransportStdio {
		return "stdio-mcp"
	}
	return "remote-mcp"
}

func mcpRemoteCommandDisplay(server mcpRemoteServer) string {
	parts := make([]string, 0, 1+len(server.Args))
	if strings.TrimSpace(server.Command) != "" {
		parts = append(parts, strings.TrimSpace(server.Command))
	}
	parts = append(parts, server.Args...)
	return strings.Join(parts, " ")
}

func findMCPRemoteServerEntry(entries []mcpRemoteServerEntry, name string) (mcpRemoteServerEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return mcpRemoteServerEntry{}, false
}
