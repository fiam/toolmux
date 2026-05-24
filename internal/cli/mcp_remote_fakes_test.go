//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func newFakeMCPRemoteServer(t *testing.T, called *map[string]any) *httptest.Server {
	t.Helper()
	return newFakeMCPRemoteServerWithBearer(t, called, "")
}

func TestMCPRemoteStdioHelper(t *testing.T) {
	if !mcpRemoteStdioHelperRequested() {
		return
	}
	os.Exit(runFakeMCPStdioServer())
}

func mcpRemoteStdioHelperCommand() []string {
	return []string{os.Args[0], "-test.run=^TestMCPRemoteStdioHelper$", "--", "--toolmux-test-mcp-stdio"}
}

func mcpRemoteStdioHelperRequested() bool {
	return slices.Contains(os.Args, "--toolmux-test-mcp-stdio")
}

func runFakeMCPStdioServer() int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			return 2
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"serverInfo": map[string]any{
					"name": "fake-stdio",
				},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{fakeMCPCreateIssueTool(), fakeMCPCalculateTool()},
			}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				fmt.Fprintf(os.Stderr, "decode call params: %v\n", err)
				return 2
			}
			switch params.Name {
			case "create_issue":
				result = mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "stdio create_issue: " + fmt.Sprint(params.Arguments["title"]),
					}},
				}
			case "calculate":
				result = mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: "stdio calculate: " + fmt.Sprint(params.Arguments["operation"]),
					}},
				}
			default:
				result = mcpCallToolResult{
					IsError: true,
					Content: []mcpContent{{
						Type: "text",
						Text: "unexpected tool " + params.Name,
					}},
				}
			}
		default:
			result = nil
		}
		if err := encoder.Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result}); err != nil {
			fmt.Fprintf(os.Stderr, "encode response: %v\n", err)
			return 2
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scan stdin: %v\n", err)
		return 2
	}
	return 0
}

func newFakeMCPRemoteServerWithBearer(t *testing.T, called *map[string]any, bearerToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if bearerToken != "" && r.Header.Get("Authorization") != "Bearer "+bearerToken {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
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
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool(), fakeMCPCalculateTool()},
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
			text := ""
			switch params.Name {
			case "create_issue":
				text = "called create_issue: " + params.Arguments["title"].(string)
			case "calculate":
				text = "called calculate: " + params.Arguments["operation"].(string)
			default:
				t.Fatalf("unexpected tool name %q", params.Name)
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mcpCallToolResult{
					Content: []mcpContent{{
						Type: "text",
						Text: text,
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
