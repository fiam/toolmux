package cli

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

func syncMCPRemoteStdioServer(ctx context.Context, entry mcpRemoteServerEntry) (mcpRemoteCache, error) {
	session, err := startMCPRemoteStdioSession(ctx, entry.Server)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	defer session.Close()
	initResult, err := session.call(ctx, "initialize", mcpRemoteInitializeParams(), mcpRemoteSSEIdleTimeout)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	if err := session.notify(ctx, "notifications/initialized", nil); err != nil {
		return mcpRemoteCache{}, err
	}
	tools, toolsResult, err := listMCPRemoteStdioTools(ctx, session)
	if err != nil {
		return mcpRemoteCache{}, err
	}
	return mcpRemoteCacheFromSync(entry, initResult, toolsResult, tools), nil
}

func listMCPRemoteStdioTools(ctx context.Context, session *mcpRemoteStdioSession) ([]mcpRemoteTool, json.RawMessage, error) {
	var tools []mcpRemoteTool
	var pages []json.RawMessage
	var cursor *string
	for range mcpRemoteToolsListMaxPages {
		var params any
		if cursor != nil {
			params = map[string]any{"cursor": *cursor}
		}
		result, err := session.call(ctx, "tools/list", params, mcpRemoteSSEIdleTimeout)
		if err != nil {
			return nil, nil, err
		}
		var decoded struct {
			Tools      *[]mcpRemoteTool `json:"tools"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &decoded); err != nil {
			return nil, nil, fmt.Errorf("decode stdio MCP tools/list: %w", err)
		}
		if decoded.Tools == nil {
			return nil, nil, fmt.Errorf("stdio MCP tools/list returned no tools array")
		}
		tools = append(tools, (*decoded.Tools)...)
		pages = append(pages, result)
		if decoded.NextCursor == nil {
			return tools, aggregateMCPRemoteToolsListResult(tools, pages), nil
		}
		cursor = decoded.NextCursor
	}
	return nil, nil, fmt.Errorf("stdio MCP tools/list exceeded %d pages", mcpRemoteToolsListMaxPages)
}

func callMCPRemoteStdioTool(ctx context.Context, entry mcpRemoteServerEntry, tool mcpRemoteTool, arguments map[string]any, responseIdleTimeout time.Duration) (mcpCallToolResult, error) {
	session, err := startMCPRemoteStdioSession(ctx, entry.Server)
	if err != nil {
		return mcpCallToolResult{}, err
	}
	defer session.Close()
	if _, err := session.call(ctx, "initialize", mcpRemoteInitializeParams(), mcpRemoteSSEIdleTimeout); err != nil {
		return mcpCallToolResult{}, err
	}
	if err := session.notify(ctx, "notifications/initialized", nil); err != nil {
		return mcpCallToolResult{}, err
	}
	result, err := session.call(ctx, "tools/call", map[string]any{
		"name":      tool.Name,
		"arguments": arguments,
	}, responseIdleTimeout)
	if err != nil {
		return mcpCallToolResult{}, err
	}
	var callResult mcpCallToolResult
	if err := json.Unmarshal(result, &callResult); err == nil && mcpRemoteCallToolResultHasPayload(callResult) {
		return callResult, nil
	}
	return mcpTextToolResult(string(result)), nil
}

type mcpRemoteStdioSession struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	messages <-chan mcpRemoteStdioMessage
	nextID   int64
	waitOnce sync.Once
	waitErr  error
}

type mcpRemoteStdioMessage struct {
	Data []byte
	Err  error
}

func startMCPRemoteStdioSession(ctx context.Context, server mcpRemoteServer) (*mcpRemoteStdioSession, error) {
	server = normalizeMCPRemoteServer(server)
	command, args, err := cleanMCPRemoteCommand(append([]string{server.Command}, server.Args...))
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- stdio MCP commands are explicit user configuration.
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start stdio MCP command %q: %w", command, err)
	}
	messages := make(chan mcpRemoteStdioMessage, 8)
	go scanMCPRemoteStdioMessages(stdout, messages)
	return &mcpRemoteStdioSession{
		cmd:      cmd,
		stdin:    stdin,
		messages: messages,
	}, nil
}

func (session *mcpRemoteStdioSession) notify(ctx context.Context, method string, params any) error {
	_, err := session.write(ctx, method, params, false)
	return err
}

func (session *mcpRemoteStdioSession) call(ctx context.Context, method string, params any, responseIdleTimeout time.Duration) (json.RawMessage, error) {
	requestID, err := session.write(ctx, method, params, true)
	if err != nil {
		return nil, err
	}
	return session.readResponse(ctx, method, requestID, responseIdleTimeout)
}

func (session *mcpRemoteStdioSession) write(ctx context.Context, method string, params any, includeID bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = data
	}
	request := mcpRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	}
	var requestID json.RawMessage
	if includeID {
		session.nextID++
		requestID = json.RawMessage(strconv.FormatInt(session.nextID, 10))
		request.ID = requestID
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if _, err := session.stdin.Write(body); err != nil {
		return nil, fmt.Errorf("write stdio MCP %s request: %w", method, err)
	}
	return requestID, nil
}

func (session *mcpRemoteStdioSession) readResponse(ctx context.Context, method string, expectedID json.RawMessage, responseIdleTimeout time.Duration) (json.RawMessage, error) {
	if responseIdleTimeout <= 0 {
		responseIdleTimeout = mcpRemoteSSEIdleTimeout
	}
	timer := time.NewTimer(responseIdleTimeout)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(responseIdleTimeout)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			session.kill()
			return nil, fmt.Errorf("decode stdio MCP %s response: timed out waiting for response message after %s of inactivity", method, responseIdleTimeout)
		case item, ok := <-session.messages:
			resetTimer()
			if !ok {
				if err := session.wait(); err != nil {
					return nil, fmt.Errorf("stdio MCP command exited before %s response: %w", method, err)
				}
				return nil, fmt.Errorf("stdio MCP command exited before %s response", method)
			}
			if item.Err != nil {
				return nil, fmt.Errorf("decode stdio MCP %s response: %w", method, item.Err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(item.Data, &fields); err != nil {
				return nil, fmt.Errorf("decode stdio MCP %s response: %w", method, err)
			}
			id, hasID := fields["id"]
			if !hasID || !bytes.Equal(bytes.TrimSpace(id), bytes.TrimSpace(expectedID)) {
				continue
			}
			var decoded mcpResponse
			if err := json.Unmarshal(item.Data, &decoded); err != nil {
				return nil, fmt.Errorf("decode stdio MCP %s response: %w", method, err)
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
	}
}

func (session *mcpRemoteStdioSession) Close() {
	if session == nil {
		return
	}
	if session.stdin != nil {
		_ = session.stdin.Close()
	}
	done := make(chan struct{})
	go func() {
		_ = session.wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(mcpRemoteStdioCloseTimeout):
		session.kill()
		<-done
	}
}

func (session *mcpRemoteStdioSession) wait() error {
	if session == nil || session.cmd == nil {
		return nil
	}
	session.waitOnce.Do(func() {
		session.waitErr = session.cmd.Wait()
	})
	return session.waitErr
}

func (session *mcpRemoteStdioSession) kill() {
	if session == nil || session.cmd == nil || session.cmd.Process == nil {
		return
	}
	_ = session.cmd.Process.Kill()
}

func scanMCPRemoteStdioMessages(reader io.Reader, messages chan<- mcpRemoteStdioMessage) {
	defer close(messages)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		data := append([]byte(nil), line...)
		messages <- mcpRemoteStdioMessage{Data: data}
	}
	if err := scanner.Err(); err != nil {
		messages <- mcpRemoteStdioMessage{Err: err}
	}
}

func mcpRemoteToolsFingerprint(tools []mcpRemoteTool) string {
	data, _ := json.Marshal(tools)
	return mcpRemoteMetadataHash(data)
}

func mcpRemoteMetadataHash(data []byte) string {
	var hash uint64 = 1469598103934665603
	for _, b := range data {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return fmt.Sprintf("%016x", hash)
}
