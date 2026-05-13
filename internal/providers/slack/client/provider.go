package slack

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
	"github.com/fiam/toolmux/internal/slackauth"
)

const (
	providerID       = "slack"
	defaultAccount   = "default"
	authTypeDirect   = "token_cookie"
	authTypeUser     = "oauth_user"
	authTypeBroker   = "oauth_broker"
	callbackPath     = "/oauth/slack/callback"
	oauthRefreshSkew = time.Minute
)

var defaultOAuthScopes = []string{
	"channels:read",
	"groups:read",
	"im:read",
	"mpim:read",
	"im:write",
	"channels:history",
	"groups:history",
	"im:history",
	"mpim:history",
	"chat:write",
	"reactions:write",
	"files:read",
	"users:read",
	"usergroups:read",
	"usergroups:write",
	"search:read",
}

var slackAuthExtract = slackauth.Extract

func init() {
	providers.Register(Descriptor())
}

func Descriptor() providers.Provider {
	return providers.Provider{
		ID:             providerID,
		DisplayName:    "Slack",
		AuthMode:       "token_cookie_or_oauth",
		BaseURLEnv:     "TOOLMUX_SLACK_API_URL",
		DefaultBaseURL: slackapi.DefaultAPIBaseURL,
		Tree: actions.Group("slack",
			actions.Short("Use Slack"),
			actions.Children(
				slackTool("auth_test", "Show Slack auth identity", "connection", actions.VerbRead, actions.EffectRead),
				slackTool("conversations_history", "Get Slack conversation history", "message", actions.VerbRead, actions.EffectRead,
					actions.StringFlag("channel_id", "", "Slack channel, DM, or MPIM ID"),
					actions.BoolFlag("include_activity_messages", false, "include join, leave, and other activity messages"),
					actions.StringFlag("cursor", "", "Slack pagination cursor"),
					actions.StringFlag("oldest", "", "only include messages after this Slack timestamp"),
					actions.StringFlag("latest", "", "only include messages before this Slack timestamp"),
					actions.BoolFlag("inclusive", false, "include messages matching oldest or latest timestamps"),
					actions.StringFlag("limit", "100", "maximum messages to fetch"),
				),
				slackTool("conversations_replies", "Get Slack thread replies", "message", actions.VerbRead, actions.EffectRead,
					actions.StringFlag("channel_id", "", "Slack channel, DM, or MPIM ID"),
					actions.StringFlag("thread_ts", "", "Slack thread timestamp"),
					actions.BoolFlag("include_activity_messages", false, "include join, leave, and other activity messages"),
					actions.StringFlag("cursor", "", "Slack pagination cursor"),
					actions.StringFlag("oldest", "", "only include replies after this Slack timestamp"),
					actions.StringFlag("latest", "", "only include replies before this Slack timestamp"),
					actions.BoolFlag("inclusive", false, "include replies matching oldest or latest timestamps"),
					actions.StringFlag("limit", "100", "maximum replies to fetch"),
				),
				slackTool("conversations_add_message", "Send a Slack message", "message", actions.VerbSend, actions.EffectWrite,
					actions.StringFlag("channel_id", "", "Slack channel, DM, or MPIM ID"),
					actions.StringFlag("thread_ts", "", "Slack thread timestamp"),
					actions.StringFlag("text", "", "message text"),
					actions.StringFlag("payload", "", "message payload; accepted as an alias for text"),
					actions.StringFlag("content_type", "text/markdown", "message content type"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("conversations_open", "Open a Slack DM or MPIM conversation", "conversation", actions.VerbOpen, actions.EffectWrite,
					actions.StringFlag("user_id", "", "single Slack user ID to open a DM with"),
					actions.StringFlag("users", "", "comma-separated Slack user IDs to open a DM or MPIM with"),
					actions.BoolFlag("prevent_creation", false, "do not create a new conversation"),
					actions.BoolFlag("return_im", true, "return the full IM conversation"),
					actions.BoolFlag("dry-run", false, "show the Slack request without opening a conversation"),
				),
				slackTool("reactions_add", "Add a Slack reaction", "reaction", actions.VerbCreate, actions.EffectWrite,
					actions.StringFlag("channel_id", "", "Slack channel, DM, or MPIM ID"),
					actions.StringFlag("timestamp", "", "message timestamp"),
					actions.StringFlag("emoji", "", "emoji name without colons"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("reactions_remove", "Remove a Slack reaction", "reaction", actions.VerbDelete, actions.EffectWrite,
					actions.StringFlag("channel_id", "", "Slack channel, DM, or MPIM ID"),
					actions.StringFlag("timestamp", "", "message timestamp"),
					actions.StringFlag("emoji", "", "emoji name without colons"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("attachment_get_data", "Download Slack attachment data", "attachment", actions.VerbRead, actions.EffectRead,
					actions.StringFlag("file_id", "", "Slack file ID"),
				),
				slackTool("conversations_search_messages", "Search Slack messages", "message", actions.VerbSearch, actions.EffectRead,
					actions.StringFlag("search_query", "", "Slack search query"),
					actions.StringFlag("filter_in_channel", "", "channel filter"),
					actions.StringFlag("filter_in_im_or_mpim", "", "DM or MPIM filter"),
					actions.StringFlag("filter_users_with", "", "with-user filter"),
					actions.StringFlag("filter_users_from", "", "from-user filter"),
					actions.StringFlag("filter_date_before", "", "before date filter"),
					actions.StringFlag("filter_date_after", "", "after date filter"),
					actions.StringFlag("filter_date_on", "", "on date filter"),
					actions.StringFlag("filter_date_during", "", "during date filter"),
					actions.BoolFlag("filter_threads_only", false, "search only thread messages"),
					actions.StringFlag("cursor", "", "Slack pagination cursor"),
					actions.IntFlag("limit", 20, "maximum matches to return"),
				),
				slackTool("conversations_unreads", "List unread Slack conversations", "message", actions.VerbRead, actions.EffectRead,
					actions.BoolFlag("include_messages", true, "include unread or recent messages"),
					actions.StringFlag("channel_types", "all", "channel type filter: all, dm, group_dm, partner, internal"),
					actions.IntFlag("max_channels", 50, "maximum channels to inspect"),
					actions.IntFlag("max_messages_per_channel", 10, "maximum messages per channel"),
					actions.BoolFlag("mentions_only", false, "only include channels with mentions when Slack exposes that count"),
					actions.BoolFlag("include_muted", false, "include muted channels"),
				),
				slackTool("conversations_mark", "Mark a Slack conversation read", "message", actions.VerbUpdate, actions.EffectWrite,
					actions.StringFlag("channel_id", "", "Slack channel, DM, or MPIM ID"),
					actions.StringFlag("ts", "", "message timestamp to mark through"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("channels_list", "List Slack channels", "conversation", actions.VerbList, actions.EffectRead,
					actions.StringFlag("channel_types", "public_channel,private_channel,mpim,im", "comma-separated Slack channel types"),
					actions.StringFlag("sort", "", "sort mode, for example popularity"),
					actions.IntFlag("limit", 100, "maximum channels to return"),
					actions.StringFlag("cursor", "", "Slack pagination cursor"),
				),
				slackTool("usergroups_list", "List Slack user groups", "usergroup", actions.VerbList, actions.EffectRead,
					actions.BoolFlag("include_users", false, "include user IDs in each group"),
					actions.BoolFlag("include_count", true, "include user counts"),
					actions.BoolFlag("include_disabled", false, "include disabled user groups"),
				),
				slackTool("usergroups_me", "Manage your Slack user group membership", "usergroup", actions.VerbUpdate, actions.EffectWrite,
					actions.StringFlag("action", "", "action to perform: list, join, or leave"),
					actions.StringFlag("usergroup_id", "", "Slack user group ID for join or leave"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("usergroups_create", "Create a Slack user group", "usergroup", actions.VerbCreate, actions.EffectWrite,
					actions.StringFlag("name", "", "user group display name"),
					actions.StringFlag("handle", "", "mention handle without @"),
					actions.StringFlag("description", "", "user group description"),
					actions.StringFlag("channels", "", "comma-separated default channel IDs"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("usergroups_update", "Update a Slack user group", "usergroup", actions.VerbUpdate, actions.EffectWrite,
					actions.StringFlag("usergroup_id", "", "Slack user group ID"),
					actions.StringFlag("name", "", "new display name"),
					actions.StringFlag("handle", "", "new mention handle without @"),
					actions.StringFlag("description", "", "new description"),
					actions.StringFlag("channels", "", "comma-separated default channel IDs"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("usergroups_users_update", "Replace Slack user group members", "usergroup", actions.VerbUpdate, actions.EffectWrite,
					actions.StringFlag("usergroup_id", "", "Slack user group ID"),
					actions.StringFlag("users", "", "comma-separated Slack user IDs"),
					actions.BoolFlag("dry-run", false, "show the Slack request without sending it"),
				),
				slackTool("users_search", "Search Slack users", "user", actions.VerbSearch, actions.EffectRead,
					actions.StringFlag("query", "", "user search query"),
					actions.IntFlag("limit", 10, "maximum users to return"),
				),
			),
		),
		Handlers: map[string]actions.Handler{
			"slack.auth_test":                     handleAuthTest,
			"slack.conversations_history":         handleConversationsHistory,
			"slack.conversations_replies":         handleConversationsReplies,
			"slack.conversations_add_message":     handleConversationsAddMessage,
			"slack.conversations_open":            handleConversationsOpen,
			"slack.reactions_add":                 handleReactionsAdd,
			"slack.reactions_remove":              handleReactionsRemove,
			"slack.attachment_get_data":           handleAttachmentGetData,
			"slack.conversations_search_messages": handleConversationsSearchMessages,
			"slack.conversations_unreads":         handleConversationsUnreads,
			"slack.conversations_mark":            handleConversationsMark,
			"slack.channels_list":                 handleChannelsList,
			"slack.usergroups_list":               handleUsergroupsList,
			"slack.usergroups_me":                 handleUsergroupsMe,
			"slack.usergroups_create":             handleUsergroupsCreate,
			"slack.usergroups_update":             handleUsergroupsUpdate,
			"slack.usergroups_users_update":       handleUsergroupsUsersUpdate,
			"slack.users_search":                  handleUsersSearch,
		},
		AddHandler:    handleAdd,
		RemoveHandler: handleRemove,
	}
}

func accountFlag() actions.Option {
	return actions.StringFlag("account", defaultAccount, "Toolmux Slack account name")
}

func slackTool(name, short, resource string, verb actions.Verb, remote actions.Effect, opts ...actions.Option) actions.Spec {
	base := []actions.Option{
		actions.Short(short),
		actions.RBAC(actions.ResourceName(resource), verb, remote),
		accountFlag(),
	}
	base = append(base, opts...)
	return actions.Command(actions.LocalName(name), name, base...)
}

func handleAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	mode, err := slackAddMode(inv)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "browser":
		if _, err := handleBrowserAuth(exec, inv); err != nil {
			return nil, err
		}
		return authResult{Message: "added Slack toolbox using browser session auth for account " + account(inv)}, nil
	case "token-cookie":
		if _, err := handleAuthSet(exec, inv); err != nil {
			return nil, err
		}
		return authResult{Message: "added Slack toolbox using token-cookie auth for account " + account(inv)}, nil
	case "oauth":
		if _, err := handleAuthLogin(exec, inv); err != nil {
			return nil, err
		}
		return authResult{Message: "added Slack toolbox using user OAuth app auth for account " + account(inv)}, nil
	case "broker":
		if _, err := handleBrokerLogin(exec, inv); err != nil {
			return nil, err
		}
		return authResult{Message: "added Slack toolbox using brokered OAuth for account " + account(inv)}, nil
	default:
		return nil, fmt.Errorf("unsupported slack auth mode %q", mode)
	}
}

func slackAddMode(inv actions.Invocation) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(inv.String("auth")))
	if mode == "" {
		switch {
		case hasAnySecretFlag(inv, "token") || hasAnySecretFlag(inv, "cookie"):
			mode = "token-cookie"
		case strings.TrimSpace(inv.String("client-id")) != "" || hasAnySecretFlag(inv, "client-secret"):
			mode = "oauth"
		case strings.TrimSpace(inv.String("from-browser")) != "" || strings.TrimSpace(inv.String("workspace")) != "":
			mode = "browser"
		default:
			mode = "browser"
		}
	}
	switch strings.ReplaceAll(mode, "_", "-") {
	case "browser", "web", "web-session", "slackauth":
		return "browser", nil
	case "token", "token-cookie", "direct", "manual":
		if strings.TrimSpace(inv.String("from-browser")) != "" {
			return "browser", nil
		}
		return "token-cookie", nil
	case "oauth", "app", "user", "user-oauth":
		return "oauth", nil
	case "broker", "brokered", "brokered-oauth", "toolmux":
		return "broker", nil
	default:
		return "", fmt.Errorf("unsupported slack auth mode %q; expected broker, browser, oauth, or token-cookie", mode)
	}
}

func hasAnySecretFlag(inv actions.Invocation, name string) bool {
	return strings.TrimSpace(inv.String(name)) != "" ||
		strings.TrimSpace(inv.String(name+"-env")) != "" ||
		strings.TrimSpace(inv.String(name+"-file")) != ""
}

func handleRemove(exec actions.Context, inv actions.Invocation) (any, error) {
	if err := exec.Credentials.DeleteOAuthTokens(exec.Context, slackCredentialRef(exec, account(inv))); err != nil {
		return nil, err
	}
	return authResult{Message: "removed Slack toolbox auth for account " + account(inv)}, nil
}

func handleAuthSet(exec actions.Context, inv actions.Invocation) (any, error) {
	token, err := secretFromInvocation(exec, inv, "token")
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("slack token is required; pass --token, --token-env, or --token-file")
	}
	cookie, err := secretFromInvocation(exec, inv, "cookie")
	if err != nil {
		return nil, err
	}
	extra := map[string]string{
		"auth_type": authTypeDirect,
	}
	if cookie = normalizeSlackCookieHeader(cookie); cookie != "" {
		extra["cookie"] = cookie
	}
	if teamID := strings.TrimSpace(inv.String("team-id")); teamID != "" {
		extra["team_id"] = teamID
	}
	if workspace := strings.TrimSpace(inv.String("workspace")); workspace != "" {
		extra["team_name"] = workspace
	}
	tokens := credentials.OAuthTokens{
		AccessToken: token,
		TokenType:   "Bearer",
		Extra:       extra,
	}
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		return nil, err
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, slackCredentialRef(exec, account(inv)), tokens); err != nil {
		return nil, err
	}
	return authResult{Message: "stored Slack token for account " + account(inv)}, nil
}

func handleBrowserAuth(exec actions.Context, inv actions.Invocation) (any, error) {
	if hasAnySecretFlag(inv, "token") || hasAnySecretFlag(inv, "cookie") {
		return nil, fmt.Errorf("slack browser auth cannot be combined with token or cookie flags")
	}
	engine, err := slackAuthEngine(inv.String("from-browser"))
	if err != nil {
		return nil, err
	}
	workspace := slackWorkspaceDomain(inv.String("workspace"))
	if workspace == "" {
		return nil, fmt.Errorf("slack browser auth requires --workspace <slack-subdomain>; pass --auth broker, --auth oauth, or token flags for other auth modes")
	}
	session, err := slackAuthExtract(exec.Context, slackauth.Options{
		Engine:          engine,
		WorkspaceDomain: workspace,
		Chooser:         slackAuthChooser(exec),
		Timeout:         timeout(inv),
	})
	if err != nil {
		return nil, fmt.Errorf("slack browser auth failed: %w", err)
	}
	if session == nil || strings.TrimSpace(session.Token) == "" || strings.TrimSpace(session.Cookie) == "" {
		return nil, fmt.Errorf("slack browser auth did not return both token and cookie")
	}
	extra := map[string]string{
		"auth_type": authTypeDirect,
		"cookie":    normalizeSlackCookieHeader(session.Cookie),
	}
	if engine != "" {
		extra["browser_engine"] = string(engine)
	}
	if session.TeamID != "" {
		extra["team_id"] = session.TeamID
	}
	if session.TeamName != "" {
		extra["team_name"] = session.TeamName
	}
	if session.TeamDomain != "" {
		extra["team_domain"] = session.TeamDomain
	}
	tokens := credentials.OAuthTokens{
		AccessToken: strings.TrimSpace(session.Token),
		TokenType:   "Bearer",
		Extra:       extra,
	}
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		return nil, err
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, slackCredentialRef(exec, account(inv)), tokens); err != nil {
		return nil, err
	}
	return authResult{Message: "stored Slack browser session for account " + account(inv)}, nil
}

func slackAuthEngine(value string) (slackauth.Engine, error) {
	switch engine := strings.ToLower(strings.TrimSpace(value)); engine {
	case "", "auto", "default":
		return "", nil
	case "webview":
		return slackauth.EngineWebView, nil
	case "chrome":
		return slackauth.EngineChrome, nil
	case "rod":
		return "", fmt.Errorf("slack browser auth engine %q is not available yet; use webview or chrome", engine)
	default:
		return "", fmt.Errorf("unsupported slack browser auth engine %q; expected webview or chrome", engine)
	}
}

func slackWorkspaceDomain(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Host
	}
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimSuffix(value, "/")
	if host, _, ok := strings.Cut(value, "/"); ok {
		value = host
	}
	if subdomain, ok := strings.CutSuffix(value, ".slack.com"); ok {
		value = subdomain
	}
	return strings.TrimSpace(value)
}

func slackAuthChooser(exec actions.Context) func([]slackauth.Team) (slackauth.Team, error) {
	if !exec.Interactive || exec.SelectString == nil {
		return nil
	}
	return func(teams []slackauth.Team) (slackauth.Team, error) {
		options := make([]actions.SelectStringOption, 0, len(teams))
		byID := make(map[string]slackauth.Team, len(teams))
		for _, team := range teams {
			id := firstNonEmpty(team.ID, team.Domain, team.Name)
			if id == "" {
				continue
			}
			label := firstNonEmpty(team.Name, team.Domain, team.ID)
			if team.Domain != "" && team.Domain != label {
				label += " (" + team.Domain + ")"
			}
			options = append(options, actions.SelectStringOption{Label: label, Value: id})
			byID[id] = team
		}
		if len(options) == 0 {
			return slackauth.Team{}, fmt.Errorf("no selectable Slack workspaces found")
		}
		selected, ok, err := exec.SelectString(exec.Context, actions.SelectStringRequest{
			Title:       "Select Slack workspace",
			Description: "Choose the workspace whose browser session should be stored.",
			Options:     options,
			Height:      min(len(options), 12),
			Filtering:   true,
		})
		if err != nil {
			return slackauth.Team{}, err
		}
		if !ok {
			return slackauth.Team{}, fmt.Errorf("slack workspace selection cancelled")
		}
		team, ok := byID[selected]
		if !ok {
			return slackauth.Team{}, fmt.Errorf("selected Slack workspace %q was not found", selected)
		}
		return team, nil
	}
}

func handleAuthLogin(exec actions.Context, inv actions.Invocation) (any, error) {
	clientID := strings.TrimSpace(inv.String("client-id"))
	if clientID == "" {
		return nil, fmt.Errorf("slack OAuth client ID is required")
	}
	clientSecret, err := secretFromInvocation(exec, inv, "client-secret")
	if err != nil {
		return nil, err
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("slack OAuth client secret is required; pass --client-secret or --client-secret-env")
	}
	state, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	callback, err := startOAuthCallback(inv.Int("redirect-port"), state)
	if err != nil {
		return nil, err
	}
	defer callback.shutdown()

	options := slackOAuthOptions(exec, inv, clientID, clientSecret, callback.redirectURI)
	authURL, err := slackapi.OAuthAuthorizeURL(options, state)
	if err != nil {
		return nil, err
	}
	if exec.OpenBrowser == nil {
		return nil, fmt.Errorf("browser opener is not configured")
	}
	if err := exec.OpenBrowser(authURL); err != nil {
		return nil, err
	}
	result, err := callback.wait(exec.Context, timeout(inv))
	if err != nil {
		return nil, err
	}
	if result.Err != nil {
		return nil, result.Err
	}
	tokens, err := slackapi.ExchangeOAuthCode(exec.Context, exec.HTTPClient, options, result.Code, time.Now())
	if err != nil {
		return nil, err
	}
	tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
		"auth_type":     authTypeUser,
		"client_id":     clientID,
		"client_secret": clientSecret,
		"token_url":     options.TokenURL,
		"token_source":  options.TokenSource,
	})
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		return nil, err
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, slackCredentialRef(exec, account(inv)), tokens); err != nil {
		return nil, err
	}
	return authResult{Message: "stored Slack OAuth token for account " + account(inv)}, nil
}

func handleBrokerLogin(exec actions.Context, inv actions.Invocation) (any, error) {
	session, err := createBrokerSession(exec, inv)
	if err != nil {
		return nil, err
	}
	if exec.OpenBrowser == nil {
		return nil, fmt.Errorf("browser opener is not configured")
	}
	if err := exec.OpenBrowser(session.AuthURL); err != nil {
		return nil, err
	}
	tokens, err := pollBrokerSession(exec, session.SessionID, timeout(inv))
	if err != nil {
		return nil, err
	}
	tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
		"auth_type":  authTypeBroker,
		"broker_url": strings.TrimRight(exec.ToolmuxdURL, "/"),
	})
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		return nil, err
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, slackCredentialRef(exec, account(inv)), tokens); err != nil {
		return nil, err
	}
	return authResult{Message: "stored brokered Slack OAuth token for account " + account(inv)}, nil
}

