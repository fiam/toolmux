package credentials

import (
	"context"
	"fmt"
	"sync"
)

type MemoryStore struct {
	mu     sync.RWMutex
	tokens map[string]OAuthTokens
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tokens: make(map[string]OAuthTokens)}
}

func (s *MemoryStore) SaveOAuthTokens(ctx context.Context, ref ConnectionRef, tokens OAuthTokens) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return err
	}
	normalized, err := NormalizeOAuthTokens(tokens)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]OAuthTokens)
	}
	s.tokens[key] = cloneOAuthTokens(normalized)
	return nil
}

func (s *MemoryStore) HasOAuthTokens(ctx context.Context, ref ConnectionRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tokens[key]
	return ok, nil
}

func (s *MemoryStore) LoadOAuthTokens(ctx context.Context, ref ConnectionRef) (OAuthTokens, error) {
	if err := ctx.Err(); err != nil {
		return OAuthTokens{}, err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return OAuthTokens{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tokens, ok := s.tokens[key]
	if !ok {
		return OAuthTokens{}, fmt.Errorf("%w: %s", ErrNotFound, ref.Display())
	}
	return cloneOAuthTokens(tokens), nil
}

func (s *MemoryStore) DeleteOAuthTokens(ctx context.Context, ref ConnectionRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := oauthTokensKey(ref)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, key)
	return nil
}

func (s *MemoryStore) Doctor(ctx context.Context) Diagnostics {
	if err := ctx.Err(); err != nil {
		return Diagnostics{
			Available: false,
			Service:   DefaultServiceName,
			Backend:   "memory",
			Message:   err.Error(),
		}
	}
	return Diagnostics{
		Available: true,
		Service:   DefaultServiceName,
		Backend:   "memory",
		Message:   "memory credential store available",
	}
}
