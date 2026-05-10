package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/version"
)

const (
	mcpRemoteTransportStreamableHTTP = "streamable-http"
	mcpRemoteCacheVersion            = 1
	mcpRemoteCacheMaxAge             = 24 * time.Hour
	mcpRemoteTraceBodyLimit          = 8 << 20
	mcpRemoteServerAnnotation        = "toolmux.remote_mcp.server"
	mcpRemoteCredentialProvider      = "mcp_remote"
)

var mcpRemoteNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type mcpRemoteServer struct {
	URL       string `json:"url" yaml:"url"`
	Transport string `json:"transport,omitempty" yaml:"transport,omitempty"`
}

type mcpRemoteServerEntry struct {
	Name   string          `json:"name" yaml:"name"`
	Scope  string          `json:"scope" yaml:"scope"`
	Scopes []string        `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path   string          `json:"path" yaml:"path"`
	Server mcpRemoteServer `json:"server" yaml:"server"`
}

type mcpRemoteCache struct {
	Version         int                        `json:"version"`
	Name            string                     `json:"name"`
	URL             string                     `json:"url"`
	Transport       string                     `json:"transport"`
	ProtocolVersion string                     `json:"protocol_version,omitempty"`
	ServerInfo      map[string]any             `json:"server_info,omitempty"`
	Tools           []mcpRemoteTool            `json:"tools"`
	SyncedAt        time.Time                  `json:"synced_at"`
	Fingerprint     string                     `json:"fingerprint,omitempty"`
	Raw             map[string]json.RawMessage `json:"raw,omitempty"`
}

type mcpRemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type mcpRemoteToolRef struct {
	Entry mcpRemoteServerEntry
	Cache mcpRemoteCache
	Tool  mcpRemoteTool
}

type mcpRemoteListItem struct {
	Name      string   `json:"name" yaml:"name"`
	Status    string   `json:"status" yaml:"status"`
	Scope     string   `json:"scope" yaml:"scope"`
	Scopes    []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path      string   `json:"path" yaml:"path"`
	URL       string   `json:"url" yaml:"url"`
	Transport string   `json:"transport" yaml:"transport"`
	Tools     *int     `json:"tools,omitempty" yaml:"tools,omitempty"`
}

type mcpRemoteToolList struct {
	Server string          `json:"server" yaml:"server"`
	Tools  []mcpRemoteTool `json:"tools" yaml:"tools"`
}

type mcpRemoteTreeItem struct {
	Name      string          `json:"name" yaml:"name"`
	Status    string          `json:"status" yaml:"status"`
	Scope     string          `json:"scope" yaml:"scope"`
	Scopes    []string        `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path      string          `json:"path" yaml:"path"`
	URL       string          `json:"url" yaml:"url"`
	Transport string          `json:"transport" yaml:"transport"`
	Tools     []mcpRemoteTool `json:"tools,omitempty" yaml:"tools,omitempty"`
}

type mcpRemoteNameConflict struct {
	Name string
}

type mcpRemoteHTTPTrace struct {
	w io.Writer
}

type mcpRemoteHTTPStatusError struct {
	Method     string
	StatusCode int
	Body       string
}

type mcpRemoteCatalogEntry struct {
	Name            string   `json:"name" yaml:"name"`
	Status          string   `json:"status" yaml:"status"`
	Registered      bool     `json:"registered" yaml:"registered"`
	RegisteredNames []string `json:"registered_names,omitempty" yaml:"registered_names,omitempty"`
	Scope           string   `json:"scope,omitempty" yaml:"scope,omitempty"`
	Scopes          []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path            string   `json:"path,omitempty" yaml:"path,omitempty"`
	Tools           *int     `json:"tools,omitempty" yaml:"tools,omitempty"`
	URL             string   `json:"url" yaml:"url"`
	Transport       string   `json:"transport" yaml:"transport"`
	Reason          string   `json:"reason,omitempty" yaml:"reason,omitempty"`
}

type mcpRemoteCatalogEnable struct {
	CatalogName    string
	RegisteredName string
}

func mcpRemoteAddCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var transport string
	var noSync bool
	cmd := &cobra.Command{
		Use:     "add <name> [url]",
		Aliases: []string{"register"},
		Short:   "Register and sync a remote MCP server",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && isMCPRemoteURLArgument(args[0]) {
				return fmt.Errorf("MCP server name is required when adding a URL; use `toolmux mcp add <name> %s`", strings.TrimSpace(args[0]))
			}
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			serverURL := ""
			if len(args) == 2 {
				serverURL = strings.TrimSpace(args[1])
			} else if builtin, ok := mcpBuiltinRemoteServers()[name]; ok {
				serverURL = builtin.URL
				if transport == "" {
					transport = builtin.Transport
				}
			} else {
				return fmt.Errorf("URL is required for unknown MCP server %q", name)
			}
			if err := validateMCPRemoteURL(serverURL); err != nil {
				return err
			}
			if transport == "" {
				transport = mcpRemoteTransportStreamableHTTP
			}
			if err := validateMCPRemoteTransport(transport); err != nil {
				return err
			}
			configPath, scopeName, err := mcpProfileWritePath(scope)
			if err != nil {
				return err
			}
			config, err := readToolmuxConfigFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if config.MCP.Servers == nil {
				config.MCP.Servers = map[string]mcpRemoteServer{}
			}
			if _, exists := config.MCP.Servers[name]; exists {
				return fmt.Errorf("MCP server %q is already registered in %s", name, configPath)
			}
			if _, exists, err := lookupMCPRemoteServer(name, ""); err != nil {
				return err
			} else if exists {
				return fmt.Errorf("MCP server %q is already registered; use `toolmux mcp rename %s <new-name>` first", name, name)
			}
			if err := ensureMCPRemoteNameAvailable(cmd.Root(), name); err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpRemoteAddSpec(), args); err != nil {
				return err
			}
			server := normalizeMCPRemoteServer(mcpRemoteServer{
				URL:       serverURL,
				Transport: transport,
			})
			register := func() error {
				config.Version = 1
				config.MCP.Servers[name] = server
				if err := writeToolmuxConfigFile(configPath, config); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "registered %s MCP server %s in %s\n", scopeName, name, configPath)
				return nil
			}
			if noSync {
				return register()
			}
			entry := mcpRemoteServerEntry{
				Name:   name,
				Scope:  scopeName,
				Scopes: []string{scopeName},
				Path:   configPath,
				Server: server,
			}
			cache, err := syncMCPRemoteCacheAfterAdd(cmd, opts, entry, args)
			if err != nil {
				return fmt.Errorf("initial sync failed for MCP server %s: %w", name, err)
			}
			if err := register(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced MCP server %s: %d tools\n", name, len(cache.Tools))
			return nil
		},
	}
	cmd.Flags().StringVar(&transport, "transport", mcpRemoteTransportStreamableHTTP, "remote MCP transport: streamable-http")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "register without immediately syncing tools")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func mcpRemoteSyncCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "sync <name>",
		Short: "Introspect and cache a remote MCP server",
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
			cache, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, args)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced MCP server %s: %d tools\n", name, len(cache.Tools))
			return nil
		},
	}
}

func mcpRemoteRenameCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a registered remote MCP server",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			newName, err := cleanMCPRemoteName(args[1])
			if err != nil {
				return err
			}
			if oldName == newName {
				return fmt.Errorf("old and new MCP server names are both %q", oldName)
			}
			entry, ok, err := lookupMCPRemoteServer(oldName, "")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", oldName)
			}
			configPath := entry.Path
			if scope.Global || scope.Project {
				var scopeName string
				configPath, scopeName, err = mcpProfileWritePath(scope)
				if err != nil {
					return err
				}
				_ = scopeName
			}
			config, err := readToolmuxConfigFile(configPath)
			if err != nil {
				return err
			}
			server, exists := config.MCP.Servers[oldName]
			if !exists {
				return fmt.Errorf("MCP server %q is not registered in %s", oldName, configPath)
			}
			if _, exists := config.MCP.Servers[newName]; exists {
				return fmt.Errorf("MCP server %q is already registered in %s", newName, configPath)
			}
			if err := ensureMCPRemoteNameAvailable(cmd.Root(), newName); err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpRemoteRenameSpec(), args); err != nil {
				return err
			}
			config.MCP.Servers[newName] = server
			delete(config.MCP.Servers, oldName)
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			_ = renameMCPRemoteCache(opts.mcpCacheDir, oldName, newName)
			fmt.Fprintf(cmd.OutOrStdout(), "renamed MCP server %s to %s in %s\n", oldName, newName, configPath)
			return nil
		},
	}
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func mcpRemoteRemoveCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:     "remove <name> [name...]",
		Aliases: []string{"rm"},
		Short:   "Remove a registered remote MCP server",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := cleanMCPRemoteNames(args)
			if err != nil {
				return err
			}
			removals, err := planMCPRemoteRemovals(names, scope)
			if err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpRemoteRemoveSpec(), args); err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			ctx := commandContext(cmd)
			for _, removal := range removals {
				for _, name := range removal.Names {
					delete(removal.Config.MCP.Servers, name)
				}
				if err := writeToolmuxConfigFile(removal.Path, removal.Config); err != nil {
					return err
				}
			}
			for _, removal := range removals {
				for _, name := range removal.Names {
					_ = removeMCPRemoteCache(opts.mcpCacheDir, name)
					if err := store.DeleteOAuthTokens(ctx, mcpRemoteCredentialRef(opts, name)); err != nil {
						return fmt.Errorf("remove stored auth for MCP server %s: %w", name, err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "removed MCP server %s from %s\n", name, removal.Path)
				}
			}
			return nil
		},
	}
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

