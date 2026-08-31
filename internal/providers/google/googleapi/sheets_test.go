package googleapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSheetsAPIReportsPermissionRateLimitAndMalformedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		want       string
	}{
		{name: "permission denied", status: http.StatusForbidden, body: `{"error":{"message":"permission denied"}}`, want: "status 403"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":{"message":"quota exceeded"}}`, retryAfter: "5", want: "retry after 5"},
		{name: "malformed success", status: http.StatusOK, body: `{not-json`, want: "invalid character"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v4/spreadsheets/sheet-1" {
					t.Errorf("unexpected Sheets path %q", r.URL.Path)
				}
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)

			client := Client{SheetsBaseURL: server.URL, HTTPClient: server.Client(), AccessToken: "token"}
			_, err := client.GetSpreadsheet(context.Background(), "sheet-1", nil, false)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}
