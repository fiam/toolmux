#ifndef SLACKAUTH_WEBVIEW_DARWIN_H
#define SLACKAUTH_WEBVIEW_DARWIN_H

// Opaque handle to a WebView session. Returned by sa_webview_create.
typedef void *sa_webview_t;

// Create the NSApp + NSWindow + WKWebView. Must be called on the main thread.
// title may be NULL.
sa_webview_t sa_webview_create(const char *title);

// Destroy the WebView session and release Cocoa resources.
void sa_webview_destroy(sa_webview_t w);

// Navigate to a URL. URL must be a valid absolute URL.
void sa_webview_navigate(sa_webview_t w, const char *url);

// Pump Cocoa events for up to timeout_ms milliseconds, or until the window is
// closed by the user. Returns 0 if the window is closed, 1 if still open.
int sa_webview_pump(sa_webview_t w, int timeout_ms);

// Evaluate JS in the current document and return the JSON-serialized result.
// Blocks pumping events until the WKWebView completion handler fires. Returns
// a malloc'd C string (caller frees) on success, or NULL on error — in which
// case *err_out is set to a malloc'd error message (caller frees).
char *sa_webview_eval(sa_webview_t w, const char *script, char **err_out);

// Read a cookie by name with a domain suffix filter. Blocks until the
// WKHTTPCookieStore completion handler fires. Returns malloc'd value (caller
// frees), or NULL if not found.
char *sa_webview_cookie(sa_webview_t w, const char *name, const char *domain_suffix);

// Read the current top-frame URL. Returns malloc'd string (caller frees), or
// NULL if the view has no URL yet.
char *sa_webview_url(sa_webview_t w);

// Install a user script that runs at document-start on every navigation in
// the main and sub frames. Used to monkey-patch fetch/XHR/WebSocket so
// Slack's token gets captured before app code runs.
void sa_webview_add_user_script(sa_webview_t w, const char *script);

#endif
