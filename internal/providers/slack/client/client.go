package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://slack.com/api"

type Client struct {
	baseURL     string
	accessToken string
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
		httpClient:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

type Error struct {
	StatusCode int           `json:"status"`
	Code       string        `json:"error"`
	Needed     string        `json:"needed,omitempty"`
	Provided   string        `json:"provided,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	RawBody    string        `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Code != "" && e.StatusCode > 0 {
		return fmt.Sprintf("slack %s (%d)", e.Code, e.StatusCode)
	}
	if e.Code != "" {
		return "slack " + e.Code
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("slack request failed with status %d", e.StatusCode)
	}
	return "slack request failed"
}

func (e *Error) Temporary() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode == http.StatusServiceUnavailable || e.StatusCode == http.StatusGatewayTimeout || e.Code == "ratelimited"
}

type ListConversationsRequest struct {
	Types           string `json:"types,omitempty"`
	ExcludeArchived bool   `json:"exclude_archived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Cursor          string `json:"cursor,omitempty"`
	TeamID          string `json:"team_id,omitempty"`
}

type ListConversationsResponse struct {
	OK               bool                 `json:"ok"`
	Channels         []Conversation       `json:"channels"`
	ResponseMetadata ResponseMetadata     `json:"response_metadata,omitzero"`
	Needed           string               `json:"needed,omitempty"`
	Provided         string               `json:"provided,omitempty"`
	Raw              *json.RawMessage     `json:"raw,omitempty"`
	Warnings         []string             `json:"warnings,omitempty"`
	Metadata         map[string]RawString `json:"metadata,omitempty"`
}

type ResponseMetadata struct {
	NextCursor string   `json:"next_cursor,omitempty"`
	Messages   []string `json:"messages,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type RawString string

type Conversation struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	User       string `json:"user,omitempty"`
	IsChannel  bool   `json:"is_channel,omitempty"`
	IsGroup    bool   `json:"is_group,omitempty"`
	IsIM       bool   `json:"is_im,omitempty"`
	IsMPIM     bool   `json:"is_mpim,omitempty"`
	IsPrivate  bool   `json:"is_private,omitempty"`
	IsArchived bool   `json:"is_archived,omitempty"`
	IsMember   bool   `json:"is_member,omitempty"`
	NumMembers int    `json:"num_members,omitempty"`
	Creator    string `json:"creator,omitempty"`
	Topic      Topic  `json:"topic,omitzero"`
	Purpose    Topic  `json:"purpose,omitzero"`
}

type Topic struct {
	Value   string `json:"value,omitempty"`
	Creator string `json:"creator,omitempty"`
	LastSet int64  `json:"last_set,omitempty"`
}

func (c Conversation) DisplayName() string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	if c.IsIM && strings.TrimSpace(c.User) != "" {
		return c.User
	}
	return c.ID
}

func (c Conversation) Type() string {
	switch {
	case c.IsIM:
		return "im"
	case c.IsMPIM:
		return "mpim"
	case c.IsGroup:
		return "private_channel"
	case c.IsChannel:
		return "public_channel"
	default:
		return "conversation"
	}
}

type PostMessageRequest struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
	Mrkdwn   bool   `json:"mrkdwn"`
}

type PostMessageResponse struct {
	OK      bool    `json:"ok"`
	Channel string  `json:"channel"`
	TS      string  `json:"ts"`
	Message Message `json:"message,omitzero"`
	Warning string  `json:"warning,omitempty"`
}

type Message struct {
	Type     string `json:"type,omitempty"`
	Subtype  string `json:"subtype,omitempty"`
	User     string `json:"user,omitempty"`
	Username string `json:"username,omitempty"`
	Text     string `json:"text,omitempty"`
	TS       string `json:"ts,omitempty"`
	Team     string `json:"team,omitempty"`
}

type SearchMessagesRequest struct {
	Query     string `json:"query"`
	Count     int    `json:"count,omitempty"`
	Page      int    `json:"page,omitempty"`
	Sort      string `json:"sort,omitempty"`
	SortDir   string `json:"sort_dir,omitempty"`
	Highlight bool   `json:"highlight,omitempty"`
}

type SearchMessagesResponse struct {
	OK       bool          `json:"ok"`
	Query    string        `json:"query,omitempty"`
	Messages SearchMatches `json:"messages"`
}

type SearchMatches struct {
	Total      int               `json:"total,omitempty"`
	Pagination SearchPagination  `json:"pagination,omitzero"`
	Matches    []SearchMatch     `json:"matches"`
	Paging     *SearchPagination `json:"paging,omitempty"`
}

type SearchPagination struct {
	Page       int `json:"page,omitempty"`
	PageCount  int `json:"page_count,omitempty"`
	PerPage    int `json:"per_page,omitempty"`
	Count      int `json:"count,omitempty"`
	TotalCount int `json:"total_count,omitempty"`
	First      int `json:"first,omitempty"`
	Last       int `json:"last,omitempty"`
}

