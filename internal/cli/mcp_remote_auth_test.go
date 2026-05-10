package cli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMCPRemoteOAuthCallbackPageSuccess(t *testing.T) {
	t.Parallel()

	callback, err := startMCPRemoteOAuthCallback(0, "state-1", mcpRemoteOAuthCallbackPage{
		ServerName:  "notion-work",
		DisplayName: "Notion",
		LogoSlug:    "notion",
		LogoText:    "N",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callback.shutdown)

	resp, err := http.Get(callback.redirectURI + "?state=state-1&code=secret-code") // #nosec G107 -- loopback test callback URL.
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected callback status 200, got %d: %s", resp.StatusCode, body)
	}
	result, err := callback.wait(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Err != nil || result.Code != "secret-code" {
		t.Fatalf("unexpected callback result: %#v", result)
	}
	text := string(body)
	for _, want := range []string{
		"Notion is connected",
		"toolmux mcp auth login notion-work",
		"OK</span> oauth callback received",
		"OK</span> MCP server link established",
		"return to your terminal",
		"Notion logo",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected callback page to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret-code") {
		t.Fatalf("callback page leaked OAuth code:\n%s", text)
	}
}

func TestMCPRemoteOAuthCallbackPageFailure(t *testing.T) {
	t.Parallel()

	callback, err := startMCPRemoteOAuthCallback(0, "state-1", mcpRemoteOAuthCallbackPage{
		ServerName:  "linear",
		DisplayName: "Linear",
		LogoSlug:    "linear",
		LogoText:    "L",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callback.shutdown)

	resp, err := http.Get(callback.redirectURI + "?state=wrong&code=secret-code") // #nosec G107 -- loopback test callback URL.
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected callback status 400, got %d: %s", resp.StatusCode, body)
	}
	result, err := callback.wait(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Err == nil || !strings.Contains(result.Err.Error(), "state mismatch") {
		t.Fatalf("expected state mismatch result, got %#v", result)
	}
	text := string(body)
	for _, want := range []string{
		"Linear authorization failed",
		"ERR</span> authorization failed",
		"MCP OAuth callback state mismatch",
		"No MCP server token was stored",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected callback page to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret-code") {
		t.Fatalf("callback page leaked OAuth code:\n%s", text)
	}
}

func TestMCPRemoteOAuthCallbackPageInfersKnownLogo(t *testing.T) {
	t.Parallel()

	page := mcpRemoteOAuthCallbackPageFor(mcpRemoteServerEntry{
		Name: "workspace",
		Server: mcpRemoteServer{
			URL: "https://mcp.slack.com/mcp",
		},
	}, mcpRemoteOAuthDiscovery{})
	if page.DisplayName != "Slack" || page.LogoSlug != "slack" || page.LogoText != "S" {
		t.Fatalf("expected Slack page metadata, got %#v", page)
	}
}
