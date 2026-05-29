package cli

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

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
	steps := workflowSteps(workflow)
	rendered := make([]renderedStep, 0, len(steps))
	stepsContext := map[string]any{}
	var previousStub map[string]any
	for index, step := range steps {
		body, err := renderWorkflowStepPrompt(workflow.Name, step, index, inputs, previousStub, stepsContext)
		if err != nil {
			return workflowRenderResult{}, err
		}
		rendered = append(rendered, renderedStep{
			ID:      step.ID,
			Name:    step.Name,
			Prompt:  body,
			Outputs: step.Outputs,
		})
		stub := workflowStepStub(step)
		previousStub = stub
		if step.ID != "" {
			stepsContext[step.ID] = stub
		}
	}
	return workflowRenderResult{
		Name:   workflow.Name,
		Path:   path,
		Inputs: inputs,
		Steps:  rendered,
		Prompt: workflowPreviewPrompt(rendered),
	}, nil
}

// renderWorkflowStepPrompt renders the body of a single step using the current
// template context (inputs + previous + steps).
func renderWorkflowStepPrompt(workflowName string, step workflowStep, index int, inputs map[string]any, previous map[string]any, steps map[string]any) (string, error) {
	data := workflowTemplateContext(inputs, previous, steps)
	label := fmt.Sprintf("workflow %s step %s prompt", workflowName, workflowStepLabel(step, index))
	body, err := renderTemplate(label, step.Prompt, data)
	if err != nil {
		return "", err
	}
	return body, nil
}

func workflowStepStub(step workflowStep) map[string]any {
	id := step.ID
	if id == "" {
		id = "previous"
	}
	outputs := map[string]any{}
	for name := range step.Outputs {
		outputs[name] = fmt.Sprintf("<%s.outputs.%s>", id, name)
	}
	return map[string]any{
		"outputs":  outputs,
		"stdout":   fmt.Sprintf("<%s.stdout>", id),
		"duration": "0s",
	}
}

func workflowSteps(workflow workflowFile) []workflowStep {
	if len(workflow.Steps) > 0 {
		return workflow.Steps
	}
	if strings.TrimSpace(workflow.Prompt) == "" {
		return nil
	}
	return []workflowStep{{Prompt: workflow.Prompt}}
}

func workflowTemplateContext(inputs map[string]any, previous map[string]any, steps map[string]any) map[string]any {
	if inputs == nil {
		inputs = map[string]any{}
	}
	if previous == nil {
		previous = map[string]any{
			"outputs":  map[string]any{},
			"stdout":   "",
			"duration": "",
		}
	}
	if steps == nil {
		steps = map[string]any{}
	}
	return map[string]any{
		"inputs":   inputs,
		"previous": previous,
		"steps":    steps,
	}
}

func workflowPreviewPrompt(steps []renderedStep) string {
	if len(steps) == 0 {
		return ""
	}
	if len(steps) == 1 {
		return steps[0].Prompt
	}
	var b strings.Builder
	for i, step := range steps {
		title := workflowStepLabel(workflowStep{ID: step.ID, Name: step.Name}, i)
		fmt.Fprintf(&b, "── step %d: %s ──\n", i+1, title)
		b.WriteString(step.Prompt)
		if !strings.HasSuffix(step.Prompt, "\n") {
			b.WriteString("\n")
		}
		if i < len(steps)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func workflowInputValues(workflow workflowFile, values []string) (map[string]any, error) {
	provided, err := parseWorkflowInputs(values)
	if err != nil {
		return nil, err
	}
	inputs := map[string]any{}
	for name, input := range workflow.Inputs {
		raw, ok := provided[name]
		if !ok {
			if input.Default != nil {
				raw = *input.Default
			} else {
				return nil, fmt.Errorf("workflow %s is missing required input %s; pass --input %s=<value>", workflow.Name, name, name)
			}
		}
		delete(provided, name)
		value, err := coerceWorkflowInput(name, input, raw)
		if err != nil {
			return nil, err
		}
		inputs[name] = value
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

func renderTemplate(name, source string, data any) (string, error) {
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
