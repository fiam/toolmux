package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/slackauth"
)

func TestSlackAddWorkspaceUsesBrowserAuth(t *testing.T) { //nolint:paralleltest // overrides a package-level browser hook.
	// This test overrides a package-level browser hook, so it intentionally
	// does not call t.Parallel().
	oldExtract := slackAuthExtract
	t.Cleanup(func() {
		slackAuthExtract = oldExtract
	})
	var gotOptions slackauth.Options
	slackAuthExtract = func(_ context.Context, opts slackauth.Options) (*slackauth.Session, error) {
		gotOptions = opts
		return &slackauth.Session{
			TeamID:     "T123",
			TeamName:   "Acme",
			TeamDomain: "acme",
			Token:      "xoxc-browser",
			Cookie:     "xoxd-browser",
		}, nil
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth.test" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer xoxc-browser" || r.Header.Get("Cookie") != "d=xoxd-browser" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			t.Errorf("unexpected auth headers: authorization=%q cookie=%q", r.Header.Get("Authorization"), r.Header.Get("Cookie"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     upstreamURL(r) + "/",
			"team":    "Acme",
			"user":    "toolmux",
			"team_id": "T123",
			"user_id": "U123",
		}); err != nil {
			t.Errorf("encode auth.test response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)

	store := credentials.NewMemoryStore()
	_, err := handleAdd(actions.Context{
		Context:     context.Background(),
		Credentials: store,
		HTTPClient:  upstream.Client(),
		Profile:     "default",
		Provider:    providerID,
		ProviderURL: upstream.URL + "/api",
	}, actions.Invocation{Flags: map[string]any{
		"account":         defaultAccount,
		"auth":            "",
		"from-browser":    "",
		"workspace":       "https://acme.slack.com/",
		"timeout-seconds": 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if gotOptions.Engine != "" {
		t.Fatalf("expected default slackauth engine, got %q", gotOptions.Engine)
	}
	if gotOptions.WorkspaceDomain != "acme" {
		t.Fatalf("expected workspace domain acme, got %q", gotOptions.WorkspaceDomain)
	}

	tokens, err := store.LoadOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  providerID,
		AccountID: defaultAccount,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "xoxc-browser" || tokens.Extra["cookie"] != "d=xoxd-browser" {
		t.Fatalf("unexpected stored browser credentials: %#v", tokens)
	}
	if tokens.Extra["team_domain"] != "acme" || tokens.Extra["api_base_url"] == "" {
		t.Fatalf("expected browser metadata and workspace API base, got %#v", tokens.Extra)
	}
}

func TestSlackBrowserAuthRejectsRodUntilEngineExists(t *testing.T) {
	t.Parallel()
	_, err := slackAuthEngine("rod")
	if err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatalf("expected rod to be rejected until implemented, got %v", err)
	}
}

func TestSlackBrowserAuthRequiresWorkspace(t *testing.T) {
	t.Parallel()
	_, err := handleAdd(actions.Context{
		Context:     context.Background(),
		Credentials: credentials.NewMemoryStore(),
		Profile:     "default",
		Provider:    providerID,
	}, actions.Invocation{Flags: map[string]any{
		"account":         defaultAccount,
		"auth":            "",
		"from-browser":    "",
		"workspace":       "",
		"timeout-seconds": 1,
	}})
	if err == nil || !strings.Contains(err.Error(), "requires --workspace") {
		t.Fatalf("expected missing workspace error, got %v", err)
	}
}

func upstreamURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
