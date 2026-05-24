package slack

import (
	"net/url"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

func handleUsersSearch(exec actions.Context, inv actions.Invocation) (any, error) {
	query, err := requiredString(inv, "query")
	if err != nil {
		return nil, err
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	limit := boundedInt(inv.Int("limit"), 10, 1, 100)
	var users []slackapi.User
	cursor := ""
	for page := 0; page < 10 && len(users) < limit; page++ {
		values := url.Values{}
		values.Set("limit", "200")
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		response, err := client.UsersList(exec.Context, values)
		if err != nil {
			return nil, err
		}
		for _, user := range response.Members {
			if !user.Deleted && userMatches(user, query) {
				users = append(users, user)
			}
			if len(users) >= limit {
				break
			}
		}
		cursor = response.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return usersSearchResult{Users: users}, nil
}
