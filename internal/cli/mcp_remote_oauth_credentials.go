package cli

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
)

func loadMCPRemoteAccessToken(ctx context.Context, opts *options, entry mcpRemoteServerEntry) (string, error) {
	store, err := opts.credentials()
	if err != nil {
		return "", err
	}
	ref := mcpRemoteCredentialRef(opts, entry.Name)
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	if mcpRemoteOAuthTokenNeedsRefresh(tokens, time.Now().UTC()) {
		refreshed, err := refreshMCPRemoteOAuthToken(ctx, opts.httpClient, tokens)
		if err != nil {
			return "", err
		}
		if err := store.SaveOAuthTokens(ctx, ref, refreshed); err != nil {
			return "", err
		}
		tokens = refreshed
	}
	return strings.TrimSpace(tokens.AccessToken), nil
}

func loadMCPRemoteStoredTokens(ctx context.Context, opts *options, name string) (credentials.OAuthTokens, bool, error) {
	store, err := opts.credentials()
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	tokens, err := store.LoadOAuthTokens(ctx, mcpRemoteCredentialRef(opts, name))
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.OAuthTokens{}, false, nil
		}
		return credentials.OAuthTokens{}, false, err
	}
	return tokens, true, nil
}

func mcpRemoteOAuthTokenNeedsRefresh(tokens credentials.OAuthTokens, now time.Time) bool {
	if !mcpRemoteStoredTokenIsOAuth(tokens) || strings.TrimSpace(tokens.RefreshToken) == "" || tokens.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(mcpRemoteOAuthRefreshSkew).Before(tokens.ExpiresAt)
}

func mcpRemoteStoredTokenIsOAuth(tokens credentials.OAuthTokens) bool {
	if tokens.Extra == nil {
		return false
	}
	return tokens.Extra["auth_type"] == mcpRemoteAuthTypeOAuth || strings.TrimSpace(tokens.Extra["token_endpoint"]) != ""
}

func mcpRemoteOAuthTokenExtra(entry mcpRemoteServerEntry, discovery mcpRemoteOAuthDiscovery, oauthClient mcpRemoteOAuthClient, scopes []string, existing map[string]string) map[string]string {
	extra := cloneMCPRemoteStringMap(existing)
	if extra == nil {
		extra = map[string]string{}
	}
	extra["auth_type"] = mcpRemoteAuthTypeOAuth
	extra["mcp_server"] = entry.Name
	extra["url"] = entry.Server.URL
	extra["resource"] = discovery.ResourceURI
	extra["authorization_server"] = discovery.AuthorizationID
	extra["issuer"] = discovery.Authorization.Issuer
	extra["authorization_endpoint"] = discovery.Authorization.AuthorizationEndpoint
	extra["token_endpoint"] = discovery.Authorization.TokenEndpoint
	extra["client_id"] = oauthClient.ClientID
	if oauthClient.ClientSecret != "" {
		extra["client_secret"] = oauthClient.ClientSecret
	}
	if len(scopes) > 0 {
		extra["scope"] = strings.Join(scopes, " ")
	}
	return extra
}

func cleanMCPRemoteOAuthScopes(values []string) []string {
	seen := map[string]bool{}
	var scopes []string
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			scopes = append(scopes, part)
		}
	}
	return scopes
}

func defaultMCPRemoteOAuthScopes(discovery mcpRemoteOAuthDiscovery) []string {
	if scopes := cleanMCPRemoteOAuthScopes(discovery.Resource.ScopesSupported); len(scopes) > 0 {
		return scopes
	}
	return cleanMCPRemoteOAuthScopes(discovery.Authorization.ScopesSupported)
}

func cloneMCPRemoteStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		if strings.TrimSpace(key) == "" {
			continue
		}
		clone[key] = item
	}
	return clone
}
