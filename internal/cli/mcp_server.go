package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/version"
)

type mcpServer struct {
	opts     *options
	cmd      *cobra.Command
	selector mcpToolSelector
	lazy     bool
}

func (server mcpServer) run(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		response, ok := server.handleMessage(ctx, line)
		if !ok {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (err mcpError) Error() string {
	return err.Message
}

func (server mcpServer) handleMessage(ctx context.Context, line []byte) (mcpResponse, bool) {
	var request mcpRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &mcpError{Code: -32700, Message: "parse error"},
		}, true
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return server.errorResponse(request, -32600, "invalid request"), true
	}
	result, err := server.handleRequest(ctx, request)
	if len(request.ID) == 0 {
		return mcpResponse{}, false
	}
	if err != nil {
		var rpcErr mcpError
		if errors.As(err, &rpcErr) {
			return mcpResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcErr}, true
		}
		return server.errorResponse(request, -32603, err.Error()), true
	}
	return mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result}, true
}

func (server mcpServer) handleRequest(ctx context.Context, request mcpRequest) (any, error) {
	switch request.Method {
	case "initialize":
		return server.initializeResult(request.Params), nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return server.toolsListResult(ctx), nil
	case "tools/call":
		var params mcpCallToolParams
		if err := decodeMCPParams(request.Params, &params); err != nil {
			return nil, mcpError{Code: -32602, Message: err.Error()}
		}
		return server.callTool(ctx, params)
	default:
		return nil, mcpError{Code: -32601, Message: "method not found: " + request.Method}
	}
}

func (server mcpServer) errorResponse(request mcpRequest, code int, message string) mcpResponse {
	id := request.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpError{Code: code, Message: message},
	}
}

type mcpInitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

func (server mcpServer) initializeResult(params json.RawMessage) map[string]any {
	protocolVersion := mcpProtocolVersion
	var initParams mcpInitializeParams
	if err := decodeMCPParams(params, &initParams); err == nil && initParams.ProtocolVersion != "" {
		protocolVersion = initParams.ProtocolVersion
	}
	serverName := "toolmux"
	if server.selector.profile != "" {
		serverName += "-" + sanitizeMCPName(server.selector.profile)
	}
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": version.Version,
		},
	}
}

func (server mcpServer) toolsListResult(ctx context.Context) map[string]any {
	if server.lazy {
		return map[string]any{"tools": []any{lazySearchTool()}}
	}
	specs := server.mcpSpecs(ctx)
	tools := make([]any, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, mcpToolFromSpec(spec))
	}
	tools = append(tools, server.remoteMCPTools(ctx)...)
	return map[string]any{
		"tools": tools,
	}
}

func (server mcpServer) mcpSpecs(ctx context.Context) []actions.Spec {
	var specs []actions.Spec
	_ = ctx
	entries, err := effectiveNativeToolboxEntries(server.opts.workDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		specs = append(specs, nativeToolboxActionSpecs(entry)...)
	}
	specs = slices.DeleteFunc(specs, func(spec actions.Spec) bool {
		return !server.selector.matches(spec)
	})
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema mcpInputSchema `json:"inputSchema"`
}

type mcpInputSchema struct {
	Type                 string                 `json:"type"`
	Properties           map[string]mcpProperty `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties bool                   `json:"additionalProperties"`
}

type mcpProperty struct {
	Type        any    `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Items       any    `json:"items,omitempty"`
	MinItems    *int   `json:"minItems,omitempty"`
	MaxItems    *int   `json:"maxItems,omitempty"`
	Default     any    `json:"default,omitempty"`
}

func mcpToolFromSpec(spec actions.Spec) mcpTool {
	schema := mcpInputSchema{
		Type:                 "object",
		Properties:           map[string]mcpProperty{},
		AdditionalProperties: false,
	}
	if spec.Args.Min > 0 || spec.Args.Max != 0 {
		property := mcpProperty{
			Type:        "array",
			Description: "Positional arguments for `toolmux " + strings.Join(spec.Path, " ") + "`.",
			Items:       map[string]string{"type": "string"},
		}
		if spec.Args.Min > 0 {
			minimum := spec.Args.Min
			property.MinItems = &minimum
			schema.Required = append(schema.Required, "args")
		}
		if spec.Args.Max >= 0 {
			maximum := spec.Args.Max
			property.MaxItems = &maximum
		}
		schema.Properties["args"] = property
	}
	for _, flag := range spec.Flags {
		schema.Properties[flag.Name] = mcpPropertyFromFlag(flag)
	}
	sort.Strings(schema.Required)
	description := firstNonEmpty(spec.Description, spec.Short, actionShort(spec))
	return mcpTool{
		Name:        spec.ID,
		Description: description,
		InputSchema: schema,
	}
}

