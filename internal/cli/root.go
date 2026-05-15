package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/version"
)

type options struct {
	output             string
	color              string
	pager              string
	profile            string
	policy             string
	readOnly           bool
	credentials        func() (credentials.Store, error)
	httpClient         *http.Client
	openBrowser        func(string) error
	providerURL        map[string]string
	providerAPI        map[string]string
	toolmuxdURL        string
	mcpCacheDir        string
	mcpToolCallTimeout time.Duration
	mcpRemoteConflicts []mcpRemoteNameConflict
	workDir            string
}

type Dependencies struct {
	Credentials credentials.Store
	HTTPClient  *http.Client
	OpenBrowser func(string) error
	Env         func(string) string
	ProviderURL map[string]string
	ProviderAPI map[string]string
	ToolmuxdURL string
	WorkDir     string
}

func NewRootCommand() *cobra.Command {
	return NewRootCommandWithDeps(Dependencies{})
}

func NewRootCommandWithDeps(deps Dependencies) *cobra.Command {
	opts := &options{
		output:             "table",
		color:              "auto",
		pager:              "auto",
		profile:            "default",
		mcpToolCallTimeout: mcpRemoteSSEIdleTimeout,
	}
	opts.credentials = func() (credentials.Store, error) {
		if deps.Credentials != nil {
			return deps.Credentials, nil
		}
		return credentials.NewKeyringStore(credentials.KeyringConfig{})
	}
	opts.httpClient = deps.HTTPClient
	if opts.httpClient == nil {
		opts.httpClient = http.DefaultClient
	}
	opts.openBrowser = deps.OpenBrowser
	env := deps.Env
	if env == nil {
		env = os.Getenv
	}
	opts.providerURL = maps.Clone(deps.ProviderURL)
	if opts.providerURL == nil {
		opts.providerURL = map[string]string{}
	}
	opts.providerAPI = maps.Clone(deps.ProviderAPI)
	if opts.providerAPI == nil {
		opts.providerAPI = map[string]string{}
	}
	opts.toolmuxdURL = strings.TrimRight(firstNonEmpty(deps.ToolmuxdURL, env("TOOLMUX_TOOLMUXD_URL"), "https://api.toolmux.com"), "/")
	opts.mcpCacheDir = strings.TrimSpace(env("TOOLMUX_MCP_CACHE_DIR"))
	opts.workDir = strings.TrimSpace(deps.WorkDir)
	configureProviders(opts, env)

	root := &cobra.Command{
		Use:           "toolmux",
		Short:         "An agentic toolbox for connecting services to local agents",
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if opts.mcpToolCallTimeout <= 0 {
				return fmt.Errorf("--mcp-tool-call-timeout must be greater than 0")
			}
			if mcpRemoteCommandAllowsConflicts(cmd) {
				return nil
			}
			return mcpRemoteConflictsError(opts.mcpRemoteConflicts)
		},
	}
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", "table", "output format: table, json, yaml")
	root.PersistentFlags().StringVar(&opts.color, "color", "auto", "color output: auto, always, never")
	root.PersistentFlags().StringVar(&opts.pager, "pager", "auto", "pager behavior: auto, always, never")
	root.PersistentFlags().StringVar(&opts.profile, "profile", "default", "Toolmux profile")
	root.PersistentFlags().StringVar(&opts.policy, "policy", "", "policy file path")
	root.PersistentFlags().BoolVar(&opts.readOnly, "read-only", false, "deny actions with remote or local write effects")
	root.PersistentFlags().DurationVar(&opts.mcpToolCallTimeout, "mcp-tool-call-timeout", mcpRemoteSSEIdleTimeout, "remote MCP tools/call inactivity timeout, such as 60s or 2m")

	root.AddCommand(versionCommand())
	root.AddCommand(toolboxAddCommand(opts))
	root.AddCommand(toolboxRemoveCommand(opts))
	root.AddCommand(statusCommand(opts))
	root.AddCommand(doctorCommand(opts))
	root.AddCommand(toolboxCatalogCommand(opts))
	root.AddCommand(policyCommand(opts))
	root.AddCommand(mcpCommand(opts))
	root.AddCommand(workflowCommand(opts))
	registerActionCommands(root, opts)
	opts.mcpRemoteConflicts = registerCachedMCPRemoteCommands(root, opts)

	return root
}

