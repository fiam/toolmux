package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ToolCall is the write model for a single tool invocation recorded by
// `toolmux mcp serve`.
type ToolCall struct {
	Time       time.Time
	Source     string
	CWD        string
	Profile    string
	ServerName string
	ToolID     string
	Provider   string
	Resource   string
	Action     string
	Effect     string
	Allowed    bool
	Rule       string
	Reason     string
	Arguments  string
	Error      string
	Duration   time.Duration
}

// ToolCallRow is a recorded tool call read back from the database.
type ToolCallRow struct {
	ID         int64     `json:"id" yaml:"id"`
	Time       time.Time `json:"time" yaml:"time"`
	Source     string    `json:"source" yaml:"source"`
	CWD        string    `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Profile    string    `json:"profile,omitempty" yaml:"profile,omitempty"`
	ServerName string    `json:"server_name,omitempty" yaml:"server_name,omitempty"`
	ToolID     string    `json:"tool_id" yaml:"tool_id"`
	Provider   string    `json:"provider,omitempty" yaml:"provider,omitempty"`
	Resource   string    `json:"resource,omitempty" yaml:"resource,omitempty"`
	Action     string    `json:"action,omitempty" yaml:"action,omitempty"`
	Effect     string    `json:"effect,omitempty" yaml:"effect,omitempty"`
	Allowed    bool      `json:"allowed" yaml:"allowed"`
	Rule       string    `json:"rule,omitempty" yaml:"rule,omitempty"`
	Reason     string    `json:"reason,omitempty" yaml:"reason,omitempty"`
	Arguments  string    `json:"arguments,omitempty" yaml:"arguments,omitempty"`
	Error      string    `json:"error,omitempty" yaml:"error,omitempty"`
	DurationMS int64     `json:"duration_ms" yaml:"duration_ms"`
}

// ToolCallFilter narrows a QueryToolCalls result.
type ToolCallFilter struct {
	Limit      int
	Project    string // substring match against cwd
	Tool       string // glob (*, ?) or substring match against tool_id
	Provider   string // exact match
	DeniedOnly bool
	Since      time.Time
}

const toolCallColumns = "id, ts, source, cwd, profile, server_name, tool_id, provider, resource, action, effect, allowed, rule, reason, arguments, error, duration_ms"

// RecordToolCall inserts a tool call and returns its row id.
func (s *Store) RecordToolCall(ctx context.Context, call ToolCall) (int64, error) {
	ts := call.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO tool_calls
(ts, source, cwd, profile, server_name, tool_id, provider, resource, action, effect, allowed, rule, reason, arguments, error, duration_ms)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ts.UTC().Format(time.RFC3339Nano), call.Source, call.CWD, call.Profile, call.ServerName,
		call.ToolID, call.Provider, call.Resource, call.Action, call.Effect,
		boolToInt(call.Allowed), call.Rule, call.Reason, call.Arguments, call.Error,
		call.Duration.Milliseconds(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// QueryToolCalls returns recorded tool calls newest-first, narrowed by filter.
func (s *Store) QueryToolCalls(ctx context.Context, filter ToolCallFilter) ([]ToolCallRow, error) {
	var conditions []string
	var args []any
	if filter.Project != "" {
		conditions = append(conditions, "cwd LIKE ?")
		args = append(args, "%"+filter.Project+"%")
	}
	if filter.Tool != "" {
		conditions = append(conditions, "tool_id LIKE ?")
		args = append(args, globToLike(filter.Tool))
	}
	if filter.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, filter.Provider)
	}
	if filter.DeniedOnly {
		conditions = append(conditions, "allowed = 0")
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "ts >= ?")
		args = append(args, filter.Since.UTC().Format(time.RFC3339Nano))
	}
	query := "SELECT " + toolCallColumns + " FROM tool_calls"
	if len(conditions) > 0 {
		// #nosec G202 -- conditions are static, parameter-free SQL fragments defined in this file; all user-supplied values are bound via ? placeholders.
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCallRow
	for rows.Next() {
		row, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetToolCall returns a single recorded tool call by id.
func (s *Store) GetToolCall(ctx context.Context, id int64) (ToolCallRow, bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+toolCallColumns+" FROM tool_calls WHERE id = ?", id)
	rec, err := scanToolCall(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolCallRow{}, false, nil
	}
	if err != nil {
		return ToolCallRow{}, false, err
	}
	return rec, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanToolCall(s rowScanner) (ToolCallRow, error) {
	var (
		row                                                                                      ToolCallRow
		ts                                                                                       string
		allowed                                                                                  int
		cwd, profile, serverName, provider, resource, action, effect, rule, reason, arguments, e sql.NullString
		durationMS                                                                               sql.NullInt64
	)
	if err := s.Scan(&row.ID, &ts, &row.Source, &cwd, &profile, &serverName, &row.ToolID,
		&provider, &resource, &action, &effect, &allowed, &rule, &reason, &arguments, &e, &durationMS); err != nil {
		return ToolCallRow{}, err
	}
	row.Time, _ = time.Parse(time.RFC3339Nano, ts)
	row.CWD = cwd.String
	row.Profile = profile.String
	row.ServerName = serverName.String
	row.Provider = provider.String
	row.Resource = resource.String
	row.Action = action.String
	row.Effect = effect.String
	row.Allowed = allowed != 0
	row.Rule = rule.String
	row.Reason = reason.String
	row.Arguments = arguments.String
	row.Error = e.String
	row.DurationMS = durationMS.Int64
	return row, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// globToLike converts a simple glob (with * and ?) to a SQL LIKE pattern. A
// pattern with no wildcards is treated as a substring match.
func globToLike(pattern string) string {
	if !strings.ContainsAny(pattern, "*?") {
		return "%" + pattern + "%"
	}
	return strings.NewReplacer("*", "%", "?", "_").Replace(pattern)
}
