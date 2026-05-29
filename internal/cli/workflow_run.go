package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
)

type workflowRunOptions struct {
	inputs    []string
	agentName string
	noSetup   bool
}

type workflowExecution struct {
	workflow workflowFile
	steps    []workflowStep
	inputs   map[string]any
	agent    workflowAgentConfig
	frame    *workflowFrame
	cmd      *cobra.Command
	ctx      context.Context //nolint:containedctx // frame writer is short-lived
}

func runWorkflow(ctx context.Context, cmd *cobra.Command, opts *options, workflow workflowFile, runOpts workflowRunOptions, agent workflowAgentConfig) error {
	if err := validateWorkflow(workflow); err != nil {
		return err
	}
	inputs, err := workflowInputValues(workflow, runOpts.inputs)
	if err != nil {
		return err
	}
	steps := workflowSteps(workflow)
	if len(steps) == 0 {
		return fmt.Errorf("workflow %s has no steps", workflow.Name)
	}
	execution := &workflowExecution{
		workflow: workflow,
		steps:    steps,
		inputs:   inputs,
		agent:    agent,
		cmd:      cmd,
		ctx:      ctx,
		frame:    newWorkflowFrame(cmd, opts, workflow, steps),
	}
	return execution.run()
}

func (e *workflowExecution) run() error {
	e.frame.start()
	previous := map[string]any{
		"outputs":  map[string]any{},
		"stdout":   "",
		"duration": "",
	}
	stepsContext := map[string]any{}
	for index, step := range e.steps {
		body, err := renderWorkflowStepPrompt(e.workflow.Name, step, index, e.inputs, previous, stepsContext)
		if err != nil {
			e.frame.fail(index, err)
			e.frame.summary(index, err)
			return err
		}
		prompt := workflowStepPromptWithSchema(step, body)
		e.frame.beginStep(index)
		started := time.Now()
		stdout, runErr := e.runAgent(prompt)
		elapsed := time.Since(started)
		previousOutputs, outputs, parseErr := decodeStepOutputs(step, stdout)
		if runErr == nil {
			runErr = parseErr
		}
		if runErr != nil {
			e.frame.endStep(index, elapsed, runErr)
			e.frame.summary(index, runErr)
			return runErr
		}
		previous = map[string]any{
			"outputs":  previousOutputs,
			"stdout":   stdout,
			"duration": elapsed.Round(100 * time.Millisecond).String(),
		}
		if step.ID != "" {
			stepsContext[step.ID] = map[string]any{
				"outputs":  previousOutputs,
				"stdout":   stdout,
				"duration": elapsed.Round(100 * time.Millisecond).String(),
			}
		}
		e.frame.endStep(index, elapsed, nil)
		// the last step's stdout also goes to the workflow's stdout so consumers
		// can pipe the workflow result. Earlier steps stream through the frame
		// only.
		if index == len(e.steps)-1 {
			_ = outputs // already bound; raw stdout is what we surface
			fmt.Fprint(e.cmd.OutOrStdout(), stdout)
		}
	}
	e.frame.summary(len(e.steps)-1, nil)
	return nil
}

func (e *workflowExecution) runAgent(prompt string) (string, error) {
	command, args, err := workflowAgentCommand(e.agent, prompt)
	if err != nil {
		return "", err
	}
	// #nosec G204 -- agent command is explicit local config.
	process := exec.CommandContext(e.ctx, command, args...)
	process.Stdin = e.cmd.InOrStdin()
	var stdoutBuf bytes.Buffer
	writers := []io.Writer{&stdoutBuf}
	if e.frame.showStdio() {
		writers = append(writers, e.frame.stepWriter())
	}
	process.Stdout = io.MultiWriter(writers...)
	process.Stderr = e.frame.errWriter()
	if err := process.Run(); err != nil {
		return stdoutBuf.String(), fmt.Errorf("agent exited with error: %w", err)
	}
	return stdoutBuf.String(), nil
}

