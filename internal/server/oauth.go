package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/notion"
)

type Config struct {
	PublicBaseURL    string
	NotionClientID   string
	NotionSecret     string
	NotionAuthURL    string
	NotionTokenURL   string
	NotionRevokeURL  string
	NotionRedirect   string
	NotionAPIVersion string
	HTTPClient       *http.Client
	SessionTTL       time.Duration
}

func ConfigFromEnv() Config {
	return Config{
		PublicBaseURL:    strings.TrimRight(os.Getenv("TOOLMUX_PUBLIC_URL"), "/"),
		NotionClientID:   os.Getenv("NOTION_CLIENT_ID"),
		NotionSecret:     os.Getenv("NOTION_CLIENT_SECRET"),
		NotionAuthURL:    firstNonEmpty(os.Getenv("NOTION_AUTH_URL"), "https://api.notion.com/v1/oauth/authorize"),
		NotionTokenURL:   firstNonEmpty(os.Getenv("NOTION_TOKEN_URL"), "https://api.notion.com/v1/oauth/token"),
		NotionRevokeURL:  firstNonEmpty(os.Getenv("NOTION_REVOKE_URL"), "https://api.notion.com/v1/oauth/revoke"),
		NotionRedirect:   os.Getenv("NOTION_REDIRECT_URI"),
		NotionAPIVersion: firstNonEmpty(os.Getenv("TOOLMUX_NOTION_VERSION"), notion.DefaultVersion),
		HTTPClient:       http.DefaultClient,
		SessionTTL:       120 * time.Second,
	}
}

type oauthBroker struct {
	config   Config
	mu       sync.Mutex
	sessions map[string]*oauthSession
}

type oauthSession struct {
	ID          string
	Provider    string
	State       string
	RedirectURI string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Status      string
	Error       string
	Tokens      credentials.OAuthTokens
	Delivered   bool
}

type createSessionRequest struct {
	Provider string `json:"provider"`
	Profile  string `json:"profile,omitempty"`
	Account  string `json:"account,omitempty"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type revokeRequest struct {
	Token string `json:"token"`
}

type notionTokenRequest struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type createSessionResponse struct {
	SessionID   string    `json:"session_id"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	AuthURL     string    `json:"auth_url"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type sessionResponse struct {
	SessionID string                   `json:"session_id"`
	Provider  string                   `json:"provider"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
	Extra     map[string]string        `json:"extra,omitempty"`
}

type healthResponse struct {
	Status string `json:"status"`
}

type errorResponse struct {
	Object  string `json:"object"`
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type revokeResponse struct {
	RequestID string `json:"request_id,omitempty"`
	Revoked   bool   `json:"revoked,omitempty"`
}

func registerOAuthHandlers(mux *http.ServeMux, config Config) {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 120 * time.Second
	}
	if config.NotionAPIVersion == "" {
		config.NotionAPIVersion = notion.DefaultVersion
	}
	broker := &oauthBroker{
		config:   config,
		sessions: make(map[string]*oauthSession),
	}
	mux.HandleFunc("POST /v1/oauth/sessions", broker.createSession)
	mux.HandleFunc("GET /v1/oauth/sessions/{session_id}", broker.getSession)
	mux.HandleFunc("GET /oauth/notion/start", broker.startNotion)
	mux.HandleFunc("GET /oauth/notion/callback", broker.notionCallback)
	mux.HandleFunc("POST /v1/oauth/notion/refresh", broker.refreshNotion)
	mux.HandleFunc("POST /v1/oauth/notion/revoke", broker.revokeNotion)
}

