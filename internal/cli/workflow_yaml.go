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
