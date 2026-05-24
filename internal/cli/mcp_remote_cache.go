package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func syncMCPRemoteCacheExplicit(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, args []string, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, bool, error) {
	if err := authorize(cmd, opts, mcpRemoteSyncSpec(), args); err != nil {
		return mcpRemoteCache{}, false, err
	}
	token := ""
	if entry.Server.Transport != mcpRemoteTransportStdio {
		var err error
		token, err = loadMCPRemoteAccessToken(commandContext(cmd), opts, entry)
		if err != nil {
			return mcpRemoteCache{}, false, err
		}
	}
	cache, err := syncMCPRemoteServer(commandContext(cmd), opts.httpClient, entry, token, trace)
	if err != nil {
		return mcpRemoteCache{}, false, err
	}
	if err := writeMCPRemoteCache(opts.mcpCacheDir, entry.Name, cache); err != nil {
		return mcpRemoteCache{}, false, err
	}
	return cache, strings.TrimSpace(token) != "", nil
}

func syncMCPRemoteCacheAfterAdd(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, args []string, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, bool, error) {
	ui := newConnectUI(cmd, opts)
	syncProgress := ui.Start("Syncing MCP server metadata")
	cache, authRequired, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, args, trace)
	if err == nil {
		syncProgress.Done("Synced MCP server metadata")
		return cache, authRequired, nil
	}
	if !mcpRemoteErrorStatus(err, http.StatusUnauthorized) {
		syncProgress.Warn("MCP server metadata sync failed")
		return mcpRemoteCache{}, false, err
	}
	if _, ok, loadErr := loadMCPRemoteStoredTokens(commandContext(cmd), opts, entry.Name); loadErr != nil {
		syncProgress.Warn("MCP stored auth lookup failed")
		return mcpRemoteCache{}, false, loadErr
	} else if ok {
		syncProgress.Warn("MCP server metadata sync needs refreshed auth")
		return mcpRemoteCache{}, true, err
	}
	if authErr := authorize(cmd, opts, mcpRemoteAuthLoginSpec(), []string{entry.Name}); authErr != nil {
		syncProgress.Warn("MCP OAuth login denied")
		return mcpRemoteCache{}, true, fmt.Errorf("%s; OAuth login was denied: %w", err.Error(), authErr)
	}
	syncProgress.Done("MCP server requires OAuth")
	fmt.Fprintf(cmd.OutOrStdout(), "MCP server %s requires auth; starting OAuth login\n", entry.Name)
	tokens, loginErr := loginMCPRemoteOAuth(cmd, opts, entry, mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute})
	if loginErr != nil {
		return mcpRemoteCache{}, true, fmt.Errorf("%s; OAuth login failed: %w", err.Error(), loginErr)
	}
	store, storeErr := opts.credentials()
	if storeErr != nil {
		return mcpRemoteCache{}, true, storeErr
	}
	if saveErr := store.SaveOAuthTokens(commandContext(cmd), mcpRemoteCredentialRef(opts, entry.Name), tokens); saveErr != nil {
		return mcpRemoteCache{}, true, saveErr
	}
	fmt.Fprintf(cmd.OutOrStdout(), "stored OAuth token for MCP server %s\n", entry.Name)
	retryProgress := ui.Start("Syncing MCP server metadata with stored auth")
	cache, _, err = syncMCPRemoteCacheExplicit(cmd, opts, entry, args, trace)
	if err != nil {
		retryProgress.Warn("Authenticated MCP server metadata sync failed")
	} else {
		retryProgress.Done("Synced MCP server metadata")
	}
	return cache, true, err
}

func refreshMCPRemoteCacheIfStale(ctx context.Context, cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, bool) {
	cache, ok, err := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name)
	if err != nil {
		return mcpRemoteCache{}, false
	}
	if ok && mcpRemoteCacheFresh(cache, time.Now().UTC()) {
		return cache, true
	}
	if err := authorize(cmd, opts, mcpRemoteSyncSpec(), []string{entry.Name}); err != nil {
		return cache, ok
	}
	token := ""
	if entry.Server.Transport != mcpRemoteTransportStdio {
		var err error
		token, err = loadMCPRemoteAccessToken(ctx, opts, entry)
		if err != nil {
			return cache, ok
		}
	}
	refreshed, err := syncMCPRemoteServer(ctx, opts.httpClient, entry, token, trace)
	if err != nil {
		return cache, ok
	}
	if err := writeMCPRemoteCache(opts.mcpCacheDir, entry.Name, refreshed); err != nil {
		return cache, ok
	}
	return refreshed, true
}

func mcpRemoteCacheFresh(cache mcpRemoteCache, now time.Time) bool {
	if cache.Version != mcpRemoteCacheVersion || cache.SyncedAt.IsZero() {
		return false
	}
	return now.Sub(cache.SyncedAt) < mcpRemoteCacheMaxAge
}

func mcpRemoteToolFromCache(cache mcpRemoteCache, name string) (mcpRemoteTool, bool) {
	for _, tool := range cache.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcpRemoteTool{}, false
}

func syncMCPRemoteServer(ctx context.Context, client *http.Client, entry mcpRemoteServerEntry, bearerToken string, trace *mcpRemoteHTTPTrace) (mcpRemoteCache, error) {
	entry.Server = normalizeMCPRemoteServer(entry.Server)
	if err := validateMCPRemoteServer(entry.Server); err != nil {
		return mcpRemoteCache{}, err
	}
	if entry.Server.Transport == mcpRemoteTransportStdio {
		return syncMCPRemoteStdioServer(ctx, entry)
	}
	if client == nil {
		client = http.DefaultClient
	}
	initResult, sessionID, err := initializeMCPRemoteSession(ctx, client, entry.Server, bearerToken, trace)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	tools, toolsResult, err := listMCPRemoteTools(ctx, client, entry.Server, bearerToken, sessionID, trace)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	return mcpRemoteCacheFromSync(entry, initResult, toolsResult, tools), nil
}

func mcpRemoteCacheFromSync(entry mcpRemoteServerEntry, initResult, toolsResult json.RawMessage, tools []mcpRemoteTool) mcpRemoteCache {
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	_ = json.Unmarshal(initResult, &init)
	for i := range tools {
		tools[i].Name = strings.TrimSpace(tools[i].Name)
		if tools[i].InputSchema == nil {
			tools[i].InputSchema = map[string]any{"type": "object"}
		}
	}
	tools = slices.DeleteFunc(tools, func(tool mcpRemoteTool) bool {
		return tool.Name == ""
	})
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	fingerprint := mcpRemoteToolsFingerprint(tools)
	return mcpRemoteCache{
		Version:         mcpRemoteCacheVersion,
		Name:            entry.Name,
		URL:             entry.Server.URL,
		Transport:       entry.Server.Transport,
		ProtocolVersion: init.ProtocolVersion,
		ServerInfo:      init.ServerInfo,
		Tools:           tools,
		SyncedAt:        time.Now().UTC(),
		Fingerprint:     fingerprint,
		Raw: map[string]json.RawMessage{
			"initialize": initResult,
			"tools_list": toolsResult,
		},
	}
}
