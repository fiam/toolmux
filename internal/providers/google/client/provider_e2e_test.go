package client_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers"
	_ "github.com/fiam/toolmux/internal/providers/google/broker"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/testutil/toolmuxtest"
)

func TestGoogleBrokerOAuthDriveFlow(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	deps := googleBrokerDeps(t, store, upstream)
	deps.OpenBrowser = followURL(deps.HTTPClient)

	result := toolmuxtest.RunResult(t, deps, "add", "google", "--auth", "token", "--token", "ya29.direct")
	if result.Err == nil {
		t.Fatalf("expected direct Google token auth to fail, got output:\n%s", result.Output)
	}
	if !strings.Contains(result.Err.Error(), "only supports brokered OAuth") {
		t.Fatalf("expected broker-only auth error, got %v", result.Err)
	}

	out := toolmuxtest.Run(t, deps, "add", "google", "--timeout-seconds", "5")
	toolmuxtest.AssertContains(t, out, "added google using Google brokered OAuth")
	upstream.assertAuthorization(t, []string{googleapi.ScopeDriveFile})

	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "default"}
	tokens, err := store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken != "refresh-google" {
		t.Fatalf("expected first Google grant refresh token to be stored, got %q", tokens.RefreshToken)
	}
	if tokens.Extra["auth_type"] != "oauth_broker" || tokens.Extra["broker_url"] == "" {
		t.Fatalf("expected broker metadata to be stored, got %#v", tokens.Extra)
	}
	if !hasScopes(tokens.Scopes, googleapi.ScopeDriveFile) || hasScopes(tokens.Scopes, googleapi.ScopeDriveMetadata) {
		t.Fatalf("expected only non-sensitive drive.file scope after add, got %#v", tokens.Scopes)
	}

	out = toolmuxtest.Run(t, deps, "google", "drive", "search", "--query", "mimeType='application/vnd.google-apps.document'")
	toolmuxtest.AssertContains(t, out, "doc-1")
	toolmuxtest.AssertContains(t, out, "Shared plan")

	out = toolmuxtest.Run(t, deps, "add", "google", "--timeout-seconds", "5")
	toolmuxtest.AssertContains(t, out, "Google already has the requested Google OAuth scopes")

	out = toolmuxtest.Run(t, deps, "status", "google")
	for _, want := range []string{"google", "native", "connected", "brokered-oauth", "8"} {
		toolmuxtest.AssertContains(t, out, want)
	}

	out = toolmuxtest.Run(t, deps, "list", "--internal")
	for _, want := range []string{"google", "internal", "connected", "8"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	if strings.Contains(out, "built-in") {
		t.Fatalf("expected internal catalog scope to omit built-in, got:\n%s", out)
	}

	result = toolmuxtest.RunResult(t, deps, "add", "google", "--scope", googleapi.ScopeDriveMetadata, "--timeout-seconds", "5")
	if result.Err == nil {
		t.Fatalf("expected broader Google OAuth scope to fail, got output:\n%s", result.Output)
	}
	if !strings.Contains(result.Err.Error(), "only supports "+googleapi.ScopeDriveFile) {
		t.Fatalf("expected drive.file-only error, got %v", result.Err)
	}

	tokens, err = store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken != "refresh-google" {
		t.Fatalf("expected rejected broader grant to preserve refresh token, got %q", tokens.RefreshToken)
	}

	tokens.ExpiresAt = time.Now().Add(-time.Minute)
	tokens.AccessToken = "expired-token"
	if err := store.SaveOAuthTokens(context.Background(), ref, tokens); err != nil {
		t.Fatal(err)
	}
	out = toolmuxtest.Run(t, deps, "google", "drive", "search")
	toolmuxtest.AssertContains(t, out, "Shared plan")
	upstream.assertRefreshAndDriveToken(t, "ya29.refreshed")

	tokens, err = store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken != "refresh-google" {
		t.Fatalf("expected refresh to preserve Google refresh token, got %q", tokens.RefreshToken)
	}
	if !hasScopes(tokens.Scopes, googleapi.ScopeDriveFile) || hasScopes(tokens.Scopes, googleapi.ScopeDriveMetadata) {
		t.Fatalf("expected refresh to preserve stored scopes, got %#v", tokens.Scopes)
	}
}

