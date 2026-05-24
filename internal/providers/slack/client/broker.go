package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

func slackOAuthOptions(exec actions.Context, inv actions.Invocation, clientID, clientSecret, redirectURI string) slackapi.OAuthOptions {
	authURL := firstNonEmpty(inv.String("auth-url"), slackapi.OAuthURLFromAPIBase(exec.ProviderURL, "/oauth/v2/authorize"), slackapi.DefaultAuthURL)
	tokenURL := firstNonEmpty(inv.String("token-url"), slackapi.OAuthURLFromAPIBase(exec.ProviderURL, "/api/oauth.v2.access"), slackapi.DefaultTokenURL)
	scopes := slackapi.CleanScopes(inv.StringSlice("scope"))
	if len(scopes) == 0 {
		scopes = append([]string(nil), defaultOAuthScopes...)
	}
	return slackapi.OAuthOptions{
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		Scopes:       scopes,
		UserScopes:   slackapi.CleanScopes(inv.StringSlice("user-scope")),
		TokenSource:  strings.TrimSpace(inv.String("token-source")),
	}
}

func createBrokerSession(exec actions.Context, inv actions.Invocation) (brokerSession, error) {
	endpoint := strings.TrimRight(exec.ToolmuxdURL, "/") + "/v1/oauth/sessions"
	scopes := slackapi.CleanScopes(inv.StringSlice("scope"))
	if len(scopes) == 0 {
		scopes = append([]string(nil), defaultOAuthScopes...)
	}
	body := map[string]any{
		"provider": providerID,
		"profile":  exec.Profile,
		"account":  account(inv),
		"scopes":   scopes,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return brokerSession{}, err
	}
	req, err := http.NewRequestWithContext(exec.Context, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return brokerSession{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return brokerSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return brokerSession{}, fmt.Errorf("toolmux OAuth broker returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session brokerSession
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return brokerSession{}, err
	}
	if strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.AuthURL) == "" {
		return brokerSession{}, fmt.Errorf("toolmux OAuth broker returned an incomplete session")
	}
	return session, nil
}

func pollBrokerSession(exec actions.Context, sessionID string, timeout time.Duration) (credentials.OAuthTokens, error) {
	ctx, cancel := context.WithTimeout(exec.Context, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		tokens, done, err := getBrokerSession(ctx, exec, sessionID)
		if err != nil {
			return credentials.OAuthTokens{}, err
		}
		if done {
			return tokens, nil
		}
		select {
		case <-ctx.Done():
			return credentials.OAuthTokens{}, fmt.Errorf("timed out waiting for slack broker OAuth: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func getBrokerSession(ctx context.Context, exec actions.Context, sessionID string) (credentials.OAuthTokens, bool, error) {
	endpoint := strings.TrimRight(exec.ToolmuxdURL, "/") + "/v1/oauth/sessions/" + url.PathEscape(sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials.OAuthTokens{}, false, fmt.Errorf("toolmux OAuth broker returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session brokerSessionStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return credentials.OAuthTokens{}, false, err
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
		return credentials.OAuthTokens{}, false, fmt.Errorf("slack broker OAuth failed: %s", session.Error)
	case "expired":
		return credentials.OAuthTokens{}, false, fmt.Errorf("slack broker OAuth session expired")
	default:
		return credentials.OAuthTokens{}, false, fmt.Errorf("slack broker OAuth returned unknown status %q", session.Status)
	}
}

func refreshBrokerToken(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	brokerURL := firstNonEmpty(tokens.Extra["broker_url"], exec.ToolmuxdURL)
	endpoint := strings.TrimRight(brokerURL, "/") + "/v1/oauth/slack/refresh"
	data, err := json.Marshal(map[string]string{"refresh_token": tokens.RefreshToken})
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req, err := http.NewRequestWithContext(exec.Context, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials.OAuthTokens{}, fmt.Errorf("slack broker refresh returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var refreshed credentials.OAuthTokens
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&refreshed); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return refreshed, nil
}