func handleAuthTest(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.AuthTest(exec.Context)
	if err != nil {
		return nil, err
	}
	return authTestResult(response), nil
}

func handleConversationsHistory(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	values := conversationMessageValues(inv)
	values.Set("channel", channelID)
	response, err := client.ConversationsHistory(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return conversationMessagesResult{
		ChannelID:   channelID,
		Messages:    filterActivityMessages(response.Messages, inv.Bool("include_activity_messages")),
		HasMore:     response.HasMore,
		NextCursor:  response.ResponseMetadata.NextCursor,
		ResultLabel: "messages",
	}, nil
}

func handleConversationsReplies(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	threadTS, err := requiredString(inv, "thread_ts")
	if err != nil {
		return nil, err
	}
	values := conversationMessageValues(inv)
	values.Set("channel", channelID)
	values.Set("ts", threadTS)
	response, err := client.ConversationsReplies(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return conversationMessagesResult{
		ChannelID:   channelID,
		ThreadTS:    threadTS,
		Messages:    filterActivityMessages(response.Messages, inv.Bool("include_activity_messages")),
		HasMore:     response.HasMore,
		NextCursor:  response.ResponseMetadata.NextCursor,
		ResultLabel: "replies",
	}, nil
}

func handleConversationsAddMessage(exec actions.Context, inv actions.Invocation) (any, error) {
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	text := firstNonEmpty(inv.String("text"), inv.String("payload"))
	if text == "" {
		return nil, fmt.Errorf("text is required; pass --text or --payload")
	}
	request := sendMessageRequest{
		Channel:     channelID,
		Text:        text,
		ThreadTS:    strings.TrimSpace(inv.String("thread_ts")),
		ContentType: strings.TrimSpace(inv.String("content_type")),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.conversations_add_message", request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.ChatPostMessage(exec.Context, request.Channel, request.Text, request.ThreadTS)
	if err != nil {
		return nil, err
	}
	return sendMessageResult(response), nil
}

func handleConversationsOpen(exec actions.Context, inv actions.Invocation) (any, error) {
	users := firstNonEmpty(inv.String("users"), inv.String("user_id"))
	if users == "" {
		return nil, fmt.Errorf("users is required; pass --user_id or --users")
	}
	request := openConversationRequest{
		Users:           users,
		PreventCreation: inv.Bool("prevent_creation"),
		ReturnIM:        inv.Bool("return_im"),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.conversations_open", request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("users", request.Users)
	if request.PreventCreation {
		values.Set("prevent_creation", "true")
	}
	if request.ReturnIM {
		values.Set("return_im", "true")
	}
	response, err := client.ConversationsOpen(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return openConversationResult(response), nil
}

func handleReactionsAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleReaction(exec, inv, "reactions.add", "slack.reactions_add")
}

func handleReactionsRemove(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleReaction(exec, inv, "reactions.remove", "slack.reactions_remove")
}

func handleReaction(exec actions.Context, inv actions.Invocation, method, action string) (any, error) {
	request, err := reactionRequestFromInvocation(inv)
	if err != nil {
		return nil, err
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(action, request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("channel", request.Channel)
	values.Set("timestamp", request.Timestamp)
	values.Set("name", strings.Trim(request.Emoji, ":"))
	if _, err := client.PostOK(exec.Context, method, values); err != nil {
		return nil, err
	}
	return actionResult{Message: action + " completed"}, nil
}

func handleAttachmentGetData(exec actions.Context, inv actions.Invocation) (any, error) {
	fileID, err := requiredString(inv, "file_id")
	if err != nil {
		return nil, err
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	info, err := client.FileInfo(exec.Context, fileID)
	if err != nil {
		return nil, err
	}
	result := attachmentDataResult{
		FileID:   info.File.ID,
		Filename: firstNonEmpty(info.File.Name, info.File.Title),
		Mimetype: info.File.Mimetype,
		Size:     info.File.Size,
	}
	downloadURL := firstNonEmpty(info.File.URLPrivateDownload, info.File.URLPrivate)
	if downloadURL == "" {
		return result, nil
	}
	content, err := client.Download(exec.Context, downloadURL, 5<<20)
	if err != nil {
		return nil, err
	}
	result.Size = len(content)
	if isTextMimetype(result.Mimetype) {
		result.Encoding = "none"
		result.Content = string(content)
	} else {
		result.Encoding = "base64"
		result.Content = base64.StdEncoding.EncodeToString(content)
	}
	return result, nil
}

func handleConversationsSearchMessages(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	query := slackSearchQuery(inv)
	if query == "" {
		return nil, fmt.Errorf("search_query or at least one search filter is required")
	}
	values := url.Values{}
	values.Set("query", query)
	if limit := inv.Int("limit"); limit > 0 {
		values.Set("count", strconv.Itoa(limit))
	}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("page", cursor)
	}
	response, err := client.SearchMessages(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return searchMessagesResult(response), nil
}

func handleConversationsUnreads(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("types", unreadChannelTypes(inv.String("channel_types")))
	values.Set("exclude_archived", "true")
	values.Set("limit", strconv.Itoa(boundedInt(inv.Int("max_channels"), 50, 1, 1000)))
	response, err := client.ConversationsList(exec.Context, values)
	if err != nil {
		return nil, err
	}
	result := unreadsResult{}
	maxMessages := boundedInt(inv.Int("max_messages_per_channel"), 10, 1, 100)
	for _, channel := range response.Channels {
		if skipUnreadChannel(channel, inv) {
			continue
		}
		item := unreadConversation{
			ChannelID:    channel.ID,
			Name:         firstNonEmpty(channel.Name, channel.User),
			Kind:         conversationKind(channel),
			UnreadCount:  channel.UnreadCount,
			MentionCount: channel.UnreadCountDisplay,
		}
		if inv.Bool("include_messages") {
			historyValues := url.Values{}
			historyValues.Set("channel", channel.ID)
			historyValues.Set("limit", strconv.Itoa(maxMessages))
			history, err := client.ConversationsHistory(exec.Context, historyValues)
			if err != nil {
				return nil, err
			}
			item.Messages = history.Messages
		}
		result.Conversations = append(result.Conversations, item)
	}
	return result, nil
}

func handleConversationsMark(exec actions.Context, inv actions.Invocation) (any, error) {
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	ts := strings.TrimSpace(inv.String("ts"))
	if ts == "" {
		ts = fmt.Sprintf("%d.000000", time.Now().Unix())
	}
	request := map[string]string{"channel": channelID, "ts": ts}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.conversations_mark", request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("channel", channelID)
	values.Set("ts", ts)
	if _, err := client.PostOK(exec.Context, "conversations.mark", values); err != nil {
		return nil, err
	}
	return actionResult{Message: "marked Slack conversation read"}, nil
}

func handleChannelsList(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("types", strings.TrimSpace(inv.String("channel_types")))
	if limit := inv.Int("limit"); limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("cursor", cursor)
	}
	response, err := client.ConversationsList(exec.Context, values)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(inv.String("sort")), "popularity") {
		sort.SliceStable(response.Channels, func(i, j int) bool {
			return response.Channels[i].NumMembers > response.Channels[j].NumMembers
		})
	}
	return conversationListResult(response), nil
}

func handleUsergroupsList(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("include_users", strconv.FormatBool(inv.Bool("include_users")))
	values.Set("include_count", strconv.FormatBool(inv.Bool("include_count")))
	values.Set("include_disabled", strconv.FormatBool(inv.Bool("include_disabled")))
	response, err := client.UsergroupsList(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return usergroupsListResult(response), nil
}

func handleUsergroupsMe(exec actions.Context, inv actions.Invocation) (any, error) {
	action := strings.ToLower(strings.TrimSpace(inv.String("action")))
	if action == "" {
		return nil, fmt.Errorf("action is required: list, join, or leave")
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	auth, err := client.AuthTest(exec.Context)
	if err != nil {
		return nil, err
	}
	userID := strings.TrimSpace(auth.UserID)
	if userID == "" {
		return nil, fmt.Errorf("slack auth.test did not return a user ID")
	}
	switch action {
	case "list":
		values := url.Values{}
		values.Set("include_users", "true")
		values.Set("include_count", "true")
		response, err := client.UsergroupsList(exec.Context, values)
		if err != nil {
			return nil, err
		}
		response.Usergroups = slicesWithUser(response.Usergroups, userID)
		return usergroupsListResult(response), nil
	case "join", "leave":
		usergroupID, err := requiredString(inv, "usergroup_id")
		if err != nil {
			return nil, err
		}
		if inv.Bool("dry-run") {
			return actions.NewDryRun("slack.usergroups_me", map[string]string{
				"action":       action,
				"usergroup_id": usergroupID,
				"user_id":      userID,
			}), nil
		}
		values := url.Values{}
		values.Set("usergroup", usergroupID)
		users, err := client.UsergroupsUsersList(exec.Context, values)
		if err != nil {
			return nil, err
		}
		nextUsers := updateUserMembership(users.Users, userID, action == "join")
		updateValues := url.Values{}
		updateValues.Set("usergroup", usergroupID)
		updateValues.Set("users", strings.Join(nextUsers, ","))
		response, err := client.PostUsergroup(exec.Context, "usergroups.users.update", updateValues)
		if err != nil {
			return nil, err
		}
		response.Usergroup.Users = nextUsers
		return usergroupResult(response), nil
	default:
		return nil, fmt.Errorf("unsupported usergroups_me action %q; expected list, join, or leave", action)
	}
}

func handleUsergroupsCreate(exec actions.Context, inv actions.Invocation) (any, error) {
	name, err := requiredString(inv, "name")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("name", name)
	setIfNotEmpty(values, "handle", inv.String("handle"))
	setIfNotEmpty(values, "description", inv.String("description"))
	setIfNotEmpty(values, "channels", inv.String("channels"))
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.usergroups_create", valuesToMap(values)), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.PostUsergroup(exec.Context, "usergroups.create", values)
	if err != nil {
		return nil, err
	}
	return usergroupResult(response), nil
}

func handleUsergroupsUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	usergroupID, err := requiredString(inv, "usergroup_id")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("usergroup", usergroupID)
	setIfNotEmpty(values, "name", inv.String("name"))
	setIfNotEmpty(values, "handle", inv.String("handle"))
	setIfNotEmpty(values, "description", inv.String("description"))
	setIfNotEmpty(values, "channels", inv.String("channels"))
	if len(values) == 1 {
		return nil, fmt.Errorf("at least one of --name, --handle, --description, or --channels is required")
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.usergroups_update", valuesToMap(values)), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.PostUsergroup(exec.Context, "usergroups.update", values)
	if err != nil {
		return nil, err
	}
	return usergroupResult(response), nil
}

func handleUsergroupsUsersUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	usergroupID, err := requiredString(inv, "usergroup_id")
	if err != nil {
		return nil, err
	}
	users, err := requiredString(inv, "users")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("usergroup", usergroupID)
	values.Set("users", strings.Join(cleanCSV(users), ","))
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.usergroups_users_update", valuesToMap(values)), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.PostUsergroup(exec.Context, "usergroups.users.update", values)
	if err != nil {
		return nil, err
	}
	return usergroupResult(response), nil
}

func handleUsersSearch(exec actions.Context, inv actions.Invocation) (any, error) {
	query, err := requiredString(inv, "query")
	if err != nil {
		return nil, err
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	limit := boundedInt(inv.Int("limit"), 10, 1, 100)
	var users []slackapi.User
	cursor := ""
	for page := 0; page < 10 && len(users) < limit; page++ {
		values := url.Values{}
		values.Set("limit", "200")
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		response, err := client.UsersList(exec.Context, values)
		if err != nil {
			return nil, err
		}
		for _, user := range response.Members {
			if !user.Deleted && userMatches(user, query) {
				users = append(users, user)
			}
			if len(users) >= limit {
				break
			}
		}
		cursor = response.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return usersSearchResult{Users: users}, nil
}

func slackClient(exec actions.Context, inv actions.Invocation) (slackapi.Client, error) {
	tokens, err := loadSlackTokens(exec, account(inv))
	if err != nil {
		return slackapi.Client{}, err
	}
	return slackClientForTokens(exec, tokens), nil
}

func slackClientForTokens(exec actions.Context, tokens credentials.OAuthTokens) slackapi.Client {
	return slackapi.Client{
		BaseURL:     firstNonEmpty(tokens.Extra["api_base_url"], exec.ProviderURL),
		HTTPClient:  exec.HTTPClient,
		AccessToken: tokens.AccessToken,
		Cookie:      normalizeSlackCookieHeader(tokens.Extra["cookie"]),
	}
}

func validateSlackAuth(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	response, err := slackClientForTokens(exec, tokens).AuthTest(exec.Context)
	if err != nil {
		return credentials.OAuthTokens{}, fmt.Errorf("slack auth validation failed: %w", err)
	}
	if !response.OK {
		return credentials.OAuthTokens{}, fmt.Errorf("slack auth validation failed: %s", firstNonEmpty(response.Error, "unknown_error"))
	}
	tokens.Extra = mergeExtra(tokens.Extra, slackAuthTestExtra(response))
	return tokens, nil
}

func slackAuthTestExtra(response slackapi.AuthTestResponse) map[string]string {
	extra := map[string]string{}
	if response.URL != "" {
		extra["team_url"] = response.URL
		if apiBaseURL := slackapi.APIBaseURLFromTeamURL(response.URL); apiBaseURL != "" {
			extra["api_base_url"] = apiBaseURL
		}
	}
	if response.TeamID != "" {
		extra["team_id"] = response.TeamID
	}
	if response.Team != "" {
		extra["team_name"] = response.Team
	}
	if response.UserID != "" {
		extra["user_id"] = response.UserID
	}
	if response.User != "" {
		extra["user_name"] = response.User
	}
	return extra
}

func loadSlackTokens(exec actions.Context, account string) (credentials.OAuthTokens, error) {
	ref := slackCredentialRef(exec, account)
	tokens, err := exec.Credentials.LoadOAuthTokens(exec.Context, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.OAuthTokens{}, fmt.Errorf("slack auth is not configured for account %s", account)
		}
		return credentials.OAuthTokens{}, err
	}
	if !slackTokenNeedsRefresh(tokens, time.Now()) {
		return ensureSlackAuthMetadata(exec, ref, tokens)
	}
	refreshed, err := refreshSlackTokens(exec, tokens)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	refreshed.Extra = mergeExtra(refreshed.Extra, tokens.Extra)
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = tokens.RefreshToken
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, refreshed); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return ensureSlackAuthMetadata(exec, ref, refreshed)
}

func ensureSlackAuthMetadata(exec actions.Context, ref credentials.ConnectionRef, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if tokens.Extra["api_base_url"] != "" && tokens.Extra["team_url"] != "" {
		return tokens, nil
	}
	enriched, err := validateSlackAuth(exec, tokens)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, enriched); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return enriched, nil
}

func refreshSlackTokens(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("slack OAuth token is expired and has no refresh token")
	}
	switch tokens.Extra["auth_type"] {
	case authTypeUser:
		options := slackapi.OAuthOptions{
			TokenURL:     firstNonEmpty(tokens.Extra["token_url"], slackapi.OAuthURLFromAPIBase(exec.ProviderURL, "/api/oauth.v2.access")),
			ClientID:     tokens.Extra["client_id"],
			ClientSecret: tokens.Extra["client_secret"],
			TokenSource:  tokens.Extra["token_source"],
		}
		return slackapi.RefreshOAuthToken(exec.Context, exec.HTTPClient, options, tokens.RefreshToken, time.Now())
	case authTypeBroker:
		return refreshBrokerToken(exec, tokens)
	default:
		return credentials.OAuthTokens{}, fmt.Errorf("slack token for auth type %q cannot be refreshed", tokens.Extra["auth_type"])
	}
}

func slackTokenNeedsRefresh(tokens credentials.OAuthTokens, now time.Time) bool {
	if tokens.ExpiresAt.IsZero() || strings.TrimSpace(tokens.RefreshToken) == "" {
		return false
	}
	return !now.Add(oauthRefreshSkew).Before(tokens.ExpiresAt)
}

func slackCredentialRef(exec actions.Context, account string) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   exec.Profile,
		Provider:  providerID,
		AccountID: account,
	}
}

func slackOAuthOptions(exec actions.Context, inv actions.Invocation, clientID, clientSecret, redirectURI string) slackapi.OAuthOptions {
	authURL := firstNonEmpty(inv.String("auth-url"), slackapi.OAuthURLFromAPIBase(exec.ProviderURL, "/oauth/v2/authorize"), slackapi.DefaultAuthURL)
	tokenURL := firstNonEmpty(inv.String("token-url"), slackapi.OAuthURLFromAPIBase(exec.ProviderURL, "/api/oauth.v2.access"), slackapi.DefaultTokenURL)
	scopes := slackapi.CleanScopes(inv.StringSlice("scope"))
	if len(scopes) == 0 {
		scopes = append([]string(nil), defaultOAuthScopes...)
	}
	return slackapi.OAuthOptions{
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		Scopes:       scopes,
		UserScopes:   slackapi.CleanScopes(inv.StringSlice("user-scope")),
		TokenSource:  strings.TrimSpace(inv.String("token-source")),
	}
}

func createBrokerSession(exec actions.Context, inv actions.Invocation) (brokerSession, error) {
	endpoint := strings.TrimRight(exec.ToolmuxdURL, "/") + "/v1/oauth/sessions"
	scopes := slackapi.CleanScopes(inv.StringSlice("scope"))
	if len(scopes) == 0 {
		scopes = append([]string(nil), defaultOAuthScopes...)
	}
	body := map[string]any{
		"provider": providerID,
		"profile":  exec.Profile,
		"account":  account(inv),
		"scopes":   scopes,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return brokerSession{}, err
	}
	req, err := http.NewRequestWithContext(exec.Context, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return brokerSession{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return brokerSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return brokerSession{}, fmt.Errorf("toolmux OAuth broker returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session brokerSession
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return brokerSession{}, err
	}
	if strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.AuthURL) == "" {
		return brokerSession{}, fmt.Errorf("toolmux OAuth broker returned an incomplete session")
	}
	return session, nil
}

func pollBrokerSession(exec actions.Context, sessionID string, timeout time.Duration) (credentials.OAuthTokens, error) {
	ctx, cancel := context.WithTimeout(exec.Context, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		tokens, done, err := getBrokerSession(ctx, exec, sessionID)
		if err != nil {
			return credentials.OAuthTokens{}, err
		}
		if done {
			return tokens, nil
		}
		select {
		case <-ctx.Done():
			return credentials.OAuthTokens{}, fmt.Errorf("timed out waiting for slack broker OAuth: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func getBrokerSession(ctx context.Context, exec actions.Context, sessionID string) (credentials.OAuthTokens, bool, error) {
	endpoint := strings.TrimRight(exec.ToolmuxdURL, "/") + "/v1/oauth/sessions/" + url.PathEscape(sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials.OAuthTokens{}, false, fmt.Errorf("toolmux OAuth broker returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session brokerSessionStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	switch session.Status {
	case "pending":
		return credentials.OAuthTokens{}, false, nil
	case "complete":
		if session.Tokens == nil {
			return credentials.OAuthTokens{}, false, fmt.Errorf("toolmux OAuth broker completed without tokens")
		}
		return *session.Tokens, true, nil
	case "failed":
		return credentials.OAuthTokens{}, false, fmt.Errorf("slack broker OAuth failed: %s", session.Error)
	case "expired":
		return credentials.OAuthTokens{}, false, fmt.Errorf("slack broker OAuth session expired")
	default:
		return credentials.OAuthTokens{}, false, fmt.Errorf("slack broker OAuth returned unknown status %q", session.Status)
	}
}

func refreshBrokerToken(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	brokerURL := firstNonEmpty(tokens.Extra["broker_url"], exec.ToolmuxdURL)
	endpoint := strings.TrimRight(brokerURL, "/") + "/v1/oauth/slack/refresh"
	data, err := json.Marshal(map[string]string{"refresh_token": tokens.RefreshToken})
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req, err := http.NewRequestWithContext(exec.Context, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials.OAuthTokens{}, fmt.Errorf("slack broker refresh returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var refreshed credentials.OAuthTokens
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&refreshed); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return refreshed, nil
}

func secretFromInvocation(exec actions.Context, inv actions.Invocation, name string) (string, error) {
	if value := strings.TrimSpace(inv.String(name)); value != "" {
		return value, nil
	}
	if envName := strings.TrimSpace(inv.String(name + "-env")); envName != "" {
		return strings.TrimSpace(os.Getenv(envName)), nil
	}
	if fileName := strings.TrimSpace(inv.String(name + "-file")); fileName != "" {
		readFile := exec.ReadFile
		if readFile == nil {
			readFile = os.ReadFile
		}
		data, err := readFile(fileName)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", nil
}

func normalizeSlackCookieHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "=") {
		return value
	}
	return "d=" + value
}

func account(inv actions.Invocation) string {
	value := strings.TrimSpace(inv.String("account"))
	if value == "" {
		return defaultAccount
	}
	return value
}

func timeout(inv actions.Invocation) time.Duration {
	seconds := inv.Int("timeout-seconds")
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

type oauthCallback struct {
	redirectURI string
	server      *http.Server
	results     <-chan oauthCallbackResult
}

type oauthCallbackResult struct {
	Code string
	Err  error
}

func startOAuthCallback(port int, state string) (oauthCallback, error) {
	if port < 0 || port > 65535 {
		return oauthCallback{}, fmt.Errorf("redirect port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return oauthCallback{}, err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return oauthCallback{}, fmt.Errorf("oauth callback listener did not return a TCP address")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", tcpAddr.Port, callbackPath)
	results := make(chan oauthCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		result := oauthCallbackResult{}
		switch {
		case query.Get("state") != state:
			result.Err = fmt.Errorf("slack OAuth callback state mismatch")
		case query.Get("error") != "":
			result.Err = fmt.Errorf("slack OAuth callback error: %s", query.Get("error"))
		case query.Get("code") == "":
			result.Err = fmt.Errorf("slack OAuth callback did not include a code")
		default:
			result.Code = query.Get("code")
		}
		writeCallbackPage(w, result.Err)
		select {
		case results <- result:
		default:
		}
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case results <- oauthCallbackResult{Err: err}:
			default:
			}
		}
	}()
	return oauthCallback{
		redirectURI: redirectURI,
		server:      server,
		results:     results,
	}, nil
}

func (callback oauthCallback) wait(ctx context.Context, timeout time.Duration) (oauthCallbackResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case result := <-callback.results:
		return result, nil
	case <-ctx.Done():
		return oauthCallbackResult{}, fmt.Errorf("timed out waiting for Slack OAuth callback: %w", ctx.Err())
	}
}

func (callback oauthCallback) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = callback.server.Shutdown(ctx)
}

func writeCallbackPage(w http.ResponseWriter, callbackErr error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if callbackErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "<!doctype html><title>Slack OAuth failed</title><p>Slack OAuth failed. Return to Toolmux.</p>")
		return
	}
	_, _ = io.WriteString(w, "<!doctype html><title>Slack OAuth complete</title><p>Slack OAuth complete. Return to Toolmux.</p>")
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type authResult struct {
	Message string `json:"message" yaml:"message"`
}

func (result authResult) Text() string {
	return result.Message
}

type actionResult struct {
	Message string `json:"message" yaml:"message"`
}

func (result actionResult) Text() string {
	return result.Message
}

type authTestResult slackapi.AuthTestResponse

func (result authTestResult) Table(opts output.Options) output.Table {
	return output.Table{
		Headers: []string{"Team", "Team ID", "User", "User ID", "URL"},
		Rows: [][]string{{
			firstNonEmpty(result.Team, "-"),
			firstNonEmpty(result.TeamID, "-"),
			firstNonEmpty(result.User, "-"),
			firstNonEmpty(result.UserID, "-"),
			firstNonEmpty(result.URL, "-"),
		}},
	}
}

type conversationMessagesResult struct {
	ChannelID   string             `json:"channel_id" yaml:"channel_id"`
	ThreadTS    string             `json:"thread_ts,omitempty" yaml:"thread_ts,omitempty"`
	Messages    []slackapi.Message `json:"messages" yaml:"messages"`
	HasMore     bool               `json:"has_more" yaml:"has_more"`
	NextCursor  string             `json:"next_cursor,omitempty" yaml:"next_cursor,omitempty"`
	ResultLabel string             `json:"-" yaml:"-"`
}

func (result conversationMessagesResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Messages))
	for _, message := range result.Messages {
		rows = append(rows, []string{
			result.ChannelID,
			firstNonEmpty(message.User, message.Username, message.BotID, "-"),
			message.TS,
			firstNonEmpty(message.ThreadTS, "-"),
			trimForTable(message.Text, 96),
			result.NextCursor,
		})
	}
	empty := "no Slack messages"
	if result.ResultLabel != "" {
		empty = "no Slack " + result.ResultLabel
	}
	return output.Table{
		Headers: []string{"Channel", "User", "TS", "Thread", "Text", "Next Cursor"},
		Rows:    rows,
		Empty:   empty,
	}
}

type conversationListResult slackapi.ConversationsListResponse

func (result conversationListResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Channels))
	for _, channel := range result.Channels {
		rows = append(rows, []string{
			channel.ID,
			firstNonEmpty(channel.Name, "-"),
			conversationKind(channel),
			strconv.FormatBool(channel.IsArchived),
			strconv.Itoa(channel.NumMembers),
		})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "Kind", "Archived", "Members"},
		Rows:    rows,
		Empty:   "no Slack conversations",
	}
}

