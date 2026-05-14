//go:build !darwin && !linux && !windows

package slackauth

// webviewAvailable: this file only covers OSes without a native WebView
// backend. Returning false keeps autodetect honest.
func webviewAvailable() bool { return false }

// chromeAvailable: the ride-along engine only supports platforms with
// provider-specific browser profile and credential-store handling.
func chromeAvailable() bool { return false }
