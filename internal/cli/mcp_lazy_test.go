package cli

import (
	"slices"
	"testing"
)

func lazyTestUniverse() []lazyToolEntry {
	names := []struct{ name, desc string }{
		{"slack.send_message", "Send a message to a Slack channel"},
		{"slack.conversations_history", "Read recent messages from a channel"},
		{"grafana.query_prometheus", "Run a Prometheus query against Grafana"},
		{"linear.create_issue", "Create a Linear issue"},
		{"notion.search", "Search Notion pages and databases"},
	}
	universe := make([]lazyToolEntry, 0, len(names))
	for _, n := range names {
		universe = append(universe, lazyToolEntry{name: n.name, description: n.desc})
	}
	return universe
}

func lazyNames(entries []lazyToolEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}

func TestRankLazyToolsSelectExact(t *testing.T) {
	t.Parallel()
	got := lazyNames(rankLazyTools(lazyTestUniverse(), "select:linear.create_issue, notion.search ,missing.tool", 10))
	want := []string{"linear.create_issue", "notion.search"}
	if !slices.Equal(got, want) {
		t.Fatalf("select got %v, want %v", got, want)
	}
}

func TestRankLazyToolsKeywordRanksNameOverDescription(t *testing.T) {
	t.Parallel()
	got := lazyNames(rankLazyTools(lazyTestUniverse(), "message", 10))
	// send_message matches the name (weight 3); conversations_history only
	// matches "messages" in the description (weight 1), so it ranks lower.
	want := []string{"slack.send_message", "slack.conversations_history"}
	if !slices.Equal(got, want) {
		t.Fatalf("keyword got %v, want %v", got, want)
	}
}

func TestRankLazyToolsRequiredTermFiltersName(t *testing.T) {
	t.Parallel()
	got := lazyNames(rankLazyTools(lazyTestUniverse(), "+slack message", 10))
	if !slices.Contains(got, "slack.send_message") {
		t.Fatalf("expected slack.send_message in %v", got)
	}
	for _, name := range got {
		if name == "grafana.query_prometheus" || name == "linear.create_issue" {
			t.Fatalf("required +slack term should exclude non-slack tools, got %v", got)
		}
	}
}

func TestRankLazyToolsRespectsMaxResults(t *testing.T) {
	t.Parallel()
	got := rankLazyTools(lazyTestUniverse(), "message channel query create search", 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d (%v)", len(got), lazyNames(got))
	}
}

func TestRankLazyToolsNoMatch(t *testing.T) {
	t.Parallel()
	if got := rankLazyTools(lazyTestUniverse(), "kubernetes", 10); len(got) != 0 {
		t.Fatalf("expected no matches, got %v", lazyNames(got))
	}
}

func TestRankLazyToolsProviderBoost(t *testing.T) {
	t.Parallel()
	got := lazyNames(rankLazyTools(lazyTestUniverse(), "slack", 10))
	want := []string{"slack.conversations_history", "slack.send_message"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("provider query got %v, want both slack tools %v", got, want)
	}
}

func TestRankLazyToolsSubsequence(t *testing.T) {
	t.Parallel()
	got := lazyNames(rankLazyTools(lazyTestUniverse(), "qprom", 10))
	if !slices.Equal(got, []string{"grafana.query_prometheus"}) {
		t.Fatalf("subsequence query got %v, want [grafana.query_prometheus]", got)
	}
}

func TestLazyProfileRoundTrips(t *testing.T) {
	t.Parallel()
	config := mcpProfileConfigFromSelection(mcpToolSelection{Tools: []string{"slack.*"}, Lazy: true})
	if !config.Lazy {
		t.Fatalf("expected profile config to persist Lazy")
	}
	if got := config.selection("ops"); !got.Lazy {
		t.Fatalf("expected selection from profile to carry Lazy")
	}
}

func TestLazyToolSelectionArgsEmitsFlag(t *testing.T) {
	t.Parallel()
	args := mcpToolSelectionArgs(mcpToolSelection{Lazy: true})
	if !slices.Contains(args, "--lazy") {
		t.Fatalf("expected --lazy in serve args, got %v", args)
	}
	if slices.Contains(mcpToolSelectionArgs(mcpToolSelection{}), "--lazy") {
		t.Fatalf("did not expect --lazy when Lazy is false")
	}
}

func TestLazySearchToolSchema(t *testing.T) {
	t.Parallel()
	tool := lazySearchTool()
	if tool.Name != lazySearchToolName {
		t.Fatalf("name = %q, want %q", tool.Name, lazySearchToolName)
	}
	if _, ok := tool.InputSchema.Properties["query"]; !ok {
		t.Fatalf("expected query property in schema")
	}
	if !slices.Equal(tool.InputSchema.Required, []string{"query"}) {
		t.Fatalf("required = %v, want [query]", tool.InputSchema.Required)
	}
}

func TestLazyToolsListAdvertisesOnlyMetaTool(t *testing.T) {
	t.Parallel()
	server := mcpServer{lazy: true}
	result := server.toolsListResult(t.Context())
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 advertised tool, got %v", result["tools"])
	}
	if tool, ok := tools[0].(mcpTool); !ok || tool.Name != lazySearchToolName {
		t.Fatalf("expected %q meta-tool, got %v", lazySearchToolName, tools[0])
	}
}
