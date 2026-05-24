package slack

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

func handleConversationsHistory(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	values := conversationMessageValues(inv)
	values.Set("channel", channelID)
	response, err := client.ConversationsHistory(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return conversationMessagesResult{
		ChannelID:   channelID,
		Messages:    filterActivityMessages(response.Messages, inv.Bool("include_activity_messages")),
		HasMore:     response.HasMore,
		NextCursor:  response.ResponseMetadata.NextCursor,
		ResultLabel: "messages",
	}, nil
}

func handleConversationsReplies(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	threadTS, err := requiredString(inv, "thread_ts")
	if err != nil {
		return nil, err
	}
	values := conversationMessageValues(inv)
	values.Set("channel", channelID)
	values.Set("ts", threadTS)
	response, err := client.ConversationsReplies(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return conversationMessagesResult{
		ChannelID:   channelID,
		ThreadTS:    threadTS,
		Messages:    filterActivityMessages(response.Messages, inv.Bool("include_activity_messages")),
		HasMore:     response.HasMore,
		NextCursor:  response.ResponseMetadata.NextCursor,
		ResultLabel: "replies",
	}, nil
}

func handleConversationsAddMessage(exec actions.Context, inv actions.Invocation) (any, error) {
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	text := firstNonEmpty(inv.String("text"), inv.String("payload"))
	if text == "" {
		return nil, fmt.Errorf("text is required; pass --text or --payload")
	}
	request := sendMessageRequest{
		Channel:     channelID,
		Text:        text,
		ThreadTS:    strings.TrimSpace(inv.String("thread_ts")),
		ContentType: strings.TrimSpace(inv.String("content_type")),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.conversations_add_message", request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.ChatPostMessage(exec.Context, request.Channel, request.Text, request.ThreadTS)
	if err != nil {
		return nil, err
	}
	return sendMessageResult(response), nil
}

func handleConversationsOpen(exec actions.Context, inv actions.Invocation) (any, error) {
	users := firstNonEmpty(inv.String("users"), inv.String("user_id"))
	if users == "" {
		return nil, fmt.Errorf("users is required; pass --user_id or --users")
	}
	request := openConversationRequest{
		Users:           users,
		PreventCreation: inv.Bool("prevent_creation"),
		ReturnIM:        inv.Bool("return_im"),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.conversations_open", request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("users", request.Users)
	if request.PreventCreation {
		values.Set("prevent_creation", "true")
	}
	if request.ReturnIM {
		values.Set("return_im", "true")
	}
	response, err := client.ConversationsOpen(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return openConversationResult(response), nil
}

func handleReactionsAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleReaction(exec, inv, "reactions.add", "slack.reactions_add")
}

func handleReactionsRemove(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleReaction(exec, inv, "reactions.remove", "slack.reactions_remove")
}

func handleReaction(exec actions.Context, inv actions.Invocation, method, action string) (any, error) {
	request, err := reactionRequestFromInvocation(inv)
	if err != nil {
		return nil, err
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(action, request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("channel", request.Channel)
	values.Set("timestamp", request.Timestamp)
	values.Set("name", strings.Trim(request.Emoji, ":"))
	if _, err := client.PostOK(exec.Context, method, values); err != nil {
		return nil, err
	}
	return actionResult{Message: action + " completed"}, nil
}

func handleAttachmentGetData(exec actions.Context, inv actions.Invocation) (any, error) {
	fileID, err := requiredString(inv, "file_id")
	if err != nil {
		return nil, err
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	info, err := client.FileInfo(exec.Context, fileID)
	if err != nil {
		return nil, err
	}
	result := attachmentDataResult{
		FileID:   info.File.ID,
		Filename: firstNonEmpty(info.File.Name, info.File.Title),
		Mimetype: info.File.Mimetype,
		Size:     info.File.Size,
	}
	downloadURL := firstNonEmpty(info.File.URLPrivateDownload, info.File.URLPrivate)
	if downloadURL == "" {
		return result, nil
	}
	content, err := client.Download(exec.Context, downloadURL, 5<<20)
	if err != nil {
		return nil, err
	}
	result.Size = len(content)
	if isTextMimetype(result.Mimetype) {
		result.Encoding = "none"
		result.Content = string(content)
	} else {
		result.Encoding = "base64"
		result.Content = base64.StdEncoding.EncodeToString(content)
	}
	return result, nil
}

func handleConversationsSearchMessages(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	query := slackSearchQuery(inv)
	if query == "" {
		return nil, fmt.Errorf("search_query or at least one search filter is required")
	}
	values := url.Values{}
	values.Set("query", query)
	if limit := inv.Int("limit"); limit > 0 {
		values.Set("count", strconv.Itoa(limit))
	}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("page", cursor)
	}
	response, err := client.SearchMessages(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return searchMessagesResult(response), nil
}

func handleConversationsUnreads(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("types", unreadChannelTypes(inv.String("channel_types")))
	values.Set("exclude_archived", "true")
	values.Set("limit", strconv.Itoa(boundedInt(inv.Int("max_channels"), 50, 1, 1000)))
	response, err := client.ConversationsList(exec.Context, values)
	if err != nil {
		return nil, err
	}
	result := unreadsResult{}
	maxMessages := boundedInt(inv.Int("max_messages_per_channel"), 10, 1, 100)
	for _, channel := range response.Channels {
		if skipUnreadChannel(channel, inv) {
			continue
		}
		item := unreadConversation{
			ChannelID:    channel.ID,
			Name:         firstNonEmpty(channel.Name, channel.User),
			Kind:         conversationKind(channel),
			UnreadCount:  channel.UnreadCount,
			MentionCount: channel.UnreadCountDisplay,
		}
		if inv.Bool("include_messages") {
			historyValues := url.Values{}
			historyValues.Set("channel", channel.ID)
			historyValues.Set("limit", strconv.Itoa(maxMessages))
			history, err := client.ConversationsHistory(exec.Context, historyValues)
			if err != nil {
				return nil, err
			}
			item.Messages = history.Messages
		}
		result.Conversations = append(result.Conversations, item)
	}
	return result, nil
}

func handleConversationsMark(exec actions.Context, inv actions.Invocation) (any, error) {
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return nil, err
	}
	ts := strings.TrimSpace(inv.String("ts"))
	if ts == "" {
		ts = fmt.Sprintf("%d.000000", time.Now().Unix())
	}
	request := map[string]string{"channel": channelID, "ts": ts}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.conversations_mark", request), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("channel", channelID)
	values.Set("ts", ts)
	if _, err := client.PostOK(exec.Context, "conversations.mark", values); err != nil {
		return nil, err
	}
	return actionResult{Message: "marked Slack conversation read"}, nil
}

func handleChannelsList(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("types", strings.TrimSpace(inv.String("channel_types")))
	if limit := inv.Int("limit"); limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("cursor", cursor)
	}
	response, err := client.ConversationsList(exec.Context, values)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(inv.String("sort")), "popularity") {
		sort.SliceStable(response.Channels, func(i, j int) bool {
			return response.Channels[i].NumMembers > response.Channels[j].NumMembers
		})
	}
	return conversationListResult(response), nil
}

func handleUsersConversations(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("types", strings.TrimSpace(inv.String("channel_types")))
	values.Set("exclude_archived", strconv.FormatBool(inv.Bool("exclude_archived")))
	if limit := inv.Int("limit"); limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("cursor", cursor)
	}
	if userID := strings.TrimSpace(inv.String("user_id")); userID != "" {
		values.Set("user", userID)
	}
	if teamID := strings.TrimSpace(inv.String("team_id")); teamID != "" {
		values.Set("team_id", teamID)
	}
	response, err := client.UsersConversations(exec.Context, values)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(inv.String("sort")), "popularity") {
		sort.SliceStable(response.Channels, func(i, j int) bool {
			return response.Channels[i].NumMembers > response.Channels[j].NumMembers
		})
	}
	return conversationListResult(response), nil
}

func handleExperimentalConversationsList(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.ClientUserBoot(exec.Context)
	if err != nil {
		return nil, err
	}
	channels := append([]slackapi.Conversation(nil), response.Channels...)
	if inv.Bool("include_ims") {
		channels = append(channels, response.IMs...)
	}
	if inv.Bool("include_mpims") {
		channels = append(channels, response.MPIMs...)
	}
	channels = filterExperimentalConversations(channels, inv.String("query"), inv.Bool("exclude_archived"))
	if limit := inv.Int("limit"); limit > 0 && len(channels) > limit {
		channels = channels[:limit]
	}
	return conversationListResult(slackapi.ConversationsListResponse{
		OK:       response.OK,
		Error:    response.Error,
		Channels: channels,
	}), nil
}

func filterExperimentalConversations(channels []slackapi.Conversation, query string, excludeArchived bool) []slackapi.Conversation {
	query = strings.ToLower(strings.TrimSpace(query))
	filtered := make([]slackapi.Conversation, 0, len(channels))
	for _, channel := range channels {
		if excludeArchived && channel.IsArchived {
			continue
		}
		if query != "" && !experimentalConversationMatches(channel, query) {
			continue
		}
		filtered = append(filtered, channel)
	}
	return filtered
}

func experimentalConversationMatches(channel slackapi.Conversation, query string) bool {
	for _, value := range []string{channel.ID, channel.Name, channel.User} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
