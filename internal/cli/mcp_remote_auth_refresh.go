package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/policy"
)

const (
	mcpRemoteAuthRefreshStatusRefreshed = "refreshed"
	mcpRemoteAuthRefreshStatusValid     = "valid"
	mcpRemoteAuthRefreshStatusSkipped   = "skipped"
	mcpRemoteAuthRefreshStatusFailed    = "failed"
)

type mcpRemoteAuthRefreshOptions struct {
	Probe             bool
	IncludeNoStored   bool
	Trace             *mcpRemoteHTTPTrace
	ReauthWhenMissing func(mcpRemoteServerEntry) error
}

type mcpRemoteAuthRefreshResult struct {
	Toolbox  string `json:"toolbox" yaml:"toolbox"`
	AuthType string `json:"auth_type" yaml:"auth_type"`
	Status   string `json:"status" yaml:"status"`
	Message  string `json:"message" yaml:"message"`
}

type mcpRemoteAuthRefreshReport struct {
	Results []mcpRemoteAuthRefreshResult `json:"results" yaml:"results"`
}

func (report mcpRemoteAuthRefreshReport) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(report.Results))
	for _, result := range report.Results {
		rows = append(rows, []string{
			result.Toolbox,
			result.AuthType,
			output.StatusBadge(opts, result.Status),
			output.Value(result.Message),
		})
	}
	return output.Table{
		Headers: []string{"Toolbox", "Auth", "Status", "Message"},
		Rows:    rows,
		Empty:   "no stored remote MCP auth to refresh",
	}
}

func mcpRemoteAuthRefreshSpec() policy.CommandSpec {
	return policy.CommandSpec{
		ID:           "toolmux.auth_refresh",
		Segment:      "auth_refresh",
		Path:         []string{"auth", "refresh"},
		Use:          "auth_refresh",
		Short:        "Refresh stored remote MCP auth",
		Description:  "Probe stored remote MCP auth and refresh OAuth tokens when they are expired or rejected by the remote server.",
		Provider:     "toolmux",
		Resource:     "mcp_remote_auth",
		Action:       string(actions.VerbUpdate),
		Effect:       string(actions.EffectWrite),
		RemoteEffect: string(actions.EffectWrite),
		LocalEffect:  string(actions.EffectWrite),
		Risk:         []string{"mcp-auth", "credential-store", "remote-mcp"},
		Flags: []actions.Flag{
			{Name: "toolbox", Type: actions.FlagString, Usage: "Single registered remote MCP toolbox to refresh."},
			{Name: "toolboxes", Type: actions.FlagStringSlice, Usage: "Registered remote MCP toolboxes to refresh."},
			{Name: "probe", Type: actions.FlagBool, Usage: "Probe the remote MCP server after checking local token expiry.", DefaultBool: true},
		},
	}
}

func mcpRemoteAuthRefreshSpecs() []policy.CommandSpec {
	return []policy.CommandSpec{mcpRemoteAuthRefreshSpec()}
}

func refreshMCPRemoteAuth(ctx context.Context, opts *options, store credentials.Store, names []string, refreshOpts mcpRemoteAuthRefreshOptions) (mcpRemoteAuthRefreshReport, error) {
	entries, explicit, err := resolveMCPRemoteAuthRefreshEntries(ctx, opts, store, names)
	if err != nil {
		return mcpRemoteAuthRefreshReport{}, err
	}
	refreshOpts.IncludeNoStored = refreshOpts.IncludeNoStored || explicit
	results := make([]mcpRemoteAuthRefreshResult, 0, len(entries))
	for _, entry := range entries {
		result, include := refreshMCPRemoteAuthForEntry(ctx, opts, store, entry, refreshOpts)
		if include {
			results = append(results, result)
		}
	}
	return mcpRemoteAuthRefreshReport{Results: results}, nil
}

