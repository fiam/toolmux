package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/supacli/internal/providers/notion"
)

const (
	DefaultBaseURL = "https://api.notion.com"
	DefaultVersion = notion.DefaultVersion
)

type Client struct {
	baseURL     string
	accessToken string
	version     string
	httpClient  *http.Client
}

type Option func(*Client)

func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		if strings.TrimSpace(baseURL) != "" {
			c.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithVersion(version string) Option {
	return func(c *Client) {
		if strings.TrimSpace(version) != "" {
			c.version = strings.TrimSpace(version)
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func NewClient(accessToken string, opts ...Option) *Client {
	client := &Client{
		baseURL:     DefaultBaseURL,
		accessToken: strings.TrimSpace(accessToken),
		version:     DefaultVersion,
		httpClient:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

type Error struct {
	StatusCode  int               `json:"status"`
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	RequestID   string            `json:"request_id,omitempty"`
	RetryAfter  time.Duration     `json:"retry_after,omitempty"`
	RawBody     string            `json:"-"`
	ExtraFields map[string]string `json:"extra_fields,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("notion %s (%d): %s", e.Code, e.StatusCode, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("notion request failed (%d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("notion request failed with status %d", e.StatusCode)
}

func (e *Error) Temporary() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode == http.StatusServiceUnavailable || e.StatusCode == http.StatusGatewayTimeout
}

type Parent struct {
	Type         string `json:"type,omitempty"`
	PageID       string `json:"page_id,omitempty"`
	DataSourceID string `json:"data_source_id,omitempty"`
	Workspace    bool   `json:"workspace,omitempty"`
}

func PageParent(id string) (Parent, error) {
	normalized, err := NormalizeID(id)
	if err != nil {
		return Parent{}, err
	}
	return Parent{Type: "page_id", PageID: normalized}, nil
}

func DataSourceParent(id string) (Parent, error) {
	normalized, err := NormalizeID(id)
	if err != nil {
		return Parent{}, err
	}
	return Parent{Type: "data_source_id", DataSourceID: normalized}, nil
}

func WorkspaceParent() Parent {
	return Parent{Type: "workspace", Workspace: true}
}

type RichText struct {
	Type      string    `json:"type,omitempty"`
	Text      *Text     `json:"text,omitempty"`
	PlainText string    `json:"plain_text,omitempty"`
	Href      string    `json:"href,omitempty"`
	Title     *struct{} `json:"title,omitempty"`
}

type Text struct {
	Content string `json:"content"`
	Link    any    `json:"link,omitempty"`
}

type Page struct {
	Object         string                     `json:"object"`
	ID             string                     `json:"id"`
	CreatedTime    string                     `json:"created_time,omitempty"`
	LastEditedTime string                     `json:"last_edited_time,omitempty"`
	URL            string                     `json:"url,omitempty"`
	PublicURL      string                     `json:"public_url,omitempty"`
	InTrash        bool                       `json:"in_trash,omitempty"`
	Parent         map[string]any             `json:"parent,omitempty"`
	Properties     map[string]json.RawMessage `json:"properties,omitempty"`
}

func (p Page) Title() string {
	for _, raw := range p.Properties {
		var prop struct {
			Type  string     `json:"type"`
			Title []RichText `json:"title"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil || prop.Type != "title" {
			continue
		}
		return richTextPlainText(prop.Title)
	}
	return ""
}

type PageMarkdown struct {
	Object          string   `json:"object"`
	ID              string   `json:"id"`
	Markdown        string   `json:"markdown"`
	Truncated       bool     `json:"truncated"`
	UnknownBlockIDs []string `json:"unknown_block_ids"`
}

type SearchRequest struct {
	Query       string      `json:"query,omitempty"`
	ObjectType  string      `json:"-"`
	StartCursor string      `json:"start_cursor,omitempty"`
	PageSize    int         `json:"page_size,omitempty"`
	Sort        *SearchSort `json:"sort,omitempty"`
}

type SearchSort struct {
	Direction string `json:"direction"`
	Timestamp string `json:"timestamp"`
}

type SearchResponse struct {
	Object     string           `json:"object"`
	Type       string           `json:"type,omitempty"`
	Results    []SearchResult   `json:"results"`
	NextCursor string           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
	Status     *RequestStatus   `json:"request_status,omitempty"`
	Raw        *json.RawMessage `json:"raw,omitempty"`
}

type SearchResult struct {
	Object         string                     `json:"object"`
	ID             string                     `json:"id"`
	CreatedTime    string                     `json:"created_time,omitempty"`
	LastEditedTime string                     `json:"last_edited_time,omitempty"`
	URL            string                     `json:"url,omitempty"`
	PublicURL      string                     `json:"public_url,omitempty"`
	InTrash        bool                       `json:"in_trash,omitempty"`
	Properties     map[string]json.RawMessage `json:"properties,omitempty"`
	Title          string                     `json:"title,omitempty"`
}

func (r *SearchResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Object         string                     `json:"object"`
		ID             string                     `json:"id"`
		CreatedTime    string                     `json:"created_time,omitempty"`
		LastEditedTime string                     `json:"last_edited_time,omitempty"`
		URL            string                     `json:"url,omitempty"`
		PublicURL      string                     `json:"public_url,omitempty"`
		InTrash        bool                       `json:"in_trash,omitempty"`
		Properties     map[string]json.RawMessage `json:"properties,omitempty"`
		Title          json.RawMessage            `json:"title,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = SearchResult{
		Object:         wire.Object,
		ID:             wire.ID,
		CreatedTime:    wire.CreatedTime,
		LastEditedTime: wire.LastEditedTime,
		URL:            wire.URL,
		PublicURL:      wire.PublicURL,
		InTrash:        wire.InTrash,
		Properties:     wire.Properties,
		Title:          titleFromRaw(wire.Title),
	}
	if r.Title == "" {
		r.Title = titleFromProperties(r.Properties)
	}
	return nil
}

type RequestStatus struct {
	Type             string `json:"type"`
	IncompleteReason string `json:"incomplete_reason,omitempty"`
}

type CreatePageRequest struct {
	Parent        Parent          `json:"parent,omitzero"`
	Title         string          `json:"-"`
	TitleProperty string          `json:"-"`
	Markdown      string          `json:"markdown,omitempty"`
	Properties    json.RawMessage `json:"-"`
}

type UpdatePageRequest struct {
	Title         string          `json:"-"`
	TitleProperty string          `json:"-"`
	Properties    json.RawMessage `json:"-"`
	InTrash       *bool           `json:"in_trash,omitempty"`
}

type InsertMarkdownRequest struct {
	Content string `json:"content"`
	After   string `json:"after,omitempty"`
}

type ReplaceMarkdownRequest struct {
	NewString            string `json:"new_str"`
	AllowDeletingContent bool   `json:"allow_deleting_content,omitempty"`
}

type ContentUpdate struct {
	OldString         string `json:"old_str"`
	NewString         string `json:"new_str"`
	ReplaceAllMatches bool   `json:"replace_all_matches,omitempty"`
}

type UpdateMarkdownRequest struct {
	ContentUpdates       []ContentUpdate `json:"content_updates"`
	AllowDeletingContent bool            `json:"allow_deleting_content,omitempty"`
}

type QueryDataSourceRequest struct {
	StartCursor      string          `json:"start_cursor,omitempty"`
	PageSize         int             `json:"page_size,omitempty"`
	FilterProperties []string        `json:"-"`
	Filter           json.RawMessage `json:"filter,omitempty"`
	Sorts            json.RawMessage `json:"sorts,omitempty"`
	ResultType       string          `json:"result_type,omitempty"`
	InTrash          *bool           `json:"in_trash,omitempty"`
}

type QueryDataSourceResponse struct {
	Object     string         `json:"object"`
	Type       string         `json:"type,omitempty"`
	Results    []Page         `json:"results"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
	Status     *RequestStatus `json:"request_status,omitempty"`
}

type ListBlockChildrenRequest struct {
	StartCursor string `json:"start_cursor,omitempty"`
	PageSize    int    `json:"page_size,omitempty"`
}

type ListBlockChildrenResponse struct {
	Object     string         `json:"object"`
	Type       string         `json:"type,omitempty"`
	Results    []Block        `json:"results"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
	Status     *RequestStatus `json:"request_status,omitempty"`
}

type Block struct {
	Object         string           `json:"object"`
	ID             string           `json:"id"`
	CreatedTime    string           `json:"created_time,omitempty"`
	LastEditedTime string           `json:"last_edited_time,omitempty"`
	HasChildren    bool             `json:"has_children,omitempty"`
	InTrash        bool             `json:"in_trash,omitempty"`
	Type           string           `json:"type"`
	ChildPage      *ChildPage       `json:"child_page,omitempty"`
	ChildDatabase  *ChildPage       `json:"child_database,omitempty"`
	Raw            *json.RawMessage `json:"raw,omitempty"`
}

type ChildPage struct {
	Title string `json:"title,omitempty"`
}

type Database struct {
	Object      string       `json:"object"`
	ID          string       `json:"id"`
	Title       []RichText   `json:"title,omitempty"`
	DataSources []DataSource `json:"data_sources,omitempty"`
}

type DataSource struct {
	Object         string                     `json:"object,omitempty"`
	ID             string                     `json:"id"`
	Name           string                     `json:"name,omitempty"`
	CreatedTime    string                     `json:"created_time,omitempty"`
	LastEditedTime string                     `json:"last_edited_time,omitempty"`
	URL            string                     `json:"url,omitempty"`
	Properties     map[string]json.RawMessage `json:"properties,omitempty"`
}

func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if request.PageSize <= 0 {
		request.PageSize = 20
	}
	if request.PageSize > 100 {
		request.PageSize = 100
	}
	body := map[string]any{
		"page_size": request.PageSize,
	}
	if request.Query != "" {
		body["query"] = request.Query
	}
	if request.StartCursor != "" {
		body["start_cursor"] = request.StartCursor
	}
	if request.Sort != nil {
		sort, err := normalizeSearchSort(*request.Sort)
		if err != nil {
			return SearchResponse{}, err
		}
		body["sort"] = sort
	}
	switch request.ObjectType {
	case "page", "data_source":
		body["filter"] = map[string]string{"property": "object", "value": request.ObjectType}
	case "", "all":
	default:
		return SearchResponse{}, fmt.Errorf("unsupported Notion search type %q", request.ObjectType)
	}
	var out SearchResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/search", nil, body, &out); err != nil {
		return SearchResponse{}, err
	}
	for i := range out.Results {
		if out.Results[i].Title == "" {
			out.Results[i].Title = titleFromProperties(out.Results[i].Properties)
		}
	}
	return out, nil
}

func (c *Client) SearchAll(ctx context.Context, request SearchRequest, limit int) (SearchResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	var out SearchResponse
	cursor := request.StartCursor
	for len(out.Results) < limit {
		pageSize := min(limit-len(out.Results), 100)
		request.StartCursor = cursor
		request.PageSize = pageSize
		page, err := c.Search(ctx, request)
		if err != nil {
			return SearchResponse{}, err
		}
		if out.Object == "" {
			out.Object = page.Object
			out.Type = page.Type
		}
		out.Results = append(out.Results, page.Results...)
		out.NextCursor = page.NextCursor
		out.HasMore = page.HasMore
		out.Status = page.Status
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(out.Results) > limit {
		out.Results = out.Results[:limit]
	}
	return out, nil
}

func normalizeSearchSort(sort SearchSort) (SearchSort, error) {
	direction := strings.TrimSpace(strings.ToLower(sort.Direction))
	switch direction {
	case "", "desc", "descending":
		direction = "descending"
	case "asc", "ascending":
		direction = "ascending"
	default:
		return SearchSort{}, fmt.Errorf("unsupported Notion search sort direction %q", sort.Direction)
	}
	timestamp := strings.TrimSpace(strings.ToLower(strings.ReplaceAll(sort.Timestamp, "-", "_")))
	switch timestamp {
	case "", "edited", "last_edited", "last_edited_time":
		timestamp = "last_edited_time"
	default:
		return SearchSort{}, fmt.Errorf("unsupported Notion search sort %q; only last_edited_time is supported", sort.Timestamp)
	}
	return SearchSort{Direction: direction, Timestamp: timestamp}, nil
}

func (c *Client) RetrievePage(ctx context.Context, pageID string, filterProperties []string) (Page, error) {
	id, err := NormalizeID(pageID)
	if err != nil {
		return Page{}, err
	}
	query := make(url.Values)
	for _, property := range filterProperties {
		if strings.TrimSpace(property) != "" {
			query.Add("filter_properties", strings.TrimSpace(property))
		}
	}
	var out Page
	if err := c.doJSON(ctx, http.MethodGet, "/v1/pages/"+url.PathEscape(id), query, nil, &out); err != nil {
		return Page{}, err
	}
	return out, nil
}

func (c *Client) RetrievePageMarkdown(ctx context.Context, pageID string, includeTranscript bool) (PageMarkdown, error) {
	id, err := NormalizeID(pageID)
	if err != nil {
		return PageMarkdown{}, err
	}
	query := make(url.Values)
	if includeTranscript {
		query.Set("include_transcript", "true")
	}
	var out PageMarkdown
	if err := c.doJSON(ctx, http.MethodGet, "/v1/pages/"+url.PathEscape(id)+"/markdown", query, nil, &out); err != nil {
		return PageMarkdown{}, err
	}
	return out, nil
}

func (c *Client) CreatePage(ctx context.Context, request CreatePageRequest) (Page, error) {
	body := map[string]any{}
	if request.Parent.Type != "" || request.Parent.PageID != "" || request.Parent.DataSourceID != "" || request.Parent.Workspace {
		body["parent"] = request.Parent
	}
	if len(request.Properties) > 0 {
		var properties map[string]any
		if err := json.Unmarshal(request.Properties, &properties); err != nil {
			return Page{}, fmt.Errorf("decode properties JSON: %w", err)
		}
		body["properties"] = properties
	} else if request.Title != "" {
		body["properties"] = titleProperties(request.TitleProperty, request.Title)
	}
	if request.Markdown != "" {
		body["markdown"] = request.Markdown
	}
	var out Page
	if err := c.doJSON(ctx, http.MethodPost, "/v1/pages", nil, body, &out); err != nil {
		return Page{}, err
	}
	return out, nil
}

func (c *Client) UpdatePage(ctx context.Context, pageID string, request UpdatePageRequest) (Page, error) {
	id, err := NormalizeID(pageID)
	if err != nil {
		return Page{}, err
	}
	body := map[string]any{}
	if len(request.Properties) > 0 {
		var properties map[string]any
		if err := json.Unmarshal(request.Properties, &properties); err != nil {
			return Page{}, fmt.Errorf("decode properties JSON: %w", err)
		}
		body["properties"] = properties
	} else if request.Title != "" {
		body["properties"] = titleProperties(request.TitleProperty, request.Title)
	}
	if request.InTrash != nil {
		body["in_trash"] = *request.InTrash
	}
	if len(body) == 0 {
		return Page{}, fmt.Errorf("no Notion page updates were provided")
	}
	var out Page
	if err := c.doJSON(ctx, http.MethodPatch, "/v1/pages/"+url.PathEscape(id), nil, body, &out); err != nil {
		return Page{}, err
	}
	return out, nil
}

func (c *Client) InsertMarkdown(ctx context.Context, pageID string, request InsertMarkdownRequest) (PageMarkdown, error) {
	if strings.TrimSpace(request.Content) == "" {
		return PageMarkdown{}, fmt.Errorf("markdown content is required")
	}
	return c.updateMarkdown(ctx, pageID, map[string]any{
		"type":           "insert_content",
		"insert_content": request,
	})
}

func (c *Client) ReplaceMarkdown(ctx context.Context, pageID string, request ReplaceMarkdownRequest) (PageMarkdown, error) {
	if strings.TrimSpace(request.NewString) == "" {
		return PageMarkdown{}, fmt.Errorf("replacement markdown is required")
	}
	return c.updateMarkdown(ctx, pageID, map[string]any{
		"type":            "replace_content",
		"replace_content": request,
	})
}

func (c *Client) UpdateMarkdown(ctx context.Context, pageID string, request UpdateMarkdownRequest) (PageMarkdown, error) {
	if len(request.ContentUpdates) == 0 {
		return PageMarkdown{}, fmt.Errorf("at least one content update is required")
	}
	for _, update := range request.ContentUpdates {
		if update.OldString == "" {
			return PageMarkdown{}, fmt.Errorf("content update old string is required")
		}
	}
	return c.updateMarkdown(ctx, pageID, map[string]any{
		"type":           "update_content",
		"update_content": request,
	})
}

func (c *Client) MovePage(ctx context.Context, pageID string, parent Parent) (Page, error) {
	id, err := NormalizeID(pageID)
	if err != nil {
		return Page{}, err
	}
	if parent.Type == "" {
		return Page{}, fmt.Errorf("new parent is required")
	}
	var out Page
	body := map[string]any{"parent": parent}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/pages/"+url.PathEscape(id)+"/move", nil, body, &out); err != nil {
		return Page{}, err
	}
	return out, nil
}

func (c *Client) QueryDataSource(ctx context.Context, dataSourceID string, request QueryDataSourceRequest) (QueryDataSourceResponse, error) {
	id, err := NormalizeID(dataSourceID)
	if err != nil {
		return QueryDataSourceResponse{}, err
	}
	if request.PageSize <= 0 {
		request.PageSize = 50
	}
	if request.PageSize > 100 {
		request.PageSize = 100
	}
	query := make(url.Values)
	for _, property := range request.FilterProperties {
		if strings.TrimSpace(property) != "" {
			query.Add("filter_properties", strings.TrimSpace(property))
		}
	}
	body := map[string]any{"page_size": request.PageSize}
	if request.StartCursor != "" {
		body["start_cursor"] = request.StartCursor
	}
	if len(request.Filter) > 0 {
		var filter any
		if err := json.Unmarshal(request.Filter, &filter); err != nil {
			return QueryDataSourceResponse{}, fmt.Errorf("decode filter JSON: %w", err)
		}
		body["filter"] = filter
	}
	if len(request.Sorts) > 0 {
		var sorts any
		if err := json.Unmarshal(request.Sorts, &sorts); err != nil {
			return QueryDataSourceResponse{}, fmt.Errorf("decode sorts JSON: %w", err)
		}
		body["sorts"] = sorts
	}
	if request.ResultType != "" {
		body["result_type"] = request.ResultType
	}
	if request.InTrash != nil {
		body["in_trash"] = *request.InTrash
	}
	var out QueryDataSourceResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/data_sources/"+url.PathEscape(id)+"/query", query, body, &out); err != nil {
		return QueryDataSourceResponse{}, err
	}
	return out, nil
}

func (c *Client) QueryDataSourceAll(ctx context.Context, dataSourceID string, request QueryDataSourceRequest, limit int) (QueryDataSourceResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	var out QueryDataSourceResponse
	cursor := request.StartCursor
	for len(out.Results) < limit {
		pageSize := min(limit-len(out.Results), 100)
		request.StartCursor = cursor
		request.PageSize = pageSize
		page, err := c.QueryDataSource(ctx, dataSourceID, request)
		if err != nil {
			return QueryDataSourceResponse{}, err
		}
		if out.Object == "" {
			out.Object = page.Object
			out.Type = page.Type
		}
		out.Results = append(out.Results, page.Results...)
		out.NextCursor = page.NextCursor
		out.HasMore = page.HasMore
		out.Status = page.Status
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(out.Results) > limit {
		out.Results = out.Results[:limit]
	}
	return out, nil
}

func (c *Client) ListBlockChildren(ctx context.Context, blockID string, request ListBlockChildrenRequest) (ListBlockChildrenResponse, error) {
	id, err := NormalizeID(blockID)
	if err != nil {
		return ListBlockChildrenResponse{}, err
	}
	if request.PageSize <= 0 {
		request.PageSize = 100
	}
	if request.PageSize > 100 {
		request.PageSize = 100
	}
	query := make(url.Values)
	query.Set("page_size", strconv.Itoa(request.PageSize))
	if request.StartCursor != "" {
		query.Set("start_cursor", request.StartCursor)
	}
	var out ListBlockChildrenResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/blocks/"+url.PathEscape(id)+"/children", query, nil, &out); err != nil {
		return ListBlockChildrenResponse{}, err
	}
	return out, nil
}

func (c *Client) RetrieveDataSource(ctx context.Context, dataSourceID string) (DataSource, error) {
	id, err := NormalizeID(dataSourceID)
	if err != nil {
		return DataSource{}, err
	}
	var out DataSource
	if err := c.doJSON(ctx, http.MethodGet, "/v1/data_sources/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return DataSource{}, err
	}
	return out, nil
}

func (c *Client) RetrieveDatabase(ctx context.Context, databaseID string) (Database, error) {
	id, err := NormalizeID(databaseID)
	if err != nil {
		return Database{}, err
	}
	var out Database
	if err := c.doJSON(ctx, http.MethodGet, "/v1/databases/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return Database{}, err
	}
	return out, nil
}

func (c *Client) updateMarkdown(ctx context.Context, pageID string, body map[string]any) (PageMarkdown, error) {
	id, err := NormalizeID(pageID)
	if err != nil {
		return PageMarkdown{}, err
	}
	var out PageMarkdown
	if err := c.doJSON(ctx, http.MethodPatch, "/v1/pages/"+url.PathEscape(id)+"/markdown", nil, body, &out); err != nil {
		return PageMarkdown{}, err
	}
	return out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint, err := c.endpoint(path, query)
	if err != nil {
		return err
	}
	// #nosec G107 -- Notion client endpoints are configured by the CLI/test harness.
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Notion-Version", c.version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) endpoint(path string, query url.Values) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	if len(query) > 0 {
		base.RawQuery = query.Encode()
	}
	return base.String(), nil
}

func decodeError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	apiErr := &Error{
		StatusCode: resp.StatusCode,
		RawBody:    string(data),
	}
	if requestID := resp.Header.Get("X-Request-Id"); requestID != "" {
		apiErr.RequestID = requestID
	}
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			apiErr.RetryAfter = time.Duration(seconds) * time.Second
		}
	}
	var decoded struct {
		Code           string         `json:"code"`
		Message        string         `json:"message"`
		RequestID      string         `json:"request_id"`
		AdditionalData map[string]any `json:"additional_data"`
	}
	if err := json.Unmarshal(data, &decoded); err == nil {
		apiErr.Code = decoded.Code
		apiErr.Message = decoded.Message
		if decoded.RequestID != "" {
			apiErr.RequestID = decoded.RequestID
		}
		if len(decoded.AdditionalData) > 0 {
			apiErr.ExtraFields = flattenAdditionalData(decoded.AdditionalData)
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(data))
	}
	if apiErr.Message == "" {
		apiErr.Message = resp.Status
	}
	return apiErr
}

func flattenAdditionalData(data map[string]any) map[string]string {
	out := make(map[string]string, len(data))
	for key, value := range data {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case []any:
			parts := make([]string, 0, len(typed))
			for _, item := range typed {
				parts = append(parts, fmt.Sprint(item))
			}
			out[key] = strings.Join(parts, "; ")
		default:
			out[key] = fmt.Sprint(value)
		}
	}
	return out
}

func titleProperties(titleProperty, title string) map[string]any {
	name := strings.TrimSpace(titleProperty)
	if name == "" {
		name = "title"
	}
	return map[string]any{
		name: map[string]any{
			"title": []map[string]any{
				{"text": map[string]string{"content": title}},
			},
		},
	}
}

func titleFromProperties(properties map[string]json.RawMessage) string {
	return Page{Properties: properties}.Title()
}

func titleFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var title string
	if err := json.Unmarshal(raw, &title); err == nil {
		return title
	}
	var richText []RichText
	if err := json.Unmarshal(raw, &richText); err == nil {
		return richTextPlainText(richText)
	}
	var object struct {
		PlainText string     `json:"plain_text"`
		Text      *Text      `json:"text"`
		Title     []RichText `json:"title"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	if object.PlainText != "" {
		return object.PlainText
	}
	if object.Text != nil && object.Text.Content != "" {
		return object.Text.Content
	}
	return richTextPlainText(object.Title)
}

func richTextPlainText(values []RichText) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value.PlainText != "" {
			parts = append(parts, value.PlainText)
			continue
		}
		if value.Text != nil && value.Text.Content != "" {
			parts = append(parts, value.Text.Content)
		}
	}
	return strings.Join(parts, "")
}

func IsNotFound(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
