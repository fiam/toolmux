package slack_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/brokers"
	_ "github.com/fiam/toolmux/internal/providers/brokers/all"
	"github.com/fiam/toolmux/internal/server"
	"github.com/fiam/toolmux/internal/testutil/toolmuxdtest"
	"github.com/fiam/toolmux/internal/testutil/toolmuxtest"
)

func TestSlackDirectTokenCookieE2E(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)

	out := toolmuxtest.Run(t, deps, "add", "slack", "--token", "xoxc-direct", "--cookie", "xoxd")
	toolmuxtest.AssertContains(t, out, "added Slack toolbox using token-cookie auth")

	tokens, err := store.LoadOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "slack",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Extra["team_url"] != upstream.Server.URL+"/" {
		t.Fatalf("expected team URL from auth.test, got %q", tokens.Extra["team_url"])
	}
	if tokens.Extra["api_base_url"] != upstream.Server.URL+"/api" {
		t.Fatalf("expected workspace API base from auth.test, got %q", tokens.Extra["api_base_url"])
	}

	out = toolmuxtest.Run(t, deps, "slack", "channels_list", "--limit", "10")
	toolmuxtest.AssertContains(t, out, "C123")
	toolmuxtest.AssertContains(t, out, "general")

	out = toolmuxtest.Run(t, deps, "slack", "users_conversations", "--limit", "10")
	toolmuxtest.AssertContains(t, out, "C123")
	toolmuxtest.AssertContains(t, out, "general")

	out = toolmuxtest.Run(t, deps, "slack", "experimental_conversations_list", "--query", "general", "--limit", "10")
	toolmuxtest.AssertContains(t, out, "C123")
	toolmuxtest.AssertContains(t, out, "general")

	out = toolmuxtest.Run(t, deps, "status", "slack")
	toolmuxtest.AssertContains(t, out, "native")
	toolmuxtest.AssertContains(t, out, "token-cookie")

	out = toolmuxtest.Run(t, deps, "status")
	toolmuxtest.AssertContains(t, out, "slack")
	toolmuxtest.AssertContains(t, out, "native")
	toolmuxtest.AssertContains(t, out, "token-cookie")

	upstream.assertDirectCookie(t)
}

func TestSlackAppearsInInternalCatalog(t *testing.T) {
	t.Parallel()
	deps := slackDeps(t, credentials.NewMemoryStore(), http.DefaultClient, "https://slack.example.test")

	out := toolmuxtest.Run(t, deps, "list", "--internal")
	for _, want := range []string{"slack", "internal", "needs auth"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	if strings.Contains(out, "linear") {
		t.Fatalf("expected internal catalog output to omit MCP entries, got:\n%s", out)
	}
}

func TestSlackAuthTestReturnsCurrentUser(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	toolmuxtest.Run(t, deps, "add", "slack", "--token", "xoxc-direct", "--cookie", "xoxd")

	out := toolmuxtest.Run(t, deps, "--output", "json", "slack", "auth_test")
	toolmuxtest.AssertContains(t, out, `"user_id": "U123"`)
	toolmuxtest.AssertContains(t, out, `"user": "toolmux"`)
}

func TestSlackHistoryAndRepliesSupportTimeBounds(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	toolmuxtest.Run(t, deps, "add", "slack", "--token", "xoxc-direct", "--cookie", "xoxd")

	out := toolmuxtest.Run(t, deps,
		"--output", "json",
		"slack", "conversations_history",
		"--channel_id", "C123",
		"--oldest", "1710000000.000000",
		"--latest", "1710003600.000000",
		"--inclusive",
		"--limit", "15",
	)
	toolmuxtest.AssertContains(t, out, "bounded update")
	upstream.assertHistoryQuery(t, url.Values{
		"channel":   []string{"C123"},
		"oldest":    []string{"1710000000.000000"},
		"latest":    []string{"1710003600.000000"},
		"inclusive": []string{"true"},
		"limit":     []string{"15"},
	})

	out = toolmuxtest.Run(t, deps,
		"--output", "json",
		"slack", "conversations_replies",
		"--channel_id", "C123",
		"--thread_ts", "1710000100.000000",
		"--oldest", "1710000100.000000",
		"--latest", "1710003600.000000",
		"--inclusive",
		"--limit", "12",
	)
	toolmuxtest.AssertContains(t, out, "bounded reply")
	upstream.assertRepliesQuery(t, url.Values{
		"channel":   []string{"C123"},
		"ts":        []string{"1710000100.000000"},
		"oldest":    []string{"1710000100.000000"},
		"latest":    []string{"1710003600.000000"},
		"inclusive": []string{"true"},
		"limit":     []string{"12"},
	})
}

func TestSlackAddFailsWhenAuthTestFails(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)

	result := toolmuxtest.RunResult(t, deps, "add", "slack", "--token", "bad-token", "--cookie", "xoxd")
	if result.Err == nil {
		t.Fatalf("expected add to fail auth validation, output:\n%s", result.Output)
	}
	if !strings.Contains(result.Err.Error(), "slack auth validation failed") {
		t.Fatalf("expected auth validation error, got %v", result.Err)
	}
	_, err := store.LoadOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "slack",
	})
	if !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("expected invalid Slack auth not to be stored, got %v", err)
	}
}

