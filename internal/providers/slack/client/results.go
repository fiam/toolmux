package slack

import (
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers/slack/slackapi"
)

type authResult struct {
	Message string `json:"message" yaml:"message"`
}

func (result authResult) Text() string {
	return result.Message
}

type actionResult struct {
	Message string `json:"message" yaml:"message"`
}

func (result actionResult) Text() string {
	return result.Message
}

type authTestResult slackapi.AuthTestResponse

func (result authTestResult) Table(opts output.Options) output.Table {
	return output.Table{
		Headers: []string{"Team", "Team ID", "User", "User ID", "URL"},
		Rows: [][]string{{
			firstNonEmpty(result.Team, "-"),
			firstNonEmpty(result.TeamID, "-"),
			firstNonEmpty(result.User, "-"),
			firstNonEmpty(result.UserID, "-"),
			firstNonEmpty(result.URL, "-"),
		}},
	}
}

type conversationMessagesResult struct {
	ChannelID   string             `json:"channel_id" yaml:"channel_id"`
	ThreadTS    string             `json:"thread_ts,omitempty" yaml:"thread_ts,omitempty"`
	Messages    []slackapi.Message `json:"messages" yaml:"messages"`
	HasMore     bool               `json:"has_more" yaml:"has_more"`
	NextCursor  string             `json:"next_cursor,omitempty" yaml:"next_cursor,omitempty"`
	ResultLabel string             `json:"-" yaml:"-"`
}

func (result conversationMessagesResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Messages))
	for _, message := range result.Messages {
		rows = append(rows, []string{
			result.ChannelID,
			firstNonEmpty(message.User, message.Username, message.BotID, "-"),
			message.TS,
			firstNonEmpty(message.ThreadTS, "-"),
			trimForTable(message.Text, 96),
			result.NextCursor,
		})
	}
	empty := "no Slack messages"
	if result.ResultLabel != "" {
		empty = "no Slack " + result.ResultLabel
	}
	return output.Table{
		Headers: []string{"Channel", "User", "TS", "Thread", "Text", "Next Cursor"},
		Rows:    rows,
		Empty:   empty,
	}
}

type conversationListResult slackapi.ConversationsListResponse

func (result conversationListResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Channels))
	for _, channel := range result.Channels {
		rows = append(rows, []string{
			channel.ID,
			firstNonEmpty(channel.Name, "-"),
			conversationKind(channel),
			strconv.FormatBool(channel.IsArchived),
			strconv.Itoa(channel.NumMembers),
		})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "Kind", "Archived", "Members"},
		Rows:    rows,
		Empty:   "no Slack conversations",
	}
}

type searchMessagesResult slackapi.SearchMessagesResponse

func (result searchMessagesResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Messages.Matches))
	for _, match := range result.Messages.Matches {
		rows = append(rows, []string{
			firstNonEmpty(match.Channel.Name, match.Channel.ID, "-"),
			firstNonEmpty(match.User, match.Username, "-"),
			match.TS,
			trimForTable(match.Text, 96),
			match.Permalink,
		})
	}
	return output.Table{
		Headers: []string{"Conversation", "User", "TS", "Text", "Permalink"},
		Rows:    rows,
		Empty:   "no Slack messages",
	}
}

type sendMessageRequest struct {
	Channel     string `json:"channel" yaml:"channel"`
	Text        string `json:"text" yaml:"text"`
	ThreadTS    string `json:"thread_ts,omitempty" yaml:"thread_ts,omitempty"`
	ContentType string `json:"content_type,omitempty" yaml:"content_type,omitempty"`
}

type sendMessageResult slackapi.ChatPostMessageResponse

func (result sendMessageResult) Table(opts output.Options) output.Table {
	text := result.Message.Text
	if text == "" {
		text = "-"
	}
	return output.Table{
		Headers: []string{"Channel", "TS", "Text"},
		Rows: [][]string{{
			result.Channel,
			result.TS,
			trimForTable(text, 96),
		}},
	}
}

type openConversationRequest struct {
	Users           string `json:"users" yaml:"users"`
	PreventCreation bool   `json:"prevent_creation,omitempty" yaml:"prevent_creation,omitempty"`
	ReturnIM        bool   `json:"return_im,omitempty" yaml:"return_im,omitempty"`
}

type openConversationResult slackapi.ConversationsOpenResponse

func (result openConversationResult) Table(opts output.Options) output.Table {
	kind := conversationKind(result.Channel)
	return output.Table{
		Headers: []string{"ID", "Kind", "User"},
		Rows: [][]string{{
			result.Channel.ID,
			kind,
			result.Channel.User,
		}},
	}
}

