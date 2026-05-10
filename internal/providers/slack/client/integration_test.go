package client_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fiam/toolmux/internal/cli"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/slack"
	"github.com/fiam/toolmux/internal/providers/slack/slacktest"
	"github.com/fiam/toolmux/internal/testutil/toolmuxdtest"
	"github.com/fiam/toolmux/internal/testutil/toolmuxtest"
)

func TestIntegrationSlackCommands(t *testing.T) {
	t.Parallel()
	upstream := slacktest.NewUpstream()
	defer upstream.Close()

	store := credentials.NewMemoryStore()
	saveSlackToken(t, store, credentials.OAuthTokens{
		AccessToken:  "fake-slack-access-token",
		RefreshToken: "fake-slack-refresh-token",
		TokenType:    "user",
		Scopes:       slack.DefaultCapabilities(),
		Extra: map[string]string{
			"workspace_id":   slacktest.TeamID,
			"workspace_name": slacktest.TeamName,
			"user_id":        slacktest.UserID,
		},
	})
	deps := cli.Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		ProviderURL: map[string]string{"slack": upstream.URL + "/api"},
	}

	status := toolmuxtest.Run(t, deps, "status", "slack")
	toolmuxtest.AssertContains(t, status, "connected")
	toolmuxtest.AssertContains(t, status, "chat:write")

	doctor := toolmuxtest.Run(t, deps, "doctor", "slack")
	toolmuxtest.AssertContains(t, doctor, "slack")
	toolmuxtest.AssertContains(t, doctor, "conversations endpoint reachable")

	conversations := toolmuxtest.Run(t, deps, "slack", "conversations", "ls")
	toolmuxtest.AssertContains(t, conversations, slacktest.ChannelID)
	toolmuxtest.AssertContains(t, conversations, "toolmux")
	toolmuxtest.AssertContains(t, conversations, slacktest.UserID)

	sent := toolmuxtest.Run(t, deps, "slack", "message", "send", "--channel", slacktest.ChannelID, "ship it")
	toolmuxtest.AssertContains(t, sent, "ship it")
	toolmuxtest.AssertContains(t, sent, "sent")

	search := toolmuxtest.Run(t, deps, "slack", "search", "deploy")
	toolmuxtest.AssertContains(t, search, "deploy is done")
	toolmuxtest.AssertContains(t, search, "https://toolmux.slack.com")

	dryRun := toolmuxtest.Run(t, deps, "slack", "message", "send", "--channel", slacktest.ChannelID, "--text", "preview", "--dry-run")
	toolmuxtest.AssertContains(t, dryRun, "Dry run")
	toolmuxtest.AssertContains(t, dryRun, "slack.message.send")
}

func TestIntegrationSlackJSONOutputIsStable(t *testing.T) {
	t.Parallel()
	upstream := slacktest.NewUpstream()
	defer upstream.Close()

	store := credentials.NewMemoryStore()
	saveSlackToken(t, store, credentials.OAuthTokens{
		AccessToken: "fake-slack-access-token",
		TokenType:   "user",
		Scopes:      slack.DefaultCapabilities(),
	})
	deps := cli.Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		ProviderURL: map[string]string{"slack": upstream.URL + "/api"},
	}

	out := toolmuxtest.Run(t, deps, "--output", "json", "slack", "search", "deploy")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("json output contains ANSI escape sequence: %q", out)
	}
	var decoded struct {
		Messages struct {
			Matches []struct {
				Text string `json:"text"`
			} `json:"matches"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Messages.Matches) != 1 || decoded.Messages.Matches[0].Text != "deploy is done" {
		t.Fatalf("unexpected Slack search JSON: %#v", decoded)
	}
}

func TestIntegrationConnectSlackUsesToolmuxdEnvOverride(t *testing.T) {
	t.Parallel()
	upstream := slacktest.NewUpstream()
	defer upstream.Close()
	toolmuxd := toolmuxdtest.NewSlack(t, upstream.URL, upstream.Client())

	deps := cli.Dependencies{
		Credentials: credentials.NewMemoryStore(),
		HTTPClient:  toolmuxd.Client(),
		Env:         toolmuxd.Env,
	}
	out := toolmuxtest.Run(t, deps, "connect", "slack", "--auth-url-only")
	toolmuxtest.AssertContains(t, out, toolmuxd.URL+"/oauth/slack/start")
	toolmuxtest.AssertContains(t, out, "/oauth/slack/callback")
}

func TestIntegrationSlackRefreshesExpiredTokens(t *testing.T) {
	t.Parallel()
	upstream := slacktest.NewUpstream()
	defer upstream.Close()
	toolmuxd := toolmuxdtest.NewSlack(t, upstream.URL, upstream.Client())

	store := credentials.NewMemoryStore()
	saveSlackToken(t, store, credentials.OAuthTokens{
		AccessToken:  "expired-slack-access-token",
		RefreshToken: "fake-slack-refresh-token",
		TokenType:    "user",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute),
		Scopes:       slack.DefaultCapabilities(),
	})
	deps := cli.Dependencies{
		Credentials: store,
		HTTPClient:  upstream.Client(),
		Env:         toolmuxd.Env,
		ProviderURL: map[string]string{"slack": upstream.URL + "/api"},
	}

	toolmuxtest.AssertContains(t, toolmuxtest.Run(t, deps, "slack", "conversations", "ls"), slacktest.ChannelID)
	tokens, err := store.LoadOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "fake-slack-refreshed-access-token" || tokens.RefreshToken != "fake-slack-rotated-refresh-token" {
		t.Fatalf("expected rotated Slack tokens, got %#v", tokens)
	}
}

func saveSlackToken(t *testing.T, store credentials.Store, tokens credentials.OAuthTokens) {
	t.Helper()
	if err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "default",
	}, tokens); err != nil {
		t.Fatal(err)
	}
}
