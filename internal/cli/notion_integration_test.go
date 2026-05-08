package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fiam/supacli/internal/credentials"
	supaclidserver "github.com/fiam/supacli/internal/server"
	"github.com/fiam/supacli/internal/testutil/fakeupstream"
)

func TestIntegrationNotionPageLifecycle(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	store := credentials.NewMemoryStore()
	saveNotionToken(t, store)
	deps := Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		NotionURL:   upstream.URL + "/notion",
	}

	notionStatus := runSupacli(t, deps, "status", "notion")
	assertOutputContains(t, notionStatus, "connected")
	assertOutputContains(t, notionStatus, "read_content")
	assertOutputContains(t, runSupacli(t, deps, "status"), "jira")
	notionDoctor := runSupacli(t, deps, "doctor", "notion")
	assertOutputContains(t, notionDoctor, "notion")
	assertOutputContains(t, notionDoctor, "search endpoint reachable")
	assertOutputContains(t, runSupacli(t, deps, "notion", "search", "roadmap"), fakeupstream.NotionPageID)
	assertOutputContains(t, runSupacli(t, deps, "notion", "search", "--query", "tasks"), "Tasks")
	limitedSearch := runSupacli(t, deps, "notion", "search", "--limit", "1", "--sort", "edited", "--direction", "asc")
	assertOutputContains(t, limitedSearch, fakeupstream.NotionPageID)
	if strings.Contains(limitedSearch, "Tasks") {
		t.Fatalf("expected search --limit 1 to hide second result, got %q", limitedSearch)
	}
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "get", fakeupstream.NotionPageID), "Roadmap")
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "read", fakeupstream.NotionPageID), "Initial content")
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "markdown", fakeupstream.NotionPageID), "# Roadmap")
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "markdown", "Roadmap"), "# Roadmap")
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "links", fakeupstream.NotionPageID), "https://example.com/spec")
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "open", fakeupstream.NotionPageID, "--url-only"), "https://notion.so/Roadmap-")
	assertOutputContains(t, runSupacli(t, deps, "notion", "page", "children", fakeupstream.NotionPageID), "April 2026")
	tree := runSupacli(t, deps, "notion", "page", "tree", fakeupstream.NotionPageID)
	assertOutputContains(t, tree, "April 2026")
	assertOutputContains(t, tree, "Week 1")
	pageDoctor := runSupacli(t, deps, "notion", "page", "doctor", fakeupstream.NotionPageID)
	assertOutputContains(t, pageDoctor, "markdown-truncation")
	assertOutputContains(t, pageDoctor, "unknown-blocks")

	created := runSupacli(
		t,
		deps,
		"notion", "page", "create",
		"--parent-type", "workspace",
		"--title", "Created Page",
		"--markdown", "# Created Page\n\nBody",
	)
	assertOutputContains(t, created, fakeupstream.NotionCreatedID)

	updated := runSupacli(
		t,
		deps,
		"notion", "page", "update", fakeupstream.NotionCreatedID,
		"--title", "Renamed Page",
	)
	assertOutputContains(t, updated, "Renamed Page")

	inserted := runSupacli(
		t,
		deps,
		"notion", "page", "content", "insert", fakeupstream.NotionCreatedID,
		"--markdown", "## Added\n\nMore text",
	)
	assertOutputContains(t, inserted, "Added")

	contentUpdated := runSupacli(
		t,
		deps,
		"notion", "page", "content", "update", fakeupstream.NotionCreatedID,
		"--old", "More text",
		"--new", "Updated text",
	)
	assertOutputContains(t, contentUpdated, "Updated text")

	replaced := runSupacli(
		t,
		deps,
		"notion", "page", "content", "replace", fakeupstream.NotionCreatedID,
		"--markdown", "# Replacement",
		"--yes",
	)
	assertOutputContains(t, replaced, "# Replacement")

	moved := runSupacli(
		t,
		deps,
		"notion", "page", "move", fakeupstream.NotionCreatedID,
		"--parent-type", "data-source",
		"--parent", fakeupstream.NotionDataSourceID,
	)
	assertOutputContains(t, moved, fakeupstream.NotionCreatedID)

	deleted := runSupacli(t, deps, "notion", "page", "delete", fakeupstream.NotionCreatedID, "--yes")
	assertOutputContains(t, deleted, "trashed")

	restored := runSupacli(t, deps, "notion", "page", "restore", fakeupstream.NotionCreatedID)
	if strings.Contains(restored, "trashed") {
		t.Fatalf("expected restored page not to be marked trashed, got %q", restored)
	}

	rows := runSupacli(t, deps, "notion", "data-source", "query", fakeupstream.NotionDataSourceID)
	assertOutputContains(t, rows, fakeupstream.NotionPageID)

	schema := runSupacli(t, deps, "notion", "data-source", "schema", fakeupstream.NotionDataSourceID)
	assertOutputContains(t, schema, "Name")
	assertOutputContains(t, schema, "checkbox")

	rowCreated := runSupacli(
		t,
		deps,
		"notion", "data-source", "row", "create", fakeupstream.NotionDataSourceID,
		"--title", "Created Row",
		"--markdown", "# Created Row\n\nBody",
	)
	assertOutputContains(t, rowCreated, "Created Row")

	rowUpdated := runSupacli(
		t,
		deps,
		"notion", "data-source", "row", "update", fakeupstream.NotionCreatedID,
		"--title", "Updated Row",
	)
	assertOutputContains(t, rowUpdated, "Updated Row")

	dataSources := runSupacli(t, deps, "notion", "database", "data-sources", fakeupstream.NotionDatabaseID)
	assertOutputContains(t, dataSources, fakeupstream.NotionDataSourceID)
}

