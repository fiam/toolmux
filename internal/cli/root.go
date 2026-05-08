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
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/notion"
	"github.com/fiam/toolmux/internal/version"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type options struct {
	output      string
	color       string
	pager       string
	profile     string
	account     string
	policy      string
	credentials func() (credentials.Store, error)
	httpClient  *http.Client
	notionBase  string
	notionAPI   string
	toolmuxdURL string
}

type Dependencies struct {
	Credentials credentials.Store
	HTTPClient  *http.Client
	NotionURL   string
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
	opts.notionBase = firstNonEmpty(deps.NotionURL, os.Getenv("TOOLMUX_NOTION_API_URL"), notion.DefaultBaseURL)
	opts.notionAPI = firstNonEmpty(os.Getenv("TOOLMUX_NOTION_VERSION"), notion.DefaultVersion)
	opts.toolmuxdURL = strings.TrimRight(firstNonEmpty(deps.ToolmuxdURL, os.Getenv("TOOLMUX_TOOLMUXD_URL"), "https://api.toolmux.com"), "/")

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

	root.AddCommand(versionCommand())
	root.AddCommand(connectCommand(opts))
	root.AddCommand(disconnectCommand(opts))
	root.AddCommand(statusCommand(opts))
	root.AddCommand(doctorCommand(opts))
	root.AddCommand(connectionsCommand())
	root.AddCommand(policyCommand(opts))
	registerNotionCommands(root, opts)
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
			if provider.ID == "notion" {
				return connectNotion(cmd, opts, opts.toolmuxdURL, authURLOnly, noBrowser, timeout)
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
			if provider.ID == "notion" {
				return disconnectNotion(cmd, opts, opts.toolmuxdURL, yes)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disconnect %s: not implemented yet\n", provider.DisplayName)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm remote token revocation and local credential deletion")
	return cmd
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
	Provider      string            `json:"provider"`
	DisplayName   string            `json:"display_name"`
	Profile       string            `json:"profile"`
	Account       string            `json:"account"`
	Connected     bool              `json:"connected"`
	TokenType     string            `json:"token_type,omitempty"`
	ExpiresAt     time.Time         `json:"expires_at,omitzero"`
	Scopes        []string          `json:"scopes,omitempty"`
	Permissions   []string          `json:"permissions,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
	WorkspaceID   string            `json:"workspace_id,omitempty"`
	WorkspaceName string            `json:"workspace_name,omitempty"`
	Message       string            `json:"message,omitempty"`
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

type providerDiagnostic struct {
	Provider    string            `json:"provider,omitempty"`
	Check       string            `json:"check"`
	Status      string            `json:"status"`
	Message     string            `json:"message"`
	Remediation string            `json:"remediation,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
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
	if provider.ID == "notion" {
		diagnostics = append(diagnostics, notionProviderDiagnostics(ctx, opts, status)...)
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

func notionProviderDiagnostics(ctx context.Context, opts *options, status providerStatus) []providerDiagnostic {
	diagnostics := []providerDiagnostic{{
		Provider: "notion",
		Check:    "toolmuxd",
		Status:   "ok",
		Message:  opts.toolmuxdURL,
	}}
	if !status.Connected {
		return diagnostics
	}
	client, err := notionClient(ctx, opts)
	if err != nil {
		return append(diagnostics, providerDiagnostic{
			Provider:    "notion",
			Check:       "api",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Run `toolmux connect notion` to refresh the local connection.",
		})
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.Search(probeCtx, notion.SearchRequest{PageSize: 1}); err != nil {
		return append(diagnostics, providerDiagnostic{
			Provider:    "notion",
			Check:       "api",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Check that the Notion connection still has access to at least one selected page or data source.",
		})
	}
	return append(diagnostics, providerDiagnostic{
		Provider: "notion",
		Check:    "api",
		Status:   "ok",
		Message:  "search endpoint reachable",
	})
}

func providerPermissions(provider providers.Provider, tokens credentials.OAuthTokens) []string {
	if len(tokens.Scopes) > 0 {
		return append([]string(nil), tokens.Scopes...)
	}
	if provider.ID == "notion" {
		return notion.DefaultCapabilities()
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
	for _, spec := range provider.Specs {
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

func registerNotionCommands(root *cobra.Command, opts *options) {
	cmd := &cobra.Command{
		Use:   "notion",
		Short: "Operate Notion pages and data sources",
	}
	cmd.AddCommand(notionSearchCommand(opts))
	cmd.AddCommand(notionPageCommand(opts))
	cmd.AddCommand(notionDataSourceCommand(opts))
	cmd.AddCommand(notionDatabaseCommand(opts))
	root.AddCommand(cmd)
}

func notionSearchCommand(opts *options) *cobra.Command {
	var query string
	var objectType string
	var limit int
	var sortBy string
	var direction string
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search Notion pages and data sources shared with Toolmux",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.search"), args); err != nil {
				return err
			}
			effectiveQuery := query
			if effectiveQuery == "" && len(args) > 0 {
				effectiveQuery = args[0]
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			request := notion.SearchRequest{
				Query:      effectiveQuery,
				ObjectType: objectType,
			}
			if strings.TrimSpace(sortBy) != "" && strings.TrimSpace(sortBy) != "none" {
				request.Sort = &notion.SearchSort{Timestamp: sortBy, Direction: direction}
			}
			results, err := client.SearchAll(cmd.Context(), request, limit)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, results, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(results.Results))
				for _, result := range results.Results {
					status := "active"
					if result.InTrash {
						status = "trashed"
					}
					rows = append(rows, []string{
						output.Value(result.Title),
						result.ID,
						output.Value(result.URL),
						result.Object,
						output.StatusBadge(human, status),
						output.Value(result.LastEditedTime),
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Title", "ID", "URL", "Type", "Status", "Last Edited"},
					Rows:    rows,
					Empty:   "no Notion results",
				})
			})
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "title query")
	cmd.Flags().StringVar(&objectType, "type", "all", "result type: all, page, data_source")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum results")
	cmd.Flags().StringVar(&sortBy, "sort", "", "sort: edited, none")
	cmd.Flags().StringVar(&direction, "direction", "desc", "sort direction: asc, desc")
	return cmd
}

func notionPageCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "page",
		Short: "Read and mutate Notion pages",
	}
	cmd.AddCommand(notionPageGetCommand(opts))
	cmd.AddCommand(notionPageReadCommand(opts))
	cmd.AddCommand(notionPageLinksCommand(opts))
	cmd.AddCommand(notionPageOpenCommand(opts))
	cmd.AddCommand(notionPageChildrenCommand(opts))
	cmd.AddCommand(notionPageTreeCommand(opts))
	cmd.AddCommand(notionPageDoctorCommand(opts))
	cmd.AddCommand(notionPageMarkdownCommand(opts))
	cmd.AddCommand(notionPageCreateCommand(opts))
	cmd.AddCommand(notionPageUpdateCommand(opts))
	cmd.AddCommand(notionPageContentCommand(opts))
	cmd.AddCommand(notionPageDeleteCommand(opts))
	cmd.AddCommand(notionPageRestoreCommand(opts))
	cmd.AddCommand(notionPageMoveCommand(opts))
	return cmd
}

func notionPageGetCommand(opts *options) *cobra.Command {
	var format string
	var includeTranscript bool
	var filterProperties []string
	cmd := &cobra.Command{
		Use:   "get <page>",
		Short: "Retrieve a Notion page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.get"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			switch format {
			case "properties":
				page, err := client.RetrievePage(cmd.Context(), pageID, filterProperties)
				if err != nil {
					return err
				}
				return writePage(cmd, opts, page)
			case "markdown":
				markdown, err := client.RetrievePageMarkdown(cmd.Context(), pageID, includeTranscript)
				if err != nil {
					return err
				}
				return writeMarkdown(cmd, opts, markdown)
			case "full":
				page, err := client.RetrievePage(cmd.Context(), pageID, filterProperties)
				if err != nil {
					return err
				}
				markdown, err := client.RetrievePageMarkdown(cmd.Context(), pageID, includeTranscript)
				if err != nil {
					return err
				}
				return writeValue(cmd, opts, map[string]any{"page": page, "markdown": markdown}, func(w io.Writer) {
					fmt.Fprintf(w, "%s %s\n\n%s\n", page.ID, firstNonEmpty(page.Title(), page.URL), markdown.Markdown)
				})
			default:
				return fmt.Errorf("--format must be properties, markdown, or full")
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "properties", "format: properties, markdown, full")
	cmd.Flags().BoolVar(&includeTranscript, "include-transcript", false, "include meeting note transcripts in markdown")
	cmd.Flags().StringSliceVar(&filterProperties, "filter-property", nil, "page property to include")
	return cmd
}

type notionPageRead struct {
	Page     notion.Page         `json:"page"`
	Markdown notion.PageMarkdown `json:"markdown"`
}

type notionPageLink struct {
	Index        int    `json:"index"`
	Label        string `json:"label"`
	URL          string `json:"url"`
	Kind         string `json:"kind"`
	NotionPageID string `json:"notion_page_id,omitempty"`
}

type notionPageChild struct {
	Depth       int    `json:"depth"`
	Title       string `json:"title"`
	ID          string `json:"id"`
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	HasChildren bool   `json:"has_children"`
}

func notionPageReadCommand(opts *options) *cobra.Command {
	var includeTranscript bool
	var follow bool
	cmd := &cobra.Command{
		Use:   "read <page>",
		Short: "Read a Notion page in the terminal",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.read"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			result, err := retrieveNotionPageRead(cmd.Context(), client, pageID, includeTranscript)
			if err != nil {
				return err
			}
			if follow {
				if !interactiveCommand(cmd, opts) {
					return fmt.Errorf("--follow requires table output with interactive stdin, stdout, and stderr")
				}
				return followNotionPageLinks(cmd, opts, client, result, includeTranscript)
			}
			return writePageRead(cmd, opts, result)
		},
	}
	cmd.Flags().BoolVar(&includeTranscript, "include-transcript", false, "include meeting note transcripts")
	cmd.Flags().BoolVar(&follow, "follow", false, "choose a link after reading; Notion links open in toolmux, external links open in the browser")
	return cmd
}

func notionPageLinksCommand(opts *options) *cobra.Command {
	var includeTranscript bool
	cmd := &cobra.Command{
		Use:   "links <page>",
		Short: "List links from a Notion page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.links"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			markdown, err := client.RetrievePageMarkdown(cmd.Context(), pageID, includeTranscript)
			if err != nil {
				return err
			}
			links := notionPageLinksFromMarkdown(markdown.Markdown)
			return writeValue(cmd, opts, links, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(links))
				for _, link := range links {
					rows = append(rows, []string{
						strconv.Itoa(link.Index),
						output.Value(link.Label),
						output.Value(link.Kind),
						output.Value(link.URL),
						output.Value(link.NotionPageID),
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"#", "Label", "Kind", "URL", "Notion Page"},
					Rows:    rows,
					Empty:   "no links on this page",
				})
			})
		},
	}
	cmd.Flags().BoolVar(&includeTranscript, "include-transcript", false, "include meeting note transcripts")
	return cmd
}

