package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers"
)

func authCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage stored auth for toolboxes",
	}
	cmd.AddCommand(authLoginCommand(opts))
	cmd.AddCommand(authSetCommand(opts))
	cmd.AddCommand(authRefreshCommand(opts))
	cmd.AddCommand(authRemoveCommand(opts))
	cmd.AddCommand(authStatusCommand(opts))
	cmd.AddCommand(authWhoamiCommand(opts))
	return cmd
}

func authWhoamiCommand(opts *options) *cobra.Command {
	var verboseHTTP bool
	cmd := &cobra.Command{
		Use:   "whoami <toolbox>",
		Short: "Show the provider identity authorized for a toolbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			if entry, ok, err := lookupNativeToolboxEntry(name, opts.workDir); err != nil {
				return err
			} else if ok {
				return runNativeToolboxWhoami(cmd, opts, entry)
			}
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("toolbox %q is not registered", name)
			}
			cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("toolbox %q has no cached tools; run `toolmux mcp sync %s`", name, name)
			}
			tool, arguments, ok := mcpRemoteIdentityTool(entry, cache)
			if !ok {
				return fmt.Errorf("toolbox %q does not advertise a supported identity probe; use `toolmux mcp list %s` to inspect its read-only tools", name, name)
			}
			arguments = mcpRemoteMergeDefaultArguments(arguments, entry.Server.DefaultArguments, tool.InputSchema)
			if err := validateMCPRemoteRequiredArguments(arguments, tool.InputSchema); err != nil {
				return err
			}
			cliName := mcpRemoteToolCLINames(entry, cache.Tools)[tool.Name]
			if err := authorize(cmd, opts, mcpRemoteActionSpecForEntry(entry, tool, cliName), nil); err != nil {
				return err
			}
			trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), verboseHTTP)
			result, err := callMCPRemoteToolForCommand(cmd, opts, entry, tool, arguments, trace)
			if err != nil {
				return err
			}
			return writeMCPRemoteToolResult(cmd, opts, result)
		},
	}
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote toolbox HTTP requests and responses to stderr")
	return cmd
}

func runNativeToolboxWhoami(cmd *cobra.Command, opts *options, entry nativeToolboxEntry) error {
	var identitySpec actions.Spec
	for _, spec := range nativeToolboxActionSpecs(entry) {
		if spec.Resource == string(actions.ResourceConnection) && spec.Action == string(actions.VerbRead) && spec.Args.Min == 0 && spec.Args.Max == 0 {
			identitySpec = spec
			break
		}
	}
	if identitySpec.ID == "" {
		return fmt.Errorf("native toolbox %q does not expose a read-only connection identity action", entry.Name)
	}
	if err := authorize(cmd, opts, identitySpec, nil); err != nil {
		return err
	}
	handler, ok := providers.ActionHandler(entry.Provider, nativeToolboxHandlerID(entry, identitySpec))
	if !ok {
		return fmt.Errorf("native toolbox %q identity action is unavailable", entry.Name)
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	execCtx := actionExecutionContext(commandContext(cmd), opts, store, entry.Provider, entry.Name)
	execCtx.Interactive = interactiveCommand(cmd, opts)
	execCtx.Progress = newConnectUI(cmd, opts)
	result, err := handler(execCtx, actions.Invocation{Spec: identitySpec, Flags: map[string]any{}})
	if err != nil {
		return err
	}
	return writeActionResult(cmd, opts, execCtx, result)
}

func mcpRemoteIdentityTool(entry mcpRemoteServerEntry, cache mcpRemoteCache) (mcpRemoteTool, map[string]any, bool) {
	_, definition, ok := mcpRemoteCatalogDefinitionForServer(entry.Name, entry.Server)
	if !ok {
		return mcpRemoteTool{}, nil, false
	}
	for _, probe := range definition.Identity {
		if tool, found := mcpRemoteToolFromCache(cache, probe.Tool); found {
			return tool, cloneMCPRemoteMap(probe.Arguments), true
		}
	}
	return mcpRemoteTool{}, nil, false
}

func mcpRemoteToolIsIdentityProbe(entry mcpRemoteServerEntry, toolName string) bool {
	_, definition, ok := mcpRemoteCatalogDefinitionForServer(entry.Name, entry.Server)
	if !ok {
		return false
	}
	for _, probe := range definition.Identity {
		if probe.Tool == toolName {
			return true
		}
	}
	return false
}

func authLoginCommand(opts *options) *cobra.Command {
	var native nativeToolboxAddOptions
	var noBrowser bool
	var timeout time.Duration
	var authServer string
	cmd := &cobra.Command{
		Use:   "login <toolbox>",
		Short: "Authorize a toolbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			if entry, ok, err := lookupNativeToolboxEntry(name, opts.workDir); err != nil {
				return err
			} else if ok {
				return runNativeToolboxAuthLogin(cmd, opts, entry, native)
			}
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("toolbox %q is not registered", name)
			}
			return runMCPRemoteAuthLogin(cmd, opts, entry, mcpRemoteAuthLoginOptions{
				NoBrowser:    noBrowser,
				Timeout:      timeout,
				ClientID:     native.ClientID,
				ClientSecret: nativeClientSecret(opts, native),
				Scopes:       native.Scopes,
				AuthServer:   authServer,
				RedirectPort: native.RedirectPort,
			}, "toolbox")
		},
	}
	addNativeToolboxAddFlags(cmd, &native)
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print OAuth authorization URLs without opening a browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "OAuth callback wait timeout")
	cmd.Flags().StringVar(&authServer, "auth-server", "", "authorization server issuer to use when a remote MCP resource advertises more than one")
	return cmd
}

