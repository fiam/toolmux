//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func newFakeMCPRemoteOAuthServer(t *testing.T, called *map[string]any) *httptest.Server {
	t.Helper()
	fixture := &fakeMCPRemoteOAuthServer{
		t:      t,
		called: called,
		codes:  map[string]fakeMCPRemoteOAuthCode{},
		accessTokens: map[string]bool{
			"oauth-access-1": true,
			"oauth-access-2": true,
		},
	}
	server := httptest.NewServer(fixture)
	fixture.serverURL = server.URL
	t.Cleanup(func() {
		if fixture.refreshCount == 0 {
			t.Error("expected OAuth refresh token flow to be exercised")
		}
	})
	return server
}

type fakeMCPRemoteOAuthCode struct {
	ClientID      string
	RedirectURI   string
	Resource      string
	CodeChallenge string
}

type fakeMCPRemoteOAuthServer struct {
	t            *testing.T
	serverURL    string
	called       *map[string]any
	codes        map[string]fakeMCPRemoteOAuthCode
	accessTokens map[string]bool
	refreshCount int
}

func (fixture *fakeMCPRemoteOAuthServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-protected-resource/mcp":
		fixture.handleProtectedResourceMetadata(w)
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server":
		fixture.handleAuthorizationServerMetadata(w)
	case r.Method == http.MethodPost && r.URL.Path == "/register":
		fixture.handleRegister(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/authorize":
		fixture.handleAuthorize(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/token":
		fixture.handleToken(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/mcp":
		fixture.handleMCP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (fixture *fakeMCPRemoteOAuthServer) mcpURL() string {
	return fixture.serverURL + "/mcp"
}

func (fixture *fakeMCPRemoteOAuthServer) handleProtectedResourceMetadata(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":              fixture.mcpURL(),
		"authorization_servers": []string{fixture.serverURL},
		"scopes_supported":      []string{"tools.read"},
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleAuthorizationServerMetadata(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                fixture.serverURL,
		"authorization_endpoint":                fixture.serverURL + "/authorize",
		"token_endpoint":                        fixture.serverURL + "/token",
		"registration_endpoint":                 fixture.serverURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fixture.t.Fatalf("decode registration: %v", err)
	}
	redirects, _ := req["redirect_uris"].([]any)
	if len(redirects) != 1 || redirects[0] == "" {
		fixture.t.Fatalf("unexpected redirect_uris in registration: %#v", req)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id": "toolmux-test-client",
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("client_id") != "toolmux-test-client" {
		fixture.t.Fatalf("unexpected client_id %q", query.Get("client_id"))
	}
	if query.Get("resource") != fixture.mcpURL() {
		fixture.t.Fatalf("unexpected resource %q", query.Get("resource"))
	}
	if scope := query.Get("scope"); scope != "" && scope != "tools.read" {
		fixture.t.Fatalf("unexpected scope %q", query.Get("scope"))
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		fixture.t.Fatalf("missing PKCE challenge: %s", r.URL.RawQuery)
	}
	code := "code-1"
	fixture.codes[code] = fakeMCPRemoteOAuthCode{
		ClientID:      query.Get("client_id"),
		RedirectURI:   query.Get("redirect_uri"),
		Resource:      query.Get("resource"),
		CodeChallenge: query.Get("code_challenge"),
	}
	redirect, err := url.Parse(query.Get("redirect_uri"))
	if err != nil {
		fixture.t.Fatalf("parse redirect URI: %v", err)
	}
	redirectQuery := redirect.Query()
	redirectQuery.Set("code", code)
	redirectQuery.Set("state", query.Get("state"))
	redirect.RawQuery = redirectQuery.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (fixture *fakeMCPRemoteOAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fixture.t.Fatalf("parse token form: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		fixture.handleAuthorizationCodeToken(w, r)
	case "refresh_token":
		fixture.handleRefreshToken(w, r)
	default:
		fixture.t.Fatalf("unexpected grant_type %q", r.Form.Get("grant_type"))
	}
}

func (fixture *fakeMCPRemoteOAuthServer) handleAuthorizationCodeToken(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	issued, ok := fixture.codes[code]
	if !ok {
		fixture.t.Fatalf("unexpected code %q", code)
	}
	if r.Form.Get("client_id") != issued.ClientID || r.Form.Get("redirect_uri") != issued.RedirectURI || r.Form.Get("resource") != issued.Resource {
		fixture.t.Fatalf("unexpected token request form: %#v", r.Form)
	}
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != issued.CodeChallenge {
		fixture.t.Fatalf("unexpected PKCE verifier challenge %q, want %q", got, issued.CodeChallenge)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  "oauth-access-1",
		"refresh_token": "oauth-refresh-1",
		"token_type":    "Bearer",
		"expires_in":    3600,
		"scope":         "tools.read",
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if r.Form.Get("client_id") != "toolmux-test-client" || r.Form.Get("refresh_token") != "oauth-refresh-1" || r.Form.Get("resource") != fixture.mcpURL() {
		fixture.t.Fatalf("unexpected refresh request form: %#v", r.Form)
	}
	fixture.refreshCount++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  "oauth-access-2",
		"refresh_token": "oauth-refresh-2",
		"token_type":    "Bearer",
		"expires_in":    3600,
		"scope":         "tools.read",
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !fixture.accessTokens[token] {
		w.Header().Add("WWW-Authenticate", `Bearer resource_metadata="`+fixture.serverURL+`/.well-known/oauth-protected-resource/mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fixture.t.Fatalf("decode request: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	switch req.Method {
	case "initialize":
		fixture.handleMCPInitialize(w, req)
	case "tools/list":
		fixture.handleMCPToolsList(w, req)
	case "tools/call":
		fixture.handleMCPToolsCall(w, req)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	default:
		_ = json.NewEncoder(w).Encode(mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcpError{Code: -32601, Message: "method not found"},
		})
	}
}

func (fixture *fakeMCPRemoteOAuthServer) handleMCPInitialize(w http.ResponseWriter, req mcpRequest) {
	_ = json.NewEncoder(w).Encode(mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"serverInfo": map[string]any{
				"name": "fake-oauth-linear",
			},
		},
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleMCPToolsList(w http.ResponseWriter, req mcpRequest) {
	_ = json.NewEncoder(w).Encode(mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": []map[string]any{fakeMCPCreateIssueTool()},
		},
	})
}

func (fixture *fakeMCPRemoteOAuthServer) handleMCPToolsCall(w http.ResponseWriter, req mcpRequest) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		fixture.t.Fatalf("decode call params: %v", err)
	}
	if fixture.called != nil {
		*fixture.called = params.Arguments
	}
	_ = json.NewEncoder(w).Encode(mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: mcpCallToolResult{
			Content: []mcpContent{{
				Type: "text",
				Text: "called create_issue: " + params.Arguments["title"].(string),
			}},
		},
	})
}
