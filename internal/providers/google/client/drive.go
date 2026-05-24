package client

import (
	"github.com/fiam/toolmux/internal/actions"
)

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
