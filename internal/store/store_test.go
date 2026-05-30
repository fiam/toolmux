package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRecordAndGetToolCall(t *testing.T) {
	t.Parallel()
	st := openTestStore(t)
	ctx := t.Context()
	id, err := st.RecordToolCall(ctx, ToolCall{
		Source: "mcp", CWD: "/home/me/project-a", Profile: "default",
		ToolID: "slack.send_message", Provider: "slack", Resource: "message",
		Action: "send", Effect: "write", Allowed: true, Rule: "default-allow",
		Arguments: `{"channel":"x"}`, Duration: 1500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	got, ok, err := st.GetToolCall(ctx, id)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ToolID != "slack.send_message" || !got.Allowed || got.DurationMS != 1500 {
		t.Fatalf("unexpected row: %+v", got)
	}
	if _, ok, _ := st.GetToolCall(ctx, 9999); ok {
		t.Fatalf("expected missing row")
	}
}

func TestQueryToolCallsFilters(t *testing.T) {
	t.Parallel()
	st := openTestStore(t)
	ctx := t.Context()
	seed := []ToolCall{
		{Source: "mcp", CWD: "/home/me/project-a", ToolID: "slack.send_message", Provider: "slack", Allowed: true},
		{Source: "mcp", CWD: "/home/me/project-b", ToolID: "grafana.query_prometheus", Provider: "grafana", Allowed: true},
		{Source: "mcp", CWD: "/home/me/project-a", ToolID: "linear.create_issue", Provider: "linear", Allowed: false, Reason: "read-only"},
	}
	for _, call := range seed {
		if _, err := st.RecordToolCall(ctx, call); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	all, err := st.QueryToolCalls(ctx, ToolCallFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all: n=%d err=%v", len(all), err)
	}
	// Newest first.
	if all[0].ToolID != "linear.create_issue" {
		t.Fatalf("expected newest first, got %s", all[0].ToolID)
	}

	byProject, _ := st.QueryToolCalls(ctx, ToolCallFilter{Project: "project-a"})
	if len(byProject) != 2 {
		t.Fatalf("project filter: %d", len(byProject))
	}
	byTool, _ := st.QueryToolCalls(ctx, ToolCallFilter{Tool: "slack.*"})
	if len(byTool) != 1 || byTool[0].Provider != "slack" {
		t.Fatalf("tool glob filter: %+v", byTool)
	}
	denied, _ := st.QueryToolCalls(ctx, ToolCallFilter{DeniedOnly: true})
	if len(denied) != 1 || denied[0].ToolID != "linear.create_issue" {
		t.Fatalf("denied filter: %+v", denied)
	}
	limited, _ := st.QueryToolCalls(ctx, ToolCallFilter{Limit: 1})
	if len(limited) != 1 {
		t.Fatalf("limit: %d", len(limited))
	}
	byProvider, _ := st.QueryToolCalls(ctx, ToolCallFilter{Provider: "grafana"})
	if len(byProvider) != 1 {
		t.Fatalf("provider filter: %d", len(byProvider))
	}
}

func TestReopenSharesData(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "reopen.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	if _, err := first.RecordToolCall(t.Context(), ToolCall{Source: "mcp", ToolID: "t", Allowed: true}); err != nil {
		t.Fatalf("record: %v", err)
	}
	_ = first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer second.Close()
	rows, _ := second.QueryToolCalls(t.Context(), ToolCallFilter{})
	if len(rows) != 1 {
		t.Fatalf("expected persisted row after reopen, got %d", len(rows))
	}
}
