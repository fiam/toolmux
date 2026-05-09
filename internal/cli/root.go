package cli

import (
	"bytes"
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
	"strings"
	"time"

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
	output      string
	color       string
	pager       string
	profile     string
	account     string
	policy      string
	readOnly    bool
	credentials func() (credentials.Store, error)
	httpClient  *http.Client
	providerURL map[string]string
	providerAPI map[string]string
	toolmuxdURL string
}

type Dependencies struct {
	Credentials credentials.Store
	HTTPClient  *http.Client
	Env         func(string) string
	ProviderURL map[string]string
	ProviderAPI map[string]string
	ToolmuxdURL string
}

func NewRootCommand() *cobra.Command {
	return NewRootCommandWithDeps(Dependencies{})
}

func NewRootCommandWithDeps(deps Dependencies) *cobra.Command {
	opts := &options{output: "table", color: "auto", pager: "auto", profile: "default"}
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
	configureProviders(opts, env)

	root := &cobra.Command{
		Use:           "toolmux",
		Short:         "A local-first mega CLI for SaaS services",
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
		},
	}
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", "table", "output format: table, json, yaml")
	root.PersistentFlags().StringVar(&opts.color, "color", "auto", "color output: auto, always, never")
	root.PersistentFlags().StringVar(&opts.pager, "pager", "auto", "pager behavior: auto, always, never")
	root.PersistentFlags().StringVar(&opts.profile, "profile", "default", "Toolmux profile")
	root.PersistentFlags().StringVar(&opts.account, "account", "", "provider account or workspace")
	root.PersistentFlags().StringVar(&opts.policy, "policy", "", "policy file path")
	root.PersistentFlags().BoolVar(&opts.readOnly, "read-only", false, "deny actions with remote or local write effects")

	root.AddCommand(versionCommand())
	root.AddCommand(connectCommand(opts))
	root.AddCommand(disconnectCommand(opts))
	root.AddCommand(statusCommand(opts))
	root.AddCommand(doctorCommand(opts))
	root.AddCommand(connectionsCommand())
	root.AddCommand(policyCommand(opts))
	registerActionCommands(root, opts)

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

type connectProviderOptions struct {
	AuthURLOnly bool
	NoBrowser   bool
	Timeout     time.Duration
}

type disconnectProviderOptions struct {
	Yes bool
}

func connectCommand(opts *options) *cobra.Command {
	var authURLOnly bool
	var noBrowser bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "connect <provider>",
		Short: "Connect a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, ok := providers.Lookup(args[0])
			if !ok {
				return fmt.Errorf("unknown provider %q", args[0])
			}
			if err := authorize(cmd, opts, providerConnectSpec(provider, args[0]), nil); err != nil {
				return err
			}
			if provider.AuthMode == "brokered_local_custody" {
				return connectBrokeredProvider(cmd, opts, provider, connectProviderOptions{
					AuthURLOnly: authURLOnly,
					NoBrowser:   noBrowser,
					Timeout:     timeout,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "connect %s: not implemented yet\n", provider.DisplayName)
			return nil
		},
	}
	cmd.Flags().BoolVar(&authURLOnly, "auth-url-only", false, "create session and print auth URL without polling")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print auth URL without opening a browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "OAuth completion timeout")
	return cmd
}

func disconnectCommand(opts *options) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "disconnect <provider>",
		Short: "Disconnect a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, ok := providers.Lookup(args[0])
			if !ok {
				return fmt.Errorf("unknown provider %q", args[0])
			}
			if err := authorize(cmd, opts, providerDisconnectSpec(provider, args[0]), nil); err != nil {
				return err
			}
			if provider.AuthMode == "brokered_local_custody" {
				return disconnectBrokeredProvider(cmd, opts, provider, disconnectProviderOptions{Yes: yes})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disconnect %s: not implemented yet\n", provider.DisplayName)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm remote token revocation and local credential deletion")
	return cmd
}

type oauthSessionResponse struct {
	SessionID   string                   `json:"session_id"`
	Provider    string                   `json:"provider"`
	Status      string                   `json:"status"`
	AuthURL     string                   `json:"auth_url"`
	RedirectURI string                   `json:"redirect_uri"`
	Error       string                   `json:"error"`
	Tokens      *credentials.OAuthTokens `json:"tokens"`
	ExpiresAt   time.Time                `json:"expires_at"`
}

