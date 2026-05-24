package slackapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c Client) AuthTest(ctx context.Context) (AuthTestResponse, error) {
	var out AuthTestResponse
	if err := c.postForm(ctx, "auth.test", nil, &out); err != nil {
		return AuthTestResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsList(ctx context.Context, values url.Values) (ConversationsListResponse, error) {
	var out ConversationsListResponse
	if err := c.get(ctx, "conversations.list", values, &out); err != nil {
		return ConversationsListResponse{}, err
	}
	return out, nil
}

func (c Client) UsersConversations(ctx context.Context, values url.Values) (ConversationsListResponse, error) {
	var out ConversationsListResponse
	if err := c.get(ctx, "users.conversations", values, &out); err != nil {
		return ConversationsListResponse{}, err
	}
	return out, nil
}

func (c Client) ClientUserBoot(ctx context.Context) (ClientUserBootResponse, error) {
	var out ClientUserBootResponse
	if err := c.get(ctx, "client.userBoot", nil, &out); err != nil {
		return ClientUserBootResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsInfo(ctx context.Context, values url.Values) (ConversationsInfoResponse, error) {
	var out ConversationsInfoResponse
	if err := c.get(ctx, "conversations.info", values, &out); err != nil {
		return ConversationsInfoResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsOpen(ctx context.Context, values url.Values) (ConversationsOpenResponse, error) {
	var out ConversationsOpenResponse
	if err := c.postForm(ctx, "conversations.open", values, &out); err != nil {
		return ConversationsOpenResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsHistory(ctx context.Context, values url.Values) (ConversationMessagesResponse, error) {
	var out ConversationMessagesResponse
	if err := c.get(ctx, "conversations.history", values, &out); err != nil {
		return ConversationMessagesResponse{}, err
	}
	return out, nil
}

func (c Client) ConversationsReplies(ctx context.Context, values url.Values) (ConversationMessagesResponse, error) {
	var out ConversationMessagesResponse
	if err := c.get(ctx, "conversations.replies", values, &out); err != nil {
		return ConversationMessagesResponse{}, err
	}
	return out, nil
}

func (c Client) ChatPostMessage(ctx context.Context, channel, text, threadTS string) (ChatPostMessageResponse, error) {
	values := url.Values{}
	values.Set("channel", channel)
	values.Set("text", text)
	if strings.TrimSpace(threadTS) != "" {
		values.Set("thread_ts", strings.TrimSpace(threadTS))
	}
	var out ChatPostMessageResponse
	if err := c.postForm(ctx, "chat.postMessage", values, &out); err != nil {
		return ChatPostMessageResponse{}, err
	}
	return out, nil
}

func (c Client) PostOK(ctx context.Context, method string, values url.Values) (OKResponse, error) {
	var out OKResponse
	if err := c.postForm(ctx, method, values, &out); err != nil {
		return OKResponse{}, err
	}
	return out, nil
}

func (c Client) FileInfo(ctx context.Context, fileID string) (FileInfoResponse, error) {
	values := url.Values{}
	values.Set("file", strings.TrimSpace(fileID))
	var out FileInfoResponse
	if err := c.get(ctx, "files.info", values, &out); err != nil {
		return FileInfoResponse{}, err
	}
	return out, nil
}

func (c Client) UsersList(ctx context.Context, values url.Values) (UsersListResponse, error) {
	var out UsersListResponse
	if err := c.get(ctx, "users.list", values, &out); err != nil {
		return UsersListResponse{}, err
	}
	return out, nil
}

func (c Client) UsergroupsList(ctx context.Context, values url.Values) (UsergroupsListResponse, error) {
	var out UsergroupsListResponse
	if err := c.get(ctx, "usergroups.list", values, &out); err != nil {
		return UsergroupsListResponse{}, err
	}
	return out, nil
}

func (c Client) UsergroupsUsersList(ctx context.Context, values url.Values) (UsergroupUsersListResponse, error) {
	var out UsergroupUsersListResponse
	if err := c.get(ctx, "usergroups.users.list", values, &out); err != nil {
		return UsergroupUsersListResponse{}, err
	}
	return out, nil
}

func (c Client) PostUsergroup(ctx context.Context, method string, values url.Values) (UsergroupResponse, error) {
	var out UsergroupResponse
	if err := c.postForm(ctx, method, values, &out); err != nil {
		return UsergroupResponse{}, err
	}
	return out, nil
}

func (c Client) SearchMessages(ctx context.Context, values url.Values) (SearchMessagesResponse, error) {
	var out SearchMessagesResponse
	if err := c.get(ctx, "search.messages", values, &out); err != nil {
		return SearchMessagesResponse{}, err
	}
	return out, nil
}

func (c Client) Download(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := httpClient(c.HTTPClient).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("slack API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("slack file download returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("slack file exceeds maximum download size of %d bytes", maxBytes)
	}
	return body, nil
}
