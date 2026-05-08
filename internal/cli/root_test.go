package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiam/supacli/internal/credentials"
	"github.com/fiam/supacli/internal/providers/notion"
)

func TestVersionCommand(t *testing.T) {
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() == "" {
		t.Fatal("expected version output")
	}
}

func TestPolicyCatalog(t *testing.T) {
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"policy", "catalog"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("gmail.send")) {
		t.Fatalf("expected gmail.send in catalog, got %q", out.String())
	}
}

func TestColorAlwaysColorsTableOutput(t *testing.T) {
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--color", "always", "policy", "catalog"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("expected --color always to color table output, got %q", out.String())
	}
}

func TestRuntimeErrorDoesNotPrintUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"object":"error","status":503,"code":"not_configured","message":"NOTION_CLIENT_ID is required"}`))
	}))
	defer server.Close()

	cmd := NewRootCommandWithDeps(Dependencies{
		HTTPClient:  server.Client(),
		SupaclidURL: server.URL,
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"connect", "notion"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected connect error")
	}
	if !strings.Contains(err.Error(), "NOTION_CLIENT_ID is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Fatalf("runtime error printed usage:\n%s", out.String())
	}
}

func TestStatusTableShowsProviderPermissions(t *testing.T) {
	store := credentials.NewMemoryStore()
	err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "linear",
		AccountID: "default",
	}, credentials.OAuthTokens{
		AccessToken: "linear-access-token",
		TokenType:   "bearer",
		Scopes:      []string{"read", "issues:create"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"status", "linear"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Provider") || !strings.Contains(rendered, "connected") || !strings.Contains(rendered, "issues:create") {
		t.Fatalf("expected connected status table with permissions, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("non-tty table output should not contain ANSI escape sequences: %q", rendered)
	}
}

func TestDoctorTableRunsCoreAndProviderDiagnostics(t *testing.T) {
	store := credentials.NewMemoryStore()
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"doctor", "linear"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "credential-store") || !strings.Contains(rendered, "not connected") {
		t.Fatalf("expected doctor diagnostics table, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("non-tty doctor output should not contain ANSI escape sequences: %q", rendered)
	}
}

func TestWritePageUsesResourceColumns(t *testing.T) {
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	opts := &options{output: "table", color: "never"}

	err := writePage(cmd, opts, notion.Page{
		ID:  "page-1",
		URL: "https://notion.so/page-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Title", "ID", "URL", "Status", "active"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected page table to contain %q, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "Field") || strings.Contains(rendered, "Value") {
		t.Fatalf("page table should use resource columns, got %q", rendered)
	}
}

func TestPageReadMarkdownSourceAnnotatesLinks(t *testing.T) {
	source := pageReadMarkdownSource(notion.Page{}, notion.PageMarkdown{
		Markdown: "# Work Diary\n\n[April 2026](https://notion.so/april)\n[May 2026](https://notion.so/may)",
	})
	if !strings.Contains(source, "- **April 2026 [1]**") {
		t.Fatalf("expected page read source to annotate links, got %q", source)
	}
	if !strings.Contains(source, "## Links") || !strings.Contains(source, "https://notion.so/may") {
		t.Fatalf("expected page read source to include visible link destinations, got %q", source)
	}
	if !strings.Contains(source, "- **April 2026 [1]**\n- **May 2026 [2]**") {
		t.Fatalf("expected page read source to preserve adjacent link lines, got %q", source)
	}
}

func TestResolveNotionPageArgKeepsIDs(t *testing.T) {
	client, closeServer := notionSearchClient(t, nil)
	defer closeServer()

	cmd := NewRootCommand()
	id, err := resolveNotionPageArg(cmd, &options{output: "table"}, client, "11111111111141118111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected normalized id %q", id)
	}
}

func TestResolveNotionPageArgSingleTitleMatch(t *testing.T) {
	client, closeServer := notionSearchClient(t, []map[string]any{{
		"object": "page",
		"id":     "11111111-1111-4111-8111-111111111111",
		"title":  "Roadmap",
	}})
	defer closeServer()

	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	id, err := resolveNotionPageArg(cmd, &options{output: "table"}, client, "Roadmap")
	if err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected resolved id %q", id)
	}
}

func TestResolveNotionPageArgMultipleNonInteractiveFails(t *testing.T) {
	client, closeServer := notionSearchClient(t, []map[string]any{
		{"object": "page", "id": "11111111-1111-4111-8111-111111111111", "title": "Roadmap"},
		{"object": "page", "id": "22222222-2222-4222-8222-222222222222", "title": "Roadmap archive"},
	})
	defer closeServer()

	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	_, err := resolveNotionPageArg(cmd, &options{output: "table"}, client, "Roadmap")
	if err == nil {
		t.Fatal("expected ambiguous title error")
	}
	if !strings.Contains(err.Error(), "multiple Notion pages match") || !strings.Contains(err.Error(), "Roadmap archive") {
		t.Fatalf("unexpected ambiguous title error: %v", err)
	}
}

func TestNotionPageSelectionLabelUsesCompactIDs(t *testing.T) {
	label := notionPageSelectionLabel(notion.SearchResult{
		ID:    "35650aa7-8fda-80fe-aafa-f24e69aca7a6",
		Title: "May 2026",
		URL:   "https://www.notion.so/35650aa78fda80feaafaf24e69aca7a6",
	})
	if label != "May 2026  35650aa7" {
		t.Fatalf("unexpected selection label %q", label)
	}
}

func TestNotionPageIDFromLinkRecognizesNotionURLs(t *testing.T) {
	id, ok := notionPageIDFromLink("https://www.notion.so/Roadmap-11111111111141118111111111111111?pvs=4")
	if !ok {
		t.Fatal("expected Notion URL to be recognized")
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected Notion page id %q", id)
	}
	if _, ok := notionPageIDFromLink("https://example.com/Roadmap-11111111111141118111111111111111"); ok {
		t.Fatal("external URL with UUID should not be treated as a Notion page")
	}
	if _, ok := notionPageIDFromLink("mailto:11111111111141118111111111111111@example.com"); ok {
		t.Fatal("non-HTTP URL with UUID should not be treated as a Notion page")
	}
}

func TestNotionLinkSelectionDetailCompactsTargets(t *testing.T) {
	notionDetail := notionLinkSelectionDetail("https://www.notion.so/Roadmap-11111111111141118111111111111111?pvs=4")
	if notionDetail != "Notion 11111111" {
		t.Fatalf("unexpected Notion detail %q", notionDetail)
	}
	externalDetail := notionLinkSelectionDetail("https://example.com/docs/setup")
	if externalDetail != "example.com/docs/setup" {
		t.Fatalf("unexpected external detail %q", externalDetail)
	}
}

func notionSearchClient(t *testing.T, results []map[string]any) (*notion.Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"object":   "list",
			"results":  results,
			"has_more": false,
		}); err != nil {
			t.Fatal(err)
		}
	}))
	client := notion.NewClient("token", notion.WithBaseURL(server.URL), notion.WithHTTPClient(server.Client()))
	return client, func() {
		server.Close()
	}
}
