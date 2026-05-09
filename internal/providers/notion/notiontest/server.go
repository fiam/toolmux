package notiontest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type Server struct {
	*httptest.Server
	mu          sync.Mutex
	notionPages map[string]notionPage
	requests    []string
}

func NewUpstream() *Server {
	mux := http.NewServeMux()
	server := &Server{
		notionPages: map[string]notionPage{
			NotionPageID: {
				ID:       NotionPageID,
				Title:    "Roadmap",
				Markdown: "# Roadmap\n\nInitial content\n\n[Spec](https://example.com/spec)",
				URL:      "https://notion.so/Roadmap-" + strings.ReplaceAll(NotionPageID, "-", ""),
				Parent:   map[string]any{"type": "page_id", "page_id": NotionParentPageID},
			},
			NotionChildPageID: {
				ID:       NotionChildPageID,
				Title:    "April 2026",
				Markdown: "# April 2026\n\nMonthly notes",
				URL:      "https://notion.so/April-2026-" + strings.ReplaceAll(NotionChildPageID, "-", ""),
				Parent:   map[string]any{"type": "page_id", "page_id": NotionPageID},
			},
			NotionNestedPageID: {
				ID:       NotionNestedPageID,
				Title:    "Week 1",
				Markdown: "# Week 1\n\nNested notes",
				URL:      "https://notion.so/Week-1-" + strings.ReplaceAll(NotionNestedPageID, "-", ""),
				Parent:   map[string]any{"type": "page_id", "page_id": NotionChildPageID},
			},
		},
	}

	mux.HandleFunc("GET /oauth/authorize", server.authorize)
	mux.HandleFunc("POST /oauth/token", server.token)
	mux.HandleFunc("POST /oauth/revoke", status(http.StatusOK))
	mux.HandleFunc("POST /notion/v1/search", server.notionSearch)
	mux.HandleFunc("POST /notion/v1/pages", server.notionCreatePage)
	mux.HandleFunc("GET /notion/v1/pages/{page_id}", server.notionGetPage)
	mux.HandleFunc("PATCH /notion/v1/pages/{page_id}", server.notionUpdatePage)
	mux.HandleFunc("GET /notion/v1/pages/{page_id}/markdown", server.notionGetMarkdown)
	mux.HandleFunc("PATCH /notion/v1/pages/{page_id}/markdown", server.notionUpdateMarkdown)
	mux.HandleFunc("POST /notion/v1/pages/{page_id}/move", server.notionMovePage)
	mux.HandleFunc("GET /notion/v1/blocks/{block_id}/children", server.notionListBlockChildren)
	mux.HandleFunc("GET /notion/v1/data_sources/{data_source_id}", server.notionGetDataSource)
	mux.HandleFunc("POST /notion/v1/data_sources/{data_source_id}/query", server.notionQueryDataSource)
	mux.HandleFunc("GET /notion/v1/databases/{database_id}", server.notionGetDatabase)

	server.Server = httptest.NewServer(mux)
	return server
}

const (
	NotionPageID       = "11111111-1111-4111-8111-111111111111"
	NotionCreatedID    = "22222222-2222-4222-8222-222222222222"
	NotionParentPageID = "33333333-3333-4333-8333-333333333333"
	NotionDataSourceID = "44444444-4444-4444-8444-444444444444"
	NotionDatabaseID   = "55555555-5555-4555-8555-555555555555"
	NotionChildPageID  = "66666666-6666-4666-8666-666666666666"
	NotionNestedPageID = "77777777-7777-4777-8777-777777777777"
)

type notionPage struct {
	ID       string
	Title    string
	Markdown string
	URL      string
	InTrash  bool
	Parent   map[string]any
}