func notionPageOpenCommand(opts *options) *cobra.Command {
	var urlOnly bool
	cmd := &cobra.Command{
		Use:   "open <page>",
		Short: "Open a Notion page in the browser",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.open"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			page, err := client.RetrievePage(cmd.Context(), pageID, nil)
			if err != nil {
				return err
			}
			pageURL := firstNonEmpty(page.URL, page.PublicURL, notionPageWebURL(page.ID))
			if pageURL == "" {
				return fmt.Errorf("Notion page %s has no URL", page.ID)
			}
			if urlOnly || opts.output != "table" {
				return writeValue(cmd, opts, map[string]string{"id": page.ID, "url": pageURL}, func(w io.Writer) {
					fmt.Fprintln(w, pageURL)
				})
			}
			if err := openURL(pageURL); err != nil {
				return fmt.Errorf("open Notion page %q: %w", pageURL, err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), pageURL)
			return nil
		},
	}
	cmd.Flags().BoolVar(&urlOnly, "url-only", false, "print the page URL without opening a browser")
	return cmd
}

func notionPageChildrenCommand(opts *options) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "children <page>",
		Short: "List child pages under a Notion page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.children"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			children, err := collectNotionPageChildren(cmd.Context(), client, pageID, 1, limit)
			if err != nil {
				return err
			}
			return writeNotionPageChildren(cmd, opts, children, "no child pages")
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum child pages")
	return cmd
}

