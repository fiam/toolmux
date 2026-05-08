package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/version"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type options struct {
	output      string
	profile     string
	account     string
	policy      string
	credentials func() (credentials.Store, error)
}

type Dependencies struct {
	Credentials credentials.Store
}

func NewRootCommand() *cobra.Command {
	return NewRootCommandWithDeps(Dependencies{})
}

func NewRootCommandWithDeps(deps Dependencies) *cobra.Command {
	opts := &options{output: "table", profile: "default"}
	opts.credentials = func() (credentials.Store, error) {
		if deps.Credentials != nil {
			return deps.Credentials, nil
		}
		return credentials.NewKeyringStore(credentials.KeyringConfig{})
	}

	root := &cobra.Command{
		Use:   "toolmux",
		Short: "A local-first mega CLI for SaaS services",
	}
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", "table", "output format: table, json, yaml")
	root.PersistentFlags().StringVar(&opts.profile, "profile", "default", "Toolmux profile")
	root.PersistentFlags().StringVar(&opts.account, "account", "", "provider account or workspace")
	root.PersistentFlags().StringVar(&opts.policy, "policy", "", "policy file path")

	root.AddCommand(versionCommand())
	root.AddCommand(connectCommand(opts))
	root.AddCommand(disconnectCommand(opts))
	root.AddCommand(statusCommand(opts))
	root.AddCommand(doctorCommand(opts))
	root.AddCommand(connectionsCommand())
	root.AddCommand(policyCommand(opts))
	registerProviderCommands(root, opts)

	return root
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "toolmux %s (%s, %s)\n", version.Version, version.Commit, version.Date)
		},
	}
}

func connectCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "connect <provider>",
		Short: "Connect a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, ok := providers.Lookup(args[0])
			if !ok {
				return fmt.Errorf("unknown provider %q", args[0])
			}
			spec := policy.CommandSpec{
				ID:       provider.ID + ".connect",
				Path:     []string{"connect", args[0]},
				Provider: provider.ID,
				Resource: "connection",
				Action:   "connect",
				Effect:   "write",
				Risk:     []string{"credential-access"},
			}
			if err := authorize(cmd, opts, spec, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "connect %s: not implemented yet\n", provider.DisplayName)
			return nil
		},
	}
}

func disconnectCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect <provider>",
		Short: "Disconnect a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, ok := providers.Lookup(args[0])
			if !ok {
				return fmt.Errorf("unknown provider %q", args[0])
			}
			spec := policy.CommandSpec{
				ID:       provider.ID + ".disconnect",
				Path:     []string{"disconnect", args[0]},
				Provider: provider.ID,
				Resource: "connection",
				Action:   "disconnect",
				Effect:   "write",
				Risk:     []string{"credential-revoke"},
			}
			if err := authorize(cmd, opts, spec, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disconnect %s: not implemented yet\n", provider.DisplayName)
			return nil
		},
	}
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