func (b *oauthBroker) createSession(w http.ResponseWriter, r *http.Request) {
	var request createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid session request")
		return
	}
	if request.Provider != "notion" {
		writeError(w, http.StatusBadRequest, "unsupported_provider", "only notion OAuth sessions are supported")
		return
	}
	if err := b.requireNotionConfig(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
		return
	}
	id, err := randomHex(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	state, err := randomHex(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	session := &oauthSession{
		ID:          id,
		Provider:    "notion",
		State:       state,
		RedirectURI: b.redirectURI(r),
		CreatedAt:   now,
		ExpiresAt:   now.Add(b.config.SessionTTL),
		Status:      "pending",
	}
	b.mu.Lock()
	b.sessions[id] = session
	b.mu.Unlock()

	writeJSON(w, http.StatusCreated, createSessionResponse{
		SessionID:   id,
		Provider:    "notion",
		Status:      session.Status,
		AuthURL:     b.publicURL(r) + "/oauth/notion/start?session_id=" + url.QueryEscape(id),
		RedirectURI: session.RedirectURI,
		ExpiresAt:   session.ExpiresAt,
	})
}

func (b *oauthBroker) getSession(w http.ResponseWriter, r *http.Request) {
	session, ok := b.session(r.PathValue("session_id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth session not found")
		return
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		writeJSON(w, http.StatusOK, sessionResponse{
			SessionID: session.ID,
			Provider:  session.Provider,
			Status:    "expired",
			ExpiresAt: session.ExpiresAt,
		})
		return
	}
	response := sessionResponse{
		SessionID: session.ID,
		Provider:  session.Provider,
		Status:    session.Status,
		Error:     session.Error,
		ExpiresAt: session.ExpiresAt,
	}
	b.mu.Lock()
	if session.Status == "complete" && !session.Delivered {
		tokens := session.Tokens
		response.Tokens = &tokens
		response.Extra = tokens.Extra
		session.Delivered = true
	}
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, response)
}

func (b *oauthBroker) startNotion(w http.ResponseWriter, r *http.Request) {
	session, ok := b.session(r.URL.Query().Get("session_id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth session not found")
		return
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		writeError(w, http.StatusGone, "expired", "OAuth session expired")
		return
	}
	authURL, err := url.Parse(b.config.NotionAuthURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_config", "invalid Notion authorization URL")
		return
	}
	query := authURL.Query()
	query.Set("client_id", b.config.NotionClientID)
	query.Set("redirect_uri", session.RedirectURI)
	query.Set("response_type", "code")
	query.Set("owner", "user")
	query.Set("state", session.State)
	authURL.RawQuery = query.Encode()
	// #nosec G107 -- redirect target is configured by the deployment operator.
	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

func (b *oauthBroker) notionCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	session, ok := b.sessionByState(state)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_state", "OAuth state is invalid or expired")
		return
	}
	if providerErr := r.URL.Query().Get("error"); providerErr != "" {
		b.failSession(session, providerErr)
		writeError(w, http.StatusBadRequest, providerErr, "Notion authorization failed")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		b.failSession(session, "missing code")
		writeError(w, http.StatusBadRequest, "missing_code", "Notion callback did not include a code")
		return
	}
	tokens, err := b.exchangeNotionCode(r, session, code)
	if err != nil {
		b.failSession(session, err.Error())
		writeError(w, http.StatusBadGateway, "token_exchange_failed", err.Error())
		return
	}
	b.completeSession(session, tokens)
	writeOAuthSuccessPage(w, oauthSuccessPage{
		Provider: notionOAuthProviderPage(),
	})
}

func (b *oauthBroker) refreshNotion(w http.ResponseWriter, r *http.Request) {
	if err := b.requireNotionConfig(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
		return
	}
	var request refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid refresh request")
		return
	}
	if strings.TrimSpace(request.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "missing_refresh_token", "refresh_token is required")
		return
	}
	tokens, err := b.postNotionToken(r, notionTokenRequest{
		GrantType:    "refresh_token",
		RefreshToken: request.RefreshToken,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (b *oauthBroker) revokeNotion(w http.ResponseWriter, r *http.Request) {
	if err := b.requireNotionConfig(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
		return
	}
	var request revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid revoke request")
		return
	}
	if strings.TrimSpace(request.Token) == "" {
		writeError(w, http.StatusBadRequest, "missing_token", "token is required")
		return
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	// #nosec G107 -- Notion revoke URL is configured by the deployment operator.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, b.config.NotionRevokeURL, bytes.NewReader(encoded))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_failed", err.Error())
		return
	}
	b.setNotionAuthHeaders(req)
	resp, err := b.config.HTTPClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "revoke_failed", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		writeError(w, http.StatusBadGateway, "revoke_failed", string(data))
		return
	}
	var out revokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		out = revokeResponse{Revoked: true}
	}
	writeJSON(w, http.StatusOK, out)
}

