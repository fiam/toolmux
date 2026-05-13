package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
)

const (
	mcpRemoteAuthTypeBearer       = "bearer"
	mcpRemoteAuthTypeOAuth        = "oauth"
	mcpRemoteOAuthCallbackPath    = "/oauth/mcp/callback"
	mcpRemoteOAuthRefreshSkew     = time.Minute
	mcpRemoteOAuthMetadataBodyMax = 4 << 20
)

type mcpRemoteAuthLoginOptions struct {
	NoBrowser    bool
	Timeout      time.Duration
	ClientID     string
	ClientSecret string
	Scopes       []string
	AuthServer   string
	RedirectPort int
}

type mcpRemoteOAuthDiscovery struct {
	Resource        mcpRemoteProtectedResourceMetadata
	Authorization   mcpRemoteAuthorizationServerMetadata
	ResourceURI     string
	AuthorizationID string
}

type mcpRemoteProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
	ResourceName         string   `json:"resource_name,omitempty"`
}

type mcpRemoteAuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ClientIDMetadataDocumentSupported bool     `json:"client_id_metadata_document_supported,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

type mcpRemoteOAuthClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

type mcpRemoteOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

type mcpRemoteOAuthCallbackResult struct {
	Code string
	Err  error
}

func mcpRemoteAuthLoginCommand(opts *options) *cobra.Command {
	login := mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute}
	cmd := &cobra.Command{
		Use:     "login <name>",
		Aliases: []string{"connect"},
		Short:   "Authorize a remote MCP server with OAuth",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := cleanMCPRemoteName(args[0])
			if err != nil {
				return err
			}
			entry, ok, err := lookupMCPRemoteServer(name, "")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			if err := authorize(cmd, opts, mcpRemoteAuthLoginSpec(), args); err != nil {
				return err
			}
			tokens, err := loginMCPRemoteOAuth(cmd, opts, entry, login)
			if err != nil {
				return err
			}
			store, err := opts.credentials()
			if err != nil {
				return err
			}
			if err := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, entry.Name), tokens); err != nil {
				return err
			}
			if err := writeMCPRemoteAuthRequired(entry, true); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for MCP server %s\n", entry.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&login.NoBrowser, "no-browser", false, "print the authorization URL without opening a browser")
	cmd.Flags().DurationVar(&login.Timeout, "timeout", 2*time.Minute, "OAuth callback wait timeout")
	cmd.Flags().StringVar(&login.ClientID, "client-id", "", "OAuth client ID to use instead of dynamic client registration")
	cmd.Flags().StringVar(&login.ClientSecret, "client-secret", "", "OAuth client secret for authorization servers that require one")
	cmd.Flags().StringArrayVar(&login.Scopes, "scope", nil, "OAuth scope to request; repeatable and comma-separated values are accepted")
	cmd.Flags().StringVar(&login.AuthServer, "auth-server", "", "authorization server issuer to use when the resource advertises more than one")
	cmd.Flags().IntVar(&login.RedirectPort, "redirect-port", 0, "loopback redirect port; 0 chooses a free port")
	return cmd
}

func loginMCPRemoteOAuth(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, login mcpRemoteAuthLoginOptions) (credentials.OAuthTokens, error) {
	client := opts.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	ctx := commandContext(cmd)
	ui := newConnectUI(cmd, opts)
	discoveryProgress := ui.Start("Discovering MCP OAuth metadata")
	discovery, err := discoverMCPRemoteOAuth(ctx, client, entry.Server, login.AuthServer)
	if err != nil {
		discoveryProgress.Warn("MCP OAuth metadata discovery failed")
		return credentials.OAuthTokens{}, err
	}
	state, err := mcpRemoteRandomURLToken(32)
	if err != nil {
		discoveryProgress.Warn("MCP OAuth setup failed")
		return credentials.OAuthTokens{}, err
	}
	callback, err := startMCPRemoteOAuthCallback(login.RedirectPort, state, mcpRemoteOAuthCallbackPageFor(entry, discovery))
	if err != nil {
		discoveryProgress.Warn("MCP OAuth callback listener failed")
		return credentials.OAuthTokens{}, err
	}
	defer callback.shutdown()

	verifier, challenge, err := newMCPRemotePKCE()
	if err != nil {
		discoveryProgress.Warn("MCP OAuth PKCE setup failed")
		return credentials.OAuthTokens{}, err
	}
	oauthClient, err := resolveMCPRemoteOAuthClient(ctx, client, discovery.Authorization, callback.redirectURI, login)
	if err != nil {
		discoveryProgress.Warn("MCP OAuth client setup failed")
		return credentials.OAuthTokens{}, err
	}
	scopes := cleanMCPRemoteOAuthScopes(login.Scopes)
	if len(scopes) == 0 {
		scopes = defaultMCPRemoteOAuthScopes(discovery)
	}
	authURL, err := mcpRemoteOAuthAuthorizationURL(discovery.Authorization.AuthorizationEndpoint, oauthClient.ClientID, callback.redirectURI, state, challenge, discovery.ResourceURI, scopes)
	if err != nil {
		discoveryProgress.Warn("MCP OAuth authorization URL setup failed")
		return credentials.OAuthTokens{}, err
	}
	discoveryProgress.Done("Discovered MCP OAuth metadata")
	fmt.Fprintf(cmd.OutOrStdout(), "open this URL to authorize MCP server %s:\n%s\n", entry.Name, authURL)
	fmt.Fprintf(cmd.OutOrStdout(), "redirect URI:\n%s\n", callback.redirectURI)
	if ui.interactive && !login.NoBrowser {
		if err := openURL(authURL); err != nil {
			ui.warn("Could not open browser automatically: %v", err)
		} else {
			ui.status("Opened browser for MCP authorization")
		}
	}
	callbackProgress := ui.Start("Waiting for MCP OAuth callback")
	result, err := callback.wait(ctx, login.Timeout)
	if err != nil {
		callbackProgress.Warn("MCP OAuth callback wait failed")
		return credentials.OAuthTokens{}, err
	}
	if result.Err != nil {
		callbackProgress.Warn("MCP OAuth callback failed")
		return credentials.OAuthTokens{}, result.Err
	}
	callbackProgress.Done("Received MCP OAuth callback")
	exchangeProgress := ui.Start("Exchanging MCP OAuth code")
	tokens, err := exchangeMCPRemoteOAuthCode(ctx, client, discovery, oauthClient, callback.redirectURI, result.Code, verifier, scopes)
	if err != nil {
		exchangeProgress.Warn("MCP OAuth token exchange failed")
		return credentials.OAuthTokens{}, err
	}
	tokens.Extra = mcpRemoteOAuthTokenExtra(entry, discovery, oauthClient, scopes, tokens.Extra)
	exchangeProgress.Done("MCP OAuth authorization complete")
	return tokens, nil
}

type mcpRemoteOAuthCallback struct {
	redirectURI string
	server      *http.Server
	results     <-chan mcpRemoteOAuthCallbackResult
}

func startMCPRemoteOAuthCallback(port int, state string, page mcpRemoteOAuthCallbackPage) (mcpRemoteOAuthCallback, error) {
	if port < 0 || port > 65535 {
		return mcpRemoteOAuthCallback{}, fmt.Errorf("redirect port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return mcpRemoteOAuthCallback{}, err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return mcpRemoteOAuthCallback{}, fmt.Errorf("OAuth callback listener did not return a TCP address")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", tcpAddr.Port, mcpRemoteOAuthCallbackPath)
	results := make(chan mcpRemoteOAuthCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(mcpRemoteOAuthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		result := mcpRemoteOAuthCallbackResult{}
		switch {
		case query.Get("state") != state:
			result.Err = fmt.Errorf("MCP OAuth callback state mismatch")
		case query.Get("error") != "":
			result.Err = fmt.Errorf("MCP OAuth callback error: %s", query.Get("error"))
		case strings.TrimSpace(query.Get("code")) == "":
			result.Err = fmt.Errorf("MCP OAuth callback did not include a code")
		default:
			result.Code = query.Get("code")
		}
		writeMCPRemoteOAuthCallbackPage(w, page, result)
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
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case results <- mcpRemoteOAuthCallbackResult{Err: err}:
			default:
			}
		}
	}()
	return mcpRemoteOAuthCallback{
		redirectURI: redirectURI,
		server:      server,
		results:     results,
	}, nil
}

func (callback mcpRemoteOAuthCallback) wait(ctx context.Context, timeout time.Duration) (mcpRemoteOAuthCallbackResult, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case result := <-callback.results:
		return result, nil
	case <-ctx.Done():
		return mcpRemoteOAuthCallbackResult{}, fmt.Errorf("timed out waiting for MCP OAuth callback: %w", ctx.Err())
	}
}

func (callback mcpRemoteOAuthCallback) shutdown() {
	if callback.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = callback.server.Shutdown(ctx)
}

type mcpRemoteOAuthCallbackPage struct {
	ServerName  string
	DisplayName string
	LogoSlug    string
	LogoText    string
}

type mcpRemoteOAuthCallbackPageView struct {
	Page    mcpRemoteOAuthCallbackPage
	Success bool
	Error   string
}

func writeMCPRemoteOAuthCallbackPage(w http.ResponseWriter, page mcpRemoteOAuthCallbackPage, result mcpRemoteOAuthCallbackResult) {
	page = normalizeMCPRemoteOAuthCallbackPage(page)
	view := mcpRemoteOAuthCallbackPageView{
		Page:    page,
		Success: result.Err == nil,
	}
	status := http.StatusOK
	if result.Err != nil {
		status = http.StatusBadRequest
		view.Error = result.Err.Error()
	}
	var body bytes.Buffer
	if err := mcpRemoteOAuthCallbackTemplate.Execute(&body, view); err != nil {
		http.Error(w, "render MCP OAuth callback page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

func normalizeMCPRemoteOAuthCallbackPage(page mcpRemoteOAuthCallbackPage) mcpRemoteOAuthCallbackPage {
	page.ServerName = strings.TrimSpace(page.ServerName)
	if page.ServerName == "" {
		page.ServerName = "server"
	}
	page.DisplayName = strings.TrimSpace(page.DisplayName)
	if page.DisplayName == "" {
		page.DisplayName = humanMCPRemoteName(page.ServerName)
	}
	page.LogoSlug = strings.TrimSpace(page.LogoSlug)
	if page.LogoSlug == "" {
		page.LogoSlug = "generic"
	}
	page.LogoText = strings.TrimSpace(page.LogoText)
	if page.LogoText == "" {
		page.LogoText = mcpRemoteLogoText(page.DisplayName)
	}
	return page
}

func mcpRemoteOAuthCallbackPageFor(entry mcpRemoteServerEntry, discovery mcpRemoteOAuthDiscovery) mcpRemoteOAuthCallbackPage {
	logoSlug := mcpRemoteKnownLogoSlug(
		entry.Name,
		entry.Server.URL,
		discovery.Resource.ResourceName,
		discovery.ResourceURI,
		discovery.Authorization.Issuer,
	)
	displayName := firstNonEmpty(
		strings.TrimSpace(discovery.Resource.ResourceName),
		mcpRemoteKnownLogoName(logoSlug),
		humanMCPRemoteName(entry.Name),
		"MCP server",
	)
	return normalizeMCPRemoteOAuthCallbackPage(mcpRemoteOAuthCallbackPage{
		ServerName:  entry.Name,
		DisplayName: displayName,
		LogoSlug:    logoSlug,
		LogoText:    mcpRemoteLogoText(displayName),
	})
}

func mcpRemoteKnownLogoSlug(values ...string) string {
	target := strings.ToLower(strings.Join(values, " "))
	for _, known := range []string{"cloudflare", "grafana", "linear", "miro", "notion", "atlassian"} {
		if strings.Contains(target, known) {
			return known
		}
	}
	return "generic"
}

func mcpRemoteKnownLogoName(slug string) string {
	switch slug {
	case "atlassian":
		return "Atlassian"
	case "cloudflare":
		return "Cloudflare"
	case "grafana":
		return "Grafana"
	case "linear":
		return "Linear"
	case "miro":
		return "Miro"
	case "notion":
		return "Notion"
	default:
		return ""
	}
}

func humanMCPRemoteName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "MCP server"
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	if len(parts) == 0 {
		return name
	}
	for i, part := range parts {
		parts[i] = titleMCPRemoteNamePart(part)
	}
	return strings.Join(parts, " ")
}

func titleMCPRemoteNamePart(part string) string {
	runes := []rune(part)
	if len(runes) == 0 {
		return part
	}
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

func mcpRemoteLogoText(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "M"
}

var mcpRemoteOAuthCallbackTemplate = template.Must(template.New("mcp-remote-oauth-callback").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Page.DisplayName}} {{if .Success}}connected{{else}}authorization failed{{end}} - Toolmux</title>
  <style>
    :root {
      color-scheme: dark;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      background: #0a0d12;
      color: #e8edf7;
    }
    * {
      box-sizing: border-box;
    }
    body {
      min-height: 100vh;
      margin: 0;
      display: grid;
      place-items: center;
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.04), transparent 30%),
        #0a0d12;
    }
    main {
      width: min(760px, calc(100vw - 32px));
      border: 1px solid rgba(255, 255, 255, 0.16);
      border-radius: 8px;
      background: rgba(14, 18, 27, 0.96);
      box-shadow: 0 24px 80px rgba(0, 0, 0, 0.38);
      overflow: hidden;
    }
    header {
      display: flex;
      align-items: center;
      gap: 18px;
      padding: 28px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.12);
    }
    .logo {
      width: 56px;
      height: 56px;
      flex: 0 0 auto;
      display: grid;
      place-items: center;
      border-radius: 8px;
      background: #ffffff;
      color: #111111;
      border: 1px solid rgba(255, 255, 255, 0.2);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      font-size: 30px;
      font-weight: 800;
      line-height: 1;
    }
    .logo svg {
      width: 38px;
      height: 38px;
      display: block;
    }
    .logo-linear {
      background: #111111;
    }
    .logo-miro {
      background: #ffd02f;
    }
    .logo-atlassian {
      background: #0052cc;
    }
    .logo-grafana {
      background: #f46800;
      color: #ffffff;
    }
    .logo-generic {
      background: #1d9a8a;
      color: #ffffff;
    }
    .eyebrow {
      margin: 0 0 8px;
      color: #8ea0b8;
      font-size: 12px;
      letter-spacing: 0;
      text-transform: uppercase;
    }
    h1 {
      margin: 0;
      font-size: clamp(24px, 4vw, 36px);
      line-height: 1.12;
      letter-spacing: 0;
    }
    .terminal {
      margin: 28px;
      padding: 20px;
      border-radius: 8px;
      background: #05070a;
      border: 1px solid rgba(255, 255, 255, 0.12);
      color: #cbd6e6;
      font-size: 15px;
      line-height: 1.8;
      overflow-wrap: anywhere;
    }
    .prompt {
      color: #7dd3fc;
    }
    .ok {
      color: #86efac;
      font-weight: 700;
    }
    .err {
      color: #fca5a5;
      font-weight: 700;
    }
    .muted {
      color: #8ea0b8;
    }
    .error-detail {
      color: #fca5a5;
    }
    .hint {
      margin: 0;
      padding: 0 28px 28px;
      color: #a8b4c6;
      line-height: 1.55;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    @media (max-width: 520px) {
      header {
        align-items: flex-start;
        padding: 22px;
      }
      .terminal {
        margin: 22px;
        font-size: 13px;
      }
      .hint {
        padding: 0 22px 22px;
      }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div class="logo logo-{{.Page.LogoSlug}}" aria-label="{{.Page.DisplayName}} logo">
        {{if eq .Page.LogoSlug "cloudflare"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <path fill="#f6821f" d="M42.6 42.8H17.1c-4.7 0-8.6-3.8-8.6-8.6 0-4.3 3.2-7.9 7.4-8.5 1.9-7.4 8.6-12.6 16.5-12.6 8.7 0 15.9 6.5 16.9 14.9 3.5.9 6.1 4.1 6.1 7.9 0 3.8-2.6 7-6.2 7.8l-6.6-.9z"/>
          <path fill="#ffb74d" d="M37.8 30.8c1.4-1.2 3.3-1.9 5.3-1.9h6c3.6 0 6.5 2.9 6.5 6.5s-2.9 6.5-6.5 6.5H25.7l12.1-11.1z"/>
        </svg>
        {{else if eq .Page.LogoSlug "linear"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <circle cx="32" cy="32" r="30" fill="#ffffff"/>
          <path fill="#111111" d="M14 41.4 41.4 14c2.2 1.2 4.1 2.8 5.7 4.7L18.7 47.1A25.3 25.3 0 0 1 14 41.4zM12 31.4 31.4 12c2.1 0 4.2.3 6.1.9L12.9 37.5a25 25 0 0 1-.9-6.1zM23.1 51.4l28.3-28.3c.8 2 1.2 4.1 1.2 6.4L29.5 52.6c-2.3 0-4.4-.4-6.4-1.2z"/>
        </svg>
        {{else if eq .Page.LogoSlug "miro"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <path fill="#111111" d="M16 12h8l6 14 7-14h8l-6 40h-8l3.1-21.7L28 44h-6l-6-31.9z"/>
        </svg>
        {{else if eq .Page.LogoSlug "notion"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <rect x="10" y="8" width="44" height="48" rx="4" fill="#ffffff" stroke="#111111" stroke-width="4"/>
          <path fill="#111111" d="M20 46V18h7.5l10.8 17.1V18H44v28h-6.9L25.7 28.2V46H20z"/>
        </svg>
        {{else if eq .Page.LogoSlug "atlassian"}}
        <svg viewBox="0 0 64 64" aria-hidden="true" focusable="false">
          <path fill="#ffffff" d="M21.8 47.6c-1.1 0-1.9-1-1.5-2l7.2-18.5c.5-1.4 2.5-1.4 3 0l3.4 8.6-4.2 10.7c-.3.8-1 1.2-1.8 1.2h-6.1z"/>
          <path fill="#ffffff" d="M37.9 47.6c-.8 0-1.5-.5-1.8-1.2L28.6 27c-.5-1.4.5-2.8 1.9-2.8h6c.8 0 1.5.5 1.8 1.2l7.9 20.2c.4 1-.4 2-1.5 2h-6.8z"/>
          <path fill="#ffffff" d="M17.8 18.2c-.9 0-1.6-.7-1.6-1.6V12h31.6v4.6c0 .9-.7 1.6-1.6 1.6H17.8z"/>
        </svg>
        {{else}}
        <span>{{.Page.LogoText}}</span>
        {{end}}
      </div>
      <div>
        <p class="eyebrow">toolmux mcp auth</p>
        <h1>{{.Page.DisplayName}} {{if .Success}}is connected{{else}}authorization failed{{end}}</h1>
      </div>
    </header>
    <section class="terminal" aria-live="polite">
      <div><span class="prompt">$</span> toolmux mcp auth login {{.Page.ServerName}}</div>
      {{if .Success}}
      <div><span class="ok">OK</span> oauth callback received</div>
      <div><span class="ok">OK</span> MCP server link established</div>
      {{else}}
      <div><span class="err">ERR</span> authorization failed</div>
      {{if .Error}}<div class="error-detail">{{.Error}}</div>{{end}}
      {{end}}
      <div><span class="muted">...</span> return to your terminal</div>
    </section>
    {{if .Success}}
    <p class="hint">You can close this window. Toolmux will finish the connection in your terminal and store MCP server auth locally in your OS credential store.</p>
    {{else}}
    <p class="hint">You can close this window and return to your terminal for details. No MCP server token was stored from this callback.</p>
    {{end}}
  </main>
</body>
</html>`))

