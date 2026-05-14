//go:build !darwin && !windows

package slackauth

// runWebViewHelper on unsupported platforms always errors. The helper-init
// path still compiles so subprocess invocations produce a clean JSON error
// rather than a panic.
func runWebViewHelper(_ helperInput) helperOutput {
	return helperOutput{Error: "webview engine is not supported on this platform"}
}
