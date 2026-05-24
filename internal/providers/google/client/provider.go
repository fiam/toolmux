package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

const (
	sharedProviderID  = "google"
	googleProviderID  = "google"
	defaultAccount    = "default"
	authTypeBroker    = "oauth_broker"
	oauthRefreshSkew  = time.Minute
	fileCacheExtraKey = "configured_files"
)

var (
	defaultDocsScopes  = []string{googleapi.ScopeDriveFile}
	defaultDriveScopes = []string{googleapi.ScopeDriveFile}
)

func init() {
	providers.Register(GoogleDescriptor())
}

func GoogleDescriptor() providers.Provider {
	return providers.Provider{
		ID:               googleProviderID,
		DisplayName:      "Google",
		AuthMode:         "broker",
		ConnectionScopes: slices.Clone(defaultDriveScopes),
		BaseURLEnv:       "TOOLMUX_GOOGLE_API_URL",
		DefaultBaseURL:   googleapi.DefaultAPIBaseURL,
		Tree: actions.Group(googleProviderID,
			actions.Short("Use Google Workspace"),
			actions.Children(
				actions.Group("drive",
					actions.Short("Use Google Drive"),
					actions.Children(
						googleDriveTool("drive.search", "search", "Search Google Drive files visible to Toolmux", actions.VerbSearch, actions.EffectRead,
							actions.StringFlag("query", "", "Drive files.list query"),
							actions.IntFlag("page-size", 20, "maximum files to return"),
							actions.StringFlag("page-token", "", "Drive pagination token"),
						),
						googleDriveTool("drive.get", "get", "Get Google Drive file metadata", actions.VerbRead, actions.EffectRead,
							actions.StringFlag("file-id", "", "Google Drive file ID"),
						),
						googleDriveToolWithEffects("drive.pick", "pick", "Open Google Picker and return selected files", actions.VerbOpen, actions.EffectWrite, actions.EffectWrite,
							actions.StringFlag("mime-type", "", "file MIME type filter"),
							pickerTimeoutFlag(),
						),
						actions.Group("selected",
							actions.Short("Manage Google Drive files selected for Toolmux"),
							actions.Children(
								googleDriveToolWithEffects("drive.selected.add", "add", "Open Google Picker and save selected file IDs", actions.VerbOpen, actions.EffectWrite, actions.EffectWrite,
									actions.StringFlag("mime-type", "", "file MIME type filter"),
									pickerTimeoutFlag(),
								),
								googleDriveToolWithEffects("drive.selected.list", "list", "List saved Google Drive file IDs", actions.VerbList, actions.EffectNone, actions.EffectRead),
								googleDriveToolWithEffects("drive.selected.remove", "remove", "Remove a saved Google Drive file ID", actions.VerbDelete, actions.EffectNone, actions.EffectWrite,
									actions.Use("remove <file-id>"),
									actions.ExactArgs(1),
								),
							),
						),
						actions.Group("files",
							actions.Short("Operate on Google Drive files"),
							actions.Children(
								googleDriveToolWithEffects("drive.files.copy", "copy", "Copy an accessible Google Drive file into My Drive", actions.VerbCreate, actions.EffectWrite, actions.EffectNone,
									actions.Use("copy [file-id-or-url]"),
									actions.MaxArgs(1),
									actions.StringFlag("file", "", "Google Drive file ID or URL to copy"),
									actions.StringFlag("name", "", "new copy name"),
									actions.StringFlag("parent-id", "root", "destination folder ID; use root for My Drive"),
									actions.BoolFlag("dry-run", false, "show the Drive files.copy request without creating a copy"),
								),
							),
						),
						googleDriveTool("drive.available", "available", "List Google Drive files currently available to Toolmux", actions.VerbSearch, actions.EffectRead,
							actions.Aliases("accessible"),
							actions.IntFlag("page-size", 20, "maximum files to return"),
							actions.StringFlag("page-token", "", "Drive pagination token"),
						),
					),
				),
			),
		),
		Handlers: map[string]actions.Handler{
			"google.drive.search":          handleDriveSearch,
			"google.drive.get":             handleDriveGet,
			"google.drive.pick":            handleDrivePick,
			"google.drive.selected.add":    handleDriveSelectedAdd,
			"google.drive.selected.list":   handleDriveSelectedList,
			"google.drive.selected.remove": handleDriveSelectedRemove,
			"google.drive.files.copy":      handleDriveFilesCopy,
			"google.drive.available":       handleDriveAvailable,
		},
		AddHandler:    handleGoogleTopAdd,
		RemoveHandler: handleGoogleRemove,
	}
}

func accountFlag() actions.Option {
	return actions.StringFlag("account", defaultAccount, "Toolmux Google account name")
}

func pickerTimeoutFlag() actions.Option {
	return actions.IntFlag("timeout-seconds", 120, "seconds to wait for Google Picker selection")
}

func googleDocsTool(localID, segment, short string, verb actions.Verb, remote actions.Effect, opts ...actions.Option) actions.Spec {
	base := []actions.Option{
		actions.Short(short),
		actions.Description(docsToolDescription(segment, short)),
		actions.RBAC(actions.ResourceName("document"), verb, remote),
		actions.Scopes(defaultDocsScopes...),
		accountFlag(),
	}
	base = append(base, opts...)
	return actions.Command(actions.LocalName(localID), segment, base...)
}