func discoverMCPRemoteOAuth(ctx context.Context, client *http.Client, server mcpRemoteServer, authServerOverride string) (mcpRemoteOAuthDiscovery, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateMCPRemoteURL(server.URL); err != nil {
		return mcpRemoteOAuthDiscovery{}, err
	}
	serverResource, err := canonicalMCPRemoteResourceURI(server.URL)
	if err != nil {
		return mcpRemoteOAuthDiscovery{}, err
	}
	candidates := mcpRemoteProtectedResourceMetadataCandidates(ctx, client, server, serverResource)
	var lastErr error
	for _, candidate := range candidates {
		resource, err := fetchMCPRemoteProtectedResourceMetadata(ctx, client, candidate.metadataURL, candidate.expectedResource)
		if err != nil {
			lastErr = err
			continue
		}
		authServer, err := selectMCPRemoteAuthorizationServer(resource.AuthorizationServers, authServerOverride)
		if err != nil {
			return mcpRemoteOAuthDiscovery{}, err
		}
		authorization, err := fetchMCPRemoteAuthorizationServerMetadata(ctx, client, authServer)
		if err != nil {
			return mcpRemoteOAuthDiscovery{}, err
		}
		return mcpRemoteOAuthDiscovery{
			Resource:        resource,
			Authorization:   authorization,
			ResourceURI:     firstNonEmpty(resource.Resource, candidate.expectedResource),
			AuthorizationID: authServer,
		}, nil
	}
	if lastErr != nil {
		return mcpRemoteOAuthDiscovery{}, fmt.Errorf("discover MCP OAuth metadata: %w", lastErr)
	}
	return mcpRemoteOAuthDiscovery{}, fmt.Errorf("discover MCP OAuth metadata: no protected resource metadata candidates")
}

