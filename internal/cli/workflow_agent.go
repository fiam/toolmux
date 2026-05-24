package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

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
