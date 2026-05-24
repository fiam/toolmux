package cli

import (
	"fmt"
	"net/http"
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
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("MCP server %q is not registered", name)
			}
			if normalizeMCPRemoteServer(entry.Server).Transport == mcpRemoteTransportStdio {
				return fmt.Errorf("MCP server %q uses stdio; configure auth in the command environment or arguments", name)
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
