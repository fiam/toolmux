package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func inspectGeminiMCPAgent(serverName string) mcpAgentStatus {
	status := mcpAgentStatus{
		Enabled: !geminiMCPServerDisabled(serverName),
	}
	records := geminiMCPServerRecords(serverName)
	if len(records) == 0 {
		return status
	}
	status.Configured = true
	for _, record := range records {
		status.Scopes = append(status.Scopes, record.Scope)
	}
	last := records[len(records)-1]
	status.Command = last.Command
	status.Args = strings.Join(last.Args, " ")
	status.Path = last.Path
	return status
}

type geminiMCPServerRecord struct {
	Scope   string
	Path    string
	Command string
	Args    []string
}

type geminiMCPServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func geminiMCPServerRecords(serverName string) []geminiMCPServerRecord {
	var records []geminiMCPServerRecord
	for _, candidate := range geminiMCPConfigPaths() {
		server, ok := geminiMCPServerConfigAtPath(candidate.Path, serverName)
		if !ok {
			continue
		}
		records = append(records, geminiMCPServerRecord{
			Scope:   candidate.Scope,
			Path:    candidate.Path,
			Command: server.Command,
			Args:    server.Args,
		})
	}
	return records
}

type geminiMCPConfigPath struct {
	Scope string
	Path  string
}

func geminiMCPConfigPaths() []geminiMCPConfigPath {
	var paths []geminiMCPConfigPath
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, geminiMCPConfigPath{Scope: "user", Path: filepath.Join(home, ".gemini", "settings.json")})
	}
	paths = append(paths, geminiMCPConfigPath{Scope: "project", Path: filepath.Join(".gemini", "settings.json")})
	return paths
}

func mcpConfigHasServer(path, serverName string) bool {
	_, ok := geminiMCPServerConfigAtPath(path, serverName)
	return ok
}

func geminiMCPServerConfigAtPath(path, serverName string) (geminiMCPServerConfig, bool) {
	// #nosec G304 -- paths are local agent configuration locations.
	data, err := os.ReadFile(path)
	if err != nil {
		return geminiMCPServerConfig{}, false
	}
	var config struct {
		MCPServers map[string]geminiMCPServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return geminiMCPServerConfig{}, false
	}
	server, ok := config.MCPServers[serverName]
	return server, ok
}

func geminiMCPServerDisabled(serverName string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	path := filepath.Join(home, ".gemini", "mcp-server-enablement.json")
	// #nosec G304 -- path is Gemini CLI's documented local enablement state.
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var states map[string]struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &states); err != nil {
		return false
	}
	state, ok := states[strings.ToLower(strings.TrimSpace(serverName))]
	return ok && !state.Enabled
}