func notionPageTreeCommand(opts *options) *cobra.Command {
	var depth int
	var limit int
	cmd := &cobra.Command{
		Use:   "tree <page>",
		Short: "List nested child pages under a Notion page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.tree"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			children, err := collectNotionPageChildren(cmd.Context(), client, pageID, depth, limit)
			if err != nil {
				return err
			}
			return writeNotionPageChildren(cmd, opts, children, "no child pages")
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 3, "maximum child-page depth")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum child pages")
	return cmd
}

func notionPageDoctorCommand(opts *options) *cobra.Command {
	var includeTranscript bool
	cmd := &cobra.Command{
		Use:   "doctor <page>",
		Short: "Check Notion page markdown export fidelity",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.doctor"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			result, err := retrieveNotionPageRead(cmd.Context(), client, pageID, includeTranscript)
			if err != nil {
				return err
			}
			return writeDiagnostics(cmd, opts, notionPageDiagnostics(result))
		},
	}
	cmd.Flags().BoolVar(&includeTranscript, "include-transcript", false, "include meeting note transcripts")
	return cmd
}

func notionPageMarkdownCommand(opts *options) *cobra.Command {
	var includeTranscript bool
	cmd := &cobra.Command{
		Use:   "markdown <page>",
		Short: "Export a Notion page as raw markdown",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.markdown"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			markdown, err := client.RetrievePageMarkdown(cmd.Context(), pageID, includeTranscript)
			if err != nil {
				return err
			}
			return writeMarkdown(cmd, opts, markdown)
		},
	}
	cmd.Flags().BoolVar(&includeTranscript, "include-transcript", false, "include meeting note transcripts")
	return cmd
}

func notionPageCreateCommand(opts *options) *cobra.Command {
	var parent string
	var parentType string
	var title string
	var titleProperty string
	var markdownText string
	var file string
	var propertiesJSON string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Notion page",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.create"), args); err != nil {
				return err
			}
			body, err := readMarkdownInput(markdownText, file)
			if err != nil {
				return err
			}
			parsedParent, err := parseNotionParent(parentType, parent, true)
			if err != nil {
				return err
			}
			request := notion.CreatePageRequest{
				Parent:        parsedParent,
				Title:         title,
				TitleProperty: titleProperty,
				Markdown:      body,
				Properties:    json.RawMessage(strings.TrimSpace(propertiesJSON)),
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.create", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			page, err := client.CreatePage(cmd.Context(), request)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "parent page id, data source id, workspace, or URL")
	cmd.Flags().StringVar(&parentType, "parent-type", "page", "parent type: page, data-source, workspace")
	cmd.Flags().StringVar(&title, "title", "", "page title")
	cmd.Flags().StringVar(&titleProperty, "title-property", "", "title property name")
	cmd.Flags().StringVar(&markdownText, "markdown", "", "markdown page body")
	cmd.Flags().StringVar(&file, "file", "", "read markdown page body from file")
	cmd.Flags().StringVar(&propertiesJSON, "properties-json", "", "raw Notion properties JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without creating the page")
	return cmd
}

func notionPageUpdateCommand(opts *options) *cobra.Command {
	var title string
	var titleProperty string
	var propertiesJSON string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "update <page>",
		Short: "Update Notion page properties",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.update"), args); err != nil {
				return err
			}
			request := notion.UpdatePageRequest{
				Title:         title,
				TitleProperty: titleProperty,
				Properties:    json.RawMessage(strings.TrimSpace(propertiesJSON)),
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.update", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			page, err := client.UpdatePage(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new page title")
	cmd.Flags().StringVar(&titleProperty, "title-property", "", "title property name")
	cmd.Flags().StringVar(&propertiesJSON, "properties-json", "", "raw Notion properties JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without updating the page")
	return cmd
}

func notionPageContentCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "content",
		Short: "Update Notion page markdown content",
	}
	cmd.AddCommand(notionPageContentInsertCommand(opts))
	cmd.AddCommand(notionPageContentReplaceCommand(opts))
	cmd.AddCommand(notionPageContentUpdateCommand(opts))
	return cmd
}

func notionPageContentInsertCommand(opts *options) *cobra.Command {
	var markdownText string
	var file string
	var after string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "insert <page>",
		Short: "Insert markdown into a Notion page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.content.insert"), args); err != nil {
				return err
			}
			body, err := readMarkdownInput(markdownText, file)
			if err != nil {
				return err
			}
			request := notion.InsertMarkdownRequest{Content: body, After: after}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.content.insert", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			markdown, err := client.InsertMarkdown(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writeMarkdown(cmd, opts, markdown)
		},
	}
	cmd.Flags().StringVar(&markdownText, "markdown", "", "markdown to insert")
	cmd.Flags().StringVar(&file, "file", "", "read markdown to insert from file")
	cmd.Flags().StringVar(&after, "after", "", "Notion markdown selection to insert after")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without updating the page")
	return cmd
}

func notionPageContentReplaceCommand(opts *options) *cobra.Command {
	var markdownText string
	var file string
	var allowDeletingContent bool
	var dryRun bool
	var yes bool
	cmd := &cobra.Command{
		Use:   "replace <page>",
		Short: "Replace all markdown content in a Notion page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.content.replace"), args); err != nil {
				return err
			}
			body, err := readMarkdownInput(markdownText, file)
			if err != nil {
				return err
			}
			request := notion.ReplaceMarkdownRequest{NewString: body, AllowDeletingContent: allowDeletingContent}
			if !yes && !dryRun {
				return fmt.Errorf("refusing to replace all Notion page content without --yes")
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.content.replace", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			markdown, err := client.ReplaceMarkdown(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writeMarkdown(cmd, opts, markdown)
		},
	}
	cmd.Flags().StringVar(&markdownText, "markdown", "", "replacement markdown")
	cmd.Flags().StringVar(&file, "file", "", "read replacement markdown from file")
	cmd.Flags().BoolVar(&allowDeletingContent, "allow-deleting-content", false, "allow deleting child pages/databases")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without updating the page")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm replacing all page content")
	return cmd
}

