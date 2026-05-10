package slacktest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type Server struct {
	*httptest.Server
	mu       sync.Mutex
	messages []slackMessage
	requests []string
}

const (
	TeamID    = "T123456"
	TeamName  = "Toolmux Test Workspace"
	UserID    = "U123456"
	ChannelID = "C123456"
	GroupID   = "G123456"
)

func NewUpstream() *Server {
	mux := http.NewServeMux()
	server := &Server{
		messages: []slackMessage{{
			ChannelID:   ChannelID,
			ChannelName: "toolmux",
			User:        UserID,
			Username:    "alberto",
			Text:        "deploy is done",
			TS:          "1715100000.000100",
			Permalink:   "https://toolmux.slack.com/archives/C123456/p1715100000000100",
		}},
	}
	mux.HandleFunc("GET /oauth/v2/authorize", server.authorize)
	mux.HandleFunc("POST /api/oauth.v2.access", server.token)
	mux.HandleFunc("GET /api/auth.revoke", server.revoke)
	mux.HandleFunc("GET /api/conversations.list", server.conversationsList)
	mux.HandleFunc("POST /api/chat.postMessage", server.chatPostMessage)
	mux.HandleFunc("GET /api/search.messages", server.searchMessages)
	server.Server = httptest.NewServer(mux)
	return server
}

type slackMessage struct {
	ChannelID   string
	ChannelName string
	User        string
	Username    string
	Text        string
	TS          string
	Permalink   string
}

func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	userScope := r.URL.Query().Get("user_scope")
	if redirectURI == "" || state == "" {
		http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
		return
	}
	if !strings.Contains(userScope, "channels:read") || !strings.Contains(userScope, "chat:write") {
		http.Error(w, "missing expected user scopes", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	values := target.Query()
	values.Set("code", "fake-slack-code")
	values.Set("state", state)
	target.RawQuery = values.Encode()
	// #nosec G710 -- fake OAuth upstream intentionally redirects to the
	// caller-provided test callback URI after parsing it.
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := r.BasicAuth(); !ok {
		slackError(w, http.StatusOK, "invalid_client_id")
		return
	}
	if err := r.ParseForm(); err != nil {
		slackError(w, http.StatusOK, "invalid_form_data")
		return
	}
	grantType := r.Form.Get("grant_type")
	if grantType == "" {
		slackError(w, http.StatusOK, "invalid_grant_type")
		return
	}
	// #nosec G101 -- deterministic fake upstream token material for tests.
	accessToken := "fake-slack-access-token"
	// #nosec G101 -- deterministic fake upstream token material for tests.
	refreshToken := "fake-slack-refresh-token"
	if grantType == "refresh_token" {
		// #nosec G101 -- deterministic fake upstream token material for tests.
		accessToken = "fake-slack-refreshed-access-token"
		// #nosec G101 -- deterministic fake upstream token material for tests.
		refreshToken = "fake-slack-rotated-refresh-token"
	}
	// #nosec G101 -- deterministic fake upstream token response for tests.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"access_token":  "xoxb-unused-bot-token",
		"token_type":    "bot",
		"scope":         "",
		"refresh_token": "unused-bot-refresh-token",
		"team": map[string]string{
			"id":   TeamID,
			"name": TeamName,
		},
		"authed_user": map[string]any{
			"id":            UserID,
			"scope":         "channels:read,groups:read,im:read,mpim:read,search:read,chat:write",
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"expires_in":    43200,
			"token_type":    "user",
		},
	})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		slackError(w, http.StatusOK, "not_authed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": true})
}

func (s *Server) conversationsList(w http.ResponseWriter, r *http.Request) {
	if !s.requireSlack(w, r) {
		return
	}
	channels := []map[string]any{
		{
			"id":          ChannelID,
			"name":        "toolmux",
			"is_channel":  true,
			"is_member":   true,
			"num_members": 3,
			"topic":       map[string]string{"value": "Toolmux work"},
		},
		{
			"id":          GroupID,
			"name":        "private-toolmux",
			"is_group":    true,
			"is_private":  true,
			"is_member":   true,
			"num_members": 2,
			"topic":       map[string]string{"value": "Private Toolmux work"},
		},
		{
			"id":    "D123456",
			"user":  UserID,
			"is_im": true,
		},
	}
	pageSize := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}
	start, end, nextCursor := paginate(len(channels), r.URL.Query().Get("cursor"), pageSize)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"channels": channels[start:end],
		"response_metadata": map[string]any{
			"next_cursor": nextCursor,
		},
	})
}

func (s *Server) chatPostMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireSlack(w, r) {
		return
	}
	var request struct {
		Channel  string `json:"channel"`
		Text     string `json:"text"`
		ThreadTS string `json:"thread_ts"`
		Mrkdwn   bool   `json:"mrkdwn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		slackError(w, http.StatusOK, "invalid_json")
		return
	}
	if request.Channel == "" || request.Text == "" {
		slackError(w, http.StatusOK, "invalid_arguments")
		return
	}
	message := slackMessage{
		ChannelID:   request.Channel,
		ChannelName: "toolmux",
		User:        UserID,
		Username:    "alberto",
		Text:        request.Text,
		TS:          "1715100001.000200",
		Permalink:   "https://toolmux.slack.com/archives/" + request.Channel + "/p1715100001000200",
	}
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"channel": request.Channel,
		"ts":      message.TS,
		"message": map[string]string{
			"type": "message",
			"user": UserID,
			"text": request.Text,
			"ts":   message.TS,
		},
	})
}

func (s *Server) searchMessages(w http.ResponseWriter, r *http.Request) {
	if !s.requireSlack(w, r) {
		return
	}
	query := strings.ToLower(r.URL.Query().Get("query"))
	count := 20
	if value := r.URL.Query().Get("count"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			count = parsed
		}
	}
	s.mu.Lock()
	messages := append([]slackMessage(nil), s.messages...)
	s.mu.Unlock()
	matches := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if query != "" && !strings.Contains(strings.ToLower(message.Text), query) {
			continue
		}
		matches = append(matches, map[string]any{
			"type":      "message",
			"user":      message.User,
			"username":  message.Username,
			"text":      message.Text,
			"ts":        message.TS,
			"permalink": message.Permalink,
			"channel": map[string]any{
				"id":         message.ChannelID,
				"name":       message.ChannelName,
				"is_channel": true,
			},
		})
	}
	if len(matches) > count {
		matches = matches[:count]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"query": r.URL.Query().Get("query"),
		"messages": map[string]any{
			"total":   len(matches),
			"matches": matches,
			"pagination": map[string]any{
				"page":        1,
				"page_count":  1,
				"per_page":    count,
				"total_count": len(matches),
			},
		},
	})
}

func (s *Server) requireSlack(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		slackError(w, http.StatusOK, "not_authed")
		return false
	}
	return true
}

func paginate(total int, cursor string, pageSize int) (int, int, string) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	start := 0
	if cursor != "" {
		if parsed, err := strconv.Atoi(cursor); err == nil && parsed >= 0 {
			start = parsed
		}
	}
	if start > total {
		start = total
	}
	end := min(start+pageSize, total)
	nextCursor := ""
	if end < total {
		nextCursor = strconv.Itoa(end)
	}
	return start, end, nextCursor
}

func slackError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