type mcpRemoteRemovalPlan struct {
	Path   string
	Config toolmuxConfigFile
	Names  []string
}

func cleanMCPRemoteNames(values []string) ([]string, error) {
	seen := map[string]bool{}
	names := make([]string, 0, len(values))
	for _, value := range values {
		name, err := cleanMCPRemoteName(value)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

func planMCPRemoteRemovals(names []string, scope mcpProfileScopeOptions) ([]mcpRemoteRemovalPlan, error) {
	if scope.Global || scope.Project {
		configPath, _, err := mcpProfileWritePath(scope)
		if err != nil {
			return nil, err
		}
		config, err := readToolmuxConfigFile(configPath)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if _, exists := config.MCP.Servers[name]; !exists {
				return nil, fmt.Errorf("MCP server %q is not registered in %s", name, configPath)
			}
		}
		return []mcpRemoteRemovalPlan{{
			Path:   configPath,
			Config: config,
			Names:  names,
		}}, nil
	}

	plansByPath := map[string]int{}
	var plans []mcpRemoteRemovalPlan
	for _, name := range names {
		entry, ok, err := lookupMCPRemoteServer(name, "")
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("MCP server %q is not registered", name)
		}
		index, exists := plansByPath[entry.Path]
		if !exists {
			config, err := readToolmuxConfigFile(entry.Path)
			if err != nil {
				return nil, err
			}
			plans = append(plans, mcpRemoteRemovalPlan{
				Path:   entry.Path,
				Config: config,
			})
			index = len(plans) - 1
			plansByPath[entry.Path] = index
		}
		plan := &plans[index]
		if _, exists := plan.Config.MCP.Servers[name]; !exists {
			return nil, fmt.Errorf("MCP server %q is not registered in %s", name, entry.Path)
		}
		plan.Names = append(plan.Names, name)
	}
	return plans, nil
}

func mcpRemoteAuthCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage stored auth for imported remote MCP servers",
	}
	cmd.AddCommand(mcpRemoteAuthLoginCommand(opts))
	cmd.AddCommand(mcpRemoteAuthSetCommand(opts))
	cmd.AddCommand(mcpRemoteAuthRemoveCommand(opts))
	cmd.AddCommand(mcpRemoteAuthStatusCommand(opts))
	return cmd
}

func mcpRemoteAuthSetCommand(opts *options) *cobra.Command {
	var bearerToken string
	var bearerTokenEnv string
	var bearerTokenStdin bool
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Store bearer token auth for a remote MCP server",
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
			if err := authorize(cmd, opts, mcpRemoteAuthSetSpec(), args); err != nil {
				return err
			}
			token, err := mcpRemoteBearerTokenFromFlags(cmd, bearerToken, bearerTokenEnv, bearerTokenStdin)
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, name), mcpRemoteBearerTokens(token, entry)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stored bearer token for MCP server %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&bearerToken, "bearer-token", "", "bearer token to store")
	cmd.Flags().StringVar(&bearerTokenEnv, "bearer-token-env", "", "environment variable containing the bearer token")
	cmd.Flags().BoolVar(&bearerTokenStdin, "bearer-token-stdin", false, "read bearer token from stdin")
	return cmd
}

func mcpRemoteAuthRemoveCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove stored auth for a remote MCP server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpRemoteAuthRemoveSpec(), args); err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.DeleteOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, name)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed stored auth for MCP server %s\n", name)
			return nil
		},
	}
}

func mcpRemoteAuthStatusCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show whether auth is stored for a remote MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			if _, ok, err := lookupMCPRemoteServer(name, ""); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			if err := authorize(cmd, opts, mcpRemoteAuthStatusSpec(), args); err != nil {
				return err
			}
			tokens, ok, err := loadMCPRemoteStoredTokens(commandContext(cmd), opts, name)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "no stored auth for MCP server %s\n", name)
				return nil
			}
			if mcpRemoteStoredTokenIsOAuth(tokens) {
				fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for MCP server %s\n", name)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stored bearer token for MCP server %s\n", name)
			return nil
		},
	}
}

func mcpRemoteListCommand(opts *options) *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:     "ls [name]",
		Aliases: []string{"list"},
		Short:   "List registered remote MCP servers",
		Args:    cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, mcpRemoteListSpec(), args); err != nil {
				return err
			}
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
						renderMCPRemoteTree(w, cmd, opts, items)
					})
				}
				result := mcpRemoteToolList{
					Server: entry.Name,
					Tools:  sortedMCPRemoteTools(cache.Tools),
				}
				return writeValue(cmd, opts, result, func(w io.Writer) {
					renderMCPRemoteToolTable(w, cmd, opts, entry.Name, result.Tools)
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
					renderMCPRemoteTree(w, cmd, opts, items)
				})
			}
			items := mcpRemoteListItems(opts, entries)
			return writeValue(cmd, opts, items, func(w io.Writer) {
				renderMCPRemoteListTable(w, cmd, opts, items)
			})
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "R", false, "recursively list tools under registered MCP servers")
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
			if err := authorize(cmd, opts, mcpRemoteShowSpec(), args); err != nil {
				return err
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

func mcpRemoteCatalogEntries(root *cobra.Command, opts *options) ([]mcpRemoteCatalogEntry, error) {
	registered, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil, err
	}
	builtins := mcpBuiltinRemoteServers()
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]mcpRemoteCatalogEntry, 0, len(names))
	for _, name := range names {
		server := normalizeMCPRemoteServer(builtins[name])
		entry := mcpRemoteCatalogEntry{
			Name:       name,
			Status:     "available",
			Registered: false,
			URL:        server.URL,
			Transport:  server.Transport,
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
	return strings.TrimRight(left.URL, "/") == strings.TrimRight(right.URL, "/") && left.Transport == right.Transport
}

func findMCPRemoteServerEntry(entries []mcpRemoteServerEntry, name string) (mcpRemoteServerEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return mcpRemoteServerEntry{}, false
}

func mcpRemoteListItems(opts *options, entries []mcpRemoteServerEntry) []mcpRemoteListItem {
	items := make([]mcpRemoteListItem, 0, len(entries))
	for _, entry := range entries {
		status := "not_synced"
		var tools *int
		if cache, ok, _ := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name); ok {
			status = "synced"
			count := len(cache.Tools)
			tools = &count
		}
		items = append(items, mcpRemoteListItem{
			Name:      entry.Name,
			Status:    status,
			Scope:     mcpRemoteScopeLabel(entry.Scope),
			Scopes:    mcpRemoteNormalizedScopes(entry.Scopes),
			Path:      entry.Path,
			URL:       entry.Server.URL,
			Transport: entry.Server.Transport,
			Tools:     tools,
		})
	}
	return items
}

func renderMCPRemoteListTable(w io.Writer, cmd *cobra.Command, opts *options, items []mcpRemoteListItem) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		status := output.ToneText(human, output.ToneWarning, "not synced")
		tools := "-"
		if item.Status == "synced" {
			status = output.ToneText(human, output.ToneSuccess, "synced")
		}
		if item.Tools != nil {
			tools = fmt.Sprintf("%d", *item.Tools)
		}
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, item.Name),
			status,
			mcpRemoteScopesLabel(item.Scopes),
			tools,
			item.URL,
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Name", "Status", "Scope", "Tools", "URL"},
		Rows:    rows,
		Empty:   "no remote MCP servers registered",
	})
}

func renderMCPRemoteToolTable(w io.Writer, cmd *cobra.Command, opts *options, serverName string, tools []mcpRemoteTool) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(tools))
	for _, tool := range tools {
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, tool.Name),
			mcpRemoteToolArgumentsLabel(tool),
			output.Value(tool.Description),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Tool", "Arguments", "Description"},
		Rows:    rows,
		Empty:   "no cached tools for " + serverName,
	})
}