func notionPageContentUpdateCommand(opts *options) *cobra.Command {
	var oldString string
	var newString string
	var replaceAll bool
	var allowDeletingContent bool
	var dryRun bool
	var yes bool
	cmd := &cobra.Command{
		Use:   "update <page>",
		Short: "Search and replace Notion page markdown content",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.content.update"), args); err != nil {
				return err
			}
			request := notion.UpdateMarkdownRequest{
				ContentUpdates: []notion.ContentUpdate{{
					OldString:         oldString,
					NewString:         newString,
					ReplaceAllMatches: replaceAll,
				}},
				AllowDeletingContent: allowDeletingContent,
			}
			if allowDeletingContent && !yes && !dryRun {
				return fmt.Errorf("refusing to allow deleting child content without --yes")
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.content.update", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			markdown, err := client.UpdateMarkdown(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writeMarkdown(cmd, opts, markdown)
		},
	}
	cmd.Flags().StringVar(&oldString, "old", "", "exact markdown text to find")
	cmd.Flags().StringVar(&newString, "new", "", "replacement markdown text")
	cmd.Flags().BoolVar(&replaceAll, "replace-all", false, "replace all matching occurrences")
	cmd.Flags().BoolVar(&allowDeletingContent, "allow-deleting-content", false, "allow deleting child pages/databases")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without updating the page")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm allowing child content deletion")
	return cmd
}

func notionPageDeleteCommand(opts *options) *cobra.Command {
	var yes bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "delete <page>",
		Aliases: []string{"trash"},
		Short:   "Move a Notion page to trash",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.delete"), args); err != nil {
				return err
			}
			if !yes && !dryRun {
				return fmt.Errorf("refusing to trash Notion page without --yes")
			}
			inTrash := true
			request := notion.UpdatePageRequest{InTrash: &inTrash}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.delete", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			page, err := client.UpdatePage(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm moving the page to trash")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without trashing the page")
	return cmd
}

func notionPageRestoreCommand(opts *options) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "restore <page>",
		Short: "Restore a Notion page from trash",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.restore"), args); err != nil {
				return err
			}
			inTrash := false
			request := notion.UpdatePageRequest{InTrash: &inTrash}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.restore", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			page, err := client.UpdatePage(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without restoring the page")
	return cmd
}

func notionPageMoveCommand(opts *options) *cobra.Command {
	var parent string
	var parentType string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "move <page>",
		Short: "Move a Notion page to a new parent",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.page.move"), args); err != nil {
				return err
			}
			parsedParent, err := parseNotionParent(parentType, parent, false)
			if err != nil {
				return err
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.page.move", map[string]any{"page": notionPageArg(args), "parent": parsedParent})
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			page, err := client.MovePage(cmd.Context(), pageID, parsedParent)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "new parent page/data source id or URL")
	cmd.Flags().StringVar(&parentType, "parent-type", "page", "parent type: page, data-source")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without moving the page")
	return cmd
}

func notionDataSourceCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "data-source",
		Aliases: []string{"datasource"},
		Short:   "Operate Notion data sources",
	}
	cmd.AddCommand(notionDataSourceQueryCommand(opts))
	cmd.AddCommand(notionDataSourceSchemaCommand(opts))
	cmd.AddCommand(notionDataSourceRowCommand(opts))
	return cmd
}

func notionDataSourceQueryCommand(opts *options) *cobra.Command {
	var limit int
	var filterJSON string
	var sortsJSON string
	var resultType string
	var filterProperties []string
	cmd := &cobra.Command{
		Use:   "query <data-source>",
		Short: "Query a Notion data source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.data_source.query"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			result, err := client.QueryDataSourceAll(cmd.Context(), args[0], notion.QueryDataSourceRequest{
				PageSize:         limit,
				FilterProperties: filterProperties,
				Filter:           json.RawMessage(strings.TrimSpace(filterJSON)),
				Sorts:            json.RawMessage(strings.TrimSpace(sortsJSON)),
				ResultType:       resultType,
			}, limit)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, result, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(result.Results))
				for _, page := range result.Results {
					status := "active"
					if page.InTrash {
						status = "trashed"
					}
					rows = append(rows, []string{
						output.Value(page.Title()),
						page.ID,
						output.Value(page.URL),
						output.StatusBadge(human, status),
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Title", "ID", "URL", "Status"},
					Rows:    rows,
					Empty:   "no Notion data source rows",
				})
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum rows")
	cmd.Flags().StringVar(&filterJSON, "filter-json", "", "raw Notion filter JSON")
	cmd.Flags().StringVar(&sortsJSON, "sorts-json", "", "raw Notion sorts JSON")
	cmd.Flags().StringVar(&resultType, "result-type", "", "optional result type: page or data_source")
	cmd.Flags().StringSliceVar(&filterProperties, "filter-property", nil, "data source property to include")
	return cmd
}

func notionDataSourceSchemaCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema <data-source>",
		Short: "Inspect a Notion data source schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.data_source.schema"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			dataSource, err := client.RetrieveDataSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, dataSource, func(w io.Writer) {
				rows := dataSourcePropertyRows(dataSource.Properties)
				output.RenderTable(w, humanOutputOptions(cmd, opts), output.Table{
					Headers: []string{"Property", "ID", "Type"},
					Rows:    rows,
					Empty:   "no data source properties",
				})
			})
		},
	}
	return cmd
}

func notionDataSourceRowCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "row",
		Short: "Create and update Notion data source rows",
	}
	cmd.AddCommand(notionDataSourceRowCreateCommand(opts))
	cmd.AddCommand(notionDataSourceRowUpdateCommand(opts))
	return cmd
}

func notionDataSourceRowCreateCommand(opts *options) *cobra.Command {
	var title string
	var titleProperty string
	var markdownText string
	var file string
	var propertiesJSON string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create <data-source>",
		Short: "Create a row in a Notion data source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.data_source.row.create"), args); err != nil {
				return err
			}
			parent, err := notion.DataSourceParent(args[0])
			if err != nil {
				return err
			}
			body, err := readMarkdownInput(markdownText, file)
			if err != nil {
				return err
			}
			request := notion.CreatePageRequest{
				Parent:        parent,
				Title:         title,
				TitleProperty: titleProperty,
				Markdown:      body,
				Properties:    json.RawMessage(strings.TrimSpace(propertiesJSON)),
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.data_source.row.create", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			page, err := client.CreatePage(cmd.Context(), request)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "row title")
	cmd.Flags().StringVar(&titleProperty, "title-property", "", "title property name")
	cmd.Flags().StringVar(&markdownText, "markdown", "", "initial row page markdown")
	cmd.Flags().StringVar(&file, "file", "", "read initial row page markdown from file")
	cmd.Flags().StringVar(&propertiesJSON, "properties-json", "", "raw Notion properties JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without creating the row")
	return cmd
}