type searchMessagesResult slackapi.SearchMessagesResponse

func (result searchMessagesResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Messages.Matches))
	for _, match := range result.Messages.Matches {
		rows = append(rows, []string{
			firstNonEmpty(match.Channel.Name, match.Channel.ID, "-"),
			firstNonEmpty(match.User, match.Username, "-"),
			match.TS,
			trimForTable(match.Text, 96),
			match.Permalink,
		})
	}
	return output.Table{
		Headers: []string{"Conversation", "User", "TS", "Text", "Permalink"},
		Rows:    rows,
		Empty:   "no Slack messages",
	}
}

type sendMessageRequest struct {
	Channel     string `json:"channel" yaml:"channel"`
	Text        string `json:"text" yaml:"text"`
	ThreadTS    string `json:"thread_ts,omitempty" yaml:"thread_ts,omitempty"`
	ContentType string `json:"content_type,omitempty" yaml:"content_type,omitempty"`
}

type sendMessageResult slackapi.ChatPostMessageResponse

func (result sendMessageResult) Table(opts output.Options) output.Table {
	text := result.Message.Text
	if text == "" {
		text = "-"
	}
	return output.Table{
		Headers: []string{"Channel", "TS", "Text"},
		Rows: [][]string{{
			result.Channel,
			result.TS,
			trimForTable(text, 96),
		}},
	}
}

