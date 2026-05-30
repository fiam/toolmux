package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
)

type whyResult struct {
	Tool            string   `json:"tool" yaml:"tool"`
	Allowed         bool     `json:"allowed" yaml:"allowed"`
	Rule            string   `json:"rule,omitempty" yaml:"rule,omitempty"`
	Reason          string   `json:"reason,omitempty" yaml:"reason,omitempty"`
	ReadOnly        bool     `json:"read_only" yaml:"read_only"`
	AllowedReadOnly bool     `json:"allowed_read_only" yaml:"allowed_read_only"`
	RemoteEffect    string   `json:"remote_effect,omitempty" yaml:"remote_effect,omitempty"`
	LocalEffect     string   `json:"local_effect,omitempty" yaml:"local_effect,omitempty"`
	Scopes          []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Risks           []string `json:"risks,omitempty" yaml:"risks,omitempty"`
}

func whyCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "why <tool> [args...]",
		Short: "Explain whether a tool is allowed and which policy rule decides",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			spec, ok := resolvePolicySpec(opts, name)
			if !ok {
				return fmt.Errorf("unknown tool %q; run `toolmux mcp schema` or `toolmux mcp search` to list tools", name)
			}
			decision, err := decisionFor(cmd, opts, spec, args[1:])
			if err != nil {
				return err
			}
			result := whyResult{
				Tool:            spec.ID,
				Allowed:         decision.Allowed,
				Rule:            decision.Rule,
				Reason:          decision.Reason,
				ReadOnly:        opts.readOnly,
				AllowedReadOnly: policy.AllowsReadOnly(spec),
				RemoteEffect:    spec.RemoteEffect,
				LocalEffect:     spec.LocalEffect,
				Scopes:          spec.Scopes,
				Risks:           spec.Risk,
			}
			return writeValue(cmd, opts, result, func(w io.Writer) {
				renderWhy(w, cmd, opts, result)
			})
		},
	}
}

// resolvePolicySpec finds a command spec by its dotted id (e.g.
// slack.conversations_add_message) or by its path joined with "." or " ".
func resolvePolicySpec(opts *options, name string) (actions.Spec, bool) {
	name = strings.TrimSpace(name)
	for _, spec := range allPolicyCommandSpecs(opts) {
		if spec.ID == name ||
			strings.Join(spec.Path, ".") == name ||
			strings.Join(spec.Path, " ") == name {
			return spec, true
		}
	}
	return actions.Spec{}, false
}

func renderWhy(w io.Writer, cmd *cobra.Command, opts *options, result whyResult) {
	human := humanOutputOptions(cmd, opts)
	status := "allowed"
	if !result.Allowed {
		status = "denied"
	}
	readOnly := "off"
	if result.ReadOnly {
		readOnly = "on"
	}
	pairs := [][2]string{
		{"Tool", output.ToneText(human, output.ToneInfo, result.Tool)},
		{"Decision", output.StatusBadge(human, status)},
		{"Rule", output.Value(result.Rule)},
		{"Reason", output.Value(result.Reason)},
		{"Read-only", readOnly},
		{"Remote effect", output.Value(result.RemoteEffect)},
		{"Local effect", output.Value(result.LocalEffect)},
		{"Scopes", output.JoinList(result.Scopes)},
		{"Risks", output.JoinList(result.Risks)},
	}
	out := make([][]string, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, []string{pair[0], pair[1]})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Field", "Value"},
		Rows:    out,
	})
}
