package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fiam/supacli/internal/actions"
	"github.com/fiam/supacli/internal/credentials"
	"github.com/fiam/supacli/internal/providers/brokers"
)

type Config struct {
	PublicBaseURL string
	Providers     map[actions.ProviderName]brokers.Config
	HTTPClient    *http.Client
	SessionTTL    time.Duration
}

func ConfigFromEnv() Config {
	httpClient := http.DefaultClient
	config := Config{
		PublicBaseURL: strings.TrimRight(os.Getenv("SUPACLI_PUBLIC_URL"), "/"),
		Providers:     map[actions.ProviderName]brokers.Config{},
		HTTPClient:    httpClient,
		SessionTTL:    120 * time.Second,
	}
	for _, descriptor := range brokers.All() {
		config.Providers[descriptor.ID] = descriptor.CompleteConfig(brokers.Config{}, httpClient)
	}
	return config
}

type oauthBroker struct {
	config    Config
	providers map[actions.ProviderName]oauthProvider
	mu        sync.Mutex
	sessions  map[string]*oauthSession
}

type oauthProvider struct {
	descriptor brokers.Descriptor
	config     brokers.Config
	broker     brokers.OAuthProvider
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

func registerOAuthHandlers(mux *http.ServeMux, config Config) {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 120 * time.Second
	}
	if config.Providers == nil {
		config.Providers = map[actions.ProviderName]brokers.Config{}
	}
	registered := map[actions.ProviderName]oauthProvider{}
	for _, descriptor := range brokers.All() {
		providerConfig := descriptor.CompleteConfig(config.Providers[descriptor.ID], config.HTTPClient)
		registered[descriptor.ID] = oauthProvider{
			descriptor: descriptor,
			config:     providerConfig,
			broker:     descriptor.NewOAuthProvider(providerConfig),
		}
	}
	broker := &oauthBroker{
		config:    config,
		providers: registered,
		sessions:  make(map[string]*oauthSession),
	}
	mux.HandleFunc("POST /v1/oauth/sessions", broker.createSession)
	mux.HandleFunc("GET /v1/oauth/sessions/{session_id}", broker.getSession)
	mux.HandleFunc("GET /oauth/{provider}/start", broker.startOAuth)
	mux.HandleFunc("GET /oauth/{provider}/callback", broker.oauthCallback)
	mux.HandleFunc("POST /v1/oauth/{provider}/refresh", broker.refreshOAuth)
	mux.HandleFunc("POST /v1/oauth/{provider}/revoke", broker.revokeOAuth)
}

func (b *oauthBroker) createSession(w http.ResponseWriter, r *http.Request) {
	var request createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid session request")
		return
	}
	provider, ok := b.provider(actions.ProviderName(strings.TrimSpace(request.Provider)))
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported_provider", "OAuth provider is not registered")
		return
	}
	if err := provider.broker.RequireConfig(); err != nil {
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
		Provider:    string(provider.descriptor.ID),
		State:       state,
		RedirectURI: b.redirectURI(r, provider),
		CreatedAt:   now,
		ExpiresAt:   now.Add(b.config.SessionTTL),
		Status:      "pending",
	}
	b.mu.Lock()
	b.sessions[id] = session
	b.mu.Unlock()

	writeJSON(w, http.StatusCreated, createSessionResponse{
		SessionID:   id,
		Provider:    string(provider.descriptor.ID),
		Status:      session.Status,
		AuthURL:     b.publicURL(r) + "/oauth/" + url.PathEscape(string(provider.descriptor.ID)) + "/start?session_id=" + url.QueryEscape(id),
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

func (b *oauthBroker) startOAuth(w http.ResponseWriter, r *http.Request) {
	provider, ok := b.provider(actions.ProviderName(r.PathValue("provider")))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth provider not found")
		return
	}
	session, ok := b.session(r.URL.Query().Get("session_id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth session not found")
		return
	}
	if session.Provider != string(provider.descriptor.ID) {
		writeError(w, http.StatusBadRequest, "provider_mismatch", "OAuth session provider does not match callback provider")
		return
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		writeError(w, http.StatusGone, "expired", "OAuth session expired")
		return
	}
	authURL, err := provider.broker.AuthURL(session.RedirectURI, session.State)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_config", "invalid authorization URL")
		return
	}
	// #nosec G107 -- redirect target is configured by the deployment operator.
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (b *oauthBroker) oauthCallback(w http.ResponseWriter, r *http.Request) {
	provider, ok := b.provider(actions.ProviderName(r.PathValue("provider")))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth provider not found")
		return
	}
	state := r.URL.Query().Get("state")
	session, ok := b.sessionByState(state)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_state", "OAuth state is invalid or expired")
		return
	}
	if session.Provider != string(provider.descriptor.ID) {
		b.failSession(session, "provider mismatch")
		writeError(w, http.StatusBadRequest, "provider_mismatch", "OAuth session provider does not match callback provider")
		return
	}
	if providerErr := r.URL.Query().Get("error"); providerErr != "" {
		b.failSession(session, providerErr)
		writeError(w, http.StatusBadRequest, providerErr, provider.descriptor.DisplayName+" authorization failed")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		b.failSession(session, "missing code")
		writeError(w, http.StatusBadRequest, "missing_code", "OAuth callback did not include a code")
		return
	}
	tokens, err := b.exchangeCode(r, provider, session, code)
	if err != nil {
		b.failSession(session, err.Error())
		writeError(w, http.StatusBadGateway, "token_exchange_failed", err.Error())
		return
	}
	b.completeSession(session, tokens)
	writeOAuthSuccessPage(w, oauthSuccessPage{
		Provider: oauthProviderPage{
			Name: provider.descriptor.DisplayName,
			Slug: string(provider.descriptor.ID),
			Logo: provider.descriptor.Logo,
		},
	})
}

func (b *oauthBroker) refreshOAuth(w http.ResponseWriter, r *http.Request) {
	provider, ok := b.provider(actions.ProviderName(r.PathValue("provider")))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth provider not found")
		return
	}
	if err := provider.broker.RequireConfig(); err != nil {
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
	tokens, err := provider.broker.Refresh(r.Context(), request.RefreshToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (b *oauthBroker) revokeOAuth(w http.ResponseWriter, r *http.Request) {
	provider, ok := b.provider(actions.ProviderName(r.PathValue("provider")))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "OAuth provider not found")
		return
	}
	if err := provider.broker.RequireConfig(); err != nil {
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
	out, err := provider.broker.Revoke(r.Context(), request.Token)
	if err != nil {
		writeError(w, http.StatusBadGateway, "revoke_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (b *oauthBroker) exchangeCode(r *http.Request, provider oauthProvider, session *oauthSession, code string) (credentials.OAuthTokens, error) {
	return provider.broker.ExchangeCode(r.Context(), code, session.RedirectURI)
}

func (b *oauthBroker) provider(id actions.ProviderName) (oauthProvider, bool) {
	id = actions.ProviderName(strings.TrimSpace(string(id)))
	provider, ok := b.providers[id]
	return provider, ok
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

func (b *oauthBroker) redirectURI(r *http.Request, provider oauthProvider) string {
	if provider.config.RedirectURI != "" {
		return provider.config.RedirectURI
	}
	return b.publicURL(r) + "/oauth/" + url.PathEscape(string(provider.descriptor.ID)) + "/callback"
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
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
