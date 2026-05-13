package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers"
)

const (
	workflowProjectRelDir  = ".toolmux/workflows"
	workflowTemplateRepo   = "fiam/toolmux"
	workflowTemplateRef    = "main"
	workflowTemplateFolder = "workflows"
)

type workflowTemplateEntry struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Source      string `json:"source" yaml:"source"`
}

type workflowFile struct {
	Version     int                         `json:"version" yaml:"version"`
	Name        string                      `json:"name" yaml:"name"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Requires    []workflowRequirement       `json:"requires,omitempty" yaml:"requires,omitempty"`
	Agent       workflowAgentRef            `json:"agent,omitzero" yaml:"agent,omitempty"`
	Inputs      map[string]workflowInput    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Prompt      string                      `json:"prompt" yaml:"prompt"`
	Template    *workflowTemplateProvenance `json:"template,omitempty" yaml:"template,omitempty"`
}

type workflowTemplateProvenance struct {
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
	Ref    string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

type workflowRequirement struct {
	Value string `json:"value" yaml:"value"`
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
}

type workflowInput struct {
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool    `json:"required,omitempty" yaml:"required,omitempty"`
	Default     *string `json:"default,omitempty" yaml:"default,omitempty"`
}

type workflowAgentRef struct {
	Set    bool
	Name   string
	Config workflowAgentConfig
}

type workflowAgentConfig struct {
	Command string   `json:"command,omitempty" yaml:"command,omitempty"`
	Args    []string `json:"args,omitempty" yaml:"args,omitempty"`
}

type workflowEntry struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Scope       string `json:"scope" yaml:"scope"`
	Path        string `json:"path" yaml:"path"`
}

type workflowRenderResult struct {
	Name   string            `json:"name" yaml:"name"`
	Path   string            `json:"path" yaml:"path"`
	Inputs map[string]string `json:"inputs" yaml:"inputs"`
	Prompt string            `json:"prompt" yaml:"prompt"`
}

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

func (req *workflowRequirement) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		req.Value = strings.TrimSpace(node.Value)
		return nil
	case yaml.MappingNode:
		var raw struct {
			URL      string `yaml:"url"`
			Catalog  string `yaml:"catalog"`
			Internal string `yaml:"internal"`
			Name     string `yaml:"name"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		req.Name = strings.TrimSpace(raw.Name)
		req.Value = firstNonEmpty(raw.URL, prefixedValue("catalog", raw.Catalog), prefixedValue("internal", raw.Internal))
		return nil
	default:
		return fmt.Errorf("workflow requirement must be a string or object")
	}
}

func (req workflowRequirement) MarshalYAML() (any, error) {
	if req.Name == "" {
		return req.Value, nil
	}
	return map[string]string{"url": req.Value, "name": req.Name}, nil
}

func prefixedValue(prefix, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, ":") {
		return value
	}
	return prefix + ":" + value
}

