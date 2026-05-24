//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func newFakeMCPRemoteSSESessionServer(t *testing.T, called *map[string]any) *httptest.Server {
	t.Helper()
	sessionCounter := 0
	sessions := map[string]bool{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			sessionCounter++
			sessionID := fmt.Sprintf("session-%d", sessionCounter)
			sessions[sessionID] = true
			w.Header().Set("Mcp-Session-Id", sessionID)
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"serverInfo": map[string]any{
						"name": "fake-sse",
					},
				},
			})
		case "notifications/initialized":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		case "tools/call":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode call params: %v", err)
			}
			if params.Name != "create_issue" {
				t.Fatalf("unexpected tool name %q", params.Name)
			}
			if called != nil {
				*called = params.Arguments
			}
			writeFakeMCPSSE(t, w, mcpResponse{
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
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcpError{Code: -32601, Message: "method not found"},
			})
		}
	}))
}

func newFakeMCPRemoteIdleToolCallServer(t *testing.T) *httptest.Server {
	t.Helper()
	sessionCounter := 0
	sessions := map[string]bool{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			sessionCounter++
			sessionID := fmt.Sprintf("session-%d", sessionCounter)
			sessions[sessionID] = true
			w.Header().Set("Mcp-Session-Id", sessionID)
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"serverInfo":      map[string]any{"name": "fake-idle"},
				},
			})
		case "notifications/initialized":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		case "tools/call":
			if !sessions[r.Header.Get("Mcp-Session-Id")] {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\n")
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`)
			fmt.Fprint(w, "\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			writeFakeMCPSSE(t, w, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcpError{Code: -32601, Message: "method not found"},
			})
		}
	}))
}

func writeFakeMCPSSE(t *testing.T, w http.ResponseWriter, response mcpResponse) {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
}
