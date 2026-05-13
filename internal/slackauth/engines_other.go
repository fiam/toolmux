//go:build !darwin && !linux && !windows

package slackauth

// webviewAvailable: on non-macOS platforms the EngineWebView path isn't
// implemented yet (WebView2 wiring on Windows is the natural next step;
// WebKitGTK on Linux is a maybe). Returning false here keeps autodetect
// honest and lets EngineRod be the default fallback.
func webviewAvailable() bool { return false }

// chromeAvailable: the ride-along engine relies on macOS-specific paths
// and Keychain plumbing today. Linux (libsecret/KWallet) and Windows
// (DPAPI / app-bound encryption) are future work.
func chromeAvailable() bool { return false }
