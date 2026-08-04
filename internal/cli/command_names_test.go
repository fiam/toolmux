package cli

import "testing"

func TestMCPRemoteToolCLINamesTrimCatalogPrefixAndAvoidCollisions(t *testing.T) {
	t.Parallel()
	entry := mcpRemoteServerEntry{
		Name:   "notion-work",
		Server: mcpBuiltinRemoteServers()["notion"],
	}
	tools := []mcpRemoteTool{
		{Name: "notion-get-users"},
		{Name: "notion-search_items"},
		{Name: "get-users"},
	}
	names := mcpRemoteToolCLINames(entry, tools)
	if got := names["notion-search_items"]; got != "search-items" {
		t.Fatalf("notion-search_items CLI name = %q, want search-items", got)
	}
	if got := names["notion-get-users"]; got != "notion-get-users" {
		t.Fatalf("colliding notion-get-users CLI name = %q, want raw fallback", got)
	}
	if got := names["get-users"]; got != "get-users" {
		t.Fatalf("colliding get-users CLI name = %q, want get-users", got)
	}
	for _, tool := range tools {
		ref := mcpRemoteToolRef{Entry: entry, Cache: mcpRemoteCache{Tools: tools}, Tool: tool}
		if got := mcpRemoteActionSpecForRef(ref).Path[1]; got != names[tool.Name] {
			t.Fatalf("policy path for %s = %q, want %q", tool.Name, got, names[tool.Name])
		}
	}
}

func TestCLINameMappingPreservesAmbiguousRawNames(t *testing.T) {
	t.Parallel()
	mapping := newCLINameMapping([]string{"user_id", "user-id", "cloudId"}, nil)
	if got := mapping.name("user_id"); got != "user_id" {
		t.Fatalf("user_id mapping = %q, want raw collision fallback", got)
	}
	if got := mapping.name("user-id"); got != "user-id" {
		t.Fatalf("user-id mapping = %q, want user-id", got)
	}
	if got := mapping.name("cloudId"); got != "cloud-id" {
		t.Fatalf("cloudId mapping = %q, want cloud-id", got)
	}
}