func mcpRemoteTreeItems(entries []mcpRemoteServerEntry, caches map[string]mcpRemoteCache) []mcpRemoteTreeItem {
	items := make([]mcpRemoteTreeItem, 0, len(entries))
	for _, entry := range entries {
		item := mcpRemoteTreeItem{
			Name:      entry.Name,
			Status:    "not_synced",
			Scope:     mcpRemoteScopeLabel(entry.Scope),
			Scopes:    mcpRemoteNormalizedScopes(entry.Scopes),
			Path:      entry.Path,
			URL:       entry.Server.URL,
			Transport: entry.Server.Transport,
		}
		if cache, ok := caches[entry.Name]; ok {
			item.Status = "synced"
			item.Tools = sortedMCPRemoteTools(cache.Tools)
		}
		items = append(items, item)
	}
	return items
}

func renderMCPRemoteTree(w io.Writer, cmd *cobra.Command, opts *options, items []mcpRemoteTreeItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, output.ToneText(humanOutputOptions(cmd, opts), output.ToneMuted, "no remote MCP servers registered"))
		return
	}
	human := humanOutputOptions(cmd, opts)
	for entryIndex, item := range items {
		suffix := "not synced"
		if item.Status == "synced" {
			suffix = fmt.Sprintf("%d tools", len(item.Tools))
		}
		fmt.Fprintf(w, "%s %s\n", output.ToneText(human, output.ToneInfo, item.Name), output.ToneText(human, output.ToneMuted, "("+suffix+")"))
		if item.Status != "synced" {
			continue
		}
		for toolIndex, tool := range item.Tools {
			connector := "+--"
			if toolIndex == len(item.Tools)-1 {
				connector = "`--"
			}
			args := mcpRemoteToolArgumentsLabel(tool)
			if args != "-" {
				args = " " + output.ToneText(human, output.ToneMuted, "("+args+")")
			} else {
				args = ""
			}
			description := ""
			if strings.TrimSpace(tool.Description) != "" {
				description = " " + output.ToneText(human, output.ToneMuted, "- "+tool.Description)
			}
			fmt.Fprintf(w, "%s %s%s%s\n", connector, tool.Name, args, description)
		}
		if entryIndex != len(items)-1 {
			fmt.Fprintln(w)
		}
	}
}

func renderMCPRemoteCatalogTable(w io.Writer, cmd *cobra.Command, opts *options, entries []mcpRemoteCatalogEntry) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		tools := "-"
		if entry.Tools != nil {
			tools = fmt.Sprint(*entry.Tools)
		}
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, entry.Name),
			mcpRemoteCatalogStatusCell(human, entry),
			output.JoinList(entry.RegisteredNames),
			mcpRemoteScopesLabel(entry.Scopes),
			tools,
			output.Value(entry.URL),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Name", "Status", "Registered As", "Scope", "Tools", "URL"},
		Rows:    rows,
		Empty:   "no known remote MCP servers",
	})
}

func mcpRemoteCatalogStatusCell(human output.Options, entry mcpRemoteCatalogEntry) string {
	switch entry.Status {
	case "registered":
		return output.ToneText(human, output.ToneSuccess, "registered")
	case "available":
		return output.ToneText(human, output.ToneInfo, "available")
	case "alias_required":
		return output.ToneText(human, output.ToneWarning, "alias required")
	case "unavailable":
		return output.ToneText(human, output.ToneWarning, "unavailable")
	default:
		return output.Value(entry.Status)
	}
}

func sortedMCPRemoteTools(tools []mcpRemoteTool) []mcpRemoteTool {
	sorted := append([]mcpRemoteTool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func mcpRemoteScopesLabel(scopes []string) string {
	labels := mcpRemoteNormalizedScopes(scopes)
	if len(labels) == 0 {
		return "-"
	}
	return output.JoinList(labels)
}

func mcpRemoteNormalizedScopes(scopes []string) []string {
	labels := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		label := mcpRemoteScopeLabel(scope)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

func mcpRemoteScopeLabel(scope string) string {
	switch strings.TrimSpace(scope) {
	case "local", "project":
		return "project"
	case "global":
		return "global"
	default:
		return strings.TrimSpace(scope)
	}
}

func mcpRemoteToolArgumentsLabel(tool mcpRemoteTool) string {
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	if len(properties) == 0 {
		return "-"
	}
	required := mcpRemoteRequiredSet(tool.InputSchema)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	labels := make([]string, 0, len(names))
	for _, name := range names {
		label := name
		if required[name] {
			label += "*"
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}

func manageMCPRemoteCatalogInteractive(cmd *cobra.Command, opts *options, scope mcpProfileScopeOptions, syncEnabled bool) error {
	if !interactiveCommand(cmd, opts) {
		return fmt.Errorf("--manage requires table output with interactive stdin, stdout, and stderr")
	}
	entries, err := mcpRemoteCatalogEntries(cmd.Root(), opts)
	if err != nil {
		return err
	}
	selected := make([]string, 0, len(entries))
	options := make([]huh.Option[string], 0, len(entries))
	for _, entry := range entries {
		if entry.Status == "alias_required" || entry.Status == "unavailable" {
			continue
		}
		var title string
		switch {
		case entry.Registered && entry.Tools != nil:
			title = fmt.Sprintf("%s as %s (%d tools)", entry.Name, strings.Join(entry.RegisteredNames, ", "), *entry.Tools)
		case entry.Registered:
			title = fmt.Sprintf("%s as %s", entry.Name, strings.Join(entry.RegisteredNames, ", "))
		default:
			title = entry.Name + " (available)"
		}
		option := huh.NewOption(title, entry.Name)
		if entry.Registered {
			selected = append(selected, entry.Name)
			option = option.Selected(true)
		}
		options = append(options, option)
	}
	if len(options) == 0 {
		return fmt.Errorf("no manageable built-in MCP servers are available")
	}
	height := min(len(options)+4, 12)
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Manage remote MCP servers").
			Description("Selected servers will be registered; unchecked registered servers will be removed.").
			Options(options...).
			Value(&selected).
			Height(height).
			Filterable(false),
	)).
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.ErrOrStderr()).
		WithWidth(terminalWidth(cmd.ErrOrStderr())).
		WithHeight(height + 7)
	if err := form.RunWithContext(commandContext(cmd)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "no MCP catalog changes selected")
			return nil
		}
		return err
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}
	var enable []string
	var disable []string
	for _, entry := range entries {
		if entry.Status == "alias_required" || entry.Status == "unavailable" {
			continue
		}
		if selectedSet[entry.Name] && !entry.Registered {
			enable = append(enable, entry.Name)
		}
		if !selectedSet[entry.Name] && entry.Registered {
			disable = append(disable, entry.Name)
		}
	}
	if len(enable) == 0 && len(disable) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no MCP catalog changes selected")
		return nil
	}
	return applyMCPRemoteCatalogChanges(cmd, opts, scope, enable, disable, syncEnabled)
}

func applyMCPRemoteCatalogChanges(cmd *cobra.Command, opts *options, scope mcpProfileScopeOptions, enable, disable []string, syncEnabled bool) error {
	enableSpecs, err := parseMCPRemoteCatalogEnableSpecs(enable)
	if err != nil {
		return err
	}
	disableNames, err := cleanMCPRemoteCatalogNames(disable)
	if err != nil {
		return err
	}
	if len(enableSpecs) == 0 && len(disableNames) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no MCP catalog changes selected")
		return nil
	}
	builtins := mcpBuiltinRemoteServers()
	registeredEntries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return err
	}
	registered := make(map[string]bool, len(registeredEntries))
	for _, entry := range registeredEntries {
		registered[entry.Name] = true
	}
	var enabled []mcpRemoteServerEntry
	if len(enableSpecs) > 0 {
		configPath, scopeName, err := mcpProfileWritePath(scope)
		if err != nil {
			return err
		}
		config, err := readToolmuxConfigFile(configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if config.MCP.Servers == nil {
			config.MCP.Servers = map[string]mcpRemoteServer{}
		}
		changed := false
		for _, spec := range enableSpecs {
			server, ok := builtins[spec.CatalogName]
			if !ok {
				return fmt.Errorf("unknown built-in MCP server %q", spec.CatalogName)
			}
			if rootNativeCommandHasName(cmd.Root(), spec.RegisteredName) {
				if spec.CatalogName == spec.RegisteredName {
					return fmt.Errorf("MCP server name %q conflicts with an existing Toolmux command; use `toolmux mcp catalog --enable %s=<new-name>`", spec.RegisteredName, spec.CatalogName)
				}
				return fmt.Errorf("MCP server name %q conflicts with an existing Toolmux command; choose a different name", spec.RegisteredName)
			}
			if registered[spec.RegisteredName] {
				continue
			}
			server = normalizeMCPRemoteServer(server)
			config.MCP.Servers[spec.RegisteredName] = server
			registered[spec.RegisteredName] = true
			changed = true
			enabled = append(enabled, mcpRemoteServerEntry{
				Name:   spec.RegisteredName,
				Scope:  scopeName,
				Scopes: []string{scopeName},
				Path:   configPath,
				Server: server,
			})
		}
		if changed {
			config.Version = 1
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			for _, entry := range enabled {
				catalogName := entry.Name
				for _, spec := range enableSpecs {
					if spec.RegisteredName == entry.Name {
						catalogName = spec.CatalogName
						break
					}
				}
				if catalogName == entry.Name {
					fmt.Fprintf(cmd.OutOrStdout(), "enabled %s MCP server %s in %s\n", entry.Scope, entry.Name, configPath)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "enabled %s MCP server %s as %s in %s\n", entry.Scope, catalogName, entry.Name, configPath)
				}
			}
		}
	}
	if len(disableNames) > 0 {
		removed, err := removeMCPRemoteCatalogServers(disableNames, opts.mcpCacheDir)
		if err != nil {
			return err
		}
		for _, name := range removed {
			fmt.Fprintf(cmd.OutOrStdout(), "disabled MCP server %s\n", name)
		}
	}
	if syncEnabled {
		for _, entry := range enabled {
			cache, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, []string{entry.Name})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced MCP server %s: %d tools\n", entry.Name, len(cache.Tools))
		}
	}
	return nil
}

