package broker

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/brokers"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

func init() {
	brokers.Register(Descriptor())
}

func Descriptor() brokers.Descriptor {
	// #nosec G101 -- descriptor values are environment variable names, endpoint
	// URLs, and OAuth scope names, not credential values.
	return brokers.Descriptor{
		ID:               actions.ProviderName("google"),
		DisplayName:      "Google",
		Logo:             "G",
		ClientIDEnv:      "GOOGLE_CLIENT_ID",
		SecretEnv:        "GOOGLE_CLIENT_SECRET",
		AuthURLEnv:       "GOOGLE_AUTH_URL",
		TokenURLEnv:      "GOOGLE_TOKEN_URL",
		RevokeURLEnv:     "GOOGLE_REVOKE_URL",
		RedirectURIEnv:   "GOOGLE_REDIRECT_URI",
		ScopesEnv:        "GOOGLE_SCOPES",
		DefaultAuthURL:   googleapi.DefaultAuthURL,
		DefaultTokenURL:  googleapi.DefaultTokenURL,
		DefaultRevokeURL: googleapi.DefaultRevokeURL,
		DefaultScopes: []string{
			googleapi.ScopeDriveFile,
		},
		ExtraEnv: map[string]string{
			pickerRedirectURIConfig: "GOOGLE_PICKER_REDIRECT_URI",
		},
		NewOAuthProvider: func(config brokers.Config) brokers.OAuthProvider {
			return Provider{config: config}
		},
		RegisterHTTP: RegisterPickerHTTP,
	}
}

type Provider struct {
	config brokers.Config
}

func (p Provider) RequireConfig() error {
	if strings.TrimSpace(p.config.ClientID) == "" {
		return fmt.Errorf("google OAuth broker is missing GOOGLE_CLIENT_ID")
	}
	if strings.TrimSpace(p.config.Secret) == "" {
		return fmt.Errorf("google OAuth broker is missing GOOGLE_CLIENT_SECRET")
	}
	if strings.TrimSpace(p.config.AuthURL) == "" {
		return fmt.Errorf("google OAuth broker is missing an authorization URL")
	}
	if strings.TrimSpace(p.config.TokenURL) == "" {
		return fmt.Errorf("google OAuth broker is missing a token URL")
	}
	return nil
}

func (p Provider) AuthURL(redirectURI, state string, scopes []string) (string, error) {
	requestedScopes := scopes
	if len(requestedScopes) == 0 {
		requestedScopes = p.config.Scopes
	}
	return googleapi.OAuthAuthorizeURL(googleapi.OAuthOptions{
		AuthURL:     p.config.AuthURL,
		ClientID:    p.config.ClientID,
		RedirectURI: redirectURI,
		Scopes:      requestedScopes,
	}, state)
}

func (p Provider) ExchangeCode(ctx context.Context, code, redirectURI string) (credentials.OAuthTokens, error) {
	return googleapi.ExchangeOAuthCode(ctx, p.httpClient(), googleapi.OAuthOptions{
		TokenURL:     p.config.TokenURL,
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.Secret,
		RedirectURI:  redirectURI,
	}, code, time.Now())
}

func (p Provider) Refresh(ctx context.Context, refreshToken string) (credentials.OAuthTokens, error) {
	return googleapi.RefreshOAuthToken(ctx, p.httpClient(), googleapi.OAuthOptions{
		TokenURL:     p.config.TokenURL,
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.Secret,
	}, refreshToken, time.Now())
}

func (p Provider) Revoke(ctx context.Context, token string) (brokers.RevokeResult, error) {
	if err := googleapi.RevokeOAuthToken(ctx, p.httpClient(), p.config.RevokeURL, token); err != nil {
		return brokers.RevokeResult{}, err
	}
	return brokers.RevokeResult{Revoked: true}, nil
}

func (p Provider) httpClient() *http.Client {
	if p.config.HTTPClient != nil {
		return p.config.HTTPClient
	}
	return http.DefaultClient
}
