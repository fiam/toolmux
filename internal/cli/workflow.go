package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
)

func workflowCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage local workflows",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown workflow command %q", args[0])
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(workflowInitCommand(opts))
	cmd.AddCommand(workflowListCommand(opts))
	cmd.AddCommand(workflowShowCommand(opts))
	cmd.AddCommand(workflowRenderCommand(opts))
	cmd.AddCommand(workflowRunCommand(opts))
	cmd.AddCommand(workflowTemplatesCommand(opts))
	cmd.AddCommand(workflowConfigCommand(opts))
	return cmd
}

func workflowInitCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var templateName string
	var force bool
	var noSetup bool
	cmd := &cobra.Command{
		Use:     "init <name>",
		Aliases: []string{"add"},
		Short:   "Create a workflow from a template",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanWorkflowName(args[0])
			if err != nil {
				return err
			}
			if templateName == "" {
				templateName = name
			}
			if err := authorize(cmd, opts, workflowInitSpec(), args); err != nil {
				return err
			}
			workflow, err := loadWorkflowTemplate(commandContext(cmd), opts.httpClient, templateName)
			if err != nil {
				return err
			}
			workflow.Name = name
			if err := validateWorkflow(workflow); err != nil {
				return err
			}
			path, scopeName, err := workflowWritePath(scope, name, opts.workDir)
			if err != nil {
				return err
			}
			if !force {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("workflow %q already exists at %s; pass --force to overwrite", name, path)
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			if !noSetup {
				if err := ensureWorkflowRequirements(cmd, opts, workflow); err != nil {
					return err
				}
			}
			if err := writeWorkflowFile(path, workflow); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created %s workflow %s in %s\n", scopeName, name, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&templateName, "template", "", "workflow template name, GitHub source, or URL")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing workflow")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "skip adding missing required toolboxes")
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

func workflowListCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workflows",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, workflowListSpec(), args); err != nil {
				return err
			}
			entries, err := workflowEntries(opts.workDir)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, entries, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(entries))
				for _, entry := range entries {
					rows = append(rows, []string{
						output.ToneText(human, output.ToneInfo, entry.Name),
						entry.Scope,
						entry.Description,
						entry.Path,
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Name", "Scope", "Description", "Path"},
					Rows:    rows,
					Empty:   "no workflows configured",
				})
			})
		},
	}
}

func workflowShowCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a workflow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, workflowShowSpec(), args); err != nil {
				return err
			}
			entry, workflow, err := lookupWorkflow(args[0], opts.workDir)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, struct {
				workflowFile `yaml:",inline"`
				Path         string `json:"path" yaml:"path"`
				Scope        string `json:"scope" yaml:"scope"`
			}{workflowFile: workflow, Path: entry.Path, Scope: entry.Scope}, nil)
		},
	}
}

func workflowRenderCommand(opts *options) *cobra.Command {
	var inputs []string
	cmd := &cobra.Command{
		Use:   "render <name>",
		Short: "Render a workflow prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, workflowRenderSpec(), args); err != nil {
				return err
			}
			result, err := renderWorkflowByName(args[0], opts.workDir, inputs)
			if err != nil {
				return err
			}
			if opts.output == "table" {
				return writePossiblyPaged(cmd, opts, result.Prompt)
			}
			return writeValue(cmd, opts, result, nil)
		},
	}
	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "workflow input as key=value")
	return cmd
}

func workflowRunCommand(opts *options) *cobra.Command {
	var inputs []string
	var agentName string
	var noSetup bool
	cmd := &cobra.Command{
		Use:   "run <name>",
		Short: "Run a workflow with a local agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, workflowRunSpec(), args); err != nil {
				return err
			}
			entry, workflow, err := lookupWorkflow(args[0], opts.workDir)
			if err != nil {
				return err
			}
			if !noSetup {
				if err := ensureWorkflowRequirements(cmd, opts, workflow); err != nil {
					return err
				}
			}
			rendered, err := renderWorkflow(workflow, entry.Path, inputs)
			if err != nil {
				return err
			}
			config, err := effectiveWorkflowConfig(opts.workDir)
			if err != nil {
				return err
			}
			agent, err := resolveWorkflowAgentForCommand(cmd, opts, workflow, config, agentName)
			if err != nil {
				return err
			}
			command, commandArgs, err := workflowAgentCommand(agent, rendered.Prompt)
			if err != nil {
				return err
			}
			return runWorkflowAgent(commandContext(cmd), cmd, command, commandArgs)
		},
	}
	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "workflow input as key=value")
	cmd.Flags().StringVar(&agentName, "agent", "", "workflow agent name or command")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "skip adding missing required toolboxes")
	return cmd
}

func workflowTemplatesCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "templates",
		Short: "List workflow templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, workflowListSpec(), args); err != nil {
				return err
			}
			templates := workflowTemplateCatalog()
			return writeValue(cmd, opts, templates, func(w io.Writer) {
				human := humanOutputOptions(cmd, opts)
				rows := make([][]string, 0, len(templates))
				for _, template := range templates {
					rows = append(rows, []string{
						output.ToneText(human, output.ToneInfo, template.Name),
						template.Description,
						template.Source,
					})
				}
				output.RenderTable(w, human, output.Table{
					Headers: []string{"Name", "Description", "Source"},
					Rows:    rows,
				})
			})
		},
	}
}

func workflowConfigCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage workflow configuration",
	}
	var scope mcpProfileScopeOptions
	set := &cobra.Command{
		Use:   "set default-agent [agent]",
		Short: "Set the default workflow agent",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "default-agent" {
				return fmt.Errorf("unknown workflow config key %q", args[0])
			}
			if err := authorize(cmd, opts, workflowConfigSetDefaultAgentSpec(), args); err != nil {
				return err
			}
			agentName := ""
			if len(args) == 2 {
				agentName = strings.TrimSpace(args[1])
			} else if interactiveCommand(cmd, opts) {
				config, err := effectiveWorkflowConfig(opts.workDir)
				if err != nil {
					return err
				}
				selected, ok, err := selectWorkflowAgentInteractive(cmd, config, "Set default workflow agent")
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), "no default workflow agent selected")
					return nil
				}
				agentName = selected
			}
			if agentName == "" {
				return fmt.Errorf("default workflow agent is required; pass an agent or run interactively")
			}
			configPath, scopeName, err := toolmuxConfigWritePath(scope, opts.workDir)
			if err != nil {
				return err
			}
			config, err := readToolmuxConfigFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			config.Version = 1
			config.Workflows.DefaultAgent = agentName
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set %s default workflow agent to %s in %s\n", scopeName, config.Workflows.DefaultAgent, configPath)
			return nil
		},
	}
	addMCPProfileScopeFlags(set, &scope)
	cmd.AddCommand(set)
	return cmd
}
