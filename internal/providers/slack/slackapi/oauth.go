package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
)

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
