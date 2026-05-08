package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiam/supacli/internal/credentials"
	"github.com/fiam/supacli/internal/testutil/fakeupstream"
)

func TestIntegrationNotionOAuthLocalCustody(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	handler := NewHandlerWithConfig(Config{
		NotionClientID:   "client-id",
		NotionSecret:     "client-secret",
		NotionAuthURL:    upstream.URL + "/oauth/authorize",
		NotionTokenURL:   upstream.URL + "/oauth/token",
		NotionRevokeURL:  upstream.URL + "/oauth/revoke",
		NotionAPIVersion: "2026-03-11",
		HTTPClient:       upstream.Client(),
		SessionTTL:       time.Minute,
	})
	supaclid := httptest.NewServer(handler)
	defer supaclid.Close()

	session := createNotionSession(t, supaclid)
	if session.AuthURL == "" {
		t.Fatal("expected auth URL")
	}
	if session.RedirectURI != supaclid.URL+"/oauth/notion/callback" {
		t.Fatalf("unexpected redirect URI %q", session.RedirectURI)
	}

	resp, err := supaclid.Client().Get(session.AuthURL)
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

	completed := getNotionSession(t, supaclid, session.SessionID)
	if completed.Status != "complete" {
		t.Fatalf("expected complete session, got %#v", completed)
	}
	if completed.Tokens == nil {
		t.Fatal("expected single-use token handoff")
	}
	if completed.Tokens.AccessToken != "fake-access-token" {
		t.Fatalf("unexpected access token %q", completed.Tokens.AccessToken)
	}
	if completed.Tokens.Extra["workspace_name"] != "Supacli Test Workspace" {
		t.Fatalf("missing workspace metadata: %#v", completed.Tokens.Extra)
	}

	again := getNotionSession(t, supaclid, session.SessionID)
	if again.Tokens != nil {
		t.Fatal("expected token handoff to be single-use")
	}

	refreshPayload := strings.NewReader(`{"refresh_token":"fake-refresh-token"}`)
	resp, err = supaclid.Client().Post(supaclid.URL+"/v1/oauth/notion/refresh", "application/json", refreshPayload)
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
	resp, err = supaclid.Client().Post(supaclid.URL+"/v1/oauth/notion/revoke", "application/json", revokePayload)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected revoke status 200, got %d", resp.StatusCode)
	}
}

func TestIntegrationNotionOAuthRejectsBadState(t *testing.T) {
	handler := NewHandlerWithConfig(Config{
		NotionClientID:   "client-id",
		NotionSecret:     "client-secret",
		NotionAuthURL:    "https://notion.example.test/oauth/authorize",
		NotionTokenURL:   "https://notion.example.test/oauth/token",
		NotionAPIVersion: "2026-03-11",
		SessionTTL:       time.Minute,
	})
	supaclid := httptest.NewServer(handler)
	defer supaclid.Close()

	resp, err := supaclid.Client().Get(supaclid.URL + "/oauth/notion/callback?state=bad&code=code")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid state to return 400, got %d", resp.StatusCode)
	}
}

func createNotionSession(t *testing.T, server *httptest.Server) createSessionResponse {
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

func getNotionSession(t *testing.T, server *httptest.Server, sessionID string) sessionResponse {
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