type mcpRemoteProtectedResourceMetadataCandidate struct {
	metadataURL      string
	expectedResource string
}

func mcpRemoteProtectedResourceMetadataCandidates(ctx context.Context, client *http.Client, server mcpRemoteServer, serverResource string) []mcpRemoteProtectedResourceMetadataCandidate {
	seen := map[string]bool{}
	var candidates []mcpRemoteProtectedResourceMetadataCandidate
	add := func(metadataURL, expected string) {
		metadataURL = strings.TrimSpace(metadataURL)
		if metadataURL == "" || seen[metadataURL] {
			return
		}
		seen[metadataURL] = true
		candidates = append(candidates, mcpRemoteProtectedResourceMetadataCandidate{metadataURL: metadataURL, expectedResource: expected})
	}
	for _, metadataURL := range probeMCPRemoteResourceMetadataURLs(ctx, client, server) {
		add(metadataURL, serverResource)
	}
	if metadataURL, err := wellKnownOAuthMetadataURL(serverResource, "oauth-protected-resource"); err == nil {
		add(metadataURL, serverResource)
	}
	if originResource, err := originMCPRemoteResourceURI(serverResource); err == nil && originResource != serverResource {
		if metadataURL, err := wellKnownOAuthMetadataURL(originResource, "oauth-protected-resource"); err == nil {
			add(metadataURL, originResource)
		}
	}
	return candidates
}