func parseMCPRemoteCatalogEnableSpecs(values []string) ([]mcpRemoteCatalogEnable, error) {
	seen := map[string]bool{}
	var specs []mcpRemoteCatalogEnable
	for _, raw := range values {
		for part := range strings.SplitSeq(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			catalogName, registeredName, hasAlias := strings.Cut(part, "=")
			catalogName, err := cleanMCPRemoteName(catalogName)
			if err != nil {
				return nil, err
			}
			if hasAlias {
				registeredName, err = cleanMCPRemoteName(registeredName)
				if err != nil {
					return nil, err
				}
			} else {
				registeredName = catalogName
			}
			key := catalogName + "=" + registeredName
			if !seen[key] {
				seen[key] = true
				specs = append(specs, mcpRemoteCatalogEnable{CatalogName: catalogName, RegisteredName: registeredName})
			}
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].CatalogName == specs[j].CatalogName {
			return specs[i].RegisteredName < specs[j].RegisteredName
		}
		return specs[i].CatalogName < specs[j].CatalogName
	})
	return specs, nil
}

func cleanMCPRemoteCatalogNames(values []string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, raw := range values {
		for part := range strings.SplitSeq(raw, ",") {
			name, err := cleanMCPRemoteName(part)
			if err != nil {
				return nil, err
			}
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func removeMCPRemoteCatalogServers(names []string, cacheDir string) ([]string, error) {
	remove := make(map[string]bool, len(names))
	builtins := mcpBuiltinRemoteServers()
	for _, name := range names {
		remove[name] = true
	}
	globalPath, err := globalToolmuxConfigPath()
	if err != nil {
		return nil, err
	}
	sources, err := loadMCPProfileSources("", globalPath)
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for _, source := range sources {
		changed := false
		for requestedName := range remove {
			if builtin, ok := builtins[requestedName]; ok {
				for registeredName, server := range source.config.MCP.Servers {
					if registeredName == requestedName || sameMCPRemoteServer(server, builtin) {
						delete(source.config.MCP.Servers, registeredName)
						changed = true
						removed[registeredName] = true
					}
				}
				continue
			}
			if _, ok := source.config.MCP.Servers[requestedName]; ok {
				delete(source.config.MCP.Servers, requestedName)
				changed = true
				removed[requestedName] = true
			}
		}
		if changed {
			if err := writeToolmuxConfigFile(source.Path, source.config); err != nil {
				return nil, err
			}
		}
	}
	var ordered []string
	for _, name := range names {
		if removed[name] {
			_ = removeMCPRemoteCache(cacheDir, name)
			ordered = append(ordered, name)
		}
	}
	for name := range removed {
		if !slices.Contains(ordered, name) {
			_ = removeMCPRemoteCache(cacheDir, name)
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	return ordered, nil
}

func mcpRemoteCatalogCommandModifies(parts []string) bool {
	for _, part := range parts {
		if part == "--manage" || part == "--enable" || part == "--disable" ||
			strings.HasPrefix(part, "--manage=") ||
			strings.HasPrefix(part, "--enable=") || strings.HasPrefix(part, "--disable=") {
			return true
		}
	}
	return false
}

func mcpBuiltinRemoteServers() map[string]mcpRemoteServer {
	return map[string]mcpRemoteServer{
		"atlassian":  {URL: "https://mcp.atlassian.com/v1/mcp/authv2", Transport: mcpRemoteTransportStreamableHTTP},
		"cloudflare": {URL: "https://mcp.cloudflare.com/mcp", Transport: mcpRemoteTransportStreamableHTTP},
		"iterate":    {URL: "https://mock.iterate.com/no-auth", Transport: mcpRemoteTransportStreamableHTTP},
		"linear":     {URL: "https://mcp.linear.app/mcp", Transport: mcpRemoteTransportStreamableHTTP},
		"miro":       {URL: "https://mcp.miro.com/", Transport: mcpRemoteTransportStreamableHTTP},
		"notion":     {URL: "https://mcp.notion.com/mcp", Transport: mcpRemoteTransportStreamableHTTP},
	}
}

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

func isMCPRemoteURLArgument(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

func validateMCPRemoteTransport(transport string) error {
	switch strings.TrimSpace(transport) {
	case "", mcpRemoteTransportStreamableHTTP:
		return nil
	default:
		return fmt.Errorf("unsupported MCP transport %q", transport)
	}
}

func ensureMCPRemoteNameAvailable(root *cobra.Command, name string) error {
	if root != nil && rootCommandHasName(root, name) {
		return fmt.Errorf("MCP server name %q conflicts with an existing Toolmux command; choose a different name", name)
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
	globalPath, err := globalToolmuxConfigPath()
	if err != nil {
		return nil, err
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
		names := make([]string, 0, len(source.config.MCP.Servers))
		for name := range source.config.MCP.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			server := normalizeMCPRemoteServer(source.config.MCP.Servers[name])
			existing, exists := byName[name]
			scopes := []string{source.Scope}
			if exists {
				scopes = append(existing.Scopes, source.Scope)
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
	server.Transport = strings.TrimSpace(server.Transport)
	if server.Transport == "" {
		server.Transport = mcpRemoteTransportStreamableHTTP
	}
	return server
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

func syncMCPRemoteCacheExplicit(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, args []string) (mcpRemoteCache, error) {
	if err := authorize(cmd, opts, mcpRemoteSyncSpec(), args); err != nil {
		return mcpRemoteCache{}, err
	}
	token, err := loadMCPRemoteAccessToken(commandContext(cmd), opts, entry)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	cache, err := syncMCPRemoteServer(commandContext(cmd), opts.httpClient, entry, token, nil)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	if err := writeMCPRemoteCache(opts.mcpCacheDir, entry.Name, cache); err != nil {
		return mcpRemoteCache{}, err
	}
	return cache, nil
}

func syncMCPRemoteCacheAfterAdd(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, args []string) (mcpRemoteCache, error) {
	cache, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, args)
	if err == nil {
		return cache, nil
	}
	if !mcpRemoteErrorStatus(err, http.StatusUnauthorized) {
		return mcpRemoteCache{}, err
	}
	if _, ok, loadErr := loadMCPRemoteStoredTokens(commandContext(cmd), opts, entry.Name); loadErr != nil {
		return mcpRemoteCache{}, loadErr
	} else if ok {
		return mcpRemoteCache{}, err
	}
	if authErr := authorize(cmd, opts, mcpRemoteAuthLoginSpec(), []string{entry.Name}); authErr != nil {
		return mcpRemoteCache{}, fmt.Errorf("%s; OAuth login was denied: %w", err.Error(), authErr)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "MCP server %s requires auth; starting OAuth login\n", entry.Name)
	tokens, loginErr := loginMCPRemoteOAuth(cmd, opts, entry, mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute})
	if loginErr != nil {
		return mcpRemoteCache{}, fmt.Errorf("%s; OAuth login failed: %w", err.Error(), loginErr)
	}
	store, storeErr := opts.credentials()
	if storeErr != nil {
		return mcpRemoteCache{}, storeErr
	}
	if saveErr := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, entry.Name), tokens); saveErr != nil {
		return mcpRemoteCache{}, saveErr
	}
	fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for MCP server %s\n", entry.Name)
	return syncMCPRemoteCacheExplicit(cmd, opts, entry, args)
}

func refreshMCPRemoteCacheIfStale(ctx context.Context, cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, bool) {
	cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
	if err != nil {
		return mcpRemoteCache{}, false
	}
	if ok && mcpRemoteCacheFresh(cache, time.Now().UTC()) {
		return cache, true
	}
	if err := authorize(cmd, opts, mcpRemoteSyncSpec(), []string{entry.Name}); err != nil {
		return cache, ok
	}
	token, err := loadMCPRemoteAccessToken(ctx, opts, entry)
	if err != nil {
		return cache, ok
	}
	refreshed, err := syncMCPRemoteServer(ctx, opts.httpClient, entry, token, trace)
	if err != nil {
		return cache, ok
	}
	if err := writeMCPRemoteCache(opts.mcpCacheDir, entry.Name, refreshed); err != nil {
		return cache, ok
	}
	return refreshed, true
}

func mcpRemoteCacheFresh(cache mcpRemoteCache, now time.Time) bool {
	if cache.Version != mcpRemoteCacheVersion || cache.SyncedAt.IsZero() {
		return false
	}
	return now.Sub(cache.SyncedAt) < mcpRemoteCacheMaxAge
}

func mcpRemoteToolFromCache(cache mcpRemoteCache, name string) (mcpRemoteTool, bool) {
	for _, tool := range cache.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcpRemoteTool{}, false
}

func syncMCPRemoteServer(ctx context.Context, client *http.Client, entry mcpRemoteServerEntry, bearerToken string, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, error) {
	if client == nil {
		client = http.DefaultClient
	}
	initResult, sessionID, err := initializeMCPRemoteSession(ctx, client, entry.Server, bearerToken, trace)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	_ = json.Unmarshal(initResult, &init)
	toolsResult, _, err := callMCPRemote(ctx, client, entry.Server, bearerToken, sessionID, "tools/list", nil, trace)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	var decoded struct {
		Tools []mcpRemoteTool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResult, &decoded); err != nil {
		return mcpRemoteCache{}, fmt.Errorf("decode remote MCP tools/list: %w", err)
	}
	for i := range decoded.Tools {
		decoded.Tools[i].Name = strings.TrimSpace(decoded.Tools[i].Name)
		if decoded.Tools[i].InputSchema == nil {
			decoded.Tools[i].InputSchema = map[string]any{"type": "object"}
		}
	}
	decoded.Tools = slices.DeleteFunc(decoded.Tools, func(tool mcpRemoteTool) bool {
		return tool.Name == ""
	})
	sort.Slice(decoded.Tools, func(i, j int) bool {
		return decoded.Tools[i].Name < decoded.Tools[j].Name
	})
	fingerprint := mcpRemoteToolsFingerprint(decoded.Tools)
	return mcpRemoteCache{
		Version:         mcpRemoteCacheVersion,
		Name:            entry.Name,
		URL:             entry.Server.URL,
		Transport:       entry.Server.Transport,
		ProtocolVersion: init.ProtocolVersion,
		ServerInfo:      init.ServerInfo,
		Tools:           decoded.Tools,
		SyncedAt:        time.Now().UTC(),
		Fingerprint:     fingerprint,
		Raw: map[string]json.RawMessage{
			"initialize": initResult,
			"tools_list": toolsResult,
		},
	}, nil
}

func mcpRemoteToolsFingerprint(tools []mcpRemoteTool) string {
	data, _ := json.Marshal(tools)
	return mcpRemoteMetadataHash(data)
}

func mcpRemoteMetadataHash(data []byte) string {
	var hash uint64 = 1469598103934665603
	for _, b := range data {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return fmt.Sprintf("%016x", hash)
}

func initializeMCPRemoteSession(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken string, trace *mcpRemoteHTTPTrace) (json.RawMessage, string, error) {
	initResult, sessionID, err := callMCPRemote(ctx, client, server, bearerToken, "", "initialize", mcpRemoteInitializeParams(), trace)
	if err != nil {
		return nil, "", err
	}
	if sessionID != "" {
		if err := notifyMCPRemoteInitialized(ctx, client, server, bearerToken, sessionID, trace); err != nil {
			return nil, "", err
		}
	}
	return initResult, sessionID, nil
}

func mcpRemoteInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "toolmux",
			"version": version.Version,
		},
	}
}

func notifyMCPRemoteInitialized(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID string, trace *mcpRemoteHTTPTrace) error {
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateMCPRemoteURL(server.URL); err != nil {
		return err
	}
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	if err != nil {
		return err
	}
	resp, err := postMCPRemote(ctx, client, server, bearerToken, sessionID, body, trace)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("remote MCP notifications/initialized returned status %d", resp.StatusCode)
	}
	return nil
}

func callMCPRemote(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID, method string, params any, trace *mcpRemoteHTTPTrace) (json.RawMessage, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateMCPRemoteURL(server.URL); err != nil {
		return nil, "", err
	}
	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, "", err
		}
		rawParams = data
	}
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return nil, "", err
	}
	resp, err := postMCPRemote(ctx, client, server, bearerToken, sessionID, body, trace)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	responseSessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id"))
	if responseSessionID == "" {
		responseSessionID = sessionID
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, responseSessionID, &mcpRemoteHTTPStatusError{
			Method:     method,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(data)),
		}
	}
	result, err := decodeMCPRemoteResponse(resp, method)
	if err != nil {
		return nil, responseSessionID, err
	}
	return result, responseSessionID, nil
}