type openConversationRequest struct {
	Users           string `json:"users" yaml:"users"`
	PreventCreation bool   `json:"prevent_creation,omitempty" yaml:"prevent_creation,omitempty"`
	ReturnIM        bool   `json:"return_im,omitempty" yaml:"return_im,omitempty"`
}

type openConversationResult slackapi.ConversationsOpenResponse

func (result openConversationResult) Table(opts output.Options) output.Table {
	kind := conversationKind(result.Channel)
	return output.Table{
		Headers: []string{"ID", "Kind", "User"},
		Rows: [][]string{{
			result.Channel.ID,
			kind,
			result.Channel.User,
		}},
	}
}

type reactionRequest struct {
	Channel   string `json:"channel" yaml:"channel"`
	Timestamp string `json:"timestamp" yaml:"timestamp"`
	Emoji     string `json:"emoji" yaml:"emoji"`
}

type attachmentDataResult struct {
	FileID   string `json:"file_id" yaml:"file_id"`
	Filename string `json:"filename,omitempty" yaml:"filename,omitempty"`
	Mimetype string `json:"mimetype,omitempty" yaml:"mimetype,omitempty"`
	Size     int    `json:"size" yaml:"size"`
	Encoding string `json:"encoding,omitempty" yaml:"encoding,omitempty"`
	Content  string `json:"content,omitempty" yaml:"content,omitempty"`
}

