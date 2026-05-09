package broker

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
	"github.com/fiam/toolmux/internal/providers/brokers"
	"github.com/fiam/toolmux/internal/providers/notion"
)

const ProviderID = string(notion.ProviderName)

const DefaultVersion = notion.DefaultVersion

type Config = brokers.Config

type Broker struct {
	config Config
}

type RevokeResponse = brokers.RevokeResult

type tokenRequest struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type tokenResponse struct {
	AccessToken          string `json:"access_token"`
	RefreshToken         string `json:"refresh_token"`
	TokenType            string `json:"token_type"`
	ExpiresIn            int64  `json:"expires_in"`
	BotID                string `json:"bot_id"`
	WorkspaceID          string `json:"workspace_id"`
	WorkspaceName        string `json:"workspace_name"`
	WorkspaceIcon        string `json:"workspace_icon"`
	DuplicatedTemplateID string `json:"duplicated_template_id"`
}

func init() {
	brokers.Register(Descriptor())
}

func Descriptor() brokers.Descriptor {
	// #nosec G101 -- these are environment variable names and public OAuth endpoint defaults, not credentials.
	return brokers.Descriptor{
		ID:                notion.ProviderName,
		DisplayName:       "Notion",
		Logo:              "N",
		ClientIDEnv:       "NOTION_CLIENT_ID",
		SecretEnv:         "NOTION_CLIENT_SECRET",
		AuthURLEnv:        "NOTION_AUTH_URL",
		TokenURLEnv:       "NOTION_TOKEN_URL",
		RevokeURLEnv:      "NOTION_REVOKE_URL",
		RedirectURIEnv:    "NOTION_REDIRECT_URI",
		APIVersionEnv:     "TOOLMUX_NOTION_VERSION",
		DefaultAuthURL:    "https://api.notion.com/v1/oauth/authorize",
		DefaultTokenURL:   "https://api.notion.com/v1/oauth/token",
		DefaultRevokeURL:  "https://api.notion.com/v1/oauth/revoke",
		DefaultAPIVersion: DefaultVersion,
		NewOAuthProvider: func(config brokers.Config) brokers.OAuthProvider {
			return New(config)
		},
	}
}

func New(config Config) *Broker {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.APIVersion == "" {
		config.APIVersion = DefaultVersion
	}
	return &Broker{config: config}
}

func DefaultCapabilities() []string {
	return notion.DefaultCapabilities()
}

func (b *Broker) RequireConfig() error {
	if strings.TrimSpace(b.config.ClientID) == "" {
		return fmt.Errorf("NOTION_CLIENT_ID is required")
	}
	if strings.TrimSpace(b.config.Secret) == "" {
		return fmt.Errorf("NOTION_CLIENT_SECRET is required")
	}
	if strings.TrimSpace(b.config.AuthURL) == "" {
		return fmt.Errorf("NOTION_AUTH_URL is required")
	}
	if strings.TrimSpace(b.config.TokenURL) == "" {
		return fmt.Errorf("NOTION_TOKEN_URL is required")
	}
	return nil
}

func (b *Broker) AuthURL(redirectURI, state string) (string, error) {
	authURL, err := url.Parse(b.config.AuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid Notion authorization URL")
	}
	query := authURL.Query()
	query.Set("client_id", b.config.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("owner", "user")
	query.Set("state", state)
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

func (b *Broker) ExchangeCode(ctx context.Context, code, redirectURI string) (credentials.OAuthTokens, error) {
	return b.postToken(ctx, tokenRequest{
		GrantType:   "authorization_code",
		Code:        code,
		RedirectURI: redirectURI,
	})
}

func (b *Broker) Refresh(ctx context.Context, refreshToken string) (credentials.OAuthTokens, error) {
	return b.postToken(ctx, tokenRequest{
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	})
}

func (b *Broker) Revoke(ctx context.Context, token string) (RevokeResponse, error) {
	encoded, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return RevokeResponse{}, err
	}
	// #nosec G107 -- Notion revoke URL is configured by the deployment operator.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.config.RevokeURL, bytes.NewReader(encoded))
	if err != nil {
		return RevokeResponse{}, err
	}
	b.setAuthHeaders(req)
	resp, err := b.config.HTTPClient.Do(req)
	if err != nil {
		return RevokeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return RevokeResponse{}, fmt.Errorf("notion revoke endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out RevokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RevokeResponse{Revoked: true}, nil
	}
	return out, nil
}

func (b *Broker) postToken(ctx context.Context, body tokenRequest) (credentials.OAuthTokens, error) {
	// #nosec G117 -- refresh tokens must be sent to Notion's token endpoint for OAuth refresh.
	encoded, err := json.Marshal(body)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	// #nosec G107 -- Notion token URL is configured by the deployment operator.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.config.TokenURL, bytes.NewReader(encoded))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	b.setAuthHeaders(req)
	resp, err := b.config.HTTPClient.Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return credentials.OAuthTokens{}, fmt.Errorf("notion token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return token.credentials(), nil
}

func (b *Broker) setAuthHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", b.config.APIVersion)
	req.SetBasicAuth(b.config.ClientID, b.config.Secret)
}

func (t tokenResponse) credentials() credentials.OAuthTokens {
	extra := map[string]string{}
	if t.BotID != "" {
		extra["bot_id"] = t.BotID
	}
	if t.WorkspaceID != "" {
		extra["workspace_id"] = t.WorkspaceID
	}
	if t.WorkspaceName != "" {
		extra["workspace_name"] = t.WorkspaceName
	}
	if t.WorkspaceIcon != "" {
		extra["workspace_icon"] = t.WorkspaceIcon
	}
	if t.DuplicatedTemplateID != "" {
		extra["duplicated_template_id"] = t.DuplicatedTemplateID
	}
	tokens := credentials.OAuthTokens{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    firstNonEmpty(t.TokenType, "bearer"),
		Scopes:       DefaultCapabilities(),
		Extra:        extra,
	}
	if t.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return tokens
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
