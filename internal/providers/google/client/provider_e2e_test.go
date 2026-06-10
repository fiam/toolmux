package client_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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

	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
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
	for _, want := range []string{"google", "native", "connected", "brokered-oauth", "22"} {
		toolmuxtest.AssertContains(t, out, want)
	}

	out = toolmuxtest.Run(t, deps, "list", "--internal")
	for _, want := range []string{"google", "internal", "connected", "22"} {
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

func TestGoogleDocsCommandsReadAndUpdateAccessibleDocument(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken:  "ya29.drive",
		RefreshToken: "refresh-google",
		TokenType:    "Bearer",
		Scopes:       []string{googleapi.ScopeDriveFile},
		Extra:        map[string]string{"auth_type": "oauth_broker"},
	}); err != nil {
		t.Fatal(err)
	}
	deps := googleDeps(t, store, upstream.Server.Client(), upstream.Server.URL)

	out := toolmuxtest.Run(t, deps, "google", "docs", "get", "--document-id", "https://docs.google.com/document/d/doc-1/edit")
	toolmuxtest.AssertContains(t, out, "Hello world")
	toolmuxtest.AssertContains(t, out, "rev-1")

	out = toolmuxtest.Run(t, deps, "google", "docs", "append", "doc-1", "--text", "Added by Toolmux\n")
	toolmuxtest.AssertContains(t, out, "doc-1")
	upstream.assertDocsInsertText(t, "Added by Toolmux\n", 12)

	out = toolmuxtest.Run(t, deps, "google", "docs", "replace-all-text", "doc-1", "--text", "Hello", "--replace-text", "Hi", "--match-case", "--required-revision-id", "rev-1")
	toolmuxtest.AssertContains(t, out, "doc-1")
	upstream.assertDocsReplaceAllText(t, "Hello", "Hi", true, "rev-1")

	out = toolmuxtest.Run(t, deps, "google", "docs", "batch-update", "doc-1", "--json", `[{"insertText":{"location":{"index":1},"text":"Start "}}]`)
	toolmuxtest.AssertContains(t, out, "1")
	upstream.assertDocsInsertText(t, "Start ", 1)

	out = toolmuxtest.Run(t, deps, "google", "docs", "find-structure", "doc-1", "--kind", "text", "--text", "world")
	toolmuxtest.AssertContains(t, out, "text")
	toolmuxtest.AssertContains(t, out, "7")

	out = toolmuxtest.Run(t, deps, "google", "docs", "export", "doc-1", "--format", "markdown")
	toolmuxtest.AssertContains(t, out, "# Shared plan")

	out = toolmuxtest.Run(t, deps, "google", "docs", "style-ranges", "doc-1", "--paragraph-style-type", "HEADING_2", "--foreground-color", "#336699")
	toolmuxtest.AssertContains(t, out, "doc-1")
	upstream.assertDocsStyleText(t, 1, 13, "#336699")

	out = toolmuxtest.Run(t, deps, "google", "docs", "insert-table", "doc-1", "--rows", "2", "--columns", "3", "--index", "12")
	toolmuxtest.AssertContains(t, out, "doc-1")
	upstream.assertDocsInsertTable(t, 2, 3, 12)

	imageURI := "https://example.com/diagram.png"
	out = toolmuxtest.Run(t, deps, "google", "docs", "insert-image", "doc-1", "--uri", imageURI, "--index", "2", "--width-pt", "50")
	toolmuxtest.AssertContains(t, out, imageURI)
	upstream.assertDocsInsertImage(t, imageURI, 2)
}

