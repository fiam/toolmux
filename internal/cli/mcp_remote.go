package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/version"
)

const (
	mcpRemoteTransportStreamableHTTP = "streamable-http"
	mcpRemoteCacheVersion            = 1
	mcpRemoteCacheMaxAge             = 24 * time.Hour
	mcpRemoteToolsListMaxPages       = 128
	mcpRemoteSSEIdleTimeout          = 30 * time.Second
	mcpRemoteTraceBodyLimit          = 8 << 20
	mcpRemoteCompactDescriptionLimit = 120
	mcpRemoteServerAnnotation        = "toolmux.remote_mcp.server"
	mcpRemoteCredentialProvider      = "mcp_remote"
)

var (
	mcpRemoteNamePattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	mcpRemoteMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
)

type mcpRemoteServer struct {
	URL              string         `json:"url" yaml:"url"`
	Transport        string         `json:"transport,omitempty" yaml:"transport,omitempty"`
	AuthRequired     *bool          `json:"auth_required,omitempty" yaml:"auth_required,omitempty"`
	DefaultArguments map[string]any `json:"default_arguments,omitempty" yaml:"default_arguments,omitempty"`
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
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description,omitempty"`
	InputSchema  map[string]any   `json:"inputSchema,omitempty"`
	OutputSchema map[string]any   `json:"outputSchema,omitempty"`
	Annotations  map[string]any   `json:"annotations,omitempty"`
	Icons        []map[string]any `json:"icons,omitempty"`
	Execution    map[string]any   `json:"execution,omitempty"`
	Meta         map[string]any   `json:"_meta,omitempty"`
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

type mcpRemoteSSEStreamItem struct {
	Message []byte
	Err     error
}

type mcpRemoteMissingRequiredArgumentsError struct {
	Names []string
}

func (err mcpRemoteMissingRequiredArgumentsError) Error() string {
	return "missing required MCP tool arguments: " + strings.Join(err.Names, ", ")
}

type mcpRemoteCatalogEntry struct {
	Name                    string                         `json:"name" yaml:"name"`
	Status                  string                         `json:"status" yaml:"status"`
	Registered              bool                           `json:"registered" yaml:"registered"`
	RegisteredNames         []string                       `json:"registered_names,omitempty" yaml:"registered_names,omitempty"`
	Scope                   string                         `json:"scope,omitempty" yaml:"scope,omitempty"`
	Scopes                  []string                       `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path                    string                         `json:"path,omitempty" yaml:"path,omitempty"`
	Tools                   *int                           `json:"tools,omitempty" yaml:"tools,omitempty"`
	URL                     string                         `json:"url" yaml:"url"`
	Transport               string                         `json:"transport" yaml:"transport"`
	DefaultArgumentHints    []mcpRemoteDefaultArgumentHint `json:"default_argument_hints,omitempty" yaml:"default_argument_hints,omitempty"`
	MissingDefaultArguments []string                       `json:"missing_default_arguments,omitempty" yaml:"missing_default_arguments,omitempty"`
	Reason                  string                         `json:"reason,omitempty" yaml:"reason,omitempty"`
}

type mcpRemoteCatalogEnable struct {
	CatalogName    string
	RegisteredName string
}

type mcpRemoteCatalogDefinition struct {
	Server               mcpRemoteServer
	DefaultArgumentHints []mcpRemoteDefaultArgumentHint
}

type mcpRemoteDefaultArgumentHint struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Example     string `json:"example,omitempty" yaml:"example,omitempty"`
}

type mcpRemoteDefaultArgumentItem struct {
	Name  string `json:"name" yaml:"name"`
	Value any    `json:"value" yaml:"value"`
}

type mcpRemoteDefaultArgumentsResult struct {
	Server    string                         `json:"server" yaml:"server"`
	Scope     string                         `json:"scope" yaml:"scope"`
	Path      string                         `json:"path" yaml:"path"`
	Arguments []mcpRemoteDefaultArgumentItem `json:"arguments" yaml:"arguments"`
}

func toolboxAddCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var nameFlag string
	var transport string
	var noSync bool
	var verboseHTTP bool
	var native nativeToolboxAddOptions
	cmd := &cobra.Command{
		Use:   "add <toolbox-or-url>",
		Short: "Add a toolbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])
			if handled, err := addNativeToolbox(cmd, opts, target, native, args); handled || err != nil {
				return err
			}
			return addMCPRemoteToolbox(cmd, opts, target, mcpRemoteToolboxAddOptions{
				Scope:       scope,
				Name:        nameFlag,
				Transport:   transport,
				NoSync:      noSync,
				VerboseHTTP: verboseHTTP,
			}, args)
		},
	}
	cmd.Flags().StringVar(&nameFlag, "name", "", "registered toolbox name")
	cmd.Flags().StringVar(&transport, "transport", "", "remote MCP transport: streamable-http")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "register without immediately syncing tools")
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote MCP HTTP requests and responses to stderr")
	addNativeToolboxAddFlags(cmd, &native)
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

type mcpRemoteToolboxAddOptions struct {
	Scope       mcpProfileScopeOptions
	Name        string
	Transport   string
	NoSync      bool
	VerboseHTTP bool
}

type nativeToolboxAddOptions struct {
	Account         string
	Auth            string
	Token           string
	TokenEnv        string
	TokenFile       string
	Cookie          string
	CookieEnv       string
	CookieFile      string
	TeamID          string
	Workspace       string
	ClientID        string
	ClientSecret    string
	ClientSecretEnv string
	AuthURL         string
	TokenURL        string
	Scopes          []string
	UserScopes      []string
	TokenSource     string
	RedirectPort    int
	TimeoutSeconds  int
}

func addNativeToolboxAddFlags(cmd *cobra.Command, opts *nativeToolboxAddOptions) {
	cmd.Flags().StringVar(&opts.Account, "account", "default", "native provider account name")
	cmd.Flags().StringVar(&opts.Auth, "auth", "", "native provider auth mode: broker, oauth, token, or token-cookie")
	cmd.Flags().StringVar(&opts.Token, "token", "", "provider access token to store")
	cmd.Flags().StringVar(&opts.TokenEnv, "token-env", "", "environment variable containing the provider access token")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "file containing the provider access token")
	cmd.Flags().StringVar(&opts.Cookie, "cookie", "", "provider cookie header to store with the token")
	cmd.Flags().StringVar(&opts.CookieEnv, "cookie-env", "", "environment variable containing the provider cookie header")
	cmd.Flags().StringVar(&opts.CookieFile, "cookie-file", "", "file containing the provider cookie header")
	cmd.Flags().StringVar(&opts.TeamID, "team-id", "", "provider team or workspace ID to store as metadata")
	cmd.Flags().StringVar(&opts.Workspace, "workspace", "", "provider workspace name to store as metadata")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", "", "OAuth client ID")
	cmd.Flags().StringVar(&opts.ClientSecret, "client-secret", "", "OAuth client secret")
	cmd.Flags().StringVar(&opts.ClientSecretEnv, "client-secret-env", "", "environment variable containing the OAuth client secret")
	cmd.Flags().StringVar(&opts.AuthURL, "auth-url", "", "OAuth authorization endpoint override")
	cmd.Flags().StringVar(&opts.TokenURL, "token-url", "", "OAuth token endpoint override")
	cmd.Flags().StringSliceVar(&opts.Scopes, "scope", nil, "OAuth scopes to request; comma-separated or repeatable")
	cmd.Flags().StringSliceVar(&opts.UserScopes, "user-scope", nil, "OAuth user scopes to request; comma-separated or repeatable")
	cmd.Flags().StringVar(&opts.TokenSource, "token-source", "auto", "OAuth token source to store: auto, bot, or user")
	cmd.Flags().IntVar(&opts.RedirectPort, "redirect-port", 0, "loopback OAuth callback port, or 0 for a random port")
	cmd.Flags().IntVar(&opts.TimeoutSeconds, "timeout-seconds", 120, "seconds to wait for OAuth completion")
}

func addNativeToolbox(cmd *cobra.Command, opts *options, target string, native nativeToolboxAddOptions, args []string) (bool, error) {
	provider, ok := providers.Lookup(target)
	if !ok || provider.AddHandler == nil {
		return false, nil
	}
	if err := authorize(cmd, opts, toolboxAddSpec(), args); err != nil {
		return true, err
	}
	store, err := opts.credentials()
	if err != nil {
		return true, err
	}
	execCtx := actionExecutionContext(commandContext(cmd), opts, store, provider)
	execCtx.Interactive = interactiveCommand(cmd, opts)
	if execCtx.OpenBrowser == nil && execCtx.Interactive {
		execCtx.OpenBrowser = openURL
	}
	result, err := provider.AddHandler(execCtx, actions.Invocation{
		Spec:  toolboxAddSpec(),
		Args:  append([]string(nil), args...),
		Flags: nativeToolboxAddFlagValues(native),
	})
	if err != nil {
		return true, err
	}
	return true, writeActionResult(cmd, opts, execCtx, result)
}

func addMCPRemoteToolbox(cmd *cobra.Command, opts *options, target string, add mcpRemoteToolboxAddOptions, args []string) error {
	name, server, err := resolveToolboxAddTarget(target, add.Name, add.Transport)
	if err != nil {
		return err
	}
	configPath, scopeName, err := mcpProfileWritePath(add.Scope)
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
	if err := authorize(cmd, opts, toolboxAddSpec(), args); err != nil {
		return err
	}
	server = normalizeMCPRemoteServer(server)
	register := func() error {
		config.Version = 1
		config.MCP.Servers[name] = server
		if err := writeToolmuxConfigFile(configPath, config); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "registered %s toolbox %s in %s\n", scopeName, name, configPath)
		writeMCPRemoteDefaultArgumentSuggestions(cmd.OutOrStdout(), name, server)
		return nil
	}
	if add.NoSync {
		return register()
	}
	entry := mcpRemoteServerEntry{
		Name:   name,
		Scope:  scopeName,
		Scopes: []string{scopeName},
		Path:   configPath,
		Server: server,
	}
	trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), add.VerboseHTTP)
	cache, authRequired, err := syncMCPRemoteCacheAfterAdd(cmd, opts, entry, args, trace)
	if err != nil {
		return fmt.Errorf("initial sync failed for MCP server %s: %w", name, err)
	}
	server.AuthRequired = new(authRequired)
	if err := register(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "synced toolbox %s: %d tools\n", name, len(cache.Tools))
	return nil
}

func nativeToolboxAddFlagValues(opts nativeToolboxAddOptions) map[string]any {
	return map[string]any{
		"account":           opts.Account,
		"auth":              opts.Auth,
		"token":             opts.Token,
		"token-env":         opts.TokenEnv,
		"token-file":        opts.TokenFile,
		"cookie":            opts.Cookie,
		"cookie-env":        opts.CookieEnv,
		"cookie-file":       opts.CookieFile,
		"team-id":           opts.TeamID,
		"workspace":         opts.Workspace,
		"client-id":         opts.ClientID,
		"client-secret":     opts.ClientSecret,
		"client-secret-env": opts.ClientSecretEnv,
		"auth-url":          opts.AuthURL,
		"token-url":         opts.TokenURL,
		"scope":             append([]string(nil), opts.Scopes...),
		"user-scope":        append([]string(nil), opts.UserScopes...),
		"token-source":      opts.TokenSource,
		"redirect-port":     opts.RedirectPort,
		"timeout-seconds":   opts.TimeoutSeconds,
	}
}

func resolveToolboxAddTarget(target, nameFlag, transportFlag string) (string, mcpRemoteServer, error) {
	if strings.TrimSpace(target) == "" {
		return "", mcpRemoteServer{}, fmt.Errorf("toolbox name or URL is required")
	}
	if isMCPRemoteURLArgument(target) {
		name, err := cleanToolboxAddName(nameFlag, target)
		if err != nil {
			return "", mcpRemoteServer{}, err
		}
		transport := strings.TrimSpace(transportFlag)
		if transport == "" {
			transport = mcpRemoteTransportStreamableHTTP
		}
		if err := validateMCPRemoteURL(target); err != nil {
			return "", mcpRemoteServer{}, err
		}
		if err := validateMCPRemoteTransport(transport); err != nil {
			return "", mcpRemoteServer{}, err
		}
		return name, mcpRemoteServer{URL: strings.TrimSpace(target), Transport: transport}, nil
	}
	catalogName, err := cleanMCPRemoteName(target)
	if err != nil {
		return "", mcpRemoteServer{}, err
	}
	builtin, ok := mcpBuiltinRemoteServers()[catalogName]
	if !ok {
		return "", mcpRemoteServer{}, fmt.Errorf("unknown toolbox %q; pass an MCP URL or a known catalog name", catalogName)
	}
	name := catalogName
	if strings.TrimSpace(nameFlag) != "" {
		name, err = cleanMCPRemoteName(nameFlag)
		if err != nil {
			return "", mcpRemoteServer{}, err
		}
	}
	transport := strings.TrimSpace(transportFlag)
	if transport == "" {
		transport = builtin.Transport
	}
	if transport == "" {
		transport = mcpRemoteTransportStreamableHTTP
	}
	if err := validateMCPRemoteTransport(transport); err != nil {
		return "", mcpRemoteServer{}, err
	}
	builtin.Transport = transport
	if err := validateMCPRemoteURL(builtin.URL); err != nil {
		return "", mcpRemoteServer{}, err
	}
	return name, builtin, nil
}

func cleanToolboxAddName(nameFlag, rawURL string) (string, error) {
	if strings.TrimSpace(nameFlag) != "" {
		return cleanMCPRemoteName(nameFlag)
	}
	name, err := defaultMCPRemoteNameFromURL(rawURL)
	if err != nil {
		return "", err
	}
	return cleanMCPRemoteName(name)
}

func mcpRemoteSyncCommand(opts *options) *cobra.Command {
	var verboseHTTP bool
	cmd := &cobra.Command{
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
			trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), verboseHTTP)
			cache, authRequired, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, args, trace)
			if err != nil {
				return err
			}
			if err := writeMCPRemoteAuthRequired(entry, authRequired); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced MCP server %s: %d tools\n", name, len(cache.Tools))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote MCP HTTP requests and responses to stderr")
	return cmd
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

func toolboxRemoveCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var nativeAccount string
	cmd := &cobra.Command{
		Use:     "remove <toolbox> [toolbox...]",
		Aliases: []string{"rm"},
		Short:   "Remove a toolbox",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if handled, err := removeNativeToolboxes(cmd, opts, args, nativeAccount); handled || err != nil {
				return err
			}
			names, err := cleanMCPRemoteNames(args)
			if err != nil {
				return err
			}
			removals, err := planMCPRemoteRemovals(names, scope)
			if err != nil {
				return err
			}
			if err := authorize(cmd, opts, toolboxRemoveSpec(), args); err != nil {
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
						return fmt.Errorf("remove stored auth for toolbox %s: %w", name, err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "removed toolbox %s from %s\n", name, removal.Path)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&nativeAccount, "account", "default", "native provider account name")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func removeNativeToolboxes(cmd *cobra.Command, opts *options, args []string, account string) (bool, error) {
	var native []providers.Provider
	for _, arg := range args {
		provider, ok := providers.Lookup(arg)
		if !ok || provider.RemoveHandler == nil {
			continue
		}
		native = append(native, provider)
	}
	if len(native) == 0 {
		return false, nil
	}
	if len(native) != len(args) {
		return true, fmt.Errorf("cannot remove native and remote toolboxes in one command")
	}
	if err := authorize(cmd, opts, toolboxRemoveSpec(), args); err != nil {
		return true, err
	}
	store, err := opts.credentials()
	if err != nil {
		return true, err
	}
	for i, provider := range native {
		execCtx := actionExecutionContext(commandContext(cmd), opts, store, provider)
		result, err := provider.RemoveHandler(execCtx, actions.Invocation{
			Spec: toolboxRemoveSpec(),
			Args: []string{args[i]},
			Flags: map[string]any{
				"account": account,
			},
		})
		if err != nil {
			return true, err
		}
		if err := writeActionResult(cmd, opts, execCtx, result); err != nil {
			return true, err
		}
	}
	return true, nil
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
			if err := writeMCPRemoteAuthRequired(entry, true); err != nil {
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
	var fullDescriptions bool
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

func mcpRemoteDefaultsCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "defaults",
		Aliases: []string{"default-args"},
		Short:   "Manage default arguments for remote MCP tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown mcp defaults command %q", args[0])
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(mcpRemoteDefaultsListCommand(opts))
	cmd.AddCommand(mcpRemoteDefaultsSetCommand(opts))
	cmd.AddCommand(mcpRemoteDefaultsRemoveCommand(opts))
	return cmd
}

func mcpRemoteDefaultsListCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <name>",
		Aliases: []string{"list", "show"},
		Short:   "List default arguments for a remote MCP server",
		Args:    cobra.ExactArgs(1),
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
			if err := authorize(cmd, opts, mcpRemoteDefaultsListSpec(), args); err != nil {
				return err
			}
			result := mcpRemoteDefaultArgumentsResult{
				Server:    entry.Name,
				Scope:     mcpRemoteScopeLabel(entry.Scope),
				Path:      entry.Path,
				Arguments: mcpRemoteDefaultArgumentItems(entry.Server.DefaultArguments),
			}
			return writeValue(cmd, opts, result, func(w io.Writer) {
				renderMCPRemoteDefaultsTable(w, cmd, opts, result)
			})
		},
	}
}

func mcpRemoteDefaultsSetCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var jsonValue bool
	cmd := &cobra.Command{
		Use:   "set <name> <argument> <value>",
		Short: "Set a default argument for a remote MCP server",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			key, err := cleanMCPRemoteDefaultArgumentName(args[1])
			if err != nil {
				return err
			}
			value, err := parseMCPRemoteDefaultArgumentValue(args[2], jsonValue)
			if err != nil {
				return err
			}
			target, err := mcpRemoteDefaultArgumentsWriteTargetForScope(name, scope, true)
			if err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpRemoteDefaultsSetSpec(), args); err != nil {
				return err
			}
			if target.Server.DefaultArguments == nil {
				target.Server.DefaultArguments = map[string]any{}
			}
			target.Server.DefaultArguments[key] = value
			target.Config.MCP.Servers[name] = target.Server
			if err := writeToolmuxConfigFile(target.Path, target.Config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set default argument %s for MCP server %s in %s\n", key, name, target.Path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonValue, "json", false, "parse value as JSON instead of a string")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func mcpRemoteDefaultsRemoveCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:     "remove <name> <argument> [argument...]",
		Aliases: []string{"rm", "unset"},
		Short:   "Remove default arguments for a remote MCP server",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			keys := make([]string, 0, len(args)-1)
			for _, arg := range args[1:] {
				key, err := cleanMCPRemoteDefaultArgumentName(arg)
				if err != nil {
					return err
				}
				keys = append(keys, key)
			}
			target, err := mcpRemoteDefaultArgumentsWriteTargetForScope(name, scope, false)
			if err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpRemoteDefaultsRemoveSpec(), args); err != nil {
				return err
			}
			for _, key := range keys {
				delete(target.Server.DefaultArguments, key)
			}
			if len(target.Server.DefaultArguments) == 0 {
				target.Server.DefaultArguments = nil
			}
			target.Config.MCP.Servers[name] = target.Server
			if err := writeToolmuxConfigFile(target.Path, target.Config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed default arguments for MCP server %s in %s\n", name, target.Path)
			return nil
		},
	}
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

type mcpRemoteDefaultArgumentsWriteTarget struct {
	Path   string
	Scope  string
	Config toolmuxConfigFile
	Server mcpRemoteServer
}

func mcpRemoteDefaultArgumentsWriteTargetForScope(name string, scope mcpProfileScopeOptions, createFromEffective bool) (mcpRemoteDefaultArgumentsWriteTarget, error) {
	configPath, scopeName, err := mcpProfileWritePath(scope)
	if err != nil {
		return mcpRemoteDefaultArgumentsWriteTarget{}, err
	}
	config, err := readToolmuxConfigFile(configPath)
	if err != nil && (!createFromEffective || !errors.Is(err, os.ErrNotExist)) {
		return mcpRemoteDefaultArgumentsWriteTarget{}, err
	}
	if config.MCP.Servers == nil {
		config.MCP.Servers = map[string]mcpRemoteServer{}
	}
	server, exists := config.MCP.Servers[name]
	if !exists {
		if !createFromEffective {
			return mcpRemoteDefaultArgumentsWriteTarget{}, fmt.Errorf("MCP server %q is not registered in %s", name, configPath)
		}
		entry, ok, err := lookupMCPRemoteServer(name, "")
		if err != nil {
			return mcpRemoteDefaultArgumentsWriteTarget{}, err
		}
		if !ok {
			return mcpRemoteDefaultArgumentsWriteTarget{}, fmt.Errorf("MCP server %q is not registered", name)
		}
		server = entry.Server
	}
	return mcpRemoteDefaultArgumentsWriteTarget{
		Path:   configPath,
		Scope:  scopeName,
		Config: config,
		Server: server,
	}, nil
}

func cleanMCPRemoteDefaultArgumentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("default argument name is required")
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("invalid default argument name %q", name)
	}
	return name, nil
}

func parseMCPRemoteDefaultArgumentValue(raw string, jsonValue bool) (any, error) {
	if !jsonValue {
		return raw, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("default argument value must be valid JSON: %w", err)
	}
	return value, nil
}

func mcpRemoteDefaultArgumentItems(arguments map[string]any) []mcpRemoteDefaultArgumentItem {
	names := make([]string, 0, len(arguments))
	for name := range arguments {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]mcpRemoteDefaultArgumentItem, 0, len(names))
	for _, name := range names {
		items = append(items, mcpRemoteDefaultArgumentItem{Name: name, Value: arguments[name]})
	}
	return items
}

func renderMCPRemoteDefaultsTable(w io.Writer, cmd *cobra.Command, opts *options, result mcpRemoteDefaultArgumentsResult) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(result.Arguments))
	for _, item := range result.Arguments {
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, item.Name),
			output.Value(mcpRemoteFormatDefaultArgumentValue(item.Value)),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Argument", "Value"},
		Rows:    rows,
		Empty:   "no default arguments for " + result.Server,
	})
}

func mcpRemoteFormatDefaultArgumentValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func writeMCPRemoteDefaultArgumentSuggestions(w io.Writer, serverName string, server mcpRemoteServer) {
	for _, hint := range mcpRemoteMissingDefaultArgumentHints(mcpRemoteServerEntry{Name: serverName, Server: server}) {
		example := firstNonEmpty(hint.Example, "<value>")
		fmt.Fprintf(w, "hint: set a default %s with `toolmux mcp defaults set %s %s %s`\n", hint.Name, serverName, hint.Name, example)
	}
}

func mcpRemoteDefaultArgumentErrorWithHint(err error, entry mcpRemoteServerEntry) error {
	var missing mcpRemoteMissingRequiredArgumentsError
	if !errors.As(err, &missing) {
		return err
	}
	missingSet := map[string]bool{}
	for _, name := range missing.Names {
		missingSet[name] = true
	}
	for _, hint := range mcpRemoteMissingDefaultArgumentHints(entry) {
		if !missingSet[hint.Name] {
			continue
		}
		example := firstNonEmpty(hint.Example, "<value>")
		return fmt.Errorf("%w\nhint: set a default %s with `toolmux mcp defaults set %s %s %s`", err, hint.Name, entry.Name, hint.Name, example)
	}
	return err
}

func mcpRemoteMissingDefaultArgumentHints(entry mcpRemoteServerEntry) []mcpRemoteDefaultArgumentHint {
	_, definition, ok := mcpRemoteCatalogDefinitionForServer(entry.Name, entry.Server)
	if !ok {
		return nil
	}
	var missing []mcpRemoteDefaultArgumentHint
	for _, hint := range definition.DefaultArgumentHints {
		if strings.TrimSpace(hint.Name) == "" {
			continue
		}
		if _, exists := entry.Server.DefaultArguments[hint.Name]; exists {
			continue
		}
		missing = append(missing, hint)
	}
	return missing
}

func mcpRemoteMissingDefaultArgumentNames(entry mcpRemoteServerEntry, hints []mcpRemoteDefaultArgumentHint) []string {
	var missing []string
	for _, hint := range hints {
		if strings.TrimSpace(hint.Name) == "" {
			continue
		}
		if _, exists := entry.Server.DefaultArguments[hint.Name]; exists {
			continue
		}
		missing = append(missing, hint.Name)
	}
	sort.Strings(missing)
	return missing
}

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

func renderMCPRemoteToolTable(w io.Writer, cmd *cobra.Command, opts *options, serverName string, tools []mcpRemoteTool, fullDescriptions bool) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(tools))
	for _, tool := range tools {
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, tool.Name),
			output.ToneText(human, output.ToneMuted, mcpRemoteToolArgumentsLabel(tool)),
			output.Value(mcpRemoteDisplayDescription(cmd, opts, tool.Description, fullDescriptions)),
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

func renderMCPRemoteTree(w io.Writer, cmd *cobra.Command, opts *options, items []mcpRemoteTreeItem, fullDescriptions bool) {
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
				description = " " + output.ToneText(human, output.ToneMuted, "- "+mcpRemoteDisplayDescription(cmd, opts, tool.Description, fullDescriptions))
			}
			fmt.Fprintf(w, "%s %s%s%s\n", connector, output.ToneText(human, output.ToneInfo, tool.Name), args, description)
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

func mcpRemoteDisplayDescription(cmd *cobra.Command, opts *options, description string, full bool) string {
	if !full && mcpRemoteCompactDescriptions(cmd, opts) {
		return mcpRemoteCompactDescription(description)
	}
	return description
}

func mcpRemoteCompactDescriptions(cmd *cobra.Command, opts *options) bool {
	return opts != nil && interactiveCommand(cmd, opts)
}

func mcpRemoteCompactDescription(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}
	lines := strings.Split(description, "\n")
	for index, line := range lines {
		if mcpRemoteDescriptionHeading(line) {
			continue
		}
		line = mcpRemoteCleanDescriptionLine(line)
		if line == "" {
			continue
		}
		if mcpRemoteGenericDescriptionLine(line) {
			continue
		}
		if strings.HasSuffix(line, ":") {
			if combined := mcpRemoteCompactColonDescription(line, lines[index+1:]); combined != "" {
				return truncateMCPRemoteDescription(combined, mcpRemoteCompactDescriptionLimit)
			}
			line = strings.TrimSpace(strings.TrimSuffix(line, ":"))
			if mcpRemoteIncompleteDescriptionLine(line) {
				continue
			}
		}
		return truncateMCPRemoteDescription(line, mcpRemoteCompactDescriptionLimit)
	}
	return truncateMCPRemoteDescription(strings.Join(strings.Fields(description), " "), mcpRemoteCompactDescriptionLimit)
}

func mcpRemoteDescriptionHeading(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "#")
}

func mcpRemoteCleanDescriptionLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
		return ""
	}
	for strings.HasPrefix(line, ">") {
		line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
	}
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	line = mcpRemoteStripListMarker(line)
	line = strings.TrimSpace(line)
	for _, checkbox := range []string{"[ ] ", "[x] ", "[X] "} {
		line = strings.TrimPrefix(line, checkbox)
	}
	line = mcpRemoteMarkdownLinkPattern.ReplaceAllString(line, "$1")
	replacer := strings.NewReplacer("`", "", "**", "", "__", "")
	line = replacer.Replace(line)
	return strings.Join(strings.Fields(line), " ")
}

func mcpRemoteStripListMarker(line string) string {
	line = strings.TrimSpace(line)
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && (line[1] == ' ' || line[1] == '\t') {
		return strings.TrimSpace(line[1:])
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			if i > 0 && (r == '.' || r == ')') && len(line) > i+1 && (line[i+1] == ' ' || line[i+1] == '\t') {
				return strings.TrimSpace(line[i+1:])
			}
			break
		}
	}
	return line
}

