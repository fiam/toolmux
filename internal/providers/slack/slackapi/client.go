package slackapi

import (
	"net/http"
)

const (
	DefaultAPIBaseURL = "https://slack.com/api"
	DefaultAuthURL    = "https://slack.com/oauth/v2/authorize"
	// #nosec G101 -- this is Slack's OAuth token endpoint URL, not a token value.
	DefaultTokenURL  = "https://slack.com/api/oauth.v2.access"
	DefaultRevokeURL = "https://slack.com/api/auth.revoke"
)

type Client struct {
	BaseURL     string
	HTTPClient  *http.Client
	AccessToken string
	Cookie      string
}

type AuthTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	URL    string `json:"url,omitempty"`
	Team   string `json:"team,omitempty"`
	User   string `json:"user,omitempty"`
	TeamID string `json:"team_id,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

type ConversationsListResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error,omitempty"`
	Channels         []Conversation `json:"channels"`
	ResponseMetadata Metadata       `json:"response_metadata,omitzero"`
}

type ClientUserBootResponse struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Channels []Conversation `json:"channels"`
	IMs      []Conversation `json:"ims"`
	MPIMs    []Conversation `json:"mpims"`
}

type ConversationsInfoResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Channel Conversation `json:"channel,omitzero"`
}

type ConversationsOpenResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Channel Conversation `json:"channel,omitzero"`
}

type ConversationMessagesResponse struct {
	OK               bool      `json:"ok"`
	Error            string    `json:"error,omitempty"`
	Messages         []Message `json:"messages"`
	HasMore          bool      `json:"has_more,omitempty"`
	ResponseMetadata Metadata  `json:"response_metadata,omitzero"`
}

type Conversation struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	User               string   `json:"user,omitempty"`
	IsChannel          bool     `json:"is_channel,omitempty"`
	IsGroup            bool     `json:"is_group,omitempty"`
	IsIM               bool     `json:"is_im,omitempty"`
	IsMPIM             bool     `json:"is_mpim,omitempty"`
	IsPrivate          bool     `json:"is_private,omitempty"`
	IsArchived         bool     `json:"is_archived,omitempty"`
	IsExtShared        bool     `json:"is_ext_shared,omitempty"`
	IsMuted            bool     `json:"is_muted,omitempty"`
	IsMember           bool     `json:"is_member,omitempty"`
	NumMembers         int      `json:"num_members,omitempty"`
	UnreadCount        int      `json:"unread_count,omitempty"`
	UnreadCountDisplay int      `json:"unread_count_display,omitempty"`
	Members            []string `json:"members,omitempty"`
}

type Metadata struct {
	NextCursor string `json:"next_cursor,omitempty"`
}

type ChatPostMessageResponse struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Channel string  `json:"channel,omitempty"`
	TS      string  `json:"ts,omitempty"`
	Message Message `json:"message,omitzero"`
}

type Message struct {
	Type        string     `json:"type,omitempty"`
	Subtype     string     `json:"subtype,omitempty"`
	User        string     `json:"user,omitempty"`
	Username    string     `json:"username,omitempty"`
	BotID       string     `json:"bot_id,omitempty"`
	Text        string     `json:"text,omitempty"`
	TS          string     `json:"ts,omitempty"`
	ThreadTS    string     `json:"thread_ts,omitempty"`
	ReplyCount  int        `json:"reply_count,omitempty"`
	LatestReply string     `json:"latest_reply,omitempty"`
	Files       []File     `json:"files,omitempty"`
	Reactions   []Reaction `json:"reactions,omitempty"`
}

type Reaction struct {
	Name  string   `json:"name,omitempty"`
	Count int      `json:"count,omitempty"`
	Users []string `json:"users,omitempty"`
}

type FileInfoResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	File  File   `json:"file,omitzero"`
}

type File struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	Title              string `json:"title,omitempty"`
	Mimetype           string `json:"mimetype,omitempty"`
	Filetype           string `json:"filetype,omitempty"`
	PrettyType         string `json:"pretty_type,omitempty"`
	URLPrivate         string `json:"url_private,omitempty"`
	URLPrivateDownload string `json:"url_private_download,omitempty"`
	Size               int    `json:"size,omitempty"`
}

