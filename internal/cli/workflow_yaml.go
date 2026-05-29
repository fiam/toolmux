package cli

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

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

func (wf *workflowFile) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow file must be a YAML mapping")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "agent" {
			return fmt.Errorf("workflow field `agent:` is no longer supported; set the workflow agent in toolmux config with `toolmux workflow config set default-agent <name>` or define a custom agent under `workflows.agents`")
		}
	}
	type workflowFileAlias workflowFile
	var alias workflowFileAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*wf = workflowFile(alias)
	return nil
}
