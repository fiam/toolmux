//go:build windows

package slackauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// runWebViewHelper on Windows drives the Edge WebView2 runtime. We point its
// user-data folder at a controlled location so we can read the cookie DB
// off-disk after sign-in — the COM cookie-manager API works too but reading
// the SQLite cookie store + DPAPI-decrypting matches what we do on Linux/Mac
// for the chrome engine, and avoids another COM dance.
func runWebViewHelper(in helperInput) helperOutput {
	dataDir, err := webview2DataDir()
	if err != nil {
		return helperOutput{Error: fmt.Sprintf("locate data dir: %v", err)}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return helperOutput{Error: fmt.Sprintf("create data dir: %v", err)}
	}

	url := startURL(in.WorkspaceDomain)
	helperEmit(Event{Kind: EventLaunching, Detail: "WebView2 (Edge runtime)", URL: url})

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:    false,
		DataPath: dataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "Sign in to Slack — slackauth",
			Width:  1100,
			Height: 800,
		},
	})
	if w == nil {
		return helperOutput{Error: "WebView2 runtime not available — install the Evergreen WebView2 runtime"}
	}
	defer w.Destroy()

	var (
		token     string
		tokenOnce sync.Once
		captured  atomic.Bool
	)
	if err := w.Bind("__slackauth_report_token", func(t string) {
		tokenOnce.Do(func() {
			if t != "" {
				token = t
				captured.Store(true)
				w.Terminate()
			}
		})
	}); err != nil {
		return helperOutput{Error: fmt.Sprintf("bind report function: %v", err)}
	}

	// Run user scripts at document-start on every navigation: the
	// fetch/XHR/WebSocket hook stashes any xoxc the SPA touches on
	// window.__slackauth_captured_token; the poller looks for that AND for
	// the localConfig_v2 / boot_data fallbacks, reporting the first hit back
	// to Go via the bound function.
	w.Init(jsHookRequests)
	w.Init(jsWindowsPoller)

	w.Navigate(url)
	helperEmit(Event{Kind: EventWaiting, Detail: "Slack sign-in in the WebView2 window"})

	// Optional timeout: terminate the run loop when ctx fires.
	if in.TimeoutNS > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(in.TimeoutNS))
		defer cancel()
		go func() {
			<-ctx.Done()
			if !captured.Load() {
				w.Terminate()
			}
		}()
	}

	w.Run() // blocks the main thread until Terminate

	if !captured.Load() {
		return helperOutput{Error: "WebView2 closed before a token could be captured"}
	}

	cookie, err := readSlackDCookieWebView2(dataDir)
	if err != nil {
		return helperOutput{Error: fmt.Sprintf("read d cookie: %v", err)}
	}
	return helperOutput{
		Teams:  []teamWithToken{{Token: token}},
		Cookie: cookie,
	}
}

// jsWindowsPoller polls localStorage and the hook-captured token, reporting
// the first xoxc- value back to the Go side. Runs once per navigation; the
// inner loop exits as soon as we report a token (so the user can sign in
// across redirects without restarting).
const jsWindowsPoller = `(async () => {
  const sleep = (ms) => new Promise(r => setTimeout(r, ms));
  const findToken = () => {
    let t = window.__slackauth_captured_token;
    if (typeof t === 'string' && t.indexOf('xoxc-') === 0) return t;
    for (const key of ['localConfig_v2', 'localConfig_v3', 'localConfig']) {
      try {
        const raw = localStorage.getItem(key);
        if (!raw) continue;
        const cfg = JSON.parse(raw);
        const teams = cfg && cfg.teams ? Object.values(cfg.teams) : [];
        for (const team of teams) {
          if (team && typeof team.token === 'string' && team.token.indexOf('xoxc-') === 0) {
            return team.token;
          }
        }
      } catch (e) {}
    }
    try {
      const boot = window.boot_data || (window.TS && window.TS.boot_data);
      if (boot && typeof boot.api_token === 'string' && boot.api_token.indexOf('xoxc-') === 0) {
        return boot.api_token;
      }
    } catch (e) {}
    return '';
  };
  for (let i = 0; i < 1200; i++) {
    const t = findToken();
    if (t && window.__slackauth_report_token) {
      window.__slackauth_report_token(t);
      return;
    }
    await sleep(1500);
  }
})();`

// webview2DataDir returns the per-user folder we hand to WebView2 as its
// `UserDataFolder`. Reusing the same path across runs lets the user stay
// signed in (cookies + localStorage persist between extractions).
func webview2DataDir() (string, error) {
	if override := os.Getenv("SLACKAUTH_WEBVIEW_DATA_DIR"); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir() // %APPDATA% on Windows
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "slackauth", "webview2"), nil
}

