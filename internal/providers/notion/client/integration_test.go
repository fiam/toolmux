package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fiam/supacli/internal/cli"
	"github.com/fiam/supacli/internal/credentials"
	"github.com/fiam/supacli/internal/policy"
	"github.com/fiam/supacli/internal/providers/notion/notiontest"
	"github.com/fiam/supacli/internal/testutil/supaclidtest"
	"github.com/fiam/supacli/internal/testutil/supaclitest"
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

	notionStatus := supaclitest.Run(t, deps, "status", "notion")
	supaclitest.AssertContains(t, notionStatus, "connected")
	supaclitest.AssertContains(t, notionStatus, "read_content")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "status"), "jira")
	notionDoctor := supaclitest.Run(t, deps, "doctor", "notion")
	supaclitest.AssertContains(t, notionDoctor, "notion")
	supaclitest.AssertContains(t, notionDoctor, "search endpoint reachable")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "search", "roadmap"), notiontest.NotionPageID)
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "search", "--query", "tasks"), "Tasks")
	limitedSearch := supaclitest.Run(t, deps, "notion", "search", "--limit", "1", "--sort", "edited", "--direction", "asc")
	supaclitest.AssertContains(t, limitedSearch, notiontest.NotionPageID)
	if strings.Contains(limitedSearch, "Tasks") {
		t.Fatalf("expected search --limit 1 to hide second result, got %q", limitedSearch)
	}
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "get", notiontest.NotionPageID), "Roadmap")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "read", notiontest.NotionPageID), "Initial content")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "markdown", notiontest.NotionPageID), "# Roadmap")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "markdown", "Roadmap"), "# Roadmap")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "links", notiontest.NotionPageID), "https://example.com/spec")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "open", notiontest.NotionPageID, "--url-only"), "https://notion.so/Roadmap-")
	supaclitest.AssertContains(t, supaclitest.Run(t, deps, "notion", "page", "children", notiontest.NotionPageID), "April 2026")
	tree := supaclitest.Run(t, deps, "notion", "page", "tree", notiontest.NotionPageID)
	supaclitest.AssertContains(t, tree, "April 2026")
	supaclitest.AssertContains(t, tree, "Week 1")
	pageDoctor := supaclitest.Run(t, deps, "notion", "page", "doctor", notiontest.NotionPageID)
	supaclitest.AssertContains(t, pageDoctor, "markdown-truncation")
	supaclitest.AssertContains(t, pageDoctor, "unknown-blocks")

	created := supaclitest.Run(
		t,
		deps,
		"notion", "page", "create",
		"--parent-type", "workspace",
		"--title", "Created Page",
		"--markdown", "# Created Page\n\nBody",
	)
	supaclitest.AssertContains(t, created, notiontest.NotionCreatedID)

	updated := supaclitest.Run(
		t,
		deps,
		"notion", "page", "update", notiontest.NotionCreatedID,
		"--title", "Renamed Page",
	)
	supaclitest.AssertContains(t, updated, "Renamed Page")

	inserted := supaclitest.Run(
		t,
		deps,
		"notion", "page", "content", "insert", notiontest.NotionCreatedID,
		"--markdown", "## Added\n\nMore text",
	)
	supaclitest.AssertContains(t, inserted, "Added")

	contentUpdated := supaclitest.Run(
		t,
		deps,
		"notion", "page", "content", "update", notiontest.NotionCreatedID,
		"--old", "More text",
		"--new", "Updated text",
	)
	supaclitest.AssertContains(t, contentUpdated, "Updated text")

	replaced := supaclitest.Run(
		t,
		deps,
		"notion", "page", "content", "replace", notiontest.NotionCreatedID,
		"--markdown", "# Replacement",
		"--yes",
	)
	supaclitest.AssertContains(t, replaced, "# Replacement")

	moved := supaclitest.Run(
		t,
		deps,
		"notion", "page", "move", notiontest.NotionCreatedID,
		"--parent-type", "data-source",
		"--parent", notiontest.NotionDataSourceID,
	)
	supaclitest.AssertContains(t, moved, notiontest.NotionCreatedID)

	deleted := supaclitest.Run(t, deps, "notion", "page", "delete", notiontest.NotionCreatedID, "--yes")
	supaclitest.AssertContains(t, deleted, "trashed")

	restored := supaclitest.Run(t, deps, "notion", "page", "restore", notiontest.NotionCreatedID)
	if strings.Contains(restored, "trashed") {
		t.Fatalf("expected restored page not to be marked trashed, got %q", restored)
	}

	rows := supaclitest.Run(t, deps, "notion", "data-source", "query", notiontest.NotionDataSourceID)
	supaclitest.AssertContains(t, rows, notiontest.NotionPageID)

	schema := supaclitest.Run(t, deps, "notion", "data-source", "schema", notiontest.NotionDataSourceID)
	supaclitest.AssertContains(t, schema, "Name")
	supaclitest.AssertContains(t, schema, "checkbox")

	rowCreated := supaclitest.Run(
		t,
		deps,
		"notion", "data-source", "row", "create", notiontest.NotionDataSourceID,
		"--title", "Created Row",
		"--markdown", "# Created Row\n\nBody",
	)
	supaclitest.AssertContains(t, rowCreated, "Created Row")

	rowUpdated := supaclitest.Run(
		t,
		deps,
		"notion", "data-source", "row", "update", notiontest.NotionCreatedID,
		"--title", "Updated Row",
	)
	supaclitest.AssertContains(t, rowUpdated, "Updated Row")

	dataSources := supaclitest.Run(t, deps, "notion", "database", "data-sources", notiontest.NotionDatabaseID)
	supaclitest.AssertContains(t, dataSources, notiontest.NotionDataSourceID)
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

	out := supaclitest.Run(t, deps, "--output", "json", "notion", "page", "markdown", notiontest.NotionPageID)
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

func TestIntegrationConnectNotionUsesSupaclidEnvOverride(t *testing.T) {
	t.Parallel()
	upstream := notiontest.NewUpstream()
	defer upstream.Close()
	supaclid := supaclidtest.NewNotion(t, upstream.URL, upstream.Client())

	deps := cli.Dependencies{
		Credentials: credentials.NewMemoryStore(),
		HTTPClient:  supaclid.Client(),
		Env:         supaclid.Env,
	}
	out := supaclitest.Run(t, deps, "connect", "notion", "--auth-url-only")
	supaclitest.AssertContains(t, out, supaclid.URL+"/oauth/notion/start")
	supaclitest.AssertContains(t, out, supaclid.URL+"/oauth/notion/callback")
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

	result := supaclitest.RunResult(t, deps, "--policy", policyPath, "notion", "page", "get", notiontest.NotionPageID)
	if result.Err == nil {
		t.Fatal("expected policy denial")
	}
	if !errors.Is(result.Err, policy.ErrDenied) {
		t.Fatalf("unexpected policy denial error: %v\noutput:\n%s", result.Err, result.Output)
	}

	result = supaclitest.RunResult(t, deps, "--policy", policyPath, "doctor", "notion")
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
			"workspace_name": "Supacli Test Workspace",
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
	return credentials.Diagnostics{Available: true, Service: "supacli", Backend: "test", Message: "ok"}
}
