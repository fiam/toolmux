package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/cli"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers/notion/notiontest"
	"github.com/fiam/toolmux/internal/testutil/toolmuxdtest"
	"github.com/fiam/toolmux/internal/testutil/toolmuxtest"
)

func TestIntegrationNotionPageLifecycle(t *testing.T) {
	t.Parallel()
	upstream := notiontest.NewUpstream()
	defer upstream.Close()

	store := credentials.NewMemoryStore()
	saveNotionToken(t, store)
	deps := cli.Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		ProviderURL: map[string]string{"notion": upstream.URL + "/notion"},
	}

	notionStatus := toolmuxtest.Run(t, deps, "status", "notion")
	toolmuxtest.AssertContains(t, notionStatus, "connected")
	toolmuxtest.AssertContains(t, notionStatus, "read_content")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "status"), "jira")
	notionDoctor := toolmuxtest.Run(t, deps, "doctor", "notion")
	toolmuxtest.AssertContains(t, notionDoctor, "notion")
	toolmuxtest.AssertContains(t, notionDoctor, "search endpoint reachable")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "search", "roadmap"), notiontest.NotionPageID)
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "search", "--query", "tasks"), "Tasks")
	limitedSearch := toolmuxtest.Run(t, deps, "notion", "search", "--limit", "1", "--sort", "edited", "--direction", "asc")
	toolmuxtest.AssertContains(t, limitedSearch, notiontest.NotionPageID)
	if strings.Contains(limitedSearch, "Tasks") {
		t.Fatalf("expected search --limit 1 to hide second result, got %q", limitedSearch)
	}
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "get", notiontest.NotionPageID), "Roadmap")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "read", notiontest.NotionPageID), "Initial content")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "markdown", notiontest.NotionPageID), "# Roadmap")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "markdown", "Roadmap"), "# Roadmap")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "links", notiontest.NotionPageID), "https://example.com/spec")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "open", notiontest.NotionPageID, "--url-only"), "https://notion.so/Roadmap-")
	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "notion", "page", "children", notiontest.NotionPageID), "April 2026")
	tree := toolmuxtest.Run(t, deps, "notion", "page", "tree", notiontest.NotionPageID)
	toolmuxtest.AssertContains(t, tree, "April 2026")
	toolmuxtest.AssertContains(t, tree, "Week 1")
	pageDoctor := toolmuxtest.Run(t, deps, "notion", "page", "doctor", notiontest.NotionPageID)
	toolmuxtest.AssertContains(t, pageDoctor, "markdown-truncation")
	toolmuxtest.AssertContains(t, pageDoctor, "unknown-blocks")

	created := toolmuxtest.Run(
		t,
		deps,
		"notion", "page", "create",
		"--parent-type", "workspace",
		"--title", "Created Page",
		"--markdown", "# Created Page\n\nBody",
	)
	toolmuxtest.AssertContains(t, created, notiontest.NotionCreatedID)

	updated := toolmuxtest.Run(
		t,
		deps,
		"notion", "page", "update", notiontest.NotionCreatedID,
		"--title", "Renamed Page",
	)
	toolmuxtest.AssertContains(t, updated, "Renamed Page")

	inserted := toolmuxtest.Run(
		t,
		deps,
		"notion", "page", "content", "insert", notiontest.NotionCreatedID,
		"--markdown", "## Added\n\nMore text",
	)
	toolmuxtest.AssertContains(t, inserted, "Added")

	contentUpdated := toolmuxtest.Run(
		t,
		deps,
		"notion", "page", "content", "update", notiontest.NotionCreatedID,
		"--old", "More text",
		"--new", "Updated text",
	)
	toolmuxtest.AssertContains(t, contentUpdated, "Updated text")

	replaced := toolmuxtest.Run(
		t,
		deps,
		"notion", "page", "content", "replace", notiontest.NotionCreatedID,
		"--markdown", "# Replacement",
		"--yes",
	)
	toolmuxtest.AssertContains(t, replaced, "# Replacement")

	moved := toolmuxtest.Run(
		t,
		deps,
		"notion", "page", "move", notiontest.NotionCreatedID,
		"--parent-type", "data-source",
		"--parent", notiontest.NotionDataSourceID,
	)
	toolmuxtest.AssertContains(t, moved, notiontest.NotionCreatedID)

	deleted := toolmuxtest.Run(t, deps, "notion", "page", "delete", notiontest.NotionCreatedID, "--yes")
	toolmuxtest.AssertContains(t, deleted, "trashed")

	restored := toolmuxtest.Run(t, deps, "notion", "page", "restore", notiontest.NotionCreatedID)
	if strings.Contains(restored, "trashed") {
		t.Fatalf("expected restored page not to be marked trashed, got %q", restored)
	}

	rows := toolmuxtest.Run(t, deps, "notion", "data-source", "query", notiontest.NotionDataSourceID)
	toolmuxtest.AssertContains(t, rows, notiontest.NotionPageID)

	schema := toolmuxtest.Run(t, deps, "notion", "data-source", "schema", notiontest.NotionDataSourceID)
	toolmuxtest.AssertContains(t, schema, "Name")
	toolmuxtest.AssertContains(t, schema, "checkbox")

	rowCreated := toolmuxtest.Run(
		t,
		deps,
		"notion", "data-source", "row", "create", notiontest.NotionDataSourceID,
		"--title", "Created Row",
		"--markdown", "# Created Row\n\nBody",
	)
	toolmuxtest.AssertContains(t, rowCreated, "Created Row")

	rowUpdated := toolmuxtest.Run(
		t,
		deps,
		"notion", "data-source", "row", "update", notiontest.NotionCreatedID,
		"--title", "Updated Row",
	)
	toolmuxtest.AssertContains(t, rowUpdated, "Updated Row")

	dataSources := toolmuxtest.Run(t, deps, "notion", "database", "data-sources", notiontest.NotionDatabaseID)
	toolmuxtest.AssertContains(t, dataSources, notiontest.NotionDataSourceID)
}

