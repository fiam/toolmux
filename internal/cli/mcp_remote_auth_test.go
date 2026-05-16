package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
			URL: "https://mcp.grafana.com/mcp",
		},
	}, mcpRemoteOAuthDiscovery{})
	if page.DisplayName != "Grafana" || page.LogoSlug != "grafana" || page.LogoText != "G" {
		t.Fatalf("expected Grafana page metadata, got %#v", page)
	}
}

func TestMCPRemoteOAuthCallbackPageInfersNewCatalogNames(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://api.githubcopilot.com/mcp/":      "GitHub",
		"https://mcp.incident.io/mcp":             "incident.io",
		"https://mcp.neon.tech/mcp":               "Neon",
		"https://mcp.pagerduty.com/mcp":           "PagerDuty",
		"https://mcp.eu.pagerduty.com/mcp":        "PagerDuty EU",
		"https://mcp.posthog.com/mcp":             "PostHog",
		"https://mcp.zoom.us/mcp/zoom/streamable": "Zoom",
		"https://mcp.zoominfo.com/mcp":            "ZoomInfo",
		"https://mcp.supabase.com/mcp":            "Supabase",
		"https://mcp.staircase.ai/mcp":            "Gainsight",
	}
	for rawURL, wantName := range tests {
		page := mcpRemoteOAuthCallbackPageFor(mcpRemoteServerEntry{
			Name: "workspace",
			Server: mcpRemoteServer{
				URL: rawURL,
			},
		}, mcpRemoteOAuthDiscovery{})
		if page.DisplayName != wantName {
			t.Fatalf("expected %s page metadata for %s, got %#v", wantName, rawURL, page)
		}
	}
}

func TestFetchMCPRemoteAuthorizationServerMetadataFallsBackToOIDC(t *testing.T) {
	t.Parallel()

	var requests []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/tenant/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 upstreamURL(r) + "/tenant",
			"authorization_endpoint": upstreamURL(r) + "/tenant/authorize",
			"token_endpoint":         upstreamURL(r) + "/tenant/token",
		})
	}))
	defer upstream.Close()

	metadata, err := fetchMCPRemoteAuthorizationServerMetadata(context.Background(), upstream.Client(), upstream.URL+"/tenant")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.AuthorizationEndpoint != upstream.URL+"/tenant/authorize" || metadata.TokenEndpoint != upstream.URL+"/tenant/token" {
		t.Fatalf("unexpected OIDC metadata: %#v", metadata)
	}
	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	want := []string{
		"/.well-known/oauth-authorization-server/tenant",
		"/.well-known/openid-configuration/tenant",
		"/tenant/.well-known/openid-configuration",
	}
	if strings.Join(gotRequests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected metadata discovery order:\ngot:\n%s\nwant:\n%s", strings.Join(gotRequests, "\n"), strings.Join(want, "\n"))
	}
}

func TestDefaultMCPRemoteOAuthScopesUsesResourceMetadata(t *testing.T) {
	t.Parallel()

	scopes := defaultMCPRemoteOAuthScopes(mcpRemoteOAuthDiscovery{
		Resource: mcpRemoteProtectedResourceMetadata{
			ScopesSupported: []string{"grafana:read grafana:write", "grafana:read"},
		},
		Authorization: mcpRemoteAuthorizationServerMetadata{
			ScopesSupported: []string{"fallback"},
		},
	})
	if strings.Join(scopes, " ") != "grafana:read grafana:write" {
		t.Fatalf("unexpected default OAuth scopes: %#v", scopes)
	}

	scopes = defaultMCPRemoteOAuthScopes(mcpRemoteOAuthDiscovery{
		Authorization: mcpRemoteAuthorizationServerMetadata{
			ScopesSupported: []string{"fallback"},
		},
	})
	if strings.Join(scopes, " ") != "fallback" {
		t.Fatalf("unexpected authorization fallback scopes: %#v", scopes)
	}
}

func upstreamURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
