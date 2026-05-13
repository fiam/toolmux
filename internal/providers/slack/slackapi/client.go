package slackapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
)

const (
	DefaultAPIBaseURL = "https://slack.com/api"
	DefaultAuthURL    = "https://slack.com/oauth/v2/authorize"
	// #nosec G101 -- this is Slack's OAuth token endpoint URL, not a token value.
	DefaultTokenURL  = "https://slack.com/api/oauth.v2.access"
	DefaultRevokeURL = "https://slack.com/api/auth.revoke"
)

type Client struct {
	BaseURL     string
	HTTPClient  *http.Client
	AccessToken string
	Cookie      string
}

type AuthTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	URL    string `json:"url,omitempty"`
	Team   string `json:"team,omitempty"`
	User   string `json:"user,omitempty"`
	TeamID string `json:"team_id,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

type ConversationsListResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error,omitempty"`
	Channels         []Conversation `json:"channels"`
	ResponseMetadata Metadata       `json:"response_metadata,omitzero"`
}

type ConversationsInfoResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Channel Conversation `json:"channel,omitzero"`
}

type ConversationsOpenResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Channel Conversation `json:"channel,omitzero"`
}

type ConversationMessagesResponse struct {
	OK               bool      `json:"ok"`
	Error            string    `json:"error,omitempty"`
	Messages         []Message `json:"messages"`
	HasMore          bool      `json:"has_more,omitempty"`
	ResponseMetadata Metadata  `json:"response_metadata,omitzero"`
}

type Conversation struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	User               string   `json:"user,omitempty"`
	IsChannel          bool     `json:"is_channel,omitempty"`
	IsGroup            bool     `json:"is_group,omitempty"`
	IsIM               bool     `json:"is_im,omitempty"`
	IsMPIM             bool     `json:"is_mpim,omitempty"`
	IsPrivate          bool     `json:"is_private,omitempty"`
	IsArchived         bool     `json:"is_archived,omitempty"`
	IsExtShared        bool     `json:"is_ext_shared,omitempty"`
	IsMuted            bool     `json:"is_muted,omitempty"`
	IsMember           bool     `json:"is_member,omitempty"`
	NumMembers         int      `json:"num_members,omitempty"`
	UnreadCount        int      `json:"unread_count,omitempty"`
	UnreadCountDisplay int      `json:"unread_count_display,omitempty"`
	Members            []string `json:"members,omitempty"`
}

type Metadata struct {
	NextCursor string `json:"next_cursor,omitempty"`
}

type ChatPostMessageResponse struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Channel string  `json:"channel,omitempty"`
	TS      string  `json:"ts,omitempty"`
	Message Message `json:"message,omitzero"`
}

type Message struct {
	Type        string     `json:"type,omitempty"`
	Subtype     string     `json:"subtype,omitempty"`
	User        string     `json:"user,omitempty"`
	Username    string     `json:"username,omitempty"`
	BotID       string     `json:"bot_id,omitempty"`
	Text        string     `json:"text,omitempty"`
	TS          string     `json:"ts,omitempty"`
	ThreadTS    string     `json:"thread_ts,omitempty"`
	ReplyCount  int        `json:"reply_count,omitempty"`
	LatestReply string     `json:"latest_reply,omitempty"`
	Files       []File     `json:"files,omitempty"`
	Reactions   []Reaction `json:"reactions,omitempty"`
}

type Reaction struct {
	Name  string   `json:"name,omitempty"`
	Count int      `json:"count,omitempty"`
	Users []string `json:"users,omitempty"`
}

type FileInfoResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	File  File   `json:"file,omitzero"`
}

type File struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	Title              string `json:"title,omitempty"`
	Mimetype           string `json:"mimetype,omitempty"`
	Filetype           string `json:"filetype,omitempty"`
	PrettyType         string `json:"pretty_type,omitempty"`
	URLPrivate         string `json:"url_private,omitempty"`
	URLPrivateDownload string `json:"url_private_download,omitempty"`
	Size               int    `json:"size,omitempty"`
}

type UsersListResponse struct {
	OK               bool     `json:"ok"`
	Error            string   `json:"error,omitempty"`
	Members          []User   `json:"members"`
	ResponseMetadata Metadata `json:"response_metadata,omitzero"`
}

type User struct {
	ID       string      `json:"id"`
	Name     string      `json:"name,omitempty"`
	RealName string      `json:"real_name,omitempty"`
	Deleted  bool        `json:"deleted,omitempty"`
	IsBot    bool        `json:"is_bot,omitempty"`
	Profile  UserProfile `json:"profile,omitzero"`
}