func authSetCommand(opts *options) *cobra.Command {
	var native nativeToolboxAddOptions
	var bearerToken string
	var bearerTokenEnv string
	var bearerTokenStdin bool
	cmd := &cobra.Command{
		Use:   "set <toolbox>",
		Short: "Store explicit auth for a toolbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			if entry, ok, err := lookupNativeToolboxEntry(name, opts.workDir); err != nil {
				return err
			} else if ok {
				if remoteBearerFlagsChanged(cmd) {
					return fmt.Errorf("native toolbox auth uses provider-specific token flags such as --token, --token-env, or --token-file")
				}
				return runNativeToolboxAuthLogin(cmd, opts, entry, native)
			}
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("toolbox %q is not registered", name)
			}
			if normalizeMCPRemoteServer(entry.Server).Transport == mcpRemoteTransportStdio {
				return fmt.Errorf("toolbox %q uses stdio; configure auth in the command environment or arguments", name)
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
			fmt.Fprintf(cmd.OutOrStdout(), "stored bearer token for toolbox %s\n", name)
			return nil
		},
	}
	addNativeToolboxAddFlags(cmd, &native)
	cmd.Flags().StringVar(&bearerToken, "bearer-token", "", "bearer token to store for remote HTTP MCP toolboxes")
	cmd.Flags().StringVar(&bearerTokenEnv, "bearer-token-env", "", "environment variable containing the bearer token")
	cmd.Flags().BoolVar(&bearerTokenStdin, "bearer-token-stdin", false, "read bearer token from stdin")
	return cmd
}

func authRefreshCommand(opts *options) *cobra.Command {
	var noProbe bool
	var verboseHTTP bool
	login := mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute}
	cmd := &cobra.Command{
		Use:   "refresh [toolbox...]",
		Short: "Probe and refresh stored toolbox auth",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			selectedRemote, selectedNative, err := selectedToolboxEntries(opts, args)
			if err != nil {
				return err
			}
			trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), verboseHTTP)
			report := mcpRemoteAuthRefreshReport{}
			if len(args) == 0 || len(selectedRemote) > 0 {
				names := remoteAuthRefreshNames(selectedRemote, len(args) > 0)
				report, err = refreshMCPRemoteAuth(commandContext(cmd), opts, store, names, mcpRemoteAuthRefreshOptions{
					Probe: !noProbe,
					Trace: trace,
					ReauthWhenMissing: func(entry mcpRemoteServerEntry) error {
						return runMCPRemoteAuthLogin(cmd, opts, entry, login, "toolbox")
					},
				})
				if err != nil {
					return err
				}
			}
			for _, entry := range selectedNative {
				result, include := refreshNativeToolboxAuth(commandContext(cmd), opts, store, entry, len(args) > 0)
				if include {
					report.Results = append(report.Results, result)
				}
			}
			return writeActionResult(cmd, opts, actions.Context{}, report)
		},
	}
	cmd.Flags().BoolVar(&noProbe, "no-probe", false, "refresh only locally expired OAuth tokens without probing remote toolboxes")
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote toolbox HTTP requests and responses to stderr")
	cmd.Flags().BoolVar(&login.NoBrowser, "no-browser", false, "print OAuth authorization URLs without opening a browser when re-auth is needed")
	cmd.Flags().DurationVar(&login.Timeout, "timeout", 2*time.Minute, "OAuth callback wait timeout when re-auth is needed")
	cmd.Flags().StringVar(&login.ClientID, "client-id", "", "OAuth client ID to use when re-auth is needed")
	cmd.Flags().StringVar(&login.ClientSecret, "client-secret", "", "OAuth client secret to use when re-auth is needed")
	cmd.Flags().StringArrayVar(&login.Scopes, "scope", nil, "OAuth scope to request when re-auth is needed; repeatable and comma-separated values are accepted")
	cmd.Flags().StringVar(&login.AuthServer, "auth-server", "", "authorization server issuer to use when re-auth is needed")
	cmd.Flags().IntVar(&login.RedirectPort, "redirect-port", 0, "loopback redirect port when re-auth is needed; 0 chooses a free port")
	return cmd
}

func authRemoveCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <toolbox> [toolbox...]",
		Aliases: []string{"rm"},
		Short:   "Remove stored auth for toolboxes",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selectedRemote, selectedNative, err := selectedToolboxEntries(opts, args)
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			ctx := commandContext(cmd)
			for _, entry := range selectedRemote {
				if err := store.DeleteOAuthTokens(ctx, mcpRemoteCredentialRef(opts, entry.Name)); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed stored auth for toolbox %s\n", entry.Name)
			}
			for _, entry := range selectedNative {
				if err := runNativeToolboxAuthRemove(ctx, cmd, opts, store, entry); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func authStatusCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status [toolbox...]",
		Short: "Show stored auth status for toolboxes",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			selectedRemote, selectedNative, err := selectedToolboxEntries(opts, args)
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			items := make([]toolboxAuthStatusItem, 0, len(selectedRemote)+len(selectedNative))
			for _, entry := range selectedRemote {
				item, err := readMCPRemoteAuthStatus(commandContext(cmd), opts, store, entry)
				if err != nil {
					return err
				}
				items = append(items, item)
			}
			for _, entry := range selectedNative {
				item, err := readNativeToolboxAuthStatus(commandContext(cmd), opts, store, entry)
				if err != nil {
					return err
				}
				items = append(items, item)
			}
			return writeValue(cmd, opts, items, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(items))
				for _, item := range items {
					rows = append(rows, []string{
						output.ToneText(human, output.ToneInfo, item.Toolbox),
						item.Kind,
						output.Value(item.Auth),
						output.StatusBadge(human, item.Status),
						output.Value(item.Message),
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Toolbox", "Kind", "Auth", "Status", "Message"},
					Rows:    rows,
					Empty:   "no toolboxes registered",
				})
			})
		},
	}
}

type toolboxAuthStatusItem struct {
	Toolbox string `json:"toolbox" yaml:"toolbox"`
	Kind    string `json:"kind" yaml:"kind"`
	Auth    string `json:"auth" yaml:"auth"`
	Status  string `json:"status" yaml:"status"`
	Message string `json:"message" yaml:"message"`
}

func runMCPRemoteAuthLogin(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, login mcpRemoteAuthLoginOptions, noun string) error {
	if normalizeMCPRemoteServer(entry.Server).Transport == mcpRemoteTransportStdio {
		return fmt.Errorf("toolbox %q uses stdio; configure auth in the command environment or arguments", entry.Name)
	}
	if login.Timeout <= 0 {
		login.Timeout = 2 * time.Minute
	}
	tokens, err := loginMCPRemoteOAuth(cmd, opts, entry, login)
	if err != nil {
		return err
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	if err := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, entry.Name), tokens); err != nil {
		return err
	}
	if err := writeMCPRemoteAuthRequired(entry, true); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for %s %s\n", noun, entry.Name)
	return nil
}

