package fakeupstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	mux.HandleFunc("POST /linear/graphql", server.linearGraphQL)
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
	// #nosec G710 -- fake OAuth upstream intentionally redirects to the
	// caller-provided test callback URI after parsing it.
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
		"scope":         "read issues:create comments:create",
	})
}

func (s *Server) linearGraphQL(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid graphql request", http.StatusBadRequest)
		return
	}

	switch {
	case contains(request.Query, "viewer"):
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"viewer": map[string]string{
					"id": "linear-user-1", "name": "Linear User", "email": "user@example.com",
				},
			},
		})
	case contains(request.Query, "issues("):
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []map[string]any{linearIssue("issue-1", "SUP-1", "Existing issue")},
				},
			},
		})
	case contains(request.Query, "issue("):
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issue": linearIssue("issue-1", "SUP-1", "Existing issue"),
			},
		})
	case contains(request.Query, "issueCreate"):
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issueCreate": map[string]any{
					"success": true,
					"issue":   linearIssue("issue-2", "SUP-2", "Created issue"),
				},
			},
		})
	case contains(request.Query, "commentCreate"):
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"commentCreate": map[string]any{
					"success": true,
					"comment": map[string]string{
						"id": "comment-1", "body": "Looks good", "url": "https://linear.app/sup/comment-1", "issueId": "issue-2",
					},
				},
			},
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []map[string]string{{"message": "unsupported fake Linear query"}},
		})
	}
}

func linearIssue(id, identifier, title string) map[string]any {
	return map[string]any{
		"id": id, "identifier": identifier, "title": title, "url": "https://linear.app/sup/issue/" + identifier,
		"team": map[string]string{"id": "team-1", "key": "SUP", "name": "Supacli"},
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
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
