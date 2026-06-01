package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
)

func mcpRemoteSyncCommand(opts *options) *cobra.Command {
	var verboseHTTP bool
	cmd := &cobra.Command{
		Use:   "sync <name>",
		Short: "Introspect and cache a remote MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), verboseHTTP)
			cache, authRequired, err := syncMCPRemoteCacheWithProgress(cmd, opts, entry, args, trace)
			if err != nil {
				return err
			}
			if err := writeMCPRemoteAuthRequired(entry, authRequired); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced MCP server %s: %d tools\n", name, len(cache.Tools))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote MCP HTTP requests and responses to stderr")
	return cmd
}

func mcpRemoteRenameCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a registered remote MCP server",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			newName, err := cleanMCPRemoteName(args[1])
			if err != nil {
				return err
			}
			if oldName == newName {
				return fmt.Errorf("old and new MCP server names are both %q", oldName)
			}
			entry, ok, err := lookupMCPRemoteServer(oldName, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", oldName)
			}
			configPath := entry.Path
			if scope.Global || scope.Project {
				var scopeName string
				configPath, scopeName, err = mcpProfileWritePath(scope)
				if err != nil {
					return err
				}
				_ = scopeName
			}
			config, err := readToolmuxConfigFile(configPath)
			if err != nil {
				return err
			}
			server, exists := configMCPRemoteServer(config, oldName)
			if !exists {
				return fmt.Errorf("MCP server %q is not registered in %s", oldName, configPath)
			}
			if _, exists := configMCPRemoteServer(config, newName); exists {
				return fmt.Errorf("MCP server %q is already registered in %s", newName, configPath)
			}
			if err := ensureMCPRemoteNameAvailable(cmd.Root(), newName); err != nil {
				return err
			}
			renameConfigMCPRemoteServer(&config, oldName, newName, server)
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			_ = renameMCPRemoteCache(opts.mcpCacheDir, oldName, newName)
			fmt.Fprintf(cmd.OutOrStdout(), "renamed MCP server %s to %s in %s\n", oldName, newName, configPath)
			return nil
		},
	}
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func toolboxRemoveCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	cmd := &cobra.Command{
		Use:     "remove <toolbox> [toolbox...]",
		Aliases: []string{"rm"},
		Short:   "Remove a toolbox",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if handled, err := removeNativeToolboxes(cmd, opts, args, scope); handled || err != nil {
				return err
			}
			names, err := cleanMCPRemoteNames(args)
			if err != nil {
				return err
			}
			removals, err := planMCPRemoteRemovals(names, scope)
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			ctx := commandContext(cmd)
			for _, removal := range removals {
				for _, name := range removal.Names {
					deleteConfigMCPRemoteServer(&removal.Config, name)
				}
				if err := writeToolmuxConfigFile(removal.Path, removal.Config); err != nil {
					return err
				}
			}
			for _, removal := range removals {
				for _, name := range removal.Names {
					_ = removeMCPRemoteCache(opts.mcpCacheDir, name)
					if err := store.DeleteOAuthTokens(ctx, mcpRemoteCredentialRef(opts, name)); err != nil {
						return fmt.Errorf("remove stored auth for toolbox %s: %w", name, err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "removed toolbox %s from %s\n", name, removal.Path)
				}
			}
			return nil
		},
	}
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func removeNativeToolboxes(cmd *cobra.Command, opts *options, args []string, scope mcpProfileScopeOptions) (bool, error) {
	entries := make([]nativeToolboxEntry, 0, len(args))
	for _, arg := range args {
		name, err := cleanMCPRemoteName(arg)
		if err != nil {
			return true, err
		}
		entry, ok, err := lookupNativeToolboxEntry(name, opts.workDir)
		if err != nil {
			return true, err
		}
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return false, nil
	}
	if len(entries) != len(args) {
		return true, fmt.Errorf("cannot remove native and remote toolboxes in one command")
	}
	store, err := opts.credentials()
	if err != nil {
		return true, err
	}
	plans, err := planNativeToolboxRemovals(entries, scope)
	if err != nil {
		return true, err
	}
	for _, plan := range plans {
		for _, name := range plan.Names {
			delete(plan.Config.Toolboxes, name)
		}
		if err := writeToolmuxConfigFile(plan.Path, plan.Config); err != nil {
			return true, err
		}
	}
	for _, entry := range entries {
		execCtx := actionExecutionContext(commandContext(cmd), opts, store, entry.Provider, entry.Name)
		result, err := entry.Provider.RemoveHandler(execCtx, actions.Invocation{
			Spec:  toolboxRemoveSpec(),
			Args:  []string{entry.Name},
			Flags: map[string]any{},
		})
		if err != nil {
			return true, err
		}
		if err := writeActionResult(cmd, opts, execCtx, result); err != nil {
			return true, err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed toolbox %s from %s\n", entry.Name, entry.Path)
	}
	return true, nil
}

type nativeToolboxRemovalPlan struct {
	Path   string
	Config toolmuxConfigFile
	Names  []string
}

func planNativeToolboxRemovals(entries []nativeToolboxEntry, scope mcpProfileScopeOptions) ([]nativeToolboxRemovalPlan, error) {
	if scope.Global || scope.Project {
		configPath, _, err := mcpProfileWritePath(scope)
		if err != nil {
			return nil, err
		}
		config, err := readToolmuxConfigFile(configPath)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			toolbox, exists := config.Toolboxes[entry.Name]
			if !exists || toolbox.Type != toolboxTypeInternal {
				return nil, fmt.Errorf("toolbox %q is not registered in %s", entry.Name, configPath)
			}
			names = append(names, entry.Name)
		}
		return []nativeToolboxRemovalPlan{{Path: configPath, Config: config, Names: names}}, nil
	}
	plansByPath := map[string]int{}
	var plans []nativeToolboxRemovalPlan
	for _, entry := range entries {
		index, exists := plansByPath[entry.Path]
		if !exists {
			config, err := readToolmuxConfigFile(entry.Path)
			if err != nil {
				return nil, err
			}
			plans = append(plans, nativeToolboxRemovalPlan{Path: entry.Path, Config: config})
			index = len(plans) - 1
			plansByPath[entry.Path] = index
		}
		plans[index].Names = append(plans[index].Names, entry.Name)
	}
	return plans, nil
}

