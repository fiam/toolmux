package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
)

type DiagnosticResult []actions.Diagnostic

func ActionHandlers() map[string]actions.Handler {
	return map[string]actions.Handler{
		ActionID(ActionConversationsList): conversationsListAction,
		ActionID(ActionMessageSend):       messageSendAction,
		ActionID(ActionSearchMessages):    searchMessagesAction,
	}
}

func ActionID(name actions.LocalName) string {
	return string(ProviderName) + "." + string(name)
}

func conversationsListAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.ListConversationsAll(actionContext(exec), ListConversationsRequest{
		Types:           inv.String("types"),
		ExcludeArchived: !inv.Bool("include-archived"),
		TeamID:          inv.String("team"),
	}, inv.Int("limit"))
}

func messageSendAction(exec actions.Context, inv actions.Invocation) (any, error) {
	request := PostMessageRequest{
		Channel:  inv.String("channel"),
		Text:     strings.TrimSpace(firstNonEmpty(inv.String("text"), strings.Join(inv.Args, " "))),
		ThreadTS: inv.String("thread"),
		Mrkdwn:   inv.Bool("mrkdwn"),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.PostMessage(actionContext(exec), request)
}

func searchMessagesAction(exec actions.Context, inv actions.Invocation) (any, error) {
	query := strings.TrimSpace(firstNonEmpty(inv.String("query"), strings.Join(inv.Args, " ")))
	client, tokens, err := actionClientWithTokens(exec)
	if err != nil {
		return nil, err
	}
	return client.SearchMessages(actionContext(exec), SearchMessagesRequest{
		Query:     normalizeSearchQuery(query, tokens),
		Count:     inv.Int("limit"),
		Sort:      inv.String("sort"),
		SortDir:   inv.String("direction"),
		Highlight: inv.Bool("highlight"),
	})
}

func Diagnostics(ctx context.Context, exec actions.Context, status actions.ConnectionStatus) []actions.Diagnostic {
	diagnostics := []actions.Diagnostic{{
		Provider: string(ProviderName),
		Check:    "toolmuxd",
		Status:   "ok",
		Message:  exec.ToolmuxdURL,
	}}
	if !status.Connected {
		return diagnostics
	}
	exec.Context = ctx
	client, err := actionClient(exec)
	if err != nil {
		return append(diagnostics, actions.Diagnostic{
			Provider:    string(ProviderName),
			Check:       "api",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Run `toolmux connect slack` to refresh the local connection.",
		})
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.ListConversations(probeCtx, ListConversationsRequest{Types: "public_channel", Limit: 1}); err != nil {
		return append(diagnostics, actions.Diagnostic{
			Provider:    string(ProviderName),
			Check:       "api",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Check that the Slack connection still has conversation read access.",
		})
	}
	return append(diagnostics, actions.Diagnostic{
		Provider: string(ProviderName),
		Check:    "api",
		Status:   "ok",
		Message:  "conversations endpoint reachable",
	})
}

func actionClient(exec actions.Context) (*Client, error) {
	client, _, err := actionClientWithTokens(exec)
	return client, err
}

func actionClientWithTokens(exec actions.Context) (*Client, credentials.OAuthTokens, error) {
	if exec.Credentials == nil {
		return nil, credentials.OAuthTokens{}, fmt.Errorf("credential store is required")
	}
	ctx := actionContext(exec)
	ref := credentialRef(exec)
	tokens, err := exec.Credentials.LoadOAuthTokens(ctx, ref)
	if err != nil {
		return nil, credentials.OAuthTokens{}, err
	}
	tokens, err = refreshTokensIfNeeded(ctx, exec, ref, tokens)
	if err != nil {
		return nil, credentials.OAuthTokens{}, err
	}
	return NewClient(
		tokens.AccessToken,
		WithBaseURL(exec.ProviderURL),
		WithHTTPClient(exec.HTTPClient),
	), tokens, nil
}

func refreshTokensIfNeeded(ctx context.Context, exec actions.Context, ref credentials.ConnectionRef, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if tokens.RefreshToken == "" || tokens.ExpiresAt.IsZero() || time.Until(tokens.ExpiresAt) > time.Minute {
		return tokens, nil
	}
	serverURL := strings.TrimRight(exec.ToolmuxdURL, "/")
	if serverURL == "" {
		return tokens, fmt.Errorf("slack token expired and toolmuxd URL is not configured")
	}
	payload, err := json.Marshal(map[string]string{"refresh_token": tokens.RefreshToken})
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	httpClient := exec.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	// #nosec G107 -- toolmuxd URL is explicit local/deployment configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/oauth/"+string(ProviderName)+"/refresh", bytes.NewReader(payload))
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return credentials.OAuthTokens{}, fmt.Errorf("refresh Slack token: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var next credentials.OAuthTokens
	if err := json.NewDecoder(resp.Body).Decode(&next); err != nil {
		return credentials.OAuthTokens{}, err
	}
	if next.AccessToken == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("toolmuxd did not return refreshed Slack tokens")
	}
	if next.RefreshToken == "" {
		next.RefreshToken = tokens.RefreshToken
	}
	if len(next.Extra) == 0 {
		next.Extra = tokens.Extra
	}
	if err := exec.Credentials.SaveOAuthTokens(ctx, ref, next); err != nil {
		return credentials.OAuthTokens{}, err
	}
	return next, nil
}

func credentialRef(exec actions.Context) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   firstNonEmpty(exec.Profile, "default"),
		Provider:  string(ProviderName),
		AccountID: firstNonEmpty(exec.Account, "default"),
	}
}