func resolveMCPRemoteAuthRefreshEntries(ctx context.Context, opts *options, store credentials.Store, names []string) ([]mcpRemoteServerEntry, bool, error) {
	if len(names) > 0 {
		cleaned, err := cleanMCPRemoteNames(names)
		if err != nil {
			return nil, false, err
		}
		entries := make([]mcpRemoteServerEntry, 0, len(cleaned))
		for _, name := range cleaned {
			entry, ok, err := lookupMCPRemoteServer(name, opts.workDir)
			if err != nil {
				return nil, true, err
			}
			if !ok {
				return nil, true, fmt.Errorf("MCP server %q is not registered", name)
			}
			entries = append(entries, entry)
		}
		return entries, true, nil
	}
	entries, err := effectiveMCPRemoteServerEntries(opts.workDir)
	if err != nil {
		return nil, false, err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if normalizeMCPRemoteServer(entry.Server).Transport == mcpRemoteTransportStdio {
			continue
		}
		if _, err := store.LoadOAuthTokens(ctx, mcpRemoteCredentialRef(opts, entry.Name)); err != nil {
			if errors.Is(err, credentials.ErrNotFound) {
				continue
			}
			return nil, false, err
		}
		filtered = append(filtered, entry)
	}
	return filtered, false, nil
}

func refreshMCPRemoteAuthForEntry(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry, refreshOpts mcpRemoteAuthRefreshOptions) (mcpRemoteAuthRefreshResult, bool) {
	entry.Server = normalizeMCPRemoteServer(entry.Server)
	if entry.Server.Transport == mcpRemoteTransportStdio {
		return mcpRemoteAuthRefreshResult{
			Toolbox:  entry.Name,
			AuthType: "stdio",
			Status:   mcpRemoteAuthRefreshStatusSkipped,
			Message:  "stdio auth is configured in the command environment or arguments",
		}, refreshOpts.IncludeNoStored
	}
	ref := mcpRemoteCredentialRef(opts, entry.Name)
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return mcpRemoteAuthRefreshResult{
				Toolbox:  entry.Name,
				AuthType: "none",
				Status:   mcpRemoteAuthRefreshStatusSkipped,
				Message:  "no stored auth",
			}, refreshOpts.IncludeNoStored
		}
		return mcpRemoteAuthRefreshResult{
			Toolbox:  entry.Name,
			AuthType: "unknown",
			Status:   mcpRemoteAuthRefreshStatusFailed,
			Message:  err.Error(),
		}, true
	}
	if mcpRemoteStoredTokenIsOAuth(tokens) {
		return refreshMCPRemoteOAuthAuthForEntry(ctx, opts, store, entry, ref, tokens, refreshOpts), true
	}
	return refreshMCPRemoteBearerAuthForEntry(ctx, opts, entry, tokens, refreshOpts), true
}

func refreshMCPRemoteOAuthAuthForEntry(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry, ref credentials.ConnectionRef, tokens credentials.OAuthTokens, refreshOpts mcpRemoteAuthRefreshOptions) mcpRemoteAuthRefreshResult {
	result := mcpRemoteAuthRefreshResult{Toolbox: entry.Name, AuthType: mcpRemoteAuthTypeOAuth}
	if mcpRemoteOAuthTokenDueForMaintenance(tokens, time.Now().UTC()) {
		if mcpRemoteOAuthTokenMissingRefreshMetadata(tokens) {
			return reauthMCPRemoteOAuthAuthForEntry(ctx, opts, store, entry, ref, refreshOpts, "stored OAuth token is missing refresh metadata")
		}
		refreshed, err := refreshAndSaveMCPRemoteOAuthToken(ctx, opts, store, ref, tokens)
		if err != nil {
			result.Status = mcpRemoteAuthRefreshStatusFailed
			result.Message = err.Error()
			return result
		}
		if !refreshOpts.Probe {
			result.Status = mcpRemoteAuthRefreshStatusRefreshed
			result.Message = "OAuth token refreshed"
			return result
		}
		if err := probeMCPRemoteStoredAuth(ctx, opts, entry, refreshed.AccessToken, refreshOpts.Trace); err != nil {
			result.Status = mcpRemoteAuthRefreshStatusFailed
			result.Message = "OAuth token refreshed but probe failed: " + err.Error()
			return result
		}
		result.Status = mcpRemoteAuthRefreshStatusRefreshed
		result.Message = "OAuth token refreshed and validated"
		return result
	}
	if !refreshOpts.Probe {
		result.Status = mcpRemoteAuthRefreshStatusValid
		result.Message = "OAuth token is not due for refresh"
		return result
	}
	if err := probeMCPRemoteStoredAuth(ctx, opts, entry, tokens.AccessToken, refreshOpts.Trace); err != nil {
		if !mcpRemoteErrorStatus(err, http.StatusUnauthorized) {
			result.Status = mcpRemoteAuthRefreshStatusFailed
			result.Message = err.Error()
			return result
		}
		if mcpRemoteOAuthTokenMissingRefreshMetadata(tokens) {
			return reauthMCPRemoteOAuthAuthForEntry(ctx, opts, store, entry, ref, refreshOpts, "remote rejected stored OAuth token and refresh metadata is missing")
		}
		refreshed, refreshErr := refreshAndSaveMCPRemoteOAuthToken(ctx, opts, store, ref, tokens)
		if refreshErr != nil {
			result.Status = mcpRemoteAuthRefreshStatusFailed
			result.Message = refreshErr.Error()
			return result
		}
		if probeErr := probeMCPRemoteStoredAuth(ctx, opts, entry, refreshed.AccessToken, refreshOpts.Trace); probeErr != nil {
			result.Status = mcpRemoteAuthRefreshStatusFailed
			result.Message = "OAuth token refreshed but probe failed: " + probeErr.Error()
			return result
		}
		result.Status = mcpRemoteAuthRefreshStatusRefreshed
		result.Message = "OAuth token refreshed after remote rejected the stored access token"
		return result
	}
	result.Status = mcpRemoteAuthRefreshStatusValid
	result.Message = "stored OAuth token is valid"
	return result
}

