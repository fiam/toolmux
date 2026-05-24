package cli

import (
	_ "embed"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers"
)

func mcpRemoteCatalogDefinitionForServer(serverName string, server mcpRemoteServer) (string, mcpRemoteCatalogDefinition, bool) {
	catalog := mcpBuiltinRemoteCatalog()
	if definition, ok := catalog[serverName]; ok && sameMCPRemoteServer(server, definition.Server) {
		return serverName, definition, true
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := catalog[name]
		if sameMCPRemoteServer(server, definition.Server) {
			return name, definition, true
		}
	}
	return "", mcpRemoteCatalogDefinition{}, false
}

func mcpRemoteCatalogCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var enable []string
	var disable []string
	var manage bool
	var syncEnabled bool
	cmd := &cobra.Command{
		Use:     "catalog",
		Aliases: []string{"available"},
		Short:   "List known remote MCP servers",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			modifies := manage || len(enable) > 0 || len(disable) > 0
			if modifies {
				if err := authorize(cmd, opts, mcpRemoteCatalogManageSpec(), args); err != nil {
					return err
				}
				if manage {
					return manageMCPRemoteCatalogInteractive(cmd, opts, scope, syncEnabled)
				}
				return applyMCPRemoteCatalogChanges(cmd, opts, scope, enable, disable, syncEnabled)
			}
			if err := authorize(cmd, opts, mcpRemoteCatalogListSpec(), args); err != nil {
				return err
			}
			entries, err := mcpRemoteCatalogEntries(cmd.Root(), opts)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, entries, func(w io.Writer) {
				renderMCPRemoteCatalogTable(w, cmd, opts, entries)
			})
		},
	}
	cmd.Flags().StringArrayVar(&enable, "enable", nil, "known MCP server name to register; use name=alias to choose the command namespace; repeatable")
	cmd.Flags().StringArrayVar(&disable, "disable", nil, "known MCP server name to remove from config; repeatable")
	cmd.Flags().BoolVar(&manage, "manage", false, "open an interactive selector to enable or disable known MCP servers")
	cmd.Flags().BoolVar(&syncEnabled, "sync", false, "sync newly enabled MCP servers after registering them")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func toolboxCatalogCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var enable []string
	var disable []string
	var manage bool
	var syncEnabled bool
	var filters toolboxCatalogFilters
	cmd := &cobra.Command{
		Use:     "catalog",
		Aliases: []string{"available"},
		Short:   "List available toolboxes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			modifies := manage || len(enable) > 0 || len(disable) > 0
			if modifies {
				if filters.Internal && !filters.MCP {
					return fmt.Errorf("catalog management only applies to MCP catalog entries")
				}
				if err := authorize(cmd, opts, toolboxCatalogManageSpec(), args); err != nil {
					return err
				}
				if manage {
					return manageMCPRemoteCatalogInteractive(cmd, opts, scope, syncEnabled)
				}
				return applyMCPRemoteCatalogChanges(cmd, opts, scope, enable, disable, syncEnabled)
			}
			if err := authorize(cmd, opts, toolboxCatalogListSpec(), args); err != nil {
				return err
			}
			entries, err := toolboxCatalogEntries(cmd.Root(), opts, filters)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, entries, func(w io.Writer) {
				renderToolboxCatalogTable(w, cmd, opts, entries)
			})
		},
	}
	cmd.Flags().BoolVar(&filters.MCP, "mcp", false, "show remote MCP catalog entries")
	cmd.Flags().BoolVar(&filters.Internal, "internal", false, "show internal Toolmux toolboxes")
	cmd.Flags().StringArrayVar(&enable, "enable", nil, "known MCP server name to register; use name=alias to choose the command namespace; repeatable")
	cmd.Flags().StringArrayVar(&disable, "disable", nil, "known MCP server name to remove from config; repeatable")
	cmd.Flags().BoolVar(&manage, "manage", false, "open an interactive selector to enable or disable known MCP servers")
	cmd.Flags().BoolVar(&syncEnabled, "sync", false, "sync newly enabled MCP servers after registering them")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func toolboxCatalogEntries(root *cobra.Command, opts *options, filters toolboxCatalogFilters) ([]toolboxCatalogEntry, error) {
	includeMCP, includeInternal := toolboxCatalogIncludes(filters)
	entries := []toolboxCatalogEntry{}
	if includeMCP {
		mcpEntries, err := mcpRemoteCatalogEntries(root, opts)
		if err != nil {
			return nil, err
		}
		for _, entry := range mcpEntries {
			entries = append(entries, toolboxCatalogEntry{
				Name:                    entry.Name,
				DisplayName:             entry.DisplayName,
				Type:                    "mcp",
				Status:                  entry.Status,
				Registered:              entry.Registered,
				RegisteredNames:         entry.RegisteredNames,
				Scope:                   entry.Scope,
				Scopes:                  entry.Scopes,
				Path:                    entry.Path,
				Tools:                   entry.Tools,
				URL:                     entry.URL,
				Transport:               entry.Transport,
				DefaultArgumentHints:    entry.DefaultArgumentHints,
				MissingDefaultArguments: entry.MissingDefaultArguments,
				Reason:                  entry.Reason,
			})
		}
	}
	if includeInternal {
		entries = append(entries, internalCatalogEntries(opts)...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Type < entries[j].Type
	})
	return entries, nil
}

func toolboxCatalogIncludes(filters toolboxCatalogFilters) (bool, bool) {
	if !filters.MCP && !filters.Internal {
		return true, true
	}
	return filters.MCP, filters.Internal
}

func internalCatalogEntries(opts *options) []toolboxCatalogEntry {
	providerList := nativeStatusProviders()
	entries := make([]toolboxCatalogEntry, 0, len(providerList))
	for _, provider := range providerList {
		tools := len(providers.ActionSpecs(provider))
		entries = append(entries, toolboxCatalogEntry{
			Name:            provider.ID,
			Type:            "internal",
			Status:          "available",
			Registered:      true,
			RegisteredNames: []string{provider.ID},
			Scope:           "built-in",
			Scopes:          []string{"built-in"},
			Tools:           &tools,
			URL:             providerBaseURL(opts, provider),
			Transport:       "native",
		})
	}
	return entries
}

func renderToolboxCatalogTable(w io.Writer, cmd *cobra.Command, opts *options, entries []toolboxCatalogEntry) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		tools := "-"
		if entry.Tools != nil {
			tools = fmt.Sprint(*entry.Tools)
		}
		name := mcpRemoteCatalogDisplayName(entry.Name, entry.DisplayName)
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, name),
			entry.Type,
			toolboxCatalogStatusCell(human, entry.Status),
			output.JoinList(entry.RegisteredNames),
			mcpRemoteScopesLabel(entry.Scopes),
			tools,
			output.Value(entry.URL),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Name", "Type", "Status", "Registered As", "Scope", "Tools", "URL"},
		Rows:    rows,
		Empty:   "no known toolboxes",
	})
}

func toolboxCatalogStatusCell(human output.Options, status string) string {
	switch status {
	case "registered":
		return output.ToneText(human, output.ToneSuccess, "registered")
	case "available":
		return output.ToneText(human, output.ToneInfo, "available")
	case "alias_required":
		return output.ToneText(human, output.ToneWarning, "alias required")
	case "unavailable":
		return output.ToneText(human, output.ToneWarning, "unavailable")
	default:
		return output.Value(status)
	}
}
