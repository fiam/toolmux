//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

func TestSyncMCPRemoteServerRejectsMissingToolsArray(t *testing.T) {
	t.Parallel()

	var initialized atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"serverInfo":      map[string]any{"name": "null-tools"},
				},
			})
		case "notifications/initialized":
			initialized.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized.Load() {
				t.Fatal("tools/list called before notifications/initialized")
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  nil,
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	_, err := syncMCPRemoteServer(context.Background(), upstream.Client(), mcpRemoteServerEntry{
		Name: "null-tools",
		Server: mcpRemoteServer{
			URL:       upstream.URL,
			Transport: mcpRemoteTransportStreamableHTTP,
		},
	}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "tools/list returned no tools array") {
		t.Fatalf("expected missing tools array error, got %v", err)
	}
}

func TestMCPRemoteToolVerbosePrintsHTTPTrace(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteServerWithBearer(t, &called, "secret-token")
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global", "--no-sync")
	runRootForRemoteTestWithInput(t, env, "secret-token", "mcp", "auth", "set", "linear", "--bearer-token-stdin")
	config, err := readToolmuxConfigFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := configMCPRemoteServer(config, "linear")
	if !ok {
		t.Fatal("expected linear server config")
	}
	if server.AuthRequired == nil || !*server.AuthRequired {
		t.Fatalf("expected bearer auth set to record auth_required true, got %#v", server)
	}
	runRootForRemoteTest(t, env, "mcp", "sync", "linear")

	output := runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "Trace", "-v")
	for _, want := range []string{
		"----- MCP HTTP request -----",
		"----- MCP HTTP response -----",
		"Authorization: <redacted>",
		`"method":"tools/call"`,
		"called create_issue: Trace",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected verbose output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("verbose output leaked bearer token:\n%s", output)
	}
}

