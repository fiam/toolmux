package cli

import (
	"fmt"
	"strings"
)

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
