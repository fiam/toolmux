package notiontest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestIntegrationOAuthRoundTrip(t *testing.T) {
	t.Parallel()
	upstream := NewUpstream()
	defer upstream.Close()

	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("state"); got != "state-1" {
			t.Fatalf("state mismatch: %q", got)
		}
		if got := r.URL.Query().Get("code"); got == "" {
			t.Fatal("missing authorization code")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()

	authURL, err := url.Parse(upstream.URL + "/oauth/authorize")
	if err != nil {
		t.Fatal(err)
	}
	q := authURL.Query()
	q.Set("redirect_uri", callback.URL)
	q.Set("state", "state-1")
	authURL.RawQuery = q.Encode()

	// #nosec G107 -- integration test calls local httptest server URL.
	resp, err := upstream.Client().Get(authURL.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected callback status 204, got %d", resp.StatusCode)
	}

	form := url.Values{"grant_type": {"authorization_code"}, "code": {"fake-auth-code"}}
	// #nosec G107 -- integration test calls local httptest server URL.
	resp, err = upstream.Client().Post(
		upstream.URL+"/oauth/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var token map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token["access_token"] == "" || token["refresh_token"] == "" {
		t.Fatalf("expected token bundle, got %#v", token)
	}
}