func (result attachmentDataResult) Table(opts output.Options) output.Table {
	return output.Table{
		Headers: []string{"File", "Name", "Mimetype", "Size", "Encoding"},
		Rows: [][]string{{
			result.FileID,
			firstNonEmpty(result.Filename, "-"),
			firstNonEmpty(result.Mimetype, "-"),
			strconv.Itoa(result.Size),
			firstNonEmpty(result.Encoding, "-"),
		}},
	}
}

type unreadConversation struct {
	ChannelID    string             `json:"channel_id" yaml:"channel_id"`
	Name         string             `json:"name,omitempty" yaml:"name,omitempty"`
	Kind         string             `json:"kind,omitempty" yaml:"kind,omitempty"`
	UnreadCount  int                `json:"unread_count" yaml:"unread_count"`
	MentionCount int                `json:"mention_count" yaml:"mention_count"`
	Messages     []slackapi.Message `json:"messages,omitempty" yaml:"messages,omitempty"`
}

type unreadsResult struct {
	Conversations []unreadConversation `json:"conversations" yaml:"conversations"`
}

func (result unreadsResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Conversations))
	for _, conversation := range result.Conversations {
		text := ""
		if len(conversation.Messages) > 0 {
			text = conversation.Messages[0].Text
		}
		rows = append(rows, []string{
			conversation.ChannelID,
			firstNonEmpty(conversation.Name, "-"),
			conversation.Kind,
			strconv.Itoa(conversation.UnreadCount),
			strconv.Itoa(conversation.MentionCount),
			trimForTable(text, 72),
		})
	}
	return output.Table{
		Headers: []string{"Channel", "Name", "Kind", "Unread", "Mentions", "Latest"},
		Rows:    rows,
		Empty:   "no Slack unread conversations",
	}
}

