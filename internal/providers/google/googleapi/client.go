package googleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

const (
	DefaultAPIBaseURL = "https://www.googleapis.com"
	DefaultAuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	// #nosec G101 -- this is Google's public OAuth token endpoint, not a token.
	DefaultTokenURL  = "https://oauth2.googleapis.com/token"
	DefaultRevokeURL = "https://oauth2.googleapis.com/revoke"

	ScopeDocs            = "https://www.googleapis.com/auth/documents"
	ScopeDriveFile       = "https://www.googleapis.com/auth/drive.file"
	ScopeDriveMetadata   = "https://www.googleapis.com/auth/drive.metadata.readonly"
	googleDocsMIME       = "application/vnd.google-apps.document"
	defaultResponseLimit = 16 << 20
)

type Client struct {
	BaseURL     string
	AccessToken string
	HTTPClient  *http.Client
}

type OAuthOptions struct {
	AuthURL       string
	TokenURL      string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	Scopes        []string
	CodeChallenge string
	CodeVerifier  string
}

type OAuthTokenResponse struct {
	AccessToken      string `json:"access_token,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	TokenType        string `json:"token_type,omitempty"`
	Scope            string `json:"scope,omitempty"`
	ExpiresIn        int    `json:"expires_in,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type Document struct {
	DocumentID string       `json:"documentId,omitempty"`
	Title      string       `json:"title,omitempty"`
	RevisionID string       `json:"revisionId,omitempty"`
	Body       DocumentBody `json:"body,omitzero"`
}

type DocumentBody struct {
	Content []StructuralElement `json:"content,omitempty"`
}

type StructuralElement struct {
	StartIndex int        `json:"startIndex,omitempty"`
	EndIndex   int        `json:"endIndex,omitempty"`
	Paragraph  *Paragraph `json:"paragraph,omitempty"`
}

type Paragraph struct {
	Elements []ParagraphElement `json:"elements,omitempty"`
}

type ParagraphElement struct {
	StartIndex int      `json:"startIndex,omitempty"`
	EndIndex   int      `json:"endIndex,omitempty"`
	TextRun    *TextRun `json:"textRun,omitempty"`
}

type TextRun struct {
	Content string `json:"content,omitempty"`
}

type BatchUpdateDocumentRequest struct {
	Requests     []DocumentRequest `json:"requests"`
	WriteControl *WriteControl     `json:"writeControl,omitempty"`
}

type DocumentRequest struct {
	InsertText     *InsertTextRequest     `json:"insertText,omitempty"`
	DeleteContent  *DeleteContentRequest  `json:"deleteContentRange,omitempty"`
	ReplaceAllText *ReplaceAllTextRequest `json:"replaceAllText,omitempty"`
}

type InsertTextRequest struct {
	Text     string   `json:"text"`
	Location Location `json:"location"`
}

type DeleteContentRequest struct {
	Range Range `json:"range"`
}

type ReplaceAllTextRequest struct {
	ContainsText ContainsText `json:"containsText"`
	ReplaceText  string       `json:"replaceText"`
}

type ContainsText struct {
	Text      string `json:"text"`
	MatchCase bool   `json:"matchCase,omitempty"`
}

type Location struct {
	Index int `json:"index"`
}

type Range struct {
	StartIndex int `json:"startIndex"`
	EndIndex   int `json:"endIndex"`
}

type WriteControl struct {
	RequiredRevisionID string `json:"requiredRevisionId,omitempty"`
	TargetRevisionID   string `json:"targetRevisionId,omitempty"`
}

type BatchUpdateDocumentResponse struct {
	DocumentID      string           `json:"documentId,omitempty"`
	WriteControl    WriteControl     `json:"writeControl,omitzero"`
	Replies         []map[string]any `json:"replies,omitempty"`
	AppliedRequests int              `json:"applied_requests,omitempty"`
}

type DriveFile struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mimeType,omitempty"`
	WebViewLink  string `json:"webViewLink,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
}

type CopyDriveFileOptions struct {
	Name     string
	ParentID string
}

type DriveFilesResponse struct {
	Files         []DriveFile `json:"files,omitempty"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

