package broker

import (
	"net/url"
	"strings"
	"testing"
)

func TestAuthURLUsesUserScopesWithoutEmptyBotScope(t *testing.T) {
	t.Parallel()
	broker := New(Config{
		ClientID: "client-id",
		Secret:   "client-secret",
		AuthURL:  "https://slack.com/oauth/v2/authorize",
		TokenURL: "https://slack.com/api/oauth.v2.access",
	})

	rawURL, err := broker.AuthURL("https://auth.example.com/oauth/slack/callback", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if _, ok := query["scope"]; ok {
		t.Fatalf("user-token auth URL should not include an empty bot scope: %s", rawURL)
	}
	if got := query.Get("user_scope"); !strings.Contains(got, "channels:read") || !strings.Contains(got, "chat:write") {
		t.Fatalf("missing user scopes: %q", got)
	}
	if query.Get("redirect_uri") != "https://auth.example.com/oauth/slack/callback" {
		t.Fatalf("unexpected redirect URI: %q", query.Get("redirect_uri"))
	}
}
