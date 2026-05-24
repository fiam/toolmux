package slack

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
	"github.com/fiam/toolmux/internal/slackauth"
)

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
	validateProgress := exec.StartProgress("Validating Slack auth")
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		validateProgress.Warn("Slack auth validation failed")
		return nil, err
	}
	validateProgress.Done("Slack auth verified")
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
	progress := newSlackBrowserAuthProgress(exec)
	defer progress.Close()
	session, err := slackAuthExtract(exec.Context, slackauth.Options{
		Engine:          engine,
		WorkspaceDomain: workspace,
		Chooser:         slackAuthChooser(exec),
		OnEvent:         progress.Event,
		Timeout:         timeout(inv),
	})
	if err != nil {
		progress.Warn("Slack browser auth failed")
		return nil, fmt.Errorf("slack browser auth failed: %w", err)
	}
	if session == nil || strings.TrimSpace(session.Token) == "" || strings.TrimSpace(session.Cookie) == "" {
		progress.Warn("Slack browser auth did not return credentials")
		return nil, fmt.Errorf("slack browser auth did not return both token and cookie")
	}
	progress.Done("Received Slack browser credentials")
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
	validateProgress := exec.StartProgress("Validating Slack auth")
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		validateProgress.Warn("Slack auth validation failed")
		return nil, err
	}
	validateProgress.Done("Slack auth verified")
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

type slackBrowserAuthProgress struct {
	exec    actions.Context
	current actions.ProgressHandle
}

func newSlackBrowserAuthProgress(exec actions.Context) *slackBrowserAuthProgress {
	return &slackBrowserAuthProgress{exec: exec}
}

func (progress *slackBrowserAuthProgress) Event(event slackauth.Event) {
	message := slackBrowserAuthProgressMessage(event)
	if message == "" {
		return
	}
	switch event.Kind {
	case slackauth.EventWaiting:
		if progress.current == nil {
			progress.current = progress.exec.StartProgress(message)
			return
		}
		progress.current.Update(message)
	case slackauth.EventExpectingAuth:
		progress.Stop()
		progress.exec.ProgressWarn(message)
	case slackauth.EventLaunching, slackauth.EventInfo:
		progress.Stop()
		progress.exec.ProgressStatus(message)
	default:
		progress.Stop()
		progress.exec.ProgressStatus(message)
	}
}

func (progress *slackBrowserAuthProgress) Stop() {
	if progress.current == nil {
		return
	}
	progress.current.Stop()
	progress.current = nil
}

func (progress *slackBrowserAuthProgress) Warn(message string) {
	if progress.current == nil {
		progress.exec.ProgressWarn(message)
		return
	}
	progress.current.Warn(message)
	progress.current = nil
}

func (progress *slackBrowserAuthProgress) Done(message string) {
	if progress.current == nil {
		progress.exec.ProgressDone(message)
		return
	}
	progress.current.Done(message)
	progress.current = nil
}

func (progress *slackBrowserAuthProgress) Close() {
	progress.Stop()
}

func slackBrowserAuthProgressMessage(event slackauth.Event) string {
	detail := strings.TrimSpace(event.Detail)
	switch event.Kind {
	case slackauth.EventLaunching:
		if detail == "" {
			return "Launching Slack browser auth"
		}
		return "Launching Slack browser auth in " + detail
	case slackauth.EventExpectingAuth:
		if detail == "" {
			return "Slack browser auth needs approval"
		}
		return detail
	case slackauth.EventWaiting:
		if detail == "" {
			return "Waiting for Slack browser auth"
		}
		return detail
	case slackauth.EventInfo:
		return detail
	default:
		return detail
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
	exec.ProgressStatus("Opened browser for Slack OAuth")
	callbackProgress := exec.StartProgress("Waiting for Slack OAuth callback")
	result, err := callback.wait(exec.Context, timeout(inv))
	if err != nil {
		callbackProgress.Warn("Slack OAuth callback wait failed")
		return nil, err
	}
	if result.Err != nil {
		callbackProgress.Warn("Slack OAuth callback failed")
		return nil, result.Err
	}
	callbackProgress.Done("Received Slack OAuth callback")
	exchangeProgress := exec.StartProgress("Exchanging Slack OAuth code")
	tokens, err := slackapi.ExchangeOAuthCode(exec.Context, exec.HTTPClient, options, result.Code, time.Now())
	if err != nil {
		exchangeProgress.Warn("Slack OAuth token exchange failed")
		return nil, err
	}
	exchangeProgress.Done("Received Slack OAuth token")
	tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
		"auth_type":     authTypeUser,
		"client_id":     clientID,
		"client_secret": clientSecret,
		"token_url":     options.TokenURL,
		"token_source":  options.TokenSource,
	})
	validateProgress := exec.StartProgress("Validating Slack auth")
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		validateProgress.Warn("Slack auth validation failed")
		return nil, err
	}
	validateProgress.Done("Slack auth verified")
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, slackCredentialRef(exec, account(inv)), tokens); err != nil {
		return nil, err
	}
	return authResult{Message: "stored Slack OAuth token for account " + account(inv)}, nil
}

func handleBrokerLogin(exec actions.Context, inv actions.Invocation) (any, error) {
	sessionProgress := exec.StartProgress("Creating Slack broker OAuth session")
	session, err := createBrokerSession(exec, inv)
	if err != nil {
		sessionProgress.Warn("Slack broker OAuth session failed")
		return nil, err
	}
	sessionProgress.Done("Created Slack broker OAuth session")
	if exec.OpenBrowser == nil {
		return nil, fmt.Errorf("browser opener is not configured")
	}
	if err := exec.OpenBrowser(session.AuthURL); err != nil {
		return nil, err
	}
	exec.ProgressStatus("Opened browser for Slack broker OAuth")
	pollProgress := exec.StartProgress("Waiting for Slack broker OAuth")
	tokens, err := pollBrokerSession(exec, session.SessionID, timeout(inv))
	if err != nil {
		pollProgress.Warn("Slack broker OAuth failed")
		return nil, err
	}
	pollProgress.Done("Received Slack broker OAuth token")
	tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
		"auth_type":  authTypeBroker,
		"broker_url": strings.TrimRight(exec.ToolmuxdURL, "/"),
	})
	validateProgress := exec.StartProgress("Validating Slack auth")
	tokens, err = validateSlackAuth(exec, tokens)
	if err != nil {
		validateProgress.Warn("Slack auth validation failed")
		return nil, err
	}
	validateProgress.Done("Slack auth verified")
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