func notionDataSourceRowUpdateCommand(opts *options) *cobra.Command {
	var title string
	var titleProperty string
	var propertiesJSON string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "update <page>",
		Short: "Update row properties for a Notion data source page",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.data_source.row.update"), args); err != nil {
				return err
			}
			request := notion.UpdatePageRequest{
				Title:         title,
				TitleProperty: titleProperty,
				Properties:    json.RawMessage(strings.TrimSpace(propertiesJSON)),
			}
			if dryRun {
				return writeDryRun(cmd, opts, "notion.data_source.row.update", request)
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			pageID, err := resolveNotionPageArg(cmd, opts, client, notionPageArg(args))
			if err != nil {
				return err
			}
			page, err := client.UpdatePage(cmd.Context(), pageID, request)
			if err != nil {
				return err
			}
			return writePage(cmd, opts, page)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "row title")
	cmd.Flags().StringVar(&titleProperty, "title-property", "", "title property name")
	cmd.Flags().StringVar(&propertiesJSON, "properties-json", "", "raw Notion properties JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show request without updating the row")
	return cmd
}

func notionDatabaseCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Inspect Notion database containers",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "data-sources <database>",
		Short: "List data sources for a Notion database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, notionSpec("notion.database.data_sources"), args); err != nil {
				return err
			}
			client, err := notionClient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			database, err := client.RetrieveDatabase(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, database, func(w io.Writer) {
				rows := make([][]string, 0, len(database.DataSources))
				for _, dataSource := range database.DataSources {
					rows = append(rows, []string{dataSource.Name, dataSource.ID})
				}
				output.RenderTable(w, humanOutputOptions(cmd, opts), output.Table{
					Headers: []string{"Name", "ID"},
					Rows:    rows,
					Empty:   "no data sources",
				})
			})
		},
	})
	return cmd
}

