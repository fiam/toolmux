package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/output"
)

func TestPageTableUsesResourceColumns(t *testing.T) {
	t.Parallel()
	rendered := renderTable(Page{
		ID:  "page-1",
		URL: "https://notion.so/page-1",
	}.Table(output.Options{}))

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
	t.Parallel()
	source := PageRead{
		Markdown: PageMarkdown{
			Markdown: "# Work Diary\n\n[April 2026](https://notion.so/april)\n[May 2026](https://notion.so/may)",
		},
	}.MarkdownSource()

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

func TestResolvePageArgKeepsIDs(t *testing.T) {
	t.Parallel()
	client, closeServer := searchClient(t, nil)
	defer closeServer()

	id, err := resolvePageArg(actions.Context{Context: context.Background()}, client, "11111111111141118111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected normalized id %q", id)
	}
}

func TestResolvePageArgSingleTitleMatch(t *testing.T) {
	t.Parallel()
	client, closeServer := searchClient(t, []map[string]any{{
		"object": "page",
		"id":     "11111111-1111-4111-8111-111111111111",
		"title":  "Roadmap",
	}})
	defer closeServer()

	id, err := resolvePageArg(actions.Context{Context: context.Background()}, client, "Roadmap")
	if err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected resolved id %q", id)
	}
}

func TestResolvePageArgMultipleNonInteractiveFails(t *testing.T) {
	t.Parallel()
	client, closeServer := searchClient(t, []map[string]any{
		{"object": "page", "id": "11111111-1111-4111-8111-111111111111", "title": "Roadmap"},
		{"object": "page", "id": "22222222-2222-4222-8222-222222222222", "title": "Roadmap archive"},
	})
	defer closeServer()

	_, err := resolvePageArg(actions.Context{Context: context.Background()}, client, "Roadmap")
	if err == nil {
		t.Fatal("expected ambiguous title error")
	}
	if !strings.Contains(err.Error(), "multiple Notion pages match") || !strings.Contains(err.Error(), "Roadmap archive") {
		t.Fatalf("unexpected ambiguous title error: %v", err)
	}
}

func TestPageSelectionLabelUsesCompactIDs(t *testing.T) {
	t.Parallel()
	label := PageSelectionLabel(SearchResult{
		ID:    "35650aa7-8fda-80fe-aafa-f24e69aca7a6",
		Title: "May 2026",
		URL:   "https://www.notion.so/35650aa78fda80feaafaf24e69aca7a6",
	})
	if label != "May 2026  35650aa7" {
		t.Fatalf("unexpected selection label %q", label)
	}
}

func TestPageIDFromLinkRecognizesNotionURLs(t *testing.T) {
	t.Parallel()
	id, ok := PageIDFromLink("https://www.notion.so/Roadmap-11111111111141118111111111111111?pvs=4")
	if !ok {
		t.Fatal("expected Notion URL to be recognized")
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected Notion page id %q", id)
	}
	if _, ok := PageIDFromLink("https://example.com/Roadmap-11111111111141118111111111111111"); ok {
		t.Fatal("external URL with UUID should not be treated as a Notion page")
	}
	if _, ok := PageIDFromLink("mailto:11111111111141118111111111111111@example.com"); ok {
		t.Fatal("non-HTTP URL with UUID should not be treated as a Notion page")
	}
}

func TestLinkSelectionDetailCompactsTargets(t *testing.T) {
	t.Parallel()
	notionDetail := LinkSelectionDetail("https://www.notion.so/Roadmap-11111111111141118111111111111111?pvs=4")
	if notionDetail != "Notion 11111111" {
		t.Fatalf("unexpected Notion detail %q", notionDetail)
	}
	externalDetail := LinkSelectionDetail("https://example.com/docs/setup")
	if externalDetail != "example.com/docs/setup" {
		t.Fatalf("unexpected external detail %q", externalDetail)
	}
}

func TestPageLinksFromMarkdownClassifiesLinks(t *testing.T) {
	t.Parallel()
	links := pageLinksFromMarkdown(`[Roadmap](https://www.notion.so/Roadmap-11111111111141118111111111111111)
[External](https://example.com/docs)`)
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %#v", links)
	}
	if links[0].Kind != "notion" || links[0].NotionPageID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected Notion link classification: %#v", links[0])
	}
	if links[1].Kind != "external" || links[1].NotionPageID != "" {
		t.Fatalf("unexpected external link classification: %#v", links[1])
	}
}

func TestPageDiagnosticsWarnsOnMarkdownFidelity(t *testing.T) {
	t.Parallel()
	diagnostics := pageDiagnostics(PageRead{
		Page: Page{ID: "11111111-1111-4111-8111-111111111111"},
		Markdown: PageMarkdown{
			ID:              "11111111-1111-4111-8111-111111111111",
			Markdown:        "[Spec](https://example.com/spec)",
			Truncated:       true,
			UnknownBlockIDs: []string{"block-1"},
		},
	})
	var sawTruncated, sawUnknown bool
	for _, diagnostic := range diagnostics {
		if diagnostic.Check == "markdown-truncation" && diagnostic.Status == "warn" {
			sawTruncated = true
		}
		if diagnostic.Check == "unknown-blocks" && diagnostic.Status == "warn" {
			sawUnknown = true
		}
	}
	if !sawTruncated || !sawUnknown {
		t.Fatalf("expected markdown fidelity warnings, got %#v", diagnostics)
	}
}

func renderTable(table output.Table) string {
	var builder strings.Builder
	output.RenderTable(&builder, output.Options{}, table)
	return builder.String()
}

func searchClient(t *testing.T, results []map[string]any) (*Client, func()) {
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
	client := NewClient("token", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	return client, func() {
		server.Close()
	}
}
