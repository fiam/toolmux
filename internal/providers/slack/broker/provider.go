package broker

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/brokers"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

func init() {
	brokers.Register(Descriptor())
}

func Descriptor() brokers.Descriptor {
	// #nosec G101 -- descriptor values are environment variable names, endpoint
	// URLs, and OAuth scope names, not credential values.
	return brokers.Descriptor{
		ID:               actions.ProviderName("slack"),
		DisplayName:      "Slack",
		Logo:             "S",
		ClientIDEnv:      "SLACK_CLIENT_ID",
		SecretEnv:        "SLACK_CLIENT_SECRET",
		AuthURLEnv:       "SLACK_AUTH_URL",
		TokenURLEnv:      "SLACK_TOKEN_URL",
		RevokeURLEnv:     "SLACK_REVOKE_URL",
		RedirectURIEnv:   "SLACK_REDIRECT_URI",
		ScopesEnv:        "SLACK_SCOPES",
		DefaultAuthURL:   slackapi.DefaultAuthURL,
		DefaultTokenURL:  slackapi.DefaultTokenURL,
		DefaultRevokeURL: slackapi.DefaultRevokeURL,
		DefaultScopes: []string{
			"channels:read",
			"groups:read",
			"im:read",
			"mpim:read",
			"channels:history",
			"groups:history",
			"im:history",
			"mpim:history",
			"chat:write",
			"reactions:write",
			"files:read",
			"users:read",
			"usergroups:read",
			"usergroups:write",
			"search:read",
		},
		NewOAuthProvider: func(config brokers.Config) brokers.OAuthProvider {
			return Provider{config: config}
		},
	}
}

type Provider struct {
	config brokers.Config
}

func (p Provider) RequireConfig() error {
	if strings.TrimSpace(p.config.ClientID) == "" {
		return fmt.Errorf("slack OAuth broker is missing SLACK_CLIENT_ID")
	}
	if strings.TrimSpace(p.config.Secret) == "" {
		return fmt.Errorf("slack OAuth broker is missing SLACK_CLIENT_SECRET")
	}
	if strings.TrimSpace(p.config.AuthURL) == "" {
		return fmt.Errorf("slack OAuth broker is missing an authorization URL")
	}
	if strings.TrimSpace(p.config.TokenURL) == "" {
		return fmt.Errorf("slack OAuth broker is missing a token URL")
	}
	return nil
}

func (p Provider) AuthURL(redirectURI, state string, scopes []string) (string, error) {
	requestedScopes := slackapi.CleanScopes(scopes)
	if len(requestedScopes) == 0 {
		requestedScopes = slices.Clone(p.config.Scopes)
	}
	return slackapi.OAuthAuthorizeURL(slackapi.OAuthOptions{
		AuthURL:     p.config.AuthURL,
		ClientID:    p.config.ClientID,
		RedirectURI: redirectURI,
		Scopes:      requestedScopes,
	}, state)
}

func (p Provider) ExchangeCode(ctx context.Context, code, redirectURI string) (credentials.OAuthTokens, error) {
	return slackapi.ExchangeOAuthCode(ctx, p.httpClient(), slackapi.OAuthOptions{
		TokenURL:     p.config.TokenURL,
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.Secret,
		RedirectURI:  redirectURI,
	}, code, time.Now())
}

func (p Provider) Refresh(ctx context.Context, refreshToken string) (credentials.OAuthTokens, error) {
	return slackapi.RefreshOAuthToken(ctx, p.httpClient(), slackapi.OAuthOptions{
		TokenURL:     p.config.TokenURL,
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.Secret,
	}, refreshToken, time.Now())
}

func (p Provider) Revoke(ctx context.Context, token string) (brokers.RevokeResult, error) {
	result, err := slackapi.RevokeOAuthToken(ctx, p.httpClient(), p.config.RevokeURL, token)
	if err != nil {
		return brokers.RevokeResult{}, err
	}
	return brokers.RevokeResult{Revoked: result.Revoked}, nil
}

func (p Provider) httpClient() *http.Client {
	if p.config.HTTPClient != nil {
		return p.config.HTTPClient
	}
	return http.DefaultClient
}