func configureProviders(opts *options, env func(string) string) {
	for _, provider := range providers.All() {
		if provider.BaseURLEnv != "" || provider.DefaultBaseURL != "" {
			opts.providerURL[provider.ID] = firstNonEmpty(opts.providerURL[provider.ID], env(provider.BaseURLEnv), provider.DefaultBaseURL)
		}
		if provider.APIVersionEnv != "" || provider.DefaultAPIVersion != "" {
			opts.providerAPI[provider.ID] = firstNonEmpty(opts.providerAPI[provider.ID], env(provider.APIVersionEnv), provider.DefaultAPIVersion)
		}
	}
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "toolmux %s\n", version.Version)
		},
	}
}

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
			if err := authorize(cmd, opts, toolboxStatusSpec(), args); err != nil {
				return err
			}
			remoteArgs, nativeProviders := partitionNativeStatusArgs(args)
			var selected []mcpRemoteServerEntry
			if len(args) == 0 || len(remoteArgs) > 0 {
				var err error
				selected, err = selectedMCPRemoteEntries(remoteArgs)
				if err != nil {
					return err
				}
			}
			includeDisconnectedNative := len(args) > 0
			if len(args) == 0 {
				nativeProviders = nativeStatusProviders()
			}
			statuses := make([]toolboxStatusItem, 0, len(selected)+len(nativeProviders))
			if len(selected) > 0 || len(nativeProviders) > 0 {
				store, err := opts.credentials()
				if err != nil {
					return err
				}
				for _, entry := range selected {
					status, err := readMCPRemoteToolboxStatus(commandContext(cmd), opts, store, entry)
					if err != nil {
						return err
					}
					statuses = append(statuses, status)
				}
				for _, provider := range nativeProviders {
					status, err := readNativeToolboxStatus(commandContext(cmd), opts, store, provider)
					if err != nil {
						return err
					}
					if includeDisconnectedNative || nativeToolboxStatusRegistered(status) {
						statuses = append(statuses, status)
					}
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
					Headers: []string{"Toolbox", "Kind", "Status", "Auth", "Scope", "Tools", "Source"},
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

func nativeToolboxStatusRegistered(status toolboxStatusItem) bool {
	return status.Status != "disconnected" || status.Auth != "none"
}

func partitionNativeStatusArgs(args []string) ([]string, []providers.Provider) {
	if len(args) == 0 {
		return nil, nil
	}
	remoteArgs := make([]string, 0, len(args))
	nativeProviders := make([]providers.Provider, 0, len(args))
	seenNative := map[string]bool{}
	for _, arg := range args {
		provider, ok := providers.Lookup(arg)
		if ok && (provider.AddHandler != nil || provider.RemoveHandler != nil) {
			if !seenNative[provider.ID] {
				nativeProviders = append(nativeProviders, provider)
				seenNative[provider.ID] = true
			}
			continue
		}
		remoteArgs = append(remoteArgs, arg)
	}
	return remoteArgs, nativeProviders
}

func selectedMCPRemoteEntries(args []string) ([]mcpRemoteServerEntry, error) {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return entries, nil
	}
	selected := make([]mcpRemoteServerEntry, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		name, err := cleanMCPRemoteName(arg)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		entry, ok := findMCPRemoteServerEntry(entries, name)
		if !ok {
			return nil, fmt.Errorf("toolbox %q is not registered", name)
		}
		seen[name] = true
		selected = append(selected, entry)
	}
	return selected, nil
}

func readNativeToolboxStatus(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) (toolboxStatusItem, error) {
	item := toolboxStatusItem{
		Name:      provider.ID,
		Kind:      "native",
		Status:    "disconnected",
		Auth:      "none",
		Scope:     "profile",
		Scopes:    []string{opts.profile},
		URL:       providerBaseURL(opts, provider),
		Transport: "native",
	}
	tokens, err := store.LoadOAuthTokens(ctx, credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  provider.ID,
		AccountID: "default",
	})
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return item, nil
		}
		return toolboxStatusItem{}, err
	}
	item.Status = "connected"
	item.Auth = nativeAuthLabel(tokens)
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

type providerDiagnostic = actions.Diagnostic

func doctorCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Toolmux setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, doctorSpec(), args); err != nil {
				return err
			}
			diagnostics := coreDiagnostics(opts)
			if diagnosticsHaveFailure(diagnostics) {
				return writeDiagnostics(cmd, opts, diagnostics)
			}
			store, err := opts.credentials()
			if err != nil {
				diagnostics = append(diagnostics, providerDiagnostic{
					Check:       "credential-store",
					Status:      "fail",
					Message:     err.Error(),
					Remediation: "Check OS credential store availability or run provider commands with a supported keyring backend.",
				})
				return writeDiagnostics(cmd, opts, diagnostics)
			}
			diagnostics = append(diagnostics, credentialStoreDiagnostic(cmd.Context(), store))
			diagnostics = append(diagnostics, mcpRemoteDoctorDiagnostics(cmd.Context(), opts, store)...)
			return writeDiagnostics(cmd, opts, diagnostics)
		},
	}
}

func diagnosticsHaveFailure(diagnostics []providerDiagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Status == "fail" {
			return true
		}
	}
	return false
}

func coreDiagnostics(opts *options) []providerDiagnostic {
	_, paths, err := policy.LoadDiscovered(opts.policy, "")
	if err != nil {
		return []providerDiagnostic{{
			Check:       "policy",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Fix the policy file or pass --policy with a readable policy path.",
		}}
	}
	if len(paths) == 0 {
		return []providerDiagnostic{{
			Check:   "policy",
			Status:  "warn",
			Message: "no policy configured",
		}}
	}
	return []providerDiagnostic{{
		Check:   "policy",
		Status:  "ok",
		Message: "loaded " + strings.Join(paths, ", "),
	}}
}

func credentialStoreDiagnostic(ctx context.Context, store credentials.Store) providerDiagnostic {
	diagnostics := store.Doctor(ctx)
	status := "fail"
	if diagnostics.Available {
		status = "ok"
	}
	return providerDiagnostic{
		Check:   "credential-store",
		Status:  status,
		Message: diagnostics.Message,
		Details: map[string]string{
			"backend": diagnostics.Backend,
			"service": diagnostics.Service,
		},
	}
}

func mcpRemoteDoctorDiagnostics(ctx context.Context, opts *options, store credentials.Store) []providerDiagnostic {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return []providerDiagnostic{{
			Check:       "mcp-config",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Fix Toolmux MCP config or remove invalid MCP server entries.",
		}}
	}
	if len(entries) == 0 {
		return []providerDiagnostic{{
			Check:       "toolboxes",
			Status:      "warn",
			Message:     "no toolboxes registered",
			Remediation: "Run `toolmux add <catalog-name-or-url>`.",
		}}
	}
	diagnostics := []providerDiagnostic{{
		Check:   "toolboxes",
		Status:  "ok",
		Message: fmt.Sprintf("%d registered", len(entries)),
	}}
	for _, entry := range entries {
		diagnostics = append(diagnostics, mcpRemoteCacheDiagnostic(opts, entry))
		diagnostics = append(diagnostics, mcpRemoteAuthDiagnostic(ctx, opts, store, entry))
	}
	return diagnostics
}