func googleDriveTool(localID, segment, short string, verb actions.Verb, remote actions.Effect, opts ...actions.Option) actions.Spec {
	return googleDriveToolWithEffects(localID, segment, short, verb, remote, actions.EffectNone, opts...)
}

func googleDriveToolWithEffects(localID, segment, short string, verb actions.Verb, remote, local actions.Effect, opts ...actions.Option) actions.Spec {
	base := []actions.Option{
		actions.Short(short),
		actions.Description(driveToolDescription(segment, short)),
		actions.RBAC(actions.ResourceName("file"), verb, remote, local),
		actions.Scopes(defaultDriveScopes...),
		accountFlag(),
	}
	base = append(base, opts...)
	return actions.Command(actions.LocalName(localID), segment, base...)
}

func docsToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"get":          "Read a Google Docs document by document ID and return its title, revision ID, plain text, and append index. The default non-sensitive drive.file scope limits access to documents created by or explicitly opened/shared with Toolmux.",
		"create":       "Create a blank Google Docs document owned by the authenticated account using the non-sensitive drive.file scope. Use --dry-run to preview the request without creating the document.",
		"replace_text": "Replace all occurrences of text in a Google Docs document using the Docs API ReplaceAllTextRequest. The default non-sensitive drive.file scope limits access to documents created by or explicitly opened/shared with Toolmux. Use --required-revision-id or --target-revision-id to guard against collaborator edits.",
		"append_text":  "Append text before the document body's trailing newline. Toolmux reads the document to calculate the insertion index, then sends a Docs API batchUpdate request. The default non-sensitive drive.file scope limits access to documents created by or explicitly opened/shared with Toolmux.",
		"batch_update": "Send a raw Google Docs documents.batchUpdate JSON request. Use this for advanced Docs API operations not yet exposed as first-class Toolmux commands. The default non-sensitive drive.file scope limits access to documents created by or explicitly opened/shared with Toolmux.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}

func driveToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"search":    "Search Google Drive files visible to the Toolmux app using Drive files.list query syntax. With the default drive.file scope, results are limited to files created by or explicitly opened/shared with Toolmux.",
		"get":       "Read metadata for a Google Drive file visible to the Toolmux app by file ID.",
		"pick":      "Open Google Picker in the browser and return the selected files. This uses the default non-sensitive drive.file OAuth scope and does not save the selection; use toolmux google drive selected add to save file IDs locally.",
		"copy":      "Copy a Google Drive file that is already visible to Toolmux into My Drive. Pass a raw file ID or a Docs/Drive URL. With the default drive.file scope, a shared source file must first be selected with toolmux google drive selected add unless Toolmux created or opened it before.",
		"available": "List Google Drive files currently available to Toolmux through the default non-sensitive drive.file scope. This is not a full Drive listing; with drive.file it only returns files created by Toolmux or explicitly opened/shared with the app.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}

func handleGoogleTopAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	return handleGoogleAdd(exec, inv, googleProviderID, "Google", defaultDriveScopes)
}

func handleGoogleAdd(exec actions.Context, inv actions.Invocation, toolbox, displayName string, defaultScopes []string) (any, error) {
	if err := requireBrokerMode(inv, displayName); err != nil {
		return nil, err
	}
	requestedScopes := oauthbroker.UnionScopes(defaultScopes, inv.StringSlice("scope"))
	if err := requireGoogleDriveFileScopes(requestedScopes); err != nil {
		return nil, err
	}
	ref := googleCredentialRef(exec, account(inv))
	existing, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return nil, err
	}
	missing := oauthbroker.MissingScopes(existing.Scopes, requestedScopes)
	if found && len(missing) == 0 {
		return authResult{Message: googleAddMessage(displayName + " already has the requested Google OAuth scopes for account " + account(inv))}, nil
	}
	if exec.OpenBrowser == nil {
		return nil, fmt.Errorf("browser opener is not configured")
	}
	sessionProgress := exec.StartProgress("Creating Google broker OAuth session")
	session, err := createBrokerSession(exec, account(inv), requestedScopes)
	if err != nil {
		sessionProgress.Warn("Google broker OAuth session failed")
		return nil, err
	}
	sessionProgress.Done("Created Google broker OAuth session")
	if err := exec.OpenBrowser(session.AuthURL); err != nil {
		return nil, err
	}
	exec.ProgressStatus("Opened browser for Google broker OAuth")
	pollProgress := exec.StartProgress("Waiting for Google broker OAuth")
	tokens, err := brokerClient(exec).PollSession(exec.Context, session.SessionID, sharedProviderID, timeout(inv))
	if err != nil {
		pollProgress.Warn("Google broker OAuth failed")
		return nil, err
	}
	pollProgress.Done("Received Google broker OAuth token")
	tokens = oauthbroker.MergeTokens(existing, tokens, requestedScopes)
	tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
		"auth_type":  authTypeBroker,
		"broker_url": strings.TrimRight(exec.ToolmuxdURL, "/"),
	})
	if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, tokens); err != nil {
		return nil, err
	}
	return authResult{Message: googleAddMessage("added " + toolbox + " using Google brokered OAuth for account " + account(inv))}, nil
}

