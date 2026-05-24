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
	specs := providers.CommandSpecs()
	specs = append(specs, cachedMCPRemoteCommandSpecs(opts)...)
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
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