func mcpRemoteCacheDiagnostic(opts *options, entry mcpRemoteServerEntry) providerDiagnostic {
	diagnostic := providerDiagnostic{
		Provider: entry.Name,
		Check:    "toolbox-cache",
		Status:   "warn",
	}
	cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
	if err != nil {
		diagnostic.Status = "fail"
		diagnostic.Message = err.Error()
		diagnostic.Remediation = "Remove the corrupt cache or run `toolmux mcp sync " + entry.Name + "`."
		return diagnostic
	}
	if !ok {
		diagnostic.Message = "no cached tools"
		diagnostic.Remediation = "Run `toolmux mcp sync " + entry.Name + "`."
		return diagnostic
	}
	diagnostic.Status = "ok"
	diagnostic.Message = fmt.Sprintf("%d cached tools", len(cache.Tools))
	if time.Since(cache.SyncedAt) > mcpRemoteCacheMaxAge {
		diagnostic.Status = "warn"
		diagnostic.Message = "cached tools are stale"
		diagnostic.Remediation = "Run `toolmux mcp sync " + entry.Name + "`."
	}
	return diagnostic
}

func mcpRemoteAuthDiagnostic(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry) providerDiagnostic {
	diagnostic := providerDiagnostic{
		Provider: entry.Name,
		Check:    "toolbox-auth",
		Status:   "ok",
		Message:  "auth not required",
	}
	if entry.Server.Transport == mcpRemoteTransportStdio {
		return diagnostic
	}
	tokens, err := store.LoadOAuthTokens(ctx, mcpRemoteCredentialRef(opts, entry.Name))
	if err != nil {
		if !errors.Is(err, credentials.ErrNotFound) {
			diagnostic.Status = "fail"
			diagnostic.Message = err.Error()
			diagnostic.Remediation = "Check OS credential store availability."
			return diagnostic
		}
		if entry.Server.AuthRequired != nil && *entry.Server.AuthRequired {
			diagnostic.Status = "warn"
			diagnostic.Message = "auth required but not stored"
			diagnostic.Remediation = "Run `toolmux mcp auth login " + entry.Name + "` or `toolmux mcp auth set " + entry.Name + "`."
			return diagnostic
		}
		if entry.Server.AuthRequired == nil {
			diagnostic.Status = "warn"
			diagnostic.Message = "auth requirement unknown"
			diagnostic.Remediation = "Run `toolmux mcp sync " + entry.Name + "`."
		}
		return diagnostic
	}
	if mcpRemoteStoredTokenIsOAuth(tokens) {
		diagnostic.Message = "OAuth auth stored"
		return diagnostic
	}
	diagnostic.Message = "bearer auth stored"
	return diagnostic
}

func writeDiagnostics(cmd *cobra.Command, opts *options, diagnostics []providerDiagnostic) error {
	return writeValue(cmd, opts, diagnostics, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		rows := make([][]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			target := firstNonEmpty(diagnostic.Provider, "toolmux")
			rows = append(rows, []string{
				target,
				diagnostic.Check,
				output.StatusBadge(human, diagnostic.Status),
				output.Value(diagnostic.Message),
				output.Value(diagnostic.Remediation),
			})
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Target", "Check", "Status", "Message", "Remediation"},
			Rows:    rows,
			Empty:   "no diagnostics",
		})
	})
}

func policyCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Inspect and evaluate local command policy",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create a starter policy file",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Join(".toolmux", "policy.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return err
			}
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists", path)
			}
			// #nosec G306 -- policy files are non-secret configuration.
			if err := os.WriteFile(path, []byte(policy.StarterPolicy), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", path)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "catalog",
		Short: "List policy-aware commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeCatalog(cmd, opts)
		},
	})

	for _, name := range []string{"check", "explain"} {
		var commandLine string
		sub := &cobra.Command{
			Use:   name,
			Short: "Evaluate a command against local policy",
			RunE: func(cmd *cobra.Command, args []string) error {
				if commandLine == "" {
					return fmt.Errorf("--command is required")
				}
				spec, ok := specForCommand(opts, commandLine)
				if !ok {
					return fmt.Errorf("no command spec found for %q", commandLine)
				}
				decision, err := decisionFor(cmd, opts, spec, nil)
				if err != nil {
					return err
				}
				if opts.output == "json" {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(decision)
				}
				if decision.Allowed {
					fmt.Fprintf(cmd.OutOrStdout(), "allowed: %s\n", decision.Reason)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "denied: %s\n", decision.Reason)
				return policy.ErrDenied
			},
		}
		sub.Flags().StringVar(&commandLine, "command", "", "command to evaluate")
		cmd.AddCommand(sub)
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check policy discovery and parsing",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, paths, err := policy.LoadDiscovered(opts.policy, "")
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "policy doctor: no policy configured")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "policy doctor: loaded %s\n", strings.Join(paths, ", "))
			return nil
		},
	})

	return cmd
}

func registerActionCommands(root *cobra.Command, opts *options) {
	for _, provider := range providers.All() {
		if len(provider.Tree.Children) == 0 {
			continue
		}
		registerActionNode(root, opts, actions.ProviderName(provider.ID), provider.Tree, nil)
	}
}

func registerActionNode(parent *cobra.Command, opts *options, provider actions.ProviderName, node actions.Spec, parentPath []string) {
	resolved := actions.Resolve(provider, node, parentPath)
	if len(node.Children) > 0 {
		group := actionGroupCommand(resolved)
		parent.AddCommand(group)
		for _, child := range node.Children {
			registerActionNode(group, opts, provider, child, resolved.Path)
		}
		return
	}
	if resolved.ID == "" {
		return
	}
	parent.AddCommand(actionCommand(opts, resolved))
}

