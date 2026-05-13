//go:build linux

package slackauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// chromeProfileBase returns the user-data directory for the first Chrome
// variant we find on disk. Most users have one of:
//
//	~/.config/google-chrome           (Google Chrome stable)
//	~/.config/google-chrome-beta      (Beta channel)
//	~/.config/google-chrome-unstable  (Dev / canary)
//	~/.config/chromium                (Open-source Chromium)
//
// SLACKAUTH_CHROME_USER_DATA_DIR overrides; otherwise we pick the most
// recently used (by Local State mtime).
func chromeProfileBase() (string, error) {
	if override := os.Getenv("SLACKAUTH_CHROME_USER_DATA_DIR"); override != "" {
		if _, err := os.Stat(override); err != nil { // #nosec G703 -- explicit Slack browser auth permits a user-selected Chrome profile root.
			return "", fmt.Errorf("SLACKAUTH_CHROME_USER_DATA_DIR=%q: %w", override, err)
		}
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	variants := []string{
		"google-chrome",
		"google-chrome-beta",
		"google-chrome-unstable",
		"chromium",
	}
	var best string
	var bestMtime int64
	for _, v := range variants {
		dir := filepath.Join(home, ".config", v)
		st, err := os.Stat(filepath.Join(dir, "Local State"))
		if err != nil {
			continue
		}
		if st.ModTime().UnixNano() > bestMtime {
			bestMtime = st.ModTime().UnixNano()
			best = dir
		}
	}
	if best == "" {
		return "", errors.New("no Google Chrome / Chromium profile found under ~/.config/")
	}
	return best, nil
}

// openChromeURL opens the URL in Chrome (or Chromium) without blocking.
// Tries each known Chrome binary in PATH, falling back to xdg-open if none
// of the chrome-* binaries are installed (which means xdg-open will use the
// system default browser — may or may not be Chrome).
func openChromeURL(ctx context.Context, url string) error {
	for _, bin := range []string{
		"google-chrome-stable",
		"google-chrome",
		"chromium",
		"chromium-browser",
	} {
		if _, err := exec.LookPath(bin); err == nil {
			cmd := exec.CommandContext(ctx, bin, url) // #nosec G204 -- bin is from a fixed Chrome allowlist and URL is Slack-owned.
			return cmd.Start()
		}
	}
	cmd := exec.CommandContext(ctx, "xdg-open", url) // #nosec G204 -- URL is Slack-owned and opened only after explicit browser auth.
	return cmd.Start()
}

// chromeSafeStorageKeys returns all AES-128 keys Chrome might have used to
// encrypt cookies on this Linux host. Chrome picks one of three secret
// backends at first run depending on the desktop environment; we don't know
// which (and Local State doesn't always record it), so we return candidates
// for each and let the cookie-decrypt loop try them in order.
//
// All Linux variants share PBKDF2-HMAC-SHA1 with salt "saltysalt" and only
// 1 iteration (vs macOS's 1003).
func chromeSafeStorageKeys(ctx context.Context, opts Options) ([][]byte, error) {
	opts.emit(Event{Kind: EventExpectingAuth, Detail: "your secret-service / KWallet may prompt to unlock the keyring"})
	var keys [][]byte
	for _, app := range []string{"chrome", "chromium"} {
		if pw, ok := readLibsecretPassword(ctx, app); ok {
			keys = append(keys, deriveChromeKey(pw, 1))
		}
	}
	for _, folder := range []string{"Chrome Safe Storage", "Chromium Safe Storage"} {
		if pw, ok := readKWalletPassword(ctx, folder); ok {
			keys = append(keys, deriveChromeKey(pw, 1))
		}
	}
	// Chrome's no-backend fallback: hard-coded password "peanuts".
	keys = append(keys, deriveChromeKey("peanuts", 1))
	return keys, nil
}

// readLibsecretPassword tries to retrieve Chrome's safe-storage password
// from the libsecret-compatible keyring (GNOME Keyring, KWallet via secrets
// proxy, etc.) via the `secret-tool` CLI. Returns ok=false if secret-tool
// isn't installed or the entry doesn't exist.
func readLibsecretPassword(ctx context.Context, application string) (string, bool) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return "", false
	}
	out, err := exec.CommandContext(ctx, "secret-tool", "lookup", "application", application).Output() // #nosec G204 -- application is chosen from fixed Chrome/Chromium values.
	if err != nil {
		return "", false
	}
	pw := strings.TrimRight(string(out), "\n")
	return pw, pw != ""
}

// readKWalletPassword queries KWallet directly via `kwallet-query`. KWallet
// 5 and 6 both ship the same CLI, but the flag set differs slightly — we
// try both invocations.
func readKWalletPassword(ctx context.Context, folder string) (string, bool) {
	if _, err := exec.LookPath("kwallet-query"); err != nil {
		return "", false
	}
	for _, args := range [][]string{
		{"-f", folder, "kdewallet", "-r", "Chromium Keys"},
		{"--folder", folder, "kdewallet", "--read-password", "Chromium Keys"},
	} {
		out, err := exec.CommandContext(ctx, "kwallet-query", args...).Output() // #nosec G204 -- args are fixed KWallet lookup forms.
		if err == nil {
			pw := strings.TrimRight(string(out), "\n")
			if pw != "" {
				return pw, true
			}
		}
	}
	return "", false
}