func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	if redirectURI == "" || state == "" {
		http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	values := target.Query()
	values.Set("code", "fake-auth-code")
	values.Set("state", state)
	target.RawQuery = values.Encode()
	// #nosec G710 -- fake OAuth upstream intentionally redirects to the
	// caller-provided test callback URI after parsing it.
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			http.Error(w, "invalid token JSON", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		for key := range r.Form {
			values[key] = r.Form.Get(key)
		}
	}
	grantType := values["grant_type"]
	if grantType == "" {
		http.Error(w, "missing grant_type", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":   "fake-access-token",
		"refresh_token":  "fake-refresh-token",
		"expires_in":     3600,
		"token_type":     "bearer",
		"scope":          "read issues:create comments:create",
		"bot_id":         "notion-bot-1",
		"workspace_id":   "notion-workspace-1",
		"workspace_name": "Toolmux Test Workspace",
	})
}

func (s *Server) notionSearch(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	var request struct {
		Query       string            `json:"query"`
		PageSize    int               `json:"page_size"`
		StartCursor string            `json:"start_cursor"`
		Sort        map[string]string `json:"sort"`
		Filter      struct {
			Property string `json:"property"`
			Value    string `json:"value"`
		} `json:"filter"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	results := []map[string]any{}
	if request.Filter.Value == "" || request.Filter.Value == "page" {
		page := s.page(NotionPageID)
		results = append(results, page.response())
	}
	if request.Filter.Value == "" || request.Filter.Value == "data_source" {
		results = append(results, map[string]any{
			"object": "data_source",
			"id":     NotionDataSourceID,
			"title": []map[string]any{{
				"type":       "text",
				"plain_text": "Tasks",
				"text":       map[string]string{"content": "Tasks"},
			}},
			"url": "https://notion.so/database/" + strings.ReplaceAll(NotionDataSourceID, "-", ""),
			"properties": map[string]any{
				"Name": map[string]any{"type": "title", "title": map[string]any{}},
			},
		})
	}
	start, end, nextCursor, hasMore := paginate(len(results), request.StartCursor, request.PageSize)
	writeJSON(w, http.StatusOK, map[string]any{
		"object":      "list",
		"type":        "page_or_data_source",
		"results":     results[start:end],
		"has_more":    hasMore,
		"next_cursor": nextCursor,
	})
}

func (s *Server) notionGetPage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	page, ok := s.pageOK(r.PathValue("page_id"))
	if !ok {
		notionError(w, http.StatusNotFound, "object_not_found", "page has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, page.response())
}

func (s *Server) notionGetMarkdown(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	page, ok := s.pageOK(r.PathValue("page_id"))
	if !ok {
		notionError(w, http.StatusNotFound, "object_not_found", "page has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object":            "page_markdown",
		"id":                page.ID,
		"markdown":          page.Markdown,
		"truncated":         false,
		"unknown_block_ids": []string{},
	})
}

func (s *Server) notionCreatePage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	var request struct {
		Parent     map[string]any `json:"parent"`
		Markdown   string         `json:"markdown"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		notionError(w, http.StatusBadRequest, "invalid_json", "invalid page create body")
		return
	}
	title := titleFromProperties(request.Properties)
	if title == "" {
		title = "Created Page"
	}
	page := notionPage{
		ID:       NotionCreatedID,
		Title:    title,
		Markdown: request.Markdown,
		URL:      "https://notion.so/Created-" + strings.ReplaceAll(NotionCreatedID, "-", ""),
		Parent:   request.Parent,
	}
	s.mu.Lock()
	s.notionPages[page.ID] = page
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, page.response())
}

