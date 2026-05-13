//go:build linux

package slackauth

import (
	"os"
	"path/filepath"
)

// webviewAvailable: there is no WebView backend implemented for Linux yet
// (WebKitGTK would be the natural target). Returning false here keeps
// autodetect honest.
func webviewAvailable() bool { return false }

// chromeAvailable: returns true if any Chrome or Chromium profile is present
// in the user's ~/.config/. Cookie / localStorage extraction works against
// any of them on Linux.
func chromeAvailable() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, v := range []string{"google-chrome", "google-chrome-beta", "google-chrome-unstable", "chromium"} {
		if _, err := os.Stat(filepath.Join(home, ".config", v, "Local State")); err == nil {
			return true
		}
	}
	return false
}
