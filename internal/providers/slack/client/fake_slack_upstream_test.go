package slack_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	_ "github.com/fiam/toolmux/internal/providers/brokers/all"
)

type fakeSlackUpstream struct {
	Server *httptest.Server

	mu              sync.Mutex
	codes           map[string]string
	codeCounter     int
	directCookie    bool
	userRefresh     bool
	brokerRefresh   bool
	lastSearchToken string
	historyQuery    url.Values
	repliesQuery    url.Values
}

func newFakeSlackUpstream(t *testing.T) *fakeSlackUpstream {
	t.Helper()
	upstream := &fakeSlackUpstream{
		codes: map[string]string{},
	}
	upstream.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/oauth/v2/authorize":
			upstream.authorize(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/oauth.v2.access":
			upstream.token(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth.test":
			upstream.authTest(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth.revoke":
			writeSlackJSON(w, map[string]any{"ok": true, "revoked": true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/conversations.list":
			upstream.conversations(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/users.conversations":
			upstream.conversations(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/client.userBoot":
			upstream.userBoot(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/conversations.history":
			upstream.history(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/conversations.replies":
			upstream.replies(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/conversations.open":
			upstream.openConversation(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/search.messages":
			upstream.search(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/chat.postMessage":
			upstream.postMessage(t, w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Server.Close)
	return upstream
}

func (s *fakeSlackUpstream) authTest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	token := bearerToken(r)
	switch token {
	case "xoxc-direct":
		if r.Header.Get("Cookie") != "d=xoxd" {
			writeSlackJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
	case "xoxb-user-initial", "xoxb-broker-initial":
	default:
		writeSlackJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
		return
	}
	writeSlackJSON(w, map[string]any{
		"ok":      true,
		"url":     s.Server.URL + "/",
		"team":    "Toolmux Test",
		"user":    "toolmux",
		"team_id": "T123",
		"user_id": "U123",
	})
}

func (s *fakeSlackUpstream) authorize(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	clientID := r.URL.Query().Get("client_id")
	if clientID != "user-client" && clientID != "broker-client" {
		http.Error(w, "unexpected client_id", http.StatusBadRequest)
		t.Errorf("unexpected client_id %q", clientID)
		return
	}
	if r.URL.Query().Get("state") == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		t.Error("missing state")
		return
	}
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		t.Error("missing redirect_uri")
		return
	}
	s.mu.Lock()
	s.codeCounter++
	code := fmt.Sprintf("code-%d", s.codeCounter)
	s.codes[code] = clientID
	s.mu.Unlock()
	redirect, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		t.Errorf("parse redirect_uri: %v", err)
		return
	}
	query := redirect.Query()
	query.Set("code", code)
	query.Set("state", r.URL.Query().Get("state"))
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *fakeSlackUpstream) token(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		t.Errorf("parse token form: %v", err)
		return
	}
	clientID, clientSecret, _ := r.BasicAuth()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.mu.Lock()
		codeClient := s.codes[r.Form.Get("code")]
		s.mu.Unlock()
		if codeClient == "" || codeClient != clientID {
			http.Error(w, "unexpected code", http.StatusBadRequest)
			t.Errorf("unexpected code/client: code=%q client=%q", r.Form.Get("code"), clientID)
			return
		}
		switch clientID {
		case "user-client":
			if clientSecret != "user-secret" {
				http.Error(w, "bad user secret", http.StatusUnauthorized)
				t.Errorf("unexpected user secret %q", clientSecret)
				return
			}
			writeSlackJSON(w, oauthResponse("xoxb-user-initial", "refresh-user", 1))
		case "broker-client":
			if clientSecret != "broker-secret" {
				http.Error(w, "bad broker secret", http.StatusUnauthorized)
				t.Errorf("unexpected broker secret %q", clientSecret)
				return
			}
			writeSlackJSON(w, oauthResponse("xoxb-broker-initial", "refresh-broker", 1))
		default:
			http.Error(w, "unexpected client", http.StatusBadRequest)
			t.Errorf("unexpected client %q", clientID)
		}
	case "refresh_token":
		switch r.Form.Get("refresh_token") {
		case "refresh-user":
			if clientID != "user-client" || clientSecret != "user-secret" {
				http.Error(w, "bad user refresh client", http.StatusUnauthorized)
				t.Errorf("unexpected user refresh client %q/%q", clientID, clientSecret)
				return
			}
			s.mu.Lock()
			s.userRefresh = true
			s.mu.Unlock()
			writeSlackJSON(w, oauthResponse("xoxb-user-refreshed", "refresh-user-2", 3600))
		case "refresh-broker":
			if clientID != "broker-client" || clientSecret != "broker-secret" {
				http.Error(w, "bad broker refresh client", http.StatusUnauthorized)
				t.Errorf("unexpected broker refresh client %q/%q", clientID, clientSecret)
				return
			}
			s.mu.Lock()
			s.brokerRefresh = true
			s.mu.Unlock()
			writeSlackJSON(w, oauthResponse("xoxb-broker-refreshed", "refresh-broker-2", 3600))
		default:
			http.Error(w, "unexpected refresh token", http.StatusBadRequest)
			t.Errorf("unexpected refresh token %q", r.Form.Get("refresh_token"))
		}
	default:
		http.Error(w, "unexpected grant_type", http.StatusBadRequest)
		t.Errorf("unexpected grant_type %q", r.Form.Get("grant_type"))
	}
}

func (s *fakeSlackUpstream) conversations(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	token := bearerToken(r)
	switch token {
	case "xoxc-direct":
		if r.Header.Get("Cookie") != "d=xoxd" {
			http.Error(w, "missing cookie", http.StatusUnauthorized)
			t.Errorf("expected direct cookie, got %q", r.Header.Get("Cookie"))
			return
		}
		s.mu.Lock()
		s.directCookie = true
		s.mu.Unlock()
	case "xoxb-broker-refreshed":
	default:
		http.Error(w, "unexpected conversation token", http.StatusUnauthorized)
		t.Errorf("unexpected conversation token %q", token)
		return
	}
	writeSlackJSON(w, map[string]any{
		"ok": true,
		"channels": []map[string]any{{
			"id":          "C123",
			"name":        "general",
			"is_channel":  true,
			"num_members": 3,
		}},
	})
}

func (s *fakeSlackUpstream) userBoot(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeDirectRead(t, w, r) {
		return
	}
	writeSlackJSON(w, map[string]any{
		"ok": true,
		"channels": []map[string]any{{
			"id":          "C123",
			"name":        "general",
			"is_channel":  true,
			"num_members": 3,
		}},
		"ims": []map[string]any{{
			"id":    "D123",
			"user":  "U123",
			"is_im": true,
		}},
	})
}

func (s *fakeSlackUpstream) history(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeDirectRead(t, w, r) {
		return
	}
	s.mu.Lock()
	s.historyQuery = cloneValues(r.URL.Query())
	s.mu.Unlock()
	writeSlackJSON(w, map[string]any{
		"ok": true,
		"messages": []map[string]any{{
			"type": "message",
			"user": "U234",
			"text": "bounded update",
			"ts":   "1710000300.000000",
		}},
	})
}

func (s *fakeSlackUpstream) replies(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeDirectRead(t, w, r) {
		return
	}
	s.mu.Lock()
	s.repliesQuery = cloneValues(r.URL.Query())
	s.mu.Unlock()
	writeSlackJSON(w, map[string]any{
		"ok": true,
		"messages": []map[string]any{{
			"type":      "message",
			"user":      "U234",
			"text":      "bounded reply",
			"ts":        "1710000301.000000",
			"thread_ts": r.URL.Query().Get("ts"),
		}},
	})
}

func (s *fakeSlackUpstream) authorizeDirectRead(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if bearerToken(r) != "xoxc-direct" {
		http.Error(w, "unexpected history token", http.StatusUnauthorized)
		t.Errorf("unexpected read token %q", bearerToken(r))
		return false
	}
	if r.Header.Get("Cookie") != "d=xoxd" {
		http.Error(w, "missing cookie", http.StatusUnauthorized)
		t.Errorf("expected direct cookie, got %q", r.Header.Get("Cookie"))
		return false
	}
	return true
}

func (s *fakeSlackUpstream) search(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	token := bearerToken(r)
	if token != "xoxb-user-refreshed" {
		http.Error(w, "unexpected search token", http.StatusUnauthorized)
		t.Errorf("unexpected search token %q", token)
		return
	}
	s.mu.Lock()
	s.lastSearchToken = token
	s.mu.Unlock()
	writeSlackJSON(w, map[string]any{
		"ok":    true,
		"query": r.URL.Query().Get("query"),
		"messages": map[string]any{
			"total": 1,
			"matches": []map[string]any{{
				"channel":   map[string]any{"id": "C123", "name": "general"},
				"user":      "U123",
				"text":      "roadmap launch",
				"ts":        "1.000001",
				"permalink": "https://example.slack.com/archives/C123/p1",
			}},
		},
	})
}

func (s *fakeSlackUpstream) postMessage(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		t.Error("missing post token")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		t.Errorf("parse post form: %v", err)
		return
	}
	writeSlackJSON(w, map[string]any{
		"ok":      true,
		"channel": r.Form.Get("channel"),
		"ts":      "2.000002",
		"message": map[string]any{"text": r.Form.Get("text")},
	})
}

func (s *fakeSlackUpstream) openConversation(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		t.Error("missing open token")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		t.Errorf("parse open form: %v", err)
		return
	}
	if r.Form.Get("users") != "U123" {
		writeSlackJSON(w, map[string]any{"ok": false, "error": "user_not_found"})
		return
	}
	writeSlackJSON(w, map[string]any{
		"ok": true,
		"channel": map[string]any{
			"id":    "D123",
			"is_im": true,
			"user":  "U123",
		},
	})
}

func (s *fakeSlackUpstream) assertDirectCookie(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.directCookie {
		t.Fatal("expected direct token+cookie request")
	}
}

func (s *fakeSlackUpstream) assertUserRefresh(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.userRefresh {
		t.Fatal("expected user OAuth refresh")
	}
	if s.lastSearchToken != "xoxb-user-refreshed" {
		t.Fatalf("expected refreshed user token in search, got %q", s.lastSearchToken)
	}
}

func (s *fakeSlackUpstream) assertBrokerRefresh(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.brokerRefresh {
		t.Fatal("expected broker OAuth refresh")
	}
}

func (s *fakeSlackUpstream) assertHistoryQuery(t *testing.T, want url.Values) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	assertQueryValues(t, s.historyQuery, want)
}

func (s *fakeSlackUpstream) assertRepliesQuery(t *testing.T, want url.Values) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	assertQueryValues(t, s.repliesQuery, want)
}

func assertQueryValues(t *testing.T, got, want url.Values) {
	t.Helper()
	for key, values := range want {
		if got.Get(key) != values[0] {
			t.Fatalf("expected query %s=%q, got %q in %v", key, values[0], got.Get(key), got)
		}
	}
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, item := range values {
		cloned[key] = append([]string(nil), item...)
	}
	return cloned
}

func oauthResponse(accessToken, refreshToken string, expiresIn int) map[string]any {
	return map[string]any{
		"ok":            true,
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "bot",
		"expires_in":    expiresIn,
		"scope":         "channels:read,search:read,chat:write",
		"team":          map[string]any{"id": "T123", "name": "Toolmux Test"},
		"app_id":        "A123",
	}
}

func writeSlackJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func bearerToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