func TestGoogleDriveUploadAndDocsInsertUploadedImage(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken:  "ya29.drive",
		RefreshToken: "refresh-google",
		TokenType:    "Bearer",
		Scopes:       []string{googleapi.ScopeDriveFile},
		Extra:        map[string]string{"auth_type": "oauth_broker"},
	}); err != nil {
		t.Fatal(err)
	}
	deps := googleDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	imagePath := filepath.Join(t.TempDir(), "diagram.png")
	if err := os.WriteFile(imagePath, []byte("fake png"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := toolmuxtest.Run(t, deps, "google", "drive", "files", "upload", imagePath, "--mime-type", "image/png", "--make-public")
	toolmuxtest.AssertContains(t, out, "image-1")
	toolmuxtest.AssertContains(t, out, "https://drive.google.com/uc?export=view&id=image-1")
	upstream.assertDriveUpload(t, "diagram.png", "image/png", "fake png")
	upstream.assertAnyoneReaderPermission(t, "image-1")

	encodedImage := base64.StdEncoding.EncodeToString([]byte("base64 png"))
	out = toolmuxtest.Run(t, deps, "google", "drive", "files", "upload", "--content-base64", encodedImage, "--name", "diagram.png", "--mime-type", "image/png")
	toolmuxtest.AssertContains(t, out, "image-1")
	upstream.assertDriveUpload(t, "diagram.png", "image/png", "base64 png")

	encodedDocx := base64.StdEncoding.EncodeToString([]byte("base64 docx"))
	docxMIME := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	out = toolmuxtest.Run(t, deps, "google", "drive", "files", "upload", "--content-base64", encodedDocx, "--name", "report.docx", "--mime-type", docxMIME, "--target-mime-type", googleapi.GoogleDocsMIMEType())
	toolmuxtest.AssertContains(t, out, googleapi.GoogleDocsMIMEType())
	upstream.assertDriveUpload(t, "report.docx", docxMIME, "base64 docx")
	upstream.assertDriveUploadTargetMIME(t, googleapi.GoogleDocsMIMEType())

	out = toolmuxtest.Run(t, deps, "google", "docs", "insert-image", "doc-1", "--upload-file", imagePath, "--mime-type", "image/png", "--make-public", "--index", "2")
	toolmuxtest.AssertContains(t, out, "image-1")
	upstream.assertDriveUpload(t, "diagram.png", "image/png", "fake png")
	upstream.assertAnyoneReaderPermission(t, "image-1")
	upstream.assertDocsInsertImage(t, "https://drive.google.com/uc?export=view&id=image-1", 2)

	out = toolmuxtest.Run(t, deps, "google", "docs", "insert-image", "doc-1", "--content-base64", encodedImage, "--name", "inline.png", "--mime-type", "image/png", "--make-public", "--index", "3")
	toolmuxtest.AssertContains(t, out, "image-1")
	upstream.assertDriveUpload(t, "inline.png", "image/png", "base64 png")
	upstream.assertDocsInsertImage(t, "https://drive.google.com/uc?export=view&id=image-1", 3)
}

func TestGoogleDocsSurfacesImageObjects(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	out := toolmuxtest.Run(t, deps, "google", "docs", "find-structure", "doc-img", "--kind", "image")
	for _, want := range []string{"inline_image", "kix.inline1", "Inline diagram", "positioned_image", "kix.pos1", "Floating logo"} {
		toolmuxtest.AssertContains(t, out, want)
	}

	out = toolmuxtest.Run(t, deps, "google", "docs", "find-structure", "doc-img", "--kind", "image", "--text", "pos1")
	toolmuxtest.AssertContains(t, out, "kix.pos1")
	if strings.Contains(out, "kix.inline1") {
		t.Fatalf("expected text filter to exclude the inline image, got:\n%s", out)
	}

	out = toolmuxtest.Run(t, deps, "--output", "json", "google", "docs", "get", "doc-img")
	for _, want := range []string{"\"images\"", "kix.inline1", "content_uri", "googleusercontent.com/inline1", "kix.pos1"} {
		toolmuxtest.AssertContains(t, out, want)
	}
}

func TestGoogleDocsReplaceAndDeleteImage(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	out := toolmuxtest.Run(t, deps, "google", "docs", "replace-image", "doc-1", "--object-id", "kix.inline1", "--uri", "https://example.com/new.png", "--replace-method", "CENTER_INSIDE")
	toolmuxtest.AssertContains(t, out, "kix.inline1")
	upstream.assertDocsReplaceImage(t, "kix.inline1", "https://example.com/new.png", "CENTER_INSIDE")

	out = toolmuxtest.Run(t, deps, "google", "docs", "delete-object", "doc-1", "--object-id", "kix.pos1")
	toolmuxtest.AssertContains(t, out, "doc-1")
	upstream.assertDocsDeletePositionedObject(t, "kix.pos1")

	toolmuxtest.Run(t, deps, "google", "docs", "delete-object", "doc-1", "--start-index", "2", "--end-index", "3")
	upstream.assertDocsDeleteContentRange(t, 2, 3)

	result := toolmuxtest.RunResult(t, deps, "google", "docs", "delete-object", "doc-1", "--object-id", "kix.pos1", "--start-index", "2", "--end-index", "3")
	if result.Err == nil {
		t.Fatalf("expected delete-object to reject both object-id and range, output:\n%s", result.Output)
	}
}

func TestGoogleDocsInsertImageWithoutDriveViaPublishCommand(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	received := filepath.Join(t.TempDir(), "received.png")
	cleanupMarker := filepath.Join(t.TempDir(), "cleanup.txt")
	publishCmd := fmt.Sprintf("cp \"$1\" %q; printf '%%s' https://cdn.example/published.png", received)
	cleanupCmd := fmt.Sprintf("printf '%%s' \"$1\" > %q", cleanupMarker)
	encoded := base64.StdEncoding.EncodeToString([]byte("png-bytes"))

	out := toolmuxtest.Run(t, deps, "google", "docs", "insert-image", "doc-1",
		"--content-base64", encoded, "--name", "x.png", "--mime-type", "image/png",
		"--image-host", "command", "--publish-command", publishCmd, "--publish-cleanup-command", cleanupCmd,
		"--index", "2",
	)
	toolmuxtest.AssertContains(t, out, "https://cdn.example/published.png")
	upstream.assertDocsInsertImage(t, "https://cdn.example/published.png", 2)
	upstream.assertNoDriveUpload(t)

	if data, err := os.ReadFile(received); err != nil || string(data) != "png-bytes" {
		t.Fatalf("expected publish command to receive image bytes, got %q (err %v)", string(data), err)
	}
	if data, err := os.ReadFile(cleanupMarker); err != nil || string(data) != "https://cdn.example/published.png" {
		t.Fatalf("expected cleanup command to receive published URL, got %q (err %v)", string(data), err)
	}
}

func TestGoogleDocsInsertImageTrashesDriveTempFile(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	imagePath := filepath.Join(t.TempDir(), "diagram.png")
	if err := os.WriteFile(imagePath, []byte("fake png"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := toolmuxtest.Run(t, deps, "google", "docs", "insert-image", "doc-1", "--upload-file", imagePath, "--mime-type", "image/png", "--make-public", "--index", "2")
	toolmuxtest.AssertContains(t, out, "https://drive.google.com/uc?export=view&id=image-1")
	upstream.assertDriveUpload(t, "diagram.png", "image/png", "fake png")
	upstream.assertAnyoneReaderPermission(t, "image-1")
	upstream.assertDocsInsertImage(t, "https://drive.google.com/uc?export=view&id=image-1", 2)
	upstream.assertImageTrashed(t)

	result := toolmuxtest.RunResult(t, deps, "google", "docs", "insert-image", "doc-1", "--upload-file", imagePath, "--mime-type", "image/png")
	if result.Err == nil {
		t.Fatalf("expected --image-host drive to require --make-public, output:\n%s", result.Output)
	}
	if !strings.Contains(result.Err.Error(), "make-public") {
		t.Fatalf("expected make-public error, got %v", result.Err)
	}
}

func TestGoogleDriveReportsMissingScopeAfterDocsSensitiveOverride(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	deps := googleDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
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

	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
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
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken:  "ya29.drive",
		RefreshToken: "refresh-google",
		TokenType:    "Bearer",
		Scopes:       []string{googleapi.ScopeDriveFile},
		Extra:        map[string]string{"auth_type": "oauth_broker"},
	}); err != nil {
		t.Fatal(err)
	}
	deps := googleDeps(t, store, upstream.Server.Client(), upstream.Server.URL)

	out := toolmuxtest.Run(t, deps, "google", "drive", "files", "copy", "https://docs.google.com/document/d/doc-1/edit", "--name", "Copied plan")
	toolmuxtest.AssertContains(t, out, "doc-copy")
	toolmuxtest.AssertContains(t, out, "Copied plan")
	upstream.assertCopyRequest(t, "doc-1", "Copied plan", "root")

	out = toolmuxtest.Run(t, deps, "google", "drive", "files", "copy", "doc-1", "--target-mime-type", googleapi.GoogleDocsMIMEType())
	toolmuxtest.AssertContains(t, out, googleapi.GoogleDocsMIMEType())
	upstream.assertCopyTargetMIME(t, googleapi.GoogleDocsMIMEType())

	out = toolmuxtest.Run(t, deps, "--output", "json", "google", "drive", "files", "copy", "--file", "doc-1", "--parent-id", "folder-1", "--dry-run")
	toolmuxtest.AssertContains(t, out, "google.drive.files.copy")
	toolmuxtest.AssertContains(t, out, "folder-1")
}

func TestGoogleDriveFilesUpdateUpdatesAccessibleFile(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken:  "ya29.drive",
		RefreshToken: "refresh-google",
		TokenType:    "Bearer",
		Scopes:       []string{googleapi.ScopeDriveFile},
		Extra:        map[string]string{"auth_type": "oauth_broker"},
	}); err != nil {
		t.Fatal(err)
	}
	deps := googleDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	replacementPath := filepath.Join(t.TempDir(), "replacement.docx")
	if err := os.WriteFile(replacementPath, []byte("replacement docx"), 0o644); err != nil {
		t.Fatal(err)
	}

	docxMIME := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	result := toolmuxtest.RunResult(t, deps, "google", "drive", "files", "update", "doc-1", "--target-mime-type", googleapi.GoogleDocsMIMEType())
	if result.Err == nil || !strings.Contains(result.Err.Error(), "--target-mime-type requires replacement content") {
		t.Fatalf("expected replacement content validation error, got err=%v output:\n%s", result.Err, result.Output)
	}

	out := toolmuxtest.Run(t, deps, "google", "drive", "files", "update", "https://docs.google.com/document/d/doc-1/edit", replacementPath, "--name", "Updated plan", "--mime-type", docxMIME, "--target-mime-type", googleapi.GoogleDocsMIMEType())
	toolmuxtest.AssertContains(t, out, "doc-1")
	toolmuxtest.AssertContains(t, out, "Updated plan")
	upstream.assertDriveUpdate(t, "doc-1", "Updated plan", nil, docxMIME, "replacement docx")
	upstream.assertDriveUpdateTargetMIME(t, googleapi.GoogleDocsMIMEType())

	encodedDocx := base64.StdEncoding.EncodeToString([]byte("replacement from base64"))
	out = toolmuxtest.Run(t, deps, "google", "drive", "files", "update", "--file", "doc-1", "--content-base64", encodedDocx, "--name", "Converted plan", "--mime-type", docxMIME, "--target-mime-type", googleapi.GoogleDocsMIMEType())
	toolmuxtest.AssertContains(t, out, googleapi.GoogleDocsMIMEType())
	upstream.assertDriveUpdate(t, "doc-1", "Converted plan", nil, docxMIME, "replacement from base64")
	upstream.assertDriveUpdateTargetMIME(t, googleapi.GoogleDocsMIMEType())

	out = toolmuxtest.Run(t, deps, "--output", "json", "google", "drive", "files", "update", "--file", "doc-1", "--trashed", "--dry-run")
	toolmuxtest.AssertContains(t, out, "google.drive.files.update")
	toolmuxtest.AssertContains(t, out, `"trashed": true`)

	out = toolmuxtest.Run(t, deps, "google", "drive", "files", "trash", "doc-1")
	toolmuxtest.AssertContains(t, out, "doc-1")
	toolmuxtest.AssertContains(t, out, "Trashed")
	trashed := true
	upstream.assertDriveUpdate(t, "doc-1", "", &trashed, "", "")
}

func TestGoogleDriveCommandsExposeMCPTools(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, spec := range providers.CommandSpecs() {
		seen[spec.ID] = true
	}
	for _, want := range []string{
		"google.docs.get",
		"google.docs.find_structure",
		"google.docs.export",
		"google.docs.append",
		"google.docs.replace_all_text",
		"google.docs.style_ranges",
		"google.docs.insert_table",
		"google.docs.insert_image",
		"google.docs.replace_image",
		"google.docs.delete_object",
		"google.docs.batch_update",
		"google.drive.selected.add",
		"google.drive.selected.list",
		"google.drive.files.copy",
		"google.drive.files.upload",
		"google.drive.files.update",
		"google.drive.files.trash",
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
