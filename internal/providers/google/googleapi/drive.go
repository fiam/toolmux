package googleapi

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

func (c Client) ListDriveFiles(ctx context.Context, query string, pageSize int, pageToken string) (DriveFilesResponse, error) {
	values := url.Values{}
	if strings.TrimSpace(query) != "" {
		values.Set("q", strings.TrimSpace(query))
	}
	if pageSize > 0 {
		values.Set("pageSize", strconv.Itoa(pageSize))
	}
	if strings.TrimSpace(pageToken) != "" {
		values.Set("pageToken", strings.TrimSpace(pageToken))
	}
	values.Set("fields", "nextPageToken,files(id,name,mimeType,webViewLink,modifiedTime)")
	var out DriveFilesResponse
	if err := c.get(ctx, "/drive/v3/files", values, &out); err != nil {
		return DriveFilesResponse{}, err
	}
	return out, nil
}

func (c Client) GetDriveFile(ctx context.Context, fileID string) (DriveFile, error) {
	values := url.Values{}
	values.Set("fields", "id,name,mimeType,webViewLink,modifiedTime")
	var out DriveFile
	if err := c.get(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID)), values, &out); err != nil {
		return DriveFile{}, err
	}
	return out, nil
}

func (c Client) CopyDriveFile(ctx context.Context, fileID string, options CopyDriveFileOptions) (DriveFile, error) {
	values := url.Values{}
	values.Set("fields", "id,name,mimeType,webViewLink,modifiedTime")
	values.Set("supportsAllDrives", "true")
	body := map[string]any{}
	if name := strings.TrimSpace(options.Name); name != "" {
		body["name"] = name
	}
	if parentID := strings.TrimSpace(options.ParentID); parentID != "" {
		body["parents"] = []string{parentID}
	}
	var out DriveFile
	if err := c.postJSONQuery(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID))+"/copy", values, body, &out); err != nil {
		return DriveFile{}, err
	}
	return out, nil
}
