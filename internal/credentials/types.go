package credentials

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultProfile     = "default"
	DefaultServiceName = "toolmux"
)

var (
	ErrInvalidRef    = errors.New("invalid credential reference")
	ErrInvalidTokens = errors.New("invalid OAuth token bundle")
	ErrNotFound      = errors.New("credential not found")
	ErrUnavailable   = errors.New("credential store unavailable")
)

type ConnectionRef struct {
	Profile   string `json:"profile"`
	Provider  string `json:"provider"`
	Service   string `json:"service,omitempty"`
	AccountID string `json:"account_id"`
}

type OAuthTokens struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	TokenType    string            `json:"token_type,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitzero"`
	Scopes       []string          `json:"scopes,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

type Store interface {
	SaveOAuthTokens(ctx context.Context, ref ConnectionRef, tokens OAuthTokens) error
	HasOAuthTokens(ctx context.Context, ref ConnectionRef) (bool, error)
	LoadOAuthTokens(ctx context.Context, ref ConnectionRef) (OAuthTokens, error)
	DeleteOAuthTokens(ctx context.Context, ref ConnectionRef) error
	Doctor(ctx context.Context) Diagnostics
}

type Diagnostics struct {
	Available bool              `json:"available"`
	Service   string            `json:"service"`
	Backend   string            `json:"backend,omitempty"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
}

func NormalizeRef(ref ConnectionRef) (ConnectionRef, error) {
	profile, err := cleanComponent("profile", ref.Profile, false)
	if err != nil {
		return ConnectionRef{}, err
	}
	if profile == "" {
		profile = DefaultProfile
	}
	provider, err := cleanComponent("provider", ref.Provider, true)
	if err != nil {
		return ConnectionRef{}, err
	}
	if provider == "" {
		return ConnectionRef{}, fmt.Errorf("%w: provider is required", ErrInvalidRef)
	}
	service, err := cleanComponent("service", ref.Service, true)
	if err != nil {
		return ConnectionRef{}, err
	}
	if service == "" {
		service = provider
	}
	accountID, err := cleanComponent("account id", ref.AccountID, false)
	if err != nil {
		return ConnectionRef{}, err
	}
	if accountID == "" {
		return ConnectionRef{}, fmt.Errorf("%w: account id is required", ErrInvalidRef)
	}
	return ConnectionRef{
		Profile:   profile,
		Provider:  provider,
		Service:   service,
		AccountID: accountID,
	}, nil
}

func (ref ConnectionRef) Validate() error {
	_, err := NormalizeRef(ref)
	return err
}

func (ref ConnectionRef) Display() string {
	normalized, err := NormalizeRef(ref)
	if err != nil {
		return "<invalid>"
	}
	return fmt.Sprintf("%s/%s/%s/%s",
		normalized.Profile,
		normalized.Provider,
		normalized.Service,
		normalized.AccountID,
	)
}

func NormalizeOAuthTokens(tokens OAuthTokens) (OAuthTokens, error) {
	accessToken := strings.TrimSpace(tokens.AccessToken)
	if accessToken == "" {
		return OAuthTokens{}, fmt.Errorf("%w: access token is required", ErrInvalidTokens)
	}
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	tokenType := strings.TrimSpace(tokens.TokenType)
	scopes := cleanScopes(tokens.Scopes)
	extra := cloneStringMap(tokens.Extra)
	return OAuthTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		ExpiresAt:    tokens.ExpiresAt.UTC(),
		Scopes:       scopes,
		Extra:        extra,
	}, nil
}

func (tokens OAuthTokens) Validate() error {
	_, err := NormalizeOAuthTokens(tokens)
	return err
}

func cloneOAuthTokens(tokens OAuthTokens) OAuthTokens {
	return OAuthTokens{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    tokens.TokenType,
		ExpiresAt:    tokens.ExpiresAt,
		Scopes:       slices.Clone(tokens.Scopes),
		Extra:        cloneStringMap(tokens.Extra),
	}
}

func oauthTokensKey(ref ConnectionRef) (string, error) {
	normalized, err := NormalizeRef(ref)
	if err != nil {
		return "", err
	}
	segments := []string{
		"profile", normalized.Profile,
		"provider", normalized.Provider,
		"service", normalized.Service,
		"account", normalized.AccountID,
		"oauth",
	}
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	return strings.Join(escaped, "/"), nil
}

func cleanComponent(name, value string, lower bool) (string, error) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return "", nil
	}
	if !utf8.ValidString(cleaned) {
		return "", fmt.Errorf("%w: %s must be valid UTF-8", ErrInvalidRef, name)
	}
	if len(cleaned) > 512 {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidRef, name)
	}
	for _, r := range cleaned {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: %s contains control characters", ErrInvalidRef, name)
		}
	}
	if lower {
		cleaned = strings.ToLower(cleaned)
	}
	return cleaned, nil
}

func cleanScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(scopes))
	cleaned := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		cleaned = append(cleaned, scope)
	}
	slices.Sort(cleaned)
	return cleaned
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	clone := make(map[string]string, len(value))
	for k, v := range value {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		clone[k] = v
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}