func connectBrokeredProvider(cmd *cobra.Command, opts *options, provider providers.Provider, options connectProviderOptions) error {
	ui := newConnectUI(cmd, opts)
	serverURL := strings.TrimRight(opts.toolmuxdURL, "/")
	if serverURL == "" {
		return fmt.Errorf("toolmuxd URL is required")
	}
	ui.status("Creating %s OAuth session", provider.DisplayName)
	payload, err := json.Marshal(map[string]string{"provider": provider.ID, "profile": opts.profile, "account": opts.account})
	if err != nil {
		return err
	}
	// #nosec G107 -- toolmuxd URL is explicit local/deployment configuration.
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, serverURL+"/v1/oauth/sessions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := opts.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("create %s OAuth session: status %d: %s", provider.DisplayName, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var session oauthSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return err
	}
	if session.AuthURL == "" {
		return fmt.Errorf("toolmuxd did not return a %s authorization URL", provider.DisplayName)
	}
	ui.done("Created %s OAuth session", provider.DisplayName)
	fmt.Fprintf(cmd.OutOrStdout(), "open this URL to connect %s:\n%s\n", provider.DisplayName, session.AuthURL)
	if session.RedirectURI != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s redirect URI:\n%s\n", provider.DisplayName, session.RedirectURI)
	}
	if options.AuthURLOnly {
		return nil
	}
	if ui.interactive && !options.NoBrowser {
		if err := openURL(session.AuthURL); err != nil {
			ui.warn("Could not open browser automatically: %v", err)
		} else {
			ui.status("Opened browser for %s authorization", provider.DisplayName)
		}
	}
	deadline := time.Now().Add(options.Timeout)
	for time.Now().Before(deadline) {
		ui.spin("Waiting for " + provider.DisplayName + " authorization")
		polled, err := pollOAuthSession(cmd.Context(), opts, serverURL, session.SessionID)
		if err != nil {
			ui.stop()
			return err
		}
		switch polled.Status {
		case "complete":
			ui.done("%s authorization complete", provider.DisplayName)
			if polled.Tokens == nil {
				return fmt.Errorf("%s OAuth session completed without token handoff", strings.ToLower(provider.DisplayName))
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.SaveOAuthTokens(cmd.Context(), providerCredentialRef(opts, provider.ID), *polled.Tokens); err != nil {
				return err
			}
			workspace := polled.Tokens.Extra["workspace_name"]
			if workspace == "" {
				workspace = polled.Tokens.Extra["workspace_id"]
			}
			fmt.Fprintf(cmd.OutOrStdout(), "connected %s: %s\n", provider.DisplayName, firstNonEmpty(workspace, "default"))
			return nil
		case "failed", "expired":
			ui.stop()
			return fmt.Errorf("%s OAuth session %s: %s", strings.ToLower(provider.DisplayName), polled.Status, polled.Error)
		}
		select {
		case <-cmd.Context().Done():
			ui.stop()
			return cmd.Context().Err()
		case <-time.After(time.Second):
		}
	}
	ui.stop()
	return fmt.Errorf("timed out waiting for %s OAuth completion", provider.DisplayName)
}

func disconnectBrokeredProvider(cmd *cobra.Command, opts *options, provider providers.Provider, options disconnectProviderOptions) error {
	if !options.Yes {
		return fmt.Errorf("refusing to disconnect %s without --yes", provider.DisplayName)
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	ref := providerCredentialRef(opts, provider.ID)
	tokens, err := store.LoadOAuthTokens(cmd.Context(), ref)
	if err != nil {
		return err
	}
	serverURL := strings.TrimRight(opts.toolmuxdURL, "/")
	if serverURL != "" && tokens.AccessToken != "" {
		payload, err := json.Marshal(map[string]string{"token": tokens.AccessToken})
		if err != nil {
			return err
		}
		// #nosec G107 -- toolmuxd URL is explicit local/deployment configuration.
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, serverURL+"/v1/oauth/"+provider.ID+"/revoke", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := opts.httpClient.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if err := resp.Body.Close(); err != nil {
			return err
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("revoke %s token: status %d", provider.DisplayName, resp.StatusCode)
		}
	}
	if err := store.DeleteOAuthTokens(cmd.Context(), ref); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "disconnected %s\n", provider.DisplayName)
	return nil
}

func pollOAuthSession(ctx context.Context, opts *options, serverURL, sessionID string) (oauthSessionResponse, error) {
	// #nosec G107 -- toolmuxd URL is explicit local/deployment configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/v1/oauth/sessions/"+sessionID, nil)
	if err != nil {
		return oauthSessionResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := opts.httpClient.Do(req)
	if err != nil {
		return oauthSessionResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return oauthSessionResponse{}, fmt.Errorf("poll OAuth session: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var session oauthSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return oauthSessionResponse{}, err
	}
	return session, nil
}

func connectionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connections",
		Short: "Manage local provider connections",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List local connections",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "no connections configured")
		},
	})
	return cmd
}

