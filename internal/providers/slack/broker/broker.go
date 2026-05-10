package broker

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
	"github.com/fiam/toolmux/internal/providers/brokers"
	"github.com/fiam/toolmux/internal/providers/slack"
)

const ProviderID = string(slack.ProviderName)

type Config = brokers.Config

type Broker struct {
	config Config
}

type RevokeResponse = brokers.RevokeResult

type tokenResponse struct {
	OK                  bool        `json:"ok"`
	Error               string      `json:"error,omitempty"`
	AccessToken         string      `json:"access_token,omitempty"`
	RefreshToken        string      `json:"refresh_token,omitempty"`
	TokenType           string      `json:"token_type,omitempty"`
	Scope               string      `json:"scope,omitempty"`
	ExpiresIn           int64       `json:"expires_in,omitempty"`
	Team                slackObject `json:"team,omitzero"`
	Enterprise          slackObject `json:"enterprise,omitzero"`
	AuthedUser          authedUser  `json:"authed_user,omitzero"`
	IsEnterpriseInstall bool        `json:"is_enterprise_install,omitempty"`
}

type slackObject struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type authedUser struct {
	ID           string `json:"id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
}

func init() {
	brokers.Register(Descriptor())
}

func Descriptor() brokers.Descriptor {
	// #nosec G101 -- these are environment variable names and public OAuth endpoint defaults, not credentials.
	return brokers.Descriptor{
		ID:               slack.ProviderName,
		DisplayName:      "Slack",
		Logo:             "S",
		ClientIDEnv:      "SLACK_CLIENT_ID",
		SecretEnv:        "SLACK_CLIENT_SECRET",
		AuthURLEnv:       "SLACK_AUTH_URL",
		TokenURLEnv:      "SLACK_TOKEN_URL",
		RevokeURLEnv:     "SLACK_REVOKE_URL",
		RedirectURIEnv:   "SLACK_REDIRECT_URI",
		DefaultAuthURL:   "https://slack.com/oauth/v2/authorize",
		DefaultTokenURL:  "https://slack.com/api/oauth.v2.access",
		DefaultRevokeURL: "https://slack.com/api/auth.revoke",
		NewOAuthProvider: func(config brokers.Config) brokers.OAuthProvider {
			return New(config)
		},
	}
}

func New(config Config) *Broker {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return &Broker{config: config}
}

func (b *Broker) RequireConfig() error {
	if strings.TrimSpace(b.config.ClientID) == "" {
		return fmt.Errorf("SLACK_CLIENT_ID is required")
	}
	if strings.TrimSpace(b.config.Secret) == "" {
		return fmt.Errorf("SLACK_CLIENT_SECRET is required")
	}
	if strings.TrimSpace(b.config.AuthURL) == "" {
		return fmt.Errorf("SLACK_AUTH_URL is required")
	}
	if strings.TrimSpace(b.config.TokenURL) == "" {
		return fmt.Errorf("SLACK_TOKEN_URL is required")
	}
	return nil
}

func (b *Broker) AuthURL(redirectURI, state string) (string, error) {
	authURL, err := url.Parse(b.config.AuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid Slack authorization URL")
	}
	query := authURL.Query()
	query.Set("client_id", b.config.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("user_scope", strings.Join(slack.DefaultCapabilities(), ","))
	query.Set("state", state)
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

func (b *Broker) ExchangeCode(ctx context.Context, code, redirectURI string) (credentials.OAuthTokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	return b.postToken(ctx, form)
}

func (b *Broker) Refresh(ctx context.Context, refreshToken string) (credentials.OAuthTokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return b.postToken(ctx, form)
}

func (b *Broker) Revoke(ctx context.Context, token string) (RevokeResponse, error) {
	// #nosec G107 -- Slack revoke URL is configured by the deployment operator.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.config.RevokeURL, nil)
	if err != nil {
		return RevokeResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := b.config.HTTPClient.Do(req)
	if err != nil {
		return RevokeResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return RevokeResponse{}, fmt.Errorf("slack revoke endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return RevokeResponse{Revoked: true}, nil
	}
	if !decoded.OK {
		return RevokeResponse{}, fmt.Errorf("slack revoke endpoint returned %s", firstNonEmpty(decoded.Error, "not_ok"))
	}
	return RevokeResponse{Revoked: true}, nil
}

func (b *Broker) postToken(ctx context.Context, form url.Values) (credentials.OAuthTokens, error) {
	// #nosec G107 -- Slack token URL is configured by the deployment operator.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(b.config.ClientID, b.config.Secret)
	resp, err := b.config.HTTPClient.Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return credentials.OAuthTokens{}, fmt.Errorf("slack token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var token tokenResponse
	if err := json.Unmarshal(data, &token); err != nil {
		return credentials.OAuthTokens{}, err
	}
	if !token.OK {
		return credentials.OAuthTokens{}, fmt.Errorf("slack token endpoint returned %s", firstNonEmpty(token.Error, "not_ok"))
	}
	return token.credentials(), nil
}

func (t tokenResponse) credentials() credentials.OAuthTokens {
	accessToken := firstNonEmpty(t.AuthedUser.AccessToken, t.AccessToken)
	refreshToken := firstNonEmpty(t.AuthedUser.RefreshToken, t.RefreshToken)
	tokenType := firstNonEmpty(t.AuthedUser.TokenType, t.TokenType, "bearer")
	scopes := splitScopes(firstNonEmpty(t.AuthedUser.Scope, t.Scope))
	expiresIn := firstNonZero(t.AuthedUser.ExpiresIn, t.ExpiresIn)
	extra := map[string]string{}
	if t.Team.ID != "" {
		extra["workspace_id"] = t.Team.ID
		extra["team_id"] = t.Team.ID
	}
	if t.Team.Name != "" {
		extra["workspace_name"] = t.Team.Name
		extra["team_name"] = t.Team.Name
	}
	if t.Enterprise.ID != "" {
		extra["enterprise_id"] = t.Enterprise.ID
	}
	if t.Enterprise.Name != "" {
		extra["enterprise_name"] = t.Enterprise.Name
	}
	if t.AuthedUser.ID != "" {
		extra["user_id"] = t.AuthedUser.ID
	}
	tokens := credentials.OAuthTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		Scopes:       scopes,
		Extra:        extra,
	}
	if expiresIn > 0 {
		tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(expiresIn) * time.Second)
	}
	return tokens
}

func splitScopes(value string) []string {
	var scopes []string
	for _, scope := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' '
	}) {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return slack.DefaultCapabilities()
	}
	return scopes
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