func actionCommand(opts *options, spec policy.CommandSpec) *cobra.Command {
	use := spec.Use
	if use == "" {
		use = spec.Path[len(spec.Path)-1]
	}
	short := spec.Short
	if short == "" {
		short = actionShort(spec)
	}
	cmd := &cobra.Command{
		Use:     use,
		Aliases: spec.Aliases,
		Short:   short,
		Long:    firstNonEmpty(spec.Description, short),
		Args:    actionArgs(spec),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, spec, args); err != nil {
				return err
			}
			provider, ok := providers.Lookup(spec.Provider)
			if !ok {
				return fmt.Errorf("unknown provider %q for %s", spec.Provider, spec.ID)
			}
			handler, ok := providers.ActionHandler(provider, spec.ID)
			if ok {
				store, err := opts.credentials()
				if err != nil {
					return err
				}
				execCtx := actionExecutionContext(commandContext(cmd), opts, store, provider)
				execCtx.Interactive = interactiveCommand(cmd, opts)
				if execCtx.OpenBrowser == nil && execCtx.Interactive {
					execCtx.OpenBrowser = openURL
				}
				execCtx.Progress = newConnectUI(cmd, opts)
				execCtx.SelectString = selectString(cmd)
				execCtx.SelectInteger = selectInteger(cmd)
				result, err := handler(execCtx, actions.Invocation{
					Spec:  spec,
					Args:  append([]string(nil), args...),
					Flags: metadataFlagValues(cmd, spec),
				})
				if err != nil {
					return err
				}
				return writeActionResult(cmd, opts, execCtx, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: not implemented yet\n", spec.ID)
			return nil
		},
	}
	addMetadataFlags(cmd, spec)
	return cmd
}

func actionGroupCommand(group actions.Spec) *cobra.Command {
	use := group.Use
	if use == "" {
		use = group.Segment
	}
	short := group.Short
	if short == "" {
		short = "Policy-aware command group"
	}
	return &cobra.Command{
		Use:                use,
		Aliases:            group.Aliases,
		Short:              short,
		Long:               firstNonEmpty(group.Description, short),
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
	}
}

func actionArgs(spec policy.CommandSpec) cobra.PositionalArgs {
	minimum := spec.Args.Min
	maximum := spec.Args.Max
	if maximum < 0 {
		return cobra.MinimumNArgs(minimum)
	}
	if minimum == maximum {
		return cobra.ExactArgs(minimum)
	}
	if minimum == 0 {
		return cobra.MaximumNArgs(maximum)
	}
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MinimumNArgs(minimum)(cmd, args); err != nil {
			return err
		}
		return cobra.MaximumNArgs(maximum)(cmd, args)
	}
}

func addMetadataFlags(cmd *cobra.Command, spec policy.CommandSpec) {
	for _, flag := range spec.Flags {
		switch flag.Type {
		case actions.FlagBool:
			cmd.Flags().Bool(flag.Name, flag.DefaultBool, flag.Usage)
		case actions.FlagInt:
			cmd.Flags().Int(flag.Name, flag.DefaultInt, flag.Usage)
		case actions.FlagString:
			cmd.Flags().String(flag.Name, flag.Default, flag.Usage)
		case actions.FlagStringSlice:
			cmd.Flags().StringSlice(flag.Name, flag.DefaultString, flag.Usage)
		}
	}
}

func metadataFlagValues(cmd *cobra.Command, spec policy.CommandSpec) map[string]any {
	values := make(map[string]any, len(spec.Flags))
	for _, flag := range spec.Flags {
		switch flag.Type {
		case actions.FlagBool:
			values[flag.Name], _ = cmd.Flags().GetBool(flag.Name)
		case actions.FlagInt:
			values[flag.Name], _ = cmd.Flags().GetInt(flag.Name)
		case actions.FlagString:
			values[flag.Name], _ = cmd.Flags().GetString(flag.Name)
		case actions.FlagStringSlice:
			values[flag.Name], _ = cmd.Flags().GetStringSlice(flag.Name)
		}
	}
	return values
}

func actionShort(spec policy.CommandSpec) string {
	return strings.TrimSpace(humanVerb(spec.Action) + " " + providerDisplayName(spec.Provider) + " " + humanResource(spec.Resource))
}

func humanVerb(verb string) string {
	switch verb {
	case "create":
		return "Create"
	case "delete":
		return "Delete"
	case "diagnose":
		return "Diagnose"
	case "list":
		return "List"
	case "move":
		return "Move"
	case "open":
		return "Open"
	case "query":
		return "Query"
	case "read":
		return "Read"
	case "restore":
		return "Restore"
	case "search":
		return "Search"
	case "send":
		return "Send"
	case "update":
		return "Update"
	default:
		return "Run"
	}
}

func providerDisplayName(id string) string {
	provider, ok := providers.Lookup(id)
	if !ok {
		return id
	}
	return provider.DisplayName
}

func humanResource(resource string) string {
	return strings.ReplaceAll(resource, "_", " ")
}

func writeCatalog(cmd *cobra.Command, opts *options) error {
	specs := allPolicyCommandSpecs(opts)
	return writeValue(cmd, opts, specs, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		rows := make([][]string, 0, len(specs))
		for _, spec := range specs {
			rows = append(rows, []string{
				spec.ID,
				spec.Provider,
				spec.Resource,
				spec.Action,
				spec.RemoteEffect,
				spec.LocalEffect,
				strings.Join(spec.Path, " "),
			})
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Command", "Provider", "Resource", "Action", "Remote", "Local", "Path"},
			Rows:    rows,
			Empty:   "no command specs",
		})
	})
}