func registerProviderCommands(root *cobra.Command, opts *options) {
	nodes := map[string]*cobra.Command{}
	for _, spec := range providers.CommandSpecs() {
		if spec.Provider == "notion" || rootOwnedPath(spec.Path) {
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
	return writeValue(cmd, opts, specs, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		rows := make([][]string, 0, len(specs))
		for _, spec := range specs {
			rows = append(rows, []string{
				spec.ID,
				spec.Provider,
				spec.Resource,
				spec.Action,
				strings.Join(spec.Path, " "),
			})
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Command", "Provider", "Resource", "Action", "Path"},
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

func notionSpec(id string) policy.CommandSpec {
	for _, spec := range notion.CommandSpecs() {
		if spec.ID == id {
			return spec
		}
	}
	panic("missing Notion command spec " + id)
}

func notionClient(ctx context.Context, opts *options) (*notion.Client, error) {
	store, err := opts.credentials()
	if err != nil {
		return nil, err
	}
	tokens, err := store.LoadOAuthTokens(ctx, notionCredentialRef(opts))
	if err != nil {
		return nil, err
	}
	return notion.NewClient(
		tokens.AccessToken,
		notion.WithBaseURL(opts.notionBase),
		notion.WithVersion(opts.notionAPI),
		notion.WithHTTPClient(opts.httpClient),
	), nil
}

func notionCredentialRef(opts *options) credentials.ConnectionRef {
	return providerCredentialRef(opts, "notion")
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

func connectNotion(cmd *cobra.Command, opts *options, serverURL string, authURLOnly, noBrowser bool, timeout time.Duration) error {
	ui := newConnectUI(cmd, opts)
	serverURL = strings.TrimRight(serverURL, "/")
	if serverURL == "" {
		return fmt.Errorf("toolmuxd URL is required")
	}
	ui.status("Creating Notion OAuth session")
	payload, err := json.Marshal(map[string]string{"provider": "notion", "profile": opts.profile, "account": opts.account})
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
		return fmt.Errorf("create Notion OAuth session: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var session oauthSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return err
	}
	if session.AuthURL == "" {
		return fmt.Errorf("toolmuxd did not return a Notion authorization URL")
	}
	ui.done("Created Notion OAuth session")
	fmt.Fprintf(cmd.OutOrStdout(), "open this URL to connect Notion:\n%s\n", session.AuthURL)
	if session.RedirectURI != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Notion redirect URI:\n%s\n", session.RedirectURI)
	}
	if authURLOnly {
		return nil
	}
	if ui.interactive && !noBrowser {
		if err := openURL(session.AuthURL); err != nil {
			ui.warn("Could not open browser automatically: %v", err)
		} else {
			ui.status("Opened browser for Notion authorization")
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ui.spin("Waiting for Notion authorization")
		polled, err := pollOAuthSession(cmd.Context(), opts, serverURL, session.SessionID)
		if err != nil {
			ui.stop()
			return err
		}
		switch polled.Status {
		case "complete":
			ui.done("Notion authorization complete")
			if polled.Tokens == nil {
				return fmt.Errorf("notion OAuth session completed without token handoff")
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.SaveOAuthTokens(cmd.Context(), notionCredentialRef(opts), *polled.Tokens); err != nil {
				return err
			}
			workspace := polled.Tokens.Extra["workspace_name"]
			if workspace == "" {
				workspace = polled.Tokens.Extra["workspace_id"]
			}
			fmt.Fprintf(cmd.OutOrStdout(), "connected Notion: %s\n", firstNonEmpty(workspace, "default"))
			return nil
		case "failed", "expired":
			ui.stop()
			return fmt.Errorf("notion OAuth session %s: %s", polled.Status, polled.Error)
		}
		select {
		case <-cmd.Context().Done():
			ui.stop()
			return cmd.Context().Err()
		case <-time.After(time.Second):
		}
	}
	ui.stop()
	return fmt.Errorf("timed out waiting for Notion OAuth completion")
}

func disconnectNotion(cmd *cobra.Command, opts *options, serverURL string, yes bool) error {
	if !yes {
		return fmt.Errorf("refusing to disconnect Notion without --yes")
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	ref := notionCredentialRef(opts)
	tokens, err := store.LoadOAuthTokens(cmd.Context(), ref)
	if err != nil {
		return err
	}
	if serverURL != "" && tokens.AccessToken != "" {
		payload, err := json.Marshal(map[string]string{"token": tokens.AccessToken})
		if err != nil {
			return err
		}
		// #nosec G107 -- toolmuxd URL is explicit local/deployment configuration.
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, strings.TrimRight(serverURL, "/")+"/v1/oauth/notion/revoke", bytes.NewReader(payload))
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
			return fmt.Errorf("revoke Notion token: status %d", resp.StatusCode)
		}
	}
	if err := store.DeleteOAuthTokens(cmd.Context(), ref); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "disconnected Notion")
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

func notionPageArg(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

func resolveNotionPageArg(cmd *cobra.Command, opts *options, client *notion.Client, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("notion page is required")
	}
	if id, err := notion.NormalizeID(value); err == nil {
		return id, nil
	}
	results, err := client.Search(commandContext(cmd), notion.SearchRequest{
		Query:      value,
		ObjectType: "page",
		PageSize:   10,
	})
	if err != nil {
		return "", fmt.Errorf("search Notion page %q: %w", value, err)
	}
	pages := notionPageSearchMatches(results.Results)
	switch len(pages) {
	case 0:
		return "", fmt.Errorf("no Notion page matches %q; pass a page ID or URL", value)
	case 1:
		return pages[0].ID, nil
	default:
		if interactiveCommand(cmd, opts) {
			return selectNotionPage(cmd, value, pages)
		}
		return "", multipleNotionPagesError(value, pages)
	}
}

func commandContext(cmd *cobra.Command) context.Context {
	ctx := cmd.Context()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func notionPageSearchMatches(results []notion.SearchResult) []notion.SearchResult {
	pages := make([]notion.SearchResult, 0, len(results))
	for _, result := range results {
		if result.Object == "page" {
			pages = append(pages, result)
		}
	}
	return pages
}

func selectNotionPage(cmd *cobra.Command, query string, pages []notion.SearchResult) (string, error) {
	selected := pages[0].ID
	options := make([]huh.Option[string], 0, len(pages))
	for _, page := range pages {
		options = append(options, huh.NewOption(notionPageSelectionLabel(page), page.ID))
	}
	height := len(options) + 4
	if height > 12 {
		height = 12
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Select a Notion page").
			Description("Multiple pages match " + strconv.Quote(query)).
			Options(options...).
			Value(&selected).
			Height(height).
			Filtering(len(options) > 6),
	)).
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.ErrOrStderr()).
		WithWidth(terminalWidth(cmd.ErrOrStderr())).
		WithHeight(height + 5)
	if err := form.RunWithContext(commandContext(cmd)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", fmt.Errorf("page selection cancelled")
		}
		return "", err
	}
	return selected, nil
}

func notionPageSelectionLabel(page notion.SearchResult) string {
	title := firstNonEmpty(page.Title, "(untitled)")
	detail := shortNotionPageID(page.ID)
	if detail == "" {
		detail = page.URL
	}
	if detail == "" {
		return title
	}
	return title + "  " + detail
}

func shortNotionPageID(id string) string {
	id = strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func multipleNotionPagesError(query string, pages []notion.SearchResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "multiple Notion pages match %q; pass a page ID or URL", query)
	for _, page := range pages {
		fmt.Fprintf(&b, "\n- %s (%s)", firstNonEmpty(page.Title, "(untitled)"), page.ID)
	}
	return errors.New(b.String())
}

func interactiveCommand(cmd *cobra.Command, opts *options) bool {
	return opts.output == "table" && isTerminal(cmd.OutOrStdout()) && isTerminal(cmd.ErrOrStderr()) && isInputTerminal(cmd.InOrStdin())
}

func parseNotionParent(parentType, parent string, allowWorkspace bool) (notion.Parent, error) {
	parentType = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(parentType, "_", "-")))
	parent = strings.TrimSpace(parent)
	if parentType == "" || parentType == "auto" {
		if strings.EqualFold(parent, "workspace") {
			parentType = "workspace"
		} else {
			parentType = "page"
		}
	}
	switch parentType {
	case "workspace":
		if !allowWorkspace {
			return notion.Parent{}, fmt.Errorf("workspace parent is not supported for this command")
		}
		return notion.WorkspaceParent(), nil
	case "page":
		if parent == "" {
			return notion.Parent{}, fmt.Errorf("--parent is required")
		}
		return notion.PageParent(parent)
	case "data-source", "datasource":
		if parent == "" {
			return notion.Parent{}, fmt.Errorf("--parent is required")
		}
		return notion.DataSourceParent(parent)
	default:
		return notion.Parent{}, fmt.Errorf("unsupported Notion parent type %q", parentType)
	}
}

func readMarkdownInput(markdownText, file string) (string, error) {
	if strings.TrimSpace(file) != "" && markdownText != "" {
		return "", fmt.Errorf("use either --markdown or --file, not both")
	}
	if strings.TrimSpace(file) == "" {
		return markdownText, nil
	}
	// #nosec G304 -- the user explicitly chooses a local markdown file to send.
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writePage(cmd *cobra.Command, opts *options, page notion.Page) error {
	return writeValue(cmd, opts, page, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		title := page.Title()
		status := "active"
		if page.InTrash {
			status = "trashed"
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Title", "ID", "URL", "Status"},
			Rows: [][]string{
				{
					output.Value(title),
					page.ID,
					output.Value(page.URL),
					output.StatusBadge(human, status),
				},
			},
		})
	})
}

func retrieveNotionPageRead(ctx context.Context, client *notion.Client, pageID string, includeTranscript bool) (notionPageRead, error) {
	page, err := client.RetrievePage(ctx, pageID, nil)
	if err != nil {
		return notionPageRead{}, err
	}
	markdown, err := client.RetrievePageMarkdown(ctx, pageID, includeTranscript)
	if err != nil {
		return notionPageRead{}, err
	}
	return notionPageRead{Page: page, Markdown: markdown}, nil
}

func notionPageDiagnostics(result notionPageRead) []providerDiagnostic {
	pageID := result.Page.ID
	if pageID == "" {
		pageID = result.Markdown.ID
	}
	title := firstNonEmpty(result.Page.Title(), pageID)
	diagnostics := []providerDiagnostic{{
		Provider: "notion",
		Check:    "page",
		Status:   "ok",
		Message:  firstNonEmpty(title, "page loaded"),
		Details: map[string]string{
			"page_id": pageID,
		},
	}}
	if result.Markdown.Truncated {
		diagnostics = append(diagnostics, providerDiagnostic{
			Provider:    "notion",
			Check:       "markdown-truncation",
			Status:      "warn",
			Message:     "Notion returned a truncated markdown export",
			Remediation: "Read the page in Notion before relying on full round-trip edits.",
			Details: map[string]string{
				"page_id": pageID,
			},
		})
	} else {
		diagnostics = append(diagnostics, providerDiagnostic{
			Provider: "notion",
			Check:    "markdown-truncation",
			Status:   "ok",
			Message:  "markdown export is complete",
			Details: map[string]string{
				"page_id": pageID,
			},
		})
	}
	if len(result.Markdown.UnknownBlockIDs) > 0 {
		diagnostics = append(diagnostics, providerDiagnostic{
			Provider:    "notion",
			Check:       "unknown-blocks",
			Status:      "warn",
			Message:     fmt.Sprintf("%d block(s) could not be represented as markdown", len(result.Markdown.UnknownBlockIDs)),
			Remediation: "Use --output json on page markdown to inspect unknown_block_ids before editing.",
			Details: map[string]string{
				"page_id":           pageID,
				"unknown_block_ids": strings.Join(result.Markdown.UnknownBlockIDs, ","),
			},
		})
	} else {
		diagnostics = append(diagnostics, providerDiagnostic{
			Provider: "notion",
			Check:    "unknown-blocks",
			Status:   "ok",
			Message:  "all exported blocks were represented",
			Details: map[string]string{
				"page_id": pageID,
			},
		})
	}
	links := notionPageLinksFromMarkdown(result.Markdown.Markdown)
	diagnostics = append(diagnostics, providerDiagnostic{
		Provider: "notion",
		Check:    "links",
		Status:   "ok",
		Message:  fmt.Sprintf("%d link(s) found", len(links)),
		Details: map[string]string{
			"page_id": pageID,
			"links":   strconv.Itoa(len(links)),
		},
	})
	return diagnostics
}

func notionPageLinksFromMarkdown(markdown string) []notionPageLink {
	rawLinks := output.ExtractMarkdownLinks(markdown)
	links := make([]notionPageLink, 0, len(rawLinks))
	for i, raw := range rawLinks {
		link := notionPageLink{
			Index: i + 1,
			Label: strings.TrimSpace(raw.Label),
			URL:   strings.TrimSpace(raw.URL),
			Kind:  "external",
		}
		if link.Label == "" {
			link.Label = link.URL
		}
		if id, ok := notionPageIDFromLink(link.URL); ok {
			link.Kind = "notion"
			link.NotionPageID = id
		}
		links = append(links, link)
	}
	return links
}

func collectNotionPageChildren(ctx context.Context, client *notion.Client, pageID string, maxDepth, limit int) ([]notionPageChild, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if limit <= 0 {
		limit = 100
	}
	children := make([]notionPageChild, 0)
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(parentID string, depth int) error {
		if depth > maxDepth || len(children) >= limit {
			return nil
		}
		blocks, err := listAllNotionChildBlocks(ctx, client, parentID, limit-len(children))
		if err != nil {
			return err
		}
		for _, block := range blocks {
			child, ok := notionPageChildFromBlock(block, depth)
			if !ok {
				continue
			}
			if seen[child.ID] {
				continue
			}
			seen[child.ID] = true
			children = append(children, child)
			if len(children) >= limit {
				return nil
			}
			if err := walk(child.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(pageID, 1); err != nil {
		return nil, err
	}
	return children, nil
}

func listAllNotionChildBlocks(ctx context.Context, client *notion.Client, blockID string, limit int) ([]notion.Block, error) {
	if limit <= 0 {
		return nil, nil
	}
	blocks := make([]notion.Block, 0)
	cursor := ""
	for len(blocks) < limit {
		pageSize := limit - len(blocks)
		if pageSize > 100 {
			pageSize = 100
		}
		result, err := client.ListBlockChildren(ctx, blockID, notion.ListBlockChildrenRequest{
			StartCursor: cursor,
			PageSize:    pageSize,
		})
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, result.Results...)
		if !result.HasMore || result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if len(blocks) > limit {
		return blocks[:limit], nil
	}
	return blocks, nil
}

func notionPageChildFromBlock(block notion.Block, depth int) (notionPageChild, bool) {
	switch block.Type {
	case "child_page":
		title := ""
		if block.ChildPage != nil {
			title = block.ChildPage.Title
		}
		return notionPageChild{
			Depth:       depth,
			Title:       firstNonEmpty(title, "(untitled)"),
			ID:          block.ID,
			Type:        block.Type,
			URL:         notionPageWebURL(block.ID),
			HasChildren: block.HasChildren,
		}, true
	case "child_database":
		title := ""
		if block.ChildDatabase != nil {
			title = block.ChildDatabase.Title
		}
		return notionPageChild{
			Depth:       depth,
			Title:       firstNonEmpty(title, "(untitled)"),
			ID:          block.ID,
			Type:        block.Type,
			HasChildren: block.HasChildren,
		}, true
	default:
		return notionPageChild{}, false
	}
}

func writeNotionPageChildren(cmd *cobra.Command, opts *options, children []notionPageChild, empty string) error {
	return writeValue(cmd, opts, children, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		rows := make([][]string, 0, len(children))
		for _, child := range children {
			rows = append(rows, []string{
				strconv.Itoa(child.Depth),
				strings.Repeat("  ", child.Depth-1) + child.Title,
				child.ID,
				child.Type,
				output.Value(child.URL),
			})
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Depth", "Title", "ID", "Type", "URL"},
			Rows:    rows,
			Empty:   empty,
		})
	})
}

func followNotionPageLinks(cmd *cobra.Command, opts *options, client *notion.Client, result notionPageRead, includeTranscript bool) error {
	for {
		if err := writePageRead(cmd, opts, result); err != nil {
			return err
		}
		links := output.ExtractMarkdownLinks(result.Markdown.Markdown)
		if len(links) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), output.ToneText(humanOutputOptions(cmd, opts), output.ToneMuted, "no links on this page"))
			return nil
		}
		link, ok, err := selectNotionPageReadLink(cmd, links)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if pageID, ok := notionPageIDFromLink(link.URL); ok {
			if err := authorize(cmd, opts, notionSpec("notion.page.read"), []string{link.URL}); err != nil {
				return err
			}
			next, err := retrieveNotionPageRead(cmd.Context(), client, pageID, includeTranscript)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout())
			result = next
			continue
		}
		if err := openURL(link.URL); err != nil {
			return fmt.Errorf("open link %q: %w", link.URL, err)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), output.ToneText(humanOutputOptions(cmd, opts), output.ToneInfo, "opened "+link.URL))
		return nil
	}
}

