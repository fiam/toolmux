package broker

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
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
		writePickerDonePage(w, pickerDonePage{
			Title:   "Google Picker failed",
			Message: "Google returned an authorization error. Return to your terminal for details.",
			Success: false,
		})
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
	writePickerDonePage(w, pickerDonePage{
		Title:   "Google Picker selection received",
		Message: "Toolmux has the selected file IDs. You can close this tab and return to your terminal.",
		Success: true,
	})
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
	for part := range strings.SplitSeq(value, ",") {
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

type pickerDonePage struct {
	Title   string
	Message string
	Success bool
}

func writePickerDonePage(w http.ResponseWriter, page pickerDonePage) {
	var body bytes.Buffer
	if err := pickerDoneTemplate.Execute(&body, page); err != nil {
		writePickerError(w, http.StatusInternalServerError, "render_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

var pickerDoneTemplate = template.Must(template.New("google-picker-done").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} - Toolmux</title>
  <style>
    :root {
      color-scheme: dark;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      background: #0a0d12;
      color: #e8edf7;
    }
    * {
      box-sizing: border-box;
    }
    body {
      min-height: 100vh;
      margin: 0;
      display: grid;
      place-items: center;
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.04), transparent 30%),
        #0a0d12;
    }
    main {
      width: min(760px, calc(100vw - 32px));
      border: 1px solid rgba(255, 255, 255, 0.16);
      border-radius: 8px;
      background: rgba(14, 18, 27, 0.96);
      box-shadow: 0 24px 80px rgba(0, 0, 0, 0.38);
      overflow: hidden;
    }
    header {
      display: flex;
      align-items: center;
      gap: 18px;
      padding: 28px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.12);
    }
    .logo {
      width: 56px;
      height: 56px;
      flex: 0 0 auto;
      display: grid;
      place-items: center;
      border-radius: 8px;
      background: #ffffff;
      color: #111111;
      border: 1px solid rgba(255, 255, 255, 0.2);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      font-size: 30px;
      font-weight: 800;
      line-height: 1;
    }
    .eyebrow {
      margin: 0 0 8px;
      color: #8ea0b8;
      font-size: 12px;
      letter-spacing: 0;
      text-transform: uppercase;
    }
    h1 {
      margin: 0;
      font-size: clamp(24px, 4vw, 36px);
      line-height: 1.12;
      letter-spacing: 0;
    }
    .terminal {
      margin: 28px;
      padding: 20px;
      border-radius: 8px;
      background: #05070a;
      border: 1px solid rgba(255, 255, 255, 0.12);
      color: #cbd6e6;
      font-size: 15px;
      line-height: 1.8;
      overflow-wrap: anywhere;
    }
    .prompt {
      color: #7dd3fc;
    }
    .ok {
      color: #86efac;
      font-weight: 700;
    }
    .err {
      color: #fca5a5;
      font-weight: 700;
    }
    .muted {
      color: #8ea0b8;
    }
    .hint {
      margin: 0;
      padding: 0 28px 28px;
      color: #a8b4c6;
      line-height: 1.55;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    @media (max-width: 520px) {
      header {
        align-items: flex-start;
        padding: 22px;
      }
      .terminal {
        margin: 22px;
        font-size: 13px;
      }
      .hint {
        padding: 0 22px 22px;
      }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div class="logo" aria-label="Google logo">G</div>
      <div>
        <p class="eyebrow">toolmux google picker</p>
        <h1>{{.Title}}</h1>
      </div>
    </header>
    <section class="terminal" aria-live="polite">
      <div><span class="muted">...</span> waiting for Google Picker selection</div>
      {{if .Success}}
      <div><span class="ok">OK</span> picker selection received</div>
      <div><span class="ok">OK</span> selected file IDs handed to Toolmux</div>
      {{else}}
      <div><span class="err">ERR</span> picker authorization failed</div>
      {{end}}
      <div><span class="muted">...</span> return to your terminal</div>
    </section>
    <p class="hint">{{.Message}}</p>
  </main>
</body>
</html>`))

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}
