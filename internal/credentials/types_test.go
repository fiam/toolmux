package credentials

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeRefDefaultsProfileAndService(t *testing.T) {
	ref, err := NormalizeRef(ConnectionRef{
		Provider:  "Google",
		Service:   "Gmail",
		AccountID: "USER@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Profile != DefaultProfile {
		t.Fatalf("profile mismatch: %q", ref.Profile)
	}
	if ref.Provider != "google" {
		t.Fatalf("provider mismatch: %q", ref.Provider)
	}
	if ref.Service != "gmail" {
		t.Fatalf("service mismatch: %q", ref.Service)
	}
	if ref.AccountID != "USER@example.com" {
		t.Fatalf("account id mismatch: %q", ref.AccountID)
	}
}

func TestNormalizeRefDefaultsServiceToProvider(t *testing.T) {
	ref, err := NormalizeRef(ConnectionRef{
		Provider:  "Notion",
		AccountID: "workspace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Service != "notion" {
		t.Fatalf("service mismatch: %q", ref.Service)
	}
}

func TestNormalizeRefRejectsInvalidReferences(t *testing.T) {
	tests := []ConnectionRef{
		{AccountID: "workspace-1"},
		{Provider: "notion"},
		{Provider: "notion", AccountID: "workspace\n1"},
	}
	for _, tt := range tests {
		if _, err := NormalizeRef(tt); !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("NormalizeRef(%#v) error = %v, want ErrInvalidRef", tt, err)
		}
	}
}

func TestOAuthTokensKeyEscapesComponents(t *testing.T) {
	key, err := oauthTokensKey(ConnectionRef{
		Profile:   "work profile",
		Provider:  "google",
		Service:   "gmail",
		AccountID: "user/example@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(key, "user/example") {
		t.Fatalf("expected account id to be escaped, got %q", key)
	}
	want := "profile/work%20profile/provider/google/service/gmail/account/user%2Fexample@example.com/oauth"
	if key != want {
		t.Fatalf("key mismatch:\n got: %q\nwant: %q", key, want)
	}
}

func TestNormalizeOAuthTokensCleansScopesAndExtra(t *testing.T) {
	expiresAt := time.Date(2026, 5, 7, 10, 0, 0, 0, time.FixedZone("WEST", 3600))
	tokens, err := NormalizeOAuthTokens(OAuthTokens{
		AccessToken:  " access-1 ",
		RefreshToken: " refresh-1 ",
		TokenType:    " Bearer ",
		ExpiresAt:    expiresAt,
		Scopes:       []string{"write", " read ", "write", ""},
		Extra: map[string]string{
			" id_token ": "id-1",
			"":           "drop",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" || tokens.TokenType != "Bearer" {
		t.Fatalf("token strings were not cleaned: %#v", tokens)
	}
	if tokens.ExpiresAt.Location() != time.UTC {
		t.Fatalf("expires_at was not converted to UTC: %v", tokens.ExpiresAt)
	}
	if got := strings.Join(tokens.Scopes, ","); got != "read,write" {
		t.Fatalf("scopes mismatch: %q", got)
	}
	if tokens.Extra["id_token"] != "id-1" {
		t.Fatalf("extra mismatch: %#v", tokens.Extra)
	}
}

func TestNormalizeOAuthTokensRequiresAccessToken(t *testing.T) {
	if _, err := NormalizeOAuthTokens(OAuthTokens{}); !errors.Is(err, ErrInvalidTokens) {
		t.Fatalf("error = %v, want ErrInvalidTokens", err)
	}
}
