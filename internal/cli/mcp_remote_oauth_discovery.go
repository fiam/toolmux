package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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
