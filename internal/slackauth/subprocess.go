package slackauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// helperEnvVar makes a slackauth process behave as the webview helper rather
// than running its normal main. The package's init() function checks this and
// hands off to runWebViewHelper before the host program's main() ever runs.
const helperEnvVar = "SLACKAUTH_WEBVIEW_HELPER"

// helperInput is the JSON the parent sends on the helper's stdin.
type helperInput struct {
	WorkspaceDomain string `json:"workspace_domain,omitempty"`
	UserDataDir     string `json:"user_data_dir,omitempty"`
	// TimeoutNS is the per-flow timeout in nanoseconds. Zero means no bound.
	TimeoutNS int64 `json:"timeout_ns,omitempty"`
}

// helperOutput is the JSON the helper writes to stdout exactly once before
// exiting. Either Teams+Cookie are populated, or Error is.
type helperOutput struct {
	Teams  []teamWithToken `json:"teams,omitempty"`
	Cookie string          `json:"cookie,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// helperEventLine is the wire format for an Event emitted by the helper on its
// stderr. The Tag field lets the parent distinguish our structured events from
// any incidental stderr output (panics, debug from imported libraries, etc.).
type helperEventLine struct {
	Tag    string `json:"__sa_event"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	URL    string `json:"url,omitempty"`
}

// helperEmit writes a single NDJSON event line to stderr. Used only from
// helper-mode subprocesses; the parent picks these up and forwards them to
// opts.OnEvent. Never used in non-helper code paths — call opts.emit instead.
func helperEmit(e Event) {
	line, err := json.Marshal(helperEventLine{
		Tag:    "1",
		Kind:   string(e.Kind),
		Detail: e.Detail,
		URL:    e.URL,
	})
	if err != nil {
		return
	}
	line = append(line, '\n')
	_, _ = os.Stderr.Write(line)
}

// extractWebView spawns this same binary as a webview helper, blocks until
// the user has signed in, and parses the result. The library never touches
// Cocoa in the parent process — that keeps callers free of main-thread or
// LockOSThread requirements. Events emitted by the helper on stderr are
// streamed back to opts.OnEvent so callers see a unified progress feed.
func extractWebView(ctx context.Context, opts Options) ([]teamWithToken, string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, "", fmt.Errorf("locate self for helper: %w", err)
	}

	input, err := json.Marshal(helperInput{
		WorkspaceDomain: opts.WorkspaceDomain,
		UserDataDir:     opts.UserDataDir,
		TimeoutNS:       int64(opts.Timeout),
	})
	if err != nil {
		return nil, "", fmt.Errorf("marshal helper input: %w", err)
	}

	cmd := exec.CommandContext(ctx, self) // #nosec G204 -- self is the current Toolmux executable used as the WebView helper.
	cmd.Env = append(os.Environ(), helperEnvVar+"=1")
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, "", fmt.Errorf("pipe helper stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start helper: %w", err)
	}

	streamHelperEvents(stderrPipe, opts)
	runErr := cmd.Wait()

	out, decErr := decodeHelperOutput(stdout.Bytes())
	switch {
	case decErr != nil && runErr != nil:
		return nil, "", fmt.Errorf("webview helper failed: %w", runErr)
	case decErr != nil:
		return nil, "", fmt.Errorf("decode helper output: %w", decErr)
	case out.Error != "":
		return nil, "", errors.New(out.Error)
	case runErr != nil:
		return nil, "", fmt.Errorf("webview helper failed: %w", runErr)
	}
	return out.Teams, out.Cookie, nil
}

// streamHelperEvents reads the helper's stderr line-by-line in the current
// goroutine and forwards events to opts.OnEvent. Returns when EOF is reached
// (i.e., the helper exits and closes its stderr). Lines that don't parse as
// our event format are surfaced as EventInfo so the caller still sees them
// without the package ever writing to stderr directly.
func streamHelperEvents(r io.Reader, opts Options) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev helperEventLine
		if err := json.Unmarshal(line, &ev); err == nil && ev.Tag == "1" {
			opts.emit(Event{Kind: EventKind(ev.Kind), Detail: ev.Detail, URL: ev.URL})
			continue
		}
		opts.emit(Event{Kind: EventInfo, Detail: string(line)})
	}
}

func decodeHelperOutput(raw []byte) (helperOutput, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return helperOutput{}, errors.New("helper produced no output")
	}
	var out helperOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return helperOutput{}, fmt.Errorf("%w (raw: %q)", err, raw)
	}
	return out, nil
}
