package policy

type CommandSpec struct {
	ID       string
	Path     []string
	Provider string
	Resource string
	Action   string
	Effect   string
	Risk     []string
	Scopes   []string
}

type Invocation struct {
	Spec       CommandSpec
	Profile    string
	Account    string
	Args       map[string]any
	OutputMode string
	WorkingDir string
}

type Decision struct {
	Allowed bool
	Reason  string
	Rule    string
}

type Policy struct {
	Version  int             `yaml:"version"`
	Default  string          `yaml:"default"`
	Roles    map[string]Role `yaml:"roles"`
	Bindings []Binding       `yaml:"bindings"`
}

type Role struct {
	Extends []string `yaml:"extends"`
	Allow   []Rule   `yaml:"allow"`
	Deny    []Rule   `yaml:"deny"`
}

type Rule struct {
	ID        string   `yaml:"id"`
	Command   string   `yaml:"command"`
	Provider  string   `yaml:"provider"`
	Resources []string `yaml:"resources"`
	Actions   []string `yaml:"actions"`
	Effects   []string `yaml:"effects"`
	Risks     []string `yaml:"risks"`
	Profiles  []string `yaml:"profiles"`
	Accounts  []string `yaml:"accounts"`
}

type Binding struct {
	Role     string   `yaml:"role"`
	Profiles []string `yaml:"profiles"`
	Accounts []string `yaml:"accounts"`
}
