package fakeupstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
)

type Server struct {
	*httptest.Server
}

func New() *Server {
	mux := http.NewServeMux()
	server := &Server{Server: httptest.NewServer(mux)}

	mux.HandleFunc("GET /oauth/authorize", server.authorize)
	mux.HandleFunc("POST /oauth/token", server.token)
	mux.HandleFunc("POST /oauth/revoke", status(http.StatusOK))
	mux.HandleFunc("POST /notion/v1/search", jsonHandler(map[string]any{
		"object":  "list",
		"results": []map[string]string{{"id": "notion-page-1", "object": "page"}},
	}))
	mux.HandleFunc("GET /jira/rest/api/3/search/jql", jsonHandler(map[string]any{
		"issues": []map[string]string{{"key": "OPS-1"}},
	}))
	mux.HandleFunc("GET /slack/api/conversations.list", jsonHandler(map[string]any{
		"ok":       true,
		"channels": []map[string]string{{"id": "C123", "name": "general"}},
	}))
	mux.HandleFunc("POST /linear/graphql", jsonHandler(map[string]any{
		"data": map[string]any{"viewer": map[string]string{"id": "linear-user-1"}},
	}))
	mux.HandleFunc("GET /google/drive/v3/files", jsonHandler(map[string]any{
		"files": []map[string]string{{"id": "file-1", "name": "Roadmap"}},
	}))
	mux.HandleFunc("GET /gmail/gmail/v1/users/me/labels", jsonHandler(map[string]any{
		"labels": []map[string]string{{"id": "INBOX", "name": "INBOX"}},
	}))

	return server
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	if redirectURI == "" || state == "" {
		http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	values := target.Query()
	values.Set("code", "fake-auth-code")
	values.Set("state", state)
	target.RawQuery = values.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	grantType := r.Form.Get("grant_type")
	if grantType == "" {
		http.Error(w, "missing grant_type", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  "fake-access-token",
		"refresh_token": "fake-refresh-token",
		"expires_in":    3600,
		"token_type":    "Bearer",
	})
}

func jsonHandler(value any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, value)
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