func probeMCPRemoteResourceMetadataURLs(ctx context.Context, client *http.Client, server mcpRemoteServer) []string {
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  mustRawJSON(mcpRemoteInitializeParams()),
	})
	if err != nil {
		return nil
	}
	resp, err := postMCPRemote(ctx, client, server, "", "", body, nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusUnauthorized {
		return nil
	}
	return mcpRemoteWWWAuthenticateResourceMetadata(resp.Header.Values("WWW-Authenticate"))
}

func mustRawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}

func fetchMCPRemoteProtectedResourceMetadata(ctx context.Context, client *http.Client, metadataURL, expectedResource string) (mcpRemoteProtectedResourceMetadata, error) {
	var metadata mcpRemoteProtectedResourceMetadata
	if err := getMCPRemoteOAuthJSON(ctx, client, metadataURL, &metadata); err != nil {
		return metadata, err
	}
	if strings.TrimSpace(metadata.Resource) == "" {
		metadata.Resource = expectedResource
	}
	if expectedResource != "" {
		got, err := canonicalMCPRemoteResourceURI(metadata.Resource)
		if err != nil {
			return metadata, err
		}
		if got != expectedResource {
			return metadata, fmt.Errorf("protected resource metadata resource %q does not match %q", got, expectedResource)
		}
	}
	if len(metadata.AuthorizationServers) == 0 {
		return metadata, fmt.Errorf("protected resource metadata did not include authorization_servers")
	}
	return metadata, nil
}

