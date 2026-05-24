package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers"
)

func registerActionCommands(root *cobra.Command, opts *options) {
	entries, err := effectiveNativeToolboxEntries(opts.workDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if len(entry.Provider.Tree.Children) == 0 {
			continue
		}
		registerActionNode(root, opts, entry, nativeToolboxTree(entry), nil)
	}
}

func registerActionNode(parent *cobra.Command, opts *options, entry nativeToolboxEntry, node actions.Spec, parentPath []string) {
	resolved := actions.Resolve(actions.ProviderName(entry.Name), node, parentPath)
	if len(node.Children) > 0 {
		group := actionGroupCommand(resolved)
		annotateNativeActionCommand(group, entry)
		parent.AddCommand(group)
		for _, child := range node.Children {
			registerActionNode(group, opts, entry, child, resolved.Path)
		}
		return
	}
	if resolved.ID == "" {
		return
	}
	cmd := actionCommand(opts, resolved, entry.Provider, nativeToolboxHandlerID(entry, resolved), entry.Name)
	annotateNativeActionCommand(cmd, entry)
	parent.AddCommand(cmd)
}

func annotateNativeActionCommand(cmd *cobra.Command, entry nativeToolboxEntry) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[nativeProviderAnnotation] = entry.Provider.ID
	cmd.Annotations[nativeToolboxAnnotation] = entry.Name
}

func actionCommand(opts *options, spec policy.CommandSpec, provider providers.Provider, handlerID, account string) *cobra.Command {
	use := spec.Use
	if use == "" {
		use = spec.Path[len(spec.Path)-1]
	}
	short := spec.Short
	if short == "" {
		short = actionShort(spec)
	}
	cmd := &cobra.Command{
		Use:     use,
		Aliases: spec.Aliases,
		Short:   short,
		Long:    firstNonEmpty(spec.Description, short),
		Args:    actionArgs(spec),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := authorize(cmd, opts, spec, args); err != nil {
				return err
			}
			handler, ok := providers.ActionHandler(provider, handlerID)
			if ok {
				store, err := opts.credentials()
				if err != nil {
					return err
				}
				execCtx := actionExecutionContext(commandContext(cmd), opts, store, provider, account)
				execCtx.Interactive = interactiveCommand(cmd, opts)
				if execCtx.OpenBrowser == nil && execCtx.Interactive {
					execCtx.OpenBrowser = openURL
				}
				execCtx.Progress = newConnectUI(cmd, opts)
				execCtx.SelectString = selectString(cmd)
				execCtx.SelectInteger = selectInteger(cmd)
				result, err := handler(execCtx, actions.Invocation{
					Spec:  spec,
					Args:  append([]string(nil), args...),
					Flags: metadataFlagValues(cmd, spec),
				})
				if err != nil {
					return err
				}
				return writeActionResult(cmd, opts, execCtx, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: not implemented yet\n", spec.ID)
			return nil
		},
	}
	addMetadataFlags(cmd, spec)
	return cmd
}

func actionGroupCommand(group actions.Spec) *cobra.Command {
	use := group.Use
	if use == "" {
		use = group.Segment
	}
	short := group.Short
	if short == "" {
		short = "Policy-aware command group"
	}
	return &cobra.Command{
		Use:                use,
		Aliases:            group.Aliases,
		Short:              short,
		Long:               firstNonEmpty(group.Description, short),
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
	}
}

func actionArgs(spec policy.CommandSpec) cobra.PositionalArgs {
	minimum := spec.Args.Min
	maximum := spec.Args.Max
	if maximum < 0 {
		return cobra.MinimumNArgs(minimum)
	}
	if minimum == maximum {
		return cobra.ExactArgs(minimum)
	}
	if minimum == 0 {
		return cobra.MaximumNArgs(maximum)
	}
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MinimumNArgs(minimum)(cmd, args); err != nil {
			return err
		}
		return cobra.MaximumNArgs(maximum)(cmd, args)
	}
}

func addMetadataFlags(cmd *cobra.Command, spec policy.CommandSpec) {
	for _, flag := range spec.Flags {
		switch flag.Type {
		case actions.FlagBool:
			cmd.Flags().Bool(flag.Name, flag.DefaultBool, flag.Usage)
		case actions.FlagInt:
			cmd.Flags().Int(flag.Name, flag.DefaultInt, flag.Usage)
		case actions.FlagString:
			cmd.Flags().String(flag.Name, flag.Default, flag.Usage)
		case actions.FlagStringSlice:
			cmd.Flags().StringSlice(flag.Name, flag.DefaultString, flag.Usage)
		}
	}
}

func metadataFlagValues(cmd *cobra.Command, spec policy.CommandSpec) map[string]any {
	values := make(map[string]any, len(spec.Flags))
	for _, flag := range spec.Flags {
		switch flag.Type {
		case actions.FlagBool:
			values[flag.Name], _ = cmd.Flags().GetBool(flag.Name)
		case actions.FlagInt:
			values[flag.Name], _ = cmd.Flags().GetInt(flag.Name)
		case actions.FlagString:
			values[flag.Name], _ = cmd.Flags().GetString(flag.Name)
		case actions.FlagStringSlice:
			values[flag.Name], _ = cmd.Flags().GetStringSlice(flag.Name)
		}
	}
	return values
}

func actionShort(spec policy.CommandSpec) string {
	return strings.TrimSpace(humanVerb(spec.Action) + " " + providerDisplayName(spec.Provider) + " " + humanResource(spec.Resource))
}

func humanVerb(verb string) string {
	switch verb {
	case "create":
		return "Create"
	case "delete":
		return "Delete"
	case "diagnose":
		return "Diagnose"
	case "list":
		return "List"
	case "move":
		return "Move"
	case "open":
		return "Open"
	case "query":
		return "Query"
	case "read":
		return "Read"
	case "restore":
		return "Restore"
	case "search":
		return "Search"
	case "send":
		return "Send"
	case "update":
		return "Update"
	default:
		return "Run"
	}
}

func providerDisplayName(id string) string {
	provider, ok := providers.Lookup(id)
	if !ok {
		return id
	}
	return provider.DisplayName
}

func humanResource(resource string) string {
	return strings.ReplaceAll(resource, "_", " ")
}
