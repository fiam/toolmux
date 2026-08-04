package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers"
)

func toolboxRenameCommand(opts *options) *cobra.Command {
	return toolboxRenameCommandWithMode(opts, false)
}

func toolboxRenameCommandWithMode(opts *options, remoteOnly bool) *cobra.Command {
	var scope mcpProfileScopeOptions
	var label string
	noun := "toolbox"
	short := "Rename a registered toolbox"
	if remoteOnly {
		noun = "MCP server"
		short = "Rename a registered remote MCP server"
	}
	cmd := &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxRename(cmd, opts, scope, label, remoteOnly, noun, args)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "human-readable account or workspace label for the renamed toolbox")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

type toolboxRenamePlan struct {
	oldName    string
	newName    string
	noun       string
	configPath string
	config     toolmuxConfigFile
	toolbox    toolboxConfig
	keepOld    bool
	oldRef     credentials.ConnectionRef
	newRef     credentials.ConnectionRef
}

func runToolboxRename(
	cmd *cobra.Command,
	opts *options,
	scope mcpProfileScopeOptions,
	label string,
	remoteOnly bool,
	noun string,
	args []string,
) error {
	plan, err := prepareToolboxRename(cmd, opts, scope, remoteOnly, noun, args)
	if err != nil {
		return err
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	hasTokens, err := copyToolboxRenameCredentials(commandContext(cmd), store, plan)
	if err != nil {
		return err
	}
	return commitToolboxRename(cmd, opts, store, plan, label, hasTokens)
}

func prepareToolboxRename(
	cmd *cobra.Command,
	opts *options,
	scope mcpProfileScopeOptions,
	remoteOnly bool,
	noun string,
	args []string,
) (toolboxRenamePlan, error) {
	oldName, err := cleanMCPRemoteName(args[0])
	if err != nil {
		return toolboxRenamePlan{}, err
	}
	newName, err := cleanMCPRemoteName(args[1])
	if err != nil {
		return toolboxRenamePlan{}, err
	}
	if oldName == newName {
		return toolboxRenamePlan{}, fmt.Errorf("old and new %s names are both %q", noun, oldName)
	}
	configPath, err := toolboxRenameConfigPath(opts, oldName, scope, remoteOnly)
	if err != nil {
		return toolboxRenamePlan{}, err
	}
	config, err := readToolmuxConfigFile(configPath)
	if err != nil {
		return toolboxRenamePlan{}, err
	}
	toolbox, exists := toolboxForRename(config, oldName)
	if !exists || (remoteOnly && toolbox.Type != toolboxTypeMCP) {
		return toolboxRenamePlan{}, fmt.Errorf("%s %q is not registered in %s", noun, oldName, configPath)
	}
	if err := validateToolboxRenameDestination(cmd, config, newName, configPath); err != nil {
		return toolboxRenamePlan{}, err
	}
	keepOld, err := toolboxRegisteredOutsidePath(opts, oldName, configPath)
	if err != nil {
		return toolboxRenamePlan{}, err
	}
	oldRef, newRef, err := toolboxRenameCredentialRefs(opts, oldName, newName, toolbox)
	if err != nil {
		return toolboxRenamePlan{}, err
	}
	return toolboxRenamePlan{
		oldName: oldName, newName: newName, noun: noun, configPath: configPath,
		config: config, toolbox: toolbox, keepOld: keepOld, oldRef: oldRef, newRef: newRef,
	}, nil
}

func toolboxForRename(config toolmuxConfigFile, name string) (toolboxConfig, bool) {
	if toolbox, exists := config.Toolboxes[name]; exists {
		return toolbox, true
	}
	server, exists := configMCPRemoteServer(config, name)
	if !exists {
		return toolboxConfig{}, false
	}
	return toolboxConfigFromMCPRemoteServer(server, ""), true
}

func validateToolboxRenameDestination(cmd *cobra.Command, config toolmuxConfigFile, name, configPath string) error {
	if _, exists := config.Toolboxes[name]; exists {
		return fmt.Errorf("toolbox %q is already registered in %s", name, configPath)
	}
	if _, exists := configMCPRemoteServer(config, name); exists {
		return fmt.Errorf("toolbox %q is already registered in %s", name, configPath)
	}
	return ensureToolboxNameAvailable(cmd.Root(), name)
}

func copyToolboxRenameCredentials(ctx context.Context, store credentials.Store, plan toolboxRenamePlan) (bool, error) {
	has, err := store.HasOAuthTokens(ctx, plan.newRef)
	if err != nil {
		return false, err
	}
	if has {
		return false, fmt.Errorf("stored auth already exists for toolbox %q; remove it before renaming", plan.newName)
	}
	tokens, err := store.LoadOAuthTokens(ctx, plan.oldRef)
	if errors.Is(err, credentials.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if plan.toolbox.Type == toolboxTypeMCP {
		if tokens.Extra == nil {
			tokens.Extra = map[string]string{}
		}
		tokens.Extra["mcp_server"] = plan.newName
	}
	if err := store.SaveOAuthTokens(ctx, plan.newRef, tokens); err != nil {
		return false, fmt.Errorf("copy stored auth to toolbox %s: %w", plan.newName, err)
	}
	return true, nil
}

func commitToolboxRename(
	cmd *cobra.Command,
	opts *options,
	store credentials.Store,
	plan toolboxRenamePlan,
	label string,
	hasTokens bool,
) error {
	delete(plan.config.Toolboxes, plan.oldName)
	delete(plan.config.MCP.Servers, plan.oldName)
	if plan.config.Toolboxes == nil {
		plan.config.Toolboxes = map[string]toolboxConfig{}
	}
	if cmd.Flags().Changed("label") {
		plan.toolbox.Label = strings.TrimSpace(label)
	}
	plan.config.Toolboxes[plan.newName] = plan.toolbox
	ctx := commandContext(cmd)
	if err := writeToolmuxConfigFile(plan.configPath, plan.config); err != nil {
		if hasTokens {
			_ = store.DeleteOAuthTokens(ctx, plan.newRef)
		}
		return err
	}
	if err := moveToolboxRenameCache(opts, plan); err != nil {
		return err
	}
	if hasTokens && !plan.keepOld {
		if err := store.DeleteOAuthTokens(ctx, plan.oldRef); err != nil {
			return fmt.Errorf("renamed %s but could not remove old stored auth for %s: %w", plan.noun, plan.oldName, err)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "renamed %s %s to %s in %s\n", plan.noun, plan.oldName, plan.newName, plan.configPath)
	return nil
}

func moveToolboxRenameCache(opts *options, plan toolboxRenamePlan) error {
	if plan.toolbox.Type != toolboxTypeMCP {
		return nil
	}
	if err := moveMCPRemoteCache(opts.mcpCacheDir, plan.oldName, plan.newName, plan.keepOld); err != nil {
		return fmt.Errorf("renamed %s config but could not move cached tools: %w", plan.noun, err)
	}
	return nil
}

func toolboxRegisteredOutsidePath(opts *options, name, selectedPath string) (bool, error) {
	sources, err := loadToolmuxConfigSources(opts.workDir)
	if err != nil {
		return false, err
	}
	for _, source := range sources {
		if sameFilesystemPath(source.Path, selectedPath) {
			continue
		}
		if _, ok := source.config.Toolboxes[name]; ok {
			return true, nil
		}
		if _, ok := source.config.MCP.Servers[name]; ok {
			return true, nil
		}
	}
	return false, nil
}

func toolboxRenameConfigPath(opts *options, oldName string, scope mcpProfileScopeOptions, remoteOnly bool) (string, error) {
	if scope.Global || scope.Project {
		path, _, err := toolmuxConfigWritePath(scope, opts.workDir)
		return path, err
	}
	if entry, ok, err := lookupMCPRemoteServer(oldName, opts.workDir); err != nil {
		return "", err
	} else if ok {
		return entry.Path, nil
	}
	if !remoteOnly {
		if entry, ok, err := lookupNativeToolboxEntry(oldName, opts.workDir); err != nil {
			return "", err
		} else if ok {
			return entry.Path, nil
		}
	}
	return "", fmt.Errorf("toolbox %q is not registered", oldName)
}

func toolboxRenameCredentialRefs(opts *options, oldName, newName string, toolbox toolboxConfig) (credentials.ConnectionRef, credentials.ConnectionRef, error) {
	providerID := mcpRemoteCredentialProvider
	oldService := ""
	newService := ""
	if toolbox.Type == toolboxTypeInternal {
		providerName := strings.TrimSpace(toolbox.Provider)
		if providerName == "" {
			providerName = oldName
		}
		provider, ok := providers.Lookup(providerName)
		if !ok {
			return credentials.ConnectionRef{}, credentials.ConnectionRef{}, fmt.Errorf("toolbox %q references unknown provider %q", oldName, providerName)
		}
		providerID = providers.CredentialProviderID(provider)
	} else {
		oldService = oldName
		newService = newName
	}
	return credentials.ConnectionRef{Profile: opts.profile, Provider: providerID, Service: oldService, AccountID: oldName},
		credentials.ConnectionRef{Profile: opts.profile, Provider: providerID, Service: newService, AccountID: newName}, nil
}