func TestSlackCommandEnrichesLegacyAuthWithTeamURL(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	ref := credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "slack",
	}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken: "xoxc-direct",
		TokenType:   "Bearer",
		Extra: map[string]string{
			"auth_type": "token_cookie",
			"cookie":    "d=xoxd",
		},
	}); err != nil {
		t.Fatal(err)
	}

	out := toolmuxtest.Run(t, deps, "slack", "channels_list", "--limit", "1")
	toolmuxtest.AssertContains(t, out, "general")

	tokens, err := store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Extra["api_base_url"] != upstream.Server.URL+"/api" {
		t.Fatalf("expected legacy auth to be enriched with workspace API base, got %q", tokens.Extra["api_base_url"])
	}
}

func TestSlackUserOAuthE2ERefreshesAndSearches(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	deps.OpenBrowser = followURL(upstream.Server.Client())

	out := toolmuxtest.Run(t, deps,
		"add", "slack",
		"--auth", "oauth",
		"--client-id", "user-client",
		"--client-secret", "user-secret",
		"--scope", "search:read",
		"--timeout-seconds", "5",
	)
	toolmuxtest.AssertContains(t, out, "added Slack toolbox using user OAuth app auth")

	out = toolmuxtest.Run(t, deps, "slack", "conversations_search_messages", "--search_query", "roadmap", "--limit", "5")
	toolmuxtest.AssertContains(t, out, "roadmap launch")
	toolmuxtest.AssertContains(t, out, "https://example.slack.com/archives/C123/p1")

	upstream.assertUserRefresh(t)
}

func TestSlackBrokerOAuthE2ERefreshesAndListsConversations(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	toolmuxd := toolmuxdtest.New(t, server.Config{
		Providers: map[actions.ProviderName]brokers.Config{
			"slack": {
				ClientID:   "broker-client",
				Secret:     "broker-secret",
				AuthURL:    upstream.Server.URL + "/oauth/v2/authorize",
				TokenURL:   upstream.Server.URL + "/api/oauth.v2.access",
				RevokeURL:  upstream.Server.URL + "/api/auth.revoke",
				Scopes:     []string{"channels:read"},
				HTTPClient: upstream.Server.Client(),
			},
		},
		HTTPClient: upstream.Server.Client(),
	})
	deps := slackDeps(t, store, toolmuxd.Client(), upstream.Server.URL)
	deps.ToolmuxdURL = toolmuxd.URL
	deps.OpenBrowser = followURL(toolmuxd.Client())

	out := toolmuxtest.Run(t, deps, "add", "slack", "--auth", "broker", "--scope", "channels:read", "--timeout-seconds", "5")
	toolmuxtest.AssertContains(t, out, "added Slack toolbox using brokered OAuth")

	out = toolmuxtest.Run(t, deps, "slack", "channels_list")
	toolmuxtest.AssertContains(t, out, "C123")
	toolmuxtest.AssertContains(t, out, "general")

	upstream.assertBrokerRefresh(t)
}