func mcpRemoteDescriptionListItem(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && (line[1] == ' ' || line[1] == '\t') {
		return true
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			return i > 0 && (r == '.' || r == ')') && len(line) > i+1 && (line[i+1] == ' ' || line[i+1] == '\t')
		}
	}
	return false
}

func mcpRemoteGenericDescriptionLine(line string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(line)), ":. ")
	switch normalized {
	case "overview", "summary", "description", "details", "usage", "examples", "example", "notes", "note", "instructions":
		return true
	default:
		return false
	}
}

func mcpRemoteIncompleteDescriptionLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return true
	}
	switch strings.ToLower(fields[len(fields)-1]) {
	case "for", "from", "in", "into", "of", "on", "over", "to", "using", "with":
		return true
	default:
		return len(fields) <= 3
	}
}

func mcpRemoteCompactColonDescription(prefix string, following []string) string {
	prefix = strings.TrimSpace(strings.TrimSuffix(prefix, ":"))
	var bullets []string
	for _, line := range following {
		if strings.TrimSpace(line) == "" {
			if len(bullets) == 0 {
				continue
			}
			break
		}
		if !mcpRemoteDescriptionListItem(line) {
			if len(bullets) == 0 && (mcpRemoteDescriptionHeading(line) || mcpRemoteGenericDescriptionLine(mcpRemoteCleanDescriptionLine(line))) {
				continue
			}
			break
		}
		bullet := mcpRemoteCleanDescriptionLine(line)
		if bullet == "" {
			continue
		}
		bullets = append(bullets, bullet)
		if len(bullets) == 3 {
			break
		}
	}
	if len(bullets) == 0 {
		return ""
	}
	description := prefix + " " + strings.Join(bullets, ", ")
	if !strings.ContainsAny(description[len(description)-1:], ".!?") {
		description += "."
	}
	return description
}

