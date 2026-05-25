package client

import (
	"fmt"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

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
		Name:           inv.String("name"),
		ParentID:       inv.String("parent-id"),
		TargetMIMEType: inv.String("target-mime-type"),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, map[string]any{
			"file_id":          fileID,
			"name":             strings.TrimSpace(request.Name),
			"parent_id":        strings.TrimSpace(request.ParentID),
			"target_mime_type": strings.TrimSpace(request.TargetMIMEType),
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
	ref := googleCredentialRef(exec, exec.AccountName())
	tokens, found, err := loadGoogleTokens(exec, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("google toolbox %q is not authorized; run `toolmux add google --name %s`", exec.Provider, exec.AccountName())
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
