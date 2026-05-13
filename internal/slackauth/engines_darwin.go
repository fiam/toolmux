//go:build darwin

package slackauth

import "os"

// webviewAvailable: WKWebView ships with every supported macOS version
// (macOS 11+ in our cgo CFLAGS), so this is unconditionally true on darwin.
func webviewAvailable() bool { return true }

// chromeAvailable: ride-along requires Google Chrome installed at the
// standard /Applications path. Browsers like Brave or Chromium that share
// the Chromium codebase have a different profile layout and are not yet
// supported by the chrome engine.
func chromeAvailable() bool {
	_, err := os.Stat("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	return err == nil
}
