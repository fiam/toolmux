//go:build darwin

package slackauth

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#include <stdlib.h>
#include "webview_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"strings"
	"time"
	"unsafe"
)

// runWebViewHelper owns the OS main thread (locked by helper_init.init), opens
// a WKWebView, waits for the user to sign in, and returns the team list +
// `d` cookie. All progress is emitted via helperEmit so the parent process
// can route it to the caller's OnEvent — the helper never prints raw text.
func runWebViewHelper(in helperInput) helperOutput {
	url := startURL(in.WorkspaceDomain)
	helperEmit(Event{Kind: EventLaunching, Detail: "WKWebView", URL: url})

	title := C.CString("Sign in to Slack — slackauth")
	defer C.free(unsafe.Pointer(title))
	handle := C.sa_webview_create(title)
	if handle == nil {
		return helperOutput{Error: "could not create WebView"}
	}
	defer C.sa_webview_destroy(handle)

	// Install the fetch/XHR/WebSocket hook before the first navigation so it
	// runs at document-start and catches Slack's first API call.
	hook := C.CString(jsHookRequests)
	C.sa_webview_add_user_script(handle, hook)
	C.free(unsafe.Pointer(hook))

	curl := C.CString(url)
	C.sa_webview_navigate(handle, curl)
	C.free(unsafe.Pointer(curl))

	helperEmit(Event{Kind: EventWaiting, Detail: "Slack sign-in in the WebView window"})

	ctx := context.Background()
	if in.TimeoutNS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(in.TimeoutNS))
		defer cancel()
	}

	teams, err := waitForTeamsWebView(ctx, handle)
	if err != nil {
		return helperOutput{Error: err.Error()}
	}

	cookie, err := readDCookieWebView(handle)
	if err != nil {
		return helperOutput{Error: err.Error()}
	}
	return helperOutput{Teams: teams, Cookie: cookie}
}

// waitForTeamsWebView interleaves Cocoa event pumping with JS polling. We
// pump in ~250ms chunks so the UI stays responsive while the user is signing
// in, and eval the team-extraction JS every ~1.5s to detect when localStorage
// is finally populated.
//
// localConfig_v2 (the source of xoxc tokens) only lives on app.slack.com.
// Slack's sign-in flow sometimes lands the user on <workspace>.slack.com
// instead, where we'd poll forever. Once we see the auth cookie, we bounce
// the WebView to app.slack.com so the modern web client mounts and writes
// its localStorage.
func waitForTeamsWebView(ctx context.Context, handle C.sa_webview_t) ([]teamWithToken, error) {
	lastPoll := time.Now().Add(-time.Hour)
	bounced := false
	start := time.Now()
	debugged := false
	for {
		if status := C.sa_webview_pump(handle, 250); status == 0 {
			return nil, errors.New("window closed before sign-in completed")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Since(lastPoll) < 1500*time.Millisecond {
			continue
		}
		lastPoll = time.Now()

		teams, err := evalTeamsWebView(handle)
		if err == nil && len(teams) > 0 {
			return teams, nil
		}

		if !bounced && isAuthenticatedWebView(handle) && !onAppSlackComWebView(handle) {
			navigateWebView(handle, "https://app.slack.com/")
			bounced = true
			helperEmit(Event{Kind: EventInfo, Detail: "session detected; bounced to app.slack.com"})
		}

		// After ~10s with nothing found, dump what we *can* see so we can
		// tell whether the page just isn't on Slack yet or whether Slack
		// moved the data we read.
		if !debugged && time.Since(start) > 10*time.Second {
			debugged = true
			emitWebViewDebug(handle)
		}
	}
}

func emitWebViewDebug(handle C.sa_webview_t) {
	js := C.CString(jsDebugInfo)
	defer C.free(unsafe.Pointer(js))
	var cErr *C.char
	cRes := C.sa_webview_eval(handle, js, &cErr)
	if cRes == nil {
		if cErr != nil {
			helperEmit(Event{Kind: EventInfo, Detail: "debug eval failed: " + C.GoString(cErr)})
			C.free(unsafe.Pointer(cErr))
		}
		return
	}
	defer C.free(unsafe.Pointer(cRes))
	helperEmit(Event{Kind: EventInfo, Detail: "still polling — " + C.GoString(cRes)})
}

func currentURLWebView(handle C.sa_webview_t) string {
	cRes := C.sa_webview_url(handle)
	if cRes == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cRes))
	return C.GoString(cRes)
}

func onAppSlackComWebView(handle C.sa_webview_t) bool {
	return strings.HasPrefix(currentURLWebView(handle), "https://app.slack.com/")
}

func isAuthenticatedWebView(handle C.sa_webview_t) bool {
	v, err := readDCookieWebView(handle)
	return err == nil && v != ""
}

func navigateWebView(handle C.sa_webview_t, url string) {
	curl := C.CString(url)
	defer C.free(unsafe.Pointer(curl))
	C.sa_webview_navigate(handle, curl)
}

func evalTeamsWebView(handle C.sa_webview_t) ([]teamWithToken, error) {
	js := C.CString(jsExtractTeams)
	defer C.free(unsafe.Pointer(js))

	var cErr *C.char
	cRes := C.sa_webview_eval(handle, js, &cErr)
	if cRes == nil {
		if cErr != nil {
			msg := C.GoString(cErr)
			C.free(unsafe.Pointer(cErr))
			return nil, errors.New(msg)
		}
		return nil, nil
	}
	res := C.GoString(cRes)
	C.free(unsafe.Pointer(cRes))
	return decodeTeamsJSON(res)
}

func readDCookieWebView(handle C.sa_webview_t) (string, error) {
	name := C.CString("d")
	defer C.free(unsafe.Pointer(name))
	suffix := C.CString("slack.com")
	defer C.free(unsafe.Pointer(suffix))

	cRes := C.sa_webview_cookie(handle, name, suffix)
	if cRes == nil {
		return "", errors.New("`d` cookie not found — make sure you completed Slack sign-in")
	}
	val := C.GoString(cRes)
	C.free(unsafe.Pointer(cRes))
	return val, nil
}
