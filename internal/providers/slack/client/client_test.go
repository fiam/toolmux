package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientPostsMessageAsJSONWithBearerToken(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization header mismatch: %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("content type mismatch: %q", got)
		}
		if r.URL.Path != "/api/chat.postMessage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request PostMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Channel != "C123" || request.Text != "hello" {
			t.Fatalf("unexpected request: %#v", request)
		}
		writeJSON(t, w, map[string]any{
			"ok":      true,
			"channel": "C123",
			"ts":      "1715100000.000100",
			"message": map[string]string{"text": "hello"},
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL+"/api"), WithHTTPClient(server.Client()))
	out, err := client.PostMessage(context.Background(), PostMessageRequest{Channel: "C123", Text: "hello", Mrkdwn: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Channel != "C123" || out.Message.Text != "hello" {
		t.Fatalf("unexpected post response: %#v", out)
	}
}

func TestClientMapsSlackOKFalseErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"ok":       false,
			"error":    "missing_scope",
			"needed":   "search:read",
			"provided": "channels:read",
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	_, err := client.SearchMessages(context.Background(), SearchMessagesRequest{Query: "deploy"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if apiErr.Code != "missing_scope" || apiErr.Needed != "search:read" {
		t.Fatalf("unexpected API error: %#v", apiErr)
	}
}

func TestListConversationsAllPaginates(t *testing.T) {
	t.Parallel()
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		channels := []map[string]any{{"id": "C1", "name": "one"}}
		nextCursor := "1"
		if len(cursors) == 2 {
			channels[0]["id"] = "C2"
			channels[0]["name"] = "two"
			nextCursor = ""
		}
		writeJSON(t, w, map[string]any{
			"ok":       true,
			"channels": channels,
			"response_metadata": map[string]string{
				"next_cursor": nextCursor,
			},
		})
	}))
	defer server.Close()

	client := NewClient("token-1", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	out, err := client.ListConversationsAll(context.Background(), ListConversationsRequest{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Channels) != 2 || out.Channels[1].ID != "C2" {
		t.Fatalf("unexpected conversations: %#v", out.Channels)
	}
	if cursors[0] != "" || cursors[1] != "1" {
		t.Fatalf("unexpected cursors: %#v", cursors)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
