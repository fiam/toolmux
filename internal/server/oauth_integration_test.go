package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/notion/notiontest"
	"github.com/fiam/toolmux/internal/testutil/toolmuxdtest"
)

func TestIntegrationNotionOAuthLocalCustody(t *testing.T) {
	t.Parallel()
	upstream := notiontest.NewUpstream()
	defer upstream.Close()
	toolmuxd := toolmuxdtest.NewNotion(t, upstream.URL, upstream.Client())

	session := createNotionSession(t, toolmuxd)
	if session.AuthURL == "" {
		t.Fatal("expected auth URL")
	}
	if session.RedirectURI != toolmuxd.URL+"/oauth/notion/callback" {
		t.Fatalf("unexpected redirect URI %q", session.RedirectURI)
	}

	resp, err := toolmuxd.Client().Get(session.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected callback to complete, got %d", resp.StatusCode)
	}
	html := string(page)
	for _, want := range []string{"Notion is connected", "agent link established", "return to your terminal"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected success page to contain %q, got %q", want, html)
		}
	}
	if strings.Contains(html, "fake-access-token") || strings.Contains(html, "fake-refresh-token") {
		t.Fatal("success page leaked token material")
	}

	completed := getNotionSession(t, toolmuxd, session.SessionID)
	if completed.Status != "complete" {
		t.Fatalf("expected complete session, got %#v", completed)
	}
	if completed.Tokens == nil {
		t.Fatal("expected single-use token handoff")
	}
	if completed.Tokens.AccessToken != "fake-access-token" {
		t.Fatalf("unexpected access token %q", completed.Tokens.AccessToken)
	}
	if completed.Tokens.Extra["workspace_name"] != "Toolmux Test Workspace" {
		t.Fatalf("missing workspace metadata: %#v", completed.Tokens.Extra)
	}

	again := getNotionSession(t, toolmuxd, session.SessionID)
	if again.Tokens != nil {
		t.Fatal("expected token handoff to be single-use")
	}

	refreshPayload := strings.NewReader(`{"refresh_token":"fake-refresh-token"}`)
	resp, err = toolmuxd.Client().Post(toolmuxd.URL+"/v1/oauth/notion/refresh", "application/json", refreshPayload)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected refresh status 200, got %d", resp.StatusCode)
	}
	var refreshed credentials.OAuthTokens
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken == "" {
		t.Fatalf("expected refreshed token bundle, got %#v", refreshed)
	}

	revokePayload := strings.NewReader(`{"token":"fake-access-token"}`)
	resp, err = toolmuxd.Client().Post(toolmuxd.URL+"/v1/oauth/notion/revoke", "application/json", revokePayload)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected revoke status 200, got %d", resp.StatusCode)
	}
}

func TestIntegrationNotionOAuthRejectsBadState(t *testing.T) {
	t.Parallel()
	config := toolmuxdtest.NotionConfig("https://notion.example.test", http.DefaultClient)
	config.SessionTTL = time.Minute
	toolmuxd := toolmuxdtest.New(t, config)

	resp, err := toolmuxd.Client().Get(toolmuxd.URL + "/oauth/notion/callback?state=bad&code=code")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid state to return 400, got %d", resp.StatusCode)
	}
}

func createNotionSession(t *testing.T, server *toolmuxdtest.Server) createSessionResponse {
	t.Helper()
	body := bytes.NewBufferString(`{"provider":"notion"}`)
	resp, err := server.Client().Post(server.URL+"/v1/oauth/sessions", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected session status 201, got %d", resp.StatusCode)
	}
	var session createSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	return session
}

func getNotionSession(t *testing.T, server *toolmuxdtest.Server, sessionID string) sessionResponse {
	t.Helper()
	resp, err := server.Client().Get(server.URL + "/v1/oauth/sessions/" + sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected session status 200, got %d", resp.StatusCode)
	}
	var session sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	return session
}

type createSessionResponse struct {
	SessionID   string    `json:"session_id"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	AuthURL     string    `json:"auth_url"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type sessionResponse struct {
	SessionID string                   `json:"session_id"`
	Provider  string                   `json:"provider"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
	Extra     map[string]string        `json:"extra,omitempty"`
}