type providerStatus = actions.ConnectionStatus

func statusCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status [provider...]",
		Short: "Show provider connection status",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := selectedProviders(args)
			if err != nil {
				return err
			}
			for _, provider := range selected {
				if err := authorize(cmd, opts, providerStatusSpec(provider), []string{provider.ID}); err != nil {
					return err
				}
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			statuses := make([]providerStatus, 0, len(selected))
			for _, provider := range selected {
				statuses = append(statuses, readProviderStatus(cmd.Context(), opts, store, provider))
			}
			return writeValue(cmd, opts, statuses, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(statuses))
				for _, status := range statuses {
					state := "disconnected"
					detail := firstNonEmpty(status.Message, "not connected")
					permissions := output.JoinList(status.Permissions)
					if status.Connected {
						state = "connected"
						detail = firstNonEmpty(status.WorkspaceName, status.WorkspaceID, status.TokenType, "connected")
					}
					rows = append(rows, []string{
						status.Provider,
						output.StatusBadge(human, state),
						output.Value(status.Account),
						output.Value(detail),
						permissions,
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Provider", "Status", "Account", "Details", "Permissions"},
					Rows:    rows,
					Empty:   "no providers selected",
				})
			})
		},
	}
}

func selectedProviders(args []string) ([]providers.Provider, error) {
	if len(args) == 0 {
		return providers.All(), nil
	}
	selected := make([]providers.Provider, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		provider, ok := providers.Lookup(arg)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q", arg)
		}
		if seen[provider.ID] {
			continue
		}
		seen[provider.ID] = true
		selected = append(selected, provider)
	}
	return selected, nil
}

type providerDiagnostic = actions.Diagnostic

func providerStatusSpec(provider providers.Provider) policy.CommandSpec {
	return policy.CommandSpec{
		ID:           provider.ID + ".status",
		Path:         []string{"status", provider.ID},
		Provider:     provider.ID,
		Resource:     string(actions.ResourceConnection),
		Action:       string(actions.VerbStatus),
		Effect:       string(actions.EffectRead),
		RemoteEffect: string(actions.EffectRead),
		LocalEffect:  string(actions.EffectNone),
	}
}

func providerDoctorSpec(provider providers.Provider) policy.CommandSpec {
	return policy.CommandSpec{
		ID:           provider.ID + ".doctor",
		Path:         []string{"doctor", provider.ID},
		Provider:     provider.ID,
		Resource:     string(actions.ResourceConnection),
		Action:       string(actions.VerbDiagnose),
		Effect:       string(actions.EffectRead),
		RemoteEffect: string(actions.EffectRead),
		LocalEffect:  string(actions.EffectNone),
	}
}

func providerConnectSpec(provider providers.Provider, pathProvider string) policy.CommandSpec {
	return policy.CommandSpec{
		ID:           provider.ID + ".connect",
		Path:         []string{"connect", pathProvider},
		Provider:     provider.ID,
		Resource:     string(actions.ResourceConnection),
		Action:       string(actions.VerbConnect),
		Effect:       string(actions.EffectWrite),
		RemoteEffect: string(actions.EffectNone),
		LocalEffect:  string(actions.EffectWrite),
		Risk:         []string{"credential-access"},
	}
}

func providerDisconnectSpec(provider providers.Provider, pathProvider string) policy.CommandSpec {
	return policy.CommandSpec{
		ID:           provider.ID + ".disconnect",
		Path:         []string{"disconnect", pathProvider},
		Provider:     provider.ID,
		Resource:     string(actions.ResourceConnection),
		Action:       string(actions.VerbDisconnect),
		Effect:       string(actions.EffectWrite),
		RemoteEffect: string(actions.EffectWrite),
		LocalEffect:  string(actions.EffectWrite),
		Risk:         []string{"credential-revoke"},
	}
}

func readProviderStatus(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) providerStatus {
	ref := providerCredentialRef(opts, provider.ID)
	status := providerStatus{
		Provider:    provider.ID,
		DisplayName: provider.DisplayName,
		Profile:     ref.Profile,
		Account:     ref.AccountID,
		Message:     "not connected",
	}
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return status
		}
		status.Message = err.Error()
		return status
	}
	status.Connected = true
	status.Message = ""
	status.TokenType = tokens.TokenType
	status.ExpiresAt = tokens.ExpiresAt
	status.Scopes = tokens.Scopes
	status.Permissions = providerPermissions(provider, tokens)
	status.Extra = tokens.Extra
	status.WorkspaceID = tokens.Extra["workspace_id"]
	status.WorkspaceName = tokens.Extra["workspace_name"]
	if site := tokens.Extra["site_name"]; status.WorkspaceName == "" && site != "" {
		status.WorkspaceName = site
	}
	return status
}

func doctorCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [provider...]",
		Short: "Check Toolmux setup and provider connections",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := selectedProviders(args)
			if err != nil {
				return err
			}
			diagnostics := coreDiagnostics(opts)
			if diagnosticsHaveFailure(diagnostics) {
				return writeDiagnostics(cmd, opts, diagnostics)
			}
			for _, provider := range selected {
				if err := authorize(cmd, opts, providerDoctorSpec(provider), []string{provider.ID}); err != nil {
					return err
				}
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
			for _, provider := range selected {
				diagnostics = append(diagnostics, doctorProvider(cmd.Context(), opts, store, provider)...)
			}
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

func doctorProvider(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) []providerDiagnostic {
	diagnostics, status := genericProviderDiagnostics(ctx, opts, store, provider)
	if provider.Diagnostics != nil {
		actionCtx := actionExecutionContext(ctx, opts, store, provider)
		diagnostics = append(diagnostics, provider.Diagnostics(ctx, actionCtx, status)...)
	}
	return diagnostics
}

func genericProviderDiagnostics(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) ([]providerDiagnostic, providerStatus) {
	status := readProviderStatus(ctx, opts, store, provider)
	if !status.Connected {
		return []providerDiagnostic{{
			Provider:    provider.ID,
			Check:       "connection",
			Status:      "warn",
			Message:     firstNonEmpty(status.Message, "not connected"),
			Remediation: "Run `toolmux connect " + provider.ID + "`.",
		}}, status
	}
	diagnostics := []providerDiagnostic{{
		Provider: provider.ID,
		Check:    "connection",
		Status:   "ok",
		Message:  "local token bundle found for " + status.Account,
	}}
	if len(status.Permissions) == 0 {
		diagnostics = append(diagnostics, providerDiagnostic{
			Provider:    provider.ID,
			Check:       "permissions",
			Status:      "warn",
			Message:     "no recorded permissions",
			Remediation: "Reconnect the provider so Toolmux can record granted scopes or capabilities.",
		})
		return diagnostics, status
	}
	missing := missingProviderPermissions(provider, status.Permissions)
	if len(missing) > 0 {
		diagnostics = append(diagnostics, providerDiagnostic{
			Provider:    provider.ID,
			Check:       "permissions",
			Status:      "warn",
			Message:     "missing " + strings.Join(missing, ","),
			Remediation: "Reconnect the provider to grant the missing permissions.",
		})
		return diagnostics, status
	}
	diagnostics = append(diagnostics, providerDiagnostic{
		Provider: provider.ID,
		Check:    "permissions",
		Status:   "ok",
		Message:  strings.Join(status.Permissions, ","),
	})
	return diagnostics, status
}

func providerPermissions(provider providers.Provider, tokens credentials.OAuthTokens) []string {
	if len(tokens.Scopes) > 0 {
		return append([]string(nil), tokens.Scopes...)
	}
	if provider.DefaultPermissions != nil {
		return provider.DefaultPermissions()
	}
	return nil
}

func missingProviderPermissions(provider providers.Provider, permissions []string) []string {
	required := requiredProviderPermissions(provider)
	if len(required) == 0 {
		return nil
	}
	have := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		have[permission] = true
	}
	var missing []string
	for _, permission := range required {
		if !have[permission] {
			missing = append(missing, permission)
		}
	}
	return missing
}

func requiredProviderPermissions(provider providers.Provider) []string {
	seen := map[string]bool{}
	var required []string
	for _, spec := range providers.ActionSpecs(provider) {
		for _, scope := range spec.Scopes {
			if seen[scope] {
				continue
			}
			seen[scope] = true
			required = append(required, scope)
		}
	}
	return required
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
			Headers: []string{"Provider", "Check", "Status", "Message", "Remediation"},
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
				spec, ok := specForCommand(commandLine)
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
	specs := providers.CommandSpecs()
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
		Account:    opts.account,
		OutputMode: opts.output,
		Args:       map[string]any{"argv": args},
	}
	return engine.Authorize(inv), nil
}

