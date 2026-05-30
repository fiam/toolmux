package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/store"
)

func auditCommand(opts *options) *cobra.Command {
	var (
		limit             int
		project, tool, pv string
		denied            bool
		since             time.Duration
	)
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Show the history of tool calls made through Toolmux",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openHistoryStore(opts)
			if err != nil {
				return err
			}
			defer st.Close()
			filter := store.ToolCallFilter{
				Limit:      limit,
				Project:    project,
				Tool:       tool,
				Provider:   pv,
				DeniedOnly: denied,
			}
			if since > 0 {
				filter.Since = time.Now().Add(-since)
			}
			rows, err := st.QueryToolCalls(commandContext(cmd), filter)
			if err != nil {
				return err
			}
			return writeValue(cmd, opts, rows, func(w io.Writer) {
				renderAuditTable(w, cmd, opts, rows)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of entries to show")
	cmd.Flags().StringVar(&project, "project", "", "filter by project path substring")
	cmd.Flags().StringVar(&tool, "tool", "", "filter by tool id glob, such as slack.*")
	cmd.Flags().StringVar(&pv, "provider", "", "filter by provider")
	cmd.Flags().BoolVar(&denied, "denied", false, "only show denied calls")
	cmd.Flags().DurationVar(&since, "since", 0, "only show calls newer than this duration, such as 24h")
	cmd.AddCommand(auditShowCommand(opts))
	return cmd
}

func auditShowCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show full details for a recorded tool call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id %q", args[0])
			}
			st, err := openHistoryStore(opts)
			if err != nil {
				return err
			}
			defer st.Close()
			row, ok, err := st.GetToolCall(commandContext(cmd), id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no recorded tool call with id %d", id)
			}
			return writeValue(cmd, opts, row, func(w io.Writer) {
				renderAuditDetail(w, cmd, opts, row)
			})
		},
	}
}

func renderAuditTable(w io.Writer, cmd *cobra.Command, opts *options, rows []store.ToolCallRow) {
	human := humanOutputOptions(cmd, opts)
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, []string{
			strconv.FormatInt(row.ID, 10),
			row.Time.Local().Format("Jan 02 15:04"),
			output.Value(auditProject(row.CWD)),
			output.ToneText(human, output.ToneInfo, row.ToolID),
			output.StatusBadge(human, auditStatus(row)),
			strconv.FormatInt(row.DurationMS, 10) + "ms",
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"ID", "Time", "Project", "Tool", "Status", "Dur"},
		Rows:    out,
		Empty:   "no recorded tool calls",
		Align:   output.RightAlign(6, 0, 5),
	})
}

func renderAuditDetail(w io.Writer, cmd *cobra.Command, opts *options, row store.ToolCallRow) {
	human := humanOutputOptions(cmd, opts)
	pairs := [][2]string{
		{"ID", strconv.FormatInt(row.ID, 10)},
		{"Time", row.Time.Local().Format("2006-01-02 15:04:05")},
		{"Source", output.Value(row.Source)},
		{"Project", output.Value(row.CWD)},
		{"Profile", output.Value(row.Profile)},
		{"Server", output.Value(row.ServerName)},
		{"Tool", output.ToneText(human, output.ToneInfo, row.ToolID)},
		{"Provider", output.Value(row.Provider)},
		{"Resource", output.Value(row.Resource)},
		{"Action", output.Value(row.Action)},
		{"Effect", output.Value(row.Effect)},
		{"Status", output.StatusBadge(human, auditStatus(row))},
		{"Rule", output.Value(row.Rule)},
		{"Reason", output.Value(row.Reason)},
		{"Duration", strconv.FormatInt(row.DurationMS, 10) + "ms"},
		{"Arguments", output.Value(row.Arguments)},
		{"Error", output.Value(row.Error)},
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

func auditProject(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

func auditStatus(row store.ToolCallRow) string {
	switch {
	case !row.Allowed:
		return "denied"
	case row.Error != "":
		return "error"
	default:
		return "allowed"
	}
}