func (err *mcpRemoteHTTPStatusError) Error() string {
	if err == nil {
		return ""
	}
	if err.Body == "" {
		return fmt.Sprintf("remote MCP %s returned status %d", err.Method, err.StatusCode)
	}
	return fmt.Sprintf("remote MCP %s returned status %d: %s", err.Method, err.StatusCode, err.Body)
}

func mcpRemoteErrorStatus(err error, status int) bool {
	var statusErr *mcpRemoteHTTPStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == status
}

func postMCPRemote(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID string, body []byte, trace *mcpRemoteHTTPTrace) (*http.Response, error) {
	// #nosec G107 -- remote MCP server URLs are explicit user configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Protocol-Version", mcpProtocolVersion)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if trace != nil {
		trace.writeRequest(req, body)
	}
	resp, err := client.Do(req)
	if err != nil {
		if trace != nil {
			trace.writeError(err)
		}
		return nil, err
	}
	if trace != nil {
		data, truncated, err := mcpRemoteReadAndRestoreResponseBody(resp)
		if err != nil {
			return nil, err
		}
		trace.writeResponse(resp, data, truncated)
	}
	return resp, nil
}

func newMCPRemoteHTTPTrace(w io.Writer, enabled bool) *mcpRemoteHTTPTrace {
	if !enabled || w == nil {
		return nil
	}
	return &mcpRemoteHTTPTrace{w: w}
}

func (trace *mcpRemoteHTTPTrace) writeRequest(req *http.Request, body []byte) {
	if trace == nil || trace.w == nil {
		return
	}
	requestURI := req.URL.RequestURI()
	if requestURI == "" {
		requestURI = "/"
	}
	fmt.Fprintln(trace.w, "----- MCP HTTP request -----")
	fmt.Fprintf(trace.w, "%s %s HTTP/1.1\n", req.Method, requestURI)
	fmt.Fprintf(trace.w, "Host: %s\n", req.URL.Host)
	writeMCPRemoteHTTPHeaders(trace.w, req.Header)
	fmt.Fprintln(trace.w)
	writeMCPRemoteHTTPBody(trace.w, body)
}

func (trace *mcpRemoteHTTPTrace) writeResponse(resp *http.Response, body []byte, truncated bool) {
	if trace == nil || trace.w == nil {
		return
	}
	proto := resp.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	fmt.Fprintln(trace.w, "----- MCP HTTP response -----")
	fmt.Fprintf(trace.w, "%s %s\n", proto, resp.Status)
	writeMCPRemoteHTTPHeaders(trace.w, resp.Header)
	fmt.Fprintln(trace.w)
	writeMCPRemoteHTTPBody(trace.w, body)
	if truncated {
		fmt.Fprintf(trace.w, "[truncated after %d bytes]\n", mcpRemoteTraceBodyLimit)
	}
}

func (trace *mcpRemoteHTTPTrace) writeError(err error) {
	if trace == nil || trace.w == nil {
		return
	}
	fmt.Fprintln(trace.w, "----- MCP HTTP error -----")
	fmt.Fprintln(trace.w, err)
}