type UserProfile struct {
	DisplayName string `json:"display_name,omitempty"`
	RealName    string `json:"real_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Title       string `json:"title,omitempty"`
}

type UsergroupsListResponse struct {
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Usergroups []Usergroup `json:"usergroups"`
}

type UsergroupResponse struct {
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	Usergroup Usergroup `json:"usergroup,omitzero"`
}

type UsergroupUsersListResponse struct {
	OK    bool     `json:"ok"`
	Error string   `json:"error,omitempty"`
	Users []string `json:"users"`
}

type Usergroup struct {
	ID          string   `json:"id"`
	TeamID      string   `json:"team_id,omitempty"`
	Name        string   `json:"name,omitempty"`
	Handle      string   `json:"handle,omitempty"`
	Description string   `json:"description,omitempty"`
	UserCount   int      `json:"user_count,omitempty"`
	IsDisabled  bool     `json:"is_disabled,omitempty"`
	IsExternal  bool     `json:"is_external,omitempty"`
	Prefs       Prefs    `json:"prefs,omitzero"`
	Users       []string `json:"users,omitempty"`
}

type Prefs struct {
	Channels []string `json:"channels,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

type OKResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type SearchMessagesResponse struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Query    string        `json:"query,omitempty"`
	Messages SearchResults `json:"messages"`
}

type SearchResults struct {
	Total      int           `json:"total,omitempty"`
	Pagination Pagination    `json:"pagination,omitzero"`
	Paging     Paging        `json:"paging,omitzero"`
	Matches    []SearchMatch `json:"matches"`
}

type Pagination struct {
	TotalCount int `json:"total_count,omitempty"`
	Page       int `json:"page,omitempty"`
	PageCount  int `json:"page_count,omitempty"`
	PerPage    int `json:"per_page,omitempty"`
}

type Paging struct {
	Total int `json:"total,omitempty"`
	Page  int `json:"page,omitempty"`
	Pages int `json:"pages,omitempty"`
	Count int `json:"count,omitempty"`
}

type SearchMatch struct {
	Channel   SearchChannel `json:"channel"`
	User      string        `json:"user,omitempty"`
	Username  string        `json:"username,omitempty"`
	Text      string        `json:"text,omitempty"`
	TS        string        `json:"ts,omitempty"`
	Permalink string        `json:"permalink,omitempty"`
	Type      string        `json:"type,omitempty"`
}

type SearchChannel struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type OAuthOptions struct {
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string
	UserScopes   []string
	TokenSource  string
}

type OAuthTokenResponse struct {
	OK           bool              `json:"ok"`
	Error        string            `json:"error,omitempty"`
	AccessToken  string            `json:"access_token,omitempty"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	TokenType    string            `json:"token_type,omitempty"`
	Scope        string            `json:"scope,omitempty"`
	ExpiresIn    int               `json:"expires_in,omitempty"`
	BotUserID    string            `json:"bot_user_id,omitempty"`
	AppID        string            `json:"app_id,omitempty"`
	Team         OAuthEntity       `json:"team,omitzero"`
	Enterprise   OAuthEntity       `json:"enterprise,omitzero"`
	AuthedUser   OAuthAuthedUser   `json:"authed_user,omitzero"`
	Extra        map[string]string `json:"-"`
}

type OAuthEntity struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type OAuthAuthedUser struct {
	ID           string `json:"id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

type RevokeResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Revoked bool   `json:"revoked,omitempty"`
}

