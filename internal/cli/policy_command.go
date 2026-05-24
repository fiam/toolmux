package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/policy"
)

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
