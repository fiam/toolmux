package cli

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/policy"
)

func writeMCPRemoteToolResult(cmd *cobra.Command, opts *options, result mcpCallToolResult) error {
	if opts.output == "table" && len(result.Content) == 1 && result.Content[0].Type == "text" {
		fmt.Fprintln(cmd.OutOrStdout(), result.Content[0].Text)
		return nil
	}
	return writeValue(cmd, opts, result, nil)
}

func mcpRemoteActionSpecForEntry(entry mcpRemoteServerEntry, tool mcpRemoteTool) actions.Spec {
	serverName := entry.Name
	id := serverName + "." + tool.Name
	localEffect := actions.EffectNone
	risks := []string{"remote-mcp", "remote-write"}
	if entry.Server.Transport == mcpRemoteTransportStdio {
		localEffect = actions.EffectWrite
		risks = []string{"stdio-mcp", "local-command", "remote-write", "local-write"}
	}
	spec := actions.Command(actions.LocalName(id), tool.Name,
		actions.Use(tool.Name),
		actions.Short(firstNonEmpty(tool.Description, "Call remote MCP tool "+tool.Name)),
		actions.RBAC(actions.ResourceName("mcp_remote"), actions.Verb("call"), actions.EffectWrite, localEffect),
		actions.Risks(risks...),
		actions.Scopes("mcp:"+serverName),
	)
	spec.Provider = serverName
	spec.Path = []string{serverName, tool.Name}
	return spec
}

func cachedMCPRemoteCommandSpecs(opts *options) []policy.CommandSpec {
	if opts == nil {
		return nil
	}
	refs := mcpRemoteToolRefs(opts.mcpCacheDir)
	specs := make([]policy.CommandSpec, 0, len(refs))
	for _, ref := range refs {
		specs = append(specs, mcpRemoteActionSpecForEntry(ref.Entry, ref.Tool))
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func mcpRemoteSpecForCommandParts(opts *options, parts []string) (policy.CommandSpec, bool) {
	if opts == nil || len(parts) < 2 {
		return policy.CommandSpec{}, false
	}
	for _, ref := range mcpRemoteToolRefs(opts.mcpCacheDir) {
		if ref.Entry.Name == parts[0] && ref.Tool.Name == parts[1] {
			return mcpRemoteActionSpecForEntry(ref.Entry, ref.Tool), true
		}
	}
	return policy.CommandSpec{}, false
}

func (server mcpServer) remoteMCPTools(ctx context.Context) []any {
	if server.opts == nil {
		return nil
	}
	refs := server.remoteMCPToolRefs(ctx)
	tools := make([]any, 0, len(refs))
	for _, ref := range refs {
		spec := mcpRemoteActionSpecForEntry(ref.Entry, ref.Tool)
		if !server.selector.matches(spec) {
			continue
		}
		tools = append(tools, mcpRemoteToolForServeWithDefaults(spec.ID, ref.Tool, ref.Entry.Server.DefaultArguments))
	}
	return tools
}

func mcpRemoteToolForServe(name string, tool mcpRemoteTool) map[string]any {
	return mcpRemoteToolForServeWithDefaults(name, tool, nil)
}

func mcpRemoteToolForServeWithDefaults(name string, tool mcpRemoteTool, defaultArguments map[string]any) map[string]any {
	out := map[string]any{
		"name":        name,
		"description": tool.Description,
		"inputSchema": mcpRemoteInputSchemaWithDefaults(tool.InputSchema, defaultArguments),
	}
	if tool.Title != "" {
		out["title"] = tool.Title
	}
	if tool.OutputSchema != nil {
		out["outputSchema"] = tool.OutputSchema
	}
	if tool.Annotations != nil {
		out["annotations"] = tool.Annotations
	}
	if len(tool.Icons) > 0 {
		out["icons"] = tool.Icons
	}
	if tool.Execution != nil {
		out["execution"] = tool.Execution
	}
	if tool.Meta != nil {
		out["_meta"] = tool.Meta
	}
	return out
}

func (server mcpServer) remoteMCPToolRefs(ctx context.Context) []mcpRemoteToolRef {
	if server.opts == nil {
		return nil
	}
	return mcpRemoteToolRefsWithRefresh(ctx, server.cmd, server.opts)
}

func mcpRemoteToolRefs(cacheDir string) []mcpRemoteToolRef {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil
	}
	var refs []mcpRemoteToolRef
	for _, entry := range entries {
		cache, ok, err := readMCPRemoteCacheIfExists(cacheDir, entry.Name)
		if err != nil || !ok {
			continue
		}
		for _, tool := range cache.Tools {
			refs = append(refs, mcpRemoteToolRef{Entry: entry, Cache: cache, Tool: tool})
		}
	}
	return refs
}

func mcpRemoteToolRefsWithRefresh(ctx context.Context, cmd *cobra.Command, opts *options) []mcpRemoteToolRef {
	entries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return nil
	}
	var refs []mcpRemoteToolRef
	for _, entry := range entries {
		cache, ok := refreshMCPRemoteCacheIfStale(ctx, cmd, opts, entry, nil)
		if !ok {
			continue
		}
		for _, tool := range cache.Tools {
			refs = append(refs, mcpRemoteToolRef{Entry: entry, Cache: cache, Tool: tool})
		}
	}
	return refs
}