func mcpPropertyFromFlag(flag actions.Flag) mcpProperty {
	property := mcpProperty{Description: flag.Usage}
	switch flag.Type {
	case actions.FlagBool:
		property.Type = "boolean"
		property.Default = flag.DefaultBool
	case actions.FlagInt:
		property.Type = "integer"
		property.Default = flag.DefaultInt
	case actions.FlagString:
		property.Type = "string"
		if flag.Default != "" {
			property.Default = flag.Default
		}
	case actions.FlagStringSlice:
		property.Type = "array"
		property.Items = map[string]string{"type": "string"}
		if len(flag.DefaultString) > 0 {
			property.Default = append([]string(nil), flag.DefaultString...)
		}
	default:
		property.Type = "string"
	}
	return property
}

type mcpCallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type mcpCallToolResult struct {
	Content           []mcpContent   `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

type mcpContent struct {
	Type        string         `json:"type"`
	Text        string         `json:"text,omitempty"`
	Data        string         `json:"data,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	URI         string         `json:"uri,omitempty"`
	Name        string         `json:"name,omitempty"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Resource    map[string]any `json:"resource,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

func (server mcpServer) callTool(ctx context.Context, params mcpCallToolParams) (mcpCallToolResult, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "tool name is required"}
	}
	if server.lazy && name == lazySearchToolName {
		return server.searchTools(ctx, params.Arguments)
	}
	spec, ok := server.lookupMCPTool(ctx, name)
	if !ok {
		if ref, ok := server.lookupRemoteMCPTool(ctx, name); ok {
			return server.callRemoteTool(ctx, ref, params.Arguments)
		}
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "unknown MCP tool " + name}
	}
	arguments, err := decodeMCPToolArguments(params.Arguments, spec)
	if err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	if err := validateMCPArgs(spec, arguments.args); err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	if err := authorize(server.cmd, server.opts, spec, arguments.args); err != nil {
		return mcpErrorToolResult(err), nil
	}
	entry, ok, err := lookupNativeToolboxEntry(spec.Provider, server.opts.workDir)
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	if !ok {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "unknown toolbox " + spec.Provider + " for " + spec.ID}
	}
	handler, ok := providers.ActionHandler(entry.Provider, nativeToolboxHandlerID(entry, spec))
	if !ok {
		return mcpErrorToolResult(fmt.Errorf("%s is not implemented", spec.ID)), nil
	}
	store, err := server.opts.credentials()
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	execCtx := actionExecutionContext(ctx, server.opts, store, entry.Provider, entry.Name)
	execCtx.Interactive = false
	if execCtx.OpenBrowser == nil && spec.Action == string(actions.VerbOpen) {
		execCtx.OpenBrowser = openURL
	}
	result, err := handler(execCtx, actions.Invocation{
		Spec:  spec,
		Args:  arguments.args,
		Flags: arguments.flags,
	})
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	text, err := mcpTextResult(result)
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	return mcpTextToolResult(text), nil
}

func (server mcpServer) callRemoteTool(ctx context.Context, ref mcpRemoteToolRef, raw json.RawMessage) (mcpCallToolResult, error) {
	arguments, err := remoteMCPToolArguments(raw)
	if err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	arguments = mcpRemoteMergeDefaultArguments(arguments, ref.Entry.Server.DefaultArguments, ref.Tool.InputSchema)
	spec := mcpRemoteActionSpecForEntry(ref.Entry, ref.Tool)
	if err := authorize(server.cmd, server.opts, spec, nil); err != nil {
		return mcpErrorToolResult(err), nil
	}
	token := ""
	if ref.Entry.Server.Transport != mcpRemoteTransportStdio {
		var err error
		token, err = loadMCPRemoteAccessToken(ctx, server.opts, ref.Entry)
		if err != nil {
			return mcpErrorToolResult(err), nil
		}
	}
	result, err := callMCPRemoteTool(ctx, server.opts.httpClient, ref.Entry, ref.Tool, arguments, token, mcpRemoteToolCallTimeout(server.opts), nil)
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	return result, nil
}

