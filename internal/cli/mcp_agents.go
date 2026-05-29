package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

type mcpAgentRuntime struct {
	lookPath func(string) (string, error)
	run      func(context.Context, string, []string) error
	inspect  func(context.Context, string, string, mcpConfigureOptions) mcpAgentStatus
}

type mcpAgentResult struct {
	Agent      string
	ServerName string
	Scope      string
	Commands   []mcpExternalCommand
}

type mcpExternalCommand struct {
	Name        string
	Args        []string
	IgnoreError bool
}

type mcpAgentStatus struct {
	Configured bool
	Enabled    bool
	Scope      string
	Command    string
	Args       string
	Transport  string
	Path       string
}

type mcpAgentInteractiveSelection struct {
	Selected []string
	Removed  []string
}

func (command mcpExternalCommand) String() string {
	values := append([]string{command.Name}, command.Args...)
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellQuote(value))
	}
	return strings.Join(quoted, " ")
}

func configureMCPAgents(ctx context.Context, runtime mcpAgentRuntime, opts *options, configure mcpConfigureOptions, names []string) ([]mcpAgentResult, error) {
	selected, err := selectMCPAgents(runtime, names, configure.DryRun)
	if err != nil {
		return nil, err
	}
	serverName := mcpConfiguredServerName(configure)
	serveArgs := mcpConfiguredServeArgs(opts, configure)
	results := make([]mcpAgentResult, 0, len(selected))
	for _, agent := range selected {
		result, err := mcpAgentConfigureCommands(agent, serverName, configure, serveArgs)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		if configure.DryRun {
			continue
		}
		if err := runMCPExternalCommands(ctx, runtime, result.Commands); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func removeMCPAgents(ctx context.Context, runtime mcpAgentRuntime, configure mcpConfigureOptions, names []string) ([]mcpAgentResult, error) {
	if len(names) == 0 {
		return nil, nil
	}
	serverName := mcpConfiguredServerName(configure)
	results := make([]mcpAgentResult, 0, len(names))
	for _, agent := range orderMCPAgents(names) {
		result, err := mcpAgentRemoveCommands(agent, serverName)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		if configure.DryRun {
			continue
		}
		if err := runMCPExternalCommands(ctx, runtime, result.Commands); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func runMCPExternalCommands(ctx context.Context, runtime mcpAgentRuntime, commands []mcpExternalCommand) error {
	for _, command := range commands {
		if err := runtime.run(ctx, command.Name, command.Args); err != nil && !command.IgnoreError {
			return err
		}
	}
	return nil
}

func selectMCPAgentsInteractive(cmd *cobra.Command, runtime mcpAgentRuntime, serverName string, configure mcpConfigureOptions) (mcpAgentInteractiveSelection, error) {
	detected, err := detectMCPAgents(runtime)
	if err != nil {
		return mcpAgentInteractiveSelection{}, err
	}
	ctx := commandContext(cmd)
	statuses := inspectMCPAgents(ctx, runtime, detected, serverName, configure)
	selected := selectedEnabledMCPAgents(statuses, detected)
	selectedSet := make(map[string]bool, len(selected))
	for _, agent := range selected {
		selectedSet[agent] = true
	}
	options := make([]huh.Option[string], 0, len(detected))
	for _, agent := range detected {
		option := huh.NewOption(mcpAgentOptionTitle(agent, statuses[agent]), agent)
		if selectedSet[agent] {
			option = option.Selected(true)
		}
		options = append(options, option)
	}
	height := min(len(options)+4, 10)
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Configure MCP agents").
			Description("Selected agents will be updated to run `toolmux mcp serve`.").
			Options(options...).
			Value(&selected).
			Height(height).
			Filterable(false),
	)).
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.ErrOrStderr()).
		WithWidth(terminalWidth(cmd.ErrOrStderr())).
		WithHeight(height + 7)
	if err := form.RunWithContext(commandContext(cmd)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return mcpAgentInteractiveSelection{}, nil
		}
		return mcpAgentInteractiveSelection{}, err
	}
	selected = orderMCPAgents(selected)
	selectedSet = make(map[string]bool, len(selected))
	for _, agent := range selected {
		selectedSet[agent] = true
	}
	var removed []string
	for _, agent := range detected {
		status := statuses[agent]
		if status.Configured && !selectedSet[agent] {
			removed = append(removed, agent)
		}
	}
	return mcpAgentInteractiveSelection{Selected: selected, Removed: orderMCPAgents(removed)}, nil
}

func inspectMCPAgents(ctx context.Context, runtime mcpAgentRuntime, agents []string, serverName string, configure mcpConfigureOptions) map[string]mcpAgentStatus {
	statuses := make(map[string]mcpAgentStatus, len(agents))
	for _, agent := range agents {
		var status mcpAgentStatus
		if runtime.inspect != nil {
			status = runtime.inspect(ctx, agent, serverName, configure)
		}
		statuses[agent] = status
	}
	return statuses
}

