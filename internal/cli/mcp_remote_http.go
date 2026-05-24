package cli

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/version"
)

func listMCPRemoteTools(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID string, trace *mcpRemoteHTTPTrace) ([]mcpRemoteTool, json.RawMessage, error) {
	var tools []mcpRemoteTool
	var pages []json.RawMessage
	var cursor *string
	for range mcpRemoteToolsListMaxPages {
		var params any
		if cursor != nil {
			params = map[string]any{"cursor": *cursor}
		}
		result, responseSessionID, err := callMCPRemote(ctx, client, server, bearerToken, sessionID, "tools/list", params, trace)
		if err != nil {
			return nil, nil, err
		}
		if responseSessionID != "" {
			sessionID = responseSessionID
		}
		var decoded struct {
			Tools      *[]mcpRemoteTool `json:"tools"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &decoded); err != nil {
			return nil, nil, fmt.Errorf("decode remote MCP tools/list: %w", err)
		}
		if decoded.Tools == nil {
			return nil, nil, fmt.Errorf("remote MCP tools/list returned no tools array")
		}
		tools = append(tools, (*decoded.Tools)...)
		pages = append(pages, result)
		if decoded.NextCursor == nil {
			return tools, aggregateMCPRemoteToolsListResult(tools, pages), nil
		}
		cursor = decoded.NextCursor
	}
	return nil, nil, fmt.Errorf("remote MCP tools/list exceeded %d pages", mcpRemoteToolsListMaxPages)
}

func aggregateMCPRemoteToolsListResult(tools []mcpRemoteTool, pages []json.RawMessage) json.RawMessage {
	if len(pages) == 1 {
		return pages[0]
	}
	result := struct {
		Tools []mcpRemoteTool   `json:"tools"`
		Pages []json.RawMessage `json:"pages,omitempty"`
	}{
		Tools: tools,
		Pages: pages,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return data
}

func initializeMCPRemoteSession(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken string, trace *mcpRemoteHTTPTrace) (json.RawMessage, string, error) {
	initResult, sessionID, err := callMCPRemote(ctx, client, server, bearerToken, "", "initialize", mcpRemoteInitializeParams(), trace)
	if err != nil {
		return nil, "", err
	}
	if err := notifyMCPRemoteInitialized(ctx, client, server, bearerToken, sessionID, trace); err != nil {
		return nil, "", err
	}
	return initResult, sessionID, nil
}

func mcpRemoteInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": mcpRemoteClientProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":        "toolmux",
			"title":       "Toolmux",
			"version":     version.Version,
			"description": "Policy-aware local CLI and agent bridge for SaaS tools and MCP servers.",
			"websiteUrl":  "https://github.com/fiam/toolmux",
			"icons": []map[string]any{{
				"src":      "https://raw.githubusercontent.com/fiam/toolmux/main/docs/assets/toolmux-icon.png",
				"mimeType": "image/png",
				"sizes":    []string{"1254x1254"},
			}},
		},
	}
}

func notifyMCPRemoteInitialized(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID string, trace *mcpRemoteHTTPTrace) error {
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateMCPRemoteURL(server.URL); err != nil {
		return err
	}
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	if err != nil {
		return err
	}
	resp, err := postMCPRemote(ctx, client, server, bearerToken, sessionID, body, trace)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("remote MCP notifications/initialized returned status %d", resp.StatusCode)
	}
	return nil
}

func callMCPRemote(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID, method string, params any, trace *mcpRemoteHTTPTrace, responseIdleTimeouts ...time.Duration) (json.RawMessage, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateMCPRemoteURL(server.URL); err != nil {
		return nil, "", err
	}
	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, "", err
		}
		rawParams = data
	}
	requestID := json.RawMessage("1")
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return nil, "", err
	}
	resp, err := postMCPRemote(ctx, client, server, bearerToken, sessionID, body, trace)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	responseSessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id"))
	if responseSessionID == "" {
		responseSessionID = sessionID
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, responseSessionID, &mcpRemoteHTTPStatusError{
			Method:     method,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(data)),
		}
	}
	result, err := decodeMCPRemoteResponse(resp, method, requestID, responseIdleTimeouts...)
	if err != nil {
		return nil, responseSessionID, err
	}
	return result, responseSessionID, nil
}

func (err *mcpRemoteHTTPStatusError) Error() string {
	if err == nil {
		return ""
	}
	if err.Body == "" {
		return fmt.Sprintf("remote MCP %s returned status %d", err.Method, err.StatusCode)
	}
	return fmt.Sprintf("remote MCP %s returned status %d: %s", err.Method, err.StatusCode, err.Body)
}

func mcpRemoteErrorStatus(err error, status int) bool {
	var statusErr *mcpRemoteHTTPStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == status
}

func postMCPRemote(ctx context.Context, client *http.Client, server mcpRemoteServer, bearerToken, sessionID string, body []byte, trace *mcpRemoteHTTPTrace) (*http.Response, error) {
	// #nosec G107 -- remote MCP server URLs are explicit user configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Protocol-Version", mcpRemoteClientProtocolVersion)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if trace != nil {
		trace.writeRequest(req, body)
	}
	resp, err := client.Do(req)
	if err != nil {
		if trace != nil {
			trace.writeError(err)
		}
		return nil, err
	}
	if trace != nil {
		data, truncated, err := mcpRemoteReadAndRestoreResponseBody(resp)
		if err != nil {
			return nil, err
		}
		trace.writeResponse(resp, data, truncated)
	}
	return resp, nil
}

func newMCPRemoteHTTPTrace(w io.Writer, enabled bool) *mcpRemoteHTTPTrace {
	if !enabled || w == nil {
		return nil
	}
	return &mcpRemoteHTTPTrace{w: w}
}

func (trace *mcpRemoteHTTPTrace) writeRequest(req *http.Request, body []byte) {
	if trace == nil || trace.w == nil {
		return
	}
	requestURI := req.URL.RequestURI()
	if requestURI == "" {
		requestURI = "/"
	}
	fmt.Fprintln(trace.w, "----- MCP HTTP request -----")
	fmt.Fprintf(trace.w, "%s %s HTTP/1.1\n", req.Method, requestURI)
	fmt.Fprintf(trace.w, "Host: %s\n", req.URL.Host)
	writeMCPRemoteHTTPHeaders(trace.w, req.Header)
	fmt.Fprintln(trace.w)
	writeMCPRemoteHTTPBody(trace.w, body)
}

func (trace *mcpRemoteHTTPTrace) writeResponse(resp *http.Response, body []byte, truncated bool) {
	if trace == nil || trace.w == nil {
		return
	}
	proto := resp.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	fmt.Fprintln(trace.w, "----- MCP HTTP response -----")
	fmt.Fprintf(trace.w, "%s %s\n", proto, resp.Status)
	writeMCPRemoteHTTPHeaders(trace.w, resp.Header)
	fmt.Fprintln(trace.w)
	writeMCPRemoteHTTPBody(trace.w, body)
	if truncated {
		fmt.Fprintf(trace.w, "[truncated after %d bytes]\n", mcpRemoteTraceBodyLimit)
	}
}

func (trace *mcpRemoteHTTPTrace) writeError(err error) {
	if trace == nil || trace.w == nil {
		return
	}
	fmt.Fprintln(trace.w, "----- MCP HTTP error -----")
	fmt.Fprintln(trace.w, err)
}

func writeMCPRemoteHTTPHeaders(w io.Writer, header http.Header) {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := header.Values(name)
		if mcpRemoteSensitiveHTTPHeader(name) {
			values = []string{"<redacted>"}
		}
		for _, value := range values {
			fmt.Fprintf(w, "%s: %s\n", name, value)
		}
	}
}

func mcpRemoteSensitiveHTTPHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	default:
		return false
	}
}

func writeMCPRemoteHTTPBody(w io.Writer, body []byte) {
	if len(body) == 0 {
		return
	}
	_, _ = w.Write(body)
	if body[len(body)-1] != '\n' {
		fmt.Fprintln(w)
	}
}

func mcpRemoteReadAndRestoreResponseBody(resp *http.Response) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, mcpRemoteTraceBodyLimit+1))
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, false, err
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	truncated := len(data) > mcpRemoteTraceBodyLimit
	if truncated {
		data = data[:mcpRemoteTraceBodyLimit]
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return data, truncated, nil
}

func decodeMCPRemoteResponse(resp *http.Response, method string, expectedID json.RawMessage, responseIdleTimeouts ...time.Duration) (json.RawMessage, error) {
	var decoded mcpResponse
	if mcpRemoteResponseIsSSE(resp.Header.Get("Content-Type")) {
		eventData, err := readMCPRemoteSSEResponse(resp.Body, expectedID, responseIdleTimeouts...)
		if err != nil {
			return nil, fmt.Errorf("decode remote MCP %s SSE response: %w", method, err)
		}
		if err := json.Unmarshal(eventData, &decoded); err != nil {
			return nil, fmt.Errorf("decode remote MCP %s SSE message: %w", method, err)
		}
	} else if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode remote MCP %s response: %w", method, err)
	}
	if decoded.Error != nil {
		return nil, decoded.Error
	}
	result, err := json.Marshal(decoded.Result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func mcpRemoteResponseIsSSE(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

func mcpRemoteToolCallTimeout(opts *options) time.Duration {
	if opts == nil || opts.mcpToolCallTimeout <= 0 {
		return mcpRemoteSSEIdleTimeout
	}
	return opts.mcpToolCallTimeout
}

func readMCPRemoteSSEResponse(reader io.Reader, expectedID json.RawMessage, idleTimeouts ...time.Duration) ([]byte, error) {
	idleTimeout := mcpRemoteSSEIdleTimeout
	if len(idleTimeouts) > 0 && idleTimeouts[0] > 0 {
		idleTimeout = idleTimeouts[0]
	}
	done := make(chan struct{})
	defer close(done)
	activity := make(chan struct{}, 1)
	messages := make(chan mcpRemoteSSEStreamItem, 4)
	go scanMCPRemoteSSEMessages(reader, done, activity, messages)

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idleTimeout)
	}

	for {
		select {
		case <-activity:
			resetTimer()
		case item, ok := <-messages:
			resetTimer()
			if !ok {
				return nil, fmt.Errorf("no response message event found")
			}
			if item.Err != nil {
				return nil, item.Err
			}
			matches, err := mcpRemoteSSEMessageIsResponse(item.Message, expectedID)
			if err != nil {
				return nil, err
			}
			if matches {
				return item.Message, nil
			}
		case <-timer.C:
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
			return nil, fmt.Errorf("timed out waiting for response message after %s of inactivity", idleTimeout)
		}
	}
}

func scanMCPRemoteSSEMessages(reader io.Reader, done <-chan struct{}, activity chan<- struct{}, messages chan<- mcpRemoteSSEStreamItem) {
	defer close(messages)
	stream := bufio.NewReader(io.LimitReader(reader, 8<<20))
	eventName := ""
	var dataLines []string
	notifyActivity := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	send := func(item mcpRemoteSSEStreamItem) bool {
		select {
		case messages <- item:
			return true
		case <-done:
			return false
		}
	}
	flush := func() bool {
		if len(dataLines) == 0 || (eventName != "" && eventName != "message") {
			return true
		}
		return send(mcpRemoteSSEStreamItem{Message: []byte(strings.Join(dataLines, "\n"))})
	}
	for {
		rawLine, err := stream.ReadString('\n')
		if rawLine != "" {
			notifyActivity()
			line := strings.TrimSuffix(rawLine, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if !flush() {
					return
				}
				eventName = ""
				dataLines = nil
			} else if !strings.HasPrefix(line, ":") {
				field, value, ok := strings.Cut(line, ":")
				if ok {
					value = strings.TrimPrefix(value, " ")
					switch field {
					case "event":
						eventName = value
					case "data":
						dataLines = append(dataLines, value)
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = flush()
				return
			}
			_ = send(mcpRemoteSSEStreamItem{Err: err})
			return
		}
	}
}

func mcpRemoteSSEMessageIsResponse(message []byte, expectedID json.RawMessage) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message, &fields); err != nil {
		return false, err
	}
	id, hasID := fields["id"]
	if !hasID {
		return false, nil
	}
	if len(expectedID) > 0 && !bytes.Equal(bytes.TrimSpace(id), bytes.TrimSpace(expectedID)) {
		return false, nil
	}
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	return hasResult || hasError, nil
}
