package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/store"
)

// openHistoryStore opens the local SQLite history database. It is used both for
// recording (mcp serve) and reading (the audit command).
func openHistoryStore(opts *options) (*store.Store, error) {
	if opts == nil || opts.dbPath == "" {
		return nil, fmt.Errorf("history database path is not configured")
	}
	return store.Open(opts.dbPath)
}

// auditWorkDir resolves the project directory recorded for a tool call: the
// explicit work dir if set, otherwise the process working directory (which, for
// an agent-launched `mcp serve`, is the project the agent runs in).
func auditWorkDir(opts *options) string {
	if dir := firstNonEmpty(opts.workDir, ""); dir != "" {
		return dir
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return ""
}

// recordToolCall best-effort writes a single tool call to the history database.
// It never fails a tool call: open/audit-disabled servers are a no-op and write
// errors are logged to stderr only.
func (server mcpServer) recordToolCall(ctx context.Context, spec actions.Spec, arguments any, decision policy.Decision, execErr error, elapsed time.Duration) {
	if server.audit == nil {
		return
	}
	args := ""
	if arguments != nil {
		if data, err := json.Marshal(arguments); err == nil {
			args = string(data)
		}
	}
	errText := ""
	if execErr != nil {
		errText = execErr.Error()
	}
	call := store.ToolCall{
		Time:       time.Now(),
		Source:     "mcp",
		CWD:        server.auditCWD,
		Profile:    server.opts.profile,
		ServerName: server.selector.profile,
		ToolID:     spec.ID,
		Provider:   spec.Provider,
		Resource:   spec.Resource,
		Action:     spec.Action,
		Effect:     spec.Effect,
		Allowed:    decision.Allowed,
		Rule:       decision.Rule,
		Reason:     decision.Reason,
		Arguments:  args,
		Error:      errText,
		Duration:   elapsed,
	}
	if _, err := server.audit.RecordToolCall(ctx, call); err != nil {
		fmt.Fprintf(server.lazyStderr(), "toolmux: audit write failed: %v\n", err)
	}
}

// startAudit opens the history store for a serving session when auditing is
// enabled, returning a close function. Failures degrade gracefully: auditing is
// skipped and a notice is written to stderr.
func (server *mcpServer) startAudit(cmd *cobra.Command) func() {
	if server.opts == nil || !server.opts.auditEnabled {
		return func() {}
	}
	st, err := openHistoryStore(server.opts)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "toolmux: audit disabled: %v\n", err)
		return func() {}
	}
	server.audit = st
	server.auditCWD = auditWorkDir(server.opts)
	return func() { _ = st.Close() }
}
