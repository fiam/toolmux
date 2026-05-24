package client_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	_ "github.com/fiam/toolmux/internal/providers/google/broker"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

type fakeGoogleUpstream struct {
	Server *httptest.Server

	mu                sync.Mutex
	codes             map[string][]string
	pickerCodes       map[string]bool
	codeChallenges    map[string]string
	codeCounter       int
	lastAuthScopes    []string
	lastPickerMIME    string
	refreshCalled     bool
	lastDriveAPIToken string
	lastBatchBody     map[string]any
	lastCopySourceID  string
	lastCopyBody      map[string]any
	lastCopyParentID  string
}

func newFakeGoogleUpstream(t *testing.T) *fakeGoogleUpstream {
	t.Helper()
	upstream := &fakeGoogleUpstream{
		codes:          map[string][]string{},
		pickerCodes:    map[string]bool{},
		codeChallenges: map[string]string{},
	}
	upstream.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/oauth/authorize":
			upstream.authorize(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			upstream.token(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/revoke":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/documents/doc-1":
			upstream.getDocument(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/documents/doc-1:batchUpdate":
			upstream.batchUpdate(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files":
			upstream.listFiles(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files/doc-1":
			upstream.getFile(t, w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/drive/v3/files/doc-1/copy":
			upstream.copyFile(t, w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Server.Close)
	return upstream
}

func (s *fakeGoogleUpstream) authorize(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if got := r.URL.Query().Get("client_id"); got != "google-client" {
		http.Error(w, "unexpected client", http.StatusBadRequest)
		t.Errorf("unexpected client_id %q", got)
		return
	}
	if r.URL.Query().Get("access_type") != "offline" {
		http.Error(w, "missing offline access", http.StatusBadRequest)
		t.Error("expected access_type=offline")
		return
	}
	onePick := r.URL.Query().Get("trigger_onepick") == "true"
	if !onePick && r.URL.Query().Get("include_granted_scopes") != "true" {
		http.Error(w, "missing incremental auth", http.StatusBadRequest)
		t.Error("expected include_granted_scopes=true")
		return
	}
	if onePick && r.URL.Query().Get("include_granted_scopes") != "" {
		http.Error(w, "unexpected incremental auth", http.StatusBadRequest)
		t.Error("one-pick must not request include_granted_scopes")
		return
	}
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "missing redirect", http.StatusBadRequest)
		t.Error("missing redirect_uri")
		return
	}
	scopes := strings.Fields(r.URL.Query().Get("scope"))
	if onePick && !sameScopes(scopes, []string{googleapi.ScopeDriveFile}) {
		http.Error(w, "bad picker scopes", http.StatusBadRequest)
		t.Errorf("expected one-pick to request only drive.file, got %#v", scopes)
		return
	}
	hasPKCE := r.URL.Query().Get("code_challenge_method") == "S256" && r.URL.Query().Get("code_challenge") != ""
	s.mu.Lock()
	s.codeCounter++
	code := fmt.Sprintf("google-code-%d", s.codeCounter)
	if hasPKCE {
		s.codeChallenges[code] = r.URL.Query().Get("code_challenge")
	}
	if onePick {
		code = fmt.Sprintf("google-picker-code-%d", s.codeCounter)
		s.pickerCodes[code] = true
		if hasPKCE {
			s.codeChallenges[code] = r.URL.Query().Get("code_challenge")
		}
		s.lastPickerMIME = r.URL.Query().Get("mimetypes")
	}
	s.codes[code] = append([]string(nil), scopes...)
	s.lastAuthScopes = append([]string(nil), scopes...)
	s.mu.Unlock()
	redirect, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect", http.StatusBadRequest)
		t.Errorf("parse redirect URI: %v", err)
		return
	}
	query := redirect.Query()
	query.Set("code", code)
	query.Set("state", r.URL.Query().Get("state"))
	if onePick {
		query.Set("picked_file_ids", "doc-1")
		query.Set("scope", strings.Join(scopes, " "))
	}
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *fakeGoogleUpstream) token(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		t.Errorf("parse token form: %v", err)
		return
	}
	if r.Form.Get("client_id") != "google-client" || r.Form.Get("client_secret") != "google-secret" {
		http.Error(w, "bad client credentials", http.StatusUnauthorized)
		t.Errorf("unexpected client credentials %q/%q", r.Form.Get("client_id"), r.Form.Get("client_secret"))
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.mu.Lock()
		scopes := append([]string(nil), s.codes[r.Form.Get("code")]...)
		pickerCode := s.pickerCodes[r.Form.Get("code")]
		challenge := s.codeChallenges[r.Form.Get("code")]
		s.mu.Unlock()
		if len(scopes) == 0 {
			http.Error(w, "bad code", http.StatusBadRequest)
			t.Errorf("unexpected auth code %q", r.Form.Get("code"))
			return
		}
		if challenge != "" {
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != challenge {
				http.Error(w, "bad code verifier", http.StatusBadRequest)
				t.Errorf("unexpected PKCE verifier challenge %q, want %q", got, challenge)
				return
			}
		}
		response := map[string]any{
			"access_token": "ya29.drive",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        strings.Join(scopes, " "),
		}
		if pickerCode {
			response["access_token"] = "ya29.picker"
		}
		if hasScopes(scopes, googleapi.ScopeDriveFile) {
			response["refresh_token"] = "refresh-google"
		}
		writeGoogleJSON(w, response)
	case "refresh_token":
		if r.Form.Get("refresh_token") != "refresh-google" {
			http.Error(w, "unexpected refresh token", http.StatusBadRequest)
			t.Errorf("unexpected refresh token %q", r.Form.Get("refresh_token"))
			return
		}
		s.mu.Lock()
		s.refreshCalled = true
		s.mu.Unlock()
		writeGoogleJSON(w, map[string]any{
			"access_token": "ya29.refreshed",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	default:
		http.Error(w, "unexpected grant", http.StatusBadRequest)
		t.Errorf("unexpected grant_type %q", r.Form.Get("grant_type"))
	}
}

func (s *fakeGoogleUpstream) getDocument(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeAPI(t, w, r) {
		return
	}
	writeGoogleJSON(w, map[string]any{
		"documentId": "doc-1",
		"title":      "Shared plan",
		"revisionId": "rev-1",
		"body": map[string]any{
			"content": []map[string]any{{
				"startIndex": 1,
				"endIndex":   13,
				"paragraph": map[string]any{
					"elements": []map[string]any{{
						"startIndex": 1,
						"endIndex":   13,
						"textRun": map[string]any{
							"content": "Hello world\n",
						},
					}},
				},
			}},
		},
	})
}

func (s *fakeGoogleUpstream) batchUpdate(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeAPI(t, w, r) {
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		t.Errorf("decode batch body: %v", err)
		return
	}
	s.mu.Lock()
	s.lastBatchBody = body
	s.mu.Unlock()
	writeGoogleJSON(w, map[string]any{
		"documentId": "doc-1",
		"writeControl": map[string]any{
			"requiredRevisionId": "rev-1",
		},
		"replies": []map[string]any{{}},
	})
}

func (s *fakeGoogleUpstream) listFiles(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeAPI(t, w, r) {
		return
	}
	s.mu.Lock()
	s.lastDriveAPIToken = bearerToken(r)
	s.mu.Unlock()
	writeGoogleJSON(w, map[string]any{
		"files": []map[string]any{{
			"id":           "doc-1",
			"name":         "Shared plan",
			"mimeType":     googleapi.GoogleDocsMIMEType(),
			"webViewLink":  "https://docs.google.com/document/d/doc-1/edit",
			"modifiedTime": "2026-05-16T12:00:00Z",
		}},
	})
}

func (s *fakeGoogleUpstream) getFile(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeAPI(t, w, r) {
		return
	}
	writeGoogleJSON(w, map[string]any{
		"id":           "doc-1",
		"name":         "Shared plan",
		"mimeType":     googleapi.GoogleDocsMIMEType(),
		"webViewLink":  "https://docs.google.com/document/d/doc-1/edit",
		"modifiedTime": "2026-05-16T12:00:00Z",
	})
}

func (s *fakeGoogleUpstream) copyFile(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !s.authorizeAPI(t, w, r) {
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		t.Errorf("decode copy body: %v", err)
		return
	}
	parentID := ""
	if parents, ok := body["parents"].([]any); ok && len(parents) > 0 {
		parentID, _ = parents[0].(string)
	}
	s.mu.Lock()
	s.lastDriveAPIToken = bearerToken(r)
	s.lastCopySourceID = "doc-1"
	s.lastCopyBody = body
	s.lastCopyParentID = parentID
	s.mu.Unlock()
	name, _ := body["name"].(string)
	if name == "" {
		name = "Copy of Shared plan"
	}
	writeGoogleJSON(w, map[string]any{
		"id":           "doc-copy",
		"name":         name,
		"mimeType":     googleapi.GoogleDocsMIMEType(),
		"webViewLink":  "https://docs.google.com/document/d/doc-copy/edit",
		"modifiedTime": "2026-05-24T10:00:00Z",
	})
}

func (s *fakeGoogleUpstream) authorizeAPI(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	switch bearerToken(r) {
	case "ya29.docs", "ya29.drive", "ya29.picker", "ya29.refreshed":
		return true
	default:
		http.Error(w, "missing or unexpected bearer token", http.StatusUnauthorized)
		t.Errorf("unexpected Google API token %q", bearerToken(r))
		return false
	}
}

func (s *fakeGoogleUpstream) assertAuthorization(t *testing.T, want []string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !sameScopes(s.lastAuthScopes, want) {
		t.Fatalf("expected last auth scopes %#v, got %#v", want, s.lastAuthScopes)
	}
}

func (s *fakeGoogleUpstream) assertCopyRequest(t *testing.T, wantSourceID, wantName, wantParentID string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastCopySourceID != wantSourceID {
		t.Fatalf("expected copy source %q, got %q", wantSourceID, s.lastCopySourceID)
	}
	if gotName, _ := s.lastCopyBody["name"].(string); gotName != wantName {
		t.Fatalf("expected copy name %q, got %q in %#v", wantName, gotName, s.lastCopyBody)
	}
	if s.lastCopyParentID != wantParentID {
		t.Fatalf("expected copy parent %q, got %q in %#v", wantParentID, s.lastCopyParentID, s.lastCopyBody)
	}
}

func (s *fakeGoogleUpstream) assertDocsInsertText(t *testing.T, wantText string, wantIndex int) {
	t.Helper()
	request := firstDocsBatchRequest(t, s.lastDocsBatchBody(t))
	insertText, ok := request["insertText"].(map[string]any)
	if !ok {
		t.Fatalf("expected insertText request, got %#v", request)
	}
	if gotText, _ := insertText["text"].(string); gotText != wantText {
		t.Fatalf("expected inserted text %q, got %q in %#v", wantText, gotText, insertText)
	}
	location, ok := insertText["location"].(map[string]any)
	if !ok {
		t.Fatalf("expected insertText location, got %#v", insertText)
	}
	if gotIndex, _ := location["index"].(float64); int(gotIndex) != wantIndex {
		t.Fatalf("expected insert index %d, got %#v in %#v", wantIndex, location["index"], location)
	}
}

func (s *fakeGoogleUpstream) assertDocsReplaceAllText(t *testing.T, wantText, wantReplaceText string, wantMatchCase bool, wantRevisionID string) {
	t.Helper()
	body := s.lastDocsBatchBody(t)
	request := firstDocsBatchRequest(t, body)
	replaceAllText, ok := request["replaceAllText"].(map[string]any)
	if !ok {
		t.Fatalf("expected replaceAllText request, got %#v", request)
	}
	containsText, ok := replaceAllText["containsText"].(map[string]any)
	if !ok {
		t.Fatalf("expected replaceAllText containsText, got %#v", replaceAllText)
	}
	if gotText, _ := containsText["text"].(string); gotText != wantText {
		t.Fatalf("expected match text %q, got %q in %#v", wantText, gotText, containsText)
	}
	if gotMatchCase, _ := containsText["matchCase"].(bool); gotMatchCase != wantMatchCase {
		t.Fatalf("expected matchCase %v, got %#v in %#v", wantMatchCase, containsText["matchCase"], containsText)
	}
	if gotReplaceText, _ := replaceAllText["replaceText"].(string); gotReplaceText != wantReplaceText {
		t.Fatalf("expected replacement text %q, got %q in %#v", wantReplaceText, gotReplaceText, replaceAllText)
	}
	writeControl, ok := body["writeControl"].(map[string]any)
	if !ok {
		t.Fatalf("expected writeControl, got %#v", body)
	}
	if gotRevisionID, _ := writeControl["requiredRevisionId"].(string); gotRevisionID != wantRevisionID {
		t.Fatalf("expected required revision %q, got %q in %#v", wantRevisionID, gotRevisionID, writeControl)
	}
}

func (s *fakeGoogleUpstream) lastDocsBatchBody(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastBatchBody == nil {
		t.Fatal("expected Docs batchUpdate request")
	}
	return s.lastBatchBody
}

func firstDocsBatchRequest(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	requests, ok := body["requests"].([]any)
	if !ok || len(requests) == 0 {
		t.Fatalf("expected Docs batchUpdate requests, got %#v", body)
	}
	request, ok := requests[0].(map[string]any)
	if !ok {
		t.Fatalf("expected Docs batchUpdate request object, got %#v", requests[0])
	}
	return request
}

func (s *fakeGoogleUpstream) assertRefreshAndDriveToken(t *testing.T, wantToken string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.refreshCalled {
		t.Fatal("expected Google refresh token flow")
	}
	if s.lastDriveAPIToken != wantToken {
		t.Fatalf("expected drive request token %q, got %q", wantToken, s.lastDriveAPIToken)
	}
}

func (s *fakeGoogleUpstream) assertDriveToken(t *testing.T, wantToken string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastDriveAPIToken != wantToken {
		t.Fatalf("expected drive request token %q, got %q", wantToken, s.lastDriveAPIToken)
	}
}

func (s *fakeGoogleUpstream) assertPickerMIME(t *testing.T, want string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastPickerMIME != want {
		t.Fatalf("expected picker MIME %q, got %q", want, s.lastPickerMIME)
	}
}

func writeGoogleJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return token
	}
	return ""
}

func hasScopes(scopes []string, wants ...string) bool {
	have := map[string]bool{}
	for _, scope := range scopes {
		have[scope] = true
	}
	for _, want := range wants {
		if !have[want] {
			return false
		}
	}
	return true
}

func sameScopes(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	have := map[string]bool{}
	for _, scope := range left {
		have[scope] = true
	}
	for _, scope := range right {
		if !have[scope] {
			return false
		}
	}
	return true
}