func allPolicyCommandSpecs(opts *options) []policy.CommandSpec {
	specs := rootCommandSpecs()
	specs = append(specs, providers.CommandSpecs()...)
	specs = append(specs, cachedMCPRemoteCommandSpecs(opts)...)
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func rootCommandSpecs() []policy.CommandSpec {
	return []policy.CommandSpec{
		mcpConfigureSpec(),
		mcpEnableSpec(),
		mcpDisableSpec(),
		mcpProfileSetSpec(),
		mcpProfileDefaultSpec(),
		toolboxAddSpec(),
		toolboxRemoveSpec(),
		toolboxStatusSpec(),
		toolboxCatalogListSpec(),
		toolboxCatalogManageSpec(),
		doctorSpec(),
		mcpRemoteSyncSpec(),
		mcpRemoteRenameSpec(),
		mcpRemoteListSpec(),
		mcpRemoteShowSpec(),
		mcpRemoteCatalogListSpec(),
		mcpRemoteCatalogManageSpec(),
		mcpRemoteDefaultsListSpec(),
		mcpRemoteDefaultsSetSpec(),
		mcpRemoteDefaultsRemoveSpec(),
		mcpRemoteAuthLoginSpec(),
		mcpRemoteAuthSetSpec(),
		mcpRemoteAuthRemoveSpec(),
		mcpRemoteAuthStatusSpec(),
		schemaSpec(),
		workflowInitSpec(),
		workflowListSpec(),
		workflowShowSpec(),
		workflowRenderSpec(),
		workflowRunSpec(),
		workflowConfigSetDefaultAgentSpec(),
	}
}

func authorize(cmd *cobra.Command, opts *options, spec policy.CommandSpec, args []string) error {
	decision, err := decisionFor(cmd, opts, spec, args)
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	return fmt.Errorf("%w: %s", policy.ErrDenied, decision.Reason)
}

func decisionFor(cmd *cobra.Command, opts *options, spec policy.CommandSpec, args []string) (policy.Decision, error) {
	if opts.readOnly && !policy.AllowsReadOnly(spec) {
		return policy.Decision{
			Allowed: false,
			Reason:  "read-only mode blocks command " + spec.ID,
			Rule:    "read-only",
		}, nil
	}
	engine, _, err := policy.LoadDiscovered(opts.policy, "")
	if err != nil {
		return policy.Decision{}, err
	}
	inv := policy.Invocation{
		Spec:       spec,
		Profile:    opts.profile,
		Account:    "default",
		OutputMode: opts.output,
		Args:       map[string]any{"argv": args},
	}
	return engine.Authorize(inv), nil
}

func specForCommand(opts *options, commandLine string) (policy.CommandSpec, bool) {
	parts := strings.Fields(commandLine)
	if spec, ok := rootSpecForCommandParts(parts); ok {
		return spec, true
	}
	if spec, ok := mcpRemoteSpecForCommandParts(opts, parts); ok {
		return spec, true
	}
	for _, spec := range providers.CommandSpecs() {
		if len(parts) >= len(spec.Path) && equalStrings(parts[:len(spec.Path)], spec.Path) {
			return spec, true
		}
	}
	return policy.CommandSpec{}, false
}

func rootSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) >= 1 && parts[0] == "add" {
		return toolboxAddSpec(), true
	}
	if len(parts) >= 1 && (parts[0] == "remove" || parts[0] == "rm") {
		return toolboxRemoveSpec(), true
	}
	if len(parts) >= 1 && parts[0] == "status" {
		return toolboxStatusSpec(), true
	}
	if len(parts) >= 1 && parts[0] == "doctor" {
		return doctorSpec(), true
	}
	if len(parts) >= 1 && parts[0] == "catalog" {
		if mcpRemoteCatalogCommandModifies(parts) {
			return toolboxCatalogManageSpec(), true
		}
		return toolboxCatalogListSpec(), true
	}
	if len(parts) >= 2 && parts[0] == "workflow" {
		switch parts[1] {
		case "init", "add":
			return workflowInitSpec(), true
		case "list", "ls":
			return workflowListSpec(), true
		case "show":
			return workflowShowSpec(), true
		case "render":
			return workflowRenderSpec(), true
		case "run":
			return workflowRunSpec(), true
		case "config":
			if len(parts) >= 4 && parts[2] == "set" && parts[3] == "default-agent" {
				return workflowConfigSetDefaultAgentSpec(), true
			}
		}
	}
	if len(parts) >= 2 && parts[0] == "mcp" && parts[1] == "configure" {
		return mcpConfigureSpec(), true
	}
	if len(parts) >= 2 && parts[0] == "mcp" && parts[1] == "enable" {
		return mcpEnableSpec(), true
	}
	if len(parts) >= 2 && parts[0] == "mcp" && parts[1] == "disable" {
		return mcpDisableSpec(), true
	}
	if len(parts) >= 3 && parts[0] == "mcp" && parts[1] == "profile" && parts[2] == "set" {
		return mcpProfileSetSpec(), true
	}
	if len(parts) >= 3 && parts[0] == "mcp" && parts[1] == "profile" && parts[2] == "default" {
		return mcpProfileDefaultSpec(), true
	}
	if len(parts) >= 2 && parts[0] == "mcp" {
		switch parts[1] {
		case "schema":
			return schemaSpec(), true
		case "sync":
			return mcpRemoteSyncSpec(), true
		case "rename":
			return mcpRemoteRenameSpec(), true
		case "ls", "list":
			return mcpRemoteListSpec(), true
		case "show":
			return mcpRemoteShowSpec(), true
		case "catalog", "available":
			if mcpRemoteCatalogCommandModifies(parts) {
				return mcpRemoteCatalogManageSpec(), true
			}
			return mcpRemoteCatalogListSpec(), true
		case "defaults", "default-args":
			if len(parts) < 3 {
				return policy.CommandSpec{}, false
			}
			switch parts[2] {
			case "ls", "list", "show":
				return mcpRemoteDefaultsListSpec(), true
			case "set":
				return mcpRemoteDefaultsSetSpec(), true
			case "remove", "rm", "unset":
				return mcpRemoteDefaultsRemoveSpec(), true
			}
		case "auth":
			if len(parts) < 3 {
				return policy.CommandSpec{}, false
			}
			switch parts[2] {
			case "login", "connect":
				return mcpRemoteAuthLoginSpec(), true
			case "set":
				return mcpRemoteAuthSetSpec(), true
			case "remove", "rm":
				return mcpRemoteAuthRemoveSpec(), true
			case "status":
				return mcpRemoteAuthStatusSpec(), true
			}
		}
	}
	return policy.CommandSpec{}, false
}

