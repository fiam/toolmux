//go:build darwin

package slackauth

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// chromeProfileBase returns the Chrome user-data directory on macOS.
func chromeProfileBase() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Google", "Chrome"), nil
}

// openChromeURL focuses Chrome (launching it if necessary) on the given URL.
// `open -a` returns immediately after handing the request to launchd, so this
// doesn't block on Chrome's lifetime.
func openChromeURL(ctx context.Context, url string) error {
	return exec.CommandContext(ctx, "open", "-a", "Google Chrome", url).Run()
}

// chromeSafeStorageKeys returns the AES-128 keys Chrome may have used to
// encrypt its cookies on this host. macOS uses a single key derived from
// the "Chrome Safe Storage" password stored in the login Keychain, with
// PBKDF2-HMAC-SHA1 / 1003 iterations.
func chromeSafeStorageKeys(ctx context.Context, opts Options) ([][]byte, error) {
	opts.emit(Event{Kind: EventExpectingAuth, Detail: "macOS Keychain may prompt for the 'Chrome Safe Storage' password — click Always Allow"})
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Chrome Safe Storage", "-w").Output()
	if err != nil {
		return nil, err
	}
	password := strings.TrimRight(string(out), "\n")
	return [][]byte{deriveChromeKey(password, 1003)}, nil
}
