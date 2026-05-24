package cli

import (
	_ "embed"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func mcpRemoteListCommand(opts *options) *cobra.Command {
	var recursive bool
	var fullDescriptions bool
	cmd := &cobra.Command{
		Use:     "ls [name]",
		Aliases: []string{"list"},
		Short:   "List registered remote MCP servers",
		Args:    cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := effectiveMCPRemoteServerEntries("")
			if err != nil {
				return err
			}
			if len(args) == 1 {
				name, err := cleanMCPRemoteName(args[0])
				if err != nil {
					return err
				}
				entry, ok := findMCPRemoteServerEntry(entries, name)
				if !ok {
					return fmt.Errorf("MCP server %q is not registered", name)
				}
				cache, ok := refreshMCPRemoteCacheIfStale(commandContext(cmd), cmd, opts, entry, nil)
				if !ok {
					return fmt.Errorf("MCP server %q has no cached tools; run `toolmux mcp sync %s`", name, name)
				}
				if recursive {
					items := mcpRemoteTreeItems([]mcpRemoteServerEntry{entry}, map[string]mcpRemoteCache{name: cache})
					return writeValue(cmd, opts, items, func(w io.Writer) {
						renderMCPRemoteTree(w, cmd, opts, items, fullDescriptions)
					})
				}
				result := mcpRemoteToolList{
					Server: entry.Name,
					Tools:  sortedMCPRemoteTools(cache.Tools),
				}
				return writeValue(cmd, opts, result, func(w io.Writer) {
					renderMCPRemoteToolTable(w, cmd, opts, entry.Name, result.Tools, fullDescriptions)
				})
			}
			if recursive {
				caches := make(map[string]mcpRemoteCache, len(entries))
				for _, entry := range entries {
					if cache, ok := refreshMCPRemoteCacheIfStale(commandContext(cmd), cmd, opts, entry, nil); ok {
						caches[entry.Name] = cache
					}
				}
				items := mcpRemoteTreeItems(entries, caches)
				return writeValue(cmd, opts, items, func(w io.Writer) {
					renderMCPRemoteTree(w, cmd, opts, items, fullDescriptions)
				})
			}
			items := mcpRemoteListItems(opts, entries)
			return writeValue(cmd, opts, items, func(w io.Writer) {
				renderMCPRemoteListTable(w, cmd, opts, items)
			})
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "R", false, "recursively list tools under registered MCP servers")
	cmd.Flags().BoolVar(&fullDescriptions, "full-descriptions", false, "show full remote MCP tool descriptions")
	return cmd
}

func mcpRemoteShowCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a registered remote MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			entry, ok, err := lookupMCPRemoteServer(name, "")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			type result struct {
				Name   string          `json:"name" yaml:"name"`
				Scope  string          `json:"scope" yaml:"scope"`
				Scopes []string        `json:"scopes,omitempty" yaml:"scopes,omitempty"`
				Path   string          `json:"path" yaml:"path"`
				Server mcpRemoteServer `json:"server" yaml:"server"`
				Cache  *mcpRemoteCache `json:"cache,omitempty" yaml:"cache,omitempty"`
			}
			var cachePtr *mcpRemoteCache
			if cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, name); err != nil {
				return err
			} else if ok {
				cachePtr = &cache
			}
			return writeValue(cmd, opts, result{
				Name:   entry.Name,
				Scope:  entry.Scope,
				Scopes: entry.Scopes,
				Path:   entry.Path,
				Server: entry.Server,
				Cache:  cachePtr,
			}, nil)
		},
	}
}
