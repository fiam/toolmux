package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fiam/supacli/internal/policy"
	"github.com/fiam/supacli/internal/providers"
	"github.com/fiam/supacli/internal/version"
	"github.com/spf13/cobra"
)

type options struct {
	output  string
	profile string
	account string
	policy  string
}

func NewRootCommand() *cobra.Command {
	opts := &options{output: "table", profile: "default"}

	root := &cobra.Command{
		Use:   "supacli",
		Short: "A local-first mega CLI for SaaS services",
	}
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", "table", "output format: table, json, yaml")
	root.PersistentFlags().StringVar(&opts.profile, "profile", "default", "Supacli profile")
	root.PersistentFlags().StringVar(&opts.account, "account", "", "provider account or workspace")
	root.PersistentFlags().StringVar(&opts.policy, "policy", "", "policy file path")

	root.AddCommand(versionCommand())
	root.AddCommand(connectCommand(opts))
	root.AddCommand(disconnectCommand(opts))
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
			fmt.Fprintf(cmd.OutOrStdout(), "supacli %s (%s, %s)\n", version.Version, version.Commit, version.Date)
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
	cmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check connection prerequisites",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "connection doctor: ok")
		},
	})
	return cmd
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
			path := filepath.Join(".supacli", "policy.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