func selectedEnabledMCPAgents(statuses map[string]mcpAgentStatus, agents []string) []string {
	selected := make([]string, 0, len(agents))
	for _, agent := range agents {
		status := statuses[agent]
		if status.Configured && status.Enabled {
			selected = append(selected, agent)
		}
	}
	return selected
}

func selectMCPAgents(runtime mcpAgentRuntime, names []string, dryRun bool) ([]string, error) {
	if len(names) == 0 {
		return detectMCPAgents(runtime)
	}
	var selected []string
	seen := map[string]bool{}
	for _, name := range names {
		agent, ok := canonicalMCPAgent(name)
		if !ok {
			return nil, fmt.Errorf("unknown MCP agent %q; supported agents: %s", name, strings.Join(supportedMCPAgents(), ", "))
		}
		if seen[agent] {
			continue
		}
		if !dryRun {
			definition, _ := mcpAgentDefinition(agent)
			if _, err := runtime.lookPath(definition.command); err != nil {
				return nil, fmt.Errorf("%s CLI not found in PATH", definition.command)
			}
		}
		selected = append(selected, agent)
		seen[agent] = true
	}
	return selected, nil
}

func detectMCPAgents(runtime mcpAgentRuntime) ([]string, error) {
	var detected []string
	for _, agent := range supportedMCPAgents() {
		definition, _ := mcpAgentDefinition(agent)
		if _, err := runtime.lookPath(definition.command); err == nil {
			detected = append(detected, agent)
		}
	}
	if len(detected) == 0 {
		return nil, fmt.Errorf("no supported MCP agents detected; pass one of: %s", strings.Join(supportedMCPAgents(), ", "))
	}
	return detected, nil
}

func orderMCPAgents(agents []string) []string {
	selected := make(map[string]bool, len(agents))
	for _, agent := range agents {
		selected[agent] = true
	}
	ordered := make([]string, 0, len(agents))
	for _, agent := range supportedMCPAgents() {
		if selected[agent] {
			ordered = append(ordered, agent)
		}
	}
	return ordered
}

type mcpAgentDefinitionValue struct {
	command         string
	workflowDefault []string
}

func supportedMCPAgents() []string {
	return []string{"codex", "claude"}
}

func canonicalMCPAgent(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "codex":
		return "codex", true
	case "claude", "claude-code":
		return "claude", true
	default:
		return "", false
	}
}

func mcpAgentDefinition(agent string) (mcpAgentDefinitionValue, bool) {
	switch agent {
	case "codex":
		return mcpAgentDefinitionValue{command: "codex", workflowDefault: []string{"exec"}}, true
	case "claude":
		return mcpAgentDefinitionValue{command: "claude", workflowDefault: []string{"-p"}}, true
	default:
		return mcpAgentDefinitionValue{}, false
	}
}

func mcpAgentDisplayName(agent string) string {
	switch agent {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	default:
		return agent
	}
}

func mcpAgentOptionTitle(agent string, status mcpAgentStatus) string {
	display := mcpAgentDisplayName(agent)
	if !status.Configured {
		return display + " - not configured"
	}
	return display + " - " + status.summary()
}

func (status mcpAgentStatus) summary() string {
	state := "enabled"
	if !status.Enabled {
		state = "disabled"
	}
	parts := []string{state}
	if status.Scope != "" {
		parts = append(parts, status.Scope)
	}
	if status.Transport != "" {
		parts = append(parts, "transport="+status.Transport)
	}
	command := strings.TrimSpace(strings.Join(compactStrings([]string{status.Command, status.Args}), " "))
	if command != "" {
		parts = append(parts, command)
	}
	if status.Path != "" {
		parts = append(parts, status.Path)
	}
	return strings.Join(parts, ", ")
}

func mcpAgentConfigureCommands(agent, serverName string, configure mcpConfigureOptions, serveArgs []string) (mcpAgentResult, error) {
	switch agent {
	case "codex":
		return mcpAgentResult{
			Agent:      "codex",
			ServerName: serverName,
			Commands: []mcpExternalCommand{
				{Name: "codex", Args: []string{"mcp", "remove", serverName}, IgnoreError: true},
				{Name: "codex", Args: append([]string{"mcp", "add", serverName, "--", configure.Command}, serveArgs...)},
			},
		}, nil
	case "claude":
		scope, flag := configure.claudeAgentScope()
		if scope != "local" && scope != "user" && scope != "project" {
			return mcpAgentResult{}, fmt.Errorf("%s must be local, user, or project", flag)
		}
		return mcpAgentResult{
			Agent:      "claude",
			ServerName: serverName,
			Scope:      "scope=" + scope,
			Commands: []mcpExternalCommand{
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", "local", serverName}, IgnoreError: true},
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", "user", serverName}, IgnoreError: true},
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", "project", serverName}, IgnoreError: true},
				{Name: "claude", Args: append([]string{"mcp", "add", "--scope", scope, "--transport", "stdio", serverName, "--", configure.Command}, serveArgs...)},
			},
		}, nil
	default:
		return mcpAgentResult{}, fmt.Errorf("unknown MCP agent %q", agent)
	}
}