func mcpRemoteOAuthTokenDueForMaintenance(tokens credentials.OAuthTokens, now time.Time) bool {
	if !mcpRemoteStoredTokenIsOAuth(tokens) || tokens.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(mcpRemoteOAuthRefreshSkew).Before(tokens.ExpiresAt)
}

func reauthMCPRemoteOAuthAuthForEntry(ctx context.Context, opts *options, store credentials.Store, entry mcpRemoteServerEntry, ref credentials.ConnectionRef, refreshOpts mcpRemoteAuthRefreshOptions, reason string) mcpRemoteAuthRefreshResult {
	result := mcpRemoteAuthRefreshResult{Toolbox: entry.Name, AuthType: mcpRemoteAuthTypeOAuth}
	if refreshOpts.ReauthWhenMissing == nil {
		result.Status = mcpRemoteAuthRefreshStatusFailed
		result.Message = "OAuth refresh failed: " + reason
		return result
	}
	if err := refreshOpts.ReauthWhenMissing(entry); err != nil {
		result.Status = mcpRemoteAuthRefreshStatusFailed
		result.Message = "OAuth re-auth failed: " + err.Error()
		return result
	}
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil {
		result.Status = mcpRemoteAuthRefreshStatusFailed
		result.Message = "OAuth re-auth completed but stored auth could not be loaded: " + err.Error()
		return result
	}
	if refreshOpts.Probe {
		if err := probeMCPRemoteStoredAuth(ctx, opts, entry, tokens.AccessToken, refreshOpts.Trace); err != nil {
			result.Status = mcpRemoteAuthRefreshStatusFailed
			result.Message = "OAuth re-auth completed but probe failed: " + err.Error()
			return result
		}
	}
	result.Status = mcpRemoteAuthRefreshStatusRefreshed
	result.Message = "OAuth re-authorized because " + reason
	return result
}

func refreshMCPRemoteBearerAuthForEntry(ctx context.Context, opts *options, entry mcpRemoteServerEntry, tokens credentials.OAuthTokens, refreshOpts mcpRemoteAuthRefreshOptions) mcpRemoteAuthRefreshResult {
	result := mcpRemoteAuthRefreshResult{Toolbox: entry.Name, AuthType: mcpRemoteAuthTypeBearer}
	if !refreshOpts.Probe {
		result.Status = mcpRemoteAuthRefreshStatusSkipped
		result.Message = "bearer auth cannot be refreshed"
		return result
	}
	if err := probeMCPRemoteStoredAuth(ctx, opts, entry, tokens.AccessToken, refreshOpts.Trace); err != nil {
		result.Status = mcpRemoteAuthRefreshStatusFailed
		result.Message = err.Error()
		return result
	}
	result.Status = mcpRemoteAuthRefreshStatusValid
	result.Message = "stored bearer token is valid"
	return result
}