func TestIntegrationNotionJSONOutputIsStable(t *testing.T) {
	t.Parallel()
	upstream := notiontest.NewUpstream()
	defer upstream.Close()

	store := credentials.NewMemoryStore()
	saveNotionToken(t, store)
	deps := cli.Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		ProviderURL: map[string]string{"notion": upstream.URL + "/notion"},
	}

	out := toolmuxtest.Run(t, deps, "--output", "json", "notion", "page", "markdown", notiontest.NotionPageID)
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("json output contains ANSI escape sequence: %q", out)
	}
	var decoded struct {
		Object   string `json:"object"`
		ID       string `json:"id"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Object != "page_markdown" || decoded.ID != notiontest.NotionPageID || !strings.Contains(decoded.Markdown, "Roadmap") {
		t.Fatalf("unexpected markdown JSON: %#v", decoded)
	}
}

func TestIntegrationConnectNotionUsesToolmuxdEnvOverride(t *testing.T) {
	t.Parallel()
	upstream := notiontest.NewUpstream()
	defer upstream.Close()
	toolmuxd := toolmuxdtest.NewNotion(t, upstream.URL, upstream.Client())

	deps := cli.Dependencies{
		Credentials: credentials.NewMemoryStore(),
		HTTPClient:  toolmuxd.Client(),
		Env:         toolmuxd.Env,
	}
	out := toolmuxtest.Run(t, deps, "connect", "notion", "--auth-url-only")
	toolmuxtest.AssertContains(t, out, toolmuxd.URL+"/oauth/notion/start")
	toolmuxtest.AssertContains(t, out, toolmuxd.URL+"/oauth/notion/callback")
}

func TestIntegrationNotionPolicyDenialPreventsCredentialRead(t *testing.T) {
	t.Parallel()
	upstream := notiontest.NewUpstream()
	defer upstream.Close()

	dir := t.TempDir()
	policyPath := dir + "/policy.yaml"
	policyYAML := []byte(`version: 1
default: deny
roles:
  reader:
    allow:
      - command: notion.search
bindings:
  - role: reader
`)
	if err := os.WriteFile(policyPath, policyYAML, 0o644); err != nil {
		t.Fatal(err)
	}

	deps := cli.Dependencies{
		Credentials: credentialStoreFunc(func(context.Context, credentials.ConnectionRef) (credentials.OAuthTokens, error) {
			t.Fatal("credential store should not be read after policy denial")
			return credentials.OAuthTokens{}, nil
		}),
		HTTPClient:  upstream.Client(),
		ProviderURL: map[string]string{"notion": upstream.URL + "/notion"},
	}

	result := toolmuxtest.RunResult(t, deps, "--policy", policyPath, "notion", "page", "get", notiontest.NotionPageID)
	if result.Err == nil {
		t.Fatal("expected policy denial")
	}
	if !errors.Is(result.Err, policy.ErrDenied) {
		t.Fatalf("unexpected policy denial error: %v\noutput:\n%s", result.Err, result.Output)
	}

	result = toolmuxtest.RunResult(t, deps, "--policy", policyPath, "doctor", "notion")
	if result.Err == nil {
		t.Fatal("expected doctor policy denial")
	}
}

func saveNotionToken(t *testing.T, store credentials.Store) {
	t.Helper()
	err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "notion",
		AccountID: "default",
	}, credentials.OAuthTokens{
		AccessToken:  "fake-access-token",
		RefreshToken: "fake-refresh-token",
		TokenType:    "bearer",
		Scopes:       []string{"read_content", "insert_content", "update_content"},
		Extra: map[string]string{
			"workspace_id":   "notion-workspace-1",
			"workspace_name": "Toolmux Test Workspace",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

type credentialStoreFunc func(context.Context, credentials.ConnectionRef) (credentials.OAuthTokens, error)

func (f credentialStoreFunc) SaveOAuthTokens(ctx context.Context, ref credentials.ConnectionRef, tokens credentials.OAuthTokens) error {
	return nil
}

func (f credentialStoreFunc) LoadOAuthTokens(ctx context.Context, ref credentials.ConnectionRef) (credentials.OAuthTokens, error) {
	return f(ctx, ref)
}

func (f credentialStoreFunc) DeleteOAuthTokens(ctx context.Context, ref credentials.ConnectionRef) error {
	return nil
}

func (f credentialStoreFunc) Doctor(ctx context.Context) credentials.Diagnostics {
	return credentials.Diagnostics{Available: true, Service: "toolmux", Backend: "test", Message: "ok"}
}