type providerStatus struct {
	Provider    string            `json:"provider"`
	DisplayName string            `json:"display_name"`
	Profile     string            `json:"profile"`
	Account     string            `json:"account"`
	Connected   bool              `json:"connected"`
	TokenType   string            `json:"token_type,omitempty"`
	ExpiresAt   time.Time         `json:"expires_at,omitzero"`
	Scopes      []string          `json:"scopes,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Message     string            `json:"message,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

type providerDiagnostic struct {
	Provider    string            `json:"provider,omitempty"`
	Check       string            `json:"check"`
	Status      string            `json:"status"`
	Message     string            `json:"message"`
	Remediation string            `json:"remediation,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

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
				for _, status := range statuses {
					state := "disconnected"
					detail := firstNonEmpty(status.Message, "not connected")
					permissions := "-"
					if status.Connected {
						state = "connected"
						detail = firstNonEmpty(status.TokenType, "connected")
						if len(status.Permissions) > 0 {
							permissions = strings.Join(status.Permissions, ",")
						}
					}
					fmt.Fprintf(w, "%-12s %-12s %-16s %-16s %s\n", status.Provider, state, status.Account, detail, permissions)
				}
			})
		},
	}
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
					Remediation: "Check OS credential store availability or use a supported keyring backend.",
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

func selectedProviders(args []string) ([]providers.Provider, error) {
	if len(args) == 0 {
		return providers.Initial(), nil
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

func providerStatusSpec(provider providers.Provider) policy.CommandSpec {
	return policy.CommandSpec{
		ID:       provider.ID + ".status",
		Path:     []string{"status", provider.ID},
		Provider: provider.ID,
		Resource: "connection",
		Action:   "status",
		Effect:   "read",
	}
}

func providerDoctorSpec(provider providers.Provider) policy.CommandSpec {
	return policy.CommandSpec{
		ID:       provider.ID + ".doctor",
		Path:     []string{"doctor", provider.ID},
		Provider: provider.ID,
		Resource: "connection",
		Action:   "diagnose",
		Effect:   "read",
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
	status.Permissions = append([]string(nil), tokens.Scopes...)
	status.Extra = tokens.Extra
	return status
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

func diagnosticsHaveFailure(diagnostics []providerDiagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Status == "fail" {
			return true
		}
	}
	return false
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
	status := readProviderStatus(ctx, opts, store, provider)
	if !status.Connected {
		return []providerDiagnostic{{
			Provider:    provider.ID,
			Check:       "connection",
			Status:      "warn",
			Message:     firstNonEmpty(status.Message, "not connected"),
			Remediation: "Run `toolmux connect " + provider.ID + "`.",
		}}
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
			Message:     "no recorded scopes or capabilities",
			Remediation: "Reconnect the provider so Toolmux can record granted permissions.",
		})
		return diagnostics
	}
	diagnostics = append(diagnostics, providerDiagnostic{
		Provider: provider.ID,
		Check:    "permissions",
		Status:   "ok",
		Message:  strings.Join(status.Permissions, ","),
	})
	return diagnostics
}

func writeDiagnostics(cmd *cobra.Command, opts *options, diagnostics []providerDiagnostic) error {
	return writeValue(cmd, opts, diagnostics, func(w io.Writer) {
		for _, diagnostic := range diagnostics {
			target := firstNonEmpty(diagnostic.Provider, "toolmux")
			fmt.Fprintf(w, "%-12s %-18s %-5s %s\n", target, diagnostic.Check, diagnostic.Status, diagnostic.Message)
			if diagnostic.Remediation != "" {
				fmt.Fprintf(w, "%-36s %s\n", "", diagnostic.Remediation)
			}
		}
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

func registerProviderCommands(root *cobra.Command, opts *options) {
	nodes := map[string]*cobra.Command{}
	for _, spec := range providers.CommandSpecs() {
		if rootOwnedPath(spec.Path) {
			continue
		}
		parent := root
		var prefix []string
		for i, part := range spec.Path {
			prefix = append(prefix, part)
			key := strings.Join(prefix, " ")
			node := nodes[key]
			if node == nil {
				node = &cobra.Command{
					Use:                part,
					Short:              "Policy-aware command group",
					FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
				}
				nodes[key] = node
				parent.AddCommand(node)
			}
			parent = node
			if i == len(spec.Path)-1 {
				leafSpec := spec
				node.RunE = func(cmd *cobra.Command, args []string) error {
					if err := authorize(cmd, opts, leafSpec, args); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s: not implemented yet\n", leafSpec.ID)
					return nil
				}
			}
		}
	}
}

func rootOwnedPath(path []string) bool {
	return len(path) > 0 && (path[0] == "status" || path[0] == "doctor")
}

func writeCatalog(cmd *cobra.Command, opts *options) error {
	specs := providers.CommandSpecs()
	if opts.output == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(specs)
	}
	for _, spec := range specs {
		fmt.Fprintf(cmd.OutOrStdout(), "%-32s %-14s %-12s %-8s %s\n",
			spec.ID,
			spec.Provider,
			spec.Resource,
			spec.Action,
			strings.Join(spec.Path, " "),
		)
	}
	return nil
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
		table(cmd.OutOrStdout())
		return nil
	default:
		return fmt.Errorf("unsupported output format %q", opts.output)
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
	for _, spec := range providers.CommandSpecs() {
		if equalStrings(parts, spec.Path) {
			return spec, true
		}
	}
	return policy.CommandSpec{}, false
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