func TestGoogleDriveReportsMissingScopeAfterDocsSensitiveOverride(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	deps := googleDeps(store, upstream.Server.Client(), upstream.Server.URL)
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "default"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken:  "ya29.docs",
		RefreshToken: "refresh-google",
		TokenType:    "Bearer",
		Scopes:       []string{googleapi.ScopeDocs},
		Extra:        map[string]string{"auth_type": "oauth_broker"},
	}); err != nil {
		t.Fatal(err)
	}

	result := toolmuxtest.RunResult(t, deps, "google", "drive", "search")
	if result.Err == nil {
		t.Fatalf("expected drive command to fail before drive.file is granted, output:\n%s", result.Output)
	}
	if !strings.Contains(result.Err.Error(), "missing Google OAuth scope") {
		t.Fatalf("expected missing scope error, got %v", result.Err)
	}

	out := toolmuxtest.Run(t, deps, "status", "google")
	toolmuxtest.AssertContains(t, out, "missing-scopes")
}

func TestGoogleDrivePickUsesBrokeredPickerByDefault(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	deps := googleBrokerDeps(t, store, upstream)
	deps.OpenBrowser = brokeredPickerSelectionBrowser(t, deps.HTTPClient)

	out := toolmuxtest.Run(t, deps, "google", "drive", "pick")
	toolmuxtest.AssertContains(t, out, "doc-1")
	toolmuxtest.AssertContains(t, out, "Shared plan")
	upstream.assertPickerMIME(t, "")

	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "default"}
	tokens, err := store.LoadOAuthTokens(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "ya29.picker" {
		t.Fatalf("expected brokered Picker access token to be stored, got %q", tokens.AccessToken)
	}
	if got := tokens.Extra["auth_type"]; got != "oauth_broker" {
		t.Fatalf("expected brokered Picker auth type, got %q", got)
	}
}

func TestGoogleDriveSelectedAddUsesBrokeredPicker(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	deps := googleBrokerDeps(t, store, upstream)
	deps.OpenBrowser = brokeredPickerSelectionBrowser(t, deps.HTTPClient)

	out := toolmuxtest.Run(t, deps, "google", "drive", "selected", "add", "--timeout-seconds", "5")
	toolmuxtest.AssertContains(t, out, "doc-1")
	toolmuxtest.AssertContains(t, out, "Shared plan")
	upstream.assertPickerMIME(t, "")

	out = toolmuxtest.Run(t, deps, "google", "drive", "selected", "list")
	toolmuxtest.AssertContains(t, out, "doc-1")

	out = toolmuxtest.Run(t, deps, "google", "drive", "selected", "remove", "doc-1")
	toolmuxtest.AssertContains(t, out, "removed Google file doc-1")

	out = toolmuxtest.Run(t, deps, "google", "drive", "selected", "list")
	toolmuxtest.AssertContains(t, out, "no files saved")

	out = toolmuxtest.Run(t, deps, "google", "drive", "available")
	toolmuxtest.AssertContains(t, out, "Shared plan")
	upstream.assertDriveToken(t, "ya29.picker")
}

func TestGoogleDriveFilesCopyCopiesAccessibleFile(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "default"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken:  "ya29.drive",
		RefreshToken: "refresh-google",
		TokenType:    "Bearer",
		Scopes:       []string{googleapi.ScopeDriveFile},
		Extra:        map[string]string{"auth_type": "oauth_broker"},
	}); err != nil {
		t.Fatal(err)
	}
	deps := googleDeps(store, upstream.Server.Client(), upstream.Server.URL)

	out := toolmuxtest.Run(t, deps, "google", "drive", "files", "copy", "https://docs.google.com/document/d/doc-1/edit", "--name", "Copied plan")
	toolmuxtest.AssertContains(t, out, "doc-copy")
	toolmuxtest.AssertContains(t, out, "Copied plan")
	upstream.assertCopyRequest(t, "doc-1", "Copied plan", "root")

	out = toolmuxtest.Run(t, deps, "--output", "json", "google", "drive", "files", "copy", "--file", "doc-1", "--parent-id", "folder-1", "--dry-run")
	toolmuxtest.AssertContains(t, out, "google.drive.files.copy")
	toolmuxtest.AssertContains(t, out, "folder-1")
}

func TestGoogleDriveCommandsExposeMCPTools(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, spec := range providers.CommandSpecs() {
		seen[spec.ID] = true
	}
	for _, want := range []string{
		"google.drive.selected.add",
		"google.drive.selected.list",
		"google.drive.files.copy",
		"google.drive.selected.remove",
		"google.drive.available",
	} {
		if !seen[want] {
			t.Fatalf("missing Google MCP tool command %s", want)
		}
	}
	if seen["google.configure.files.add"] {
		t.Fatal("Google configure command should not be exposed as an MCP tool")
	}
	if seen["google.drive.files.list"] {
		t.Fatal("google.drive.files.list should remain reserved for future Drive files.list support")
	}
	if seen["google.drive.accessible"] {
		t.Fatal("google.drive.accessible should remain a CLI alias, not an MCP tool")
	}
}