type usergroupsListResult slackapi.UsergroupsListResponse

func (result usergroupsListResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Usergroups))
	for _, group := range result.Usergroups {
		rows = append(rows, []string{
			group.ID,
			group.Name,
			group.Handle,
			strconv.Itoa(group.UserCount),
			strconv.FormatBool(group.IsDisabled),
			strconv.FormatBool(group.IsExternal),
			strings.Join(group.Users, ","),
		})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "Handle", "Users", "Disabled", "External", "Members"},
		Rows:    rows,
		Empty:   "no Slack user groups",
	}
}

type usergroupResult slackapi.UsergroupResponse

func (result usergroupResult) Table(opts output.Options) output.Table {
	group := result.Usergroup
	return output.Table{
		Headers: []string{"ID", "Name", "Handle", "Users", "Description"},
		Rows: [][]string{{
			group.ID,
			group.Name,
			group.Handle,
			strconv.Itoa(group.UserCount),
			trimForTable(group.Description, 80),
		}},
	}
}

type usersSearchResult struct {
	Users []slackapi.User `json:"users" yaml:"users"`
}

func (result usersSearchResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Users))
	for _, user := range result.Users {
		rows = append(rows, []string{
			user.ID,
			user.Name,
			firstNonEmpty(user.RealName, user.Profile.RealName),
			user.Profile.DisplayName,
			user.Profile.Email,
			user.Profile.Title,
		})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "Real Name", "Display Name", "Email", "Title"},
		Rows:    rows,
		Empty:   "no Slack users",
	}
}