func actionExecutionContext(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) actions.Context {
	return actions.Context{
		Context:     ctx,
		Credentials: store,
		HTTPClient:  opts.httpClient,
		Profile:     opts.profile,
		Account:     "default",
		Provider:    provider.ID,
		ProviderURL: opts.providerURL[provider.ID],
		ProviderAPI: opts.providerAPI[provider.ID],
		ToolmuxdURL: opts.toolmuxdURL,
		ReadFile:    os.ReadFile,
		OpenBrowser: opts.openBrowser,
	}
}

func selectString(cmd *cobra.Command) func(context.Context, actions.SelectStringRequest) (string, bool, error) {
	return func(ctx context.Context, request actions.SelectStringRequest) (string, bool, error) {
		if len(request.Options) == 0 {
			return "", false, nil
		}
		selected := request.Options[0].Value
		options := make([]huh.Option[string], 0, len(request.Options))
		for _, option := range request.Options {
			options = append(options, huh.NewOption(option.Label, option.Value))
		}
		height := request.Height
		if height <= 0 {
			height = min(len(options)+4, 12)
		}
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title(request.Title).
				Description(request.Description).
				Options(options...).
				Value(&selected).
				Height(height).
				Filtering(request.Filtering),
		)).
			WithTheme(huh.ThemeCharm()).
			WithInput(cmd.InOrStdin()).
			WithOutput(cmd.ErrOrStderr()).
			WithWidth(terminalWidth(cmd.ErrOrStderr())).
			WithHeight(height + 5)
		if err := form.RunWithContext(ctx); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return "", false, nil
			}
			return "", false, err
		}
		return selected, true, nil
	}
}

func selectInteger(cmd *cobra.Command) func(context.Context, actions.SelectIntegerRequest) (int, bool, error) {
	return func(ctx context.Context, request actions.SelectIntegerRequest) (int, bool, error) {
		if len(request.Options) == 0 {
			return 0, false, nil
		}
		selected := request.Options[0].Value
		options := make([]huh.Option[int], 0, len(request.Options))
		for _, option := range request.Options {
			options = append(options, huh.NewOption(option.Label, option.Value))
		}
		height := request.Height
		if height <= 0 {
			height = min(len(options)+4, 14)
		}
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().
				Title(request.Title).
				Description(request.Description).
				Options(options...).
				Value(&selected).
				Height(height).
				Filtering(request.Filtering),
		)).
			WithTheme(huh.ThemeCharm()).
			WithInput(cmd.InOrStdin()).
			WithOutput(cmd.ErrOrStderr()).
			WithWidth(terminalWidth(cmd.ErrOrStderr())).
			WithHeight(height + 5)
		if err := form.RunWithContext(ctx); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return 0, false, nil
			}
			return 0, false, err
		}
		return selected, true, nil
	}
}

