package client

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

func googleClient(exec actions.Context, inv actions.Invocation, requiredScopes []string) (googleapi.Client, error) {
	tokens, err := googleTokens(exec, inv, requiredScopes)
	if err != nil {
		return googleapi.Client{}, err
	}
	return googleapi.Client{
		BaseURL:     exec.ProviderURL,
		AccessToken: tokens.AccessToken,
		HTTPClient:  exec.HTTPClient,
	}, nil
}

func googleTokens(exec actions.Context, inv actions.Invocation, requiredScopes []string) (credentials.OAuthTokens, error) {
	accountName := exec.AccountName()
	ref := googleCredentialRef(exec, accountName)
	tokens, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	if !found {
		return credentials.OAuthTokens{}, fmt.Errorf("google toolbox %q is not authorized; run `toolmux add google --name %s`", exec.Provider, accountName)
	}
	if missing := oauthbroker.MissingScopes(tokens.Scopes, requiredScopes); len(missing) > 0 {
		return credentials.OAuthTokens{}, fmt.Errorf("%s requires missing Google OAuth scope(s): %s; run `toolmux add google --name %s`", exec.Provider, strings.Join(missing, ", "), accountName)
	}
	if googleTokenNeedsRefresh(tokens, time.Now().UTC()) {
		authType := firstNonEmpty(tokens.Extra["auth_type"], authTypeBroker)
		refreshed, err := refreshGoogleTokens(exec, tokens)
		if err != nil {
			return credentials.OAuthTokens{}, err
		}
		tokens = oauthbroker.MergeTokens(tokens, refreshed, tokens.Scopes)
		tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
			"auth_type": authType,
		})
		if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, tokens); err != nil {
			return credentials.OAuthTokens{}, err
		}
	}
	return tokens, nil
}

func refreshGoogleTokens(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("google OAuth token is expired and has no refresh token")
	}
	brokerURL := firstNonEmpty(tokens.Extra["broker_url"], exec.ToolmuxdURL)
	return oauthbroker.Client{
		BaseURL:    brokerURL,
		HTTPClient: exec.HTTPClient,
	}.Refresh(exec.Context, sharedProviderID, tokens.RefreshToken)
}

func loadGoogleTokens(exec actions.Context, ref credentials.ConnectionRef) (credentials.OAuthTokens, bool, error) {
	tokens, err := exec.Credentials.LoadOAuthTokens(exec.Context, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.OAuthTokens{}, false, nil
		}
		return credentials.OAuthTokens{}, false, err
	}
	if tokens.Extra == nil {
		tokens.Extra = map[string]string{}
	}
	return tokens, true, nil
}

func googleTokenNeedsRefresh(tokens credentials.OAuthTokens, now time.Time) bool {
	if tokens.ExpiresAt.IsZero() || strings.TrimSpace(tokens.RefreshToken) == "" {
		return false
	}
	return !now.Add(oauthRefreshSkew).Before(tokens.ExpiresAt)
}

func createBrokerSession(exec actions.Context, account string, scopes []string) (oauthbroker.Session, error) {
	return brokerClient(exec).CreateSession(exec.Context, sharedProviderID, exec.Profile, account, scopes)
}

func brokerClient(exec actions.Context) oauthbroker.Client {
	return oauthbroker.Client{
		BaseURL:    exec.ToolmuxdURL,
		HTTPClient: exec.HTTPClient,
	}
}

func googleCredentialRef(exec actions.Context, account string) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   exec.Profile,
		Provider:  sharedProviderID,
		AccountID: account,
	}
}
