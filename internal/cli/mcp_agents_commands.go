package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

func mcpConfigureCommand(opts *options) *cobra.Command {
	configure := mcpConfigureOptions{
		Command:    "toolmux",
		AgentScope: "user",
	}
	cmd := &cobra.Command{
		Use:   "configure [agent...]",
		Short: "Configure installed agents to use Toolmux MCP",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveMCPToolSelection(configure.mcpToolSelection)
			if err != nil {
				return err
			}
			if _, err := newMCPToolSelector(resolved); err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpConfigureSpec(), args); err != nil {
				return err
			}
			runtime := mcpAgentRuntime{
				lookPath: exec.LookPath,
				run:      runMCPAgentCommand,
				inspect:  inspectMCPAgent,
			}
			var removed []string
			if len(args) == 0 && interactiveCommand(cmd, opts) {
				selection, err := selectMCPAgentsInteractive(cmd, runtime, mcpConfiguredServerName(configure), configure)
				if err != nil {
					return err
				}
				if len(selection.Selected) == 0 && len(selection.Removed) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no agent changes selected")
					return nil
				}
				args = selection.Selected
				removed = selection.Removed
			}
			removedResults, err := removeMCPAgents(commandContext(cmd), runtime, configure, removed)
			if err != nil {
				return err
			}
			results, err := configureMCPAgents(commandContext(cmd), runtime, opts, configure, args)
			if err != nil {
				return err
			}
			for _, result := range removedResults {
				if configure.DryRun {
					for _, command := range result.Commands {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", result.Agent, command.String())
					}
					continue
				}
				detail := ""
				if result.Scope != "" {
					detail = " (" + result.Scope + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s MCP server %s%s\n", result.Agent, result.ServerName, detail)
			}
			for _, result := range results {
				if configure.DryRun {
					for _, command := range result.Commands {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", result.Agent, command.String())
					}
					continue
				}
				detail := ""
				if result.Scope != "" {
					detail = " (" + result.Scope + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "configured %s MCP server %s%s\n", result.Agent, result.ServerName, detail)
			}
			return nil
		},
	}
	addMCPToolSelectionFlags(cmd, &configure.mcpToolSelection)
	addMCPAgentConfigureFlags(cmd, &configure)
	return cmd
}

func mcpEnableCommand(opts *options) *cobra.Command {
	enable := mcpConfigureOptions{
		Command:    "toolmux",
		AgentScope: "user",
	}
	cmd := &cobra.Command{
		Use:   "enable [agent...]",
		Short: "Enable Toolmux MCP for agents",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveMCPToolSelection(enable.mcpToolSelection)
			if err != nil {
				return err
			}
			if _, err := newMCPToolSelector(resolved); err != nil {
				return err
			}
			if err := authorize(cmd, opts, mcpEnableSpec(), args); err != nil {
				return err
			}
			runtime := mcpAgentRuntime{
				lookPath: exec.LookPath,
				run:      runMCPAgentCommand,
				inspect:  inspectMCPAgent,
			}
			results, err := configureMCPAgents(commandContext(cmd), runtime, opts, enable, args)
			if err != nil {
				return err
			}
			for _, result := range results {
				if enable.DryRun {
					for _, command := range result.Commands {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", result.Agent, command.String())
					}
					continue
				}
				detail := ""
				if result.Scope != "" {
					detail = " (" + result.Scope + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "enabled %s MCP server %s%s\n", result.Agent, result.ServerName, detail)
			}
			return nil
		},
	}
	addMCPToolSelectionFlags(cmd, &enable.mcpToolSelection)
	addMCPAgentConfigureFlags(cmd, &enable)
	return cmd
}

func mcpDisableCommand(opts *options) *cobra.Command {
	disable := mcpConfigureOptions{}
	cmd := &cobra.Command{
		Use:   "disable [agent...]",
		Short: "Disable Toolmux MCP for agents",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, mcpDisableSpec(), args); err != nil {
				return err
			}
			runtime := mcpAgentRuntime{
				lookPath: exec.LookPath,
				run:      runMCPAgentCommand,
				inspect:  inspectMCPAgent,
			}
			selected, err := selectMCPAgents(runtime, args, disable.DryRun)
			if err != nil {
				return err
			}
			results, err := removeMCPAgents(commandContext(cmd), runtime, disable, selected)
			if err != nil {
				return err
			}
			for _, result := range results {
				if disable.DryRun {
					for _, command := range result.Commands {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", result.Agent, command.String())
					}
					continue
				}
				detail := ""
				if result.Scope != "" {
					detail = " (" + result.Scope + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "disabled %s MCP server %s%s\n", result.Agent, result.ServerName, detail)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&disable.ServerName, "server-name", "", "MCP server name removed from agents")
	cmd.Flags().StringVar(&disable.Profile, "mcp-profile", "", "MCP profile name used to derive the server name")
	cmd.Flags().BoolVar(&disable.DryRun, "dry-run", false, "print agent configuration commands without running them")
	return cmd
}

func addMCPAgentConfigureFlags(cmd *cobra.Command, configure *mcpConfigureOptions) {
	cmd.Flags().StringVar(&configure.Command, "command", "toolmux", "toolmux executable configured for agent MCP launches")
	cmd.Flags().StringVar(&configure.ServerName, "server-name", "", "MCP server name configured in agents")
	cmd.Flags().StringVar(&configure.AgentScope, "scope", "user", "agent MCP config scope: user or project")
	cmd.Flags().StringVar(&configure.ClaudeScope, "claude-scope", "", "Claude Code MCP scope override: local, user, or project")
	cmd.Flags().StringVar(&configure.GeminiScope, "gemini-scope", "", "Gemini CLI MCP scope override: user or project")
	cmd.Flags().BoolVar(&configure.DryRun, "dry-run", false, "print agent configuration commands without running them")
}
