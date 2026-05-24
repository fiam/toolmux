package cli

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
)

func mcpRemoteDefaultsCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "defaults",
		Aliases: []string{"default-args"},
		Short:   "Manage default arguments for remote MCP tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown mcp defaults command %q", args[0])
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(mcpRemoteDefaultsListCommand(opts))
	cmd.AddCommand(mcpRemoteDefaultsSetCommand(opts))
	cmd.AddCommand(mcpRemoteDefaultsRemoveCommand(opts))
	return cmd
}

func mcpRemoteDefaultsListCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "ls <name>",
		Aliases: []string{"list", "show"},
		Short:   "List default arguments for a remote MCP server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			entry, ok, err := lookupMCPRemoteServer(name, "")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			result := mcpRemoteDefaultArgumentsResult{
				Server:    entry.Name,
				Scope:     mcpRemoteScopeLabel(entry.Scope),
				Path:      entry.Path,
				Arguments: mcpRemoteDefaultArgumentItems(entry.Server.DefaultArguments),
			}
			return writeValue(cmd, opts, result, func(w io.Writer) {
				renderMCPRemoteDefaultsTable(w, cmd, opts, result)
			})
		},
	}
}

func mcpRemoteDefaultsSetCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var jsonValue bool
	cmd := &cobra.Command{
		Use:   "set <name> <argument> <value>",
		Short: "Set a default argument for a remote MCP server",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			key, err := cleanMCPRemoteDefaultArgumentName(args[1])
			if err != nil {
				return err
			}
			value, err := parseMCPRemoteDefaultArgumentValue(args[2], jsonValue)
			if err != nil {
				return err
			}
			target, err := mcpRemoteDefaultArgumentsWriteTargetForScope(name, scope, true)
			if err != nil {
				return err
			}
			if target.Server.DefaultArguments == nil {
				target.Server.DefaultArguments = map[string]any{}
			}
			target.Server.DefaultArguments[key] = value
			target.Config.MCP.Servers[name] = target.Server
			if err := writeToolmuxConfigFile(target.Path, target.Config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set default argument %s for MCP server %s in %s\n", key, name, target.Path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonValue, "json", false, "parse value as JSON instead of a string")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func mcpRemoteDefaultsRemoveCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:     "remove <name> <argument> [argument...]",
		Aliases: []string{"rm", "unset"},
		Short:   "Remove default arguments for a remote MCP server",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			keys := make([]string, 0, len(args)-1)
			for _, arg := range args[1:] {
				key, err := cleanMCPRemoteDefaultArgumentName(arg)
				if err != nil {
					return err
				}
				keys = append(keys, key)
			}
			target, err := mcpRemoteDefaultArgumentsWriteTargetForScope(name, scope, false)
			if err != nil {
				return err
			}
			for _, key := range keys {
				delete(target.Server.DefaultArguments, key)
			}
			if len(target.Server.DefaultArguments) == 0 {
				target.Server.DefaultArguments = nil
			}
			target.Config.MCP.Servers[name] = target.Server
			if err := writeToolmuxConfigFile(target.Path, target.Config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed default arguments for MCP server %s in %s\n", name, target.Path)
			return nil
		},
	}
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

type mcpRemoteDefaultArgumentsWriteTarget struct {
	Path   string
	Scope  string
	Config toolmuxConfigFile
	Server mcpRemoteServer
}

func mcpRemoteDefaultArgumentsWriteTargetForScope(name string, scope mcpProfileScopeOptions, createFromEffective bool) (mcpRemoteDefaultArgumentsWriteTarget, error) {
	configPath, scopeName, err := mcpProfileWritePath(scope)
	if err != nil {
		return mcpRemoteDefaultArgumentsWriteTarget{}, err
	}
	config, err := readToolmuxConfigFile(configPath)
	if err != nil && (!createFromEffective || !errors.Is(err, os.ErrNotExist)) {
		return mcpRemoteDefaultArgumentsWriteTarget{}, err
	}
	if config.MCP.Servers == nil {
		config.MCP.Servers = map[string]mcpRemoteServer{}
	}
	server, exists := config.MCP.Servers[name]
	if !exists {
		if !createFromEffective {
			return mcpRemoteDefaultArgumentsWriteTarget{}, fmt.Errorf("MCP server %q is not registered in %s", name, configPath)
		}
		entry, ok, err := lookupMCPRemoteServer(name, "")
		if err != nil {
			return mcpRemoteDefaultArgumentsWriteTarget{}, err
		}
		if !ok {
			return mcpRemoteDefaultArgumentsWriteTarget{}, fmt.Errorf("MCP server %q is not registered", name)
		}
		server = entry.Server
	}
	return mcpRemoteDefaultArgumentsWriteTarget{
		Path:   configPath,
		Scope:  scopeName,
		Config: config,
		Server: server,
	}, nil
}

func cleanMCPRemoteDefaultArgumentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("default argument name is required")
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("invalid default argument name %q", name)
	}
	return name, nil
}

