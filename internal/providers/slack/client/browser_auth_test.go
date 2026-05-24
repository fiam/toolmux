package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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
		opts.OnEvent(slackauth.Event{Kind: slackauth.EventLaunching, Detail: "Test Browser"})
		opts.OnEvent(slackauth.Event{Kind: slackauth.EventWaiting, Detail: "Waiting for test Slack login"})
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
	progress := &recordingProgress{}
	_, err := handleAdd(actions.Context{
		Context:     context.Background(),
		Credentials: store,
		HTTPClient:  upstream.Client(),
		Profile:     "default",
		Account:     providerID,
		Provider:    providerID,
		ProviderURL: upstream.URL + "/api",
		Progress:    progress,
	}, actions.Invocation{Flags: map[string]any{
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
	for _, want := range []string{
		"status:Launching Slack browser auth in Test Browser",
		"start:Waiting for test Slack login",
		"done:Received Slack browser credentials",
		"start:Validating Slack auth",
		"done:Slack auth verified",
	} {
		if !progress.Contains(want) {
			t.Fatalf("expected progress event %q, got %#v", want, progress.events)
		}
	}

	tokens, err := store.LoadOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  providerID,
		AccountID: providerID,
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
		Account:     providerID,
		Provider:    providerID,
	}, actions.Invocation{Flags: map[string]any{
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

type recordingProgress struct {
	events []string
}

func (progress *recordingProgress) Start(message string) actions.ProgressHandle {
	progress.record("start:" + message)
	return &recordingProgressHandle{progress: progress}
}

func (progress *recordingProgress) Status(message string) {
	progress.record("status:" + message)
}

func (progress *recordingProgress) Warn(message string) {
	progress.record("warn:" + message)
}

func (progress *recordingProgress) Done(message string) {
	progress.record("done:" + message)
}

func (progress *recordingProgress) Contains(event string) bool {
	return slices.Contains(progress.events, event)
}

func (progress *recordingProgress) record(event string) {
	progress.events = append(progress.events, event)
}

type recordingProgressHandle struct {
	progress *recordingProgress
}

func (handle *recordingProgressHandle) Update(message string) {
	handle.progress.record("update:" + message)
}

func (handle *recordingProgressHandle) Stop() {
	handle.progress.record("stop")
}

func (handle *recordingProgressHandle) Warn(message string) {
	handle.progress.record("warn:" + message)
}

func (handle *recordingProgressHandle) Done(message string) {
	handle.progress.record("done:" + message)
}
