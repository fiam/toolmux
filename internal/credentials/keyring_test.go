package credentials

import (
	"context"
	"errors"
	"testing"

	keyring "github.com/99designs/keyring"
)

type fakeKeyring struct {
	items     map[string]keyring.Item
	getErr    error
	setErr    error
	removeErr error
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{items: make(map[string]keyring.Item)}
}

func (r *fakeKeyring) Get(key string) (keyring.Item, error) {
	if r.getErr != nil {
		return keyring.Item{}, r.getErr
	}
	item, ok := r.items[key]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	item.Data = append([]byte(nil), item.Data...)
	return item, nil
}

func (r *fakeKeyring) GetMetadata(key string) (keyring.Metadata, error) {
	item, err := r.Get(key)
	if err != nil {
		return keyring.Metadata{}, err
	}
	item.Data = nil
	return keyring.Metadata{Item: &item}, nil
}

func (r *fakeKeyring) Set(item keyring.Item) error {
	if r.setErr != nil {
		return r.setErr
	}
	item.Data = append([]byte(nil), item.Data...)
	r.items[item.Key] = item
	return nil
}

func (r *fakeKeyring) Remove(key string) error {
	if r.removeErr != nil {
		return r.removeErr
	}
	if _, ok := r.items[key]; !ok {
		return keyring.ErrKeyNotFound
	}
	delete(r.items, key)
	return nil
}

func (r *fakeKeyring) Keys() ([]string, error) {
	keys := make([]string, 0, len(r.items))
	for key := range r.items {
		keys = append(keys, key)
	}
	return keys, nil
}

func TestKeyringStoreRoundTripOAuthTokens(t *testing.T) {
	ring := newFakeKeyring()
	store := newKeyringStore(ring, "supacli-test", []string{"keychain"})
	ref := ConnectionRef{Profile: "default", Provider: "notion", AccountID: "workspace-1"}

	if err := store.SaveOAuthTokens(context.Background(), ref, OAuthTokens{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Scopes:       []string{"read"},
	}); err != nil {
		t.Fatal(err)
	}

	key, err := oauthTokensKey(ref)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := ring.items[key]
	if !ok {
		t.Fatalf("expected keyring item at %q", key)
	}
	if item.Label != "Supacli notion OAuth tokens" {
		t.Fatalf("label mismatch: %q", item.Label)
	}

	tokens, err := store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("token mismatch: %#v", tokens)
	}
}

func TestKeyringStoreLoadMissingReturnsNotFound(t *testing.T) {
	store := newKeyringStore(newFakeKeyring(), "supacli-test", []string{"keychain"})
	_, err := store.LoadOAuthTokens(context.Background(), ConnectionRef{
		Provider:  "slack",
		AccountID: "team-1",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestKeyringStoreDeleteMissingIsIdempotent(t *testing.T) {
	store := newKeyringStore(newFakeKeyring(), "supacli-test", []string{"keychain"})
	err := store.DeleteOAuthTokens(context.Background(), ConnectionRef{
		Provider:  "slack",
		AccountID: "team-1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKeyringStoreRejectsCorruptOAuthPayload(t *testing.T) {
	ring := newFakeKeyring()
	store := newKeyringStore(ring, "supacli-test", []string{"keychain"})
	ref := ConnectionRef{Provider: "linear", AccountID: "workspace-1"}
	key, err := oauthTokensKey(ref)
	if err != nil {
		t.Fatal(err)
	}
	ring.items[key] = keyring.Item{Key: key, Data: []byte(`{"version":1,"kind":"oauth","tokens":{}}`)}

	_, err = store.LoadOAuthTokens(context.Background(), ref)
	if !errors.Is(err, ErrInvalidTokens) {
		t.Fatalf("error = %v, want ErrInvalidTokens", err)
	}
}

func TestKeyringStoreDoctor(t *testing.T) {
	ring := newFakeKeyring()
	store := newKeyringStore(ring, "supacli-test", []string{"keychain"})

	diagnostics := store.Doctor(context.Background())
	if !diagnostics.Available {
		t.Fatalf("expected keyring store to be available: %#v", diagnostics)
	}
	if _, ok := ring.items["diagnostics/probe/oauth"]; ok {
		t.Fatal("expected doctor probe to be removed")
	}
}

func TestKeyringStoreDoctorReportsWriteFailure(t *testing.T) {
	ring := newFakeKeyring()
	ring.setErr = errors.New("locked")
	store := newKeyringStore(ring, "supacli-test", []string{"keychain"})

	diagnostics := store.Doctor(context.Background())
	if diagnostics.Available {
		t.Fatalf("expected keyring store to be unavailable: %#v", diagnostics)
	}
}

func TestKeyringBackendTypesRejectsUnknownBackend(t *testing.T) {
	_, err := keyringBackendTypes([]string{"unknown"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}
