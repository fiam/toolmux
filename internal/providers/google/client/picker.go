package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

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