type UsersListResponse struct {
	OK               bool     `json:"ok"`
	Error            string   `json:"error,omitempty"`
	Members          []User   `json:"members"`
	ResponseMetadata Metadata `json:"response_metadata,omitzero"`
}

type User struct {
	ID       string      `json:"id"`
	Name     string      `json:"name,omitempty"`
	RealName string      `json:"real_name,omitempty"`
	Deleted  bool        `json:"deleted,omitempty"`
	IsBot    bool        `json:"is_bot,omitempty"`
	Profile  UserProfile `json:"profile,omitzero"`
}

type UserProfile struct {
	DisplayName string `json:"display_name,omitempty"`
	RealName    string `json:"real_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Title       string `json:"title,omitempty"`
}

type UsergroupsListResponse struct {
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Usergroups []Usergroup `json:"usergroups"`
}

type UsergroupResponse struct {
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	Usergroup Usergroup `json:"usergroup,omitzero"`
}

type UsergroupUsersListResponse struct {
	OK    bool     `json:"ok"`
	Error string   `json:"error,omitempty"`
	Users []string `json:"users"`
}

type Usergroup struct {
	ID          string   `json:"id"`
	TeamID      string   `json:"team_id,omitempty"`
	Name        string   `json:"name,omitempty"`
	Handle      string   `json:"handle,omitempty"`
	Description string   `json:"description,omitempty"`
	UserCount   int      `json:"user_count,omitempty"`
	IsDisabled  bool     `json:"is_disabled,omitempty"`
	IsExternal  bool     `json:"is_external,omitempty"`
	Prefs       Prefs    `json:"prefs,omitzero"`
	Users       []string `json:"users,omitempty"`
}

type Prefs struct {
	Channels []string `json:"channels,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

type OKResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type SearchMessagesResponse struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Query    string        `json:"query,omitempty"`
	Messages SearchResults `json:"messages"`
}

type SearchResults struct {
	Total      int           `json:"total,omitempty"`
	Pagination Pagination    `json:"pagination,omitzero"`
	Paging     Paging        `json:"paging,omitzero"`
	Matches    []SearchMatch `json:"matches"`
}

type Pagination struct {
	TotalCount int `json:"total_count,omitempty"`
	Page       int `json:"page,omitempty"`
	PageCount  int `json:"page_count,omitempty"`
	PerPage    int `json:"per_page,omitempty"`
}

type Paging struct {
	Total int `json:"total,omitempty"`
	Page  int `json:"page,omitempty"`
	Pages int `json:"pages,omitempty"`
	Count int `json:"count,omitempty"`
}

type SearchMatch struct {
	Channel   SearchChannel `json:"channel"`
	User      string        `json:"user,omitempty"`
	Username  string        `json:"username,omitempty"`
	Text      string        `json:"text,omitempty"`
	TS        string        `json:"ts,omitempty"`
	Permalink string        `json:"permalink,omitempty"`
	Type      string        `json:"type,omitempty"`
}

type SearchChannel struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type OAuthOptions struct {
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string
	UserScopes   []string
	TokenSource  string
}

type OAuthTokenResponse struct {
	OK           bool              `json:"ok"`
	Error        string            `json:"error,omitempty"`
	AccessToken  string            `json:"access_token,omitempty"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	TokenType    string            `json:"token_type,omitempty"`
	Scope        string            `json:"scope,omitempty"`
	ExpiresIn    int               `json:"expires_in,omitempty"`
	BotUserID    string            `json:"bot_user_id,omitempty"`
	AppID        string            `json:"app_id,omitempty"`
	Team         OAuthEntity       `json:"team,omitzero"`
	Enterprise   OAuthEntity       `json:"enterprise,omitzero"`
	AuthedUser   OAuthAuthedUser   `json:"authed_user,omitzero"`
	Extra        map[string]string `json:"-"`
}

type OAuthEntity struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type OAuthAuthedUser struct {
	ID           string `json:"id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

type RevokeResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Revoked bool   `json:"revoked,omitempty"`
}