func selectMCPRemoteAuthorizationServer(servers []string, override string) (string, error) {
	override = strings.TrimSpace(override)
	if override != "" {
		for _, server := range servers {
			if strings.TrimSpace(server) == override {
				return override, nil
			}
		}
		return "", fmt.Errorf("authorization server %q is not advertised by the MCP resource", override)
	}
	for _, server := range servers {
		if server = strings.TrimSpace(server); server != "" {
			return server, nil
		}
	}
	return "", fmt.Errorf("protected resource metadata did not include a usable authorization server")
}

func fetchMCPRemoteAuthorizationServerMetadata(ctx context.Context, client *http.Client, issuer string) (mcpRemoteAuthorizationServerMetadata, error) {
	metadataURLs, err := mcpRemoteAuthorizationServerMetadataURLs(issuer)
	if err != nil {
		return mcpRemoteAuthorizationServerMetadata{}, err
	}
	var lastErr error
	for _, metadataURL := range metadataURLs {
		var metadata mcpRemoteAuthorizationServerMetadata
		if err := getMCPRemoteOAuthJSON(ctx, client, metadataURL, &metadata); err != nil {
			lastErr = err
			continue
		}
		if metadata.Issuer == "" {
			metadata.Issuer = strings.TrimSpace(issuer)
		}
		if metadata.AuthorizationEndpoint == "" {
			return metadata, fmt.Errorf("authorization server metadata did not include authorization_endpoint")
		}
		if metadata.TokenEndpoint == "" {
			return metadata, fmt.Errorf("authorization server metadata did not include token_endpoint")
		}
		return metadata, nil
	}
	if lastErr != nil {
		return mcpRemoteAuthorizationServerMetadata{}, lastErr
	}
	return mcpRemoteAuthorizationServerMetadata{}, fmt.Errorf("authorization server metadata discovery had no candidates")
}

