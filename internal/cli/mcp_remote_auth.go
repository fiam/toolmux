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
	ui.status("Discovering MCP OAuth metadata")
	discovery, err := discoverMCPRemoteOAuth(ctx, client, entry.Server, login.AuthServer)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	state, err := mcpRemoteRandomURLToken(32)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	callback, err := startMCPRemoteOAuthCallback(login.RedirectPort, state)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer callback.shutdown()

	verifier, challenge, err := newMCPRemotePKCE()
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	oauthClient, err := resolveMCPRemoteOAuthClient(ctx, client, discovery.Authorization, callback.redirectURI, login)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	scopes := cleanMCPRemoteOAuthScopes(login.Scopes)
	authURL, err := mcpRemoteOAuthAuthorizationURL(discovery.Authorization.AuthorizationEndpoint, oauthClient.ClientID, callback.redirectURI, state, challenge, discovery.ResourceURI, scopes)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	ui.done("Discovered MCP OAuth metadata")
	fmt.Fprintf(cmd.OutOrStdout(), "open this URL to authorize MCP server %s:\n%s\n", entry.Name, authURL)
	fmt.Fprintf(cmd.OutOrStdout(), "redirect URI:\n%s\n", callback.redirectURI)
	if ui.interactive && !login.NoBrowser {
		if err := openURL(authURL); err != nil {
			ui.warn("Could not open browser automatically: %v", err)
		} else {
			ui.status("Opened browser for MCP authorization")
		}
	}
	result, err := callback.wait(ctx, login.Timeout)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	if result.Err != nil {
		return credentials.OAuthTokens{}, result.Err
	}
	ui.status("Exchanging MCP OAuth code")
	tokens, err := exchangeMCPRemoteOAuthCode(ctx, client, discovery, oauthClient, callback.redirectURI, result.Code, verifier, scopes)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	tokens.Extra = mcpRemoteOAuthTokenExtra(entry, discovery, oauthClient, scopes, tokens.Extra)
	ui.done("MCP OAuth authorization complete")
	return tokens, nil
}

type mcpRemoteOAuthCallback struct {
	redirectURI string
	server      *http.Server
	results     <-chan mcpRemoteOAuthCallbackResult
}

func startMCPRemoteOAuthCallback(port int, state string) (mcpRemoteOAuthCallback, error) {
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if result.Err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<!doctype html><title>Toolmux MCP OAuth</title><p>Authorization failed. Return to Toolmux.</p>")
		} else {
			_, _ = io.WriteString(w, "<!doctype html><title>Toolmux MCP OAuth</title><p>Authorization complete. You can close this window.</p>")
		}
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
	metadataURL, err := wellKnownOAuthMetadataURL(issuer, "oauth-authorization-server")
	if err != nil {
		return mcpRemoteAuthorizationServerMetadata{}, err
	}
	var metadata mcpRemoteAuthorizationServerMetadata
	if err := getMCPRemoteOAuthJSON(ctx, client, metadataURL, &metadata); err != nil {
		return metadata, err
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

func getMCPRemoteOAuthJSON(ctx context.Context, client *http.Client, rawURL string, target any) error {
	// #nosec G107 -- remote MCP OAuth metadata URLs are discovered from explicit user-configured MCP servers.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Protocol-Version", mcpProtocolVersion)
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