func googleAddMessage(prefix string) string {
	return prefix + "\nWith the default drive.file scope, existing Drive files are not accessible until the user selects them for Toolmux. Run `toolmux google drive selected add` to open Google Picker and save selected file IDs locally."
}

func requireBrokerMode(inv actions.Invocation, displayName string) error {
	mode := strings.ToLower(strings.TrimSpace(inv.String("auth")))
	if mode == "" {
		mode = "broker"
	}
	switch strings.ReplaceAll(mode, "_", "-") {
	case "broker":
	default:
		return fmt.Errorf("%s only supports brokered OAuth; pass --auth broker", displayName)
	}
	if hasAnySecretFlag(inv, "token") {
		return fmt.Errorf("%s does not accept direct tokens; use brokered OAuth", displayName)
	}
	return nil
}

func requireGoogleDriveFileScopes(scopes []string) error {
	for _, scope := range oauthbroker.CleanScopes(scopes) {
		if scope != googleapi.ScopeDriveFile {
			return fmt.Errorf("Google Drive support only supports %s; got %s", googleapi.ScopeDriveFile, scope)
		}
	}
	return nil
}

func handleGoogleRemove(exec actions.Context, inv actions.Invocation) (any, error) {
	if err := exec.Credentials.DeleteOAuthTokens(exec.Context, googleCredentialRef(exec, account(inv))); err != nil {
		return nil, err
	}
	return authResult{Message: "removed shared Google OAuth auth for account " + account(inv)}, nil
}

func handleDriveSelectedAdd(exec actions.Context, inv actions.Invocation) (any, error) {
	result, err := runGooglePicker(exec, inv, inv.String("mime-type"))
	if err != nil {
		return nil, err
	}
	if err := saveSelectedGoogleFiles(exec, inv, result.Files); err != nil {
		return nil, err
	}
	return googleConfiguredFilesResult(result), nil
}

func handleDriveSelectedList(exec actions.Context, inv actions.Invocation) (any, error) {
	tokens, err := googleTokens(exec, inv, nil)
	if err != nil {
		return nil, err
	}
	return googleConfiguredFilesResult{Files: configuredGoogleFiles(tokens)}, nil
}

func handleDriveFilesCopy(exec actions.Context, inv actions.Invocation) (any, error) {
	source, err := driveCopySource(inv)
	if err != nil {
		return nil, err
	}
	fileID, err := googleDriveFileID(source)
	if err != nil {
		return nil, err
	}
	request := googleapi.CopyDriveFileOptions{
		Name:     inv.String("name"),
		ParentID: inv.String("parent-id"),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, map[string]any{
			"file_id":   fileID,
			"name":      strings.TrimSpace(request.Name),
			"parent_id": strings.TrimSpace(request.ParentID),
		}), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	file, err := client.CopyDriveFile(exec.Context, fileID, request)
	if err != nil {
		return nil, fmt.Errorf("copying Google Drive file %s failed: %w. If this is a shared file, select it first with `toolmux google drive selected add` so the drive.file grant includes it", fileID, err)
	}
	return driveFileResult(file), nil
}

func handleDriveSelectedRemove(exec actions.Context, inv actions.Invocation) (any, error) {
	fileID := ""
	if len(inv.Args) > 0 {
		fileID = strings.TrimSpace(inv.Args[0])
	}
	if fileID == "" {
		return nil, fmt.Errorf("file ID is required")
	}
	ref := googleCredentialRef(exec, account(inv))
	tokens, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("google account %q is not authorized; run `toolmux add %s --account %s`", account(inv), exec.Provider, account(inv))
	}
	files, removed := removeConfiguredGoogleFile(configuredGoogleFiles(tokens), fileID)
	if !removed {
		return authResult{Message: "Google file " + fileID + " was not in Toolmux's saved file list"}, nil
	}
	if err := storeConfiguredGoogleFiles(exec, ref, tokens, files); err != nil {
		return nil, err
	}
	return authResult{Message: "removed Google file " + fileID + " from Toolmux's saved file list"}, nil
}

func handleDrivePick(exec actions.Context, inv actions.Invocation) (any, error) {
	return runGooglePicker(exec, inv, inv.String("mime-type"))
}

func runGooglePicker(exec actions.Context, inv actions.Invocation, mimeType string) (googlePickerResult, error) {
	if exec.OpenBrowser == nil {
		return googlePickerResult{}, fmt.Errorf("browser opener is not configured")
	}
	mimeType = strings.TrimSpace(mimeType)
	ref := googleCredentialRef(exec, account(inv))
	existing, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return googlePickerResult{}, err
	}
	if !found {
		existing = credentials.OAuthTokens{}
	}
	return handleBrokeredGooglePickerConfigure(exec, inv, existing, mimeType)
}

