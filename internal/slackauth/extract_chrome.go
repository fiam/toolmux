//go:build darwin || linux

package slackauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1" // #nosec G505 -- Chrome cookie key derivation requires PBKDF2-HMAC-SHA1.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"golang.org/x/crypto/pbkdf2"
)

// extractChrome rides along with the user's existing Chrome install. We tell
// the system "open this Slack URL in Chrome", then poll the on-disk cookie
// store and localStorage LevelDB until the session cookie and xoxc token are
// both present. Platform-specific bits (profile path, "open in chrome"
// command, safe-storage password retrieval + KDF params) live in the
// per-OS files extract_chrome_{darwin,linux}.go; this file holds the
// platform-agnostic flow.
func extractChrome(ctx context.Context, opts Options) ([]teamWithToken, string, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	base, err := chromeProfileBase()
	if err != nil {
		return nil, "", err
	}
	profileDir, err := findChromeProfile(base)
	if err != nil {
		return nil, "", err
	}

	url := "https://app.slack.com/"
	opts.emit(Event{Kind: EventLaunching, Detail: "Google Chrome", URL: url})
	if err := openChromeURL(ctx, url); err != nil {
		return nil, "", fmt.Errorf("focus Slack tab in Chrome: %w", err)
	}

	keys, err := chromeSafeStorageKeys(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	if len(keys) == 0 {
		return nil, "", errors.New("no Chrome safe-storage keys available — cannot decrypt cookies")
	}

	opts.emit(Event{Kind: EventWaiting, Detail: "Slack sign-in in Chrome"})

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var cookie, token string
	for {
		if cookie == "" {
			if c := tryReadSlackDCookie(profileDir, keys); c != "" {
				cookie = c
				opts.emit(Event{Kind: EventInfo, Detail: "found Slack session cookie"})
			}
		}
		if token == "" {
			if t, _ := tryReadSlackTokenFromLevelDB(profileDir); t != "" {
				token = t
				opts.emit(Event{Kind: EventInfo, Detail: "found Slack xoxc token"})
			}
		}
		if cookie != "" && token != "" {
			return []teamWithToken{{
				Team:  Team{Domain: opts.WorkspaceDomain},
				Token: token,
			}}, cookie, nil
		}

		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// chromeCookiesPath returns the cookie DB path for a profile. Chrome 96+
// moved cookies to <profile>/Network/Cookies on every platform; older
// installs still use <profile>/Cookies. Prefer the new location, fall back.
func chromeCookiesPath(profileDir string) string {
	newPath := filepath.Join(profileDir, "Network", "Cookies")
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	return filepath.Join(profileDir, "Cookies")
}

// findChromeProfile returns the profile under `base` most likely to hold an
// active Slack session — the one whose cookie DB was modified most recently.
// SLACKAUTH_CHROME_PROFILE pins a specific profile by folder name.
func findChromeProfile(base string) (string, error) {
	if pinned := os.Getenv("SLACKAUTH_CHROME_PROFILE"); pinned != "" {
		dir := filepath.Join(base, pinned)
		if _, err := os.Stat(dir); err != nil { // #nosec G703 -- explicit Slack browser auth permits a user-selected Chrome profile.
			return "", fmt.Errorf("pinned Chrome profile %q not found at %s", pinned, dir)
		}
		return dir, nil
	}

	profiles := []string{"Default"}
	if data, err := os.ReadFile(filepath.Join(base, "Local State")); err == nil { // #nosec G304 -- reads Chrome metadata only during explicit Slack browser auth.
		var ls struct {
			Profile struct {
				InfoCache map[string]json.RawMessage `json:"info_cache"`
			} `json:"profile"`
		}
		if json.Unmarshal(data, &ls) == nil && len(ls.Profile.InfoCache) > 0 {
			profiles = profiles[:0]
			for k := range ls.Profile.InfoCache {
				profiles = append(profiles, k)
			}
		}
	}

	type entry struct {
		dir   string
		mtime time.Time
	}
	var ranked []entry
	for _, p := range profiles {
		dir := filepath.Join(base, p)
		st, err := os.Stat(chromeCookiesPath(dir))
		if err != nil {
			continue
		}
		ranked = append(ranked, entry{dir, st.ModTime()})
	}
	if len(ranked) == 0 {
		return "", errors.New("no Chrome profile with a Cookies file found — is Chrome installed and have you used it?")
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].mtime.After(ranked[j].mtime) })
	return ranked[0].dir, nil
}

// tryReadSlackDCookie copies Chrome's cookie DB out of the way of its WAL
// lock, queries for the slack.com `d` cookie, and tries each candidate key
// in turn (Linux can have multiple secret-store backends, so we don't know
// up front which key Chrome used to encrypt). Returns "" when the cookie
// isn't there yet or no key decrypts it.
func tryReadSlackDCookie(profileDir string, keys [][]byte) string {
	src := chromeCookiesPath(profileDir)
	tmpDir, err := os.MkdirTemp("", "slackauth-cookies-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmpDir)
	dst := filepath.Join(tmpDir, "Cookies")
	if err := copyFile(src, dst); err != nil {
		return ""
	}

	out, err := exec.Command("sqlite3", dst, // #nosec G204 -- sqlite3 runs against a temp copy with a static query.
		"SELECT hex(encrypted_value) FROM cookies WHERE host_key LIKE '%.slack.com' AND name = 'd' LIMIT 1;",
	).Output()
	if err != nil {
		return ""
	}
	hexStr := strings.TrimSpace(string(out))
	if hexStr == "" {
		return ""
	}
	encrypted, err := hex.DecodeString(hexStr)
	if err != nil || len(encrypted) < 4 {
		return ""
	}
	if string(encrypted[:3]) != "v10" {
		return ""
	}
	ciphertext := encrypted[3:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return ""
	}

	for _, key := range keys {
		if val := tryDecryptCookieValue(ciphertext, key); val != "" {
			return val
		}
	}
	return ""
}

func tryDecryptCookieValue(ciphertext, key []byte) string {
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	iv := bytes.Repeat([]byte{' '}, 16)
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	plain = pkcs7Unpad(plain)
	val := string(plain)
	// macOS Chrome prefixes the plaintext with SHA-256(host_key) as a tamper
	// check. Older / cross-platform formats have no prefix — try both.
	if !strings.HasPrefix(val, "xoxd-") && !strings.HasPrefix(val, "xoxs-") && len(plain) > 32 {
		val = string(plain[32:])
	}
	if !strings.HasPrefix(val, "xoxd-") && !strings.HasPrefix(val, "xoxs-") {
		return ""
	}
	return val
}

// deriveChromeKey: PBKDF2-HMAC-SHA1 with the salt and iteration count Chrome
// uses on this platform.
func deriveChromeKey(password string, iterations int) []byte {
	return pbkdf2.Key([]byte(password), []byte("saltysalt"), iterations, 16, sha1.New)
}

// tryReadSlackTokenFromLevelDB scans Chrome's localStorage LevelDB for an
// xoxc- token. We copy the database to dodge Chrome's process lock, then
// iterate every key and look for the token in both UTF-8 form (raw bytes)
// and UTF-16 form (alternating NUL bytes, stripped before scanning).
func tryReadSlackTokenFromLevelDB(profileDir string) (string, error) {
	src := filepath.Join(profileDir, "Local Storage", "leveldb")
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp("", "slackauth-localstorage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	if err := copyDir(src, tmpDir, "LOCK"); err != nil {
		return "", err
	}

	db, err := leveldb.OpenFile(tmpDir, &opt.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer db.Close()

	iter := db.NewIterator(nil, nil)
	defer iter.Release()

	for iter.Next() {
		v := iter.Value()
		if tok := scanForXOXC(v); tok != "" {
			return tok, nil
		}
	}
	return "", iter.Error()
}

func scanForXOXC(v []byte) string {
	if tok := findXOXC(v); tok != "" {
		return tok
	}
	if bytes.IndexByte(v, 0) >= 0 {
		stripped := bytes.ReplaceAll(v, []byte{0}, nil)
		if tok := findXOXC(stripped); tok != "" {
			return tok
		}
	}
	return ""
}

func findXOXC(b []byte) string {
	const prefix = "xoxc-"
	i := bytes.Index(b, []byte(prefix))
	if i < 0 {
		return ""
	}
	j := i + len(prefix)
	for j < len(b) {
		c := b[j]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' {
			j++
			continue
		}
		break
	}
	if j-i <= len(prefix) {
		return ""
	}
	return string(b[i:j])
}

func pkcs7Unpad(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	n := int(b[len(b)-1])
	if n == 0 || n > len(b) || n > aes.BlockSize {
		return b
	}
	return b[:len(b)-n]
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- explicit Slack browser auth copies known Chrome/WebView data files.
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst) // #nosec G304 -- destination is an internal temp-copy path.
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string, skip ...string) error {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[s] = true
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if skipSet[e.Name()] {
			continue
		}
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(dstPath, 0o700); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}