type brokerSession struct {
	SessionID string    `json:"session_id"`
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type brokerSessionStatus struct {
	SessionID string                   `json:"session_id"`
	Provider  string                   `json:"provider"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
	Extra     map[string]string        `json:"extra,omitempty"`
}

func conversationMessageValues(inv actions.Invocation) url.Values {
	values := url.Values{}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("cursor", cursor)
	}
	setIfNotEmpty(values, "oldest", inv.String("oldest"))
	setIfNotEmpty(values, "latest", inv.String("latest"))
	if inv.Bool("inclusive") {
		values.Set("inclusive", "true")
	}
	values.Set("limit", strconv.Itoa(parseSlackLimit(inv.String("limit"), 100)))
	return values
}

func parseSlackLimit(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return boundedInt(limit, fallback, 1, 1000)
}

func boundedInt(value, fallback, minValue, maxValue int) int {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if maxValue > 0 && value > maxValue {
		return maxValue
	}
	return value
}

func requiredString(inv actions.Invocation, name string) (string, error) {
	value := strings.TrimSpace(inv.String(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func filterActivityMessages(messages []slackapi.Message, include bool) []slackapi.Message {
	if include {
		return messages
	}
	out := make([]slackapi.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Subtype {
		case "channel_join", "channel_leave", "channel_topic", "channel_purpose", "channel_name":
			continue
		default:
			out = append(out, message)
		}
	}
	return out
}

func reactionRequestFromInvocation(inv actions.Invocation) (reactionRequest, error) {
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return reactionRequest{}, err
	}
	timestamp, err := requiredString(inv, "timestamp")
	if err != nil {
		return reactionRequest{}, err
	}
	emoji, err := requiredString(inv, "emoji")
	if err != nil {
		return reactionRequest{}, err
	}
	return reactionRequest{
		Channel:   channelID,
		Timestamp: timestamp,
		Emoji:     strings.Trim(emoji, ":"),
	}, nil
}

func isTextMimetype(mimetype string) bool {
	mimetype = strings.ToLower(strings.TrimSpace(mimetype))
	if strings.HasPrefix(mimetype, "text/") {
		return true
	}
	switch mimetype {
	case "application/json", "application/xml", "application/javascript", "application/x-yaml", "application/yaml", "application/x-sh":
		return true
	default:
		return false
	}
}

func slackSearchQuery(inv actions.Invocation) string {
	parts := []string{}
	if query := strings.TrimSpace(inv.String("search_query")); query != "" {
		parts = append(parts, query)
	}
	addSearchFilter := func(prefix, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, prefix+":"+value)
		}
	}
	addSearchFilter("in", inv.String("filter_in_channel"))
	addSearchFilter("in", inv.String("filter_in_im_or_mpim"))
	addSearchFilter("with", inv.String("filter_users_with"))
	addSearchFilter("from", inv.String("filter_users_from"))
	addSearchFilter("before", inv.String("filter_date_before"))
	addSearchFilter("after", inv.String("filter_date_after"))
	addSearchFilter("on", inv.String("filter_date_on"))
	addSearchFilter("during", inv.String("filter_date_during"))
	if inv.Bool("filter_threads_only") {
		parts = append(parts, "is:thread")
	}
	return strings.Join(parts, " ")
}

func unreadChannelTypes(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dm":
		return "im"
	case "group_dm", "group-dm", "mpim":
		return "mpim"
	case "partner", "internal":
		return "public_channel,private_channel"
	case "", "all":
		return "public_channel,private_channel,mpim,im"
	default:
		return value
	}
}

func skipUnreadChannel(channel slackapi.Conversation, inv actions.Invocation) bool {
	if channel.IsMuted && !inv.Bool("include_muted") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(inv.String("channel_types"))) {
	case "partner":
		if !channel.IsExtShared {
			return true
		}
	case "internal":
		if channel.IsExtShared {
			return true
		}
	}
	return inv.Bool("mentions_only") && channel.UnreadCountDisplay == 0
}

func setIfNotEmpty(values url.Values, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values.Set(key, value)
	}
}

func valuesToMap(values url.Values) map[string]string {
	out := make(map[string]string, len(values))
	for key, vals := range values {
		if len(vals) > 0 {
			out[key] = vals[0]
		}
	}
	return out
}

func cleanCSV(value string) []string {
	seen := map[string]bool{}
	var out []string
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func slicesWithUser(groups []slackapi.Usergroup, userID string) []slackapi.Usergroup {
	out := make([]slackapi.Usergroup, 0, len(groups))
	for _, group := range groups {
		if slices.Contains(group.Users, userID) {
			out = append(out, group)
		}
	}
	return out
}

func updateUserMembership(users []string, userID string, member bool) []string {
	seen := map[string]bool{}
	for _, user := range users {
		user = strings.TrimSpace(user)
		if user != "" {
			seen[user] = true
		}
	}
	if member {
		seen[userID] = true
	} else {
		delete(seen, userID)
	}
	out := make([]string, 0, len(seen))
	for user := range seen {
		out = append(out, user)
	}
	sort.Strings(out)
	return out
}

func userMatches(user slackapi.User, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		user.ID,
		user.Name,
		user.RealName,
		user.Profile.RealName,
		user.Profile.DisplayName,
		user.Profile.Email,
		user.Profile.Title,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func conversationKind(channel slackapi.Conversation) string {
	switch {
	case channel.IsIM:
		return "im"
	case channel.IsMPIM:
		return "mpim"
	case channel.IsGroup || channel.IsPrivate:
		return "private"
	case channel.IsChannel:
		return "public"
	default:
		return "conversation"
	}
}

func trimForTable(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit-1]) + "..."
}

func mergeExtra(target map[string]string, values map[string]string) map[string]string {
	if target == nil {
		target = map[string]string{}
	}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		target[key] = value
	}
	return target
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
