package brokers

import (
	"context"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/fiam/supacli/internal/actions"
	"github.com/fiam/supacli/internal/credentials"
)

type Config struct {
	ClientID    string
	Secret      string
	AuthURL     string
	TokenURL    string
	RevokeURL   string
	RedirectURI string
	APIVersion  string
	HTTPClient  *http.Client
}

type RevokeResult struct {
	RequestID string `json:"request_id,omitempty"`
	Revoked   bool   `json:"revoked,omitempty"`
}

type OAuthProvider interface {
	RequireConfig() error
	AuthURL(redirectURI, state string) (string, error)
	ExchangeCode(ctx context.Context, code, redirectURI string) (credentials.OAuthTokens, error)
	Refresh(ctx context.Context, refreshToken string) (credentials.OAuthTokens, error)
	Revoke(ctx context.Context, token string) (RevokeResult, error)
}

type Descriptor struct {
	ID                actions.ProviderName
	DisplayName       string
	Logo              string
	ClientIDEnv       string
	SecretEnv         string
	AuthURLEnv        string
	TokenURLEnv       string
	RevokeURLEnv      string
	RedirectURIEnv    string
	APIVersionEnv     string
	DefaultAuthURL    string
	DefaultTokenURL   string
	DefaultRevokeURL  string
	DefaultAPIVersion string
	NewOAuthProvider  func(Config) OAuthProvider
}

var registry = struct {
	sync.RWMutex
	byID map[actions.ProviderName]Descriptor
}{
	byID: map[actions.ProviderName]Descriptor{},
}

func Register(descriptor Descriptor) {
	descriptor = normalizeDescriptor(descriptor)
	if descriptor.ID == "" {
		panic("broker provider id is required")
	}
	if descriptor.DisplayName == "" {
		panic("broker display name is required for " + string(descriptor.ID))
	}
	if descriptor.NewOAuthProvider == nil {
		panic("broker OAuth factory is required for " + string(descriptor.ID))
	}

	registry.Lock()
	defer registry.Unlock()
	if _, ok := registry.byID[descriptor.ID]; ok {
		panic("broker provider already registered: " + string(descriptor.ID))
	}
	registry.byID[descriptor.ID] = descriptor
}

func Lookup(id actions.ProviderName) (Descriptor, bool) {
	registry.RLock()
	defer registry.RUnlock()
	descriptor, ok := registry.byID[id]
	return descriptor, ok
}

func All() []Descriptor {
	registry.RLock()
	defer registry.RUnlock()
	descriptors := make([]Descriptor, 0, len(registry.byID))
	for _, descriptor := range registry.byID {
		descriptors = append(descriptors, descriptor)
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].ID < descriptors[j].ID
	})
	return descriptors
}

func (d Descriptor) CompleteConfig(config Config, httpClient *http.Client) Config {
	if config.ClientID == "" {
		config.ClientID = strings.TrimSpace(os.Getenv(d.ClientIDEnv))
	}
	if config.Secret == "" {
		config.Secret = strings.TrimSpace(os.Getenv(d.SecretEnv))
	}
	if config.AuthURL == "" {
		config.AuthURL = firstNonEmpty(os.Getenv(d.AuthURLEnv), d.DefaultAuthURL)
	}
	if config.TokenURL == "" {
		config.TokenURL = firstNonEmpty(os.Getenv(d.TokenURLEnv), d.DefaultTokenURL)
	}
	if config.RevokeURL == "" {
		config.RevokeURL = firstNonEmpty(os.Getenv(d.RevokeURLEnv), d.DefaultRevokeURL)
	}
	if config.RedirectURI == "" {
		config.RedirectURI = strings.TrimSpace(os.Getenv(d.RedirectURIEnv))
	}
	if config.APIVersion == "" {
		config.APIVersion = firstNonEmpty(os.Getenv(d.APIVersionEnv), d.DefaultAPIVersion)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = httpClient
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return config
}

func normalizeDescriptor(descriptor Descriptor) Descriptor {
	descriptor.ID = actions.ProviderName(strings.TrimSpace(string(descriptor.ID)))
	descriptor.DisplayName = strings.TrimSpace(descriptor.DisplayName)
	descriptor.Logo = strings.TrimSpace(descriptor.Logo)
	if descriptor.Logo == "" && descriptor.DisplayName != "" {
		descriptor.Logo = strings.ToUpper(descriptor.DisplayName[:1])
	}
	envs := []*string{
		&descriptor.ClientIDEnv,
		&descriptor.SecretEnv,
		&descriptor.AuthURLEnv,
		&descriptor.TokenURLEnv,
		&descriptor.RevokeURLEnv,
		&descriptor.RedirectURIEnv,
		&descriptor.APIVersionEnv,
	}
	for _, env := range envs {
		*env = strings.TrimSpace(*env)
	}
	defaults := []*string{
		&descriptor.DefaultAuthURL,
		&descriptor.DefaultTokenURL,
		&descriptor.DefaultRevokeURL,
		&descriptor.DefaultAPIVersion,
	}
	for _, value := range defaults {
		*value = strings.TrimSpace(*value)
	}
	return descriptor
}

func RegisteredIDs() []actions.ProviderName {
	descriptors := All()
	ids := make([]actions.ProviderName, 0, len(descriptors))
	for _, descriptor := range descriptors {
		ids = append(ids, descriptor.ID)
	}
	return slices.Clip(ids)
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