type reactionRequest struct {
	Channel   string `json:"channel" yaml:"channel"`
	Timestamp string `json:"timestamp" yaml:"timestamp"`
	Emoji     string `json:"emoji" yaml:"emoji"`
}

type attachmentDataResult struct {
	FileID   string `json:"file_id" yaml:"file_id"`
	Filename string `json:"filename,omitempty" yaml:"filename,omitempty"`
	Mimetype string `json:"mimetype,omitempty" yaml:"mimetype,omitempty"`
	Size     int    `json:"size" yaml:"size"`
	Encoding string `json:"encoding,omitempty" yaml:"encoding,omitempty"`
	Content  string `json:"content,omitempty" yaml:"content,omitempty"`
}

func (result attachmentDataResult) Table(opts output.Options) output.Table {
	return output.Table{
		Headers: []string{"File", "Name", "Mimetype", "Size", "Encoding"},
		Rows: [][]string{{
			result.FileID,
			firstNonEmpty(result.Filename, "-"),
			firstNonEmpty(result.Mimetype, "-"),
			strconv.Itoa(result.Size),
			firstNonEmpty(result.Encoding, "-"),
		}},
	}
}

type unreadConversation struct {
	ChannelID    string             `json:"channel_id" yaml:"channel_id"`
	Name         string             `json:"name,omitempty" yaml:"name,omitempty"`
	Kind         string             `json:"kind,omitempty" yaml:"kind,omitempty"`
	UnreadCount  int                `json:"unread_count" yaml:"unread_count"`
	MentionCount int                `json:"mention_count" yaml:"mention_count"`
	Messages     []slackapi.Message `json:"messages,omitempty" yaml:"messages,omitempty"`
}

type unreadsResult struct {
	Conversations []unreadConversation `json:"conversations" yaml:"conversations"`
}

func (result unreadsResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Conversations))
	for _, conversation := range result.Conversations {
		text := ""
		if len(conversation.Messages) > 0 {
			text = conversation.Messages[0].Text
		}
		rows = append(rows, []string{
			conversation.ChannelID,
			firstNonEmpty(conversation.Name, "-"),
			conversation.Kind,
			strconv.Itoa(conversation.UnreadCount),
			strconv.Itoa(conversation.MentionCount),
			trimForTable(text, 72),
		})
	}
	return output.Table{
		Headers: []string{"Channel", "Name", "Kind", "Unread", "Mentions", "Latest"},
		Rows:    rows,
		Empty:   "no Slack unread conversations",
	}
}

type usergroupsListResult slackapi.UsergroupsListResponse

func (result usergroupsListResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Usergroups))
	for _, group := range result.Usergroups {
		rows = append(rows, []string{
			group.ID,
			group.Name,
			group.Handle,
			strconv.Itoa(group.UserCount),
			strconv.FormatBool(group.IsDisabled),
			strconv.FormatBool(group.IsExternal),
			strings.Join(group.Users, ","),
		})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "Handle", "Users", "Disabled", "External", "Members"},
		Rows:    rows,
		Empty:   "no Slack user groups",
	}
}

type usergroupResult slackapi.UsergroupResponse

func (result usergroupResult) Table(opts output.Options) output.Table {
	group := result.Usergroup
	return output.Table{
		Headers: []string{"ID", "Name", "Handle", "Users", "Description"},
		Rows: [][]string{{
			group.ID,
			group.Name,
			group.Handle,
			strconv.Itoa(group.UserCount),
			trimForTable(group.Description, 80),
		}},
	}
}

type usersSearchResult struct {
	Users []slackapi.User `json:"users" yaml:"users"`
}

func (result usersSearchResult) Table(opts output.Options) output.Table {
	rows := make([][]string, 0, len(result.Users))
	for _, user := range result.Users {
		rows = append(rows, []string{
			user.ID,
			user.Name,
			firstNonEmpty(user.RealName, user.Profile.RealName),
			user.Profile.DisplayName,
			user.Profile.Email,
			user.Profile.Title,
		})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "Real Name", "Display Name", "Email", "Title"},
		Rows:    rows,
		Empty:   "no Slack users",
	}
}

type brokerSession struct {
	SessionID string    `json:"session_id"`
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type brokerSessionStatus struct {
	SessionID string                   `json:"session_id"`
	Provider  string                   `json:"provider"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
	Extra     map[string]string        `json:"extra,omitempty"`
}