func TestSlackSendDryRunDoesNotReadCredentials(t *testing.T) {
	t.Parallel()
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, http.DefaultClient, "https://slack.example.test")

	out := toolmuxtest.Run(t, deps, "--output", "json", "slack", "conversations_add_message", "--channel_id", "C123", "--text", "hello", "--dry-run")
	toolmuxtest.AssertContains(t, out, `"dry_run": true`)
	toolmuxtest.AssertContains(t, out, `"channel": "C123"`)
}

func TestSlackConversationsOpenReturnsDMChannel(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	toolmuxtest.Run(t, deps, "add", "slack", "--token", "xoxc-direct", "--cookie", "xoxd")

	out := toolmuxtest.Run(t, deps, "--output", "json", "slack", "conversations_open", "--user_id", "U123")
	toolmuxtest.AssertContains(t, out, `"id": "D123"`)
	toolmuxtest.AssertContains(t, out, `"user": "U123"`)
}

func TestSlackAuthSetupSubcommandsAreNotExposed(t *testing.T) {
	t.Parallel()
	deps := slackDeps(t, credentials.NewMemoryStore(), http.DefaultClient, "https://slack.example.test")

	out := toolmuxtest.Run(t, deps, "slack", "--help")
	for _, command := range []string{"auth_login", "auth_set", "broker_login"} {
		if strings.Contains(out, command) {
			t.Fatalf("slack help should not expose %s subcommand: %s", command, out)
		}
	}
}

func TestSlackExposesSlackMCPServerToolNames(t *testing.T) {
	t.Parallel()
	want := []string{
		"slack.auth_test",
		"slack.conversations_history",
		"slack.conversations_replies",
		"slack.conversations_add_message",
		"slack.conversations_open",
		"slack.reactions_add",
		"slack.reactions_remove",
		"slack.attachment_get_data",
		"slack.conversations_search_messages",
		"slack.conversations_unreads",
		"slack.conversations_mark",
		"slack.channels_list",
		"slack.users_conversations",
		"slack.experimental_conversations_list",
		"slack.usergroups_list",
		"slack.usergroups_me",
		"slack.usergroups_create",
		"slack.usergroups_update",
		"slack.usergroups_users_update",
		"slack.users_search",
	}
	seen := map[string]bool{}
	for _, spec := range providers.CommandSpecs() {
		seen[spec.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("missing Slack tool command %s", id)
		}
	}
}

func TestSlackSearchMessagesExposedOverMCPServe(t *testing.T) {
	t.Parallel()
	store := credentials.NewMemoryStore()
	if err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "slack",
	}, credentials.OAuthTokens{AccessToken: "xoxb-test"}); err != nil {
		t.Fatal(err)
	}
	deps := slackDeps(t, store, http.DefaultClient, "https://slack.example.test")

	out := runToolmuxWithInput(t, deps,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		"mcp", "serve", "--tool", "slack.*",
	)
	toolmuxtest.AssertContains(t, out, `"name":"slack.conversations_search_messages"`)
	toolmuxtest.AssertContains(t, out, "Slack search syntax")
	toolmuxtest.AssertContains(t, out, `"search_query"`)
}

func TestSlackCredentialRefUsesDefaultProfile(t *testing.T) {
	t.Parallel()
	upstream := newFakeSlackUpstream(t)
	store := credentials.NewMemoryStore()
	deps := slackDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	toolmuxtest.Run(t, deps, "add", "slack", "--token", "xoxc-direct", "--cookie", "xoxd")

	_, err := store.LoadOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "slack",
		AccountID: "slack",
	})
	if err != nil {
		t.Fatal(err)
	}
}
