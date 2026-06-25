package client

import (
	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
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

func handleDriveCommentsList(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	source, err := driveFileSource(inv)
	if err != nil {
		return nil, err
	}
	fileID, err := googleDriveFileID(source)
	if err != nil {
		return nil, err
	}
	pageSize := inv.Int("page-size")
	if pageSize <= 0 {
		pageSize = 20
	}
	response, err := client.ListDriveComments(exec.Context, fileID, googleapi.ListDriveCommentsOptions{
		PageSize:          pageSize,
		PageToken:         inv.String("page-token"),
		IncludeDeleted:    inv.Bool("include-deleted"),
		StartModifiedTime: inv.String("start-modified-time"),
	})
	if err != nil {
		return nil, err
	}
	return driveCommentsResult{
		FileID:        fileID,
		Comments:      response.Comments,
		NextPageToken: response.NextPageToken,
	}, nil
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