type mcpRemoteRemovalPlan struct {
	Path   string
	Config toolmuxConfigFile
	Names  []string
}

func cleanMCPRemoteNames(values []string) ([]string, error) {
	seen := map[string]bool{}
	names := make([]string, 0, len(values))
	for _, value := range values {
		name, err := cleanMCPRemoteName(value)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

func planMCPRemoteRemovals(names []string, scope mcpProfileScopeOptions) ([]mcpRemoteRemovalPlan, error) {
	if scope.Global || scope.Project {
		configPath, _, err := mcpProfileWritePath(scope)
		if err != nil {
			return nil, err
		}
		config, err := readToolmuxConfigFile(configPath)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if _, exists := configMCPRemoteServer(config, name); !exists {
				return nil, fmt.Errorf("MCP server %q is not registered in %s", name, configPath)
			}
		}
		return []mcpRemoteRemovalPlan{{
			Path:   configPath,
			Config: config,
			Names:  names,
		}}, nil
	}

	plansByPath := map[string]int{}
	var plans []mcpRemoteRemovalPlan
	for _, name := range names {
		entry, ok, err := lookupMCPRemoteServer(name, "")
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("MCP server %q is not registered", name)
		}
		index, exists := plansByPath[entry.Path]
		if !exists {
			config, err := readToolmuxConfigFile(entry.Path)
			if err != nil {
				return nil, err
			}
			plans = append(plans, mcpRemoteRemovalPlan{
				Path:   entry.Path,
				Config: config,
			})
			index = len(plans) - 1
			plansByPath[entry.Path] = index
		}
		plan := &plans[index]
		if _, exists := configMCPRemoteServer(plan.Config, name); !exists {
			return nil, fmt.Errorf("MCP server %q is not registered in %s", name, entry.Path)
		}
		plan.Names = append(plan.Names, name)
	}
	return plans, nil
}

func mcpRemoteAuthCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage stored auth for imported remote MCP servers",
	}
	cmd.AddCommand(mcpRemoteAuthLoginCommand(opts))
	cmd.AddCommand(mcpRemoteAuthSetCommand(opts))
	cmd.AddCommand(mcpRemoteAuthRemoveCommand(opts))
	cmd.AddCommand(mcpRemoteAuthStatusCommand(opts))
	return cmd
}

func mcpRemoteAuthSetCommand(opts *options) *cobra.Command {
	var bearerToken string
	var bearerTokenEnv string
	var bearerTokenStdin bool
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Store bearer token auth for a remote MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			if normalizeMCPRemoteServer(entry.Server).Transport == mcpRemoteTransportStdio {
				return fmt.Errorf("MCP server %q uses stdio; configure auth in the command environment or arguments", name)
			}
			token, err := mcpRemoteBearerTokenFromFlags(cmd, bearerToken, bearerTokenEnv, bearerTokenStdin)
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, name), mcpRemoteBearerTokens(token, entry)); err != nil {
				return err
			}
			if err := writeMCPRemoteAuthRequired(entry, true); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stored bearer token for MCP server %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&bearerToken, "bearer-token", "", "bearer token to store")
	cmd.Flags().StringVar(&bearerTokenEnv, "bearer-token-env", "", "environment variable containing the bearer token")
	cmd.Flags().BoolVar(&bearerTokenStdin, "bearer-token-stdin", false, "read bearer token from stdin")
	return cmd
}

func mcpRemoteAuthRemoveCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove stored auth for a remote MCP server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.DeleteOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, name)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed stored auth for MCP server %s\n", name)
			return nil
		},
	}
}

func mcpRemoteAuthStatusCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show whether auth is stored for a remote MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			if _, ok, err := lookupMCPRemoteServer(name, opts.workDir); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			tokens, ok, err := loadMCPRemoteStoredTokens(commandContext(cmd), opts, name)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "no stored auth for MCP server %s\n", name)
				return nil
			}
			if mcpRemoteStoredTokenIsOAuth(tokens) {
				fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for MCP server %s\n", name)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stored bearer token for MCP server %s\n", name)
			return nil
		},
	}
}