func OAuthAuthorizeURL(options OAuthOptions, state string) (string, error) {
	authURL := firstNonEmpty(options.AuthURL, DefaultAuthURL)
	parsed, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("client_id", strings.TrimSpace(options.ClientID))
	query.Set("redirect_uri", strings.TrimSpace(options.RedirectURI))
	query.Set("response_type", "code")
	query.Set("state", state)
	query.Set("access_type", "offline")
	query.Set("include_granted_scopes", "true")
	query.Set("prompt", "consent")
	if challenge := strings.TrimSpace(options.CodeChallenge); challenge != "" {
		query.Set("code_challenge", challenge)
		query.Set("code_challenge_method", "S256")
	}
	if scopes := oauthbroker.CleanScopes(options.Scopes); len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func PickerAuthorizeURL(options OAuthOptions, state, mimeType string) (string, error) {
	authURL := firstNonEmpty(options.AuthURL, DefaultAuthURL)
	parsed, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("client_id", strings.TrimSpace(options.ClientID))
	query.Set("redirect_uri", strings.TrimSpace(options.RedirectURI))
	query.Set("response_type", "code")
	query.Set("state", state)
	query.Set("access_type", "offline")
	query.Set("prompt", "consent")
	query.Set("scope", ScopeDriveFile)
	query.Set("trigger_onepick", "true")
	if challenge := strings.TrimSpace(options.CodeChallenge); challenge != "" {
		query.Set("code_challenge", challenge)
		query.Set("code_challenge_method", "S256")
	}
	if mimeType = strings.TrimSpace(mimeType); mimeType != "" {
		query.Set("mimetypes", mimeType)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func ExchangeOAuthCode(ctx context.Context, client *http.Client, options OAuthOptions, code string, now time.Time) (credentials.OAuthTokens, error) {
	values := url.Values{}
	values.Set("code", strings.TrimSpace(code))
	values.Set("client_id", strings.TrimSpace(options.ClientID))
	if secret := strings.TrimSpace(options.ClientSecret); secret != "" {
		values.Set("client_secret", secret)
	}
	values.Set("redirect_uri", strings.TrimSpace(options.RedirectURI))
	values.Set("grant_type", "authorization_code")
	if verifier := strings.TrimSpace(options.CodeVerifier); verifier != "" {
		values.Set("code_verifier", verifier)
	}
	response, err := postOAuthToken(ctx, client, options, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	return response.Credentials(now)
}

func RefreshOAuthToken(ctx context.Context, client *http.Client, options OAuthOptions, refreshToken string, now time.Time) (credentials.OAuthTokens, error) {
	values := url.Values{}
	values.Set("client_id", strings.TrimSpace(options.ClientID))
	if secret := strings.TrimSpace(options.ClientSecret); secret != "" {
		values.Set("client_secret", secret)
	}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(refreshToken))
	response, err := postOAuthToken(ctx, client, options, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	tokens, err := response.Credentials(now)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = strings.TrimSpace(refreshToken)
	}
	return tokens, nil
}

func RevokeOAuthToken(ctx context.Context, client *http.Client, revokeURL, token string) error {
	revokeURL = firstNonEmpty(revokeURL, DefaultRevokeURL)
	values := url.Values{}
	values.Set("token", strings.TrimSpace(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("google revoke endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (response OAuthTokenResponse) Credentials(now time.Time) (credentials.OAuthTokens, error) {
	if response.Error != "" {
		return credentials.OAuthTokens{}, fmt.Errorf("google OAuth failed: %s", firstNonEmpty(response.ErrorDescription, response.Error))
	}
	accessToken := strings.TrimSpace(response.AccessToken)
	if accessToken == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("google OAuth response did not include an access token")
	}
	tokenType := firstNonEmpty(response.TokenType, "Bearer")
	tokens := credentials.OAuthTokens{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(response.RefreshToken),
		TokenType:    tokenType,
		Scopes:       oauthbroker.CleanScopes([]string{response.Scope}),
	}
	if response.ExpiresIn > 0 {
		tokens.ExpiresAt = now.UTC().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	return tokens, nil
}

func (c Client) GetDocument(ctx context.Context, documentID string) (Document, error) {
	var out Document
	if err := c.get(ctx, "/docs/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID)), nil, &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

func (c Client) CreateDocument(ctx context.Context, title string) (Document, error) {
	var out Document
	body := map[string]string{"title": strings.TrimSpace(title)}
	if err := c.postJSON(ctx, "/docs/v1/documents", body, &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

func (c Client) BatchUpdateDocument(ctx context.Context, documentID string, request BatchUpdateDocumentRequest) (BatchUpdateDocumentResponse, error) {
	var out BatchUpdateDocumentResponse
	if err := c.postJSON(ctx, "/docs/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID))+":batchUpdate", request, &out); err != nil {
		return BatchUpdateDocumentResponse{}, err
	}
	out.AppliedRequests = len(request.Requests)
	return out, nil
}

func (c Client) BatchUpdateDocumentRaw(ctx context.Context, documentID string, request map[string]any) (BatchUpdateDocumentResponse, error) {
	var out BatchUpdateDocumentResponse
	if err := c.postJSON(ctx, "/docs/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID))+":batchUpdate", request, &out); err != nil {
		return BatchUpdateDocumentResponse{}, err
	}
	if requests, ok := request["requests"].([]any); ok {
		out.AppliedRequests = len(requests)
	}
	return out, nil
}

func (c Client) ListDriveFiles(ctx context.Context, query string, pageSize int, pageToken string) (DriveFilesResponse, error) {
	values := url.Values{}
	if strings.TrimSpace(query) != "" {
		values.Set("q", strings.TrimSpace(query))
	}
	if pageSize > 0 {
		values.Set("pageSize", strconv.Itoa(pageSize))
	}
	if strings.TrimSpace(pageToken) != "" {
		values.Set("pageToken", strings.TrimSpace(pageToken))
	}
	values.Set("fields", "nextPageToken,files(id,name,mimeType,webViewLink,modifiedTime)")
	var out DriveFilesResponse
	if err := c.get(ctx, "/drive/v3/files", values, &out); err != nil {
		return DriveFilesResponse{}, err
	}
	return out, nil
}

func (c Client) GetDriveFile(ctx context.Context, fileID string) (DriveFile, error) {
	values := url.Values{}
	values.Set("fields", "id,name,mimeType,webViewLink,modifiedTime")
	var out DriveFile
	if err := c.get(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID)), values, &out); err != nil {
		return DriveFile{}, err
	}
	return out, nil
}

func (c Client) CopyDriveFile(ctx context.Context, fileID string, options CopyDriveFileOptions) (DriveFile, error) {
	values := url.Values{}
	values.Set("fields", "id,name,mimeType,webViewLink,modifiedTime")
	values.Set("supportsAllDrives", "true")
	body := map[string]any{}
	if name := strings.TrimSpace(options.Name); name != "" {
		body["name"] = name
	}
	if parentID := strings.TrimSpace(options.ParentID); parentID != "" {
		body["parents"] = []string{parentID}
	}
	var out DriveFile
	if err := c.postJSONQuery(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID))+"/copy", values, body, &out); err != nil {
		return DriveFile{}, err
	}
	return out, nil
}

func (document Document) PlainText() string {
	var builder strings.Builder
	for _, element := range document.Body.Content {
		if element.Paragraph == nil {
			continue
		}
		for _, child := range element.Paragraph.Elements {
			if child.TextRun != nil {
				builder.WriteString(child.TextRun.Content)
			}
		}
	}
	return builder.String()
}

func (document Document) AppendIndex() int {
	index := 1
	for _, element := range document.Body.Content {
		if element.EndIndex > index {
			index = element.EndIndex
		}
	}
	if index > 1 {
		return index - 1
	}
	return index
}

func GoogleDocsMIMEType() string {
	return googleDocsMIME
}

func postOAuthToken(ctx context.Context, client *http.Client, options OAuthOptions, values url.Values) (OAuthTokenResponse, error) {
	tokenURL := firstNonEmpty(options.TokenURL, DefaultTokenURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultResponseLimit))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OAuthTokenResponse{}, fmt.Errorf("google OAuth token endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out OAuthTokenResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return OAuthTokenResponse{}, err
	}
	if out.Error != "" {
		return OAuthTokenResponse{}, fmt.Errorf("google OAuth failed: %s", firstNonEmpty(out.ErrorDescription, out.Error))
	}
	return out, nil
}

func (c Client) get(ctx context.Context, suffix string, values url.Values, out any) error {
	reqURL, err := url.Parse(apiURL(c.BaseURL, suffix))
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	return doGoogle(req, c.HTTPClient, out)
}

func (c Client) postJSON(ctx context.Context, suffix string, body any, out any) error {
	return c.postJSONQuery(ctx, suffix, nil, body, out)
}

func (c Client) postJSONQuery(ctx context.Context, suffix string, values url.Values, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reqURL, err := url.Parse(apiURL(c.BaseURL, suffix))
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	return doGoogle(req, c.HTTPClient, out)
}

func (c Client) authorize(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.AccessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func doGoogle(req *http.Request, client *http.Client, out any) error {
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultResponseLimit))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("google API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("google API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return err
	}
	return nil
}

func apiURL(baseURL, suffix string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + "/" + strings.TrimLeft(suffix, "/")
	}
	parsed.Path = path.Join(parsed.Path, strings.TrimLeft(suffix, "/"))
	return parsed.String()
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
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