func mcpRemoteAuthorizationServerMetadataURLs(issuer string) ([]string, error) {
	var urls []string
	seen := map[string]bool{}
	add := func(raw string, err error) error {
		if err != nil {
			return err
		}
		if !seen[raw] {
			seen[raw] = true
			urls = append(urls, raw)
		}
		return nil
	}
	if err := add(wellKnownOAuthMetadataURL(issuer, "oauth-authorization-server")); err != nil {
		return nil, err
	}
	if err := add(wellKnownOAuthMetadataURL(issuer, "openid-configuration")); err != nil {
		return nil, err
	}
	if err := add(wellKnownOpenIDAppendedMetadataURL(issuer)); err != nil {
		return nil, err
	}
	return urls, nil
}

func getMCPRemoteOAuthJSON(ctx context.Context, client *http.Client, rawURL string, target any) error {
	// #nosec G107 -- remote MCP OAuth metadata URLs are discovered from explicit user-configured MCP servers.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Protocol-Version", mcpRemoteClientProtocolVersion)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("GET %s returned status %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, mcpRemoteOAuthMetadataBodyMax)).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return nil
}

func resolveMCPRemoteOAuthClient(ctx context.Context, client *http.Client, metadata mcpRemoteAuthorizationServerMetadata, redirectURI string, login mcpRemoteAuthLoginOptions) (mcpRemoteOAuthClient, error) {
	clientID := strings.TrimSpace(login.ClientID)
	if clientID != "" {
		return mcpRemoteOAuthClient{
			ClientID:     clientID,
			ClientSecret: strings.TrimSpace(login.ClientSecret),
		}, nil
	}
	if strings.TrimSpace(metadata.RegistrationEndpoint) == "" {
		return mcpRemoteOAuthClient{}, fmt.Errorf("authorization server does not advertise dynamic client registration; pass --client-id")
	}
	payload := map[string]any{
		"client_name":                "Toolmux",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	// #nosec G107 -- registration endpoint comes from authorization server metadata.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RegistrationEndpoint, bytes.NewReader(data))
	if err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return mcpRemoteOAuthClient{}, fmt.Errorf("dynamic client registration returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var registered mcpRemoteOAuthClient
	if err := json.NewDecoder(io.LimitReader(resp.Body, mcpRemoteOAuthMetadataBodyMax)).Decode(&registered); err != nil {
		return mcpRemoteOAuthClient{}, err
	}
	if strings.TrimSpace(registered.ClientID) == "" {
		return mcpRemoteOAuthClient{}, fmt.Errorf("dynamic client registration did not return client_id")
	}
	registered.ClientID = strings.TrimSpace(registered.ClientID)
	registered.ClientSecret = strings.TrimSpace(registered.ClientSecret)
	return registered, nil
}

func mcpRemoteOAuthAuthorizationURL(endpoint, clientID, redirectURI, state, challenge, resourceURI string, scopes []string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("resource", resourceURI)
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeMCPRemoteOAuthCode(ctx context.Context, client *http.Client, discovery mcpRemoteOAuthDiscovery, oauthClient mcpRemoteOAuthClient, redirectURI, code, verifier string, scopes []string) (credentials.OAuthTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", redirectURI)
	values.Set("client_id", oauthClient.ClientID)
	values.Set("code_verifier", verifier)
	values.Set("resource", discovery.ResourceURI)
	if oauthClient.ClientSecret != "" {
		values.Set("client_secret", oauthClient.ClientSecret)
	}
	response, err := postMCPRemoteOAuthToken(ctx, client, discovery.Authorization.TokenEndpoint, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	return response.credentials(scopes, time.Now().UTC()), nil
}

func refreshMCPRemoteOAuthToken(ctx context.Context, client *http.Client, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimSpace(tokens.Extra["token_endpoint"])
	clientID := strings.TrimSpace(tokens.Extra["client_id"])
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	if endpoint == "" || clientID == "" || refreshToken == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("stored MCP OAuth token is missing refresh metadata")
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", clientID)
	if resource := strings.TrimSpace(tokens.Extra["resource"]); resource != "" {
		values.Set("resource", resource)
	}
	if clientSecret := strings.TrimSpace(tokens.Extra["client_secret"]); clientSecret != "" {
		values.Set("client_secret", clientSecret)
	}
	response, err := postMCPRemoteOAuthToken(ctx, client, endpoint, values)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	refreshed := response.credentials(tokens.Scopes, time.Now().UTC())
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = tokens.RefreshToken
	}
	refreshed.Extra = cloneMCPRemoteStringMap(tokens.Extra)
	return refreshed, nil
}

func postMCPRemoteOAuthToken(ctx context.Context, client *http.Client, endpoint string, values url.Values) (mcpRemoteOAuthTokenResponse, error) {
	// #nosec G107 -- token endpoint comes from authorization server metadata stored for this MCP server.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return mcpRemoteOAuthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return mcpRemoteOAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return mcpRemoteOAuthTokenResponse{}, fmt.Errorf("OAuth token endpoint returned status %d", resp.StatusCode)
	}
	var token mcpRemoteOAuthTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, mcpRemoteOAuthMetadataBodyMax)).Decode(&token); err != nil {
		return mcpRemoteOAuthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return mcpRemoteOAuthTokenResponse{}, fmt.Errorf("OAuth token endpoint did not return access_token")
	}
	return token, nil
}

