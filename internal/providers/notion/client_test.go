package notion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeIDAcceptsURLsAndCompactIDs(t *testing.T) {
	tests := map[string]string{
		"11111111111141118111111111111111":                                     "11111111-1111-4111-8111-111111111111",
		"https://www.notion.so/Roadmap-11111111111141118111111111111111?pvs=4": "11111111-1111-4111-8111-111111111111",
		"11111111-1111-4111-8111-111111111111":                                 "11111111-1111-4111-8111-111111111111",
	}
	for input, want := range tests {
		got, err := NormalizeID(input)
		if err != nil {
			t.Fatalf("NormalizeID(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClientSendsNotionVersionAndBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization header mismatch: %q", got)
		}
		if got := r.Header.Get("Notion-Version"); got != "2026-03-11" {
			t.Fatalf("Notion-Version mismatch: %q", got)
		}
		if r.URL.Path != "/v1/pages/11111111-1111-4111-8111-111111111111/markdown" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"object":            "page_markdown",
			"id":                "11111111-1111-4111-8111-111111111111",
			"markdown":          "# Roadmap",
			"truncated":         false,
			"unknown_block_ids": []string{},
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL))
	page, err := client.RetrievePageMarkdown(context.Background(), "11111111111141118111111111111111", false)
	if err != nil {
		t.Fatal(err)
	}
	if page.Markdown != "# Roadmap" {
		t.Fatalf("unexpected markdown %q", page.Markdown)
	}
}

func TestClientListsBlockChildren(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/blocks/11111111-1111-4111-8111-111111111111/children" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("page_size"); got != "2" {
			t.Fatalf("page_size mismatch: %q", got)
		}
		writeJSON(t, w, map[string]any{
			"object": "list",
			"type":   "block",
			"results": []map[string]any{{
				"object":       "block",
				"id":           "22222222-2222-4222-8222-222222222222",
				"type":         "child_page",
				"has_children": true,
				"child_page": map[string]any{
					"title": "April 2026",
				},
			}},
			"has_more":    false,
			"next_cursor": nil,
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL))
	children, err := client.ListBlockChildren(context.Background(), "11111111111141118111111111111111", ListBlockChildrenRequest{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(children.Results) != 1 || children.Results[0].ChildPage.Title != "April 2026" {
		t.Fatalf("unexpected block children: %#v", children.Results)
	}
}

func TestClientRetrievesDataSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/data_sources/44444444-4444-4444-8444-444444444444" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"object": "data_source",
			"id":     "44444444-4444-4444-8444-444444444444",
			"name":   "Tasks",
			"properties": map[string]any{
				"Name": map[string]any{"id": "title", "type": "title"},
			},
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL))
	dataSource, err := client.RetrieveDataSource(context.Background(), "44444444444444448444444444444444")
	if err != nil {
		t.Fatal(err)
	}
	if dataSource.Name != "Tasks" || len(dataSource.Properties) != 1 {
		t.Fatalf("unexpected data source: %#v", dataSource)
	}
}

func TestClientMapsNotionErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(t, w, map[string]any{
			"object":  "error",
			"status":  429,
			"code":    "rate_limited",
			"message": "slow down",
			"additional_data": map[string]any{
				"retry_guidance": []any{"respect Retry-After"},
			},
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL))
	_, err := client.Search(context.Background(), SearchRequest{Query: "roadmap"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if apiErr.Code != "rate_limited" || !apiErr.Temporary() {
		t.Fatalf("unexpected API error: %#v", apiErr)
	}
	if !strings.Contains(apiErr.ExtraFields["retry_guidance"], "Retry-After") {
		t.Fatalf("missing retry guidance: %#v", apiErr.ExtraFields)
	}
}

func TestClientSearchAcceptsRichTextTitles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"object": "list",
			"results": []map[string]any{
				{
					"object": "data_source",
					"id":     "44444444-4444-4444-8444-444444444444",
					"title": []map[string]any{{
						"type":       "text",
						"plain_text": "Work",
						"text":       map[string]string{"content": "Work"},
					}},
				},
				{
					"object": "page",
					"id":     "11111111-1111-4111-8111-111111111111",
					"properties": map[string]any{
						"Name": map[string]any{
							"type": "title",
							"title": []map[string]any{{
								"type":       "text",
								"plain_text": "Roadmap",
								"text":       map[string]string{"content": "Roadmap"},
							}},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL))
	results, err := client.Search(context.Background(), SearchRequest{Query: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if got := results.Results[0].Title; got != "Work" {
		t.Fatalf("expected rich text title, got %q", got)
	}
	if got := results.Results[1].Title; got != "Roadmap" {
		t.Fatalf("expected property title, got %q", got)
	}
}

func TestClientSearchAllPaginatesAndSorts(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		results := []map[string]any{{
			"object": "page",
			"id":     "11111111-1111-4111-8111-111111111111",
		}}
		hasMore := len(requests) == 1
		var nextCursor any
		if hasMore {
			nextCursor = "cursor-2"
		}
		if len(requests) == 2 {
			results[0]["id"] = "22222222-2222-4222-8222-222222222222"
		}
		writeJSON(t, w, map[string]any{
			"object":      "list",
			"results":     results,
			"has_more":    hasMore,
			"next_cursor": nextCursor,
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL))
	results, err := client.SearchAll(context.Background(), SearchRequest{
		Query: "roadmap",
		Sort:  &SearchSort{Timestamp: "edited", Direction: "desc"},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) != 2 {
		t.Fatalf("expected 2 results, got %#v", results.Results)
	}
	sort, ok := requests[0]["sort"].(map[string]any)
	if !ok || sort["timestamp"] != "last_edited_time" || sort["direction"] != "descending" {
		t.Fatalf("unexpected sort request: %#v", requests[0])
	}
	if requests[1]["start_cursor"] != "cursor-2" {
		t.Fatalf("second request did not use cursor: %#v", requests[1])
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