func writeMCPRemoteHTTPHeaders(w io.Writer, header http.Header) {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := header.Values(name)
		if mcpRemoteSensitiveHTTPHeader(name) {
			values = []string{"<redacted>"}
		}
		for _, value := range values {
			fmt.Fprintf(w, "%s: %s\n", name, value)
		}
	}
}

func mcpRemoteSensitiveHTTPHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	default:
		return false
	}
}

func writeMCPRemoteHTTPBody(w io.Writer, body []byte) {
	if len(body) == 0 {
		return
	}
	_, _ = w.Write(body)
	if body[len(body)-1] != '\n' {
		fmt.Fprintln(w)
	}
}

func mcpRemoteReadAndRestoreResponseBody(resp *http.Response) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, mcpRemoteTraceBodyLimit+1))
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, false, err
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	truncated := len(data) > mcpRemoteTraceBodyLimit
	if truncated {
		data = data[:mcpRemoteTraceBodyLimit]
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return data, truncated, nil
}

func decodeMCPRemoteResponse(resp *http.Response, method string) (json.RawMessage, error) {
	var decoded mcpResponse
	if mcpRemoteResponseIsSSE(resp.Header.Get("Content-Type")) {
		eventData, err := readMCPRemoteSSEMessage(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("decode remote MCP %s SSE response: %w", method, err)
		}
		if err := json.Unmarshal(eventData, &decoded); err != nil {
			return nil, fmt.Errorf("decode remote MCP %s SSE message: %w", method, err)
		}
	} else if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode remote MCP %s response: %w", method, err)
	}
	if decoded.Error != nil {
		return nil, decoded.Error
	}
	result, err := json.Marshal(decoded.Result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func mcpRemoteResponseIsSSE(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

func readMCPRemoteSSEMessage(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 8<<20))
	if err != nil {
		return nil, err
	}
	eventName := ""
	var dataLines []string
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if line == "" {
			if len(dataLines) > 0 && (eventName == "" || eventName == "message") {
				return []byte(strings.Join(dataLines, "\n")), nil
			}
			eventName = ""
			dataLines = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if len(dataLines) > 0 && (eventName == "" || eventName == "message") {
		return []byte(strings.Join(dataLines, "\n")), nil
	}
	return nil, fmt.Errorf("no message event found")
}

func callMCPRemoteTool(ctx context.Context, client *http.Client, entry mcpRemoteServerEntry, tool mcpRemoteTool, arguments map[string]any, bearerToken string, trace *mcpRemoteHTTPTrace) (mcpCallToolResult, error) {
	_, sessionID, err := initializeMCPRemoteSession(ctx, client, entry.Server, bearerToken, trace)
	if err != nil {
		return mcpCallToolResult{}, err
	}
	result, _, err := callMCPRemote(ctx, client, entry.Server, bearerToken, sessionID, "tools/call", map[string]any{
		"name":      tool.Name,
		"arguments": arguments,
	}, trace)
	if err != nil {
		return mcpCallToolResult{}, err
	}
	var callResult mcpCallToolResult
	if err := json.Unmarshal(result, &callResult); err == nil && len(callResult.Content) > 0 {
		return callResult, nil
	}
	return mcpTextToolResult(string(result)), nil
}

func registerCachedMCPRemoteCommands(root *cobra.Command, opts *options) []mcpRemoteNameConflict {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil
	}
	var conflicts []mcpRemoteNameConflict
	for _, entry := range entries {
		if rootCommandHasName(root, entry.Name) {
			conflicts = append(conflicts, mcpRemoteNameConflict{Name: entry.Name})
			continue
		}
		command := mcpRemoteRootCommand(opts, entry)
		root.AddCommand(command)
	}
	return conflicts
}

func mcpRemoteRootCommand(opts *options, entry mcpRemoteServerEntry) *cobra.Command {
	cache, ok, _ := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
	cmd := &cobra.Command{
		Use:   entry.Name,
		Short: "Imported remote MCP server " + entry.Name,
		Annotations: map[string]string{
			mcpRemoteServerAnnotation: entry.Name,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return fmt.Errorf("MCP server %q has no command %q; run `toolmux mcp sync %s` to refresh cached tools", entry.Name, strings.Join(args, " "), entry.Name)
		},
	}
	if !ok {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("MCP server %q has no cached tools; run `toolmux mcp sync %s`", entry.Name, entry.Name)
		}
		return cmd
	}
	for _, tool := range cache.Tools {
		cmd.AddCommand(mcpRemoteToolCommand(opts, entry, tool))
	}
	return cmd
}

func mcpRemoteToolCommand(opts *options, entry mcpRemoteServerEntry, tool mcpRemoteTool) *cobra.Command {
	var rawJSON string
	var verboseHTTP bool
	spec := mcpRemoteActionSpec(entry.Name, tool)
	cmd := &cobra.Command{
		Use:   tool.Name,
		Short: firstNonEmpty(tool.Description, "Call remote MCP tool "+tool.Name),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := decodeMCPRemoteCLIArguments(cmd, rawJSON, tool)
			if err != nil {
				return err
			}
			if err := authorize(cmd, opts, spec, nil); err != nil {
				return err
			}
			trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), verboseHTTP)
			toolForCall := tool
			if refreshed, ok := refreshMCPRemoteCacheIfStale(commandContext(cmd), cmd, opts, entry, trace); ok {
				freshTool, found := mcpRemoteToolFromCache(refreshed, tool.Name)
				if !found {
					return fmt.Errorf("remote MCP tool %s.%s no longer exists after cache refresh", entry.Name, tool.Name)
				}
				toolForCall = freshTool
			}
			token, err := loadMCPRemoteAccessToken(commandContext(cmd), opts, entry)
			if err != nil {
				return err
			}
			result, err := callMCPRemoteTool(commandContext(cmd), opts.httpClient, entry, toolForCall, arguments, token, trace)
			if err != nil {
				return err
			}
			return writeMCPRemoteToolResult(cmd, opts, result)
		},
	}
	cmd.Flags().StringVar(&rawJSON, "json", "", "JSON object with remote MCP tool arguments, or @path")
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote MCP HTTP requests and responses to stderr")
	addMCPRemoteToolFlags(cmd, tool)
	setMCPRemoteToolHelp(cmd, entry, tool)
	return cmd
}

func schemaCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "schema <server.tool|server tool>",
		Short: "Show a tool input schema",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, schemaSpec(), args); err != nil {
				return err
			}
			serverName, toolName, err := parseSchemaToolArgs(args)
			if err != nil {
				return err
			}
			ref, ok, err := lookupMCPRemoteToolForSchema(commandContext(cmd), cmd, opts, serverName, toolName)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("remote MCP tool %s.%s not found; run `toolmux mcp sync %s` to refresh cached tools", serverName, toolName, serverName)
			}
			return writeValue(cmd, opts, ref.Tool.InputSchema, nil)
		},
	}
}

func parseSchemaToolArgs(args []string) (string, string, error) {
	if len(args) == 2 {
		serverName, err := cleanMCPRemoteName(args[0])
		if err != nil {
			return "", "", err
		}
		toolName := strings.TrimSpace(args[1])
		if toolName == "" {
			return "", "", fmt.Errorf("tool name is required")
		}
		return serverName, toolName, nil
	}
	toolID := strings.TrimSpace(args[0])
	serverName, toolName, ok := strings.Cut(toolID, ".")
	if !ok || strings.TrimSpace(serverName) == "" || strings.TrimSpace(toolName) == "" {
		return "", "", fmt.Errorf("tool must be referenced as <server>.<tool> or <server> <tool>")
	}
	cleanedServerName, err := cleanMCPRemoteName(serverName)
	if err != nil {
		return "", "", err
	}
	return cleanedServerName, strings.TrimSpace(toolName), nil
}

func lookupMCPRemoteToolForSchema(ctx context.Context, cmd *cobra.Command, opts *options, serverName, toolName string) (mcpRemoteToolRef, bool, error) {
	entry, ok, err := lookupMCPRemoteServer(serverName, "")
	if err != nil {
		return mcpRemoteToolRef{}, false, err
	}
	if !ok {
		return mcpRemoteToolRef{}, false, fmt.Errorf("MCP server %q is not registered", serverName)
	}
	cache, ok := refreshMCPRemoteCacheIfStale(ctx, cmd, opts, entry, nil)
	if !ok {
		return mcpRemoteToolRef{}, false, nil
	}
	tool, found := mcpRemoteToolFromCache(cache, toolName)
	if !found {
		return mcpRemoteToolRef{}, false, nil
	}
	return mcpRemoteToolRef{Entry: entry, Cache: cache, Tool: tool}, true, nil
}

