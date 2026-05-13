package slackauth

import (
	"encoding/json"
	"io"
	"os"
	"runtime"
)

// init runs before main() of any program that imports this package. When the
// helperEnvVar is set, the process is meant to be the WebView subprocess: it
// reads helperInput from stdin, hands off to the platform's runWebViewHelper
// (Cocoa on darwin; an error stub elsewhere), prints helperOutput to stdout,
// and exits without ever returning control to the host program's main.
//
// On a normal invocation (env var unset) this returns immediately and the
// host program runs as usual.
func init() {
	if os.Getenv(helperEnvVar) == "" {
		return
	}

	// Cocoa code MUST run on the OS main thread. We're still in init(), which
	// runs on the main goroutine before any user code has had a chance to
	// migrate it; locking now pins it for the helper's lifetime.
	runtime.LockOSThread()

	var in helperInput
	if data, err := io.ReadAll(os.Stdin); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &in)
	}

	out := runWebViewHelper(in)

	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		// Last-ditch report: if even encoding fails, surface as an event
		// (which the parent treats as EventInfo) and bail with a distinct
		// exit code so the parent's error message is specific.
		helperEmit(Event{Kind: EventInfo, Detail: "encode result failed: " + err.Error()})
		os.Exit(2)
	}
	if out.Error != "" {
		os.Exit(1)
	}
	os.Exit(0)
}
