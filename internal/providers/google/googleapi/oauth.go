package googleapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

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