func TestIntegrationNotionJSONOutputIsStable(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	store := credentials.NewMemoryStore()
	saveNotionToken(t, store)
	deps := Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		NotionURL:   upstream.URL + "/notion",
	}

	out := runSupacli(t, deps, "--output", "json", "notion", "page", "markdown", fakeupstream.NotionPageID)
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
	if decoded.Object != "page_markdown" || decoded.ID != fakeupstream.NotionPageID || !strings.Contains(decoded.Markdown, "Roadmap") {
		t.Fatalf("unexpected markdown JSON: %#v", decoded)
	}
}

func TestIntegrationConnectNotionUsesSupaclidEnvOverride(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	handler := supaclidserver.NewHandlerWithConfig(supaclidserver.Config{
		NotionClientID:   "client-id",
		NotionSecret:     "client-secret",
		NotionAuthURL:    upstream.URL + "/oauth/authorize",
		NotionTokenURL:   upstream.URL + "/oauth/token",
		NotionRevokeURL:  upstream.URL + "/oauth/revoke",
		NotionAPIVersion: "2026-03-11",
		HTTPClient:       upstream.Client(),
		SessionTTL:       time.Minute,
	})
	supaclid := httptest.NewServer(handler)
	defer supaclid.Close()
	t.Setenv("SUPACLI_SUPACLID_URL", supaclid.URL)

	deps := Dependencies{
		Credentials: credentials.NewMemoryStore(),
		HTTPClient:  supaclid.Client(),
	}
	out := runSupacli(t, deps, "connect", "notion", "--auth-url-only")
	assertOutputContains(t, out, supaclid.URL+"/oauth/notion/start")
	assertOutputContains(t, out, supaclid.URL+"/oauth/notion/callback")
}

func TestIntegrationNotionPolicyDenialPreventsCredentialRead(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	dir := t.TempDir()
	policyPath := dir + "/policy.yaml"
	policy := []byte(`version: 1
default: deny
roles:
  reader:
    allow:
      - command: notion.search
bindings:
  - role: reader
`)
	if err := os.WriteFile(policyPath, policy, 0o644); err != nil {
		t.Fatal(err)
	}

	deps := Dependencies{
		Credentials: credentialStoreFunc(func(context.Context, credentials.ConnectionRef) (credentials.OAuthTokens, error) {
			t.Fatal("credential store should not be read after policy denial")
			return credentials.OAuthTokens{}, nil
		}),
		HTTPClient: upstream.Client(),
		NotionURL:  upstream.URL + "/notion",
	}

	cmd := NewRootCommandWithDeps(deps)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--policy", policyPath, "notion", "page", "get", fakeupstream.NotionPageID})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected policy denial")
	}

	cmd = NewRootCommandWithDeps(deps)
	out = &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--policy", policyPath, "doctor", "notion"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected doctor policy denial")
	}
}

func runSupacli(t *testing.T, deps Dependencies, args ...string) string {
	t.Helper()
	cmd := NewRootCommandWithDeps(deps)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("supacli %s failed: %v\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
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

func assertOutputContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("expected output to contain %q, got %q", want, output)
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