func (s *Server) notionUpdatePage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	var request struct {
		InTrash    *bool          `json:"in_trash"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		notionError(w, http.StatusBadRequest, "invalid_json", "invalid page update body")
		return
	}
	id := r.PathValue("page_id")
	s.mu.Lock()
	page, ok := s.notionPages[id]
	if ok {
		if request.InTrash != nil {
			page.InTrash = *request.InTrash
		}
		if title := titleFromProperties(request.Properties); title != "" {
			page.Title = title
		}
		s.notionPages[id] = page
	}
	s.mu.Unlock()
	if !ok {
		notionError(w, http.StatusNotFound, "object_not_found", "page has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, page.response())
}

func (s *Server) notionUpdateMarkdown(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	var request struct {
		Type          string `json:"type"`
		InsertContent struct {
			Content string `json:"content"`
		} `json:"insert_content"`
		ReplaceContent struct {
			NewString string `json:"new_str"`
		} `json:"replace_content"`
		UpdateContent struct {
			ContentUpdates []struct {
				OldString string `json:"old_str"`
				NewString string `json:"new_str"`
			} `json:"content_updates"`
		} `json:"update_content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		notionError(w, http.StatusBadRequest, "invalid_json", "invalid markdown update body")
		return
	}
	id := r.PathValue("page_id")
	s.mu.Lock()
	page, ok := s.notionPages[id]
	if ok {
		switch request.Type {
		case "insert_content":
			if page.Markdown != "" {
				page.Markdown += "\n\n"
			}
			page.Markdown += request.InsertContent.Content
		case "replace_content":
			page.Markdown = request.ReplaceContent.NewString
		case "update_content":
			for _, update := range request.UpdateContent.ContentUpdates {
				page.Markdown = strings.Replace(page.Markdown, update.OldString, update.NewString, 1)
			}
		}
		s.notionPages[id] = page
	}
	s.mu.Unlock()
	if !ok {
		notionError(w, http.StatusNotFound, "object_not_found", "page has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object":            "page_markdown",
		"id":                page.ID,
		"markdown":          page.Markdown,
		"truncated":         false,
		"unknown_block_ids": []string{},
	})
}

func (s *Server) notionMovePage(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	var request struct {
		Parent map[string]any `json:"parent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		notionError(w, http.StatusBadRequest, "invalid_json", "invalid move body")
		return
	}
	id := r.PathValue("page_id")
	s.mu.Lock()
	page, ok := s.notionPages[id]
	if ok {
		page.Parent = request.Parent
		s.notionPages[id] = page
	}
	s.mu.Unlock()
	if !ok {
		notionError(w, http.StatusNotFound, "object_not_found", "page has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, page.response())
}

func (s *Server) notionListBlockChildren(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	children := s.blockChildren(r.PathValue("block_id"))
	pageSize := 100
	if value := r.URL.Query().Get("page_size"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}
	start, end, nextCursor, hasMore := paginate(len(children), r.URL.Query().Get("start_cursor"), pageSize)
	results := make([]map[string]any, 0, end-start)
	for _, child := range children[start:end] {
		results = append(results, child.childPageBlock(s.hasBlockChildren(child.ID)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object":      "list",
		"type":        "block",
		"results":     results,
		"has_more":    hasMore,
		"next_cursor": nextCursor,
	})
}

func (s *Server) notionGetDataSource(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	if r.PathValue("data_source_id") != NotionDataSourceID {
		notionError(w, http.StatusNotFound, "object_not_found", "data source has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, dataSourceResponse())
}

func (s *Server) notionQueryDataSource(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	if r.PathValue("data_source_id") != NotionDataSourceID {
		notionError(w, http.StatusNotFound, "object_not_found", "data source has not been shared with the connection")
		return
	}
	page := s.page(NotionPageID)
	var request struct {
		PageSize    int    `json:"page_size"`
		StartCursor string `json:"start_cursor"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	results := []map[string]any{page.response()}
	start, end, nextCursor, hasMore := paginate(len(results), request.StartCursor, request.PageSize)
	writeJSON(w, http.StatusOK, map[string]any{
		"object":      "list",
		"type":        "page",
		"results":     results[start:end],
		"has_more":    hasMore,
		"next_cursor": nextCursor,
	})
}

func (s *Server) notionGetDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.requireNotion(w, r) {
		return
	}
	if r.PathValue("database_id") != NotionDatabaseID {
		notionError(w, http.StatusNotFound, "object_not_found", "database has not been shared with the connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "database",
		"id":     NotionDatabaseID,
		"title":  []map[string]any{{"type": "text", "plain_text": "Tasks"}},
		"data_sources": []map[string]string{
			{"id": NotionDataSourceID, "name": "Tasks"},
		},
	})
}

