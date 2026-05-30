package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func mcpCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve and configure MCP tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown mcp command %q", args[0])
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(mcpServeCommand(opts))
	cmd.AddCommand(mcpSearchCommand(opts))
	cmd.AddCommand(mcpConfigureCommand(opts))
	cmd.AddCommand(mcpEnableCommand(opts))
	cmd.AddCommand(mcpDisableCommand(opts))
	cmd.AddCommand(mcpRemoteSyncCommand(opts))
	cmd.AddCommand(mcpRemoteRenameCommand(opts))
	cmd.AddCommand(mcpRemoteAuthCommand(opts))
	cmd.AddCommand(mcpRemoteListCommand(opts))
	cmd.AddCommand(mcpRemoteShowCommand(opts))
	cmd.AddCommand(mcpRemoteDefaultsCommand(opts))
	cmd.AddCommand(schemaCommand(opts))
	cmd.AddCommand(mcpProfileCommand(opts))
	return cmd
}

func mcpServeCommand(opts *options) *cobra.Command {
	var selection mcpToolSelection
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve Toolmux tools over MCP stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveMCPToolSelection(selection)
			if err != nil {
				return err
			}
			selector, err := newMCPToolSelector(resolved)
			if err != nil {
				return err
			}
			server := mcpServer{
				opts:     opts,
				cmd:      cmd,
				selector: selector,
				lazy:     resolved.Lazy,
			}
			closeAudit := server.startAudit(cmd)
			defer closeAudit()
			if resolved.Lazy {
				server.logLazyStart()
			}
			return server.run(commandContext(cmd), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	addMCPToolSelectionFlags(cmd, &selection)
	return cmd
}
