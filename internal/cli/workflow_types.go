package cli

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
