package slackauth

// AvailableEngines returns the engines that can run on this host, in priority
// order (most preferred first). Callers can inspect this to surface a
// platform-aware "what's installed" list to the user.
func AvailableEngines() []Engine {
	all := []Engine{EngineWebView, EngineChrome}
	out := make([]Engine, 0, len(all))
	for _, e := range all {
		if EngineAvailable(e) {
			out = append(out, e)
		}
	}
	return out
}

// EngineAvailable reports whether the given engine can be selected on this
// host — its dependencies (Chrome install, system WebView runtime, etc.) are
// present and usable. Note this is a *can it start* check, not a guarantee
// the extraction will succeed.
func EngineAvailable(e Engine) bool {
	switch e {
	case EngineWebView:
		return webviewAvailable()
	case EngineChrome:
		return chromeAvailable()
	}
	return false
}

// defaultEngine picks the engine for this host. We prefer EngineWebView when
// available: it's predictable (a fresh sign-in window), doesn't require an
// existing browser install, and doesn't depend on the user already being
// signed in to Slack somewhere else. EngineChrome is the fallback on
// platforms where webview isn't implemented (Linux). Returns "" if no
// engine works here — Extract surfaces that as a clear error.
func defaultEngine() Engine {
	for _, e := range []Engine{EngineWebView, EngineChrome} {
		if EngineAvailable(e) {
			return e
		}
	}
	return ""
}
