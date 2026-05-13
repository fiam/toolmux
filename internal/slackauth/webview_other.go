//go:build !darwin && !windows

package slackauth

// runWebViewHelper on non-macOS platforms always errors — there's no WKWebView
// to drive. The helper-init path still compiles so subprocess invocations
// produce a clean JSON error rather than a panic.
func runWebViewHelper(_ helperInput) helperOutput {
	return helperOutput{Error: "webview engine is only supported on macOS"}
}
