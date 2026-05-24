package oauthbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Session struct {
	SessionID   string    `json:"session_id"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	AuthURL     string    `json:"auth_url"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type SessionStatus struct {
	SessionID string                   `json:"session_id"`
	Provider  string                   `json:"provider"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
	Extra     map[string]string        `json:"extra,omitempty"`
}

func (c Client) CreateSession(ctx context.Context, provider, profile, account string, scopes []string) (Session, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/oauth/sessions"
	body := map[string]any{
		"provider": strings.TrimSpace(provider),
		"profile":  strings.TrimSpace(profile),
		"account":  strings.TrimSpace(account),
		"scopes":   CleanScopes(scopes),
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Session{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return Session{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(c.HTTPClient).Do(req)
	if err != nil {
		return Session{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Session{}, fmt.Errorf("toolmux OAuth broker returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session Session
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.AuthURL) == "" {
		return Session{}, fmt.Errorf("toolmux OAuth broker returned an incomplete session")
	}
	return session, nil
}

func (c Client) PollSession(ctx context.Context, sessionID, provider string, timeout time.Duration) (credentials.OAuthTokens, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		tokens, done, err := c.GetSession(ctx, sessionID, provider)
		if err != nil {
			return credentials.OAuthTokens{}, err
		}
		if done {
			return tokens, nil
		}
		select {
		case <-ctx.Done():
			return credentials.OAuthTokens{}, fmt.Errorf("timed out waiting for %s broker OAuth: %w", provider, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c Client) GetSession(ctx context.Context, sessionID, provider string) (credentials.OAuthTokens, bool, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/oauth/sessions/" + url.PathEscape(strings.TrimSpace(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(c.HTTPClient).Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials.OAuthTokens{}, false, fmt.Errorf("toolmux OAuth broker returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session SessionStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	label := strings.TrimSpace(provider)
	if label == "" {
		label = strings.TrimSpace(session.Provider)
	}
	switch session.Status {
	case "pending":
		return credentials.OAuthTokens{}, false, nil
	case "complete":
		if session.Tokens == nil {
			return credentials.OAuthTokens{}, false, fmt.Errorf("toolmux OAuth broker completed without tokens")
		}
		return *session.Tokens, true, nil
	case "failed":
		return credentials.OAuthTokens{}, false, fmt.Errorf("%s broker OAuth failed: %s", label, session.Error)
	case "expired":
		return credentials.OAuthTokens{}, false, fmt.Errorf("%s broker OAuth session expired", label)
	default:
		return credentials.OAuthTokens{}, false, fmt.Errorf("%s broker OAuth returned unknown status %q", label, session.Status)
	}
}

func (c Client) Refresh(ctx context.Context, provider string, refreshToken string) (credentials.OAuthTokens, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/oauth/" + url.PathEscape(strings.TrimSpace(provider)) + "/refresh"
	data, err := json.Marshal(map[string]string{"refresh_token": strings.TrimSpace(refreshToken)})
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(c.HTTPClient).Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials.OAuthTokens{}, fmt.Errorf("%s broker refresh returned status %d: %s", provider, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var refreshed credentials.OAuthTokens
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&refreshed); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return refreshed, nil
}

func MissingScopes(granted, required []string) []string {
	have := map[string]bool{}
	for _, scope := range CleanScopes(granted) {
		have[scope] = true
	}
	var missing []string
	for _, scope := range CleanScopes(required) {
		if !have[scope] {
			missing = append(missing, scope)
		}
	}
	return slices.Clip(missing)
}

func HasScopes(granted, required []string) bool {
	return len(MissingScopes(granted, required)) == 0
}

func MergeTokens(existing, incoming credentials.OAuthTokens, requestedScopes []string) credentials.OAuthTokens {
	merged := incoming
	if strings.TrimSpace(merged.RefreshToken) == "" {
		merged.RefreshToken = strings.TrimSpace(existing.RefreshToken)
	}
	if strings.TrimSpace(merged.TokenType) == "" {
		merged.TokenType = firstNonEmpty(existing.TokenType, "Bearer")
	}
	merged.Scopes = UnionScopes(existing.Scopes, incoming.Scopes, requestedScopes)
	merged.Extra = mergeStringMap(existing.Extra, incoming.Extra)
	return merged
}

func UnionScopes(values ...[]string) []string {
	seen := map[string]bool{}
	var scopes []string
	for _, value := range values {
		for _, scope := range CleanScopes(value) {
			if seen[scope] {
				continue
			}
			seen[scope] = true
			scopes = append(scopes, scope)
		}
	}
	return slices.Clip(scopes)
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
	return slices.Clip(scopes)
}

func mergeStringMap(base, overlay map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	for key, value := range overlay {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
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