func (server mcpServer) lookupRemoteMCPTool(ctx context.Context, name string) (mcpRemoteToolRef, bool) {
	for _, ref := range server.remoteMCPToolRefs(ctx) {
		spec := mcpRemoteActionSpecForEntry(ref.Entry, ref.Tool)
		if spec.ID == name && server.selector.matches(spec) {
			return ref, true
		}
	}
	return mcpRemoteToolRef{}, false
}

func remoteMCPToolArguments(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tool arguments must be an object")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return arguments, nil
}

func mcpRemoteConflictsError(conflicts []mcpRemoteNameConflict) error {
	if len(conflicts) == 0 {
		return nil
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Name < conflicts[j].Name
	})
	conflict := conflicts[0]
	return fmt.Errorf("imported MCP server %q conflicts with a native Toolmux command; rename it with: toolmux mcp rename %s <new-name>", conflict.Name, conflict.Name)
}

func mcpRemoteCommandAllowsConflicts(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	path := commandPathNames(cmd)
	if len(path) >= 2 && path[0] == "toolmux" && (path[1] == "add" || path[1] == "remove" || path[1] == "rm") {
		return true
	}
	if len(path) < 3 || path[0] != "toolmux" || path[1] != "mcp" {
		return false
	}
	switch path[2] {
	case "sync", "rename", "ls", "list", "show", "catalog", "available", "auth":
		return true
	default:
		return false
	}
}

func commandPathNames(cmd *cobra.Command) []string {
	var reversed []string
	for current := cmd; current != nil; current = current.Parent() {
		reversed = append(reversed, current.Name())
	}
	names := make([]string, len(reversed))
	for i := range reversed {
		names[i] = reversed[len(reversed)-1-i]
	}
	return names
}

func mcpRemoteBearerTokenFromFlags(cmd *cobra.Command, literal, envName string, stdin bool) (string, error) {
	sources := 0
	if strings.TrimSpace(literal) != "" {
		sources++
	}
	if strings.TrimSpace(envName) != "" {
		sources++
	}
	if stdin {
		sources++
	}
	if sources != 1 {
		return "", fmt.Errorf("provide exactly one of --bearer-token, --bearer-token-env, or --bearer-token-stdin")
	}
	token := literal
	if strings.TrimSpace(envName) != "" {
		token = os.Getenv(strings.TrimSpace(envName))
	}
	if stdin {
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
		if err != nil {
			return "", err
		}
		token = string(data)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("bearer token is required")
	}
	return token, nil
}

func mcpRemoteCredentialRef(opts *options, name string) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   opts.profile,
		Provider:  mcpRemoteCredentialProvider,
		Service:   name,
		AccountID: "default",
	}
}

func mcpRemoteBearerTokens(token string, entry mcpRemoteServerEntry) credentials.OAuthTokens {
	return credentials.OAuthTokens{
		AccessToken: token,
		TokenType:   "bearer",
		Extra: map[string]string{
			"auth_type":  mcpRemoteAuthTypeBearer,
			"mcp_server": entry.Name,
			"url":        entry.Server.URL,
		},
	}
}

