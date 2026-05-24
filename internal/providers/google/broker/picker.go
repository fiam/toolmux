package broker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/brokers"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

const (
	// #nosec G101 -- this is a broker config map key, not credential material.
	pickerRedirectURIConfig = "picker_redirect_uri"
)

type pickerBroker struct {
	config        brokers.Config
	publicBaseURL string
	ttl           time.Duration

	mu       sync.Mutex
	sessions map[string]*pickerSession
}

type pickerSession struct {
	ID        string
	State     string
	MIMEType  string
	CreatedAt time.Time
	ExpiresAt time.Time
	Status    string
	Error     string
	Files     []pickerFile
	Tokens    credentials.OAuthTokens
	Delivered bool
}

type createPickerSessionRequest struct {
	MIMEType string `json:"mime_type,omitempty"`
}

type createPickerSessionResponse struct {
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	PickerURL string    `json:"picker_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pickerSessionResponse struct {
	SessionID string                   `json:"session_id"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Files     []pickerFile             `json:"files,omitempty"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
}

type pickerFile struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	URL      string `json:"url,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

func RegisterPickerHTTP(mux *http.ServeMux, config brokers.Config, ctx brokers.HTTPContext) {
	ttl := ctx.SessionTTL
	if ttl <= 0 {
		ttl = 120 * time.Second
	}
	broker := &pickerBroker{
		config:        config,
		publicBaseURL: strings.TrimRight(ctx.PublicBaseURL, "/"),
		ttl:           ttl,
		sessions:      map[string]*pickerSession{},
	}
	mux.HandleFunc("POST /v1/google/picker/sessions", broker.createSession)
	mux.HandleFunc("GET /v1/google/picker/sessions/{session_id}", broker.getSession)
	mux.HandleFunc("GET /oauth/google/picker/callback", broker.callback)
	if ctx.OAuthCallbackFallbacks != nil {
		ctx.OAuthCallbackFallbacks[actions.ProviderName("google")] = broker.callbackFallback
	}
}

func (b *pickerBroker) createSession(w http.ResponseWriter, r *http.Request) {
	if err := b.requirePickerConfig(); err != nil {
		writePickerError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
		return
	}
	var request createPickerSessionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writePickerError(w, http.StatusBadRequest, "invalid_json", "invalid Picker session request")
		return
	}
	mimeType := strings.TrimSpace(request.MIMEType)
	if strings.ContainsAny(mimeType, "\r\n") {
		writePickerError(w, http.StatusBadRequest, "invalid_mime_type", "mime_type must be a single line")
		return
	}
	id, err := pickerRandomHex(16)
	if err != nil {
		writePickerError(w, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	state, err := pickerRandomHex(24)
	if err != nil {
		writePickerError(w, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	session := &pickerSession{
		ID:        id,
		State:     state,
		MIMEType:  mimeType,
		CreatedAt: now,
		ExpiresAt: now.Add(b.ttl),
		Status:    "pending",
	}
	pickerURL, err := b.pickerURL(r, session)
	if err != nil {
		writePickerError(w, http.StatusInternalServerError, "invalid_config", err.Error())
		return
	}
	b.mu.Lock()
	b.pruneExpiredLocked(now)
	b.sessions[id] = session
	b.mu.Unlock()

	writePickerJSON(w, http.StatusCreated, createPickerSessionResponse{
		SessionID: id,
		Status:    session.Status,
		PickerURL: pickerURL,
		ExpiresAt: session.ExpiresAt,
	})
}

func (b *pickerBroker) getSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		writePickerError(w, http.StatusNotFound, "not_found", "Google Picker session not found")
		return
	}
	b.mu.Lock()
	session, ok := b.sessions[sessionID]
	if !ok {
		b.mu.Unlock()
		writePickerError(w, http.StatusNotFound, "not_found", "Google Picker session not found")
		return
	}
	status := session.Status
	if time.Now().UTC().After(session.ExpiresAt) {
		status = "expired"
	}
	response := pickerSessionResponse{
		SessionID: session.ID,
		Status:    status,
		Error:     session.Error,
		ExpiresAt: session.ExpiresAt,
		Files:     append([]pickerFile(nil), session.Files...),
	}
	if session.Status == "complete" && !session.Delivered {
		tokens := session.Tokens
		response.Tokens = &tokens
		session.Tokens = credentials.OAuthTokens{}
		session.Delivered = true
	}
	b.mu.Unlock()
	writePickerJSON(w, http.StatusOK, response)
}

func (b *pickerBroker) callback(w http.ResponseWriter, r *http.Request) {
	b.handleCallback(w, r, true)
}

func (b *pickerBroker) callbackFallback(w http.ResponseWriter, r *http.Request) bool {
	return b.handleCallback(w, r, false)
}

func (b *pickerBroker) handleCallback(w http.ResponseWriter, r *http.Request, writeInvalidState bool) bool {
	session, ok := b.sessionByState(r.URL.Query().Get("state"))
	if !ok {
		if writeInvalidState {
			writePickerError(w, http.StatusBadRequest, "invalid_state", "Google Picker state is invalid or expired")
		}
		return false
	}
	if providerErr := strings.TrimSpace(r.URL.Query().Get("error")); providerErr != "" {
		b.failSession(session, providerErr)
		writePickerDonePage(w, "Google Picker failed", "Google returned an authorization error. Return to your terminal for details.")
		return true
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		b.failSession(session, "missing code")
		writePickerError(w, http.StatusBadRequest, "missing_code", "Google Picker callback did not include a code")
		return true
	}
	files := pickedFileIDs(r.URL.Query().Get("picked_file_ids"))
	if len(files) == 0 {
		b.failSession(session, "missing picked file IDs")
		writePickerError(w, http.StatusBadRequest, "missing_picked_file_ids", "Google Picker callback did not include picked_file_ids")
		return true
	}
	tokens, err := googleapi.ExchangeOAuthCode(r.Context(), httpClient(b.config.HTTPClient), googleapi.OAuthOptions{
		TokenURL:     b.config.TokenURL,
		ClientID:     b.config.ClientID,
		ClientSecret: b.config.Secret,
		RedirectURI:  b.redirectURI(r),
	}, code, time.Now().UTC())
	if err != nil {
		b.failSession(session, err.Error())
		writePickerError(w, http.StatusBadGateway, "token_exchange_failed", err.Error())
		return true
	}
	tokens = oauthbroker.MergeTokens(credentials.OAuthTokens{}, tokens, []string{googleapi.ScopeDriveFile})
	b.completeSession(session, files, tokens)
	writePickerDonePage(w, "Google Picker selection received", "Toolmux has the selected file IDs. You can close this tab and return to your terminal.")
	return true
}

func (b *pickerBroker) requirePickerConfig() error {
	if strings.TrimSpace(b.config.ClientID) == "" {
		return fmt.Errorf("google picker broker is missing GOOGLE_CLIENT_ID")
	}
	if strings.TrimSpace(b.config.Secret) == "" {
		return fmt.Errorf("google picker broker is missing GOOGLE_CLIENT_SECRET")
	}
	if strings.TrimSpace(b.config.AuthURL) == "" {
		return fmt.Errorf("google picker broker is missing an authorization URL")
	}
	if strings.TrimSpace(b.config.TokenURL) == "" {
		return fmt.Errorf("google picker broker is missing a token URL")
	}
	if !oauthbroker.HasScopes(b.config.Scopes, []string{googleapi.ScopeDriveFile}) {
		return fmt.Errorf("google picker broker requires %s in GOOGLE_SCOPES", googleapi.ScopeDriveFile)
	}
	return nil
}

func (b *pickerBroker) pickerURL(r *http.Request, session *pickerSession) (string, error) {
	return googleapi.PickerAuthorizeURL(googleapi.OAuthOptions{
		AuthURL:     b.config.AuthURL,
		ClientID:    b.config.ClientID,
		RedirectURI: b.redirectURI(r),
	}, session.State, session.MIMEType)
}

func (b *pickerBroker) redirectURI(r *http.Request) string {
	if value := strings.TrimSpace(b.config.Extra[pickerRedirectURIConfig]); value != "" {
		return value
	}
	return b.publicURL(r) + "/oauth/google/callback"
}

func (b *pickerBroker) sessionByState(state string) (*pickerSession, bool) {
	state = strings.TrimSpace(state)
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

func (b *pickerBroker) failSession(session *pickerSession, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session.Status = "failed"
	session.Error = strings.TrimSpace(message)
}

func (b *pickerBroker) completeSession(session *pickerSession, files []pickerFile, tokens credentials.OAuthTokens) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session.Status = "complete"
	session.Files = cleanPickerFiles(files)
	session.Tokens = tokens
}

func (b *pickerBroker) pruneExpiredLocked(now time.Time) {
	for id, session := range b.sessions {
		if now.After(session.ExpiresAt) {
			delete(b.sessions, id)
		}
	}
}

func (b *pickerBroker) publicURL(r *http.Request) string {
	if b.publicBaseURL != "" {
		return b.publicBaseURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func pickedFileIDs(value string) []pickerFile {
	seen := map[string]bool{}
	var files []pickerFile
	for _, part := range strings.Split(value, ",") {
		id := strings.TrimSpace(part)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		files = append(files, pickerFile{ID: id})
	}
	return files
}

func cleanPickerFiles(files []pickerFile) []pickerFile {
	out := make([]pickerFile, 0, len(files))
	for _, file := range files {
		file.ID = strings.TrimSpace(file.ID)
		file.Name = strings.TrimSpace(file.Name)
		file.URL = strings.TrimSpace(file.URL)
		file.MIMEType = strings.TrimSpace(file.MIMEType)
		if file.ID != "" {
			out = append(out, file)
		}
	}
	return out
}

func pickerRandomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writePickerJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writePickerError(w http.ResponseWriter, status int, code, message string) {
	writePickerJSON(w, status, map[string]any{
		"object":  "error",
		"status":  status,
		"code":    code,
		"message": message,
	})
}

func writePickerDonePage(w http.ResponseWriter, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s - Toolmux</title>
  <style>
    :root { color-scheme: light; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f8fb; color: #111827; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; }
    main { width: min(34rem, calc(100vw - 2rem)); padding: 2rem; border: 1px solid #dbe3ef; border-radius: 8px; background: #ffffff; box-shadow: 0 18px 48px rgba(15, 23, 42, 0.10); text-align: center; }
    h1 { margin: 0; font-size: 1.45rem; line-height: 1.2; letter-spacing: 0; }
    p { margin: 0.75rem 0 0; line-height: 1.55; color: #4b5563; }
  </style>
</head>
<body>
  <main>
    <h1>%s</h1>
    <p>%s</p>
  </main>
</body>
</html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}
