package cli

import (
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var reservedWorkflowIdentifiers = map[string]bool{
	"inputs":   true,
	"previous": true,
	"steps":    true,
	"workflow": true,
	"env":      true,
}

func validateWorkflow(workflow workflowFile) error {
	if workflow.Version != 0 && workflow.Version != 1 {
		return fmt.Errorf("unsupported workflow version %d", workflow.Version)
	}
	if _, err := cleanWorkflowName(workflow.Name); err != nil {
		return err
	}
	hasPrompt := strings.TrimSpace(workflow.Prompt) != ""
	hasSteps := len(workflow.Steps) > 0
	if hasPrompt && hasSteps {
		return fmt.Errorf("workflow %s: use either prompt or steps, not both", workflow.Name)
	}
	if !hasPrompt && !hasSteps {
		return fmt.Errorf("workflow %s: prompt or steps is required", workflow.Name)
	}
	seenIDs := map[string]bool{}
	for name, input := range workflow.Inputs {
		if err := validateWorkflowIdentifier(name, "input"); err != nil {
			return err
		}
		if err := validateWorkflowFieldType(input.Type, "input "+name); err != nil {
			return err
		}
		if err := validateWorkflowJSONSchema(input.Type, input.Schema, "input "+name); err != nil {
			return err
		}
	}
	for index, step := range workflow.Steps {
		if strings.TrimSpace(step.Prompt) == "" {
			return fmt.Errorf("workflow %s: step %d has empty prompt", workflow.Name, index+1)
		}
		if step.ID != "" {
			if err := validateWorkflowIdentifier(step.ID, "step id"); err != nil {
				return err
			}
			if seenIDs[step.ID] {
				return fmt.Errorf("workflow %s: duplicate step id %q", workflow.Name, step.ID)
			}
			seenIDs[step.ID] = true
		}
		for name, output := range step.Outputs {
			label := "step " + workflowStepLabel(step, index) + " output " + name
			if err := validateWorkflowIdentifier(name, "output"); err != nil {
				return err
			}
			if err := validateWorkflowFieldType(output.Type, label); err != nil {
				return err
			}
			if err := validateWorkflowJSONSchema(output.Type, output.Schema, label); err != nil {
				return err
			}
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

func validateWorkflowIdentifier(name, kind string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_'
		if i > 0 {
			ok = ok || r >= '0' && r <= '9'
		}
		if !ok {
			return fmt.Errorf("invalid %s name %q: use Go template identifier characters", kind, name)
		}
	}
	if reservedWorkflowIdentifiers[name] {
		return fmt.Errorf("%s name %q is reserved", kind, name)
	}
	return nil
}

func validateWorkflowFieldType(t workflowFieldType, label string) error {
	switch t {
	case "", workflowFieldString, workflowFieldInt, workflowFieldBool, workflowFieldJSON, workflowFieldDuration:
		return nil
	default:
		return fmt.Errorf("%s has unsupported type %q", label, t)
	}
}

func validateWorkflowJSONSchema(t workflowFieldType, schema map[string]any, label string) error {
	if len(schema) == 0 {
		return nil
	}
	if t != workflowFieldJSON {
		return fmt.Errorf("%s: schema is only valid when type is json", label)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(label, schema); err != nil {
		return fmt.Errorf("%s: invalid json schema: %w", label, err)
	}
	if _, err := compiler.Compile(label); err != nil {
		return fmt.Errorf("%s: invalid json schema: %w", label, err)
	}
	return nil
}

func workflowStepLabel(step workflowStep, index int) string {
	if step.ID != "" {
		return step.ID
	}
	if step.Name != "" {
		return step.Name
	}
	return fmt.Sprintf("#%d", index+1)
}