// workflowStepPromptWithSchema appends a structured-output suffix to the step
// prompt when the step declares outputs.
func workflowStepPromptWithSchema(step workflowStep, body string) string {
	if len(step.Outputs) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n---\n")
	b.WriteString("When this step is complete, output a single JSON object on the LAST line of your reply, with no surrounding prose, no Markdown fences, and no commentary. Include exactly these fields:\n\n")
	names := make([]string, 0, len(step.Outputs))
	for name := range step.Outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		o := step.Outputs[name]
		t := o.Type
		if t == "" {
			t = workflowFieldString
		}
		fmt.Fprintf(&b, "- %s (%s)", name, t)
		if o.Description != "" {
			fmt.Fprintf(&b, ": %s", o.Description)
		}
		if t == workflowFieldJSON && len(o.Schema) > 0 {
			schemaBytes, err := json.MarshalIndent(o.Schema, "  ", "  ")
			if err == nil {
				fmt.Fprintf(&b, "\n  schema:\n  %s", string(schemaBytes))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// decodeStepOutputs parses the agent's stdout, extracts declared outputs from
// the final JSON line, and returns both the typed outputs (for the template
// context) and the raw decoded map (for downstream use).
func decodeStepOutputs(step workflowStep, stdout string) (map[string]any, map[string]any, error) {
	if len(step.Outputs) == 0 {
		return map[string]any{}, map[string]any{}, nil
	}
	jsonLine, ok := lastJSONObjectLine(stdout)
	if !ok {
		return nil, nil, fmt.Errorf("step %q expected a JSON object on the final line of the agent reply; got %q", workflowStepLabel(step, 0), trimForError(stdout))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonLine), &decoded); err != nil {
		return nil, nil, fmt.Errorf("step %q: invalid JSON on final line: %w", workflowStepLabel(step, 0), err)
	}
	outputs := map[string]any{}
	for name, def := range step.Outputs {
		raw, present := decoded[name]
		if !present {
			return nil, nil, fmt.Errorf("step %q: missing declared output %q in agent reply", workflowStepLabel(step, 0), name)
		}
		coerced, err := coerceWorkflowOutput(name, def, raw)
		if err != nil {
			return nil, nil, err
		}
		outputs[name] = coerced
	}
	return outputs, decoded, nil
}

func coerceWorkflowOutput(name string, def workflowOutput, raw any) (any, error) {
	switch def.Type {
	case "", workflowFieldString:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("output %s: expected string, got %T", name, raw)
		}
		return s, nil
	case workflowFieldInt:
		switch v := raw.(type) {
		case float64:
			return int64(v), nil
		case int64:
			return v, nil
		default:
			return nil, fmt.Errorf("output %s: expected int, got %T", name, raw)
		}
	case workflowFieldBool:
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("output %s: expected bool, got %T", name, raw)
		}
		return b, nil
	case workflowFieldJSON:
		if len(def.Schema) > 0 {
			if err := validateWorkflowValueAgainstSchema(raw, def.Schema, "output "+name); err != nil {
				return nil, err
			}
		}
		return raw, nil
	case workflowFieldDuration:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("output %s: expected duration string, got %T", name, raw)
		}
		if _, err := parseWorkflowDuration(s); err != nil {
			return nil, fmt.Errorf("output %s: %w", name, err)
		}
		return s, nil
	}
	return nil, fmt.Errorf("output %s: unsupported type %q", name, def.Type)
}

// lastJSONObjectLine scans output from the bottom looking for a line that
// starts with `{` and parses as a JSON object. Trailing blank lines are
// ignored.
func lastJSONObjectLine(s string) (string, bool) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for _, line := range slices.Backward(lines) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
			return "", false
		}
		var probe any
		if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
			return "", false
		}
		if _, ok := probe.(map[string]any); !ok {
			return "", false
		}
		return trimmed, true
	}
	return "", false
}

