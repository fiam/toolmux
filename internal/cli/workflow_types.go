package cli

const (
	workflowProjectRelDir  = ".toolmux/workflows"
	workflowTemplateRepo   = "fiam/toolmux"
	workflowTemplateRef    = "main"
	workflowTemplateFolder = "workflows"
)

type workflowFieldType string

const (
	workflowFieldString   workflowFieldType = "string"
	workflowFieldInt      workflowFieldType = "int"
	workflowFieldBool     workflowFieldType = "bool"
	workflowFieldJSON     workflowFieldType = "json"
	workflowFieldDuration workflowFieldType = "duration"
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
	Inputs      map[string]workflowInput    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Prompt      string                      `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Steps       []workflowStep              `json:"steps,omitempty" yaml:"steps,omitempty"`
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
	Type        workflowFieldType `json:"type,omitempty" yaml:"type,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool              `json:"required,omitempty" yaml:"required,omitempty"`
	Default     *string           `json:"default,omitempty" yaml:"default,omitempty"`
	Schema      map[string]any    `json:"schema,omitempty" yaml:"schema,omitempty"`
}

type workflowStep struct {
	ID      string                    `json:"id,omitempty" yaml:"id,omitempty"`
	Name    string                    `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt  string                    `json:"prompt" yaml:"prompt"`
	Outputs map[string]workflowOutput `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}

type workflowOutput struct {
	Type        workflowFieldType `json:"type,omitempty" yaml:"type,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Schema      map[string]any    `json:"schema,omitempty" yaml:"schema,omitempty"`
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
	Name   string         `json:"name" yaml:"name"`
	Path   string         `json:"path" yaml:"path"`
	Inputs map[string]any `json:"inputs" yaml:"inputs"`
	Steps  []renderedStep `json:"steps" yaml:"steps"`
	Prompt string         `json:"prompt" yaml:"prompt"`
}

type renderedStep struct {
	ID      string                    `json:"id,omitempty" yaml:"id,omitempty"`
	Name    string                    `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt  string                    `json:"prompt" yaml:"prompt"`
	Outputs map[string]workflowOutput `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}
