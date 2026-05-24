package slack

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

func conversationMessageValues(inv actions.Invocation) url.Values {
	values := url.Values{}
	if cursor := strings.TrimSpace(inv.String("cursor")); cursor != "" {
		values.Set("cursor", cursor)
	}
	setIfNotEmpty(values, "oldest", inv.String("oldest"))
	setIfNotEmpty(values, "latest", inv.String("latest"))
	if inv.Bool("inclusive") {
		values.Set("inclusive", "true")
	}
	values.Set("limit", strconv.Itoa(parseSlackLimit(inv.String("limit"), 100)))
	return values
}

func parseSlackLimit(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return boundedInt(limit, fallback, 1, 1000)
}

func boundedInt(value, fallback, minValue, maxValue int) int {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if maxValue > 0 && value > maxValue {
		return maxValue
	}
	return value
}

func requiredString(inv actions.Invocation, name string) (string, error) {
	value := strings.TrimSpace(inv.String(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func filterActivityMessages(messages []slackapi.Message, include bool) []slackapi.Message {
	if include {
		return messages
	}
	out := make([]slackapi.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Subtype {
		case "channel_join", "channel_leave", "channel_topic", "channel_purpose", "channel_name":
			continue
		default:
			out = append(out, message)
		}
	}
	return out
}

func reactionRequestFromInvocation(inv actions.Invocation) (reactionRequest, error) {
	channelID, err := requiredString(inv, "channel_id")
	if err != nil {
		return reactionRequest{}, err
	}
	timestamp, err := requiredString(inv, "timestamp")
	if err != nil {
		return reactionRequest{}, err
	}
	emoji, err := requiredString(inv, "emoji")
	if err != nil {
		return reactionRequest{}, err
	}
	return reactionRequest{
		Channel:   channelID,
		Timestamp: timestamp,
		Emoji:     strings.Trim(emoji, ":"),
	}, nil
}

func isTextMimetype(mimetype string) bool {
	mimetype = strings.ToLower(strings.TrimSpace(mimetype))
	if strings.HasPrefix(mimetype, "text/") {
		return true
	}
	switch mimetype {
	case "application/json", "application/xml", "application/javascript", "application/x-yaml", "application/yaml", "application/x-sh":
		return true
	default:
		return false
	}
}

func slackSearchQuery(inv actions.Invocation) string {
	parts := []string{}
	if query := strings.TrimSpace(inv.String("search_query")); query != "" {
		parts = append(parts, query)
	}
	addSearchFilter := func(prefix, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, prefix+":"+value)
		}
	}
	addSearchFilter("in", inv.String("filter_in_channel"))
	addSearchFilter("in", inv.String("filter_in_im_or_mpim"))
	addSearchFilter("with", inv.String("filter_users_with"))
	addSearchFilter("from", inv.String("filter_users_from"))
	addSearchFilter("before", inv.String("filter_date_before"))
	addSearchFilter("after", inv.String("filter_date_after"))
	addSearchFilter("on", inv.String("filter_date_on"))
	addSearchFilter("during", inv.String("filter_date_during"))
	if inv.Bool("filter_threads_only") {
		parts = append(parts, "is:thread")
	}
	return strings.Join(parts, " ")
}

func unreadChannelTypes(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dm":
		return "im"
	case "group_dm", "group-dm", "mpim":
		return "mpim"
	case "partner", "internal":
		return "public_channel,private_channel"
	case "", "all":
		return "public_channel,private_channel,mpim,im"
	default:
		return value
	}
}

func skipUnreadChannel(channel slackapi.Conversation, inv actions.Invocation) bool {
	if channel.IsMuted && !inv.Bool("include_muted") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(inv.String("channel_types"))) {
	case "partner":
		if !channel.IsExtShared {
			return true
		}
	case "internal":
		if channel.IsExtShared {
			return true
		}
	}
	return inv.Bool("mentions_only") && channel.UnreadCountDisplay == 0
}

func setIfNotEmpty(values url.Values, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values.Set(key, value)
	}
}

func valuesToMap(values url.Values) map[string]string {
	out := make(map[string]string, len(values))
	for key, vals := range values {
		if len(vals) > 0 {
			out[key] = vals[0]
		}
	}
	return out
}

func cleanCSV(value string) []string {
	seen := map[string]bool{}
	var out []string
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func slicesWithUser(groups []slackapi.Usergroup, userID string) []slackapi.Usergroup {
	out := make([]slackapi.Usergroup, 0, len(groups))
	for _, group := range groups {
		if slices.Contains(group.Users, userID) {
			out = append(out, group)
		}
	}
	return out
}

func updateUserMembership(users []string, userID string, member bool) []string {
	seen := map[string]bool{}
	for _, user := range users {
		user = strings.TrimSpace(user)
		if user != "" {
			seen[user] = true
		}
	}
	if member {
		seen[userID] = true
	} else {
		delete(seen, userID)
	}
	out := make([]string, 0, len(seen))
	for user := range seen {
		out = append(out, user)
	}
	sort.Strings(out)
	return out
}

func userMatches(user slackapi.User, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		user.ID,
		user.Name,
		user.RealName,
		user.Profile.RealName,
		user.Profile.DisplayName,
		user.Profile.Email,
		user.Profile.Title,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func conversationKind(channel slackapi.Conversation) string {
	switch {
	case channel.IsIM:
		return "im"
	case channel.IsMPIM:
		return "mpim"
	case channel.IsGroup || channel.IsPrivate:
		return "private"
	case channel.IsChannel:
		return "public"
	default:
		return "conversation"
	}
}

func trimForTable(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit-1]) + "..."
}

func mergeExtra(target map[string]string, values map[string]string) map[string]string {
	if target == nil {
		target = map[string]string{}
	}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		target[key] = value
	}
	return target
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