func specForCommand(commandLine string) (policy.CommandSpec, bool) {
	parts := strings.Fields(commandLine)
	if spec, ok := rootSpecForCommandParts(parts); ok {
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
	if len(parts) < 2 {
		return policy.CommandSpec{}, false
	}
	provider, ok := providers.Lookup(parts[1])
	if !ok {
		return policy.CommandSpec{}, false
	}
	switch parts[0] {
	case "status":
		return providerStatusSpec(provider), true
	case "doctor":
		return providerDoctorSpec(provider), true
	case "connect":
		return providerConnectSpec(provider, parts[1]), true
	case "disconnect":
		return providerDisconnectSpec(provider, parts[1]), true
	default:
		return policy.CommandSpec{}, false
	}
}

func providerCredentialRef(opts *options, provider string) credentials.ConnectionRef {
	account := strings.TrimSpace(opts.account)
	if account == "" {
		account = "default"
	}
	return credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  provider,
		AccountID: account,
	}
}

func actionExecutionContext(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) actions.Context {
	account := strings.TrimSpace(opts.account)
	if account == "" {
		account = "default"
	}
	return actions.Context{
		Context:     ctx,
		Credentials: store,
		HTTPClient:  opts.httpClient,
		Profile:     opts.profile,
		Account:     account,
		Provider:    provider.ID,
		ProviderURL: opts.providerURL[provider.ID],
		ProviderAPI: opts.providerAPI[provider.ID],
		ToolmuxdURL: opts.toolmuxdURL,
		ReadFile:    os.ReadFile,
		OpenBrowser: openURL,
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
	w           io.Writer
	output      *termenv.Output
	palette     semanticPalette
	interactive bool
	color       bool
	active      bool
	frame       int
}

type semanticTone string

const (
	toneInfo    semanticTone = "info"
	toneSuccess semanticTone = "success"
	toneWarning semanticTone = "warning"
)

type semanticPalette struct {
	info    termenv.Color
	success termenv.Color
	warning termenv.Color
}

func newConnectUI(cmd *cobra.Command, opts *options) *connectUI {
	stderr := cmd.ErrOrStderr()
	interactive := opts.output == "table" && isTerminal(cmd.OutOrStdout()) && isTerminal(stderr)
	output := termenv.NewOutput(stderr, termenv.WithProfile(termenv.EnvColorProfile()), termenv.WithTTY(interactive))
	return &connectUI{
		w:           stderr,
		output:      output,
		palette:     semanticPaletteFor(output, interactive),
		interactive: interactive,
		color:       interactive && colorEnabled(opts.color, interactive),
	}
}

func (ui *connectUI) status(format string, args ...any) {
	if !ui.interactive {
		return
	}
	ui.stop()
	fmt.Fprintf(ui.w, "%s %s\n", ui.marker(toneInfo, "i"), fmt.Sprintf(format, args...))
}

func (ui *connectUI) warn(format string, args ...any) {
	if !ui.interactive {
		return
	}
	ui.stop()
	fmt.Fprintf(ui.w, "%s %s\n", ui.marker(toneWarning, "!"), fmt.Sprintf(format, args...))
}

func (ui *connectUI) done(format string, args ...any) {
	if !ui.interactive {
		return
	}
	ui.stop()
	fmt.Fprintf(ui.w, "%s %s\n", ui.marker(toneSuccess, "+"), fmt.Sprintf(format, args...))
}

func (ui *connectUI) spin(message string) {
	if !ui.interactive {
		return
	}
	frames := []string{"|", "/", "-", "\\"}
	frame := frames[ui.frame%len(frames)]
	ui.frame++
	ui.active = true
	fmt.Fprintf(ui.w, "\r%s %s", ui.marker(toneInfo, frame), message)
}

func (ui *connectUI) stop() {
	if !ui.interactive || !ui.active {
		return
	}
	fmt.Fprint(ui.w, "\r")
	ui.output.ClearLine()
	ui.active = false
}

func (ui *connectUI) marker(tone semanticTone, value string) string {
	if !ui.color {
		return value
	}
	switch tone {
	case toneInfo:
		return termenv.String(value).Foreground(ui.palette.info).String()
	case toneSuccess:
		return termenv.String(value).Foreground(ui.palette.success).String()
	case toneWarning:
		return termenv.String(value).Foreground(ui.palette.warning).String()
	default:
		return value
	}
}

func semanticPaletteFor(output *termenv.Output, interactive bool) semanticPalette {
	profile := termenv.EnvColorProfile()
	if output != nil {
		profile = output.Profile
	}
	if interactive && output != nil && !output.HasDarkBackground() {
		return semanticPalette{
			info:    profile.Color("#0969da"),
			success: profile.Color("#1a7f37"),
			warning: profile.Color("#9a6700"),
		}
	}
	return semanticPalette{
		info:    profile.Color("#7dd3fc"),
		success: profile.Color("#86efac"),
		warning: profile.Color("#facc15"),
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