func refreshAndSaveMCPRemoteOAuthToken(ctx context.Context, opts *options, store credentials.Store, ref credentials.ConnectionRef, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if !mcpRemoteStoredTokenIsOAuth(tokens) {
		return credentials.OAuthTokens{}, fmt.Errorf("stored auth is not OAuth")
	}
	refreshed, err := refreshMCPRemoteOAuthToken(ctx, opts.httpClient, tokens)
	if err != nil {
		return credentials.OAuthTokens{}, fmt.Errorf("OAuth refresh failed: %w", err)
	}
	if err := store.SaveOAuthTokens(ctx, ref, refreshed); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return refreshed, nil
}

func refreshMCPRemoteAccessTokenAfterUnauthorized(ctx context.Context, opts *options, entry mcpRemoteServerEntry) (string, bool, error) {
	store, err := opts.credentials()
	if err != nil {
		return "", false, err
	}
	ref := mcpRemoteCredentialRef(opts, entry.Name)
	tokens, err := store.LoadOAuthTokens(ctx, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if !mcpRemoteStoredTokenIsOAuth(tokens) {
		return "", false, nil
	}
	refreshed, err := refreshAndSaveMCPRemoteOAuthToken(ctx, opts, store, ref, tokens)
	if err != nil {
		return "", true, err
	}
	return strings.TrimSpace(refreshed.AccessToken), true, nil
}

func loadMCPRemoteAccessTokenForCommand(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry) (string, error) {
	ctx := commandContext(cmd)
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
	if mcpRemoteOAuthTokenDueForMaintenance(tokens, time.Now().UTC()) && mcpRemoteOAuthTokenMissingRefreshMetadata(tokens) {
		if !interactiveCommand(cmd, opts) {
			return "", fmt.Errorf("stored OAuth token for toolbox %s is expired and missing refresh metadata; run `toolmux auth login %s`", entry.Name, entry.Name)
		}
		if err := runMCPRemoteAuthLogin(cmd, opts, entry, mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute}, "toolbox"); err != nil {
			return "", err
		}
		tokens, err = store.LoadOAuthTokens(ctx, ref)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(tokens.AccessToken), nil
	}
	if mcpRemoteOAuthTokenNeedsRefresh(tokens, time.Now().UTC()) {
		refreshed, err := refreshAndSaveMCPRemoteOAuthToken(ctx, opts, store, ref, tokens)
		if err != nil {
			return "", err
		}
		tokens = refreshed
	}
	return strings.TrimSpace(tokens.AccessToken), nil
}

func refreshMCPRemoteAccessTokenAfterUnauthorizedForCommand(cmd *cobra.Command, opts *options, entry mcpRemoteServerEntry) (string, bool, error) {
	token, refreshed, err := refreshMCPRemoteAccessTokenAfterUnauthorized(commandContext(cmd), opts, entry)
	if err == nil || !mcpRemoteOAuthRefreshMetadataError(err) {
		return token, refreshed, err
	}
	if !interactiveCommand(cmd, opts) {
		return "", refreshed, err
	}
	if loginErr := runMCPRemoteAuthLogin(cmd, opts, entry, mcpRemoteAuthLoginOptions{Timeout: 2 * time.Minute}, "toolbox"); loginErr != nil {
		return "", true, loginErr
	}
	tokens, ok, loadErr := loadMCPRemoteStoredTokens(commandContext(cmd), opts, entry.Name)
	if loadErr != nil {
		return "", true, loadErr
	}
	if !ok {
		return "", true, fmt.Errorf("OAuth re-auth completed but no stored auth was found for toolbox %s", entry.Name)
	}
	return strings.TrimSpace(tokens.AccessToken), true, nil
}

func mcpRemoteOAuthRefreshMetadataError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "stored MCP OAuth token is missing refresh metadata")
}

func probeMCPRemoteStoredAuth(ctx context.Context, opts *options, entry mcpRemoteServerEntry, accessToken string, trace *mcpRemoteHTTPTrace) error {
	_, _, err := initializeMCPRemoteSession(ctx, opts.httpClient, normalizeMCPRemoteServer(entry.Server), strings.TrimSpace(accessToken), trace)
	return err
}