func parseMCPRemoteDefaultArgumentValue(raw string, jsonValue bool) (any, error) {
	if !jsonValue {
		return raw, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("default argument value must be valid JSON: %w", err)
	}
	return value, nil
}

func mcpRemoteDefaultArgumentItems(arguments map[string]any) []mcpRemoteDefaultArgumentItem {
	names := make([]string, 0, len(arguments))
	for name := range arguments {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]mcpRemoteDefaultArgumentItem, 0, len(names))
	for _, name := range names {
		items = append(items, mcpRemoteDefaultArgumentItem{Name: name, Value: arguments[name]})
	}
	return items
}

func renderMCPRemoteDefaultsTable(w io.Writer, cmd *cobra.Command, opts *options, result mcpRemoteDefaultArgumentsResult) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(result.Arguments))
	for _, item := range result.Arguments {
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, item.Name),
			output.Value(mcpRemoteFormatDefaultArgumentValue(item.Value)),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Argument", "Value"},
		Rows:    rows,
		Empty:   "no default arguments for " + result.Server,
	})
}

func mcpRemoteFormatDefaultArgumentValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func writeMCPRemoteDefaultArgumentSuggestions(w io.Writer, serverName string, server mcpRemoteServer) {
	for _, hint := range mcpRemoteMissingDefaultArgumentHints(mcpRemoteServerEntry{Name: serverName, Server: server}) {
		example := firstNonEmpty(hint.Example, "<value>")
		fmt.Fprintf(w, "hint: set a default %s with `toolmux mcp defaults set %s %s %s`\n", hint.Name, serverName, hint.Name, example)
	}
}

func mcpRemoteDefaultArgumentErrorWithHint(err error, entry mcpRemoteServerEntry) error {
	var missing mcpRemoteMissingRequiredArgumentsError
	if !errors.As(err, &missing) {
		return err
	}
	missingSet := map[string]bool{}
	for _, name := range missing.Names {
		missingSet[name] = true
	}
	for _, hint := range mcpRemoteMissingDefaultArgumentHints(entry) {
		if !missingSet[hint.Name] {
			continue
		}
		example := firstNonEmpty(hint.Example, "<value>")
		return fmt.Errorf("%w\nhint: set a default %s with `toolmux mcp defaults set %s %s %s`", err, hint.Name, entry.Name, hint.Name, example)
	}
	return err
}

func mcpRemoteMissingDefaultArgumentHints(entry mcpRemoteServerEntry) []mcpRemoteDefaultArgumentHint {
	_, definition, ok := mcpRemoteCatalogDefinitionForServer(entry.Name, entry.Server)
	if !ok {
		return nil
	}
	var missing []mcpRemoteDefaultArgumentHint
	for _, hint := range definition.DefaultArgumentHints {
		if strings.TrimSpace(hint.Name) == "" {
			continue
		}
		if _, exists := entry.Server.DefaultArguments[hint.Name]; exists {
			continue
		}
		missing = append(missing, hint)
	}
	return missing
}

func mcpRemoteMissingDefaultArgumentNames(entry mcpRemoteServerEntry, hints []mcpRemoteDefaultArgumentHint) []string {
	var missing []string
	for _, hint := range hints {
		if strings.TrimSpace(hint.Name) == "" {
			continue
		}
		if _, exists := entry.Server.DefaultArguments[hint.Name]; exists {
			continue
		}
		missing = append(missing, hint.Name)
	}
	sort.Strings(missing)
	return missing
}
