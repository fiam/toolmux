package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func mcpProfileCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage MCP tool profiles",
	}

	var setSelection mcpToolSelection
	var setScope mcpProfileScopeOptions
	set := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or update an MCP tool profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}
			setSelection.Profile = name
			if _, err := newMCPToolSelector(setSelection); err != nil {
				return err
			}
			configPath, scope, err := mcpProfileWritePath(setScope)
			if err != nil {
				return err
			}
			config, err := readToolmuxConfigFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if config.MCP.Profiles == nil {
				config.MCP.Profiles = map[string]mcpProfileConfig{}
			}
			config.Version = 1
			config.MCP.Profiles[name] = mcpProfileConfigFromSelection(setSelection)
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved %s MCP profile %s in %s\n", scope, name, configPath)
			return nil
		},
	}
	addMCPToolPatternFlags(set, &setSelection)
	addMCPProfileScopeFlags(set, &setScope)
	cmd.AddCommand(set)

	var defaultScope mcpProfileScopeOptions
	defaultCommand := &cobra.Command{
		Use:   "default <name>",
		Short: "Set the default MCP tool profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name is required")
			}
			configPath, scope, err := mcpProfileWritePath(defaultScope)
			if err != nil {
				return err
			}
			config, err := readToolmuxConfigFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if config.MCP.Profiles == nil {
				config.MCP.Profiles = map[string]mcpProfileConfig{}
			}
			if scope == "global" {
				if _, ok := config.MCP.Profiles[name]; !ok {
					return fmt.Errorf("MCP profile %q not found in %s", name, configPath)
				}
			} else {
				if _, ok := config.MCP.Profiles[name]; !ok {
					if _, ok, err := lookupMCPProfile(name, ""); err != nil {
						return err
					} else if !ok {
						return fmt.Errorf("MCP profile %q not found", name)
					}
				}
			}
			config.Version = 1
			config.MCP.DefaultProfile = name
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set %s default MCP profile to %s in %s\n", scope, name, configPath)
			return nil
		},
	}
	addMCPProfileScopeFlags(defaultCommand, &defaultScope)
	cmd.AddCommand(defaultCommand)

	cmd.AddCommand(&cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List MCP tool profiles",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := effectiveMCPProfileEntries("")
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no MCP profiles configured")
				return nil
			}
			for _, entry := range entries {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", entry.Name, strings.Join(entry.Scopes, ","))
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show an MCP tool profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			entry, ok, err := lookupMCPProfile(name, "")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP profile %q not found", name)
			}
			return writeValue(cmd, opts, entry, nil)
		},
	})

	return cmd
}

func addMCPToolSelectionFlags(cmd *cobra.Command, selection *mcpToolSelection) {
	cmd.Flags().StringVar(&selection.Profile, "mcp-profile", "", "name for this MCP tool profile")
	addMCPToolPatternFlags(cmd, selection)
}

func addMCPToolPatternFlags(cmd *cobra.Command, selection *mcpToolSelection) {
	cmd.Flags().StringArrayVar(&selection.Tools, "tool", nil, "shell-style tool glob to include, such as linear.*")
	cmd.Flags().StringArrayVar(&selection.ToolRegex, "tool-regex", nil, "regular expression for tool IDs to include")
	cmd.Flags().StringArrayVar(&selection.ExcludeTools, "exclude-tool", nil, "shell-style tool glob to exclude")
	cmd.Flags().StringArrayVar(&selection.ExcludeToolRegex, "exclude-tool-regex", nil, "regular expression for tool IDs to exclude")
}

func addMCPProfileScopeFlags(cmd *cobra.Command, scope *mcpProfileScopeOptions) {
	cmd.Flags().BoolVar(&scope.Global, "global", false, "write to the user config")
	cmd.Flags().BoolVar(&scope.Project, "project", false, "write to the project config")
}
