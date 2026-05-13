// Built only on darwin via the cgo directives in webview_darwin.go.

#import <Cocoa/Cocoa.h>
#import <Foundation/Foundation.h>
#import <WebKit/WebKit.h>

#include "webview_darwin.h"
#include <stdlib.h>
#include <string.h>

static NSString *sa_safari_user_agent(void);

@interface SAWebView : NSObject <WKNavigationDelegate, WKUIDelegate, NSWindowDelegate>
@property (nonatomic, strong) NSWindow *window;
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic) BOOL closed;
@end

static void sa_install_main_menu(void);

@implementation SAWebView

- (instancetype)initWithTitle:(NSString *)title {
	self = [super init];
	if (!self) return nil;

	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
	// Without a main menu, the responder chain has no Edit items so Cmd-C /
	// Cmd-V / Cmd-A do nothing. Install a minimal one so standard text-field
	// shortcuts work inside the WKWebView.
	sa_install_main_menu();

	NSRect frame = NSMakeRect(0, 0, 1100, 800);
	self.window = [[NSWindow alloc] initWithContentRect:frame
	                                          styleMask:(NSWindowStyleMaskTitled |
	                                                     NSWindowStyleMaskClosable |
	                                                     NSWindowStyleMaskResizable |
	                                                     NSWindowStyleMaskMiniaturizable)
	                                            backing:NSBackingStoreBuffered
	                                              defer:NO];
	self.window.title = title.length > 0 ? title : @"slackauth";
	self.window.delegate = self;
	[self.window center];

	WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
	// Default data store is persistent and scoped to this binary, so the user
	// stays signed in across runs without leaking into Safari.
	config.websiteDataStore = WKWebsiteDataStore.defaultDataStore;

	self.webView = [[WKWebView alloc] initWithFrame:frame configuration:config];
	self.webView.navigationDelegate = self;
	self.webView.UIDelegate = self;
	// Enable Web Inspector — right-click → Inspect Element works, and Safari's
	// Develop menu can also attach (Develop → <Mac name> → page title).
	if (@available(macOS 13.3, *)) {
		self.webView.inspectable = YES;
	}
	// WKWebView's default UA lacks the `Version/X Safari/Y` tokens that real
	// Safari emits, and Slack's signin page rejects it as "no longer supported".
	// We mirror the UA real Safari would send on this machine — see
	// sa_safari_user_agent for why we only need to read the Safari version.
	self.webView.customUserAgent = sa_safari_user_agent();
	self.window.contentView = self.webView;

	[self.window makeKeyAndOrderFront:nil];
	[NSApp activateIgnoringOtherApps:YES];
	return self;
}

- (void)windowWillClose:(NSNotification *)note {
	self.closed = YES;
}

// Slack uses target=_blank for some SSO popups; route those into the same view
// rather than dropping the navigation on the floor.
- (WKWebView *)webView:(WKWebView *)webView
    createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
               forNavigationAction:(WKNavigationAction *)action
                    windowFeatures:(WKWindowFeatures *)windowFeatures {
	if (!action.targetFrame.isMainFrame) {
		[webView loadRequest:action.request];
	}
	return nil;
}

@end