func (ref *workflowAgentRef) UnmarshalYAML(node *yaml.Node) error {
	ref.Set = true
	switch node.Kind {
	case yaml.ScalarNode:
		ref.Name = strings.TrimSpace(node.Value)
		return nil
	case yaml.MappingNode:
		var raw struct {
			Name    string   `yaml:"name"`
			Target  string   `yaml:"target"`
			Command string   `yaml:"command"`
			Args    []string `yaml:"args"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		ref.Name = firstNonEmpty(raw.Name, raw.Target)
		ref.Config = workflowAgentConfig{Command: strings.TrimSpace(raw.Command), Args: append([]string(nil), raw.Args...)}
		return nil
	default:
		return fmt.Errorf("workflow agent must be a string or object")
	}
}

func (ref workflowAgentRef) MarshalYAML() (any, error) {
	if !ref.Set {
		return nil, nil
	}
	if ref.Config.Command == "" && len(ref.Config.Args) == 0 {
		return ref.Name, nil
	}
	out := map[string]any{}
	if ref.Name != "" {
		out["name"] = ref.Name
	}
	if ref.Config.Command != "" {
		out["command"] = ref.Config.Command
	}
	if len(ref.Config.Args) > 0 {
		out["args"] = ref.Config.Args
	}
	return out, nil
}

func (ref workflowAgentRef) IsZero() bool {
	return !ref.Set
}

func renderWorkflowByName(name, startDir string, values []string) (workflowRenderResult, error) {
	entry, workflow, err := lookupWorkflow(name, startDir)
	if err != nil {
		return workflowRenderResult{}, err
	}
	return renderWorkflow(workflow, entry.Path, values)
}

func renderWorkflow(workflow workflowFile, path string, values []string) (workflowRenderResult, error) {
	if err := validateWorkflow(workflow); err != nil {
		return workflowRenderResult{}, err
	}
	inputs, err := workflowInputValues(workflow, values)
	if err != nil {
		return workflowRenderResult{}, err
	}
	prompt, err := renderTemplate("workflow "+workflow.Name+" prompt", workflow.Prompt, inputs)
	if err != nil {
		return workflowRenderResult{}, err
	}
	return workflowRenderResult{
		Name:   workflow.Name,
		Path:   path,
		Inputs: inputs,
		Prompt: prompt,
	}, nil
}

func workflowInputValues(workflow workflowFile, values []string) (map[string]string, error) {
	provided, err := parseWorkflowInputs(values)
	if err != nil {
		return nil, err
	}
	inputs := map[string]string{}
	for name, input := range workflow.Inputs {
		if value, ok := provided[name]; ok {
			inputs[name] = value
			delete(provided, name)
			continue
		}
		if input.Default != nil {
			inputs[name] = *input.Default
			continue
		}
		return nil, fmt.Errorf("workflow %s is missing required input %s; pass --input %s=<value>", workflow.Name, name, name)
	}
	if len(provided) > 0 {
		names := make([]string, 0, len(provided))
		for name := range provided {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown workflow input(s): %s", strings.Join(names, ", "))
	}
	return inputs, nil
}

func parseWorkflowInputs(values []string) (map[string]string, error) {
	inputs := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("workflow input must be key=value: %q", value)
		}
		inputs[key] = val
	}
	return inputs, nil
}

func renderTemplate(name, source string, data map[string]string) (string, error) {
	tpl, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func resolveWorkflowAgent(workflow workflowFile, config workflowConfig, override string) (workflowAgentConfig, error) {
	if strings.TrimSpace(override) != "" {
		return workflowAgentByName(strings.TrimSpace(override), config)
	}
	if workflow.Agent.Set {
		if workflow.Agent.Config.Command != "" {
			return workflow.Agent.Config, nil
		}
		if workflow.Agent.Name != "" {
			return workflowAgentByName(workflow.Agent.Name, config)
		}
	}
	if config.DefaultAgent != "" {
		return workflowAgentByName(config.DefaultAgent, config)
	}
	return workflowAgentConfig{}, fmt.Errorf("workflow %s has no agent; pass --agent or set workflows.default_agent", workflow.Name)
}

func resolveWorkflowAgentForCommand(cmd *cobra.Command, opts *options, workflow workflowFile, config workflowConfig, override string) (workflowAgentConfig, error) {
	if strings.TrimSpace(override) != "" || workflow.Agent.Set || config.DefaultAgent != "" || !interactiveCommand(cmd, opts) {
		return resolveWorkflowAgent(workflow, config, override)
	}
	selected, ok, err := selectWorkflowAgentInteractive(cmd, config, "Select workflow agent")
	if err != nil {
		return workflowAgentConfig{}, err
	}
	if !ok {
		return workflowAgentConfig{}, fmt.Errorf("workflow %s has no agent; pass --agent or set workflows.default_agent", workflow.Name)
	}
	return workflowAgentByName(selected, config)
}

func workflowAgentByName(name string, config workflowConfig) (workflowAgentConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return workflowAgentConfig{}, fmt.Errorf("workflow agent is required")
	}
	if config.Agents != nil {
		if agent, ok := config.Agents[name]; ok {
			if agent.Command == "" {
				agent.Command = name
			}
			return agent, nil
		}
	}
	parts, err := splitWorkflowAgentCommandLine(name)
	if err != nil {
		return workflowAgentConfig{}, err
	}
	if len(parts) == 0 {
		return workflowAgentConfig{}, fmt.Errorf("workflow agent is required")
	}
	if config.Agents != nil {
		if agent, ok := config.Agents[parts[0]]; ok {
			if agent.Command == "" {
				agent.Command = parts[0]
			}
			agent.Args = append(append([]string(nil), agent.Args...), parts[1:]...)
			return agent, nil
		}
	}
	if canonical, ok := canonicalMCPAgent(parts[0]); ok {
		definition, _ := mcpAgentDefinition(canonical)
		return workflowAgentConfig{Command: definition.command, Args: parts[1:]}, nil
	}
	return workflowAgentConfig{Command: parts[0], Args: parts[1:]}, nil
}

func selectWorkflowAgentInteractive(cmd *cobra.Command, config workflowConfig, title string) (string, bool, error) {
	candidates := workflowAgentCandidates(config)
	if len(candidates) == 0 {
		return "", false, fmt.Errorf("no workflow agents available; install codex, claude, or gemini, configure workflows.agents, or pass --agent")
	}
	selected := candidates[0].Name
	options := make([]huh.Option[string], 0, len(candidates))
	for _, candidate := range candidates {
		options = append(options, huh.NewOption(candidate.Label, candidate.Name))
	}
	height := min(len(options)+4, 10)
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Description("Choose the local agent command for this workflow.").
			Options(options...).
			Value(&selected).
			Height(height).
			Filtering(false),
	)).
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.ErrOrStderr()).
		WithWidth(terminalWidth(cmd.ErrOrStderr())).
		WithHeight(height + 5)
	if err := form.RunWithContext(commandContext(cmd)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", false, nil
		}
		return "", false, err
	}
	return selected, true, nil
}

type workflowAgentCandidate struct {
	Name  string
	Label string
}

func workflowAgentCandidates(config workflowConfig) []workflowAgentCandidate {
	seen := map[string]bool{}
	var candidates []workflowAgentCandidate
	names := make([]string, 0, len(config.Agents))
	for name := range config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		candidates = append(candidates, workflowAgentCandidate{Name: name, Label: name + " (configured)"})
		seen[name] = true
	}
	for _, agent := range supportedMCPAgents() {
		definition, _ := mcpAgentDefinition(agent)
		if _, err := exec.LookPath(definition.command); err != nil {
			continue
		}
		if seen[agent] {
			continue
		}
		candidates = append(candidates, workflowAgentCandidate{Name: agent, Label: mcpAgentDisplayName(agent)})
		seen[agent] = true
	}
	return candidates
}

func workflowAgentCommand(agent workflowAgentConfig, prompt string) (string, []string, error) {
	if strings.TrimSpace(agent.Command) == "" {
		return "", nil, fmt.Errorf("workflow agent command is required")
	}
	data := map[string]string{"prompt": prompt}
	usesPrompt := strings.Contains(agent.Command, ".prompt")
	for _, arg := range agent.Args {
		if strings.Contains(arg, ".prompt") {
			usesPrompt = true
			break
		}
	}
	command, err := renderTemplate("workflow agent command", agent.Command, data)
	if err != nil {
		return "", nil, err
	}
	args := make([]string, 0, len(agent.Args)+1)
	for _, raw := range agent.Args {
		arg, err := renderTemplate("workflow agent arg", raw, data)
		if err != nil {
			return "", nil, err
		}
		args = append(args, arg)
	}
	if !usesPrompt {
		args = append(args, prompt)
	}
	if strings.TrimSpace(command) == "" {
		return "", nil, fmt.Errorf("workflow agent command rendered empty")
	}
	return command, args, nil
}

func splitWorkflowAgentCommandLine(value string) ([]string, error) {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	hadToken := false
	for _, r := range value {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			hadToken = true
		case r == '\\' && !inSingle:
			escaped = true
			hadToken = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			hadToken = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			hadToken = true
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			if hadToken {
				args = append(args, current.String())
				current.Reset()
				hadToken = false
			}
		default:
			current.WriteRune(r)
			hadToken = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("workflow agent command has unfinished escape")
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("workflow agent command has unterminated quote")
	}
	if hadToken {
		args = append(args, current.String())
	}
	return args, nil
}

func runWorkflowAgent(ctx context.Context, cmd *cobra.Command, name string, args []string) error {
	// #nosec G204 -- workflow agent commands come from explicit local workflow configuration.
	process := exec.CommandContext(ctx, name, args...)
	process.Stdin = cmd.InOrStdin()
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	return process.Run()
}

func validateWorkflow(workflow workflowFile) error {
	if workflow.Version == 0 {
		return fmt.Errorf("workflow version is required")
	}
	if workflow.Version != 1 {
		return fmt.Errorf("unsupported workflow version %d", workflow.Version)
	}
	if _, err := cleanWorkflowName(workflow.Name); err != nil {
		return err
	}
	if strings.TrimSpace(workflow.Prompt) == "" {
		return fmt.Errorf("workflow %s prompt is required", workflow.Name)
	}
	for name := range workflow.Inputs {
		if err := validateWorkflowInputName(name); err != nil {
			return err
		}
	}
	for _, req := range workflow.Requires {
		if strings.TrimSpace(req.Value) == "" {
			return fmt.Errorf("workflow %s has an empty requirement", workflow.Name)
		}
	}
	return nil
}

func cleanWorkflowName(value string) (string, error) {
	name := sanitizeMCPName(value)
	if name == "" {
		return "", fmt.Errorf("workflow name is required")
	}
	return name, nil
}

func validateWorkflowInputName(name string) error {
	if name == "" {
		return fmt.Errorf("workflow input name is required")
	}
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_'
		if i > 0 {
			ok = ok || r >= '0' && r <= '9'
		}
		if !ok {
			return fmt.Errorf("invalid workflow input name %q: use Go template identifier characters", name)
		}
	}
	return nil
}

func lookupWorkflow(name, startDir string) (workflowEntry, workflowFile, error) {
	cleaned, err := cleanWorkflowName(name)
	if err != nil {
		return workflowEntry{}, workflowFile{}, err
	}
	entries, err := workflowEntries(startDir)
	if err != nil {
		return workflowEntry{}, workflowFile{}, err
	}
	for _, entry := range entries {
		if entry.Name != cleaned {
			continue
		}
		workflow, err := readWorkflowFile(entry.Path)
		if err != nil {
			return workflowEntry{}, workflowFile{}, err
		}
		return entry, workflow, nil
	}
	return workflowEntry{}, workflowFile{}, fmt.Errorf("workflow %q not found", cleaned)
}

func workflowEntries(startDir string) ([]workflowEntry, error) {
	byName := map[string]workflowEntry{}
	globalDir := ""
	if dir, err := globalWorkflowDir(); err != nil {
		return nil, err
	} else if entries, err := workflowEntriesInDir(dir, "global"); err != nil {
		return nil, err
	} else {
		globalDir = dir
		for _, entry := range entries {
			byName[entry.Name] = entry
		}
	}
	if dir, ok, err := discoverWorkflowDir(startDir); err != nil {
		return nil, err
	} else if ok && !sameFilesystemPath(dir, globalDir) {
		entries, err := workflowEntriesInDir(dir, "project")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			byName[entry.Name] = entry
		}
	}
	entries := make([]workflowEntry, 0, len(byName))
	for _, entry := range byName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func workflowEntriesInDir(dir, scope string) ([]workflowEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var entries []workflowEntry
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, item.Name())
		workflow, err := readWorkflowFile(path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, workflowEntry{
			Name:        workflow.Name,
			Description: workflow.Description,
			Scope:       scope,
			Path:        path,
		})
	}
	return entries, nil
}

func readWorkflowFile(path string) (workflowFile, error) {
	// #nosec G304 -- workflow paths are explicit Toolmux configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return workflowFile{}, err
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return workflowFile{}, err
	}
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if workflow.Name == "" {
		workflow.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return workflow, validateWorkflow(workflow)
}

func writeWorkflowFile(path string, workflow workflowFile) error {
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := yaml.Marshal(workflow)
	if err != nil {
		return err
	}
	// #nosec G306 -- workflows are non-secret local tool configuration.
	return os.WriteFile(path, data, 0o644)
}

func workflowWritePath(scope mcpProfileScopeOptions, name, startDir string) (string, string, error) {
	if scope.Global && scope.Project {
		return "", "", fmt.Errorf("use only one of --global or --project")
	}
	if scope.Project {
		if startDir == "" {
			var err error
			startDir, err = os.Getwd()
			if err != nil {
				return "", "", err
			}
		}
		return filepath.Join(startDir, workflowProjectRelDir, name+".yaml"), "project", nil
	}
	dir, err := globalWorkflowDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, name+".yaml"), "global", nil
}

func globalWorkflowDir() (string, error) {
	configPath, err := globalToolmuxConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "workflows"), nil
}

func discoverWorkflowDir(startDir string) (string, bool, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", false, err
		}
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false, err
	}
	for {
		candidate := filepath.Join(dir, workflowProjectRelDir)
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate, true, nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false, nil
}

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

func loadWorkflowTemplate(ctx context.Context, client *http.Client, source string) (workflowFile, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return workflowFile{}, fmt.Errorf("workflow template source is required")
	}
	if template, ok := workflowTemplateByName(source); ok {
		source = template.Source
	}
	if spec, ok := strings.CutPrefix(source, "github:"); ok {
		return loadGitHubWorkflowTemplate(ctx, client, spec)
	}
	if isMCPRemoteURLArgument(source) {
		return loadURLWorkflowTemplate(ctx, client, source)
	}
	return workflowFile{}, fmt.Errorf("unknown workflow template %q", source)
}

func loadGitHubWorkflowTemplate(ctx context.Context, client *http.Client, spec string) (workflowFile, error) {
	ref := "main"
	base, after, found := strings.Cut(spec, "@")
	if found {
		ref = after
	}
	parts := strings.SplitN(base, "/", 3)
	if len(parts) < 3 {
		return workflowFile{}, fmt.Errorf("GitHub workflow template must be github:owner/repo/path[@ref]")
	}
	path := strings.TrimPrefix(parts[2], "/")
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		path = strings.TrimRight(path, "/") + "/workflow.yaml"
	}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), url.PathEscape(ref), path)
	workflow, err := loadURLWorkflowTemplate(ctx, client, rawURL)
	if err != nil {
		return workflowFile{}, err
	}
	workflow.Template = &workflowTemplateProvenance{Source: "github:" + base, Ref: ref}
	return workflow, nil
}

func loadURLWorkflowTemplate(ctx context.Context, client *http.Client, rawURL string) (workflowFile, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return workflowFile{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return workflowFile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return workflowFile{}, fmt.Errorf("workflow template %s returned status %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return workflowFile{}, err
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return workflowFile{}, err
	}
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if workflow.Template == nil {
		workflow.Template = &workflowTemplateProvenance{Source: rawURL}
	}
	return workflow, validateWorkflow(workflow)
}

func workflowTemplateCatalog() []workflowTemplateEntry {
	return []workflowTemplateEntry{{
		Name:        "slack-recap",
		Description: "Summarize Slack channel activity since a time",
		Source:      workflowTemplateGitHubSource("slack-recap.yaml"),
	}}
}

func workflowTemplateByName(name string) (workflowTemplateEntry, bool) {
	name = strings.TrimSpace(name)
	for _, template := range workflowTemplateCatalog() {
		if template.Name == name {
			return template, true
		}
	}
	return workflowTemplateEntry{}, false
}

func workflowTemplateGitHubSource(name string) string {
	path := strings.Trim(strings.TrimSpace(name), "/")
	return fmt.Sprintf("github:%s/%s/%s@%s", workflowTemplateRepo, workflowTemplateFolder, path, workflowTemplateRef)
}

func workflowInitSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.init", "init",
		actions.Use("workflow init <name>"),
		actions.Short("Create workflow"),
		actions.RBAC("workflow", actions.VerbCreate, actions.EffectNone, actions.EffectWrite),
	)
}

func workflowListSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.list", "list",
		actions.Use("workflow list"),
		actions.Short("List workflows"),
		actions.RBAC("workflow", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func workflowShowSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.show", "show",
		actions.Use("workflow show <name>"),
		actions.Short("Show workflow"),
		actions.RBAC("workflow", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}

func workflowRenderSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.render", "render",
		actions.Use("workflow render <name>"),
		actions.Short("Render workflow prompt"),
		actions.RBAC("workflow", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}

func workflowRunSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.run", "run",
		actions.Use("workflow run <name>"),
		actions.Short("Run workflow"),
		actions.RBAC("workflow", actions.VerbRun, actions.EffectWrite, actions.EffectWrite),
	)
}

func workflowConfigSetDefaultAgentSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.config.default_agent", "default-agent",
		actions.Use("workflow config set default-agent [agent]"),
		actions.Short("Set default workflow agent"),
		actions.RBAC("workflow_config", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
	)
}