func commandContext(cmd *cobra.Command) context.Context {
	ctx := cmd.Context()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func interactiveCommand(cmd *cobra.Command, opts *options) bool {
	return opts.output == "table" && isTerminal(cmd.OutOrStdout()) && isTerminal(cmd.ErrOrStderr()) && isInputTerminal(cmd.InOrStdin())
}

func markdownForOutput(w io.Writer, opts *options, source string) string {
	if !isTerminal(w) || os.Getenv("TERM") == "dumb" {
		return source
	}
	width := terminalWidth(w)
	theme := output.MarkdownDark
	if !colorEnabled(opts.color, true) {
		theme = output.MarkdownPlain
	} else {
		terminal := termenv.NewOutput(w, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(true))
		if !terminal.HasDarkBackground() {
			theme = output.MarkdownLight
		}
	}
	rendered, err := output.RenderMarkdown(source, output.MarkdownOptions{
		Width: width,
		Theme: theme,
	})
	if err != nil {
		return source
	}
	return strings.TrimRight(rendered, "\n")
}

func humanOutputOptions(cmd *cobra.Command, opts *options) output.Options {
	w := cmd.OutOrStdout()
	tty := isTerminal(w)
	terminal := termenv.NewOutput(w, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(tty))
	color := colorEnabled(opts.color, tty)
	darkBackground := true
	if tty {
		darkBackground = terminal.HasDarkBackground()
	}
	return output.Options{
		Color:          color,
		DarkBackground: darkBackground,
		Width:          terminalWidth(w),
	}
}

func writePossiblyPaged(cmd *cobra.Command, opts *options, content string) error {
	text := strings.TrimRight(content, "\n") + "\n"
	if shouldPage(cmd.OutOrStdout(), opts, text) {
		pager, ok := pagerCommand()
		if ok {
			return runPager(cmd, pager, text)
		}
	}
	fmt.Fprint(cmd.OutOrStdout(), text)
	return nil
}

func shouldPage(w io.Writer, opts *options, content string) bool {
	switch strings.ToLower(strings.TrimSpace(opts.pager)) {
	case "never":
		return false
	case "always":
		return isTerminal(w)
	default:
		return isTerminal(w) && lineCount(content) > terminalHeight(w)-2
	}
}

func pagerCommand() (string, bool) {
	if pager := strings.TrimSpace(os.Getenv("PAGER")); pager != "" {
		return pager, true
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less -R", true
	}
	return "", false
}

func runPager(cmd *cobra.Command, pager, content string) error {
	name, args := pagerShellCommand(pager)
	// #nosec G204 -- the pager is an explicit user-controlled terminal command.
	process := exec.CommandContext(cmd.Context(), name, args...)
	process.Stdin = strings.NewReader(content)
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	return process.Run()
}

func pagerShellCommand(pager string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", pager}
	}
	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-c", pager}
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func writeActionResult(cmd *cobra.Command, opts *options, execCtx actions.Context, result any) error {
	for {
		if err := writeActionResultOnce(cmd, opts, result); err != nil {
			return err
		}
		follower, ok := result.(actions.FollowRenderable)
		if !ok {
			return nil
		}
		next, keepGoing, err := follower.Follow(execCtx)
		if err != nil {
			return err
		}
		if !keepGoing || next == nil {
			return nil
		}
		if opts.output == "table" {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		result = next
	}
}

func writeActionResultOnce(cmd *cobra.Command, opts *options, result any) error {
	if result == nil {
		return nil
	}
	switch opts.output {
	case "json", "yaml":
		return writeValue(cmd, opts, result, nil)
	case "table":
		if opener, ok := result.(actions.BrowserOpenRenderable); ok && opener.BrowserURL() != "" && !opener.BrowserURLOnly() {
			if err := openURL(opener.BrowserURL()); err != nil {
				return fmt.Errorf("open %q: %w", opener.BrowserURL(), err)
			}
		}
		if markdown, ok := result.(actions.MarkdownRenderable); ok {
			source := markdown.MarkdownSource()
			rendered := markdownForOutput(cmd.OutOrStdout(), opts, source)
			if truncated, unknown := markdown.MarkdownTruncated(); truncated {
				rendered += fmt.Sprintf("\n\n%s\n", output.ToneText(humanOutputOptions(cmd, opts), output.ToneWarning, fmt.Sprintf("truncated: %d unknown blocks", unknown)))
			}
			return writePossiblyPaged(cmd, opts, rendered)
		}
		if text, ok := result.(actions.TextRenderable); ok {
			return writePossiblyPaged(cmd, opts, text.Text())
		}
		if table, ok := result.(actions.TableRenderable); ok {
			output.RenderTable(cmd.OutOrStdout(), humanOutputOptions(cmd, opts), table.Table(humanOutputOptions(cmd, opts)))
			return nil
		}
		return writeValue(cmd, opts, result, nil)
	default:
		return fmt.Errorf("unsupported output format %q", opts.output)
	}
}

func writeValue(cmd *cobra.Command, opts *options, value any, table func(io.Writer)) error {
	switch opts.output {
	case "json":
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	case "yaml":
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		defer encoder.Close()
		return encoder.Encode(value)
	case "table":
		if table != nil {
			table(cmd.OutOrStdout())
			return nil
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	default:
		return fmt.Errorf("unsupported output format %q", opts.output)
	}
}

type connectUI struct {
	w            io.Writer
	output       *termenv.Output
	styles       connectStyles
	spinner      spinner.Spinner
	mu           sync.Mutex
	active       *connectProgressHandle
	interactive  bool
	clearOnWrite bool
}

type semanticTone string

const (
	toneInfo    semanticTone = "info"
	toneSuccess semanticTone = "success"
	toneWarning semanticTone = "warning"
)

type semanticPalette struct {
	info    string
	success string
	warning string
	muted   string
}

type connectStyles struct {
	info    lipgloss.Style
	success lipgloss.Style
	warning lipgloss.Style
	muted   lipgloss.Style
	spinner lipgloss.Style
}

func newConnectUI(cmd *cobra.Command, opts *options) *connectUI {
	stderr := cmd.ErrOrStderr()
	interactive := opts.output == "table" && isTerminal(cmd.OutOrStdout()) && isTerminal(stderr)
	terminal := termenv.NewOutput(stderr, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(interactive))
	color := interactive && colorEnabled(opts.color, interactive)
	palette := semanticPaletteFor(terminal, interactive)
	return &connectUI{
		w:            stderr,
		output:       terminal,
		styles:       newConnectStyles(color, palette),
		spinner:      spinner.Line,
		interactive:  interactive,
		clearOnWrite: interactive,
	}
}

func (ui *connectUI) Start(message string) actions.ProgressHandle {
	if !ui.interactive {
		return noopCLIProgressHandle{}
	}
	handle := &connectProgressHandle{
		ui:      ui,
		message: strings.TrimSpace(message),
		done:    make(chan struct{}),
	}
	ui.mu.Lock()
	ui.stopLocked()
	ui.active = handle
	ui.mu.Unlock()
	go handle.run()
	return handle
}

func (ui *connectUI) Status(message string) {
	if !ui.interactive {
		return
	}
	ui.writeLine(toneInfo, "i", message)
}

func (ui *connectUI) Warn(message string) {
	if !ui.interactive {
		return
	}
	ui.writeLine(toneWarning, "!", message)
}

func (ui *connectUI) Done(message string) {
	if !ui.interactive {
		return
	}
	ui.writeLine(toneSuccess, "+", message)
}

func (ui *connectUI) status(format string, args ...any) {
	ui.Status(fmt.Sprintf(format, args...))
}

func (ui *connectUI) warn(format string, args ...any) {
	ui.Warn(fmt.Sprintf(format, args...))
}

func (ui *connectUI) writeLine(tone semanticTone, marker, message string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.stopLocked()
	fmt.Fprintf(ui.w, "%s %s\n", ui.marker(tone, marker), strings.TrimSpace(message))
}

func (ui *connectUI) stopLocked() {
	if ui.active != nil {
		ui.active.close()
		ui.active = nil
	}
	ui.clearLineLocked()
}

func (ui *connectUI) clearLineLocked() {
	if !ui.clearOnWrite {
		return
	}
	fmt.Fprint(ui.w, "\r")
	if ui.output != nil {
		ui.output.ClearLine()
	}
}

func (ui *connectUI) marker(tone semanticTone, value string) string {
	switch tone {
	case toneInfo:
		return ui.styles.info.Render(value)
	case toneSuccess:
		return ui.styles.success.Render(value)
	case toneWarning:
		return ui.styles.warning.Render(value)
	default:
		return value
	}
}

type connectProgressHandle struct {
	ui      *connectUI
	message string
	frame   int
	done    chan struct{}
	once    sync.Once
}

func (handle *connectProgressHandle) run() {
	handle.render()
	interval := handle.ui.spinner.FPS
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-handle.done:
			return
		case <-ticker.C:
			handle.ui.mu.Lock()
			if handle.ui.active != handle {
				handle.ui.mu.Unlock()
				return
			}
			handle.frame++
			handle.renderLocked()
			handle.ui.mu.Unlock()
		}
	}
}

func (handle *connectProgressHandle) Update(message string) {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active != handle {
		return
	}
	handle.message = strings.TrimSpace(message)
	handle.renderLocked()
}

func (handle *connectProgressHandle) Stop() {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active == handle {
		handle.close()
		handle.ui.active = nil
		handle.ui.clearLineLocked()
		return
	}
	handle.close()
}

func (handle *connectProgressHandle) Warn(message string) {
	handle.finish(toneWarning, "!", message)
}

func (handle *connectProgressHandle) Done(message string) {
	handle.finish(toneSuccess, "+", message)
}

func (handle *connectProgressHandle) finish(tone semanticTone, marker, message string) {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active != handle {
		handle.close()
		return
	}
	handle.close()
	handle.ui.active = nil
	handle.ui.clearLineLocked()
	fmt.Fprintf(handle.ui.w, "%s %s\n", handle.ui.marker(tone, marker), strings.TrimSpace(message))
}

func (handle *connectProgressHandle) render() {
	handle.ui.mu.Lock()
	defer handle.ui.mu.Unlock()
	if handle.ui.active != handle {
		return
	}
	handle.renderLocked()
}

func (handle *connectProgressHandle) renderLocked() {
	handle.ui.clearLineLocked()
	frames := handle.ui.spinner.Frames
	frame := "-"
	if len(frames) > 0 {
		frame = frames[handle.frame%len(frames)]
	}
	fmt.Fprintf(handle.ui.w, "%s %s", handle.ui.styles.spinner.Render(frame), handle.ui.styles.muted.Render(handle.message))
}

func (handle *connectProgressHandle) close() {
	handle.once.Do(func() {
		close(handle.done)
	})
}

type noopCLIProgressHandle struct{}

func (noopCLIProgressHandle) Update(string) {}
func (noopCLIProgressHandle) Stop()         {}
func (noopCLIProgressHandle) Warn(string)   {}
func (noopCLIProgressHandle) Done(string)   {}

func newConnectStyles(color bool, palette semanticPalette) connectStyles {
	if !color {
		style := lipgloss.NewStyle()
		return connectStyles{
			info:    style,
			success: style,
			warning: style,
			muted:   style,
			spinner: style,
		}
	}
	return connectStyles{
		info:    lipgloss.NewStyle().Foreground(lipgloss.Color(palette.info)),
		success: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.success)),
		warning: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.warning)),
		muted:   lipgloss.NewStyle().Foreground(lipgloss.Color(palette.muted)),
		spinner: lipgloss.NewStyle().Foreground(lipgloss.Color(palette.info)),
	}
}

