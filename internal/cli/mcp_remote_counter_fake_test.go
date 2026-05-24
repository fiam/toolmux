//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func newFakeMCPRemoteServerWithToolListCounter(t *testing.T, called *map[string]any, toolsListCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"serverInfo": map[string]any{
						"name": "fake-linear",
					},
				},
			})
		case "tools/list":
			(*toolsListCalls)++
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode call params: %v", err)
			}
			if called != nil {
				*called = params.Arguments
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "called create_issue: " + params.Arguments["title"].(string),
					}},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcpError{Code: -32601, Message: "method not found"},
			})
		}
	}))
}
