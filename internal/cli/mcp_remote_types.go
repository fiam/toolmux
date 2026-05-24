package cli

import (
	_ "embed"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	mcpRemoteTransportStreamableHTTP = "streamable-http"
	mcpRemoteTransportStdio          = "stdio"
	mcpRemoteCacheVersion            = 1
	mcpRemoteCacheMaxAge             = 24 * time.Hour
	mcpRemoteToolsListMaxPages       = 128
	mcpRemoteSSEIdleTimeout          = 60 * time.Second
	mcpRemoteStdioCloseTimeout       = 2 * time.Second
	mcpRemoteTraceBodyLimit          = 8 << 20
	mcpRemoteCompactDescriptionLimit = 120
	mcpRemoteServerAnnotation        = "toolmux.remote_mcp.server"
	mcpRemoteCredentialProvider      = "mcp_remote"
)

var (
	mcpRemoteNamePattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	mcpRemoteMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
)

//go:embed mcp_remote_catalog.yaml
var mcpBuiltinRemoteCatalogYAML []byte

var (
	mcpBuiltinRemoteCatalogOnce  sync.Once
	mcpBuiltinRemoteCatalogValue map[string]mcpRemoteCatalogDefinition
	mcpBuiltinRemoteCatalogErr   error
)

type mcpRemoteServer struct {
	URL              string         `json:"url" yaml:"url"`
	Command          string         `json:"command,omitempty" yaml:"command,omitempty"`
	Args             []string       `json:"args,omitempty" yaml:"args,omitempty"`
	Transport        string         `json:"transport,omitempty" yaml:"transport,omitempty"`
	AuthRequired     *bool          `json:"auth_required,omitempty" yaml:"auth_required,omitempty"`
	DefaultArguments map[string]any `json:"default_arguments,omitempty" yaml:"default_arguments,omitempty"`
}

type mcpRemoteServerEntry struct {
	Name   string          `json:"name" yaml:"name"`
	Scope  string          `json:"scope" yaml:"scope"`
	Scopes []string        `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path   string          `json:"path" yaml:"path"`
	Server mcpRemoteServer `json:"server" yaml:"server"`
}

type mcpRemoteCache struct {
	Version         int                        `json:"version"`
	Name            string                     `json:"name"`
	URL             string                     `json:"url"`
	Transport       string                     `json:"transport"`
	ProtocolVersion string                     `json:"protocol_version,omitempty"`
	ServerInfo      map[string]any             `json:"server_info,omitempty"`
	Tools           []mcpRemoteTool            `json:"tools"`
	SyncedAt        time.Time                  `json:"synced_at"`
	Fingerprint     string                     `json:"fingerprint,omitempty"`
	Raw             map[string]json.RawMessage `json:"raw,omitempty"`
}

type mcpRemoteTool struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description,omitempty"`
	InputSchema  map[string]any   `json:"inputSchema,omitempty"`
	OutputSchema map[string]any   `json:"outputSchema,omitempty"`
	Annotations  map[string]any   `json:"annotations,omitempty"`
	Icons        []map[string]any `json:"icons,omitempty"`
	Execution    map[string]any   `json:"execution,omitempty"`
	Meta         map[string]any   `json:"_meta,omitempty"`
}

type mcpRemoteToolRef struct {
	Entry mcpRemoteServerEntry
	Cache mcpRemoteCache
	Tool  mcpRemoteTool
}

type mcpRemoteListItem struct {
	Name      string   `json:"name" yaml:"name"`
	Status    string   `json:"status" yaml:"status"`
	Scope     string   `json:"scope" yaml:"scope"`
	Scopes    []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path      string   `json:"path" yaml:"path"`
	URL       string   `json:"url" yaml:"url"`
	Command   string   `json:"command,omitempty" yaml:"command,omitempty"`
	Transport string   `json:"transport" yaml:"transport"`
	Tools     *int     `json:"tools,omitempty" yaml:"tools,omitempty"`
}

type mcpRemoteToolList struct {
	Server string          `json:"server" yaml:"server"`
	Tools  []mcpRemoteTool `json:"tools" yaml:"tools"`
}

