package credentials

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeRefDefaultsProfileAndService(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	want := "work%20profile/google/gmail/user%2Fexample@example.com"
	if key != want {
		t.Fatalf("key mismatch:\n got: %q\nwant: %q", key, want)
	}
}

func TestOAuthTokensKeyShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ref  ConnectionRef
		want string
	}{
		{"canonical", ConnectionRef{Profile: "default", Provider: "google", Service: "google", AccountID: "google"}, "google"},
		{"account differs", ConnectionRef{Provider: "google", AccountID: "alice"}, "google/alice"},
		{"non-default profile", ConnectionRef{Profile: "work", Provider: "google", AccountID: "google"}, "work/google/google/google"},
		{"service differs", ConnectionRef{Provider: "jira", Service: "jira-eu", AccountID: "jira"}, "default/jira/jira-eu/jira"},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		got, err := oauthTokensKey(tc.ref)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
		if prev, dup := seen[got]; dup {
			t.Fatalf("key collision: %q shared by %q and %q", got, prev, tc.name)
		}
		seen[got] = tc.name
	}
}

func TestNormalizeOAuthTokensCleansScopesAndExtra(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	if _, err := NormalizeOAuthTokens(OAuthTokens{}); !errors.Is(err, ErrInvalidTokens) {
		t.Fatalf("error = %v, want ErrInvalidTokens", err)
	}
}