func toolboxAddSpec() actions.Spec {
	return actions.Command("toolbox.add", "add",
		actions.Use("add <toolbox-or-url>"),
		actions.Short("Add a toolbox"),
		actions.RBAC("mcp_server", actions.VerbCreate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func toolboxStatusSpec() actions.Spec {
	return actions.Command("toolbox.status", "status",
		actions.Use("status [toolbox...]"),
		actions.Short("Show toolbox status"),
		actions.RBAC("toolbox", actions.VerbStatus, actions.EffectNone, actions.EffectRead),
	)
}

func toolboxCatalogListSpec() actions.Spec {
	return actions.Command("toolbox.catalog", "catalog",
		actions.Use("catalog [--mcp|--internal]"),
		actions.Short("List available toolboxes"),
		actions.RBAC("toolbox_catalog", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func toolboxCatalogManageSpec() actions.Spec {
	return actions.Command("toolbox.catalog.manage", "catalog",
		actions.Use("catalog --manage|--enable <name>|--disable <name>"),
		actions.Short("Enable or disable known MCP servers"),
		actions.RBAC("mcp_server", actions.VerbUpdate, actions.EffectRead, actions.EffectWrite),
		actions.Risks("mcp-config", "remote-mcp"),
	)
}

func doctorSpec() actions.Spec {
	return actions.Command("doctor", "doctor",
		actions.Use("doctor"),
		actions.Short("Check Toolmux setup"),
		actions.RBAC("toolbox", actions.VerbDiagnose, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteSyncSpec() actions.Spec {
	return actions.Command("mcp.sync", "sync",
		actions.Use("mcp sync <name>"),
		actions.Short("Introspect and cache a remote MCP server"),
		actions.RBAC("mcp_server_cache", actions.VerbUpdate, actions.EffectRead, actions.EffectWrite),
		actions.Risks("remote-mcp", "mcp-cache"),
	)
}

func mcpRemoteRenameSpec() actions.Spec {
	return actions.Command("mcp.rename", "rename",
		actions.Use("mcp rename <old-name> <new-name>"),
		actions.Short("Rename a registered remote MCP server"),
		actions.RBAC("mcp_server", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func toolboxRemoveSpec() actions.Spec {
	return actions.Command("toolbox.remove", "remove",
		actions.Use("remove <toolbox> [toolbox...]"),
		actions.Short("Remove a toolbox"),
		actions.RBAC("mcp_server", actions.VerbDelete, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config", "mcp-auth"),
	)
}

func mcpRemoteListSpec() actions.Spec {
	return actions.Command("mcp.ls", "ls",
		actions.Use("mcp ls [name]"),
		actions.Short("List registered remote MCP servers"),
		actions.RBAC("mcp_server", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteShowSpec() actions.Spec {
	return actions.Command("mcp.show", "show",
		actions.Use("mcp show <name>"),
		actions.Short("Show a registered remote MCP server"),
		actions.RBAC("mcp_server", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteCatalogListSpec() actions.Spec {
	return actions.Command("mcp.catalog", "catalog",
		actions.Use("mcp catalog"),
		actions.Short("List known remote MCP servers"),
		actions.RBAC("mcp_server_catalog", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteCatalogManageSpec() actions.Spec {
	return actions.Command("mcp.catalog.manage", "catalog",
		actions.Use("mcp catalog --manage|--enable <name>|--disable <name>"),
		actions.Short("Enable or disable known remote MCP servers"),
		actions.RBAC("mcp_server", actions.VerbUpdate, actions.EffectRead, actions.EffectWrite),
		actions.Risks("mcp-config", "remote-mcp"),
	)
}

func mcpRemoteDefaultsListSpec() actions.Spec {
	return actions.Command("mcp.defaults.ls", "ls",
		actions.Use("mcp defaults ls <name>"),
		actions.Short("List default arguments for a remote MCP server"),
		actions.RBAC("mcp_server_defaults", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func mcpRemoteDefaultsSetSpec() actions.Spec {
	return actions.Command("mcp.defaults.set", "set",
		actions.Use("mcp defaults set <name> <argument> <value>"),
		actions.Short("Set a default argument for a remote MCP server"),
		actions.RBAC("mcp_server_defaults", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func mcpRemoteDefaultsRemoveSpec() actions.Spec {
	return actions.Command("mcp.defaults.remove", "remove",
		actions.Use("mcp defaults remove <name> <argument> [argument...]"),
		actions.Short("Remove default arguments for a remote MCP server"),
		actions.RBAC("mcp_server_defaults", actions.VerbDelete, actions.EffectNone, actions.EffectWrite),
		actions.Risks("mcp-config"),
	)
}

func mcpRemoteAuthLoginSpec() actions.Spec {
	return actions.Command("mcp.auth.login", "login",
		actions.Use("mcp auth login <name>"),
		actions.Short("Authorize a remote MCP server with OAuth"),
		actions.RBAC("mcp_server_auth", actions.VerbConnect, actions.EffectWrite, actions.EffectWrite),
		actions.Risks("remote-mcp", "secret-storage"),
	)
}

func mcpRemoteAuthSetSpec() actions.Spec {
	return actions.Command("mcp.auth.set", "set",
		actions.Use("mcp auth set <name>"),
		actions.Short("Store bearer token auth for a remote MCP server"),
		actions.RBAC("mcp_server_auth", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
		actions.Risks("secret-storage"),
	)
}

func mcpRemoteAuthRemoveSpec() actions.Spec {
	return actions.Command("mcp.auth.remove", "remove",
		actions.Use("mcp auth remove <name>"),
		actions.Short("Remove stored auth for a remote MCP server"),
		actions.RBAC("mcp_server_auth", actions.VerbDelete, actions.EffectNone, actions.EffectWrite),
		actions.Risks("secret-storage"),
	)
}

func mcpRemoteAuthStatusSpec() actions.Spec {
	return actions.Command("mcp.auth.status", "status",
		actions.Use("mcp auth status <name>"),
		actions.Short("Show remote MCP stored auth status"),
		actions.RBAC("mcp_server_auth", actions.VerbStatus, actions.EffectNone, actions.EffectRead),
	)
}

func schemaSpec() actions.Spec {
	return actions.Command("toolmux.mcp.schema", "schema",
		actions.Use("mcp schema <server.tool|server tool>"),
		actions.Short("Show a tool input schema"),
		actions.RBAC("tool_schema", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}
