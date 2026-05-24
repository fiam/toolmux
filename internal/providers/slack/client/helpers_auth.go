package slack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
)

func secretFromInvocation(exec actions.Context, inv actions.Invocation, name string) (string, error) {
	if value := strings.TrimSpace(inv.String(name)); value != "" {
		return value, nil
	}
	if envName := strings.TrimSpace(inv.String(name + "-env")); envName != "" {
		return strings.TrimSpace(os.Getenv(envName)), nil
	}
	if fileName := strings.TrimSpace(inv.String(name + "-file")); fileName != "" {
		readFile := exec.ReadFile
		if readFile == nil {
			readFile = os.ReadFile
		}
		data, err := readFile(fileName)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", nil
}

func normalizeSlackCookieHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "=") {
		return value
	}
	return "d=" + value
}

func account(inv actions.Invocation) string {
	value := strings.TrimSpace(inv.String("account"))
	if value == "" {
		return defaultAccount
	}
	return value
}

func timeout(inv actions.Invocation) time.Duration {
	seconds := inv.Int("timeout-seconds")
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

type oauthCallback struct {
	redirectURI string
	server      *http.Server
	results     <-chan oauthCallbackResult
}

type oauthCallbackResult struct {
	Code string
	Err  error
}

func startOAuthCallback(port int, state string) (oauthCallback, error) {
	if port < 0 || port > 65535 {
		return oauthCallback{}, fmt.Errorf("redirect port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return oauthCallback{}, err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return oauthCallback{}, fmt.Errorf("oauth callback listener did not return a TCP address")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", tcpAddr.Port, callbackPath)
	results := make(chan oauthCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		result := oauthCallbackResult{}
		switch {
		case query.Get("state") != state:
			result.Err = fmt.Errorf("slack OAuth callback state mismatch")
		case query.Get("error") != "":
			result.Err = fmt.Errorf("slack OAuth callback error: %s", query.Get("error"))
		case query.Get("code") == "":
			result.Err = fmt.Errorf("slack OAuth callback did not include a code")
		default:
			result.Code = query.Get("code")
		}
		writeCallbackPage(w, result.Err)
		select {
		case results <- result:
		default:
		}
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case results <- oauthCallbackResult{Err: err}:
			default:
			}
		}
	}()
	return oauthCallback{
		redirectURI: redirectURI,
		server:      server,
		results:     results,
	}, nil
}

func (callback oauthCallback) wait(ctx context.Context, timeout time.Duration) (oauthCallbackResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case result := <-callback.results:
		return result, nil
	case <-ctx.Done():
		return oauthCallbackResult{}, fmt.Errorf("timed out waiting for Slack OAuth callback: %w", ctx.Err())
	}
}

func (callback oauthCallback) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = callback.server.Shutdown(ctx)
}

func writeCallbackPage(w http.ResponseWriter, callbackErr error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if callbackErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "<!doctype html><title>Slack OAuth failed</title><p>Slack OAuth failed. Return to Toolmux.</p>")
		return
	}
	_, _ = io.WriteString(w, "<!doctype html><title>Slack OAuth complete</title><p>Slack OAuth complete. Return to Toolmux.</p>")
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