func (s *Server) requireNotion(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	if r.Header.Get("Notion-Version") == "" {
		notionError(w, http.StatusBadRequest, "missing_version", "Notion-Version header should be defined")
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		notionError(w, http.StatusUnauthorized, "unauthorized", "API token is invalid")
		return false
	}
	return true
}

func (s *Server) page(id string) notionPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notionPages[id]
}

func (s *Server) pageOK(id string) (notionPage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page, ok := s.notionPages[id]
	return page, ok
}

func (s *Server) blockChildren(blockID string) []notionPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch blockID {
	case NotionPageID:
		return []notionPage{s.notionPages[NotionChildPageID]}
	case NotionChildPageID:
		return []notionPage{s.notionPages[NotionNestedPageID]}
	default:
		return nil
	}
}

func (s *Server) hasBlockChildren(blockID string) bool {
	return len(s.blockChildren(blockID)) > 0
}

func (p notionPage) response() map[string]any {
	return map[string]any{
		"object":           "page",
		"id":               p.ID,
		"created_time":     "2026-05-07T10:00:00.000Z",
		"last_edited_time": "2026-05-07T10:00:00.000Z",
		"url":              p.URL,
		"in_trash":         p.InTrash,
		"parent":           p.Parent,
		"properties": map[string]any{
			"Name": map[string]any{
				"id":   "title",
				"type": "title",
				"title": []map[string]any{{
					"type":       "text",
					"plain_text": p.Title,
					"text":       map[string]string{"content": p.Title},
				}},
			},
		},
	}
}

func (p notionPage) childPageBlock(hasChildren bool) map[string]any {
	return map[string]any{
		"object":           "block",
		"id":               p.ID,
		"created_time":     "2026-05-07T10:00:00.000Z",
		"last_edited_time": "2026-05-07T10:00:00.000Z",
		"type":             "child_page",
		"has_children":     hasChildren,
		"in_trash":         p.InTrash,
		"child_page": map[string]any{
			"title": p.Title,
		},
	}
}

func paginate(total int, cursor string, pageSize int) (int, int, any, bool) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 100 {
		pageSize = 100
	}
	start := 0
	if cursor != "" {
		if parsed, err := strconv.Atoi(cursor); err == nil && parsed >= 0 {
			start = parsed
		}
	}
	if start > total {
		start = total
	}
	end := min(start+pageSize, total)
	hasMore := end < total
	var nextCursor any
	if hasMore {
		nextCursor = strconv.Itoa(end)
	}
	return start, end, nextCursor, hasMore
}

func dataSourceResponse() map[string]any {
	return map[string]any{
		"object": "data_source",
		"id":     NotionDataSourceID,
		"name":   "Tasks",
		"url":    "https://notion.so/database/" + strings.ReplaceAll(NotionDataSourceID, "-", ""),
		"properties": map[string]any{
			"Name": map[string]any{
				"id":   "title",
				"type": "title",
				"name": "Name",
			},
			"Done": map[string]any{
				"id":   "done",
				"type": "checkbox",
				"name": "Done",
			},
		},
	}
}

func titleFromProperties(properties map[string]any) string {
	for _, raw := range properties {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		values, ok := prop["title"].([]any)
		if !ok {
			continue
		}
		var parts []string
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if plain, ok := item["plain_text"].(string); ok && plain != "" {
				parts = append(parts, plain)
				continue
			}
			text, ok := item["text"].(map[string]any)
			if !ok {
				continue
			}
			if content, ok := text["content"].(string); ok {
				parts = append(parts, content)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "")
		}
	}
	return ""
}

func notionError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"object":  "error",
		"status":  status,
		"code":    code,
		"message": message,
	})
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
