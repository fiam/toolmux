package cli

import (
	"fmt"
	"maps"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/version"
)

const (
	nativeProviderAnnotation = "toolmux.native.provider"
	nativeToolboxAnnotation  = "toolmux.native.toolbox"
)

type options struct {
	output                  string
	color                   string
	pager                   string
	profile                 string
	policy                  string
	readOnly                bool
	credentials             func() (credentials.Store, error)
	httpClient              *http.Client
	openBrowser             func(string) error
	env                     func(string) string
	providerURL             map[string]string
	providerAPI             map[string]string
	toolmuxdURL             string
	mcpCacheDir             string
	mcpToolCallTimeout      time.Duration
	mcpRemoteConflicts      []mcpRemoteNameConflict
	workDir                 string
	workflowAgentDiscoverer workflowAgentDiscoverer
}

type Dependencies struct {
	Credentials             credentials.Store
	HTTPClient              *http.Client
	OpenBrowser             func(string) error
	Env                     func(string) string
	ProviderURL             map[string]string
	ProviderAPI             map[string]string
	ToolmuxdURL             string
	WorkDir                 string
	WorkflowAgentDiscoverer func(config WorkflowConfigSnapshot) []WorkflowAgentCandidate
}

// WorkflowAgentCandidate is the public projection of an internal workflow
// agent candidate, used by external callers wiring up a custom discoverer.
type WorkflowAgentCandidate struct {
	Name    string
	Label   string
	Command string
	Args    []string
}

// WorkflowConfigSnapshot is the read-only view of the workflow config exposed
// to external discoverers.
type WorkflowConfigSnapshot struct {
	DefaultAgent string
	AgentNames   []string
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
	opts.env = env
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
	if deps.WorkflowAgentDiscoverer != nil {
		opts.workflowAgentDiscoverer = adaptWorkflowAgentDiscoverer(deps.WorkflowAgentDiscoverer)
	}
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
	root.AddCommand(configCommand(opts))
	root.AddCommand(policyCommand(opts))
	root.AddCommand(mcpCommand(opts))
	root.AddCommand(workflowCommand(opts))
	registerActionCommands(root, opts)
	opts.mcpRemoteConflicts = registerCachedMCPRemoteCommands(root, opts)

	return root
}

func adaptWorkflowAgentDiscoverer(external func(WorkflowConfigSnapshot) []WorkflowAgentCandidate) workflowAgentDiscoverer {
	return func(config workflowConfig) []workflowAgentCandidate {
		names := make([]string, 0, len(config.Agents))
		for name := range config.Agents {
			names = append(names, name)
		}
		snapshot := WorkflowConfigSnapshot{
			DefaultAgent: config.DefaultAgent,
			AgentNames:   names,
		}
		external := external(snapshot)
		out := make([]workflowAgentCandidate, 0, len(external))
		for _, candidate := range external {
			out = append(out, workflowAgentCandidate{
				Name:  candidate.Name,
				Label: firstNonEmpty(candidate.Label, candidate.Name),
				Config: workflowAgentConfig{
					Command: candidate.Command,
					Args:    append([]string(nil), candidate.Args...),
				},
			})
		}
		return out
	}
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