func TestMCPRemoteAddVerbosePrintsHTTPTraceAndUsesLatestClientMetadata(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var sawInitialize bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Protocol-Version") != mcpRemoteClientProtocolVersion {
			t.Fatalf("expected MCP protocol header %q, got %q", mcpRemoteClientProtocolVersion, r.Header.Get("Mcp-Protocol-Version"))
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string         `json:"protocolVersion"`
				ClientInfo      map[string]any `json:"clientInfo"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatalf("decode initialize params: %v", err)
			}
			if params.ProtocolVersion != mcpRemoteClientProtocolVersion {
				t.Fatalf("expected initialize protocol %q, got %q", mcpRemoteClientProtocolVersion, params.ProtocolVersion)
			}
			if params.ClientInfo["title"] != "Toolmux" || params.ClientInfo["websiteUrl"] != "https://github.com/fiam/toolmux" {
				t.Fatalf("unexpected clientInfo: %#v", params.ClientInfo)
			}
			icons, _ := params.ClientInfo["icons"].([]any)
			if len(icons) != 1 {
				t.Fatalf("expected clientInfo icon, got %#v", params.ClientInfo["icons"])
			}
			sawInitialize = true
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": mcpRemoteClientProtocolVersion,
					"serverInfo":      map[string]any{"name": "metadata-test"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !sawInitialize {
				t.Fatal("tools/list called before initialize")
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCreateIssueTool()},
				},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	output := runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "debug", "--global", "-v")
	for _, want := range []string{
		"----- MCP HTTP request -----",
		"Mcp-Protocol-Version: " + mcpRemoteClientProtocolVersion,
		`"protocolVersion":"` + mcpRemoteClientProtocolVersion + `"`,
		`"title":"Toolmux"`,
		`"method":"tools/list"`,
		"synced toolbox debug: 1 tools",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected add verbose output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestSyncMCPRemoteServerCapturesAndReportsInstructions(t *testing.T) {
	t.Parallel()

	const notice = "Heads up: the SSE endpoint stops working after 30 June 2026; move to streamable HTTP."
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"protocolVersion": mcpRemoteClientProtocolVersion,
					"serverInfo":      map[string]any{"name": "notes"},
					"instructions":    notice,
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{"tools": []map[string]any{fakeMCPCreateIssueTool()}},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	cache, err := syncMCPRemoteServer(context.Background(), upstream.Client(), mcpRemoteServerEntry{
		Name:   "notes",
		Server: mcpRemoteServer{URL: upstream.URL, Transport: mcpRemoteTransportStreamableHTTP},
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cache.Instructions != notice {
		t.Fatalf("expected captured instructions %q, got %q", notice, cache.Instructions)
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetErr(&buf)
	reportMCPRemoteServerNotice(cmd, "notes", cache)
	out := buf.String()
	if !strings.Contains(out, "notice from MCP server notes") || !strings.Contains(out, "30 June 2026") {
		t.Fatalf("expected surfaced notice, got %q", out)
	}

	buf.Reset()
	reportMCPRemoteServerNotice(cmd, "notes", mcpRemoteCache{})
	if buf.String() != "" {
		t.Fatalf("expected no output without instructions, got %q", buf.String())
	}
}

func TestSyncMCPRemoteServerFollowsToolsListPagination(t *testing.T) {
	t.Parallel()

	var initialized atomic.Bool
	var cursors []*string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"protocolVersion": mcpRemoteClientProtocolVersion,
					"serverInfo":      map[string]any{"name": "paged-tools"},
				},
			})
		case "notifications/initialized":
			initialized.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized.Load() {
				t.Fatal("tools/list called before notifications/initialized")
			}
			var params struct {
				Cursor *string `json:"cursor"`
			}
			if len(req.Params) > 0 {
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Fatalf("decode tools/list params: %v", err)
				}
			}
			cursors = append(cursors, params.Cursor)
			if len(cursors) == 1 {
				_ = json.NewEncoder(w).Encode(mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"tools":      []map[string]any{fakeMCPCreateIssueTool()},
						"nextCursor": "",
					},
				})
				return
			}
			if params.Cursor == nil || *params.Cursor != "" {
				t.Fatalf("expected empty-string cursor on second page, got %#v", params.Cursor)
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{fakeMCPCalculateTool()},
				},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer upstream.Close()

	cache, err := syncMCPRemoteServer(context.Background(), upstream.Client(), mcpRemoteServerEntry{
		Name: "paged",
		Server: mcpRemoteServer{
			URL:       upstream.URL,
			Transport: mcpRemoteTransportStreamableHTTP,
		},
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Tools) != 2 || cache.Tools[0].Name != "calculate" || cache.Tools[1].Name != "create_issue" {
		t.Fatalf("unexpected paginated tools: %#v", cache.Tools)
	}
	if len(cursors) != 2 || cursors[0] != nil || cursors[1] == nil || *cursors[1] != "" {
		t.Fatalf("unexpected cursor sequence: %#v", cursors)
	}
	var raw struct {
		Tools []mcpRemoteTool   `json:"tools"`
		Pages []json.RawMessage `json:"pages"`
	}
	if err := json.Unmarshal(cache.Raw["tools_list"], &raw); err != nil {
		t.Fatalf("decode cached tools_list raw: %v", err)
	}
	if len(raw.Tools) != 2 || len(raw.Pages) != 2 {
		t.Fatalf("expected aggregated raw tools and pages, got %#v", raw)
	}
}

func TestMCPRemoteServerSupportsSSEAndSessionID(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	upstream := newFakeMCPRemoteSSESessionServer(t, &called)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "iterate", "--global")

	output := runRootForRemoteTest(t, env, "iterate", "create_issue", "--title", "SSE")
	if !strings.Contains(output, "called create_issue: SSE") {
		t.Fatalf("expected SSE remote tool output, got %q", output)
	}
	if called["title"] != "SSE" {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
}

func TestReadMCPRemoteSSEResponseSkipsNotifications(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
		``,
		`event: message`,
		`data: {"jsonrpc":"2.0","id":99,"result":{"ignored":true}}`,
		``,
		`event: message`,
		`data: {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"query_prometheus","inputSchema":{"type":"object"}}]}}`,
		``,
	}, "\n")
	message, err := readMCPRemoteSSEResponse(strings.NewReader(stream), json.RawMessage("1"))
	if err != nil {
		t.Fatal(err)
	}
	var response mcpResponse
	if err := json.Unmarshal(message, &response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.ID, json.RawMessage("1")) {
		t.Fatalf("expected response id 1, got %s", response.ID)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "query_prometheus") {
		t.Fatalf("expected final tools/list response, got %s", data)
	}
}

func TestReadMCPRemoteSSEResponseDefaultTimeoutIsSixtySeconds(t *testing.T) {
	t.Parallel()

	if mcpRemoteSSEIdleTimeout != time.Minute {
		t.Fatalf("expected default SSE idle timeout to be 60s, got %s", mcpRemoteSSEIdleTimeout)
	}
}

func TestReadMCPRemoteSSEResponseReturnsBeforeStreamCloses(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	type result struct {
		message []byte
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := readMCPRemoteSSEResponse(reader, json.RawMessage("1"), 2*time.Second)
		resultCh <- result{message: message, err: err}
	}()

	_, err := io.WriteString(writer, strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
		``,
	}, "\n")+"\n")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	_, err = io.WriteString(writer, strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"query_prometheus","inputSchema":{"type":"object"}}]}}`,
		``,
	}, "\n")+"\n")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !strings.Contains(string(result.message), "query_prometheus") {
			t.Fatalf("expected final tools/list response, got %s", result.message)
		}
	case <-time.After(750 * time.Millisecond):
		t.Fatal("timed out waiting for matching SSE response before stream close")
	}
}

