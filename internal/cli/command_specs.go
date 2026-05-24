package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers"
)

func writeCatalog(cmd *cobra.Command, opts *options) error {
	specs := allPolicyCommandSpecs(opts)
	return writeValue(cmd, opts, specs, func(w io.Writer) {
		human := humanOutputOptions(cmd, opts)
		rows := make([][]string, 0, len(specs))
		for _, spec := range specs {
			rows = append(rows, []string{
				spec.ID,
				spec.Provider,
				spec.Resource,
				spec.Action,
				spec.RemoteEffect,
				spec.LocalEffect,
				strings.Join(spec.Path, " "),
			})
		}
		output.RenderTable(w, human, output.Table{
			Headers: []string{"Command", "Provider", "Resource", "Action", "Remote", "Local", "Path"},
			Rows:    rows,
			Empty:   "no command specs",
		})
	})
}

func allPolicyCommandSpecs(opts *options) []policy.CommandSpec {
	specs := rootCommandSpecs()
	specs = append(specs, providers.CommandSpecs()...)
	specs = append(specs, cachedMCPRemoteCommandSpecs(opts)...)
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func rootCommandSpecs() []policy.CommandSpec {
	specs := []policy.CommandSpec{
		mcpConfigureSpec(),
		mcpEnableSpec(),
		mcpDisableSpec(),
		mcpProfileSetSpec(),
		mcpProfileDefaultSpec(),
		toolboxAddSpec(),
		toolboxRemoveSpec(),
		toolboxStatusSpec(),
		toolboxCatalogListSpec(),
		toolboxCatalogManageSpec(),
		doctorSpec(),
		mcpRemoteSyncSpec(),
		mcpRemoteRenameSpec(),
		mcpRemoteListSpec(),
		mcpRemoteShowSpec(),
		mcpRemoteDefaultsListSpec(),
		mcpRemoteDefaultsSetSpec(),
		mcpRemoteDefaultsRemoveSpec(),
		mcpRemoteAuthLoginSpec(),
		mcpRemoteAuthSetSpec(),
		mcpRemoteAuthRemoveSpec(),
		mcpRemoteAuthStatusSpec(),
		schemaSpec(),
		workflowInitSpec(),
		workflowListSpec(),
		workflowShowSpec(),
		workflowRenderSpec(),
		workflowRunSpec(),
		workflowConfigSetDefaultAgentSpec(),
	}
	return specs
}

func authorize(cmd *cobra.Command, opts *options, spec policy.CommandSpec, args []string) error {
	decision, err := decisionFor(cmd, opts, spec, args)
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	return fmt.Errorf("%w: %s", policy.ErrDenied, decision.Reason)
}

func decisionFor(cmd *cobra.Command, opts *options, spec policy.CommandSpec, args []string) (policy.Decision, error) {
	if opts.readOnly && !policy.AllowsReadOnly(spec) {
		return policy.Decision{
			Allowed: false,
			Reason:  "read-only mode blocks command " + spec.ID,
			Rule:    "read-only",
		}, nil
	}
	engine, _, err := policy.LoadDiscovered(opts.policy, "")
	if err != nil {
		return policy.Decision{}, err
	}
	inv := policy.Invocation{
		Spec:       spec,
		Profile:    opts.profile,
		Account:    "default",
		OutputMode: opts.output,
		Args:       map[string]any{"argv": args},
	}
	return engine.Authorize(inv), nil
}

func specForCommand(opts *options, commandLine string) (policy.CommandSpec, bool) {
	parts := strings.Fields(commandLine)
	if spec, ok := rootSpecForCommandParts(parts); ok {
		return spec, true
	}
	if spec, ok := mcpRemoteSpecForCommandParts(opts, parts); ok {
		return spec, true
	}
	for _, spec := range providers.CommandSpecs() {
		if len(parts) >= len(spec.Path) && equalStrings(parts[:len(spec.Path)], spec.Path) {
			return spec, true
		}
	}
	return policy.CommandSpec{}, false
}

func rootSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) == 0 {
		return policy.CommandSpec{}, false
	}
	switch parts[0] {
	case "add":
		return toolboxAddSpec(), true
	case "remove", "rm":
		return toolboxRemoveSpec(), true
	case "status":
		return toolboxStatusSpec(), true
	case "doctor":
		return doctorSpec(), true
	case "list", "ls":
		return rootCatalogSpecForCommandParts(parts), true
	case "workflow":
		return workflowSpecForCommandParts(parts)
	case "mcp":
		return rootMCPSpecForCommandParts(parts)
	default:
		return policy.CommandSpec{}, false
	}
}

