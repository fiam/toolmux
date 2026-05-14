package slackauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Extract launches a browser, waits for the user to sign in to Slack, picks a
// workspace (auto, via Chooser, or the only one available) and returns its
// xoxc token plus the `d` cookie.
//
// The returned error is ctx.Err() if the user cancels (Ctrl-C) or the timeout
// fires before sign-in completes.
func Extract(ctx context.Context, opts Options) (*Session, error) {
	if opts.Engine == "" {
		opts.Engine = defaultEngine()
	}
	if opts.Engine == "" {
		return nil, fmt.Errorf("no browser auth engine available on %s", runtime.GOOS)
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	teams, cookie, err := runEngine(ctx, opts)
	if err != nil {
		return nil, err
	}

	team, token, err := pickTeam(teams, opts)
	if err != nil {
		return nil, err
	}
	return &Session{
		TeamID:     team.ID,
		TeamName:   team.Name,
		TeamDomain: team.Domain,
		Token:      token,
		Cookie:     cookie,
	}, nil
}

func runEngine(ctx context.Context, opts Options) ([]teamWithToken, string, error) {
	switch opts.Engine {
	case EngineWebView:
		return extractWebView(ctx, opts)
	case EngineChrome:
		return extractChrome(ctx, opts)
	default:
		return nil, "", fmt.Errorf("unsupported engine %q (valid: webview, chrome)", opts.Engine)
	}
}

// teamWithToken is the internal shape: it carries the xoxc token alongside the
// public Team fields so we never accidentally hand tokens out through Team.
type teamWithToken struct {
	Team
	Token string `json:"token"`
}

// decodeTeamsJSON parses the JSON our injected JS returns. Empty / null input
// is treated as "no teams yet" rather than an error, so callers can poll.
//
//lint:ignore U1000 used by platform-specific webview backends.
func decodeTeamsJSON(s string) ([]teamWithToken, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" || s == "undefined" {
		return nil, nil
	}
	var teams []teamWithToken
	if err := json.Unmarshal([]byte(s), &teams); err != nil {
		return nil, fmt.Errorf("decode teams: %w", err)
	}
	return teams, nil
}

func pickTeam(teams []teamWithToken, opts Options) (Team, string, error) {
	if len(teams) == 0 {
		return Team{}, "", errors.New("no Slack workspaces found in session")
	}

	if opts.WorkspaceDomain != "" {
		for _, t := range teams {
			if strings.EqualFold(t.Domain, opts.WorkspaceDomain) {
				return t.Team, t.Token, nil
			}
		}
		// If team metadata wasn't extractable (e.g. the JS fell back to a raw
		// xoxc scan) and we only have one team, trust the user's -workspace
		// flag — they intended exactly that workspace.
		if len(teams) == 1 {
			return teams[0].Team, teams[0].Token, nil
		}
		return Team{}, "", fmt.Errorf("workspace %q not signed in (have: %s)", opts.WorkspaceDomain, joinDomains(teams))
	}

	if len(teams) == 1 || opts.Chooser == nil {
		return teams[0].Team, teams[0].Token, nil
	}

	public := make([]Team, len(teams))
	for i, t := range teams {
		public[i] = t.Team
	}
	picked, err := opts.Chooser(public)
	if err != nil {
		return Team{}, "", err
	}
	for _, t := range teams {
		if t.ID == picked.ID {
			return t.Team, t.Token, nil
		}
	}
	return Team{}, "", fmt.Errorf("chooser returned unknown team id %q", picked.ID)
}

// startURL picks the initial page to load. We deliberately avoid slack.com/*
// (the marketing site, which does aggressive server-side UA sniffing and 403s
// our launched Chrome as "unsupported"). app.slack.com is the actual web app
// and is lenient: signed-out users get a "Sign in to Slack" form there, and
// signed-in users land in their workspace.
//
//lint:ignore U1000 used by platform-specific webview backends.
func startURL(workspace string) string {
	if workspace == "" {
		return "https://app.slack.com/"
	}
	return "https://" + workspace + ".slack.com/"
}

func joinDomains(teams []teamWithToken) string {
	ds := make([]string, len(teams))
	for i, t := range teams {
		ds[i] = t.Domain
	}
	return strings.Join(ds, ", ")
}