func selectNotionPageReadLink(cmd *cobra.Command, links []output.MarkdownLink) (output.MarkdownLink, bool, error) {
	selected := len(links)
	options := make([]huh.Option[int], 0, len(links)+1)
	for i, link := range links {
		options = append(options, huh.NewOption(notionLinkSelectionLabel(i, link), i))
	}
	options = append(options, huh.NewOption("Done", len(links)))
	height := len(options) + 4
	if height > 14 {
		height = 14
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("Follow a link").
			Description("Choose a link from this page").
			Options(options...).
			Value(&selected).
			Height(height).
			Filtering(len(options) > 8),
	)).
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.ErrOrStderr()).
		WithWidth(terminalWidth(cmd.ErrOrStderr())).
		WithHeight(height + 5)
	if err := form.RunWithContext(commandContext(cmd)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return output.MarkdownLink{}, false, nil
		}
		return output.MarkdownLink{}, false, err
	}
	if selected < 0 || selected >= len(links) {
		return output.MarkdownLink{}, false, nil
	}
	return links[selected], true, nil
}

func notionLinkSelectionLabel(index int, link output.MarkdownLink) string {
	label := firstNonEmpty(strings.TrimSpace(link.Label), strings.TrimSpace(link.URL))
	detail := notionLinkSelectionDetail(link.URL)
	if detail == "" || detail == label {
		return fmt.Sprintf("[%d] %s", index+1, label)
	}
	return fmt.Sprintf("[%d] %s  %s", index+1, label, detail)
}