func (response mcpRemoteOAuthTokenResponse) credentials(requestedScopes []string, now time.Time) credentials.OAuthTokens {
	tokenType := strings.TrimSpace(response.TokenType)
	if tokenType == "" {
		tokenType = "bearer"
	}
	scopes := strings.Fields(response.Scope)
	if len(scopes) == 0 {
		scopes = append([]string(nil), requestedScopes...)
	}
	tokens := credentials.OAuthTokens{
		AccessToken:  strings.TrimSpace(response.AccessToken),
		RefreshToken: strings.TrimSpace(response.RefreshToken),
		TokenType:    tokenType,
		Scopes:       scopes,
	}
	if response.ExpiresIn > 0 {
		tokens.ExpiresAt = now.Add(time.Duration(response.ExpiresIn) * time.Second).UTC()
	}
	return tokens
}

func loadMCPRemoteAccessToken(ctx context.Context, opts *options, entry mcpRemoteServerEntry) (string, error) {
	store, err := opts.credentials()
	if err != nil {
		return "", err
	}
	ref := mcpRemoteCredentialRef(opts, entry.Name)
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	if mcpRemoteOAuthTokenNeedsRefresh(tokens, time.Now().UTC()) {
		refreshed, err := refreshMCPRemoteOAuthToken(ctx, opts.httpClient, tokens)
		if err != nil {
			return "", err
		}
		if err := store.SaveOAuthTokens(ctx, ref, refreshed); err != nil {
			return "", err
		}
		tokens = refreshed
	}
	return strings.TrimSpace(tokens.AccessToken), nil
}

func loadMCPRemoteStoredTokens(ctx context.Context, opts *options, name string) (credentials.OAuthTokens, bool, error) {
	store, err := opts.credentials()
	if err != nil {
		return credentials.OAuthTokens{}, false, err
	}
	tokens, err := store.LoadOAuthTokens(ctx, mcpRemoteCredentialRef(opts, name))
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.OAuthTokens{}, false, nil
		}
		return credentials.OAuthTokens{}, false, err
	}
	return tokens, true, nil
}

