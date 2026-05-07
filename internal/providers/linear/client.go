package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const DefaultGraphQLEndpoint = "https://api.linear.app/graphql"

type Client struct {
	endpoint    string
	accessToken string
	httpClient  *http.Client
}

type Option func(*Client)

func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		c.endpoint = endpoint
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
		endpoint:    DefaultGraphQLEndpoint,
		accessToken: accessToken,
		httpClient:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

type Viewer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Team struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Issue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Team       Team   `json:"team"`
}

type Comment struct {
	ID      string `json:"id"`
	Body    string `json:"body"`
	URL     string `json:"url"`
	IssueID string `json:"issueId"`
}

type CreateIssueInput struct {
	TeamID      string `json:"teamId"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type CreateCommentInput struct {
	IssueID string `json:"issueId"`
	Body    string `json:"body"`
}

func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	var out struct {
		Viewer Viewer `json:"viewer"`
	}
	err := c.graphql(ctx, `query Viewer { viewer { id name email } }`, nil, &out)
	return out.Viewer, err
}

func (c *Client) ListIssues(ctx context.Context, first int) ([]Issue, error) {
	if first <= 0 {
		first = 25
	}
	var out struct {
		Issues struct {
			Nodes []Issue `json:"nodes"`
		} `json:"issues"`
	}
	err := c.graphql(ctx, `query Issues($first: Int!) {
		issues(first: $first) {
			nodes { id identifier title url team { id key name } }
		}
	}`, map[string]any{"first": first}, &out)
	return out.Issues.Nodes, err
}

func (c *Client) GetIssue(ctx context.Context, id string) (Issue, error) {
	var out struct {
		Issue Issue `json:"issue"`
	}
	err := c.graphql(ctx, `query Issue($id: String!) {
		issue(id: $id) { id identifier title url team { id key name } }
	}`, map[string]any{"id": id}, &out)
	return out.Issue, err
}

func (c *Client) CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error) {
	var out struct {
		IssueCreate struct {
			Success bool  `json:"success"`
			Issue   Issue `json:"issue"`
		} `json:"issueCreate"`
	}
	err := c.graphql(ctx, `mutation IssueCreate($input: IssueCreateInput!) {
		issueCreate(input: $input) {
			success
			issue { id identifier title url team { id key name } }
		}
	}`, map[string]any{"input": input}, &out)
	if err != nil {
		return Issue{}, err
	}
	if !out.IssueCreate.Success {
		return Issue{}, fmt.Errorf("linear issueCreate returned success=false")
	}
	return out.IssueCreate.Issue, nil
}

func (c *Client) CreateComment(ctx context.Context, input CreateCommentInput) (Comment, error) {
	var out struct {
		CommentCreate struct {
			Success bool    `json:"success"`
			Comment Comment `json:"comment"`
		} `json:"commentCreate"`
	}
	err := c.graphql(ctx, `mutation CommentCreate($input: CommentCreateInput!) {
		commentCreate(input: $input) {
			success
			comment { id body url issueId }
		}
	}`, map[string]any{"input": input}, &out)
	if err != nil {
		return Comment{}, err
	}
	if !out.CommentCreate.Success {
		return Comment{}, fmt.Errorf("linear commentCreate returned success=false")
	}
	return out.CommentCreate.Comment, nil
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors,omitempty"`
}

type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

func (e GraphQLError) Error() string {
	return e.Message
}

func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("linear graphql request failed: status %d", resp.StatusCode)
	}
	var decoded graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if len(decoded.Errors) > 0 {
		return decoded.Errors[0]
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(decoded.Data, out)
}
