package client

import (
	"fmt"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/providers/oauthbroker"
)

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
			return fmt.Errorf("google drive support only supports %s; got %s", googleapi.ScopeDriveFile, scope)
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
