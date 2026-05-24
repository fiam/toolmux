package slack

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

func slackClient(exec actions.Context, inv actions.Invocation) (slackapi.Client, error) {
	_ = inv
	tokens, err := loadSlackTokens(exec, exec.AccountName())
	if err != nil {
		return slackapi.Client{}, err
	}
	return slackClientForTokens(exec, tokens), nil
}

func slackClientForTokens(exec actions.Context, tokens credentials.OAuthTokens) slackapi.Client {
	return slackapi.Client{
		BaseURL:     firstNonEmpty(tokens.Extra["api_base_url"], exec.ProviderURL),
		HTTPClient:  exec.HTTPClient,
		AccessToken: tokens.AccessToken,
		Cookie:      normalizeSlackCookieHeader(tokens.Extra["cookie"]),
	}
}

func validateSlackAuth(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	response, err := slackClientForTokens(exec, tokens).AuthTest(exec.Context)
	if err != nil {
		return credentials.OAuthTokens{}, fmt.Errorf("slack auth validation failed: %w", err)
	}
	if !response.OK {
		return credentials.OAuthTokens{}, fmt.Errorf("slack auth validation failed: %s", firstNonEmpty(response.Error, "unknown_error"))
	}
	tokens.Extra = mergeExtra(tokens.Extra, slackAuthTestExtra(response))
	return tokens, nil
}

func slackAuthTestExtra(response slackapi.AuthTestResponse) map[string]string {
	extra := map[string]string{}
	if response.URL != "" {
		extra["team_url"] = response.URL
		if apiBaseURL := slackapi.APIBaseURLFromTeamURL(response.URL); apiBaseURL != "" {
			extra["api_base_url"] = apiBaseURL
		}
	}
	if response.TeamID != "" {
		extra["team_id"] = response.TeamID
	}
	if response.Team != "" {
		extra["team_name"] = response.Team
	}
	if response.UserID != "" {
		extra["user_id"] = response.UserID
	}
	if response.User != "" {
		extra["user_name"] = response.User
	}
	return extra
}

func loadSlackTokens(exec actions.Context, account string) (credentials.OAuthTokens, error) {
	ref := slackCredentialRef(exec, account)
	tokens, err := exec.Credentials.LoadOAuthTokens(exec.Context, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.OAuthTokens{}, fmt.Errorf("slack auth is not configured for account %s", account)
		}
		return credentials.OAuthTokens{}, err
	}
	if !slackTokenNeedsRefresh(tokens, time.Now()) {
		return ensureSlackAuthMetadata(exec, ref, tokens)
	}
	refreshed, err := refreshSlackTokens(exec, tokens)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	refreshed.Extra = mergeExtra(refreshed.Extra, tokens.Extra)
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = tokens.RefreshToken
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, refreshed); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return ensureSlackAuthMetadata(exec, ref, refreshed)
}

func ensureSlackAuthMetadata(exec actions.Context, ref credentials.ConnectionRef, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if tokens.Extra["api_base_url"] != "" && tokens.Extra["team_url"] != "" {
		return tokens, nil
	}
	enriched, err := validateSlackAuth(exec, tokens)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, enriched); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return enriched, nil
}

func refreshSlackTokens(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("slack OAuth token is expired and has no refresh token")
	}
	switch tokens.Extra["auth_type"] {
	case authTypeUser:
		options := slackapi.OAuthOptions{
			TokenURL:     firstNonEmpty(tokens.Extra["token_url"], slackapi.OAuthURLFromAPIBase(exec.ProviderURL, "/api/oauth.v2.access")),
			ClientID:     tokens.Extra["client_id"],
			ClientSecret: tokens.Extra["client_secret"],
			TokenSource:  tokens.Extra["token_source"],
		}
		return slackapi.RefreshOAuthToken(exec.Context, exec.HTTPClient, options, tokens.RefreshToken, time.Now())
	case authTypeBroker:
		return refreshBrokerToken(exec, tokens)
	default:
		return credentials.OAuthTokens{}, fmt.Errorf("slack token for auth type %q cannot be refreshed", tokens.Extra["auth_type"])
	}
}

func slackTokenNeedsRefresh(tokens credentials.OAuthTokens, now time.Time) bool {
	if tokens.ExpiresAt.IsZero() || strings.TrimSpace(tokens.RefreshToken) == "" {
		return false
	}
	return !now.Add(oauthRefreshSkew).Before(tokens.ExpiresAt)
}

func slackCredentialRef(exec actions.Context, account string) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   exec.Profile,
		Provider:  providerID,
		AccountID: account,
	}
}