func rootCatalogSpecForCommandParts(parts []string) policy.CommandSpec {
	if mcpRemoteCatalogCommandModifies(parts) {
		return toolboxCatalogManageSpec()
	}
	return toolboxCatalogListSpec()
}

func workflowSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) < 2 {
		return policy.CommandSpec{}, false
	}
	switch parts[1] {
	case "init", "add":
		return workflowInitSpec(), true
	case "list", "ls":
		return workflowListSpec(), true
	case "show":
		return workflowShowSpec(), true
	case "render":
		return workflowRenderSpec(), true
	case "run":
		return workflowRunSpec(), true
	case "config":
		return workflowConfigSpecForCommandParts(parts)
	default:
		return policy.CommandSpec{}, false
	}
}

func workflowConfigSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) >= 4 && parts[2] == "set" && parts[3] == "default-agent" {
		return workflowConfigSetDefaultAgentSpec(), true
	}
	return policy.CommandSpec{}, false
}

func rootMCPSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) < 2 {
		return policy.CommandSpec{}, false
	}
	switch parts[1] {
	case "configure":
		return mcpConfigureSpec(), true
	case "enable":
		return mcpEnableSpec(), true
	case "disable":
		return mcpDisableSpec(), true
	case "profile":
		return mcpProfileSpecForCommandParts(parts)
	case "schema":
		return schemaSpec(), true
	case "sync":
		return mcpRemoteSyncSpec(), true
	case "rename":
		return mcpRemoteRenameSpec(), true
	case "ls", "list":
		return mcpRemoteListSpec(), true
	case "show":
		return mcpRemoteShowSpec(), true
	case "defaults", "default-args":
		return mcpDefaultsSpecForCommandParts(parts)
	case "auth":
		return mcpAuthSpecForCommandParts(parts)
	default:
		return policy.CommandSpec{}, false
	}
}

func mcpProfileSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) < 3 {
		return policy.CommandSpec{}, false
	}
	switch parts[2] {
	case "set":
		return mcpProfileSetSpec(), true
	case "default":
		return mcpProfileDefaultSpec(), true
	default:
		return policy.CommandSpec{}, false
	}
}

func mcpDefaultsSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) < 3 {
		return policy.CommandSpec{}, false
	}
	switch parts[2] {
	case "ls", "list", "show":
		return mcpRemoteDefaultsListSpec(), true
	case "set":
		return mcpRemoteDefaultsSetSpec(), true
	case "remove", "rm", "unset":
		return mcpRemoteDefaultsRemoveSpec(), true
	default:
		return policy.CommandSpec{}, false
	}
}

func mcpAuthSpecForCommandParts(parts []string) (policy.CommandSpec, bool) {
	if len(parts) < 3 {
		return policy.CommandSpec{}, false
	}
	switch parts[2] {
	case "login", "connect":
		return mcpRemoteAuthLoginSpec(), true
	case "set":
		return mcpRemoteAuthSetSpec(), true
	case "remove", "rm":
		return mcpRemoteAuthRemoveSpec(), true
	case "status":
		return mcpRemoteAuthStatusSpec(), true
	default:
		return policy.CommandSpec{}, false
	}
}

func actionExecutionContext(ctx context.Context, opts *options, store credentials.Store, provider providers.Provider) actions.Context {
	return actions.Context{
		Context:     ctx,
		Credentials: store,
		HTTPClient:  opts.httpClient,
		Profile:     opts.profile,
		Account:     "default",
		Provider:    provider.ID,
		ProviderURL: opts.providerURL[provider.ID],
		ProviderAPI: opts.providerAPI[provider.ID],
		ToolmuxdURL: opts.toolmuxdURL,
		Env:         opts.env,
		ReadFile:    os.ReadFile,
		OpenBrowser: opts.openBrowser,
	}
}
