package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
)

func callMCPRemoteTool(ctx context.Context, client *http.Client, entry mcpRemoteServerEntry, tool mcpRemoteTool, arguments map[string]any, bearerToken string, responseIdleTimeout time.Duration, trace *mcpRemoteHTTPTrace) (mcpCallToolResult, error) {
	entry.Server = normalizeMCPRemoteServer(entry.Server)
	if entry.Server.Transport == mcpRemoteTransportStdio {
		return callMCPRemoteStdioTool(ctx, entry, tool, arguments, responseIdleTimeout)
	}
	_, sessionID, err := initializeMCPRemoteSession(ctx, client, entry.Server, bearerToken, trace)
	if err != nil {
		return mcpCallToolResult{}, err
	}
	result, _, err := callMCPRemote(ctx, client, entry.Server, bearerToken, sessionID, "tools/call", map[string]any{
		"name":      tool.Name,
		"arguments": arguments,
	}, trace, responseIdleTimeout)
	if err != nil {
		return mcpCallToolResult{}, err
	}
	var callResult mcpCallToolResult
	if err := json.Unmarshal(result, &callResult); err == nil && mcpRemoteCallToolResultHasPayload(callResult) {
		return callResult, nil
	}
	return mcpTextToolResult(string(result)), nil
}

func mcpRemoteCallToolResultHasPayload(result mcpCallToolResult) bool {
	return len(result.Content) > 0 || result.StructuredContent != nil || result.IsError || len(result.Meta) > 0
}

func registerCachedMCPRemoteCommands(root *cobra.Command, opts *options) []mcpRemoteNameConflict {
	entries, err := effectiveMCPRemoteServerEntries(opts.workDir)
	if err != nil {
		return nil
	}
	var conflicts []mcpRemoteNameConflict
	for _, entry := range entries {
		if rootNativeCommandHasName(root, entry.Name) {
			conflicts = append(conflicts, mcpRemoteNameConflict{Name: entry.Name})
			continue
		}
		command := mcpRemoteRootCommand(opts, entry)
		root.AddCommand(command)
	}
	return conflicts
}

func mcpRemoteRootCommand(opts *options, entry mcpRemoteServerEntry) *cobra.Command {
	cache, ok, _ := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
	var fullHelp bool
	cmd := &cobra.Command{
		Use:   entry.Name,
		Short: "Imported remote MCP server " + entry.Name,
		Annotations: map[string]string{
			mcpRemoteServerAnnotation: entry.Name,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return fmt.Errorf("MCP server %q has no command %q; run `toolmux mcp sync %s` to refresh cached tools", entry.Name, strings.Join(args, " "), entry.Name)
		},
	}
	cmd.Flags().BoolVar(&fullHelp, "full-help", false, "show full upstream MCP tool descriptions")
	setMCPRemoteRootHelp(cmd, opts, &fullHelp)
	if !ok {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("MCP server %q has no cached tools; run `toolmux mcp sync %s`", entry.Name, entry.Name)
		}
		return cmd
	}
	for _, tool := range cache.Tools {
		cmd.AddCommand(mcpRemoteToolCommand(opts, entry, tool))
	}
	return cmd
}

