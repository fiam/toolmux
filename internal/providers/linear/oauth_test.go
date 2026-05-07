package linear

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/fiam/supacli/internal/testutil/fakeupstream"
)

func TestAuthorizationURLUsesCommaSeparatedScopesAndPKCE(t *testing.T) {
	cfg := OAuthConfig{
		ClientID:     "client-1",
		RedirectURI:  "http://127.0.0.1:1234/callback",
		AuthorizeURL: "https://linear.test/oauth/authorize",
	}

	rawURL, err := cfg.AuthorizationURL("state-1", "challenge-1", DefaultScopes)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if got := query.Get("scope"); got != "read,issues:create,comments:create" {
		t.Fatalf("scope mismatch: %q", got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("PKCE method mismatch: %q", got)
	}
}

func TestExchangeRefreshAndRevokeAgainstFakeUpstream(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	cfg := OAuthConfig{
		ClientID:     "client-1",
		RedirectURI:  "http://127.0.0.1/callback",
		TokenURL:     upstream.URL + "/oauth/token",
		RevokeURL:    upstream.URL + "/oauth/revoke",
		AuthorizeURL: upstream.URL + "/oauth/authorize",
	}
	ctx := context.Background()

	token, err := cfg.ExchangePKCE(ctx, upstream.Client(), "code-1", "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		t.Fatalf("expected token bundle, got %#v", token)
	}

	refreshed, err := cfg.Refresh(ctx, upstream.Client(), token.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(refreshed.Scopes, " "), ScopeRead) {
		t.Fatalf("expected read scope, got %#v", refreshed.Scopes)
	}

	if err := cfg.Revoke(ctx, upstream.Client(), refreshed.AccessToken, "access_token"); err != nil {
		t.Fatal(err)
	}
}

func TestScopeListAcceptsLegacyArray(t *testing.T) {
	var response tokenResponse
	if err := response.Scopes.UnmarshalJSON([]byte(`["read","issues:create"]`)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(response.Scopes, ","); got != "read,issues:create" {
		t.Fatalf("scope mismatch: %q", got)
	}
}

func TestScopeListSplitsCommaSeparatedStrings(t *testing.T) {
	var response tokenResponse
	if err := response.Scopes.UnmarshalJSON([]byte(`"read,comments:create"`)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(response.Scopes, ","); got != "read,comments:create" {
		t.Fatalf("scope mismatch: %q", got)
	}
}
