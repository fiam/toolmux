package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func schemaCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "schema <server.tool|server tool>",
		Short: "Show a tool input schema",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			serverName, toolName, err := parseSchemaToolArgs(args)
			if err != nil {
				return err
			}
			ref, ok, err := lookupMCPRemoteToolForSchema(commandContext(cmd), cmd, opts, serverName, toolName)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("remote MCP tool %s.%s not found; run `toolmux mcp sync %s` to refresh cached tools", serverName, toolName, serverName)
			}
			return writeValue(cmd, opts, ref.Tool.InputSchema, nil)
		},
	}
}

func parseSchemaToolArgs(args []string) (string, string, error) {
	if len(args) == 2 {
		serverName, err := cleanMCPRemoteName(args[0])
		if err != nil {
			return "", "", err
		}
		toolName := strings.TrimSpace(args[1])
		if toolName == "" {
			return "", "", fmt.Errorf("tool name is required")
		}
		return serverName, toolName, nil
	}
	toolID := strings.TrimSpace(args[0])
	serverName, toolName, ok := strings.Cut(toolID, ".")
	if !ok || strings.TrimSpace(serverName) == "" || strings.TrimSpace(toolName) == "" {
		return "", "", fmt.Errorf("tool must be referenced as <server>.<tool> or <server> <tool>")
	}
	cleanedServerName, err := cleanMCPRemoteName(serverName)
	if err != nil {
		return "", "", err
	}
	return cleanedServerName, strings.TrimSpace(toolName), nil
}

func lookupMCPRemoteToolForSchema(ctx context.Context, cmd *cobra.Command, opts *options, serverName, toolName string) (mcpRemoteToolRef, bool, error) {
	entry, ok, err := lookupMCPRemoteServer(serverName, opts.workDir)
	if err != nil {
		return mcpRemoteToolRef{}, false, err
	}
	if !ok {
		return mcpRemoteToolRef{}, false, fmt.Errorf("MCP server %q is not registered", serverName)
	}
	cache, ok := refreshMCPRemoteCacheIfStale(ctx, cmd, opts, entry, nil)
	if !ok {
		return mcpRemoteToolRef{}, false, nil
	}
	tool, found := mcpRemoteToolFromCache(cache, toolName)
	if !found {
		return mcpRemoteToolRef{}, false, nil
	}
	return mcpRemoteToolRef{Entry: entry, Cache: cache, Tool: tool}, true, nil
}