func mcpRemoteToolCommand(opts *options, entry mcpRemoteServerEntry, tool mcpRemoteTool) *cobra.Command {
	var rawJSON string
	var verboseHTTP bool
	spec := mcpRemoteActionSpecForEntry(entry, tool)
	description := firstNonEmpty(tool.Description, "Call remote MCP tool "+tool.Name)
	cmd := &cobra.Command{
		Use:   tool.Name,
		Short: description,
		Long:  description,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := decodeMCPRemoteCLIArguments(cmd, rawJSON, tool, entry.Server.DefaultArguments)
			if err != nil {
				return mcpRemoteDefaultArgumentErrorWithHint(err, entry)
			}
			if err := authorize(cmd, opts, spec, nil); err != nil {
				return err
			}
			trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), verboseHTTP)
			toolForCall := tool
			if refreshed, ok := refreshMCPRemoteCacheIfStale(commandContext(cmd), cmd, opts, entry, trace); ok {
				freshTool, found := mcpRemoteToolFromCache(refreshed, tool.Name)
				if !found {
					return fmt.Errorf("remote MCP tool %s.%s no longer exists after cache refresh", entry.Name, tool.Name)
				}
				toolForCall = freshTool
			}
			token := ""
			if entry.Server.Transport != mcpRemoteTransportStdio {
				var err error
				token, err = loadMCPRemoteAccessToken(commandContext(cmd), opts, entry)
				if err != nil {
					return err
				}
			}
			result, err := callMCPRemoteTool(commandContext(cmd), opts.httpClient, entry, toolForCall, arguments, token, mcpRemoteToolCallTimeout(opts), trace)
			if err != nil {
				return err
			}
			return writeMCPRemoteToolResult(cmd, opts, result)
		},
	}
	cmd.Flags().StringVar(&rawJSON, "json", "", "JSON object with remote MCP tool arguments, or @path")
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote MCP HTTP requests and responses to stderr")
	addMCPRemoteToolFlags(cmd, tool, entry.Server.DefaultArguments)
	setMCPRemoteToolHelp(cmd, entry, tool)
	return cmd
}

func setMCPRemoteRootHelp(cmd *cobra.Command, opts *options, fullHelp *bool) {
	defaultHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(helpCmd *cobra.Command, args []string) {
		if mcpRemoteFullHelp(helpCmd, fullHelp) || !mcpRemoteCompactDescriptions(helpCmd, opts) {
			defaultHelp(helpCmd, args)
			return
		}
		renderMCPRemoteRootCompactHelp(helpCmd, opts)
	})
}

func mcpRemoteFullHelp(cmd *cobra.Command, fullHelp *bool) bool {
	if fullHelp != nil && *fullHelp {
		return true
	}
	value, err := cmd.Flags().GetBool("full-help")
	return err == nil && value
}

func renderMCPRemoteRootCompactHelp(cmd *cobra.Command, opts *options) {
	w := cmd.OutOrStdout()
	human := humanOutputOptions(cmd, opts)
	if cmd.Short != "" {
		fmt.Fprintln(w, cmd.Short)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Usage:"))
	fmt.Fprintf(w, "  %s <tool> [flags]\n", cmd.CommandPath())
	children := mcpRemoteHelpCommands(cmd)
	if len(children) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Available Commands:"))
		width := 0
		for _, child := range children {
			width = max(width, len(child.Name()))
		}
		for _, child := range children {
			name := fmt.Sprintf("%-*s", width, child.Name())
			description := output.Value(mcpRemoteCompactDescription(child.Short))
			fmt.Fprintf(w, "  %s  %s\n",
				output.ToneText(human, output.ToneInfo, name),
				output.ToneText(human, output.ToneMuted, description),
			)
		}
	}
	if flags := strings.TrimRight(cmd.LocalFlags().FlagUsages(), "\n"); flags != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Flags:"))
		fmt.Fprintln(w, flags)
	}
	if flags := strings.TrimRight(cmd.InheritedFlags().FlagUsages(), "\n"); flags != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, output.ToneText(human, output.ToneMuted, "Global Flags:"))
		fmt.Fprintln(w, flags)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Use %q for full upstream descriptions.\n", cmd.CommandPath()+" --full-help")
	fmt.Fprintf(w, "Use %q for one tool's flags and schema hint.\n", cmd.CommandPath()+" <tool> --help")
}

func mcpRemoteHelpCommands(cmd *cobra.Command) []*cobra.Command {
	children := slices.Clone(cmd.Commands())
	children = slices.DeleteFunc(children, func(child *cobra.Command) bool {
		return !child.IsAvailableCommand()
	})
	sort.Slice(children, func(i, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	return children
}
