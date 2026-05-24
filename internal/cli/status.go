package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

type toolboxStatusItem struct {
	Name         string     `json:"name" yaml:"name"`
	Kind         string     `json:"kind" yaml:"kind"`
	Status       string     `json:"status" yaml:"status"`
	Auth         string     `json:"auth" yaml:"auth"`
	Scope        string     `json:"scope" yaml:"scope"`
	Scopes       []string   `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	URL          string     `json:"url" yaml:"url"`
	Command      string     `json:"command,omitempty" yaml:"command,omitempty"`
	Transport    string     `json:"transport" yaml:"transport"`
	Tools        *int       `json:"tools,omitempty" yaml:"tools,omitempty"`
	SyncedAt     *time.Time `json:"synced_at,omitempty" yaml:"synced_at,omitempty"`
	AuthRequired *bool      `json:"auth_required,omitempty" yaml:"auth_required,omitempty"`
	Path         string     `json:"path" yaml:"path"`
}

func statusCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status [toolbox...]",
		Short: "Show toolbox status",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			selectedRemote, selectedNative, err := selectedToolboxEntries(opts, args)
			if err != nil {
				return err
			}
			statuses := make([]toolboxStatusItem, 0, len(selectedRemote)+len(selectedNative))
			if len(selectedRemote) > 0 || len(selectedNative) > 0 {
				store, err := opts.credentials()
				if err != nil {
					return err
				}
				for _, entry := range selectedRemote {
					status, err := readMCPRemoteToolboxStatus(commandContext(cmd), opts, store, entry)
					if err != nil {
						return err
					}
					statuses = append(statuses, status)
				}
				for _, entry := range selectedNative {
					status, err := readNativeToolboxStatus(commandContext(cmd), opts, store, entry)
					if err != nil {
						return err
					}
					statuses = append(statuses, status)
				}
			}
			return writeValue(cmd, opts, statuses, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(statuses))
				for _, status := range statuses {
					tools := "-"
					if status.Tools != nil {
						tools = fmt.Sprintf("%d", *status.Tools)
					}
					rows = append(rows, []string{
						output.ToneText(human, output.ToneInfo, status.Name),
						status.Kind,
						output.StatusBadge(human, status.Status),
						output.Value(status.Auth),
						mcpRemoteScopesLabel(status.Scopes),
						tools,
						mcpRemoteServerSource(mcpRemoteServer{URL: status.URL, Command: status.Command, Transport: status.Transport}),
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Toolbox", "Kind", "Status", "Auth", "Scope/Profile", "Tools", "Source"},
					Rows:    rows,
					Empty:   "no toolboxes registered",
				})
			})
		},
	}
}

func nativeStatusProviders() []providers.Provider {
	all := providers.All()
	return slices.DeleteFunc(all, func(provider providers.Provider) bool {
		return provider.AddHandler == nil && provider.RemoveHandler == nil
	})
}

func selectedToolboxEntries(opts *options, args []string) ([]mcpRemoteServerEntry, []nativeToolboxEntry, error) {
	remoteEntries, err := effectiveMCPRemoteServerEntries(opts.workDir)
	if err != nil {
		return nil, nil, err
	}
	nativeEntries, err := effectiveNativeToolboxEntries(opts.workDir)
	if err != nil {
		return nil, nil, err
	}
	if len(args) == 0 {
		return remoteEntries, nativeEntries, nil
	}
	selectedRemote := make([]mcpRemoteServerEntry, 0, len(args))
	selectedNative := make([]nativeToolboxEntry, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		name, err := cleanMCPRemoteName(arg)
		if err != nil {
			return nil, nil, err
		}
		if seen[name] {
			continue
		}
		if entry, ok := findMCPRemoteServerEntry(remoteEntries, name); ok {
			selectedRemote = append(selectedRemote, entry)
			seen[name] = true
			continue
		}
		if entry, ok := findNativeToolboxEntry(nativeEntries, name); ok {
			selectedNative = append(selectedNative, entry)
			seen[name] = true
			continue
		}
		return nil, nil, fmt.Errorf("toolbox %q is not registered", name)
	}
	return selectedRemote, selectedNative, nil
}

func findNativeToolboxEntry(entries []nativeToolboxEntry, name string) (nativeToolboxEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return nativeToolboxEntry{}, false
}

func readNativeToolboxStatus(ctx context.Context, opts *options, store credentials.Store, entry nativeToolboxEntry) (toolboxStatusItem, error) {
	item := toolboxStatusItem{
		Name:      entry.Name,
		Kind:      "native",
		Status:    "needs_auth",
		Auth:      "missing",
		Scope:     entry.Scope,
		Scopes:    entry.Scopes,
		URL:       providerBaseURL(opts, entry.Provider),
		Transport: "native",
		Path:      entry.Path,
	}
	tools := len(nativeToolboxActionSpecs(entry))
	item.Tools = &tools
	tokens, err := store.LoadOAuthTokens(ctx, credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  providers.CredentialProviderID(entry.Provider),
		AccountID: entry.Name,
	})
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return item, nil
		}
		return toolboxStatusItem{}, err
	}
	item.Status = "connected"
	item.Auth = nativeAuthLabel(tokens)
	if missing := oauthbroker.MissingScopes(tokens.Scopes, entry.Provider.ConnectionScopes); len(missing) > 0 {
		item.Status = "needs_auth"
		item.Auth = "missing-scopes"
	}
	return item, nil
}

func nativeAuthLabel(tokens credentials.OAuthTokens) string {
	switch tokens.Extra["auth_type"] {
	case "token_cookie":
		return "token-cookie"
	case "oauth_user":
		return "oauth"
	case "oauth_broker":
		return "brokered-oauth"
	default:
		if tokens.TokenType != "" {
			return strings.ToLower(tokens.TokenType)
		}
		return "oauth"
	}
}

func providerBaseURL(opts *options, provider providers.Provider) string {
	if value := strings.TrimSpace(opts.providerURL[provider.ID]); value != "" {
		return value
	}
	return provider.DefaultBaseURL
}

func readMCPRemoteToolboxStatus(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry) (toolboxStatusItem, error) {
	ref := mcpRemoteCredentialRef(opts, entry.Name)
	authRequired := entry.Server.AuthRequired
	if entry.Server.Transport == mcpRemoteTransportStdio && authRequired == nil {
		authRequired = new(false)
	}
	item := toolboxStatusItem{
		Name:         entry.Name,
		Kind:         mcpRemoteKind(entry.Server),
		Status:       "not_synced",
		Auth:         mcpRemoteAuthLabel(false, credentials.OAuthTokens{}, authRequired),
		Scope:        mcpRemoteScopeLabel(entry.Scope),
		Scopes:       mcpRemoteNormalizedScopes(entry.Scopes),
		URL:          entry.Server.URL,
		Command:      mcpRemoteCommandDisplay(entry.Server),
		Transport:    entry.Server.Transport,
		AuthRequired: authRequired,
		Path:         entry.Path,
	}
	if cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name); err != nil {
		return toolboxStatusItem{}, err
	} else if ok {
		count := len(cache.Tools)
		syncedAt := cache.SyncedAt
		item.Tools = &count
		item.SyncedAt = &syncedAt
		item.Status = "connected"
	}
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return toolboxStatusItem{}, err
	}
	authStored := err == nil
	if authRequired != nil && *authRequired && !authStored {
		item.Status = "needs_auth"
	}
	item.Auth = mcpRemoteAuthLabel(authStored, tokens, authRequired)
	return item, nil
}

func mcpRemoteAuthLabel(stored bool, tokens credentials.OAuthTokens, authRequired *bool) string {
	if stored {
		if mcpRemoteStoredTokenIsOAuth(tokens) {
			return "oauth"
		}
		return "bearer"
	}
	if authRequired == nil {
		return "unknown"
	}
	if *authRequired {
		return "missing"
	}
	return "not required"
}