func (b *oauthBroker) exchangeNotionCode(r *http.Request, session *oauthSession, code string) (credentials.OAuthTokens, error) {
	return b.postNotionToken(r, notionTokenRequest{
		GrantType:   "authorization_code",
		Code:        code,
		RedirectURI: session.RedirectURI,
	})
}

func (b *oauthBroker) postNotionToken(r *http.Request, body notionTokenRequest) (credentials.OAuthTokens, error) {
	// #nosec G117 -- refresh tokens must be sent to Notion's token endpoint for OAuth refresh.
	encoded, err := json.Marshal(body)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	// #nosec G107 -- Notion token URL is configured by the deployment operator.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, b.config.NotionTokenURL, bytes.NewReader(encoded))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	b.setNotionAuthHeaders(req)
	resp, err := b.config.HTTPClient.Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return credentials.OAuthTokens{}, fmt.Errorf("notion token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return token.credentials(), nil
}

func (b *oauthBroker) setNotionAuthHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", b.config.NotionAPIVersion)
	req.SetBasicAuth(b.config.NotionClientID, b.config.NotionSecret)
}

func (b *oauthBroker) requireNotionConfig() error {
	if strings.TrimSpace(b.config.NotionClientID) == "" {
		return fmt.Errorf("NOTION_CLIENT_ID is required")
	}
	if strings.TrimSpace(b.config.NotionSecret) == "" {
		return fmt.Errorf("NOTION_CLIENT_SECRET is required")
	}
	if strings.TrimSpace(b.config.NotionAuthURL) == "" {
		return fmt.Errorf("NOTION_AUTH_URL is required")
	}
	if strings.TrimSpace(b.config.NotionTokenURL) == "" {
		return fmt.Errorf("NOTION_TOKEN_URL is required")
	}
	return nil
}

func (b *oauthBroker) session(id string) (*oauthSession, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session, ok := b.sessions[id]
	if !ok || time.Now().UTC().After(session.ExpiresAt) {
		return nil, false
	}
	return session, true
}

func (b *oauthBroker) sessionByState(state string) (*oauthSession, bool) {
	if state == "" {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	for _, session := range b.sessions {
		if session.State == state && now.Before(session.ExpiresAt) {
			return session, true
		}
	}
	return nil, false
}

func (b *oauthBroker) failSession(session *oauthSession, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session.Status = "failed"
	session.Error = message
}

func (b *oauthBroker) completeSession(session *oauthSession, tokens credentials.OAuthTokens) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session.Status = "complete"
	session.Tokens = tokens
}

func (b *oauthBroker) publicURL(r *http.Request) string {
	if b.config.PublicBaseURL != "" {
		return b.config.PublicBaseURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (b *oauthBroker) redirectURI(r *http.Request) string {
	if b.config.NotionRedirect != "" {
		return b.config.NotionRedirect
	}
	return b.publicURL(r) + "/oauth/notion/callback"
}

type tokenResponse struct {
	AccessToken          string `json:"access_token"`
	RefreshToken         string `json:"refresh_token"`
	TokenType            string `json:"token_type"`
	ExpiresIn            int64  `json:"expires_in"`
	BotID                string `json:"bot_id"`
	WorkspaceID          string `json:"workspace_id"`
	WorkspaceName        string `json:"workspace_name"`
	WorkspaceIcon        string `json:"workspace_icon"`
	DuplicatedTemplateID string `json:"duplicated_template_id"`
}

func (t tokenResponse) credentials() credentials.OAuthTokens {
	extra := map[string]string{}
	if t.BotID != "" {
		extra["bot_id"] = t.BotID
	}
	if t.WorkspaceID != "" {
		extra["workspace_id"] = t.WorkspaceID
	}
	if t.WorkspaceName != "" {
		extra["workspace_name"] = t.WorkspaceName
	}
	if t.WorkspaceIcon != "" {
		extra["workspace_icon"] = t.WorkspaceIcon
	}
	if t.DuplicatedTemplateID != "" {
		extra["duplicated_template_id"] = t.DuplicatedTemplateID
	}
	tokens := credentials.OAuthTokens{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    firstNonEmpty(t.TokenType, "bearer"),
		Scopes:       notion.DefaultCapabilities(),
		Extra:        extra,
	}
	if t.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return tokens
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Object:  "error",
		Status:  status,
		Code:    code,
		Message: message,
	})
}