type brokeredGooglePickerSession struct {
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	PickerURL string    `json:"picker_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type brokeredGooglePickerStatus struct {
	SessionID string                   `json:"session_id"`
	Status    string                   `json:"status"`
	Error     string                   `json:"error,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
	Files     []googlePickerFile       `json:"files,omitempty"`
	Tokens    *credentials.OAuthTokens `json:"tokens,omitempty"`
}

type googlePickerBrokerStatusError struct {
	Status int
	Body   string
}

func (err googlePickerBrokerStatusError) Error() string {
	return fmt.Sprintf("toolmux Google Picker broker returned status %d: %s", err.Status, strings.TrimSpace(err.Body))
}

func handleBrokeredGooglePickerConfigure(exec actions.Context, inv actions.Invocation, tokens credentials.OAuthTokens, mimeType string) (googlePickerResult, error) {
	sessionProgress := exec.StartProgress("Creating Google Picker broker session")
	session, err := createBrokeredGooglePickerSession(exec, mimeType)
	if err != nil {
		sessionProgress.Warn("Google Picker broker session failed")
		return googlePickerResult{}, err
	}
	sessionProgress.Done("Created Google Picker broker session")
	if err := exec.OpenBrowser(session.PickerURL); err != nil {
		return googlePickerResult{}, err
	}
	exec.ProgressStatus("Opened Google Picker")
	pollProgress := exec.StartProgress("Waiting for Google Picker selection")
	status, err := pollBrokeredGooglePickerSession(exec, session.SessionID, timeout(inv))
	if err != nil {
		pollProgress.Warn("Google Picker selection failed")
		return googlePickerResult{}, err
	}
	pollProgress.Done("Received Google Picker selection")
	result := googlePickerResult{Files: status.Files}
	if status.Tokens != nil {
		result = hydrateBrokeredGooglePickerFiles(exec, *status.Tokens, result)
		if err := saveGooglePickerTokens(exec, inv, tokens, *status.Tokens); err != nil {
			return googlePickerResult{}, err
		}
	}
	return result, nil
}

func createBrokeredGooglePickerSession(exec actions.Context, mimeType string) (brokeredGooglePickerSession, error) {
	endpoint := strings.TrimRight(exec.ToolmuxdURL, "/") + "/v1/google/picker/sessions"
	data, err := json.Marshal(map[string]string{
		"mime_type": strings.TrimSpace(mimeType),
	})
	if err != nil {
		return brokeredGooglePickerSession{}, err
	}
	req, err := http.NewRequestWithContext(exec.Context, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return brokeredGooglePickerSession{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return brokeredGooglePickerSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return brokeredGooglePickerSession{}, googlePickerBrokerStatusError{Status: resp.StatusCode, Body: string(body)}
	}
	var session brokeredGooglePickerSession
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return brokeredGooglePickerSession{}, err
	}
	if strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.PickerURL) == "" {
		return brokeredGooglePickerSession{}, fmt.Errorf("toolmux Google Picker broker returned an incomplete session")
	}
	return session, nil
}