func mcpRemoteOAuthTokenNeedsRefresh(tokens credentials.OAuthTokens, now time.Time) bool {
	if !mcpRemoteStoredTokenIsOAuth(tokens) || strings.TrimSpace(tokens.RefreshToken) == "" || tokens.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(mcpRemoteOAuthRefreshSkew).Before(tokens.ExpiresAt)
}

func mcpRemoteStoredTokenIsOAuth(tokens credentials.OAuthTokens) bool {
	if tokens.Extra == nil {
		return false
	}
	return tokens.Extra["auth_type"] == mcpRemoteAuthTypeOAuth || strings.TrimSpace(tokens.Extra["token_endpoint"]) != ""
}

func mcpRemoteOAuthTokenExtra(entry mcpRemoteServerEntry, discovery mcpRemoteOAuthDiscovery, oauthClient mcpRemoteOAuthClient, scopes []string, existing map[string]string) map[string]string {
	extra := cloneMCPRemoteStringMap(existing)
	if extra == nil {
		extra = map[string]string{}
	}
	extra["auth_type"] = mcpRemoteAuthTypeOAuth
	extra["mcp_server"] = entry.Name
	extra["url"] = entry.Server.URL
	extra["resource"] = discovery.ResourceURI
	extra["authorization_server"] = discovery.AuthorizationID
	extra["issuer"] = discovery.Authorization.Issuer
	extra["authorization_endpoint"] = discovery.Authorization.AuthorizationEndpoint
	extra["token_endpoint"] = discovery.Authorization.TokenEndpoint
	extra["client_id"] = oauthClient.ClientID
	if oauthClient.ClientSecret != "" {
		extra["client_secret"] = oauthClient.ClientSecret
	}
	if len(scopes) > 0 {
		extra["scope"] = strings.Join(scopes, " ")
	}
	return extra
}

func cleanMCPRemoteOAuthScopes(values []string) []string {
	seen := map[string]bool{}
	var scopes []string
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			scopes = append(scopes, part)
		}
	}
	return scopes
}

func defaultMCPRemoteOAuthScopes(discovery mcpRemoteOAuthDiscovery) []string {
	if scopes := cleanMCPRemoteOAuthScopes(discovery.Resource.ScopesSupported); len(scopes) > 0 {
		return scopes
	}
	return cleanMCPRemoteOAuthScopes(discovery.Authorization.ScopesSupported)
}

func cloneMCPRemoteStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		if strings.TrimSpace(key) == "" {
			continue
		}
		clone[key] = item
	}
	return clone
}

func newMCPRemotePKCE() (string, string, error) {
	verifier, err := mcpRemoteRandomURLToken(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func mcpRemoteRandomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func mcpRemoteWWWAuthenticateResourceMetadata(values []string) []string {
	var urls []string
	for _, value := range values {
		remaining := value
		for {
			index := strings.Index(strings.ToLower(remaining), "resource_metadata")
			if index < 0 {
				break
			}
			remaining = remaining[index+len("resource_metadata"):]
			remaining = strings.TrimLeft(remaining, " \t")
			if !strings.HasPrefix(remaining, "=") {
				continue
			}
			remaining = strings.TrimLeft(remaining[1:], " \t")
			metadataURL, rest := readMCPRemoteWWWAuthenticateValue(remaining)
			if metadataURL != "" {
				urls = append(urls, metadataURL)
			}
			if rest == "" || rest == remaining {
				break
			}
			remaining = rest
		}
	}
	return urls
}

func readMCPRemoteWWWAuthenticateValue(value string) (string, string) {
	if strings.HasPrefix(value, `"`) {
		var out strings.Builder
		escaped := false
		for i, r := range value[1:] {
			switch {
			case escaped:
				out.WriteRune(r)
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				return out.String(), value[i+2:]
			default:
				out.WriteRune(r)
			}
		}
		return out.String(), ""
	}
	end := len(value)
	for i, r := range value {
		if r == ',' || r == ' ' || r == '\t' {
			end = i
			break
		}
	}
	return strings.TrimSpace(value[:end]), value[end:]
}

func canonicalMCPRemoteResourceURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("resource URI must include scheme and host")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String(), nil
}

func originMCPRemoteResourceURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return canonicalMCPRemoteResourceURI(parsed.String())
}

func wellKnownOAuthMetadataURL(resourceOrIssuer, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(resourceOrIssuer))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("metadata issuer/resource must include scheme and host")
	}
	pathPart := parsed.EscapedPath()
	if pathPart == "/" {
		pathPart = ""
	}
	if pathPart != "" && !strings.HasPrefix(pathPart, "/") {
		pathPart = "/" + pathPart
	}
	parsed.Path = "/.well-known/" + suffix + pathPart
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func wellKnownOpenIDAppendedMetadataURL(issuer string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("metadata issuer must include scheme and host")
	}
	pathPart := strings.TrimRight(parsed.EscapedPath(), "/")
	parsed.Path = pathPart + "/.well-known/openid-configuration"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