static void sa_install_main_menu(void) {
	if (NSApp.mainMenu != nil) return;

	NSString *appName = [NSProcessInfo processInfo].processName;
	NSMenu *menubar = [[NSMenu alloc] init];

	// Application menu — Hide / Hide Others / Quit. macOS conventionally
	// renders the first menu item's title in bold as the app name.
	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	[menubar addItem:appItem];
	NSMenu *appMenu = [[NSMenu alloc] init];
	[appMenu addItemWithTitle:[NSString stringWithFormat:@"Hide %@", appName]
	                   action:@selector(hide:)
	            keyEquivalent:@"h"];
	NSMenuItem *hideOthers = [appMenu addItemWithTitle:@"Hide Others"
	                                            action:@selector(hideOtherApplications:)
	                                     keyEquivalent:@"h"];
	hideOthers.keyEquivalentModifierMask = NSEventModifierFlagOption | NSEventModifierFlagCommand;
	[appMenu addItemWithTitle:@"Show All"
	                   action:@selector(unhideAllApplications:)
	            keyEquivalent:@""];
	[appMenu addItem:[NSMenuItem separatorItem]];
	[appMenu addItemWithTitle:[NSString stringWithFormat:@"Quit %@", appName]
	                   action:@selector(terminate:)
	            keyEquivalent:@"q"];
	[appItem setSubmenu:appMenu];

	// Edit menu — Undo / Redo / Cut / Copy / Paste / Select All / Find.
	// AppKit's first-responder chain routes these to the focused WKWebView,
	// which handles them via JavaScript's exec-command equivalents.
	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	[menubar addItem:editItem];
	NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"Edit"];
	[editMenu addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
	NSMenuItem *redo = [editMenu addItemWithTitle:@"Redo"
	                                       action:@selector(redo:)
	                                keyEquivalent:@"z"];
	redo.keyEquivalentModifierMask = NSEventModifierFlagShift | NSEventModifierFlagCommand;
	[editMenu addItem:[NSMenuItem separatorItem]];
	[editMenu addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
	[editMenu addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
	[editMenu addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
	NSMenuItem *pasteMatch = [editMenu addItemWithTitle:@"Paste and Match Style"
	                                             action:@selector(pasteAsPlainText:)
	                                      keyEquivalent:@"V"];
	pasteMatch.keyEquivalentModifierMask = NSEventModifierFlagOption |
	                                       NSEventModifierFlagShift |
	                                       NSEventModifierFlagCommand;
	[editMenu addItemWithTitle:@"Delete" action:@selector(delete:) keyEquivalent:@""];
	[editMenu addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
	[editMenu addItem:[NSMenuItem separatorItem]];
	[editMenu addItemWithTitle:@"Find…"
	                    action:@selector(performTextFinderAction:)
	             keyEquivalent:@"f"];
	[editItem setSubmenu:editMenu];

	NSApp.mainMenu = menubar;
}

static inline SAWebView *sa_unwrap(sa_webview_t handle) {
	return (__bridge SAWebView *)handle;
}

// sa_safari_user_agent builds a Safari UA string that mirrors what real
// Safari sends on this machine. Apple intentionally freezes the OS X token
// at 10_15_7 and the WebKit tokens at 605.1.15 inside Safari's UA, no matter
// the actual macOS or WebKit version, so the only piece we need to keep in
// sync is `Version/<safari-app-version>` — read straight from the installed
// Safari bundle. Falls back to a recent stable if Safari isn't present.
static NSString *sa_safari_user_agent(void) {
	NSString *safariVersion = nil;
	NSString *plistPath = @"/Applications/Safari.app/Contents/Info.plist";
	NSDictionary *info = [NSDictionary dictionaryWithContentsOfFile:plistPath];
	NSString *v = info[@"CFBundleShortVersionString"];
	if (v.length > 0) {
		safariVersion = v;
	} else {
		safariVersion = @"18.0";
	}
	return [NSString stringWithFormat:
	        @"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
	        @"AppleWebKit/605.1.15 (KHTML, like Gecko) "
	        @"Version/%@ Safari/605.1.15",
	        safariVersion];
}

static char *sa_strdup_ns(NSString *s) {
	if (!s) return NULL;
	const char *u = s.UTF8String;
	if (!u) return NULL;
	return strdup(u);
}

// sa_json_string serializes the WKWebView evaluateJavaScript result so it can
// cross the cgo boundary as a JSON-encoded NSString. If JS already handed us
// a string (the typical pattern — JS returns JSON.stringify(...)), we pass it
// through verbatim; otherwise NSJSONSerialization handles the bridge.
static NSString *sa_json_string(id obj) {
	if (!obj || obj == [NSNull null]) return @"null";
	if ([obj isKindOfClass:[NSString class]]) return obj;
	NSError *err = nil;
	NSData *data = [NSJSONSerialization dataWithJSONObject:obj
	                                               options:NSJSONWritingFragmentsAllowed
	                                                 error:&err];
	if (!data) return nil;
	return [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
}

sa_webview_t sa_webview_create(const char *title) {
	@autoreleasepool {
		NSString *tt = title ? [NSString stringWithUTF8String:title] : nil;
		SAWebView *w = [[SAWebView alloc] initWithTitle:tt];
		return (__bridge_retained sa_webview_t)w;
	}
}

void sa_webview_destroy(sa_webview_t handle) {
	if (!handle) return;
	@autoreleasepool {
		SAWebView *w = (__bridge_transfer SAWebView *)handle;
		[w.window close];
		(void)w;
	}
}

void sa_webview_navigate(sa_webview_t handle, const char *url) {
	if (!handle || !url) return;
	@autoreleasepool {
		SAWebView *w = sa_unwrap(handle);
		NSURL *u = [NSURL URLWithString:[NSString stringWithUTF8String:url]];
		if (!u) return;
		[w.webView loadRequest:[NSURLRequest requestWithURL:u]];
	}
}

int sa_webview_pump(sa_webview_t handle, int timeout_ms) {
	@autoreleasepool {
		SAWebView *w = sa_unwrap(handle);
		if (!w || w.closed) return 0;
		NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:timeout_ms / 1000.0];
		while (!w.closed) {
			NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny
			                                    untilDate:deadline
			                                       inMode:NSDefaultRunLoopMode
			                                      dequeue:YES];
			if (!event) break;
			[NSApp sendEvent:event];
		}
		return w.closed ? 0 : 1;
	}
}

char *sa_webview_eval(sa_webview_t handle, const char *script, char **err_out) {
	if (err_out) *err_out = NULL;
	@autoreleasepool {
		SAWebView *w = sa_unwrap(handle);
		if (!w || !script) {
			if (err_out) *err_out = strdup("invalid handle or script");
			return NULL;
		}
		NSString *js = [NSString stringWithUTF8String:script];
		__block BOOL done = NO;
		__block NSString *resultJSON = nil;
		__block NSString *errMessage = nil;

		// callAsyncJavaScript: treats the script as the body of an async
		// function and awaits Promises explicitly — far less finicky than
		// evaluateJavaScript:'s Promise-bridging path.
		[w.webView callAsyncJavaScript:js
		                     arguments:nil
		                       inFrame:nil
		                inContentWorld:[WKContentWorld pageWorld]
		             completionHandler:^(id obj, NSError *error) {
			if (error) {
				errMessage = error.localizedDescription;
			} else {
				resultJSON = sa_json_string(obj);
				if (!resultJSON) errMessage = @"JS result not JSON-serializable";
			}
			done = YES;
		}];

		while (!done) {
			if (w.closed) {
				if (err_out) *err_out = strdup("window closed");
				return NULL;
			}
			NSDate *until = [NSDate dateWithTimeIntervalSinceNow:0.05];
			NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny
			                                    untilDate:until
			                                       inMode:NSDefaultRunLoopMode
			                                      dequeue:YES];
			if (event) [NSApp sendEvent:event];
		}
		if (errMessage) {
			if (err_out) *err_out = sa_strdup_ns(errMessage);
			return NULL;
		}
		return sa_strdup_ns(resultJSON);
	}
}

void sa_webview_add_user_script(sa_webview_t handle, const char *script) {
	if (!handle || !script) return;
	@autoreleasepool {
		SAWebView *w = sa_unwrap(handle);
		if (!w) return;
		NSString *src = [NSString stringWithUTF8String:script];
		WKUserScript *us = [[WKUserScript alloc] initWithSource:src
		                                          injectionTime:WKUserScriptInjectionTimeAtDocumentStart
		                                       forMainFrameOnly:NO
		                                         inContentWorld:[WKContentWorld pageWorld]];
		[w.webView.configuration.userContentController addUserScript:us];
	}
}

char *sa_webview_url(sa_webview_t handle) {
	@autoreleasepool {
		SAWebView *w = sa_unwrap(handle);
		if (!w) return NULL;
		return sa_strdup_ns(w.webView.URL.absoluteString);
	}
}

char *sa_webview_cookie(sa_webview_t handle, const char *name, const char *domain_suffix) {
	@autoreleasepool {
		SAWebView *w = sa_unwrap(handle);
		if (!w || !name) return NULL;
		NSString *targetName = [NSString stringWithUTF8String:name];
		NSString *suffix = domain_suffix ? [NSString stringWithUTF8String:domain_suffix] : @"";

		__block BOOL done = NO;
		__block NSString *value = nil;

		WKHTTPCookieStore *store = w.webView.configuration.websiteDataStore.httpCookieStore;
		[store getAllCookies:^(NSArray<NSHTTPCookie *> *cookies) {
			for (NSHTTPCookie *c in cookies) {
				if ([c.name isEqualToString:targetName] &&
				    (suffix.length == 0 || [c.domain hasSuffix:suffix])) {
					value = c.value;
					break;
				}
			}
			done = YES;
		}];

		while (!done) {
			if (w.closed) return NULL;
			NSDate *until = [NSDate dateWithTimeIntervalSinceNow:0.05];
			NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny
			                                    untilDate:until
			                                       inMode:NSDefaultRunLoopMode
			                                      dequeue:YES];
			if (event) [NSApp sendEvent:event];
		}
		return sa_strdup_ns(value);
	}
}
