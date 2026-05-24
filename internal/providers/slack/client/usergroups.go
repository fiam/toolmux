package slack

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
)

func handleUsergroupsList(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("include_users", strconv.FormatBool(inv.Bool("include_users")))
	values.Set("include_count", strconv.FormatBool(inv.Bool("include_count")))
	values.Set("include_disabled", strconv.FormatBool(inv.Bool("include_disabled")))
	response, err := client.UsergroupsList(exec.Context, values)
	if err != nil {
		return nil, err
	}
	return usergroupsListResult(response), nil
}

func handleUsergroupsMe(exec actions.Context, inv actions.Invocation) (any, error) {
	action := strings.ToLower(strings.TrimSpace(inv.String("action")))
	if action == "" {
		return nil, fmt.Errorf("action is required: list, join, or leave")
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	auth, err := client.AuthTest(exec.Context)
	if err != nil {
		return nil, err
	}
	userID := strings.TrimSpace(auth.UserID)
	if userID == "" {
		return nil, fmt.Errorf("slack auth.test did not return a user ID")
	}
	switch action {
	case "list":
		values := url.Values{}
		values.Set("include_users", "true")
		values.Set("include_count", "true")
		response, err := client.UsergroupsList(exec.Context, values)
		if err != nil {
			return nil, err
		}
		response.Usergroups = slicesWithUser(response.Usergroups, userID)
		return usergroupsListResult(response), nil
	case "join", "leave":
		usergroupID, err := requiredString(inv, "usergroup_id")
		if err != nil {
			return nil, err
		}
		if inv.Bool("dry-run") {
			return actions.NewDryRun("slack.usergroups_me", map[string]string{
				"action":       action,
				"usergroup_id": usergroupID,
				"user_id":      userID,
			}), nil
		}
		values := url.Values{}
		values.Set("usergroup", usergroupID)
		users, err := client.UsergroupsUsersList(exec.Context, values)
		if err != nil {
			return nil, err
		}
		nextUsers := updateUserMembership(users.Users, userID, action == "join")
		updateValues := url.Values{}
		updateValues.Set("usergroup", usergroupID)
		updateValues.Set("users", strings.Join(nextUsers, ","))
		response, err := client.PostUsergroup(exec.Context, "usergroups.users.update", updateValues)
		if err != nil {
			return nil, err
		}
		response.Usergroup.Users = nextUsers
		return usergroupResult(response), nil
	default:
		return nil, fmt.Errorf("unsupported usergroups_me action %q; expected list, join, or leave", action)
	}
}

func handleUsergroupsCreate(exec actions.Context, inv actions.Invocation) (any, error) {
	name, err := requiredString(inv, "name")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("name", name)
	setIfNotEmpty(values, "handle", inv.String("handle"))
	setIfNotEmpty(values, "description", inv.String("description"))
	setIfNotEmpty(values, "channels", inv.String("channels"))
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.usergroups_create", valuesToMap(values)), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.PostUsergroup(exec.Context, "usergroups.create", values)
	if err != nil {
		return nil, err
	}
	return usergroupResult(response), nil
}

func handleUsergroupsUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	usergroupID, err := requiredString(inv, "usergroup_id")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("usergroup", usergroupID)
	setIfNotEmpty(values, "name", inv.String("name"))
	setIfNotEmpty(values, "handle", inv.String("handle"))
	setIfNotEmpty(values, "description", inv.String("description"))
	setIfNotEmpty(values, "channels", inv.String("channels"))
	if len(values) == 1 {
		return nil, fmt.Errorf("at least one of --name, --handle, --description, or --channels is required")
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.usergroups_update", valuesToMap(values)), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.PostUsergroup(exec.Context, "usergroups.update", values)
	if err != nil {
		return nil, err
	}
	return usergroupResult(response), nil
}

func handleUsergroupsUsersUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	usergroupID, err := requiredString(inv, "usergroup_id")
	if err != nil {
		return nil, err
	}
	users, err := requiredString(inv, "users")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("usergroup", usergroupID)
	values.Set("users", strings.Join(cleanCSV(users), ","))
	if inv.Bool("dry-run") {
		return actions.NewDryRun("slack.usergroups_users_update", valuesToMap(values)), nil
	}
	client, err := slackClient(exec, inv)
	if err != nil {
		return nil, err
	}
	response, err := client.PostUsergroup(exec.Context, "usergroups.users.update", values)
	if err != nil {
		return nil, err
	}
	return usergroupResult(response), nil
}
