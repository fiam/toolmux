package client

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
)

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
