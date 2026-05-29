package cli

const (
	mcpProtocolVersion             = "2025-06-18"
	mcpRemoteClientProtocolVersion = "2025-11-25"
)
const toolmuxConfigRelPath = ".toolmux/config.yaml"
const mcpDefaultProfileName = "default"

type mcpToolSelection struct {
	Profile          string
	Tools            []string
	ToolRegex        []string
	ExcludeTools     []string
	ExcludeToolRegex []string
}

type mcpConfigureOptions struct {
	mcpToolSelection
	Command     string
	ServerName  string
	AgentScope  string
	ClaudeScope string
	DryRun      bool
}

type mcpProfileScopeOptions struct {
	Global  bool
	Project bool
}