func TestReadMCPRemoteSSEResponseTimesOutWhenStreamGoesIdle(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := readMCPRemoteSSEResponse(reader, json.RawMessage("1"), 100*time.Millisecond)
		errCh <- err
	}()

	_, err := io.WriteString(writer, strings.Join([]string{
		`event: message`,
		`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
		``,
	}, "\n")+"\n")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected idle timeout error")
		}
		if !strings.Contains(err.Error(), "timed out waiting for response message") {
			t.Fatalf("expected idle timeout error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle SSE response error")
	}
}

func TestRemoteMCPToolCommandUsesConfiguredCallTimeout(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	upstream := newFakeMCPRemoteIdleToolCallServer(t)
	defer upstream.Close()

	runRootForRemoteTest(t, env, "add", upstream.URL, "--name", "linear", "--global")
	_, err := runRootForRemoteTestError(t, env,
		"--mcp-tool-call-timeout", "100ms",
		"linear", "create_issue",
		"--title", "Slow",
	)
	if err == nil {
		t.Fatal("expected MCP tool call timeout error")
	}
	if !strings.Contains(err.Error(), "timed out waiting for response message after 100ms of inactivity") {
		t.Fatalf("expected configured timeout in error, got %v", err)
	}
}

func TestMCPRemoteServerRefreshesStaleCacheOnCommand(t *testing.T) {
	env := newMCPRemoteTestEnv(t)
	var called map[string]any
	var toolsListCalls int
	upstream := newFakeMCPRemoteServerWithToolListCounter(t, &called, &toolsListCalls)
	defer upstream.Close()
	writeRemoteTestConfig(t, env, map[string]mcpRemoteServer{
		"linear": {URL: upstream.URL, Transport: mcpRemoteTransportStreamableHTTP},
	})
	if err := writeMCPRemoteCache(env.CacheDir, "linear", mcpRemoteCache{
		Name:      "linear",
		URL:       upstream.URL,
		Transport: mcpRemoteTransportStreamableHTTP,
		Tools: []mcpRemoteTool{{
			Name:        "create_issue",
			Description: "Stale create issue",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
		}},
		SyncedAt: time.Now().Add(-25 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	output := runRootForRemoteTest(t, env, "linear", "create_issue", "--title", "Refreshed")
	if !strings.Contains(output, "called create_issue: Refreshed") {
		t.Fatalf("expected remote tool output, got %q", output)
	}
	if toolsListCalls == 0 {
		t.Fatal("expected stale cache to be refreshed before command execution")
	}
	if called["title"] != "Refreshed" {
		t.Fatalf("unexpected remote arguments: %#v", called)
	}
}
