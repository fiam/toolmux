// Package slackauth extracts a Slack web session (xoxc token + d cookie) by
// driving an explicit browser-auth flow.
package slackauth

import "time"

// Team is a Slack workspace the user is signed into. The xoxc token for each
// team is intentionally not exposed here — callers receive it via Session.
type Team struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

// Session is the extracted Slack session for a single workspace.
type Session struct {
	TeamID     string `json:"team_id"`
	TeamName   string `json:"team_name"`
	TeamDomain string `json:"team_domain"`
	// Token is the xoxc-... value; pass it as SLACK_MCP_XOXC_TOKEN.
	Token string `json:"token"`
	// Cookie is the raw value of the `d` cookie; pass it as SLACK_MCP_XOXD_TOKEN.
	Cookie string `json:"cookie"`
}

// Engine names a browser backend.
type Engine string

const (
	// EngineWebView uses a native system WebView — WKWebView on macOS,
	// WebView2 on Windows. Returns an error on Linux. No external
	// browser install is required; the user signs in inside a window we
	// create.
	EngineWebView Engine = "webview"

	// EngineChrome rides along with the user's *existing* Chrome install. We
	// ask Chrome (via `open -a` / equivalent) to focus a Slack URL, then poll
	// Chrome's on-disk cookie store and localStorage for the credentials —
	// all while the user's normal Chrome session, autofill, extensions, and
	// 2SV continue to work. Currently supported on macOS and Linux.
	EngineChrome Engine = "chrome"
)

// EventKind enumerates the lifecycle signals emitted via Options.OnEvent.
type EventKind string

const (
	// EventLaunching: a browser process or window is about to be created.
	// Event.Detail is a human-readable target ("WKWebView" or
	// "Google Chrome") and Event.URL is the URL we're loading, if any.
	EventLaunching EventKind = "launching"

	// EventExpectingAuth: the user is about to be prompted for a credential
	// outside our window — typically a macOS Keychain prompt for the "Chrome
	// Safe Storage" password, or a system password for sudo/keychain unlock.
	// Use this to surface a heads-up in your CLI before the prompt appears.
	EventExpectingAuth EventKind = "expecting_auth"

	// EventWaiting: we're polling for a state change that depends on the
	// user (typically sign-in completion). Event.Detail describes what we're
	// waiting on.
	EventWaiting EventKind = "waiting"

	// EventInfo: generic status update. Use sparingly.
	EventInfo EventKind = "info"
)

// Event is a structured progress signal sent through Options.OnEvent.
// New fields may be added over time; callers should ignore unknown Kinds.
type Event struct {
	Kind   EventKind
	Detail string // human-readable description
	URL    string // optional: relevant URL, e.g. the page we're about to load
}

// Options configure Extract.
type Options struct {
	// Engine picks which browser backend to drive. Empty selects the platform
	// default: webview when available (macOS, Windows), else chrome (Linux).
	Engine Engine

	// WorkspaceDomain, if set, auto-picks the team whose slack subdomain matches
	// (the leading label of {domain}.slack.com), and also navigates directly to
	// that workspace's sign-in page when starting.
	WorkspaceDomain string

	// Chooser is invoked when more than one workspace is signed in and no
	// WorkspaceDomain was given. If nil, the first team is used.
	Chooser func(teams []Team) (Team, error)

	// OnEvent receives progress signals during extraction so callers can show
	// the user what's happening — especially useful before a system Keychain
	// prompt or browser window appears. May be nil.
	OnEvent func(Event)

	// UserDataDir is the browser profile directory. Reusing the same one across
	// runs keeps the user signed in. Currently unused by both shipping
	// engines (the WKWebView default data store is process-scoped, and the
	// chrome engine reads the user's existing Chrome profile). Kept for
	// forward compatibility.
	UserDataDir string

	// Timeout bounds the whole flow. Zero means no timeout (wait until ctx is
	// canceled — typically by the user pressing Ctrl-C).
	Timeout time.Duration
}

// emit is a helper for engines: invokes opts.OnEvent if set, otherwise no-op.
func (o Options) emit(e Event) {
	if o.OnEvent != nil {
		o.OnEvent(e)
	}
}
