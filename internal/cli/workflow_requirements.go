package cli

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers"
)

func effectiveWorkflowConfig(startDir string) (workflowConfig, error) {
	globalPath, err := globalToolmuxConfigPath()
	if err != nil {
		return workflowConfig{}, err
	}
	sources, err := loadMCPProfileSources(startDir, globalPath)
	if err != nil {
		return workflowConfig{}, err
	}
	config := workflowConfig{Agents: map[string]workflowAgentConfig{}}
	for _, source := range sources {
		if source.config.Workflows.DefaultAgent != "" {
			config.DefaultAgent = source.config.Workflows.DefaultAgent
		}
		maps.Copy(config.Agents, source.config.Workflows.Agents)
	}
	return config, nil
}

func ensureWorkflowRequirements(cmd *cobra.Command, opts *options, workflow workflowFile) error {
	if len(workflow.Requires) == 0 {
		return nil
	}
	store, err := opts.credentials()
	if err != nil {
		return err
	}
	for _, req := range workflow.Requires {
		if err := ensureWorkflowRequirement(cmd, opts, store, req); err != nil {
			return err
		}
	}
	return nil
}

func ensureWorkflowRequirement(cmd *cobra.Command, opts *options, store credentials.Store, req workflowRequirement) error {
	value := strings.TrimSpace(req.Value)
	switch {
	case strings.HasPrefix(value, "internal:"):
		name := strings.TrimSpace(strings.TrimPrefix(value, "internal:"))
		provider, ok := providers.Lookup(name)
		if !ok {
			return fmt.Errorf("workflow requires unknown internal toolbox %q", name)
		}
		ref := credentials.ConnectionRef{Profile: opts.profile, Provider: provider.ID, AccountID: "default"}
		if _, err := store.LoadOAuthTokens(commandContext(cmd), ref); err == nil {
			return nil
		} else if !errors.Is(err, credentials.ErrNotFound) {
			return err
		}
		if provider.AddHandler == nil {
			return fmt.Errorf("workflow requires %s; run `toolmux add %s` first", value, name)
		}
		if handled, err := addNativeToolbox(cmd, opts, name, nativeToolboxAddOptions{}, []string{name}); err != nil {
			return fmt.Errorf("workflow requires %s: automatic toolbox add failed: %w", value, err)
		} else if !handled {
			return fmt.Errorf("workflow requires %s; run `toolmux add %s` first", value, name)
		}
		return nil
	case strings.HasPrefix(value, "catalog:"):
		name := strings.TrimSpace(strings.TrimPrefix(value, "catalog:"))
		if _, ok, err := lookupMCPRemoteServer(name, opts.workDir); err != nil {
			return err
		} else if ok {
			return nil
		}
		if err := addMCPRemoteToolbox(cmd, opts, name, mcpRemoteToolboxAddOptions{}, []string{name}); err != nil {
			return fmt.Errorf("workflow requires catalog:%s: automatic toolbox add failed: %w", name, err)
		}
		return nil
	case isMCPRemoteURLArgument(value):
		name := req.Name
		if name == "" {
			var err error
			name, err = defaultMCPRemoteNameFromURL(value)
			if err != nil {
				return err
			}
		}
		if _, ok, err := lookupMCPRemoteServer(name, opts.workDir); err != nil {
			return err
		} else if ok {
			return nil
		}
		if err := addMCPRemoteToolbox(cmd, opts, value, mcpRemoteToolboxAddOptions{Name: name}, []string{value}); err != nil {
			return fmt.Errorf("workflow requires %s: automatic toolbox add failed: %w", value, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported workflow requirement %q", value)
	}
}