func addMCPRemoteToolFlags(cmd *cobra.Command, tool mcpRemoteTool) {
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	required := mcpRemoteRequiredSet(tool.InputSchema)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if mcpRemoteFlagNameReserved(cmd, name) {
			continue
		}
		property, _ := properties[name].(map[string]any)
		usage := mcpRemoteFlagUsage(property, required[name])
		switch mcpRemoteSchemaType(property) {
		case "boolean":
			cmd.Flags().Bool(name, false, usage)
		case "integer":
			cmd.Flags().Int(name, 0, usage)
		case "number":
			cmd.Flags().Float64(name, 0, usage)
		case "string":
			cmd.Flags().String(name, "", usage)
		case "boolean_array":
			cmd.Flags().BoolSlice(name, nil, usage)
		case "integer_array":
			cmd.Flags().IntSlice(name, nil, usage)
		case "number_array":
			cmd.Flags().Float64Slice(name, nil, usage)
		case "string_array":
			cmd.Flags().StringArray(name, nil, usage)
		}
	}
}

func decodeMCPRemoteCLIArguments(cmd *cobra.Command, rawJSON string, tool mcpRemoteTool) (map[string]any, error) {
	arguments := map[string]any{}
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON != "" {
		data := []byte(rawJSON)
		if path, ok := strings.CutPrefix(rawJSON, "@"); ok {
			read, err := os.ReadFile(path) // #nosec G304 -- explicit CLI argument file.
			if err != nil {
				return nil, err
			}
			data = read
		}
		if err := json.Unmarshal(data, &arguments); err != nil {
			return nil, fmt.Errorf("--json must be a JSON object: %w", err)
		}
		if arguments == nil {
			return nil, fmt.Errorf("--json must be a JSON object")
		}
	}
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	for name := range properties {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || !cmd.Flags().Changed(name) {
			continue
		}
		property, _ := properties[name].(map[string]any)
		switch mcpRemoteSchemaType(property) {
		case "boolean":
			value, err := cmd.Flags().GetBool(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "integer":
			value, err := cmd.Flags().GetInt(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "number":
			value, err := cmd.Flags().GetFloat64(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "string":
			value, err := cmd.Flags().GetString(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "boolean_array":
			value, err := cmd.Flags().GetBoolSlice(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "integer_array":
			value, err := cmd.Flags().GetIntSlice(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "number_array":
			value, err := cmd.Flags().GetFloat64Slice(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "string_array":
			value, err := cmd.Flags().GetStringArray(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		}
	}
	if err := validateMCPRemoteRequiredArguments(arguments, tool.InputSchema); err != nil {
		return nil, err
	}
	return arguments, nil
}

func mcpRemoteSchemaType(property map[string]any) string {
	if mcpRemoteSchemaHasType(property, "array") {
		items, _ := property["items"].(map[string]any)
		switch mcpRemoteSchemaScalarType(items) {
		case "boolean":
			return "boolean_array"
		case "integer":
			return "integer_array"
		case "number":
			return "number_array"
		case "string":
			return "string_array"
		default:
			return ""
		}
	}
	if scalar := mcpRemoteSchemaScalarType(property); scalar != "" {
		return scalar
	}
	if len(mcpRemoteSchemaEnum(property)) > 0 {
		return "string"
	}
	return ""
}

func mcpRemoteSchemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return map[string]any{}
	}
	return properties
}

func mcpRemoteSchemaScalarType(property map[string]any) string {
	for _, typ := range mcpRemoteSchemaTypes(property["type"]) {
		switch typ {
		case "boolean", "integer", "number", "string":
			return typ
		}
	}
	return ""
}

func mcpRemoteSchemaHasType(property map[string]any, want string) bool {
	return slices.Contains(mcpRemoteSchemaTypes(property["type"]), want)
}

func mcpRemoteSchemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	case []any:
		types := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok {
				types = append(types, value)
			}
		}
		return types
	default:
		return nil
	}
}

func mcpRemoteFlagNameReserved(cmd *cobra.Command, name string) bool {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
		return true
	}
	if cmd.Flags().Lookup(name) != nil {
		return true
	}
	switch name {
	case "account", "color", "help", "output", "pager", "policy", "profile", "read-only":
		return true
	default:
		return false
	}
}

func mcpRemoteFlagUsage(property map[string]any, required bool) string {
	usage, _ := property["description"].(string)
	usage = strings.TrimSpace(usage)
	var details []string
	if enum := mcpRemoteSchemaEnum(property); len(enum) > 0 {
		details = append(details, "one of: "+strings.Join(enum, ", "))
	}
	if required {
		details = append(details, "required")
	}
	if len(details) == 0 {
		return usage
	}
	detail := strings.Join(details, "; ")
	if usage == "" {
		return detail
	}
	return usage + " (" + detail + ")"
}

func mcpRemoteSchemaEnum(property map[string]any) []string {
	raw, ok := property["enum"]
	if !ok {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return values
	case []any:
		enum := make([]string, 0, len(values))
		for _, value := range values {
			enum = append(enum, fmt.Sprint(value))
		}
		return enum
	default:
		return nil
	}
}

func validateMCPRemoteRequiredArguments(arguments map[string]any, schema map[string]any) error {
	required := mcpRemoteRequiredSet(schema)
	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0, len(required))
	for name := range required {
		if _, ok := arguments[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing required MCP tool arguments: %s", strings.Join(missing, ", "))
}

func mcpRemoteRequiredSet(schema map[string]any) map[string]bool {
	required := map[string]bool{}
	switch values := schema["required"].(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				required[value] = true
			}
		}
	case []any:
		for _, item := range values {
			value, ok := item.(string)
			if ok {
				if value = strings.TrimSpace(value); value != "" {
					required[value] = true
				}
			}
		}
	}
	return required
}

func setMCPRemoteToolHelp(cmd *cobra.Command, entry mcpRemoteServerEntry, tool mcpRemoteTool) {
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		description := strings.TrimSpace(cmd.Long)
		if description == "" {
			description = strings.TrimSpace(cmd.Short)
		}
		if description != "" {
			fmt.Fprintln(cmd.OutOrStdout(), description)
			fmt.Fprintln(cmd.OutOrStdout())
		}
		if cmd.Runnable() || cmd.HasSubCommands() {
			fmt.Fprint(cmd.OutOrStdout(), cmd.UsageString())
		}
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintf(cmd.OutOrStdout(), "Run `toolmux schema %s %s` to view the full input schema.\n", entry.Name, tool.Name)
	})
}

func writeMCPRemoteToolResult(cmd *cobra.Command, opts *options, result mcpCallToolResult) error {
	if opts.output == "table" && len(result.Content) == 1 && result.Content[0].Type == "text" {
		fmt.Fprintln(cmd.OutOrStdout(), result.Content[0].Text)
		return nil
	}
	return writeValue(cmd, opts, result, nil)
}

func mcpRemoteActionSpec(serverName string, tool mcpRemoteTool) actions.Spec {
	id := serverName + "." + tool.Name
	spec := actions.Command(actions.LocalName(id), tool.Name,
		actions.Use(tool.Name),
		actions.Short(firstNonEmpty(tool.Description, "Call remote MCP tool "+tool.Name)),
		actions.RBAC(actions.ResourceName("mcp_remote"), actions.Verb("call"), actions.EffectWrite, actions.EffectNone),
		actions.Risks("remote-mcp", "remote-write"),
		actions.Scopes("mcp:"+serverName),
	)
	spec.Provider = serverName
	spec.Path = []string{serverName, tool.Name}
	return spec
}