func truncateMCPRemoteDescription(description string, limit int) string {
	if limit <= 0 || len(description) <= limit {
		return description
	}
	cut := strings.LastIndexAny(description[:limit+1], " \t")
	if cut <= 0 {
		cut = limit
	}
	return strings.TrimSpace(description[:cut]) + "..."
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
				writeMCPRemoteDefaultArgumentSuggestions(cmd.OutOrStdout(), entry.Name, entry.Server)
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
			cache, authRequired, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, []string{entry.Name}, nil)
			if err != nil {
				return err
			}
			if err := writeMCPRemoteAuthRequired(entry, authRequired); err != nil {
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
	catalog := mcpBuiltinRemoteCatalog()
	servers := make(map[string]mcpRemoteServer, len(catalog))
	for name, definition := range catalog {
		servers[name] = definition.Server
	}
	return servers
}

func mcpBuiltinRemoteCatalog() map[string]mcpRemoteCatalogDefinition {
	return map[string]mcpRemoteCatalogDefinition{
		"atlassian": {
			Server: mcpRemoteServer{URL: "https://mcp.atlassian.com/v1/mcp/authv2", Transport: mcpRemoteTransportStreamableHTTP},
			DefaultArgumentHints: []mcpRemoteDefaultArgumentHint{{
				Name:        "cloudId",
				Description: "Atlassian Cloud site ID used by many Atlassian MCP tools.",
				Example:     "<cloud-id>",
			}},
		},
		"cloudflare": {Server: mcpRemoteServer{URL: "https://mcp.cloudflare.com/mcp", Transport: mcpRemoteTransportStreamableHTTP}},
		"grafana":    {Server: mcpRemoteServer{URL: "https://mcp.grafana.com/mcp", Transport: mcpRemoteTransportStreamableHTTP}},
		"iterate":    {Server: mcpRemoteServer{URL: "https://mock.iterate.com/no-auth", Transport: mcpRemoteTransportStreamableHTTP}},
		"linear":     {Server: mcpRemoteServer{URL: "https://mcp.linear.app/mcp", Transport: mcpRemoteTransportStreamableHTTP}},
		"miro":       {Server: mcpRemoteServer{URL: "https://mcp.miro.com/", Transport: mcpRemoteTransportStreamableHTTP}},
		"notion":     {Server: mcpRemoteServer{URL: "https://mcp.notion.com/mcp", Transport: mcpRemoteTransportStreamableHTTP}},
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
	server, exists := config.MCP.Servers[entry.Name]
	if !exists {
		return fmt.Errorf("MCP server %q is not registered in %s", entry.Name, entry.Path)
	}
	server.AuthRequired = new(required)
	config.MCP.Servers[entry.Name] = server
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

func syncMCPRemoteCacheExplicit(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, args []string, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, bool, error) {
	if err := authorize(cmd, opts, mcpRemoteSyncSpec(), args); err != nil {
		return mcpRemoteCache{}, false, err
	}
	token, err := loadMCPRemoteAccessToken(commandContext(cmd), opts, entry)
	if err != nil {
		return mcpRemoteCache{}, false, err
	}
	cache, err := syncMCPRemoteServer(commandContext(cmd), opts.httpClient, entry, token, trace)
	if err != nil {
		return mcpRemoteCache{}, false, err
	}
	if err := writeMCPRemoteCache(opts.mcpCacheDir, entry.Name, cache); err != nil {
		return mcpRemoteCache{}, false, err
	}
	return cache, strings.TrimSpace(token) != "", nil
}

func syncMCPRemoteCacheAfterAdd(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, args []string, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, bool, error) {
	cache, authRequired, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, args, trace)
	if err == nil {
		return cache, authRequired, nil
	}
	if !mcpRemoteErrorStatus(err, http.StatusUnauthorized) {
		return mcpRemoteCache{}, false, err
	}
	if _, ok, loadErr := loadMCPRemoteStoredTokens(commandContext(cmd), opts, entry.Name); loadErr != nil {
		return mcpRemoteCache{}, false, loadErr
	} else if ok {
		return mcpRemoteCache{}, true, err
	}
	if authErr := authorize(cmd, opts, mcpRemoteAuthLoginSpec(), []string{entry.Name}); authErr != nil {
		return mcpRemoteCache{}, true, fmt.Errorf("%s; OAuth login was denied: %w", err.Error(), authErr)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "MCP server %s requires auth; starting OAuth login\n", entry.Name)
	tokens, loginErr := loginMCPRemoteOAuth(cmd, opts, entry, mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute})
	if loginErr != nil {
		return mcpRemoteCache{}, true, fmt.Errorf("%s; OAuth login failed: %w", err.Error(), loginErr)
	}
	store, storeErr := opts.credentials()
	if storeErr != nil {
		return mcpRemoteCache{}, true, storeErr
	}
	if saveErr := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, entry.Name), tokens); saveErr != nil {
		return mcpRemoteCache{}, true, saveErr
	}
	fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for MCP server %s\n", entry.Name)
	cache, _, err = syncMCPRemoteCacheExplicit(cmd, opts, entry, args, trace)
	return cache, true, err
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
	tools, toolsResult, err := listMCPRemoteTools(ctx, client, entry.Server, bearerToken, sessionID, trace)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	for i := range tools {
		tools[i].Name = strings.TrimSpace(tools[i].Name)
		if tools[i].InputSchema == nil {
			tools[i].InputSchema = map[string]any{"type": "object"}
		}
	}
	tools = slices.DeleteFunc(tools, func(tool mcpRemoteTool) bool {
		return tool.Name == ""
	})
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	fingerprint := mcpRemoteToolsFingerprint(tools)
	return mcpRemoteCache{
		Version:         mcpRemoteCacheVersion,
		Name:            entry.Name,
		URL:             entry.Server.URL,
		Transport:       entry.Server.Transport,
		ProtocolVersion: init.ProtocolVersion,
		ServerInfo:      init.ServerInfo,
		Tools:           tools,
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

func listMCPRemoteTools(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID string, trace *mcpRemoteHTTPTrace) ([]mcpRemoteTool, json.RawMessage, error) {
	var tools []mcpRemoteTool
	var pages []json.RawMessage
	var cursor *string
	for range mcpRemoteToolsListMaxPages {
		var params any
		if cursor != nil {
			params = map[string]any{"cursor": *cursor}
		}
		result, responseSessionID, err := callMCPRemote(ctx, client, server, bearerToken, sessionID, "tools/list", params, trace)
		if err != nil {
			return nil, nil, err
		}
		if responseSessionID != "" {
			sessionID = responseSessionID
		}
		var decoded struct {
			Tools      *[]mcpRemoteTool `json:"tools"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &decoded); err != nil {
			return nil, nil, fmt.Errorf("decode remote MCP tools/list: %w", err)
		}
		if decoded.Tools == nil {
			return nil, nil, fmt.Errorf("remote MCP tools/list returned no tools array")
		}
		tools = append(tools, (*decoded.Tools)...)
		pages = append(pages, result)
		if decoded.NextCursor == nil {
			return tools, aggregateMCPRemoteToolsListResult(tools, pages), nil
		}
		cursor = decoded.NextCursor
	}
	return nil, nil, fmt.Errorf("remote MCP tools/list exceeded %d pages", mcpRemoteToolsListMaxPages)
}

func aggregateMCPRemoteToolsListResult(tools []mcpRemoteTool, pages []json.RawMessage) json.RawMessage {
	if len(pages) == 1 {
		return pages[0]
	}
	result := struct {
		Tools []mcpRemoteTool   `json:"tools"`
		Pages []json.RawMessage `json:"pages,omitempty"`
	}{
		Tools: tools,
		Pages: pages,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return data
}

func initializeMCPRemoteSession(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken string, trace *mcpRemoteHTTPTrace) (json.RawMessage, string, error) {
	initResult, sessionID, err := callMCPRemote(ctx, client, server, bearerToken, "", "initialize", mcpRemoteInitializeParams(), trace)
	if err != nil {
		return nil, "", err
	}
	if err := notifyMCPRemoteInitialized(ctx, client, server, bearerToken, sessionID, trace); err != nil {
		return nil, "", err
	}
	return initResult, sessionID, nil
}

func mcpRemoteInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": mcpRemoteClientProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":        "toolmux",
			"title":       "Toolmux",
			"version":     version.Version,
			"description": "Policy-aware local CLI and agent bridge for SaaS tools and MCP servers.",
			"websiteUrl":  "https://github.com/fiam/toolmux",
			"icons": []map[string]any{{
				"src":      "https://raw.githubusercontent.com/fiam/toolmux/main/docs/assets/toolmux-icon.png",
				"mimeType": "image/png",
				"sizes":    []string{"1254x1254"},
			}},
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
	requestID := json.RawMessage("1")
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      requestID,
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
	result, err := decodeMCPRemoteResponse(resp, method, requestID)
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
	req.Header.Set("Mcp-Protocol-Version", mcpRemoteClientProtocolVersion)
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

func decodeMCPRemoteResponse(resp *http.Response, method string, expectedID json.RawMessage) (json.RawMessage, error) {
	var decoded mcpResponse
	if mcpRemoteResponseIsSSE(resp.Header.Get("Content-Type")) {
		eventData, err := readMCPRemoteSSEResponse(resp.Body, expectedID)
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

func readMCPRemoteSSEResponse(reader io.Reader, expectedID json.RawMessage, idleTimeouts ...time.Duration) ([]byte, error) {
	idleTimeout := mcpRemoteSSEIdleTimeout
	if len(idleTimeouts) > 0 && idleTimeouts[0] > 0 {
		idleTimeout = idleTimeouts[0]
	}
	done := make(chan struct{})
	defer close(done)
	activity := make(chan struct{}, 1)
	messages := make(chan mcpRemoteSSEStreamItem, 4)
	go scanMCPRemoteSSEMessages(reader, done, activity, messages)

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idleTimeout)
	}

	for {
		select {
		case <-activity:
			resetTimer()
		case item, ok := <-messages:
			resetTimer()
			if !ok {
				return nil, fmt.Errorf("no response message event found")
			}
			if item.Err != nil {
				return nil, item.Err
			}
			matches, err := mcpRemoteSSEMessageIsResponse(item.Message, expectedID)
			if err != nil {
				return nil, err
			}
			if matches {
				return item.Message, nil
			}
		case <-timer.C:
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
			return nil, fmt.Errorf("timed out waiting for response message after %s of inactivity", idleTimeout)
		}
	}
}

func scanMCPRemoteSSEMessages(reader io.Reader, done <-chan struct{}, activity chan<- struct{}, messages chan<- mcpRemoteSSEStreamItem) {
	defer close(messages)
	stream := bufio.NewReader(io.LimitReader(reader, 8<<20))
	eventName := ""
	var dataLines []string
	notifyActivity := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	send := func(item mcpRemoteSSEStreamItem) bool {
		select {
		case messages <- item:
			return true
		case <-done:
			return false
		}
	}
	flush := func() bool {
		if len(dataLines) == 0 || (eventName != "" && eventName != "message") {
			return true
		}
		return send(mcpRemoteSSEStreamItem{Message: []byte(strings.Join(dataLines, "\n"))})
	}
	for {
		rawLine, err := stream.ReadString('\n')
		if rawLine != "" {
			notifyActivity()
			line := strings.TrimSuffix(rawLine, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if !flush() {
					return
				}
				eventName = ""
				dataLines = nil
			} else if !strings.HasPrefix(line, ":") {
				field, value, ok := strings.Cut(line, ":")
				if ok {
					value = strings.TrimPrefix(value, " ")
					switch field {
					case "event":
						eventName = value
					case "data":
						dataLines = append(dataLines, value)
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = flush()
				return
			}
			_ = send(mcpRemoteSSEStreamItem{Err: err})
			return
		}
	}
}

func mcpRemoteSSEMessageIsResponse(message []byte, expectedID json.RawMessage) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message, &fields); err != nil {
		return false, err
	}
	id, hasID := fields["id"]
	if !hasID {
		return false, nil
	}
	if len(expectedID) > 0 && !bytes.Equal(bytes.TrimSpace(id), bytes.TrimSpace(expectedID)) {
		return false, nil
	}
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	return hasResult || hasError, nil
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
	if err := json.Unmarshal(result, &callResult); err == nil && mcpRemoteCallToolResultHasPayload(callResult) {
		return callResult, nil
	}
	return mcpTextToolResult(string(result)), nil
}

func mcpRemoteCallToolResultHasPayload(result mcpCallToolResult) bool {
	return len(result.Content) > 0 || result.StructuredContent != nil || result.IsError || len(result.Meta) > 0
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
	var fullHelp bool
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
	cmd.Flags().BoolVar(&fullHelp, "full-help", false, "show full upstream MCP tool descriptions")
	setMCPRemoteRootHelp(cmd, opts, &fullHelp)
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
	description := firstNonEmpty(tool.Description, "Call remote MCP tool "+tool.Name)
	cmd := &cobra.Command{
		Use:   tool.Name,
		Short: description,
		Long:  description,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := decodeMCPRemoteCLIArguments(cmd, rawJSON, tool, entry.Server.DefaultArguments)
			if err != nil {
				return mcpRemoteDefaultArgumentErrorWithHint(err, entry)
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
	addMCPRemoteToolFlags(cmd, tool, entry.Server.DefaultArguments)
	setMCPRemoteToolHelp(cmd, entry, tool)
	return cmd
}

func setMCPRemoteRootHelp(cmd *cobra.Command, opts *options, fullHelp *bool) {
	defaultHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(helpCmd *cobra.Command, args []string) {
		if mcpRemoteFullHelp(helpCmd, fullHelp) || !mcpRemoteCompactDescriptions(helpCmd, opts) {
			defaultHelp(helpCmd, args)
			return
		}
		renderMCPRemoteRootCompactHelp(helpCmd, opts)
	})
}

func mcpRemoteFullHelp(cmd *cobra.Command, fullHelp *bool) bool {
	if fullHelp != nil && *fullHelp {
		return true
	}
	value, err := cmd.Flags().GetBool("full-help")
	return err == nil && value
}

func renderMCPRemoteRootCompactHelp(cmd *cobra.Command, opts *options) {
	w := cmd.OutOrStdout()
	human := humanOutputOptions(cmd, opts)
	if cmd.Short != "" {
		fmt.Fprintln(w, cmd.Short)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Usage:"))
	fmt.Fprintf(w, "  %s <tool> [flags]\n", cmd.CommandPath())
	children := mcpRemoteHelpCommands(cmd)
	if len(children) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Available Commands:"))
		width := 0
		for _, child := range children {
			width = max(width, len(child.Name()))
		}
		for _, child := range children {
			name := fmt.Sprintf("%-*s", width, child.Name())
			description := output.Value(mcpRemoteCompactDescription(child.Short))
			fmt.Fprintf(w, "  %s  %s\n",
				output.ToneText(human, output.ToneInfo, name),
				output.ToneText(human, output.ToneMuted, description),
			)
		}
	}
	if flags := strings.TrimRight(cmd.LocalFlags().FlagUsages(), "\n"); flags != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Flags:"))
		fmt.Fprintln(w, flags)
	}
	if flags := strings.TrimRight(cmd.InheritedFlags().FlagUsages(), "\n"); flags != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Global Flags:"))
		fmt.Fprintln(w, flags)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Use %q for full upstream descriptions.\n", cmd.CommandPath()+" --full-help")
	fmt.Fprintf(w, "Use %q for one tool's flags and schema hint.\n", cmd.CommandPath()+" <tool> --help")
}

func mcpRemoteHelpCommands(cmd *cobra.Command) []*cobra.Command {
	children := slices.Clone(cmd.Commands())
	children = slices.DeleteFunc(children, func(child *cobra.Command) bool {
		return !child.IsAvailableCommand()
	})
	sort.Slice(children, func(i, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	return children
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

func addMCPRemoteToolFlags(cmd *cobra.Command, tool mcpRemoteTool, defaultArguments ...map[string]any) {
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	required := mcpRemoteRequiredSet(tool.InputSchema)
	if len(defaultArguments) > 0 {
		for name := range mcpRemoteDefaultArgumentsForTool(defaultArguments[0], tool.InputSchema) {
			delete(required, name)
		}
	}
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

func decodeMCPRemoteCLIArguments(cmd *cobra.Command, rawJSON string, tool mcpRemoteTool, defaultArguments map[string]any) (map[string]any, error) {
	arguments := mcpRemoteDefaultArgumentsForTool(defaultArguments, tool.InputSchema)
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
		var rawArguments map[string]any
		if err := json.Unmarshal(data, &rawArguments); err != nil {
			return nil, fmt.Errorf("--json must be a JSON object: %w", err)
		}
		if rawArguments == nil {
			return nil, fmt.Errorf("--json must be a JSON object")
		}
		maps.Copy(arguments, rawArguments)
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

func mcpRemoteDefaultArgumentsForTool(defaultArguments map[string]any, schema map[string]any) map[string]any {
	arguments := map[string]any{}
	if len(defaultArguments) == 0 {
		return arguments
	}
	properties := mcpRemoteSchemaProperties(schema)
	for name, value := range defaultArguments {
		if _, ok := properties[name]; ok {
			arguments[name] = value
		}
	}
	return arguments
}

func mcpRemoteMergeDefaultArguments(arguments map[string]any, defaultArguments map[string]any, schema map[string]any) map[string]any {
	merged := mcpRemoteDefaultArgumentsForTool(defaultArguments, schema)
	maps.Copy(merged, arguments)
	return merged
}

func mcpRemoteInputSchemaWithDefaults(schema map[string]any, defaultArguments map[string]any) map[string]any {
	effectiveDefaults := mcpRemoteDefaultArgumentsForTool(defaultArguments, schema)
	if len(effectiveDefaults) == 0 {
		return schema
	}
	cloned := cloneMCPRemoteMap(schema)
	properties := mcpRemoteSchemaProperties(cloned)
	for name, value := range effectiveDefaults {
		property, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		property["default"] = value
	}
	if required := mcpRemoteRequiredNames(cloned); len(required) > 0 {
		filtered := make([]string, 0, len(required))
		for _, name := range required {
			if _, ok := effectiveDefaults[name]; !ok {
				filtered = append(filtered, name)
			}
		}
		if len(filtered) == 0 {
			delete(cloned, "required")
		} else {
			cloned["required"] = filtered
		}
	}
	return cloned
}

func cloneMCPRemoteMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
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
	case "color", "help", "output", "pager", "policy", "profile", "read-only":
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
	return mcpRemoteMissingRequiredArgumentsError{Names: missing}
}

func mcpRemoteRequiredNames(schema map[string]any) []string {
	var required []string
	switch values := schema["required"].(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				required = append(required, value)
			}
		}
	case []any:
		for _, item := range values {
			value, ok := item.(string)
			if ok {
				if value = strings.TrimSpace(value); value != "" {
					required = append(required, value)
				}
			}
		}
	}
	return required
}

func mcpRemoteRequiredSet(schema map[string]any) map[string]bool {
	required := map[string]bool{}
	for _, value := range mcpRemoteRequiredNames(schema) {
		required[value] = true
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
		tools = append(tools, mcpRemoteToolForServeWithDefaults(spec.ID, ref.Tool, ref.Entry.Server.DefaultArguments))
	}
	return tools
}

func mcpRemoteToolForServe(name string, tool mcpRemoteTool) map[string]any {
	return mcpRemoteToolForServeWithDefaults(name, tool, nil)
}

func mcpRemoteToolForServeWithDefaults(name string, tool mcpRemoteTool, defaultArguments map[string]any) map[string]any {
	out := map[string]any{
		"name":        name,
		"description": tool.Description,
		"inputSchema": mcpRemoteInputSchemaWithDefaults(tool.InputSchema, defaultArguments),
	}
	if tool.Title != "" {
		out["title"] = tool.Title
	}
	if tool.OutputSchema != nil {
		out["outputSchema"] = tool.OutputSchema
	}
	if tool.Annotations != nil {
		out["annotations"] = tool.Annotations
	}
	if len(tool.Icons) > 0 {
		out["icons"] = tool.Icons
	}
	if tool.Execution != nil {
		out["execution"] = tool.Execution
	}
	if tool.Meta != nil {
		out["_meta"] = tool.Meta
	}
	return out
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
	if len(path) >= 2 && path[0] == "toolmux" && (path[1] == "add" || path[1] == "remove" || path[1] == "rm") {
		return true
	}
	if len(path) < 3 || path[0] != "toolmux" || path[1] != "mcp" {
		return false
	}
	switch path[2] {
	case "sync", "rename", "ls", "list", "show", "catalog", "available", "auth":
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
	return credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  mcpRemoteCredentialProvider,
		Service:   name,
		AccountID: "default",
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

func toolboxAddSpec() actions.Spec {
	return actions.Command("toolbox.add", "add",
		actions.Use("add <toolbox-or-url>"),
		actions.Short("Add a toolbox"),
		actions.RBAC("mcp_server", actions.VerbCreate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func toolboxStatusSpec() actions.Spec {
	return actions.Command("toolbox.status", "status",
		actions.Use("status [toolbox...]"),
		actions.Short("Show toolbox status"),
		actions.RBAC("toolbox", actions.VerbStatus, actions.EffectNone, actions.EffectRead),
	)
}

func doctorSpec() actions.Spec {
	return actions.Command("doctor", "doctor",
		actions.Use("doctor"),
		actions.Short("Check Toolmux setup"),
		actions.RBAC("toolbox", actions.VerbDiagnose, actions.EffectNone, actions.EffectRead),
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

func toolboxRemoveSpec() actions.Spec {
	return actions.Command("toolbox.remove", "remove",
		actions.Use("remove <toolbox> [toolbox...]"),
		actions.Short("Remove a toolbox"),
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

func mcpRemoteDefaultsListSpec() actions.Spec {
	return actions.Command("mcp.defaults.ls", "ls",
		actions.Use("mcp defaults ls <name>"),
		actions.Short("List default arguments for a remote MCP server"),
		actions.RBAC("mcp_server_defaults", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteDefaultsSetSpec() actions.Spec {
	return actions.Command("mcp.defaults.set", "set",
		actions.Use("mcp defaults set <name> <argument> <value>"),
		actions.Short("Set a default argument for a remote MCP server"),
		actions.RBAC("mcp_server_defaults", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func mcpRemoteDefaultsRemoveSpec() actions.Spec {
	return actions.Command("mcp.defaults.remove", "remove",
		actions.Use("mcp defaults remove <name> <argument> [argument...]"),
		actions.Short("Remove default arguments for a remote MCP server"),
		actions.RBAC("mcp_server_defaults", actions.VerbDelete, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
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
