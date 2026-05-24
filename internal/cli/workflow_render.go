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