type mcpRemoteTreeItem struct {
	Name      string          `json:"name" yaml:"name"`
	Status    string          `json:"status" yaml:"status"`
	Scope     string          `json:"scope" yaml:"scope"`
	Scopes    []string        `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path      string          `json:"path" yaml:"path"`
	URL       string          `json:"url" yaml:"url"`
	Command   string          `json:"command,omitempty" yaml:"command,omitempty"`
	Transport string          `json:"transport" yaml:"transport"`
	Tools     []mcpRemoteTool `json:"tools,omitempty" yaml:"tools,omitempty"`
}

type mcpRemoteNameConflict struct {
	Name string
}

type mcpRemoteHTTPTrace struct {
	w io.Writer
}

type mcpRemoteHTTPStatusError struct {
	Method     string
	StatusCode int
	Body       string
}

type mcpRemoteSSEStreamItem struct {
	Message []byte
	Err     error
}

type mcpRemoteMissingRequiredArgumentsError struct {
	Names []string
}

func (err mcpRemoteMissingRequiredArgumentsError) Error() string {
	return "missing required MCP tool arguments: " + strings.Join(err.Names, ", ")
}

type mcpRemoteCatalogEntry struct {
	Name                    string                         `json:"name" yaml:"name"`
	DisplayName             string                         `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Status                  string                         `json:"status" yaml:"status"`
	Registered              bool                           `json:"registered" yaml:"registered"`
	RegisteredNames         []string                       `json:"registered_names,omitempty" yaml:"registered_names,omitempty"`
	Scope                   string                         `json:"scope,omitempty" yaml:"scope,omitempty"`
	Scopes                  []string                       `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path                    string                         `json:"path,omitempty" yaml:"path,omitempty"`
	Tools                   *int                           `json:"tools,omitempty" yaml:"tools,omitempty"`
	URL                     string                         `json:"url" yaml:"url"`
	Transport               string                         `json:"transport" yaml:"transport"`
	DefaultArgumentHints    []mcpRemoteDefaultArgumentHint `json:"default_argument_hints,omitempty" yaml:"default_argument_hints,omitempty"`
	MissingDefaultArguments []string                       `json:"missing_default_arguments,omitempty" yaml:"missing_default_arguments,omitempty"`
	Reason                  string                         `json:"reason,omitempty" yaml:"reason,omitempty"`
}

type toolboxCatalogEntry struct {
	Name                    string                         `json:"name" yaml:"name"`
	DisplayName             string                         `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Type                    string                         `json:"type" yaml:"type"`
	Status                  string                         `json:"status" yaml:"status"`
	Registered              bool                           `json:"registered" yaml:"registered"`
	RegisteredNames         []string                       `json:"registered_names,omitempty" yaml:"registered_names,omitempty"`
	Scope                   string                         `json:"scope,omitempty" yaml:"scope,omitempty"`
	Scopes                  []string                       `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Path                    string                         `json:"path,omitempty" yaml:"path,omitempty"`
	Tools                   *int                           `json:"tools,omitempty" yaml:"tools,omitempty"`
	URL                     string                         `json:"url,omitempty" yaml:"url,omitempty"`
	Transport               string                         `json:"transport,omitempty" yaml:"transport,omitempty"`
	DefaultArgumentHints    []mcpRemoteDefaultArgumentHint `json:"default_argument_hints,omitempty" yaml:"default_argument_hints,omitempty"`
	MissingDefaultArguments []string                       `json:"missing_default_arguments,omitempty" yaml:"missing_default_arguments,omitempty"`
	Reason                  string                         `json:"reason,omitempty" yaml:"reason,omitempty"`
}

type toolboxCatalogFilters struct {
	MCP      bool
	Internal bool
}

type mcpRemoteCatalogEnable struct {
	CatalogName    string
	RegisteredName string
}

type mcpRemoteCatalogDefinition struct {
	Server               mcpRemoteServer                `json:"server" yaml:"server"`
	DisplayName          string                         `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	DefaultArgumentHints []mcpRemoteDefaultArgumentHint `json:"default_argument_hints,omitempty" yaml:"default_argument_hints,omitempty"`
}

type mcpRemoteDefaultArgumentHint struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Example     string `json:"example,omitempty" yaml:"example,omitempty"`
}

type mcpRemoteDefaultArgumentItem struct {
	Name  string `json:"name" yaml:"name"`
	Value any    `json:"value" yaml:"value"`
}

type mcpRemoteDefaultArgumentsResult struct {
	Server    string                         `json:"server" yaml:"server"`
	Scope     string                         `json:"scope" yaml:"scope"`
	Path      string                         `json:"path" yaml:"path"`
	Arguments []mcpRemoteDefaultArgumentItem `json:"arguments" yaml:"arguments"`
}
