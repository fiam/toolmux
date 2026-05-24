package credentials

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreRoundTripOAuthTokens(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ref := ConnectionRef{
		Provider:  "test-provider",
		AccountID: "workspace-1",
	}
	tokens := OAuthTokens{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
		Scopes:       []string{"issues:create", "read"},
		Extra:        map[string]string{"id_token": "id-1"},
	}

	if err := store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	has, err := store.HasOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected saved OAuth tokens to be present")
	}
	if got.AccessToken != tokens.AccessToken || got.RefreshToken != tokens.RefreshToken {
		t.Fatalf("token mismatch: %#v", got)
	}
	if got.Extra["id_token"] != "id-1" {
		t.Fatalf("extra mismatch: %#v", got.Extra)
	}

	got.Extra["id_token"] = "mutated"
	got.Scopes[0] = "mutated"
	again, err := store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if again.Extra["id_token"] != "id-1" || again.Scopes[0] == "mutated" {
		t.Fatalf("store returned mutable token data: %#v", again)
	}
}

func TestMemoryStoreMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	has, err := store.HasOAuthTokens(context.Background(), ConnectionRef{
		Provider:  "notion",
		AccountID: "workspace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("missing OAuth tokens should not be present")
	}
	_, err = store.LoadOAuthTokens(context.Background(), ConnectionRef{
		Provider:  "notion",
		AccountID: "workspace-1",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreZeroValueCanSaveTokens(t *testing.T) {
	t.Parallel()
	var store MemoryStore
	ref := ConnectionRef{Provider: "notion", AccountID: "workspace-1"}
	if err := store.SaveOAuthTokens(context.Background(), ref, OAuthTokens{AccessToken: "access-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadOAuthTokens(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStoreDeleteOAuthTokensIsIdempotent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ref := ConnectionRef{Provider: "notion", AccountID: "workspace-1"}
	if err := store.SaveOAuthTokens(context.Background(), ref, OAuthTokens{AccessToken: "access-1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOAuthTokens(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOAuthTokens(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadOAuthTokens(context.Background(), ref)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreDoctor(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	diagnostics := store.Doctor(context.Background())
	if !diagnostics.Available {
		t.Fatalf("expected memory store to be available: %#v", diagnostics)
	}
	if diagnostics.Backend != "memory" {
		t.Fatalf("backend mismatch: %#v", diagnostics)
	}
}
