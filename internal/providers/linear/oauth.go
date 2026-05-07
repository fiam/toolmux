package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	DefaultAuthorizeURL = "https://linear.app/oauth/authorize"
	DefaultTokenURL     = "https://api.linear.app/oauth/token" // #nosec G101 -- OAuth token endpoint URL, not a credential.
	DefaultRevokeURL    = "https://api.linear.app/oauth/revoke"

	ScopeRead           = "read"
	ScopeIssuesCreate   = "issues:create"
	ScopeCommentsCreate = "comments:create"
)

var DefaultScopes = []string{ScopeRead, ScopeIssuesCreate, ScopeCommentsCreate}

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AuthorizeURL string
	TokenURL     string
	RevokeURL    string
}

type TokenSet struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	Scopes       []string `json:"scope"`
}

type tokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	Scopes       scopeList `json:"scope"`
}

type scopeList []string

func (s *scopeList) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*s = splitScopes(asString)
		return nil
	}
	var asList []string
	if err := json.Unmarshal(data, &asList); err == nil {
		*s = asList
		return nil
	}
	return fmt.Errorf("linear scope must be a string or string array")
}

func splitScopes(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == ','
	})
}

func (cfg OAuthConfig) AuthorizationURL(state, codeChallenge string, scopes []string) (string, error) {
	base := cfg.authorizeURL()
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	values := u.Query()
	values.Set("client_id", cfg.ClientID)
	values.Set("redirect_uri", cfg.RedirectURI)
	values.Set("response_type", "code")
	values.Set("state", state)
	values.Set("scope", strings.Join(scopes, ","))
	if codeChallenge != "" {
		values.Set("code_challenge", codeChallenge)
		values.Set("code_challenge_method", "S256")
	}
	u.RawQuery = values.Encode()
	return u.String(), nil
}

func (cfg OAuthConfig) ExchangePKCE(ctx context.Context, client *http.Client, code, verifier string) (TokenSet, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	}
	if cfg.ClientSecret != "" {
		values.Set("client_secret", cfg.ClientSecret)
	}
	return cfg.postToken(ctx, client, values)
}

func (cfg OAuthConfig) Refresh(ctx context.Context, client *http.Client, refreshToken string) (TokenSet, error) {
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {cfg.ClientID},
	}
	if cfg.ClientSecret != "" {
		values.Set("client_secret", cfg.ClientSecret)
	}
	return cfg.postToken(ctx, client, values)
}

func (cfg OAuthConfig) Revoke(ctx context.Context, client *http.Client, token, hint string) error {
	if client == nil {
		client = http.DefaultClient
	}
	values := url.Values{"token": {token}}
	if hint != "" {
		values.Set("token_type_hint", hint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.revokeURL(), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("linear revoke failed: status %d", resp.StatusCode)
	}
	return nil
}

func (cfg OAuthConfig) postToken(ctx context.Context, client *http.Client, values url.Values) (TokenSet, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.tokenURL(), strings.NewReader(values.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return TokenSet{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return TokenSet{}, fmt.Errorf("linear token request failed: status %d", resp.StatusCode)
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return TokenSet{}, err
	}
	return TokenSet{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresIn:    token.ExpiresIn,
		Scopes:       token.Scopes,
	}, nil
}

func (cfg OAuthConfig) authorizeURL() string {
	if cfg.AuthorizeURL != "" {
		return cfg.AuthorizeURL
	}
	return DefaultAuthorizeURL
}

func (cfg OAuthConfig) tokenURL() string {
	if cfg.TokenURL != "" {
		return cfg.TokenURL
	}
	return DefaultTokenURL
}

func (cfg OAuthConfig) revokeURL() string {
	if cfg.RevokeURL != "" {
		return cfg.RevokeURL
	}
	return DefaultRevokeURL
}