func actionContext(exec actions.Context) context.Context {
	if exec.Context != nil {
		return exec.Context
	}
	return context.Background()
}

func normalizeSearchQuery(query string, tokens credentials.OAuthTokens) string {
	userID := strings.TrimSpace(tokens.Extra["user_id"])
	if userID == "" {
		return query
	}
	parts := strings.Fields(query)
	for i, part := range parts {
		switch strings.ToLower(part) {
		case "from:me":
			parts[i] = "from:<@" + userID + ">"
		}
	}
	if len(parts) == 0 {
		return query
	}
	return strings.Join(parts, " ")
}

func (results ListConversationsResponse) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(results.Channels))
	for _, channel := range results.Channels {
		status := "active"
		if channel.IsArchived {
			status = "archived"
		}
		rows = append(rows, []string{
			output.Value(channel.DisplayName()),
			channel.ID,
			channel.Type(),
			output.StatusBadge(human, status),
			strconv.Itoa(channel.NumMembers),
			output.Value(channel.Topic.Value),
		})
	}
	return output.Table{
		Headers: []string{"Name", "ID", "Type", "Status", "Members", "Topic"},
		Rows:    rows,
		Empty:   "no Slack conversations",
	}
}

func (message PostMessageResponse) Table(human output.Options) output.Table {
	return output.Table{
		Headers: []string{"Channel", "Timestamp", "Text", "Status"},
		Rows: [][]string{{
			message.Channel,
			message.TS,
			output.Value(firstNonEmpty(message.Message.Text, "sent")),
			output.StatusBadge(human, "sent"),
		}},
	}
}

func (results SearchMessagesResponse) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(results.Messages.Matches))
	for _, match := range results.Messages.Matches {
		channel := firstNonEmpty(match.Channel.Name, match.Channel.ID)
		rows = append(rows, []string{
			output.Value(channel),
			output.Value(firstNonEmpty(match.Username, match.User)),
			match.TS,
			output.Value(strings.TrimSpace(match.Text)),
			output.Value(match.Permalink),
		})
	}
	return output.Table{
		Headers: []string{"Channel", "User", "Timestamp", "Text", "Permalink"},
		Rows:    rows,
		Empty:   "no Slack message results",
	}
}

func (diagnostics DiagnosticResult) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		target := firstNonEmpty(diagnostic.Provider, "toolmux")
		rows = append(rows, []string{
			target,
			diagnostic.Check,
			output.StatusBadge(human, diagnostic.Status),
			output.Value(diagnostic.Message),
			output.Value(diagnostic.Remediation),
		})
	}
	return output.Table{
		Headers: []string{"Provider", "Check", "Status", "Message", "Remediation"},
		Rows:    rows,
		Empty:   "no diagnostics",
	}
}