func runNativeToolboxAuthLogin(cmd *cobra.Command, opts *options, entry nativeToolboxEntry, native nativeToolboxAddOptions) error {
	if entry.Provider.AddHandler == nil {
		return fmt.Errorf("toolbox %q does not support auth login", entry.Name)
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	execCtx := actionExecutionContext(commandContext(cmd), opts, store, entry.Provider, entry.Name)
	execCtx.Interactive = interactiveCommand(cmd, opts)
	if execCtx.OpenBrowser == nil && execCtx.Interactive {
		execCtx.OpenBrowser = openURL
	}
	execCtx.Progress = newConnectUI(cmd, opts)
	execCtx.SelectString = selectString(cmd)
	execCtx.SelectInteger = selectInteger(cmd)
	result, err := entry.Provider.AddHandler(execCtx, actions.Invocation{
		Spec:  toolboxAddSpec(),
		Args:  []string{entry.Provider.ID},
		Flags: nativeToolboxAddFlagValues(native),
	})
	if err != nil {
		return err
	}
	return writeActionResult(cmd, opts, execCtx, result)
}

func runNativeToolboxAuthRemove(ctx context.Context, cmd *cobra.Command, opts *options, store credentials.Store, entry nativeToolboxEntry) error {
	if entry.Provider.RemoveHandler != nil {
		execCtx := actionExecutionContext(ctx, opts, store, entry.Provider, entry.Name)
		result, err := entry.Provider.RemoveHandler(execCtx, actions.Invocation{
			Spec:  toolboxRemoveSpec(),
			Args:  []string{entry.Name},
			Flags: map[string]any{},
		})
		if err != nil {
			return err
		}
		if err := writeActionResult(cmd, opts, execCtx, result); err != nil {
			return err
		}
		return nil
	}
	if err := store.DeleteOAuthTokens(ctx, nativeToolboxCredentialRef(opts, entry)); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removed stored auth for toolbox %s\n", entry.Name)
	return nil
}

func readMCPRemoteAuthStatus(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry) (toolboxAuthStatusItem, error) {
	diagnostic := mcpRemoteAuthDiagnostic(ctx, opts, store, entry)
	status := diagnostic.Status
	if status == "ok" {
		status = "connected"
	}
	auth := "none"
	if strings.Contains(diagnostic.Message, "OAuth") {
		auth = "oauth"
	} else if strings.Contains(diagnostic.Message, "bearer") {
		auth = "bearer"
	} else if strings.Contains(diagnostic.Message, "not required") {
		auth = "not required"
	}
	return toolboxAuthStatusItem{
		Toolbox: entry.Name,
		Kind:    mcpRemoteKind(entry.Server),
		Auth:    auth,
		Status:  status,
		Message: diagnostic.Message,
	}, nil
}

func readNativeToolboxAuthStatus(ctx context.Context, opts *options, store credentials.Store, entry nativeToolboxEntry) (toolboxAuthStatusItem, error) {
	status, err := readNativeToolboxStatus(ctx, opts, store, entry)
	if err != nil {
		return toolboxAuthStatusItem{}, err
	}
	message := "stored auth is present"
	if status.Status == "needs_auth" {
		message = "auth required but not stored"
		if status.Auth == "missing-scopes" {
			message = "stored auth is missing required scopes"
		}
	}
	return toolboxAuthStatusItem{
		Toolbox: entry.Name,
		Kind:    "native",
		Auth:    status.Auth,
		Status:  status.Status,
		Message: message,
	}, nil
}

func refreshNativeToolboxAuth(ctx context.Context, opts *options, store credentials.Store, entry nativeToolboxEntry, explicit bool) (mcpRemoteAuthRefreshResult, bool) {
	tokens, err := store.LoadOAuthTokens(ctx, nativeToolboxCredentialRef(opts, entry))
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return mcpRemoteAuthRefreshResult{
				Toolbox:  entry.Name,
				AuthType: "none",
				Status:   mcpRemoteAuthRefreshStatusSkipped,
				Message:  "no stored auth",
			}, explicit
		}
		return mcpRemoteAuthRefreshResult{
			Toolbox:  entry.Name,
			AuthType: "unknown",
			Status:   mcpRemoteAuthRefreshStatusFailed,
			Message:  err.Error(),
		}, true
	}
	message := "stored native auth is present; provider commands refresh it on use"
	if !tokens.ExpiresAt.IsZero() && !time.Now().Add(mcpRemoteOAuthRefreshSkew).Before(tokens.ExpiresAt) {
		message = "stored native auth may need provider-managed refresh; run a provider command or `toolmux auth login " + entry.Name + "` if refresh fails"
	}
	return mcpRemoteAuthRefreshResult{
		Toolbox:  entry.Name,
		AuthType: nativeAuthLabel(tokens),
		Status:   mcpRemoteAuthRefreshStatusValid,
		Message:  message,
	}, true
}

func remoteAuthRefreshNames(entries []mcpRemoteServerEntry, explicit bool) []string {
	if !explicit {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func nativeToolboxCredentialRef(opts *options, entry nativeToolboxEntry) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  providers.CredentialProviderID(entry.Provider),
		AccountID: entry.Name,
	}
}

func nativeClientSecret(opts *options, native nativeToolboxAddOptions) string {
	if value := strings.TrimSpace(native.ClientSecret); value != "" {
		return value
	}
	if name := strings.TrimSpace(native.ClientSecretEnv); name != "" && opts.env != nil {
		return opts.env(name)
	}
	return ""
}

func remoteBearerFlagsChanged(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("bearer-token") ||
		cmd.Flags().Changed("bearer-token-env") ||
		cmd.Flags().Changed("bearer-token-stdin")
}
