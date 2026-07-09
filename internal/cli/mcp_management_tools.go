package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/policy"
)

func (server mcpServer) managementMCPSpecs(ctx context.Context) []actions.Spec {
	_ = ctx
	var specs []actions.Spec
	for _, spec := range mcpRemoteAuthRefreshSpecs() {
		if server.selector.matches(spec) {
			specs = append(specs, spec)
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func (server mcpServer) lookupMCPManagementTool(ctx context.Context, name string) (actions.Spec, bool) {
	for _, spec := range server.managementMCPSpecs(ctx) {
		if spec.ID == name {
			return spec, true
		}
	}
	return actions.Spec{}, false
}

func (server mcpServer) callManagementTool(ctx context.Context, spec actions.Spec, raw json.RawMessage) (mcpCallToolResult, error) {
	arguments, err := decodeMCPToolArguments(raw, spec)
	if err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	if err := validateMCPArgs(spec, arguments.args); err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	decision, derr := decisionFor(server.cmd, server.opts, spec, arguments.args)
	if derr != nil {
		server.recordToolCall(ctx, spec, arguments, policy.Decision{Reason: derr.Error(), Rule: "error"}, derr, 0)
		return mcpErrorToolResult(derr), nil
	}
	if !decision.Allowed {
		server.recordToolCall(ctx, spec, arguments, decision, nil, 0)
		return mcpErrorToolResult(fmt.Errorf("%w: %s", policy.ErrDenied, decision.Reason)), nil
	}
	started := time.Now()
	result, err := server.callAuthRefreshManagementTool(ctx, spec, arguments)
	server.recordToolCall(ctx, spec, arguments, decision, err, time.Since(started))
	if err != nil {
		return mcpErrorToolResult(err), nil
	}
	return result, nil
}

func (server mcpServer) callAuthRefreshManagementTool(ctx context.Context, spec actions.Spec, arguments mcpToolArguments) (mcpCallToolResult, error) {
	if spec.ID != mcpRemoteAuthRefreshSpec().ID {
		return mcpCallToolResult{}, fmt.Errorf("%s is not implemented", spec.ID)
	}
	toolbox, _ := arguments.flags["toolbox"].(string)
	toolbox = strings.TrimSpace(toolbox)
	toolboxes, _ := arguments.flags["toolboxes"].([]string)
	toolboxes = compactStrings(toolboxes)
	if toolbox != "" && len(toolboxes) > 0 {
		return mcpCallToolResult{}, fmt.Errorf("provide either toolbox or toolboxes, not both")
	}
	names := toolboxes
	if toolbox != "" {
		names = []string{toolbox}
	}
	probe, ok := arguments.flags["probe"].(bool)
	if !ok {
		probe = true
	}
	store, err := server.opts.credentials()
	if err != nil {
		return mcpCallToolResult{}, err
	}
	report, err := refreshMCPRemoteAuth(ctx, server.opts, store, names, mcpRemoteAuthRefreshOptions{
		Probe: probe,
	})
	if err != nil {
		return mcpCallToolResult{}, err
	}
	return mcpCallToolResult{
		Content: []mcpContent{{
			Type: "text",
			Text: mcpRemoteAuthRefreshReportText(report),
		}},
		StructuredContent: report,
	}, nil
}

func mcpRemoteAuthRefreshReportText(report mcpRemoteAuthRefreshReport) string {
	if len(report.Results) == 0 {
		return "no stored remote MCP auth to refresh"
	}
	lines := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = result.Status
		}
		lines = append(lines, fmt.Sprintf("%s: %s (%s)", result.Toolbox, message, result.Status))
	}
	return strings.Join(lines, "\n")
}
