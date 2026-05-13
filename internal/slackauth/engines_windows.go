//go:build windows

package slackauth

import (
	"os"
	"path/filepath"
)

// webviewAvailable: WebView2 ships preinstalled on Windows 11. On Windows 10
// it's typically auto-installed via Edge updates; otherwise users must
// install the Evergreen runtime. We detect it by looking for the per-user
// EBWebView client install marker; the system-wide path is also checked.
func webviewAvailable() bool {
	if base, err := os.UserConfigDir(); err == nil {
		// Per-user install of the WebView2 runtime puts a marker here.
		if _, err := os.Stat(filepath.Join(base, "Microsoft", "EdgeWebView", "Application")); err == nil {
			return true
		}
	}
	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\EdgeWebView\Application`,
		`C:\Program Files\Microsoft\EdgeWebView\Application`,
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// chromeAvailable: the on-disk chrome ride-along engine isn't implemented on
// Windows yet (DPAPI + Chrome 127+ app-bound encryption make it a separate
// project). Until that lands, Windows users should rely on EngineWebView.
func chromeAvailable() bool { return false }