// readSlackDCookieWebView2 reads the `d` cookie out of the WebView2 cookie
// store. WebView2 stores cookies the same way Chrome does: SQLite at
// <DataPath>/EBWebView/Default/Network/Cookies, with encrypted values keyed
// off the os_crypt.encrypted_key from Local State (DPAPI-wrapped on Windows).
//
// We deliberately skip Chrome 127+ app-bound (`v20`) cookies because
// decrypting them requires elevation / impersonating the Edge process.
// WebView2 doesn't currently roll out app-bound encryption AFAIK, so this
// path should be sufficient.
func readSlackDCookieWebView2(dataDir string) (string, error) {
	root := filepath.Join(dataDir, "EBWebView", "Default")
	cookiesPath := filepath.Join(root, "Network", "Cookies")
	if _, err := os.Stat(cookiesPath); err != nil {
		cookiesPath = filepath.Join(root, "Cookies")
	}

	keyBytes, err := webview2EncryptionKey(filepath.Join(dataDir, "EBWebView", "Local State"))
	if err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "slackauth-cookies-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	dst := filepath.Join(tmpDir, "Cookies")
	if err := copyFileWindows(cookiesPath, dst); err != nil {
		return "", err
	}

	out, err := exec.Command("sqlite3", dst,
		"SELECT hex(encrypted_value) FROM cookies WHERE host_key LIKE '%.slack.com' AND name = 'd' LIMIT 1;",
	).Output()
	if err != nil {
		return "", err
	}
	hexStr := strings.TrimSpace(string(out))
	if hexStr == "" {
		return "", errors.New("d cookie not present in WebView2 store")
	}
	encrypted, err := hex.DecodeString(hexStr)
	if err != nil || len(encrypted) < 4 {
		return "", errors.New("d cookie blob malformed")
	}

	switch string(encrypted[:3]) {
	case "v10":
		return decryptCookieAESGCM(encrypted[3:], keyBytes)
	case "v20":
		return "", errors.New("d cookie uses Chrome's app-bound encryption (v20); not supported")
	default:
		return "", fmt.Errorf("unknown cookie encryption version %q", string(encrypted[:3]))
	}
}

// webview2EncryptionKey reads <UserDataFolder>/EBWebView/Local State,
// extracts os_crypt.encrypted_key (base64), strips the "DPAPI" prefix, and
// unprotects it via the Windows DPAPI to get the 32-byte AES-256 key
// WebView2 used to encrypt cookies.
func webview2EncryptionKey(localStatePath string) ([]byte, error) {
	data, err := os.ReadFile(localStatePath)
	if err != nil {
		return nil, fmt.Errorf("read Local State: %w", err)
	}
	var ls struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, fmt.Errorf("parse Local State: %w", err)
	}
	if ls.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("Local State has no os_crypt.encrypted_key")
	}
	wrapped, err := base64.StdEncoding.DecodeString(ls.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted_key: %w", err)
	}
	if !strings.HasPrefix(string(wrapped), "DPAPI") {
		return nil, errors.New("encrypted_key missing DPAPI prefix")
	}
	return dpapiUnprotect(wrapped[len("DPAPI"):])
}

// decryptCookieAESGCM decodes a Chrome `v10`-format cookie body using the
// WebView2 AES-256 key. Layout after the v10 prefix is:
//
//	[12-byte nonce][ciphertext][16-byte GCM tag]
func decryptCookieAESGCM(body, key []byte) (string, error) {
	if len(body) < 12+16 {
		return "", errors.New("cookie body too short")
	}
	nonce := body[:12]
	ct := body[12:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("AES-GCM decrypt: %w", err)
	}
	val := string(plain)
	if !strings.HasPrefix(val, "xoxd-") && !strings.HasPrefix(val, "xoxs-") {
		return "", fmt.Errorf("decrypted cookie has unexpected prefix")
	}
	return val, nil
}

// dpapiUnprotect calls CryptUnprotectData via golang.org/x/sys/windows.
func dpapiUnprotect(in []byte) ([]byte, error) {
	if len(in) == 0 {
		return nil, errors.New("empty DPAPI blob")
	}
	var input windows.DataBlob
	input.Size = uint32(len(in))
	input.Data = &in[0]
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, 0, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	plain := make([]byte, output.Size)
	copy(plain, unsafe.Slice(output.Data, output.Size))
	return plain, nil
}

// copyFileWindows: small helper local to this file so we don't pull in
// extract_chrome.go's copyFile (different build tag).
func copyFileWindows(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o600)
}