func (server mcpServer) lookupMCPTool(ctx context.Context, name string) (actions.Spec, bool) {
	for _, spec := range server.mcpSpecs(ctx) {
		if spec.ID == name {
			return spec, true
		}
	}
	return actions.Spec{}, false
}

type mcpToolArguments struct {
	args  []string
	flags map[string]any
}

func decodeMCPToolArguments(raw json.RawMessage, spec actions.Spec) (mcpToolArguments, error) {
	var decoded map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		decoded = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(raw, &decoded); err != nil {
		return mcpToolArguments{}, fmt.Errorf("tool arguments must be an object")
	}
	args, err := decodeMCPPositionalArgs(decoded["args"])
	if err != nil {
		return mcpToolArguments{}, err
	}
	flags := make(map[string]any, len(spec.Flags))
	for _, flag := range spec.Flags {
		value, err := decodeMCPFlagValue(decoded[flag.Name], flag)
		if err != nil {
			return mcpToolArguments{}, err
		}
		flags[flag.Name] = value
	}
	return mcpToolArguments{args: args, flags: flags}, nil
}

func decodeMCPPositionalArgs(raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	return nil, fmt.Errorf("args must be a string or an array of strings")
}

func decodeMCPFlagValue(raw json.RawMessage, flag actions.Flag) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return mcpDefaultFlagValue(flag), nil
	}
	switch flag.Type {
	case actions.FlagBool:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s must be a boolean", flag.Name)
		}
		return value, nil
	case actions.FlagInt:
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s must be an integer", flag.Name)
		}
		return value, nil
	case actions.FlagString:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s must be a string", flag.Name)
		}
		return value, nil
	case actions.FlagStringSlice:
		var values []string
		if err := json.Unmarshal(raw, &values); err == nil {
			return values, nil
		}
		var single string
		if err := json.Unmarshal(raw, &single); err == nil {
			return []string{single}, nil
		}
		return nil, fmt.Errorf("%s must be a string or an array of strings", flag.Name)
	default:
		return nil, fmt.Errorf("%s has unsupported flag type %q", flag.Name, flag.Type)
	}
}

func mcpDefaultFlagValue(flag actions.Flag) any {
	switch flag.Type {
	case actions.FlagBool:
		return flag.DefaultBool
	case actions.FlagInt:
		return flag.DefaultInt
	case actions.FlagString:
		return flag.Default
	case actions.FlagStringSlice:
		return append([]string(nil), flag.DefaultString...)
	default:
		return nil
	}
}

func validateMCPArgs(spec actions.Spec, args []string) error {
	if len(args) < spec.Args.Min {
		return fmt.Errorf("%s requires at least %d positional argument(s)", spec.ID, spec.Args.Min)
	}
	if spec.Args.Max >= 0 && len(args) > spec.Args.Max {
		return fmt.Errorf("%s accepts at most %d positional argument(s)", spec.ID, spec.Args.Max)
	}
	return nil
}

func mcpErrorToolResult(err error) mcpCallToolResult {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "tool failed"
	}
	return mcpCallToolResult{
		IsError: true,
		Content: []mcpContent{{
			Type: "text",
			Text: message,
		}},
	}
}

func mcpTextToolResult(text string) mcpCallToolResult {
	return mcpCallToolResult{
		Content: []mcpContent{{
			Type: "text",
			Text: text,
		}},
	}
}

func mcpTextResult(result any) (string, error) {
	if result == nil {
		return "", nil
	}
	if markdown, ok := result.(actions.MarkdownRenderable); ok {
		text := markdown.MarkdownSource()
		if truncated, unknown := markdown.MarkdownTruncated(); truncated {
			text = strings.TrimRight(text, "\n") + "\n\ntruncated: " + strconv.Itoa(unknown) + " unknown blocks"
		}
		return text, nil
	}
	if text, ok := result.(actions.TextRenderable); ok {
		return text.Text(), nil
	}
	if opener, ok := result.(actions.BrowserOpenRenderable); ok && opener.BrowserURL() != "" {
		return opener.BrowserURL(), nil
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeMCPParams(raw json.RawMessage, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid params")
	}
	return nil
}