func notionLinkSelectionDetail(rawURL string) string {
	if id, ok := notionPageIDFromLink(rawURL); ok {
		return "Notion " + shortNotionPageID(id)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	detail := parsed.Host
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path != "" {
		detail += path
	}
	if len(detail) > 72 {
		return detail[:69] + "..."
	}
	return detail
}

func notionPageIDFromLink(rawURL string) (string, bool) {
	id, err := notion.NormalizeID(rawURL)
	if err != nil {
		return "", false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if parsed.Host == "" {
		if parsed.Scheme == "" && bareNotionLink(rawURL, id) {
			return id, true
		}
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "notion.so" || host == "www.notion.so" || strings.HasSuffix(host, ".notion.so") || host == "notion.site" || strings.HasSuffix(host, ".notion.site") {
		return id, true
	}
	return "", false
}

func bareNotionLink(rawURL, id string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(rawURL))
	compact := strings.ReplaceAll(strings.ToLower(id), "-", "")
	return cleaned == strings.ToLower(id) || cleaned == compact || strings.HasSuffix(cleaned, "-"+compact)
}

func notionPageWebURL(id string) string {
	compact := strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if compact == "" {
		return ""
	}
	return "https://www.notion.so/" + compact
}

func writePageRead(cmd *cobra.Command, opts *options, result notionPageRead) error {
	switch opts.output {
	case "json":
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	case "yaml":
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		defer encoder.Close()
		return encoder.Encode(result)
	case "table":
		source := pageReadMarkdownSource(result.Page, result.Markdown)
		rendered := markdownForOutput(cmd.OutOrStdout(), opts, source)
		if result.Markdown.Truncated {
			rendered += fmt.Sprintf("\n\n%s\n", output.ToneText(humanOutputOptions(cmd, opts), output.ToneWarning, fmt.Sprintf("truncated: %d unknown blocks", len(result.Markdown.UnknownBlockIDs))))
		}
		return writePossiblyPaged(cmd, opts, rendered)
	default:
		return fmt.Errorf("unsupported output format %q", opts.output)
	}
}

func pageReadMarkdownSource(page notion.Page, markdown notion.PageMarkdown) string {
	source := strings.TrimSpace(markdown.Markdown)
	title := strings.TrimSpace(page.Title())
	if title != "" && !strings.HasPrefix(source, "# ") && !strings.HasPrefix(source, "## ") {
		if source == "" {
			source = "# " + title
		} else {
			source = "# " + title + "\n\n" + source
		}
	}
	return output.PrepareReadableMarkdown(source)
}

func writeMarkdown(cmd *cobra.Command, opts *options, markdown notion.PageMarkdown) error {
	return writeValue(cmd, opts, markdown, func(w io.Writer) {
		fmt.Fprintln(w, strings.TrimRight(markdown.Markdown, "\n"))
		if markdown.Truncated {
			fmt.Fprintf(w, "\ntruncated: %d unknown blocks\n", len(markdown.UnknownBlockIDs))
		}
	})
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

func writeDryRun(cmd *cobra.Command, opts *options, action string, request any) error {
	value := map[string]any{
		"dry_run": true,
		"action":  action,
		"request": request,
	}
	return writeValue(cmd, opts, value, func(w io.Writer) {
		output.RenderTable(w, humanOutputOptions(cmd, opts), output.Table{
			Headers: []string{"Field", "Value"},
			Rows: [][]string{
				{"Dry run", "true"},
				{"Action", action},
				{"Request", "use --output json to inspect payload"},
			},
		})
	})
}

func dataSourcePropertyRows(properties map[string]json.RawMessage) [][]string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		var property struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		_ = json.Unmarshal(properties[name], &property)
		rows = append(rows, []string{name, output.Value(property.ID), output.Value(property.Type)})
	}
	return rows
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