func addMCPRemoteToolFlags(cmd *cobra.Command, tool mcpRemoteTool, defaultArguments ...map[string]any) {
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	required := mcpRemoteRequiredSet(tool.InputSchema)
	if len(defaultArguments) > 0 {
		for name := range mcpRemoteDefaultArgumentsForTool(defaultArguments[0], tool.InputSchema) {
			delete(required, name)
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if mcpRemoteFlagNameReserved(cmd, name) {
			continue
		}
		property, _ := properties[name].(map[string]any)
		usage := mcpRemoteFlagUsage(property, required[name])
		switch mcpRemoteSchemaType(property) {
		case "boolean":
			cmd.Flags().Bool(name, false, usage)
		case "integer":
			cmd.Flags().Int(name, 0, usage)
		case "number":
			cmd.Flags().Float64(name, 0, usage)
		case "string":
			cmd.Flags().String(name, "", usage)
		case "boolean_array":
			cmd.Flags().BoolSlice(name, nil, usage)
		case "integer_array":
			cmd.Flags().IntSlice(name, nil, usage)
		case "number_array":
			cmd.Flags().Float64Slice(name, nil, usage)
		case "string_array":
			cmd.Flags().StringArray(name, nil, usage)
		}
	}
}

func decodeMCPRemoteCLIArguments(cmd *cobra.Command, rawJSON string, tool mcpRemoteTool, defaultArguments map[string]any) (map[string]any, error) {
	arguments := mcpRemoteDefaultArgumentsForTool(defaultArguments, tool.InputSchema)
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON != "" {
		data := []byte(rawJSON)
		if path, ok := strings.CutPrefix(rawJSON, "@"); ok {
			read, err := os.ReadFile(path) // #nosec G304 -- explicit CLI argument file.
			if err != nil {
				return nil, err
			}
			data = read
		}
		var rawArguments map[string]any
		if err := json.Unmarshal(data, &rawArguments); err != nil {
			return nil, fmt.Errorf("--json must be a JSON object: %w", err)
		}
		if rawArguments == nil {
			return nil, fmt.Errorf("--json must be a JSON object")
		}
		maps.Copy(arguments, rawArguments)
	}
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	for name := range properties {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || !cmd.Flags().Changed(name) {
			continue
		}
		property, _ := properties[name].(map[string]any)
		switch mcpRemoteSchemaType(property) {
		case "boolean":
			value, err := cmd.Flags().GetBool(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "integer":
			value, err := cmd.Flags().GetInt(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "number":
			value, err := cmd.Flags().GetFloat64(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "string":
			value, err := cmd.Flags().GetString(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "boolean_array":
			value, err := cmd.Flags().GetBoolSlice(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "integer_array":
			value, err := cmd.Flags().GetIntSlice(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "number_array":
			value, err := cmd.Flags().GetFloat64Slice(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		case "string_array":
			value, err := cmd.Flags().GetStringArray(name)
			if err != nil {
				return nil, err
			}
			arguments[name] = value
		}
	}
	if err := validateMCPRemoteRequiredArguments(arguments, tool.InputSchema); err != nil {
		return nil, err
	}
	return arguments, nil
}

func mcpRemoteDefaultArgumentsForTool(defaultArguments map[string]any, schema map[string]any) map[string]any {
	arguments := map[string]any{}
	if len(defaultArguments) == 0 {
		return arguments
	}
	properties := mcpRemoteSchemaProperties(schema)
	for name, value := range defaultArguments {
		if _, ok := properties[name]; ok {
			arguments[name] = value
		}
	}
	return arguments
}

func mcpRemoteMergeDefaultArguments(arguments map[string]any, defaultArguments map[string]any, schema map[string]any) map[string]any {
	merged := mcpRemoteDefaultArgumentsForTool(defaultArguments, schema)
	maps.Copy(merged, arguments)
	return merged
}

func mcpRemoteInputSchemaWithDefaults(schema map[string]any, defaultArguments map[string]any) map[string]any {
	effectiveDefaults := mcpRemoteDefaultArgumentsForTool(defaultArguments, schema)
	if len(effectiveDefaults) == 0 {
		return schema
	}
	cloned := cloneMCPRemoteMap(schema)
	properties := mcpRemoteSchemaProperties(cloned)
	for name, value := range effectiveDefaults {
		property, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		property["default"] = value
	}
	if required := mcpRemoteRequiredNames(cloned); len(required) > 0 {
		filtered := make([]string, 0, len(required))
		for _, name := range required {
			if _, ok := effectiveDefaults[name]; !ok {
				filtered = append(filtered, name)
			}
		}
		if len(filtered) == 0 {
			delete(cloned, "required")
		} else {
			cloned["required"] = filtered
		}
	}
	return cloned
}

func cloneMCPRemoteMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}

func mcpRemoteSchemaType(property map[string]any) string {
	if mcpRemoteSchemaHasType(property, "array") {
		items, _ := property["items"].(map[string]any)
		switch mcpRemoteSchemaScalarType(items) {
		case "boolean":
			return "boolean_array"
		case "integer":
			return "integer_array"
		case "number":
			return "number_array"
		case "string":
			return "string_array"
		default:
			return ""
		}
	}
	if scalar := mcpRemoteSchemaScalarType(property); scalar != "" {
		return scalar
	}
	if len(mcpRemoteSchemaEnum(property)) > 0 {
		return "string"
	}
	return ""
}

func mcpRemoteSchemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return map[string]any{}
	}
	return properties
}

func mcpRemoteSchemaScalarType(property map[string]any) string {
	for _, typ := range mcpRemoteSchemaTypes(property["type"]) {
		switch typ {
		case "boolean", "integer", "number", "string":
			return typ
		}
	}
	return ""
}

func mcpRemoteSchemaHasType(property map[string]any, want string) bool {
	return slices.Contains(mcpRemoteSchemaTypes(property["type"]), want)
}

func mcpRemoteSchemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	case []any:
		types := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok {
				types = append(types, value)
			}
		}
		return types
	default:
		return nil
	}
}

func mcpRemoteFlagNameReserved(cmd *cobra.Command, name string) bool {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
		return true
	}
	if cmd.Flags().Lookup(name) != nil {
		return true
	}
	switch name {
	case "color", "help", "output", "pager", "policy", "profile", "read-only":
		return true
	default:
		return false
	}
}

func mcpRemoteFlagUsage(property map[string]any, required bool) string {
	usage, _ := property["description"].(string)
	usage = strings.TrimSpace(usage)
	var details []string
	if enum := mcpRemoteSchemaEnum(property); len(enum) > 0 {
		details = append(details, "one of: "+strings.Join(enum, ", "))
	}
	if required {
		details = append(details, "required")
	}
	if len(details) == 0 {
		return usage
	}
	detail := strings.Join(details, "; ")
	if usage == "" {
		return detail
	}
	return usage + " (" + detail + ")"
}

func mcpRemoteSchemaEnum(property map[string]any) []string {
	raw, ok := property["enum"]
	if !ok {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return values
	case []any:
		enum := make([]string, 0, len(values))
		for _, value := range values {
			enum = append(enum, fmt.Sprint(value))
		}
		return enum
	default:
		return nil
	}
}

func validateMCPRemoteRequiredArguments(arguments map[string]any, schema map[string]any) error {
	required := mcpRemoteRequiredSet(schema)
	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0, len(required))
	for name := range required {
		if _, ok := arguments[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return mcpRemoteMissingRequiredArgumentsError{Names: missing}
}

func mcpRemoteRequiredNames(schema map[string]any) []string {
	var required []string
	switch values := schema["required"].(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				required = append(required, value)
			}
		}
	case []any:
		for _, item := range values {
			value, ok := item.(string)
			if ok {
				if value = strings.TrimSpace(value); value != "" {
					required = append(required, value)
				}
			}
		}
	}
	return required
}

func mcpRemoteRequiredSet(schema map[string]any) map[string]bool {
	required := map[string]bool{}
	for _, value := range mcpRemoteRequiredNames(schema) {
		required[value] = true
	}
	return required
}

func setMCPRemoteToolHelp(cmd *cobra.Command, entry mcpRemoteServerEntry, tool mcpRemoteTool) {
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		description := strings.TrimSpace(cmd.Long)
		if description == "" {
			description = strings.TrimSpace(cmd.Short)
		}
		if description != "" {
			fmt.Fprintln(cmd.OutOrStdout(), description)
			fmt.Fprintln(cmd.OutOrStdout())
		}
		if cmd.Runnable() || cmd.HasSubCommands() {
			fmt.Fprint(cmd.OutOrStdout(), cmd.UsageString())
		}
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintf(cmd.OutOrStdout(), "Run `toolmux mcp schema %s %s` to view the full input schema.\n", entry.Name, tool.Name)
	})
}