func cachedMCPRemoteCommandSpecs(opts *options) []policy.CommandSpec {
	if opts == nil {
		return nil
	}
	refs := mcpRemoteToolRefs(opts.mcpCacheDir)
	specs := make([]policy.CommandSpec, 0, len(refs))
	for _, ref := range refs {
		specs = append(specs, mcpRemoteActionSpec(ref.Entry.Name, ref.Tool))
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func mcpRemoteSpecForCommandParts(opts *options, parts []string) (policy.CommandSpec, bool) {
	if opts == nil || len(parts) < 2 {
		return policy.CommandSpec{}, false
	}
	for _, ref := range mcpRemoteToolRefs(opts.mcpCacheDir) {
		if ref.Entry.Name == parts[0] && ref.Tool.Name == parts[1] {
			return mcpRemoteActionSpec(ref.Entry.Name, ref.Tool), true
		}
	}
	return policy.CommandSpec{}, false
}

func (server mcpServer) remoteMCPTools(ctx context.Context) []any {
	if server.opts == nil {
		return nil
	}
	refs := server.remoteMCPToolRefs(ctx)
	tools := make([]any, 0, len(refs))
	for _, ref := range refs {
		spec := mcpRemoteActionSpec(ref.Entry.Name, ref.Tool)
		if !server.selector.matches(spec) {
			continue
		}
		tools = append(tools, map[string]any{
			"name":        spec.ID,
			"description": ref.Tool.Description,
			"inputSchema": ref.Tool.InputSchema,
		})
	}
	return tools
}

func (server mcpServer) remoteMCPToolRefs(ctx context.Context) []mcpRemoteToolRef {
	if server.opts == nil {
		return nil
	}
	return mcpRemoteToolRefsWithRefresh(ctx, server.cmd, server.opts)
}

func mcpRemoteToolRefs(cacheDir string) []mcpRemoteToolRef {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil
	}
	var refs []mcpRemoteToolRef
	for _, entry := range entries {
		cache, ok, err := readMCPRemoteCacheIfExists(cacheDir, entry.Name)
		if err != nil || !ok {
			continue
		}
		for _, tool := range cache.Tools {
			refs = append(refs, mcpRemoteToolRef{Entry: entry, Cache: cache, Tool: tool})
		}
	}
	return refs
}

func mcpRemoteToolRefsWithRefresh(ctx context.Context, cmd *cobra.Command, opts *options) []mcpRemoteToolRef {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil
	}
	var refs []mcpRemoteToolRef
	for _, entry := range entries {
		cache, ok := refreshMCPRemoteCacheIfStale(ctx, cmd, opts, entry, nil)
		if !ok {
			continue
		}
		for _, tool := range cache.Tools {
			refs = append(refs, mcpRemoteToolRef{Entry: entry, Cache: cache, Tool: tool})
		}
	}
	return refs
}

func (server mcpServer) lookupRemoteMCPTool(ctx context.Context, name string) (mcpRemoteToolRef, bool) {
	for _, ref := range server.remoteMCPToolRefs(ctx) {
		spec := mcpRemoteActionSpec(ref.Entry.Name, ref.Tool)
		if spec.ID == name && server.selector.matches(spec) {
			return ref, true
		}
	}
	return mcpRemoteToolRef{}, false
}

func remoteMCPToolArguments(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tool arguments must be an object")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return arguments, nil
}

func mcpRemoteConflictsError(conflicts []mcpRemoteNameConflict) error {
	if len(conflicts) == 0 {
		return nil
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Name < conflicts[j].Name
	})
	conflict := conflicts[0]
	return fmt.Errorf("imported MCP server %q conflicts with a native Toolmux command; rename it with: toolmux mcp rename %s <new-name>", conflict.Name, conflict.Name)
}

func mcpRemoteCommandAllowsConflicts(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	path := commandPathNames(cmd)
	if len(path) < 3 || path[0] != "toolmux" || path[1] != "mcp" {
		return false
	}
	switch path[2] {
	case "add", "register", "sync", "rename", "remove", "rm", "ls", "list", "show", "catalog", "available", "auth":
		return true
	default:
		return false
	}
}

func commandPathNames(cmd *cobra.Command) []string {
	var reversed []string
	for current := cmd; current != nil; current = current.Parent() {
		reversed = append(reversed, current.Name())
	}
	names := make([]string, len(reversed))
	for i := range reversed {
		names[i] = reversed[len(reversed)-1-i]
	}
	return names
}

func mcpRemoteBearerTokenFromFlags(cmd *cobra.Command, literal, envName string, stdin bool) (string, error) {
	sources := 0
	if strings.TrimSpace(literal) != "" {
		sources++
	}
	if strings.TrimSpace(envName) != "" {
		sources++
	}
	if stdin {
		sources++
	}
	if sources != 1 {
		return "", fmt.Errorf("provide exactly one of --bearer-token, --bearer-token-env, or --bearer-token-stdin")
	}
	token := literal
	if strings.TrimSpace(envName) != "" {
		token = os.Getenv(strings.TrimSpace(envName))
	}
	if stdin {
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
		if err != nil {
			return "", err
		}
		token = string(data)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("bearer token is required")
	}
	return token, nil
}

func mcpRemoteCredentialRef(opts *options, name string) credentials.ConnectionRef {
	account := strings.TrimSpace(opts.account)
	if account == "" {
		account = "default"
	}
	return credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  mcpRemoteCredentialProvider,
		Service:   name,
		AccountID: account,
	}
}

func mcpRemoteBearerTokens(token string, entry mcpRemoteServerEntry) credentials.OAuthTokens {
	return credentials.OAuthTokens{
		AccessToken: token,
		TokenType:   "bearer",
		Extra: map[string]string{
			"auth_type":  mcpRemoteAuthTypeBearer,
			"mcp_server": entry.Name,
			"url":        entry.Server.URL,
		},
	}
}

func mcpRemoteAddSpec() actions.Spec {
	return actions.Command("mcp.add", "add",
		actions.Use("mcp add <name> [url]"),
		actions.Short("Register a remote MCP server"),
		actions.RBAC("mcp_server", actions.VerbCreate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func mcpRemoteSyncSpec() actions.Spec {
	return actions.Command("mcp.sync", "sync",
		actions.Use("mcp sync <name>"),
		actions.Short("Introspect and cache a remote MCP server"),
		actions.RBAC("mcp_server_cache", actions.VerbUpdate, actions.EffectRead, actions.EffectWrite),
		actions.Risks("remote-mcp", "mcp-cache"),
	)
}

func mcpRemoteRenameSpec() actions.Spec {
	return actions.Command("mcp.rename", "rename",
		actions.Use("mcp rename <old-name> <new-name>"),
		actions.Short("Rename a registered remote MCP server"),
		actions.RBAC("mcp_server", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func mcpRemoteRemoveSpec() actions.Spec {
	return actions.Command("mcp.remove", "remove",
		actions.Use("mcp remove <name> [name...]"),
		actions.Short("Remove a registered remote MCP server"),
		actions.RBAC("mcp_server", actions.VerbDelete, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config", "mcp-auth"),
	)
}

func mcpRemoteListSpec() actions.Spec {
	return actions.Command("mcp.ls", "ls",
		actions.Use("mcp ls [name]"),
		actions.Short("List registered remote MCP servers"),
		actions.RBAC("mcp_server", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteShowSpec() actions.Spec {
	return actions.Command("mcp.show", "show",
		actions.Use("mcp show <name>"),
		actions.Short("Show a registered remote MCP server"),
		actions.RBAC("mcp_server", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteCatalogListSpec() actions.Spec {
	return actions.Command("mcp.catalog", "catalog",
		actions.Use("mcp catalog"),
		actions.Short("List known remote MCP servers"),
		actions.RBAC("mcp_server_catalog", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteCatalogManageSpec() actions.Spec {
	return actions.Command("mcp.catalog.manage", "catalog",
		actions.Use("mcp catalog --manage|--enable <name>|--disable <name>"),
		actions.Short("Enable or disable known remote MCP servers"),
		actions.RBAC("mcp_server", actions.VerbUpdate, actions.EffectRead, actions.EffectWrite),
		actions.Risks("mcp-config", "remote-mcp"),
	)
}

func mcpRemoteAuthLoginSpec() actions.Spec {
	return actions.Command("mcp.auth.login", "login",
		actions.Use("mcp auth login <name>"),
		actions.Short("Authorize a remote MCP server with OAuth"),
		actions.RBAC("mcp_server_auth", actions.VerbConnect, actions.EffectWrite, actions.EffectWrite),
		actions.Risks("remote-mcp", "secret-storage"),
	)
}

func mcpRemoteAuthSetSpec() actions.Spec {
	return actions.Command("mcp.auth.set", "set",
		actions.Use("mcp auth set <name>"),
		actions.Short("Store bearer token auth for a remote MCP server"),
		actions.RBAC("mcp_server_auth", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("secret-storage"),
	)
}

func mcpRemoteAuthRemoveSpec() actions.Spec {
	return actions.Command("mcp.auth.remove", "remove",
		actions.Use("mcp auth remove <name>"),
		actions.Short("Remove stored auth for a remote MCP server"),
		actions.RBAC("mcp_server_auth", actions.VerbDelete, actions.EffectNone, actions.EffectWrite),
		actions.Risks("secret-storage"),
	)
}

func mcpRemoteAuthStatusSpec() actions.Spec {
	return actions.Command("mcp.auth.status", "status",
		actions.Use("mcp auth status <name>"),
		actions.Short("Show remote MCP stored auth status"),
		actions.RBAC("mcp_server_auth", actions.VerbStatus, actions.EffectNone, actions.EffectRead),
	)
}

func schemaSpec() actions.Spec {
	return actions.Command("schema", "schema",
		actions.Use("schema <server.tool|server tool>"),
		actions.Short("Show a tool input schema"),
		actions.RBAC("tool_schema", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}