func (c Client) AuthTest(ctx context.Context) (AuthTestResponse, error) {
	var out AuthTestResponse
	if err := c.postForm(ctx, "auth.test", nil, &out); err != nil {
		return AuthTestResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsList(ctx context.Context, values url.Values) (ConversationsListResponse, error) {
	var out ConversationsListResponse
	if err := c.get(ctx, "conversations.list", values, &out); err != nil {
		return ConversationsListResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsInfo(ctx context.Context, values url.Values) (ConversationsInfoResponse, error) {
	var out ConversationsInfoResponse
	if err := c.get(ctx, "conversations.info", values, &out); err != nil {
		return ConversationsInfoResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsOpen(ctx context.Context, values url.Values) (ConversationsOpenResponse, error) {
	var out ConversationsOpenResponse
	if err := c.postForm(ctx, "conversations.open", values, &out); err != nil {
		return ConversationsOpenResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsHistory(ctx context.Context, values url.Values) (ConversationMessagesResponse, error) {
	var out ConversationMessagesResponse
	if err := c.get(ctx, "conversations.history", values, &out); err != nil {
		return ConversationMessagesResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsReplies(ctx context.Context, values url.Values) (ConversationMessagesResponse, error) {
	var out ConversationMessagesResponse
	if err := c.get(ctx, "conversations.replies", values, &out); err != nil {
		return ConversationMessagesResponse{}, err
	}
	return out, nil
}

func (c Client) ChatPostMessage(ctx context.Context, channel, text, threadTS string) (ChatPostMessageResponse, error) {
	values := url.Values{}
	values.Set("channel", channel)
	values.Set("text", text)
	if strings.TrimSpace(threadTS) != "" {
		values.Set("thread_ts", strings.TrimSpace(threadTS))
	}
	var out ChatPostMessageResponse
	if err := c.postForm(ctx, "chat.postMessage", values, &out); err != nil {
		return ChatPostMessageResponse{}, err
	}
	return out, nil
}

func (c Client) PostOK(ctx context.Context, method string, values url.Values) (OKResponse, error) {
	var out OKResponse
	if err := c.postForm(ctx, method, values, &out); err != nil {
		return OKResponse{}, err
	}
	return out, nil
}

func (c Client) FileInfo(ctx context.Context, fileID string) (FileInfoResponse, error) {
	values := url.Values{}
	values.Set("file", strings.TrimSpace(fileID))
	var out FileInfoResponse
	if err := c.get(ctx, "files.info", values, &out); err != nil {
		return FileInfoResponse{}, err
	}
	return out, nil
}

func (c Client) UsersList(ctx context.Context, values url.Values) (UsersListResponse, error) {
	var out UsersListResponse
	if err := c.get(ctx, "users.list", values, &out); err != nil {
		return UsersListResponse{}, err
	}
	return out, nil
}

func (c Client) UsergroupsList(ctx context.Context, values url.Values) (UsergroupsListResponse, error) {
	var out UsergroupsListResponse
	if err := c.get(ctx, "usergroups.list", values, &out); err != nil {
		return UsergroupsListResponse{}, err
	}
	return out, nil
}

func (c Client) UsergroupsUsersList(ctx context.Context, values url.Values) (UsergroupUsersListResponse, error) {
	var out UsergroupUsersListResponse
	if err := c.get(ctx, "usergroups.users.list", values, &out); err != nil {
		return UsergroupUsersListResponse{}, err
	}
	return out, nil
}

func (c Client) PostUsergroup(ctx context.Context, method string, values url.Values) (UsergroupResponse, error) {
	var out UsergroupResponse
	if err := c.postForm(ctx, method, values, &out); err != nil {
		return UsergroupResponse{}, err
	}
	return out, nil
}

func (c Client) SearchMessages(ctx context.Context, values url.Values) (SearchMessagesResponse, error) {
	var out SearchMessagesResponse
	if err := c.get(ctx, "search.messages", values, &out); err != nil {
		return SearchMessagesResponse{}, err
	}
	return out, nil
}

func (c Client) Download(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := httpClient(c.HTTPClient).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("slack API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("slack file download returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("slack file exceeds maximum download size of %d bytes", maxBytes)
	}
	return body, nil
}

func OAuthAuthorizeURL(options OAuthOptions, state string) (string, error) {
	authURL := strings.TrimSpace(options.AuthURL)
	if authURL == "" {
		authURL = DefaultAuthURL
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("client_id", strings.TrimSpace(options.ClientID))
	query.Set("redirect_uri", strings.TrimSpace(options.RedirectURI))
	query.Set("state", state)
	if scopes := CleanScopes(options.Scopes); len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, ","))
	}
	if scopes := CleanScopes(options.UserScopes); len(scopes) > 0 {
		query.Set("user_scope", strings.Join(scopes, ","))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func ExchangeOAuthCode(ctx context.Context, client *http.Client, options OAuthOptions, code string, now time.Time) (credentials.OAuthTokens, error) {
	values := url.Values{}
	values.Set("code", strings.TrimSpace(code))
	values.Set("redirect_uri", strings.TrimSpace(options.RedirectURI))
	values.Set("grant_type", "authorization_code")
	response, err := postOAuthToken(ctx, client, options, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	return response.Credentials(options, now)
}

func RefreshOAuthToken(ctx context.Context, client *http.Client, options OAuthOptions, refreshToken string, now time.Time) (credentials.OAuthTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(refreshToken))
	response, err := postOAuthToken(ctx, client, options, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	return response.Credentials(options, now)
}

func RevokeOAuthToken(ctx context.Context, client *http.Client, revokeURL, token string) (RevokeResponse, error) {
	revokeURL = strings.TrimSpace(revokeURL)
	if revokeURL == "" {
		revokeURL = DefaultRevokeURL
	}
	values := url.Values{}
	values.Set("token", strings.TrimSpace(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(values.Encode()))
	if err != nil {
		return RevokeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return RevokeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return RevokeResponse{}, fmt.Errorf("slack revoke endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out RevokeResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return RevokeResponse{}, err
	}
	if !out.OK {
		return RevokeResponse{}, fmt.Errorf("slack revoke failed: %s", firstNonEmpty(out.Error, "unknown_error"))
	}
	return out, nil
}

func (response OAuthTokenResponse) Credentials(options OAuthOptions, now time.Time) (credentials.OAuthTokens, error) {
	source := strings.ToLower(strings.TrimSpace(options.TokenSource))
	accessToken := strings.TrimSpace(response.AccessToken)
	refreshToken := strings.TrimSpace(response.RefreshToken)
	tokenType := strings.TrimSpace(response.TokenType)
	expiresIn := response.ExpiresIn
	scopes := SplitScopes(response.Scope)
	if source == "user" || (source == "" || source == "auto") && accessToken == "" && response.AuthedUser.AccessToken != "" {
		accessToken = strings.TrimSpace(response.AuthedUser.AccessToken)
		refreshToken = firstNonEmpty(response.AuthedUser.RefreshToken, refreshToken)
		tokenType = firstNonEmpty(response.AuthedUser.TokenType, tokenType)
		expiresIn = firstNonZero(response.AuthedUser.ExpiresIn, expiresIn)
		scopes = SplitScopes(firstNonEmpty(response.AuthedUser.Scope, response.Scope))
	}
	if accessToken == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("slack OAuth response did not include an access token")
	}
	if tokenType == "" {
		tokenType = "Bearer"
	}
	extra := map[string]string{}
	if response.Team.ID != "" {
		extra["team_id"] = response.Team.ID
	}
	if response.Team.Name != "" {
		extra["team_name"] = response.Team.Name
	}
	if response.Enterprise.ID != "" {
		extra["enterprise_id"] = response.Enterprise.ID
	}
	if response.Enterprise.Name != "" {
		extra["enterprise_name"] = response.Enterprise.Name
	}
	if response.BotUserID != "" {
		extra["bot_user_id"] = response.BotUserID
	}
	if response.AppID != "" {
		extra["app_id"] = response.AppID
	}
	if response.AuthedUser.ID != "" {
		extra["authed_user_id"] = response.AuthedUser.ID
	}
	for key, value := range response.Extra {
		if strings.TrimSpace(value) != "" {
			extra[key] = value
		}
	}
	tokens := credentials.OAuthTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		Scopes:       scopes,
		Extra:        extra,
	}
	if expiresIn > 0 {
		tokens.ExpiresAt = now.UTC().Add(time.Duration(expiresIn) * time.Second)
	}
	return tokens, nil
}

func CleanScopes(values []string) []string {
	seen := map[string]bool{}
	var scopes []string
	for _, value := range values {
		for part := range strings.FieldsSeq(strings.ReplaceAll(value, ",", " ")) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			scopes = append(scopes, part)
		}
	}
	return scopes
}

func SplitScopes(value string) []string {
	return CleanScopes([]string{value})
}

func APIURLFromBase(baseURL, method string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + "/" + method
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + method
	return parsed.String()
}

func APIBaseURLFromTeamURL(teamURL string) string {
	teamURL = strings.TrimSpace(teamURL)
	if teamURL == "" {
		return ""
	}
	parsed, err := url.Parse(teamURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = path.Join(strings.TrimRight(parsed.Path, "/"), "api")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func OAuthURLFromAPIBase(baseURL, suffix string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	if prefix, ok := strings.CutSuffix(strings.TrimRight(parsed.Path, "/"), "/api"); ok {
		parsed.Path = prefix
	}
	parsed.Path = path.Join(parsed.Path, suffix)
	return parsed.String()
}

func (c Client) get(ctx context.Context, method string, values url.Values, out any) error {
	endpoint := APIURLFromBase(c.BaseURL, method)
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, val := range vals {
			query.Add(key, val)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	return doSlack(req, c.HTTPClient, out)
}

func (c Client) postForm(ctx context.Context, method string, values url.Values, out any) error {
	if values == nil {
		values = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, APIURLFromBase(c.BaseURL, method), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.authorize(req)
	return doSlack(req, c.HTTPClient, out)
}

func (c Client) authorize(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.AccessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie := strings.TrimSpace(c.Cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
}

func doSlack(req *http.Request, client *http.Client, out any) error {
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("slack API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("slack API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return err
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("slack API failed: %s", firstNonEmpty(envelope.Error, "unknown_error"))
	}
	return nil
}

func postOAuthToken(ctx context.Context, client *http.Client, options OAuthOptions, values url.Values) (OAuthTokenResponse, error) {
	tokenURL := strings.TrimSpace(options.TokenURL)
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	clientID := strings.TrimSpace(options.ClientID)
	clientSecret := strings.TrimSpace(options.ClientSecret)
	if clientID != "" || clientSecret != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OAuthTokenResponse{}, fmt.Errorf("slack OAuth token endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out OAuthTokenResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return OAuthTokenResponse{}, err
	}
	if !out.OK {
		return OAuthTokenResponse{}, fmt.Errorf("slack OAuth failed: %s", firstNonEmpty(out.Error, "unknown_error"))
	}
	return out, nil
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