func pollBrokeredGooglePickerSession(exec actions.Context, sessionID string, wait time.Duration) (brokeredGooglePickerStatus, error) {
	ctx, cancel := context.WithTimeout(exec.Context, wait)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := getBrokeredGooglePickerSession(ctx, exec, sessionID)
		if err != nil {
			return brokeredGooglePickerStatus{}, err
		}
		switch status.Status {
		case "pending":
		case "complete":
			if len(status.Files) == 0 {
				return brokeredGooglePickerStatus{}, fmt.Errorf("google picker did not return a file")
			}
			return status, nil
		case "failed":
			return brokeredGooglePickerStatus{}, fmt.Errorf("google picker selection failed: %s", status.Error)
		case "expired":
			return brokeredGooglePickerStatus{}, fmt.Errorf("google picker session expired")
		default:
			return brokeredGooglePickerStatus{}, fmt.Errorf("google picker broker returned unknown status %q", status.Status)
		}
		select {
		case <-ctx.Done():
			return brokeredGooglePickerStatus{}, fmt.Errorf("timed out waiting for Google Picker selection: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func getBrokeredGooglePickerSession(ctx context.Context, exec actions.Context, sessionID string) (brokeredGooglePickerStatus, error) {
	endpoint := strings.TrimRight(exec.ToolmuxdURL, "/") + "/v1/google/picker/sessions/" + url.PathEscape(strings.TrimSpace(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return brokeredGooglePickerStatus{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(exec.HTTPClient).Do(req)
	if err != nil {
		return brokeredGooglePickerStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return brokeredGooglePickerStatus{}, googlePickerBrokerStatusError{Status: resp.StatusCode, Body: string(body)}
	}
	var status brokeredGooglePickerStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status); err != nil {
		return brokeredGooglePickerStatus{}, err
	}
	return status, nil
}

func hydrateBrokeredGooglePickerFiles(exec actions.Context, tokens credentials.OAuthTokens, result googlePickerResult) googlePickerResult {
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return result
	}
	client := googleapi.Client{
		BaseURL:     exec.ProviderURL,
		AccessToken: tokens.AccessToken,
		HTTPClient:  exec.HTTPClient,
	}
	hydrated := googlePickerResult{Files: make([]googlePickerFile, 0, len(result.Files))}
	for _, file := range cleanGooglePickerFiles(result.Files) {
		driveFile, err := client.GetDriveFile(exec.Context, file.ID)
		if err != nil {
			exec.ProgressWarn("Google Picker returned " + file.ID + " but Drive metadata lookup failed; keeping the file ID only")
			hydrated.Files = append(hydrated.Files, file)
			continue
		}
		hydrated.Files = append(hydrated.Files, googlePickerFile{
			ID:       firstNonEmpty(driveFile.ID, file.ID),
			Name:     firstNonEmpty(driveFile.Name, file.Name),
			URL:      firstNonEmpty(driveFile.WebViewLink, file.URL),
			MIMEType: firstNonEmpty(driveFile.MIMEType, file.MIMEType),
		})
	}
	return hydrated
}

func saveGooglePickerTokens(exec actions.Context, inv actions.Invocation, existing, picker credentials.OAuthTokens) error {
	ref := googleCredentialRef(exec, account(inv))
	merged := mergeGooglePickerTokens(existing, picker)
	merged.Extra = mergeExtra(merged.Extra, map[string]string{
		"auth_type":  authTypeBroker,
		"broker_url": strings.TrimRight(exec.ToolmuxdURL, "/"),
	})
	return exec.Credentials.SaveOAuthTokens(exec.Context, ref, merged)
}

func mergeGooglePickerTokens(existing, picker credentials.OAuthTokens) credentials.OAuthTokens {
	picker.Scopes = oauthbroker.UnionScopes(picker.Scopes, []string{googleapi.ScopeDriveFile})
	if len(oauthbroker.MissingScopes(picker.Scopes, existing.Scopes)) == 0 {
		return oauthbroker.MergeTokens(existing, picker, []string{googleapi.ScopeDriveFile})
	}
	merged := existing
	if strings.TrimSpace(merged.AccessToken) == "" {
		merged.AccessToken = strings.TrimSpace(picker.AccessToken)
		merged.ExpiresAt = picker.ExpiresAt
	}
	if strings.TrimSpace(merged.RefreshToken) == "" {
		merged.RefreshToken = strings.TrimSpace(picker.RefreshToken)
	}
	if strings.TrimSpace(merged.TokenType) == "" {
		merged.TokenType = firstNonEmpty(picker.TokenType, "Bearer")
	}
	merged.Scopes = oauthbroker.UnionScopes(existing.Scopes, picker.Scopes, []string{googleapi.ScopeDriveFile})
	merged.Extra = mergeExtra(existing.Extra, picker.Extra)
	return merged
}

func handleDocsGet(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDocsScopes)
	if err != nil {
		return nil, err
	}
	documentID, err := requiredString(inv, "document-id")
	if err != nil {
		return nil, err
	}
	document, err := client.GetDocument(exec.Context, documentID)
	if err != nil {
		return nil, err
	}
	return documentResultFromAPI(document), nil
}

func handleDocsCreate(exec actions.Context, inv actions.Invocation) (any, error) {
	title, err := requiredString(inv, "title")
	if err != nil {
		return nil, err
	}
	request := map[string]string{"title": title}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDocsScopes)
	if err != nil {
		return nil, err
	}
	document, err := client.CreateDocument(exec.Context, title)
	if err != nil {
		return nil, err
	}
	return documentResultFromAPI(document), nil
}

func handleDocsReplaceText(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := requiredString(inv, "document-id")
	if err != nil {
		return nil, err
	}
	find, err := requiredString(inv, "find")
	if err != nil {
		return nil, err
	}
	request := googleapi.BatchUpdateDocumentRequest{
		Requests: []googleapi.DocumentRequest{{
			ReplaceAllText: &googleapi.ReplaceAllTextRequest{
				ContainsText: googleapi.ContainsText{
					Text:      find,
					MatchCase: inv.Bool("match-case"),
				},
				ReplaceText: inv.String("replace"),
			},
		}},
		WriteControl: writeControl(inv),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDocsScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateDocument(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return batchUpdateResult(response), nil
}

func handleDocsAppendText(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := requiredString(inv, "document-id")
	if err != nil {
		return nil, err
	}
	text, err := requiredString(inv, "text")
	if err != nil {
		return nil, err
	}
	client, err := googleClient(exec, inv, defaultDocsScopes)
	if err != nil {
		return nil, err
	}
	document, err := client.GetDocument(exec.Context, documentID)
	if err != nil {
		return nil, err
	}
	request := googleapi.BatchUpdateDocumentRequest{
		Requests: []googleapi.DocumentRequest{{
			InsertText: &googleapi.InsertTextRequest{
				Text:     text,
				Location: googleapi.Location{Index: document.AppendIndex()},
			},
		}},
		WriteControl: writeControl(inv),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	response, err := client.BatchUpdateDocument(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return batchUpdateResult(response), nil
}

func handleDocsBatchUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := requiredString(inv, "document-id")
	if err != nil {
		return nil, err
	}
	var request map[string]any
	if err := jsonRequest(exec, inv.String("json"), &request); err != nil {
		return nil, err
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDocsScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateDocumentRaw(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return batchUpdateResult(response), nil
}

func handleDriveSearch(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	pageSize := inv.Int("page-size")
	if pageSize <= 0 {
		pageSize = 20
	}
	response, err := client.ListDriveFiles(exec.Context, inv.String("query"), pageSize, inv.String("page-token"))
	if err != nil {
		return nil, err
	}
	return driveFilesResult(response), nil
}

func handleDriveGet(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	fileID, err := requiredString(inv, "file-id")
	if err != nil {
		return nil, err
	}
	file, err := client.GetDriveFile(exec.Context, fileID)
	if err != nil {
		return nil, err
	}
	return driveFileResult(file), nil
}

func handleDriveAvailable(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	pageSize := inv.Int("page-size")
	if pageSize <= 0 {
		pageSize = 20
	}
	response, err := client.ListDriveFiles(exec.Context, "", pageSize, inv.String("page-token"))
	if err != nil {
		return nil, err
	}
	return driveFilesResult(response), nil
}

func googleClient(exec actions.Context, inv actions.Invocation, requiredScopes []string) (googleapi.Client, error) {
	tokens, err := googleTokens(exec, inv, requiredScopes)
	if err != nil {
		return googleapi.Client{}, err
	}
	return googleapi.Client{
		BaseURL:     exec.ProviderURL,
		AccessToken: tokens.AccessToken,
		HTTPClient:  exec.HTTPClient,
	}, nil
}

func googleTokens(exec actions.Context, inv actions.Invocation, requiredScopes []string) (credentials.OAuthTokens, error) {
	ref := googleCredentialRef(exec, account(inv))
	tokens, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return credentials.OAuthTokens{}, err
	}
	if !found {
		return credentials.OAuthTokens{}, fmt.Errorf("google account %q is not authorized; run `toolmux add %s --account %s`", account(inv), exec.Provider, account(inv))
	}
	if missing := oauthbroker.MissingScopes(tokens.Scopes, requiredScopes); len(missing) > 0 {
		return credentials.OAuthTokens{}, fmt.Errorf("%s requires missing Google OAuth scope(s): %s; run `toolmux add %s --account %s`", exec.Provider, strings.Join(missing, ", "), exec.Provider, account(inv))
	}
	if googleTokenNeedsRefresh(tokens, time.Now().UTC()) {
		authType := firstNonEmpty(tokens.Extra["auth_type"], authTypeBroker)
		refreshed, err := refreshGoogleTokens(exec, tokens)
		if err != nil {
			return credentials.OAuthTokens{}, err
		}
		tokens = oauthbroker.MergeTokens(tokens, refreshed, tokens.Scopes)
		tokens.Extra = mergeExtra(tokens.Extra, map[string]string{
			"auth_type": authType,
		})
		if err := exec.Credentials.SaveOAuthTokens(exec.Context, ref, tokens); err != nil {
			return credentials.OAuthTokens{}, err
		}
	}
	return tokens, nil
}

func refreshGoogleTokens(exec actions.Context, tokens credentials.OAuthTokens) (credentials.OAuthTokens, error) {
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return credentials.OAuthTokens{}, fmt.Errorf("google OAuth token is expired and has no refresh token")
	}
	brokerURL := firstNonEmpty(tokens.Extra["broker_url"], exec.ToolmuxdURL)
	return oauthbroker.Client{
		BaseURL:    brokerURL,
		HTTPClient: exec.HTTPClient,
	}.Refresh(exec.Context, sharedProviderID, tokens.RefreshToken)
}

func loadGoogleTokens(exec actions.Context, ref credentials.ConnectionRef) (credentials.OAuthTokens, bool, error) {
	tokens, err := exec.Credentials.LoadOAuthTokens(exec.Context, ref)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return credentials.OAuthTokens{}, false, nil
		}
		return credentials.OAuthTokens{}, false, err
	}
	if tokens.Extra == nil {
		tokens.Extra = map[string]string{}
	}
	return tokens, true, nil
}

func googleTokenNeedsRefresh(tokens credentials.OAuthTokens, now time.Time) bool {
	if tokens.ExpiresAt.IsZero() || strings.TrimSpace(tokens.RefreshToken) == "" {
		return false
	}
	return !now.Add(oauthRefreshSkew).Before(tokens.ExpiresAt)
}

func createBrokerSession(exec actions.Context, account string, scopes []string) (oauthbroker.Session, error) {
	return brokerClient(exec).CreateSession(exec.Context, sharedProviderID, exec.Profile, account, scopes)
}

func brokerClient(exec actions.Context) oauthbroker.Client {
	return oauthbroker.Client{
		BaseURL:    exec.ToolmuxdURL,
		HTTPClient: exec.HTTPClient,
	}
}

func googleCredentialRef(exec actions.Context, account string) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   exec.Profile,
		Provider:  sharedProviderID,
		AccountID: account,
	}
}

