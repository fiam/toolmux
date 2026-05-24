package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"

	keyring "github.com/99designs/keyring"
)

const oauthEnvelopeVersion = 1

type KeyringConfig struct {
	ServiceName     string
	AllowedBackends []string
}

type KeyringStore struct {
	ring            keyring.Keyring
	serviceName     string
	allowedBackends []string
}

type oauthEnvelope struct {
	Version int         `json:"version"`
	Kind    string      `json:"kind"`
	Tokens  OAuthTokens `json:"tokens"`
}

func NewKeyringStore(cfg KeyringConfig) (*KeyringStore, error) {
	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	backendNames := cfg.AllowedBackends
	if backendNames == nil {
		backendNames = defaultBackendNames()
	}
	backendTypes, err := keyringBackendTypes(backendNames)
	if err != nil {
		return nil, err
	}
	ring, err := keyring.Open(keyring.Config{
		ServiceName:     serviceName,
		AllowedBackends: backendTypes,
	})
	if err != nil {
		return nil, mapKeyringOpenError(err)
	}
	return newKeyringStore(ring, serviceName, backendNames), nil
}

func (s *KeyringStore) SaveOAuthTokens(ctx context.Context, ref ConnectionRef, tokens OAuthTokens) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return err
	}
	normalizedRef, err := NormalizeRef(ref)
	if err != nil {
		return err
	}
	normalizedTokens, err := NormalizeOAuthTokens(tokens)
	if err != nil {
		return err
	}
	data, err := json.Marshal(oauthEnvelope{
		Version: oauthEnvelopeVersion,
		Kind:    "oauth",
		Tokens:  normalizedTokens,
	})
	if err != nil {
		return err
	}
	if err := s.ring.Set(keyring.Item{
		Key:         key,
		Data:        data,
		Label:       fmt.Sprintf("Toolmux %s OAuth tokens", normalizedRef.Provider),
		Description: fmt.Sprintf("OAuth tokens for %s", normalizedRef.Display()),
	}); err != nil {
		return fmt.Errorf("save OAuth tokens: %w", mapKeyringError(err))
	}
	return nil
}

func (s *KeyringStore) HasOAuthTokens(ctx context.Context, ref ConnectionRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return false, err
	}
	if _, err := s.ring.GetMetadata(key); err == nil {
		return true, nil
	} else if errors.Is(err, keyring.ErrKeyNotFound) {
		return false, nil
	} else if !errors.Is(err, keyring.ErrMetadataNeedsCredentials) && !errors.Is(err, keyring.ErrMetadataNotSupported) {
		return false, fmt.Errorf("check OAuth tokens: %w", mapKeyringError(err))
	}

	_, err = s.LoadOAuthTokens(ctx, ref)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (s *KeyringStore) LoadOAuthTokens(ctx context.Context, ref ConnectionRef) (OAuthTokens, error) {
	if err := ctx.Err(); err != nil {
		return OAuthTokens{}, err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return OAuthTokens{}, err
	}
	item, err := s.ring.Get(key)
	if err != nil {
		return OAuthTokens{}, fmt.Errorf("load OAuth tokens: %w", mapKeyringError(err))
	}
	var envelope oauthEnvelope
	if err := json.Unmarshal(item.Data, &envelope); err != nil {
		return OAuthTokens{}, fmt.Errorf("load OAuth tokens: decode credential: %w", err)
	}
	if envelope.Version != oauthEnvelopeVersion {
		return OAuthTokens{}, fmt.Errorf("load OAuth tokens: unsupported credential version %d", envelope.Version)
	}
	if envelope.Kind != "oauth" {
		return OAuthTokens{}, fmt.Errorf("load OAuth tokens: unexpected credential kind %q", envelope.Kind)
	}
	tokens, err := NormalizeOAuthTokens(envelope.Tokens)
	if err != nil {
		return OAuthTokens{}, fmt.Errorf("load OAuth tokens: %w", err)
	}
	return tokens, nil
}

func (s *KeyringStore) DeleteOAuthTokens(ctx context.Context, ref ConnectionRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return err
	}
	if err := s.ring.Remove(key); err != nil && !errors.Is(err, keyring.ErrKeyNotFound) {
		return fmt.Errorf("delete OAuth tokens: %w", mapKeyringError(err))
	}
	return nil
}

func (s *KeyringStore) Doctor(ctx context.Context) Diagnostics {
	if err := ctx.Err(); err != nil {
		return Diagnostics{
			Available: false,
			Service:   s.serviceName,
			Backend:   strings.Join(s.allowedBackends, ","),
			Message:   err.Error(),
		}
	}
	const key = "diagnostics/probe/oauth"
	probe := keyring.Item{
		Key:         key,
		Data:        []byte("ok"),
		Label:       "Toolmux credential store probe",
		Description: "Temporary credential store probe created by toolmux doctor",
	}
	if err := s.ring.Set(probe); err != nil {
		return s.diagnostics(false, fmt.Sprintf("credential store write failed: %v", mapKeyringError(err)))
	}
	item, err := s.ring.Get(key)
	if err != nil {
		_ = s.ring.Remove(key)
		return s.diagnostics(false, fmt.Sprintf("credential store read failed: %v", mapKeyringError(err)))
	}
	if string(item.Data) != "ok" {
		_ = s.ring.Remove(key)
		return s.diagnostics(false, "credential store probe returned unexpected data")
	}
	if err := s.ring.Remove(key); err != nil {
		return s.diagnostics(false, fmt.Sprintf("credential store delete failed: %v", mapKeyringError(err)))
	}
	return s.diagnostics(true, "OS credential store available")
}

func newKeyringStore(ring keyring.Keyring, serviceName string, allowedBackends []string) *KeyringStore {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	return &KeyringStore{
		ring:            ring,
		serviceName:     serviceName,
		allowedBackends: append([]string(nil), allowedBackends...),
	}
}

func (s *KeyringStore) diagnostics(available bool, message string) Diagnostics {
	return Diagnostics{
		Available: available,
		Service:   s.serviceName,
		Backend:   strings.Join(s.allowedBackends, ","),
		Message:   message,
	}
}

func defaultBackendNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"keychain"}
	case "windows":
		return []string{"wincred"}
	case "linux", "freebsd", "openbsd", "netbsd":
		return []string{"secret-service", "kwallet"}
	default:
		return []string{}
	}
}

func keyringBackendTypes(names []string) ([]keyring.BackendType, error) {
	types := make([]keyring.BackendType, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		switch name {
		case "secret-service":
			types = append(types, keyring.SecretServiceBackend)
		case "keychain":
			types = append(types, keyring.KeychainBackend)
		case "kwallet":
			types = append(types, keyring.KWalletBackend)
		case "wincred":
			types = append(types, keyring.WinCredBackend)
		case "pass":
			types = append(types, keyring.PassBackend)
		case "keyctl":
			types = append(types, keyring.KeyCtlBackend)
		case "file":
			types = append(types, keyring.FileBackend)
		default:
			return nil, fmt.Errorf("%w: unknown keyring backend %q", ErrUnavailable, name)
		}
	}
	return types, nil
}

func mapKeyringOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNoAvailImpl) {
		return fmt.Errorf("%w: no supported OS credential store backend found", ErrUnavailable)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func mapKeyringError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, keyring.ErrNoAvailImpl) {
		return ErrUnavailable
	}
	return err
}