func semanticPaletteFor(output *termenv.Output, interactive bool) semanticPalette {
	if interactive && output != nil && !output.HasDarkBackground() {
		return semanticPalette{
			info:    "#0969da",
			success: "#1a7f37",
			warning: "#9a6700",
			muted:   "#6e7781",
		}
	}
	return semanticPalette{
		info:    "#7dd3fc",
		success: "#86efac",
		warning: "#facc15",
		muted:   "#8ea0b8",
	}
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func isInputTerminal(r io.Reader) bool {
	file, ok := r.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func terminalWidth(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 100
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return 100
	}
	if width < 40 {
		return 40
	}
	return width
}

func terminalHeight(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 24
	}
	_, height, err := term.GetSize(int(file.Fd()))
	if err != nil || height <= 0 {
		return 24
	}
	if height < 10 {
		return 10
	}
	return height
}

func colorAllowed() bool {
	if termenv.EnvNoColor() {
		return false
	}
	if colorForced() {
		return true
	}
	return os.Getenv("CLICOLOR") != "0" && os.Getenv("TERM") != "dumb"
}

func colorEnabled(policy string, tty bool) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "always":
		return true
	case "never":
		return false
	default:
		return colorAllowed() && (tty || colorForced())
	}
}

func colorForced() bool {
	force := os.Getenv("CLICOLOR_FORCE")
	return force != "" && force != "0"
}

var openURL = openBrowser

func openBrowser(rawURL string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{rawURL}
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		command = "xdg-open"
		args = []string{rawURL}
	}
	// #nosec G204 -- the URL is generated by toolmuxd or selected from visible command output.
	return exec.Command(command, args...).Start()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