type SearchMatch struct {
	Type      string         `json:"type,omitempty"`
	Channel   SearchChannel  `json:"channel,omitzero"`
	User      string         `json:"user,omitempty"`
	Username  string         `json:"username,omitempty"`
	Text      string         `json:"text,omitempty"`
	TS        string         `json:"ts,omitempty"`
	Permalink string         `json:"permalink,omitempty"`
	Team      string         `json:"team,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
}

type SearchChannel struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	IsChannel bool   `json:"is_channel,omitempty"`
	IsGroup   bool   `json:"is_group,omitempty"`
	IsIM      bool   `json:"is_im,omitempty"`
}

func (c *Client) ListConversations(ctx context.Context, request ListConversationsRequest) (ListConversationsResponse, error) {
	query := make(url.Values)
	if strings.TrimSpace(request.Types) != "" {
		query.Set("types", strings.TrimSpace(request.Types))
	}
	if request.ExcludeArchived {
		query.Set("exclude_archived", "true")
	}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(min(request.Limit, 1000)))
	}
	if strings.TrimSpace(request.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(request.Cursor))
	}
	if strings.TrimSpace(request.TeamID) != "" {
		query.Set("team_id", strings.TrimSpace(request.TeamID))
	}
	var out ListConversationsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/conversations.list", query, nil, &out); err != nil {
		return ListConversationsResponse{}, err
	}
	return out, nil
}

func (c *Client) ListConversationsAll(ctx context.Context, request ListConversationsRequest, limit int) (ListConversationsResponse, error) {
	if limit <= 0 {
		limit = 100
	}
	var out ListConversationsResponse
	cursor := request.Cursor
	for len(out.Channels) < limit {
		request.Cursor = cursor
		request.Limit = min(limit-len(out.Channels), 200)
		page, err := c.ListConversations(ctx, request)
		if err != nil {
			return ListConversationsResponse{}, err
		}
		out.OK = page.OK
		out.Channels = append(out.Channels, page.Channels...)
		out.ResponseMetadata = page.ResponseMetadata
		out.Needed = page.Needed
		out.Provided = page.Provided
		if page.ResponseMetadata.NextCursor == "" {
			break
		}
		cursor = page.ResponseMetadata.NextCursor
	}
	if len(out.Channels) > limit {
		out.Channels = out.Channels[:limit]
	}
	return out, nil
}

func (c *Client) PostMessage(ctx context.Context, request PostMessageRequest) (PostMessageResponse, error) {
	request.Channel = strings.TrimSpace(request.Channel)
	request.Text = strings.TrimSpace(request.Text)
	if request.Channel == "" {
		return PostMessageResponse{}, fmt.Errorf("slack channel is required")
	}
	if request.Text == "" {
		return PostMessageResponse{}, fmt.Errorf("slack message text is required")
	}
	var out PostMessageResponse
	if err := c.doJSON(ctx, http.MethodPost, "/chat.postMessage", nil, request, &out); err != nil {
		return PostMessageResponse{}, err
	}
	return out, nil
}

func (c *Client) SearchMessages(ctx context.Context, request SearchMessagesRequest) (SearchMessagesResponse, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return SearchMessagesResponse{}, fmt.Errorf("slack search query is required")
	}
	query := make(url.Values)
	query.Set("query", request.Query)
	if request.Count > 0 {
		query.Set("count", strconv.Itoa(min(request.Count, 100)))
	}
	if request.Page > 0 {
		query.Set("page", strconv.Itoa(request.Page))
	}
	if strings.TrimSpace(request.Sort) != "" {
		query.Set("sort", normalizeSearchSort(request.Sort))
	}
	if strings.TrimSpace(request.SortDir) != "" {
		sortDir, err := normalizeSortDir(request.SortDir)
		if err != nil {
			return SearchMessagesResponse{}, err
		}
		query.Set("sort_dir", sortDir)
	}
	if request.Highlight {
		query.Set("highlight", "true")
	}
	var out SearchMessagesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/search.messages", query, nil, &out); err != nil {
		return SearchMessagesResponse{}, err
	}
	out.Query = request.Query
	return out, nil
}

func normalizeSearchSort(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "score":
		return "score"
	default:
		return "timestamp"
	}
}

func normalizeSortDir(value string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "desc", "descending":
		return "desc", nil
	case "asc", "ascending":
		return "asc", nil
	default:
		return "", fmt.Errorf("--direction must be asc or desc")
	}
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
	// #nosec G107 -- Slack client endpoints are configured by the CLI/test harness.
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
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
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeError(resp, data)
	}
	var envelope struct {
		OK       *bool  `json:"ok"`
		Error    string `json:"error"`
		Needed   string `json:"needed"`
		Provided string `json:"provided"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.OK != nil && !*envelope.OK {
		return apiError(resp, data, envelope.Error, envelope.Needed, envelope.Provided)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
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

func decodeError(resp *http.Response, data []byte) error {
	var decoded struct {
		Error    string `json:"error"`
		Needed   string `json:"needed"`
		Provided string `json:"provided"`
	}
	_ = json.Unmarshal(data, &decoded)
	return apiError(resp, data, decoded.Error, decoded.Needed, decoded.Provided)
}

func apiError(resp *http.Response, data []byte, code, needed, provided string) error {
	apiErr := &Error{
		StatusCode: resp.StatusCode,
		Code:       firstNonEmpty(code, strings.TrimSpace(string(data)), resp.Status),
		Needed:     needed,
		Provided:   provided,
		RawBody:    string(data),
	}
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			apiErr.RetryAfter = time.Duration(seconds) * time.Second
		}
	}
	return apiErr
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
