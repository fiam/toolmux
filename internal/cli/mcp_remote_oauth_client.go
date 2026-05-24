package cli

import (
	"bytes"
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

func resolveMCPRemoteOAuthClient(ctx context.Context, client *http.Client, metadata mcpRemoteAuthorizationServerMetadata, redirectURI string, login mcpRemoteAuthLoginOptions) (mcpRemoteOAuthClient, error) {
	clientID := strings.TrimSpace(login.ClientID)
	if clientID != "" {
		return mcpRemoteOAuthClient{
			ClientID:     clientID,
			ClientSecret: strings.TrimSpace(login.ClientSecret),
		}, nil
	}
	if strings.TrimSpace(metadata.RegistrationEndpoint) == "" {
		return mcpRemoteOAuthClient{}, fmt.Errorf("authorization server does not advertise dynamic client registration; pass --client-id")
	}
	payload := map[string]any{
		"client_name":                "Toolmux",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	// #nosec G107 -- registration endpoint comes from authorization server metadata.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RegistrationEndpoint, bytes.NewReader(data))
	if err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return mcpRemoteOAuthClient{}, fmt.Errorf("dynamic client registration returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var registered mcpRemoteOAuthClient
	if err := json.NewDecoder(io.LimitReader(resp.Body, mcpRemoteOAuthMetadataBodyMax)).Decode(&registered); err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	if strings.TrimSpace(registered.ClientID) == "" {
		return mcpRemoteOAuthClient{}, fmt.Errorf("dynamic client registration did not return client_id")
	}
	registered.ClientID = strings.TrimSpace(registered.ClientID)
	registered.ClientSecret = strings.TrimSpace(registered.ClientSecret)
	return registered, nil
}

func mcpRemoteOAuthAuthorizationURL(endpoint, clientID, redirectURI, state, challenge, resourceURI string, scopes []string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("resource", resourceURI)
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeMCPRemoteOAuthCode(ctx context.Context, client *http.Client, discovery mcpRemoteOAuthDiscovery, oauthClient mcpRemoteOAuthClient, redirectURI, code, verifier string, scopes []string) (credentials.OAuthTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", redirectURI)
	values.Set("client_id", oauthClient.ClientID)
	values.Set("code_verifier", verifier)
	values.Set("resource", discovery.ResourceURI)
	if oauthClient.ClientSecret != "" {
		values.Set("client_secret", oauthClient.ClientSecret)
	}
	response, err := postMCPRemoteOAuthToken(ctx, client, discovery.Authorization.TokenEndpoint, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	return response.credentials(scopes, time.Now().UTC()), nil
}

func refreshMCPRemoteOAuthToken(ctx context.Context, client *http.Client, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimSpace(tokens.Extra["token_endpoint"])
	clientID := strings.TrimSpace(tokens.Extra["client_id"])
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	if endpoint == "" || clientID == "" || refreshToken == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("stored MCP OAuth token is missing refresh metadata")
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", clientID)
	if resource := strings.TrimSpace(tokens.Extra["resource"]); resource != "" {
		values.Set("resource", resource)
	}
	if clientSecret := strings.TrimSpace(tokens.Extra["client_secret"]); clientSecret != "" {
		values.Set("client_secret", clientSecret)
	}
	response, err := postMCPRemoteOAuthToken(ctx, client, endpoint, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	refreshed := response.credentials(tokens.Scopes, time.Now().UTC())
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = tokens.RefreshToken
	}
	refreshed.Extra = cloneMCPRemoteStringMap(tokens.Extra)
	return refreshed, nil
}

func postMCPRemoteOAuthToken(ctx context.Context, client *http.Client, endpoint string, values url.Values) (mcpRemoteOAuthTokenResponse, error) {
	// #nosec G107 -- token endpoint comes from authorization server metadata stored for this MCP server.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return mcpRemoteOAuthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return mcpRemoteOAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return mcpRemoteOAuthTokenResponse{}, fmt.Errorf("OAuth token endpoint returned status %d", resp.StatusCode)
	}
	var token mcpRemoteOAuthTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, mcpRemoteOAuthMetadataBodyMax)).Decode(&token); err != nil {
		return mcpRemoteOAuthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return mcpRemoteOAuthTokenResponse{}, fmt.Errorf("OAuth token endpoint did not return access_token")
	}
	return token, nil
}

func (response mcpRemoteOAuthTokenResponse) credentials(requestedScopes []string, now time.Time) credentials.OAuthTokens {
	tokenType := strings.TrimSpace(response.TokenType)
	if tokenType == "" {
		tokenType = "bearer"
	}
	scopes := strings.Fields(response.Scope)
	if len(scopes) == 0 {
		scopes = append([]string(nil), requestedScopes...)
	}
	tokens := credentials.OAuthTokens{
		AccessToken:  strings.TrimSpace(response.AccessToken),
		RefreshToken: strings.TrimSpace(response.RefreshToken),
		TokenType:    tokenType,
		Scopes:       scopes,
	}
	if response.ExpiresIn > 0 {
		tokens.ExpiresAt = now.Add(time.Duration(response.ExpiresIn) * time.Second).UTC()
	}
	return tokens
}