func writeControl(inv actions.Invocation) *googleapi.WriteControl {
	control := &googleapi.WriteControl{
		RequiredRevisionID: strings.TrimSpace(inv.String("required-revision-id")),
		TargetRevisionID:   strings.TrimSpace(inv.String("target-revision-id")),
	}
	if control.RequiredRevisionID == "" && control.TargetRevisionID == "" {
		return nil
	}
	return control
}

func jsonRequest(exec actions.Context, value string, out any) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("json is required; pass --json with inline JSON or @path")
	}
	var data []byte
	if path, ok := strings.CutPrefix(value, "@"); ok {
		if path == "" {
			return fmt.Errorf("json file path is required after @")
		}
		if exec.ReadFile == nil {
			return fmt.Errorf("file reader is not configured")
		}
		content, err := exec.ReadFile(path)
		if err != nil {
			return err
		}
		data = content
	} else {
		data = []byte(value)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	return nil
}

type googlePickerResult struct {
	Files []googlePickerFile `json:"files" yaml:"files"`
}

type googlePickerFile struct {
	ID       string `json:"id,omitempty" yaml:"id,omitempty"`
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`
	MIMEType string `json:"mime_type,omitempty" yaml:"mime_type,omitempty"`
}

type googleConfiguredFilesResult struct {
	Files []googlePickerFile `json:"files" yaml:"files"`
}

func (result googleConfiguredFilesResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Name", "Type", "ID"},
		Rows:    googlePickerTableRows(result.Files),
		Empty:   "no files saved",
	}
}

func (result googlePickerResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Name", "Type", "ID"},
		Rows:    googlePickerTableRows(result.Files),
		Empty:   "no files selected",
	}
}

func googlePickerTableRows(files []googlePickerFile) [][]string {
	rows := make([][]string, 0, len(files))
	for _, file := range files {
		rows = append(rows, []string{
			output.Value(file.Name),
			googleFileTypeLabel(file.MIMEType),
			file.ID,
		})
	}
	return rows
}

func googleFileTypeLabel(mimeType string) string {
	switch strings.TrimSpace(mimeType) {
	case googleapi.GoogleDocsMIMEType():
		return "Google Doc"
	case "application/vnd.google-apps.spreadsheet":
		return "Google Sheet"
	case "application/vnd.google-apps.presentation":
		return "Google Slides"
	case "application/vnd.google-apps.folder":
		return "Folder"
	case "application/pdf":
		return "PDF"
	case "image/jpeg":
		return "JPEG"
	case "image/png":
		return "PNG"
	case "text/plain":
		return "Text"
	case "":
		return "-"
	default:
		return mimeType
	}
}

func saveSelectedGoogleFiles(exec actions.Context, inv actions.Invocation, selected []googlePickerFile) error {
	ref := googleCredentialRef(exec, account(inv))
	tokens, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("google account %q is not authorized; run `toolmux add %s --account %s`", account(inv), exec.Provider, account(inv))
	}
	files := mergeConfiguredGoogleFiles(configuredGoogleFiles(tokens), selected)
	return storeConfiguredGoogleFiles(exec, ref, tokens, files)
}

func configuredGoogleFiles(tokens credentials.OAuthTokens) []googlePickerFile {
	raw := strings.TrimSpace(tokens.Extra[fileCacheExtraKey])
	if raw == "" {
		return nil
	}
	var files []googlePickerFile
	if err := json.Unmarshal([]byte(raw), &files); err != nil {
		return nil
	}
	return cleanGooglePickerFiles(files)
}

func storeConfiguredGoogleFiles(exec actions.Context, ref credentials.ConnectionRef, tokens credentials.OAuthTokens, files []googlePickerFile) error {
	if tokens.Extra == nil {
		tokens.Extra = map[string]string{}
	}
	files = cleanGooglePickerFiles(files)
	if len(files) == 0 {
		delete(tokens.Extra, fileCacheExtraKey)
		return exec.Credentials.SaveOAuthTokens(exec.Context, ref, tokens)
	}
	data, err := json.Marshal(files)
	if err != nil {
		return err
	}
	tokens.Extra[fileCacheExtraKey] = string(data)
	return exec.Credentials.SaveOAuthTokens(exec.Context, ref, tokens)
}

func mergeConfiguredGoogleFiles(existing, selected []googlePickerFile) []googlePickerFile {
	merged := cleanGooglePickerFiles(existing)
	index := map[string]int{}
	for i, file := range merged {
		index[file.ID] = i
	}
	for _, file := range cleanGooglePickerFiles(selected) {
		if i, ok := index[file.ID]; ok {
			merged[i] = file
			continue
		}
		index[file.ID] = len(merged)
		merged = append(merged, file)
	}
	return slices.Clip(merged)
}

func removeConfiguredGoogleFile(files []googlePickerFile, fileID string) ([]googlePickerFile, bool) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return cleanGooglePickerFiles(files), false
	}
	var removed bool
	out := make([]googlePickerFile, 0, len(files))
	for _, file := range cleanGooglePickerFiles(files) {
		if file.ID == fileID {
			removed = true
			continue
		}
		out = append(out, file)
	}
	return slices.Clip(out), removed
}

func cleanGooglePickerFiles(files []googlePickerFile) []googlePickerFile {
	seen := map[string]bool{}
	out := make([]googlePickerFile, 0, len(files))
	for _, file := range files {
		file.ID = strings.TrimSpace(file.ID)
		if file.ID == "" || seen[file.ID] {
			continue
		}
		file.Name = strings.TrimSpace(file.Name)
		file.URL = strings.TrimSpace(file.URL)
		file.MIMEType = strings.TrimSpace(file.MIMEType)
		seen[file.ID] = true
		out = append(out, file)
	}
	return slices.Clip(out)
}

func timeout(inv actions.Invocation) time.Duration {
	seconds := inv.Int("timeout-seconds")
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func account(inv actions.Invocation) string {
	if value := strings.TrimSpace(inv.String("account")); value != "" {
		return value
	}
	return defaultAccount
}

func requiredString(inv actions.Invocation, name string) (string, error) {
	value := strings.TrimSpace(inv.String(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func driveCopySource(inv actions.Invocation) (string, error) {
	flagValue := strings.TrimSpace(inv.String("file"))
	argValue := ""
	if len(inv.Args) > 0 {
		argValue = strings.TrimSpace(inv.Args[0])
	}
	switch {
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the Google Drive file as either --file or a positional argument, not both")
	case flagValue != "":
		return flagValue, nil
	case argValue != "":
		return argValue, nil
	default:
		return "", fmt.Errorf("Google Drive file ID or URL is required")
	}
}

func googleDriveFileID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("Google Drive file ID or URL is required")
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
			return id, nil
		}
		segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
		for i, segment := range segments {
			if segment != "d" && segment != "folders" {
				continue
			}
			if i+1 >= len(segments) {
				break
			}
			id, err := url.PathUnescape(segments[i+1])
			if err != nil {
				return "", err
			}
			if id = strings.TrimSpace(id); id != "" {
				return id, nil
			}
		}
		return "", fmt.Errorf("could not find a Google Drive file ID in %q", value)
	}
	if strings.ContainsAny(value, "/?#") {
		return "", fmt.Errorf("could not parse %q as a Google Drive URL; pass the raw file ID instead", value)
	}
	return value, nil
}

func hasAnySecretFlag(inv actions.Invocation, name string) bool {
	return strings.TrimSpace(inv.String(name)) != "" ||
		strings.TrimSpace(inv.String(name+"-env")) != "" ||
		strings.TrimSpace(inv.String(name+"-file")) != ""
}

func mergeExtra(base, overlay map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	for key, value := range overlay {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	return merged
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

type authResult struct {
	Message string `json:"message"`
}

func (result authResult) Text() string {
	return result.Message
}

type documentResult struct {
	DocumentID  string `json:"document_id,omitempty" yaml:"document_id,omitempty"`
	Title       string `json:"title,omitempty" yaml:"title,omitempty"`
	RevisionID  string `json:"revision_id,omitempty" yaml:"revision_id,omitempty"`
	AppendIndex int    `json:"append_index,omitempty" yaml:"append_index,omitempty"`
	Text        string `json:"text,omitempty" yaml:"text,omitempty"`
}

func documentResultFromAPI(document googleapi.Document) documentResult {
	return documentResult{
		DocumentID:  document.DocumentID,
		Title:       document.Title,
		RevisionID:  document.RevisionID,
		AppendIndex: document.AppendIndex(),
		Text:        document.PlainText(),
	}
}

func (result documentResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Document ID", result.DocumentID},
			{"Title", result.Title},
			{"Revision", result.RevisionID},
			{"Append index", strconv.Itoa(result.AppendIndex)},
			{"Text", truncate(result.Text, 160)},
		},
	}
}

type batchUpdateResult googleapi.BatchUpdateDocumentResponse

func (result batchUpdateResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Document ID", result.DocumentID},
			{"Applied requests", strconv.Itoa(result.AppliedRequests)},
			{"Required revision", result.WriteControl.RequiredRevisionID},
			{"Target revision", result.WriteControl.TargetRevisionID},
		},
	}
}

type driveFileResult googleapi.DriveFile

func (result driveFileResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"File ID", result.ID},
			{"Name", result.Name},
			{"MIME type", result.MIMEType},
			{"Modified", result.ModifiedTime},
			{"URL", result.WebViewLink},
		},
	}
}

type driveFilesResult googleapi.DriveFilesResponse

func (result driveFilesResult) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(result.Files))
	for _, file := range result.Files {
		rows = append(rows, []string{file.ID, file.Name, file.MIMEType, file.ModifiedTime, file.WebViewLink})
	}
	if result.NextPageToken != "" {
		rows = append(rows, []string{"next page", result.NextPageToken, "", "", ""})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "MIME type", "Modified", "URL"},
		Rows:    rows,
		Empty:   "no files",
	}
}

func truncate(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}

var _ actions.TableRenderable = documentResult{}
var _ actions.TableRenderable = batchUpdateResult{}
var _ actions.TableRenderable = driveFileResult{}
var _ actions.TableRenderable = driveFilesResult{}
var _ actions.TableRenderable = googlePickerResult{}
var _ actions.TableRenderable = googleConfiguredFilesResult{}
var _ actions.TextRenderable = authResult{}
