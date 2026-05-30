//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/store"
)

// syncBuffer is a goroutine-safe io.Writer so the follow loop can write while
// the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testDBPath(home string) string {
	return filepath.Join(home, ".toolmux", "toolmux.db")
}

func seedAuditStore(t *testing.T, home string, calls ...store.ToolCall) {
	t.Helper()
	st, err := store.Open(testDBPath(home))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	for _, call := range calls {
		if _, err := st.RecordToolCall(t.Context(), call); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestAuditListsAndFilters(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	seedAuditStore(t, env.Home,
		store.ToolCall{Source: "mcp", CWD: "/work/project-a", ToolID: "slack.send_message", Provider: "slack", Allowed: true},
		store.ToolCall{Source: "mcp", CWD: "/work/project-b", ToolID: "linear.create_issue", Provider: "linear", Allowed: false, Reason: "read-only mode"},
	)

	all := runRootForRemoteTest(t, env, "audit", "--output", "json")
	var rows []store.ToolCallRow
	if err := json.Unmarshal([]byte(all), &rows); err != nil {
		t.Fatalf("decode: %v\n%s", err, all)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	denied := runRootForRemoteTest(t, env, "audit", "--denied", "--output", "json")
	var deniedRows []store.ToolCallRow
	if err := json.Unmarshal([]byte(denied), &deniedRows); err != nil {
		t.Fatalf("decode denied: %v", err)
	}
	if len(deniedRows) != 1 || deniedRows[0].ToolID != "linear.create_issue" {
		t.Fatalf("denied filter: %+v", deniedRows)
	}

	byProject := runRootForRemoteTest(t, env, "audit", "--project", "project-a", "--output", "json")
	if !strings.Contains(byProject, "slack.send_message") || strings.Contains(byProject, "linear.create_issue") {
		t.Fatalf("project filter unexpected: %s", byProject)
	}
}

func TestAuditTableShowsProjectAndStatus(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	seedAuditStore(t, env.Home,
		store.ToolCall{Source: "mcp", CWD: "/work/project-a", ToolID: "slack.send_message", Allowed: true},
	)
	out := runRootForRemoteTest(t, env, "audit", "--color", "never")
	if !strings.Contains(out, "project-a") || !strings.Contains(out, "slack.send_message") {
		t.Fatalf("table missing project/tool: %s", out)
	}
	if !strings.Contains(out, "allowed") {
		t.Fatalf("table missing status: %s", out)
	}
}

func TestAuditShowByID(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	st, err := store.Open(testDBPath(env.Home))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.RecordToolCall(t.Context(), store.ToolCall{
		Source: "mcp", CWD: "/work/p", ToolID: "slack.send_message", Allowed: true, Arguments: `{"channel":"x"}`,
	})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := runRootForRemoteTest(t, env, "audit", "show", "1", "--color", "never")
	_ = id
	if !strings.Contains(out, "slack.send_message") || !strings.Contains(out, `{"channel":"x"}`) {
		t.Fatalf("show missing detail: %s", out)
	}
	if _, err := runRootForRemoteTestError(t, env, "audit", "show", "999"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestWhyExplainsDecision(t *testing.T) {
	workDir := t.TempDir()
	if err := writeToolmuxConfigFile(filepath.Join(workDir, toolmuxConfigRelPath), toolmuxConfigFile{
		Version:   1,
		Toolboxes: map[string]toolboxConfig{"google": {Type: toolboxTypeInternal, Provider: "google"}},
	}); err != nil {
		t.Fatal(err)
	}
	opts := &options{workDir: workDir, profile: "default", env: os.Getenv}
	specs := allPolicyCommandSpecs(opts)
	if len(specs) == 0 {
		t.Fatal("expected google specs")
	}
	// Pick a write tool to exercise the read-only path.
	var writeID string
	for _, spec := range specs {
		if spec.Effect == "write" {
			writeID = spec.ID
			break
		}
	}
	if writeID == "" {
		t.Skip("no write tool available")
	}

	spec, ok := resolvePolicySpec(opts, writeID)
	if !ok {
		t.Fatalf("resolve %s", writeID)
	}
	if d, err := decisionFor(nil, opts, spec, nil); err != nil || !d.Allowed {
		t.Fatalf("expected allowed, got allowed=%v err=%v", d.Allowed, err)
	}
	roOpts := &options{workDir: workDir, profile: "default", readOnly: true, env: os.Getenv}
	if d, err := decisionFor(nil, roOpts, spec, nil); err != nil || d.Allowed || d.Rule != "read-only" {
		t.Fatalf("expected read-only denial, got %+v err=%v", d, err)
	}
}

func TestAuditFollowStreamsNewCalls(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "follow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	for _, call := range []store.ToolCall{
		{Source: "mcp", CWD: "/w/proj", ToolID: "slack.send_message", Allowed: true},
		{Source: "mcp", CWD: "/w/proj", ToolID: "grafana.query_prometheus", Allowed: true},
	} {
		if _, err := st.RecordToolCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}

	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	buf := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- followAuditCalls(fctx, buf, output.Options{}, st, store.ToolCallFilter{Limit: 50}, 10*time.Millisecond)
	}()

	// Backlog (both seeded rows) should appear first.
	waitFor(t, "backlog", func() bool { return strings.Contains(buf.String(), "grafana.query_prometheus") })

	// A new call recorded after following starts should stream in.
	if _, err := st.RecordToolCall(ctx, store.ToolCall{Source: "mcp", CWD: "/w/proj", ToolID: "linear.create_issue", Allowed: false, Reason: "read-only"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "streamed row", func() bool { return strings.Contains(buf.String(), "linear.create_issue") })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("follow returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "slack.send_message") || !strings.Contains(out, "denied") {
		t.Fatalf("unexpected follow output:\n%s", out)
	}
}

func TestWhyUnknownTool(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	if _, err := runRootForRemoteTestError(t, env, "why", "does.not.exist"); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