func mcpAgentRemoveCommands(agent, serverName string) (mcpAgentResult, error) {
	switch agent {
	case "codex":
		return mcpAgentResult{
			Agent:      "codex",
			ServerName: serverName,
			Commands: []mcpExternalCommand{
				{Name: "codex", Args: []string{"mcp", "remove", serverName}, IgnoreError: true},
			},
		}, nil
	case "claude":
		return mcpAgentResult{
			Agent:      "claude",
			ServerName: serverName,
			Scope:      "all scopes",
			Commands: []mcpExternalCommand{
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", "local", serverName}, IgnoreError: true},
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", "user", serverName}, IgnoreError: true},
				{Name: "claude", Args: []string{"mcp", "remove", "--scope", "project", serverName}, IgnoreError: true},
			},
		}, nil
	default:
		return mcpAgentResult{}, fmt.Errorf("unknown MCP agent %q", agent)
	}
}

func (configure mcpConfigureOptions) claudeAgentScope() (string, string) {
	if scope := strings.TrimSpace(configure.ClaudeScope); scope != "" {
		return scope, "--claude-scope"
	}
	return configure.commonAgentScope(), "--scope"
}

func (configure mcpConfigureOptions) commonAgentScope() string {
	scope := strings.TrimSpace(configure.AgentScope)
	if scope == "" {
		return "user"
	}
	return scope
}

func mcpConfiguredServerName(configure mcpConfigureOptions) string {
	if name := strings.TrimSpace(configure.ServerName); name != "" {
		return name
	}
	if profile := sanitizeMCPName(configure.Profile); profile != "" {
		return "toolmux-" + profile
	}
	return "toolmux"
}

func sanitizeMCPName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if allowed {
			builder.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func mcpConfiguredServeArgs(opts *options, configure mcpConfigureOptions) []string {
	args := []string{"mcp", "serve"}
	if opts.profile != "" && opts.profile != "default" {
		args = append(args, "--profile", opts.profile)
	}
	if opts.policy != "" {
		args = append(args, "--policy", opts.policy)
	}
	if opts.readOnly {
		args = append(args, "--read-only")
	}
	if timeout := mcpRemoteToolCallTimeout(opts); timeout != mcpRemoteSSEIdleTimeout {
		args = append(args, "--mcp-tool-call-timeout", timeout.String())
	}
	args = append(args, mcpToolSelectionArgs(configure.mcpToolSelection)...)
	return args
}

func runMCPAgentCommand(ctx context.Context, name string, args []string) error {
	_, err := outputMCPAgentCommand(ctx, name, args)
	return err
}

func outputMCPAgentCommand(ctx context.Context, name string, args []string) (string, error) {
	// #nosec G204 -- agent commands are selected from supported local CLIs.
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return string(output), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, detail)
		}
		return string(output), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(output), nil
}

func inspectMCPAgent(ctx context.Context, agent, serverName string, configure mcpConfigureOptions) mcpAgentStatus {
	switch agent {
	case "codex":
		return inspectCommandMCPAgent(ctx, "codex", []string{"mcp", "get", serverName})
	case "claude":
		return inspectCommandMCPAgent(ctx, "claude", []string{"mcp", "get", serverName})
	default:
		return mcpAgentStatus{}
	}
}

func inspectCommandMCPAgent(ctx context.Context, command string, args []string) mcpAgentStatus {
	output, err := outputMCPAgentCommand(ctx, command, args)
	if err != nil {
		return mcpAgentStatus{}
	}
	status := mcpAgentStatus{
		Configured: true,
		Enabled:    true,
	}
	fields := parseMCPAgentFields(output)
	if enabled, ok := fields["enabled"]; ok {
		status.Enabled = !strings.EqualFold(enabled, "false")
	}
	status.Transport = fields["transport"]
	status.Command = fields["command"]
	status.Args = fields["args"]
	if scope := fields["scope"]; scope != "" {
		status.Scope = simplifyMCPAgentScope(scope)
	}
	return status
}

func parseMCPAgentFields(output string) map[string]string {
	fields := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			fields[key] = value
		}
	}
	return fields
}

func simplifyMCPAgentScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch {
	case strings.Contains(scope, "local"):
		return "scope=local"
	case strings.Contains(scope, "user"):
		return "scope=user"
	case strings.Contains(scope, "project"):
		return "scope=project"
	default:
		return strings.TrimSpace(scope)
	}
}