func trimForError(s string) string {
	s = strings.TrimSpace(s)
	const limit = 200
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// ─── frame ───────────────────────────────────────────────────────────────────

type workflowFrame struct {
	stderr     io.Writer
	options    output.Options
	enabled    bool
	steps      []workflowStep
	workflow   workflowFile
	startedAt  time.Time
	stepIO     bool
	stepPrefix string
}

func newWorkflowFrame(cmd *cobra.Command, opts *options, workflow workflowFile, steps []workflowStep) *workflowFrame {
	stderr := cmd.ErrOrStderr()
	tty := isTerminal(stderr)
	options := output.Options{
		Color:          colorEnabled(opts.color, tty),
		DarkBackground: true,
		Width:          terminalWidth(stderr),
	}
	return &workflowFrame{
		stderr:     stderr,
		options:    options,
		enabled:    len(steps) > 1,
		steps:      steps,
		workflow:   workflow,
		stepIO:     true,
		stepPrefix: "  │ ",
	}
}

func (f *workflowFrame) showStdio() bool { return f.enabled && f.stepIO }

func (f *workflowFrame) start() {
	f.startedAt = time.Now()
	if !f.enabled {
		return
	}
	header := fmt.Sprintf("%s · %d steps", f.workflow.Name, len(f.steps))
	fmt.Fprintln(f.stderr, output.ToneText(f.options, output.ToneInfo, header))
}

func (f *workflowFrame) beginStep(index int) {
	if !f.enabled {
		return
	}
	title := workflowFrameTitle(f.steps[index], index)
	fmt.Fprintln(f.stderr)
	fmt.Fprintf(f.stderr, "%s %s\n", output.ToneText(f.options, output.ToneInfo, "▶"), title)
}

func (f *workflowFrame) endStep(index int, elapsed time.Duration, err error) {
	if !f.enabled {
		return
	}
	if err != nil {
		fmt.Fprintf(f.stderr, "  %s %s · %s\n",
			output.ToneText(f.options, output.ToneDanger, "✗"),
			output.ToneText(f.options, output.ToneDanger, "failed"),
			fmtDuration(elapsed),
		)
		fmt.Fprintf(f.stderr, "    %s\n", output.ToneText(f.options, output.ToneMuted, err.Error()))
		return
	}
	fmt.Fprintf(f.stderr, "  %s %s · %s\n",
		output.ToneText(f.options, output.ToneSuccess, "✓"),
		output.ToneText(f.options, output.ToneMuted, "ok"),
		fmtDuration(elapsed),
	)
	_ = index
}

func (f *workflowFrame) fail(index int, err error) {
	if !f.enabled || err == nil {
		return
	}
	fmt.Fprintf(f.stderr, "  %s %s\n",
		output.ToneText(f.options, output.ToneDanger, "✗"),
		output.ToneText(f.options, output.ToneDanger, err.Error()),
	)
	_ = index
}

func (f *workflowFrame) summary(lastIndex int, err error) {
	if !f.enabled {
		return
	}
	elapsed := time.Since(f.startedAt)
	separator := strings.Repeat("─", min(terminalWidth(f.stderr), 72))
	fmt.Fprintln(f.stderr)
	fmt.Fprintln(f.stderr, output.ToneText(f.options, output.ToneMuted, separator))
	total := len(f.steps)
	if err == nil {
		fmt.Fprintf(f.stderr, "done in %s · %d/%d ok\n", fmtDuration(elapsed), total, total)
		return
	}
	fmt.Fprintf(f.stderr, "%s in %s · %d/%d ok · 1 failed\n",
		output.ToneText(f.options, output.ToneDanger, "aborted"),
		fmtDuration(elapsed),
		lastIndex,
		total,
	)
}

func (f *workflowFrame) stepWriter() io.Writer {
	if !f.enabled {
		return io.Discard
	}
	return &linePrefixWriter{w: f.stderr, prefix: f.stepPrefix, options: f.options}
}

func (f *workflowFrame) errWriter() io.Writer {
	if !f.enabled {
		return f.stderr
	}
	return &linePrefixWriter{w: f.stderr, prefix: f.stepPrefix, options: f.options, danger: true}
}

func workflowFrameTitle(step workflowStep, index int) string {
	if step.Name != "" {
		return step.Name
	}
	if step.ID != "" {
		return step.ID
	}
	return fmt.Sprintf("Step %d", index+1)
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(10 * time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// linePrefixWriter rewrites the stream so every line lands prefixed with the
// configured indent, optionally tinted in a danger tone.
type linePrefixWriter struct {
	w       io.Writer
	prefix  string
	options output.Options
	danger  bool
	buf     bytes.Buffer
}

func (lpw *linePrefixWriter) Write(p []byte) (int, error) {
	n := len(p)
	lpw.buf.Write(p)
	for {
		idx := bytes.IndexByte(lpw.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := string(lpw.buf.Next(idx + 1))
		if err := lpw.writeLine(line); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (lpw *linePrefixWriter) writeLine(line string) error {
	body := strings.TrimRight(line, "\n")
	tone := output.ToneMuted
	if lpw.danger {
		tone = output.ToneDanger
	}
	_, err := fmt.Fprintln(lpw.w, lpw.prefix+output.ToneText(lpw.options, tone, body))
	return err
}
