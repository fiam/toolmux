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
	values.Set("fields", "id,name,mimeType,webViewLink,webContentLink,modifiedTime")
	var out DriveFile
	if err := c.get(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID)), values, &out); err != nil {
		return DriveFile{}, err
	}
	return out, nil
}

func (c Client) CopyDriveFile(ctx context.Context, fileID string, options CopyDriveFileOptions) (DriveFile, error) {
	values := url.Values{}
	values.Set("fields", "id,name,mimeType,webViewLink,webContentLink,modifiedTime")
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

func (c Client) UploadDriveFile(ctx context.Context, options UploadDriveFileOptions) (DriveFile, error) {
	values := url.Values{}
	values.Set("uploadType", "multipart")
	values.Set("fields", "id,name,mimeType,webViewLink,webContentLink,modifiedTime")
	values.Set("supportsAllDrives", "true")
	metadata := map[string]any{}
	if name := strings.TrimSpace(options.Name); name != "" {
		metadata["name"] = name
	}
	if parentID := strings.TrimSpace(options.ParentID); parentID != "" {
		metadata["parents"] = []string{parentID}
	}
	var out DriveFile
	if err := c.postMultipartQuery(ctx, "/upload/drive/v3/files", values, metadata, strings.TrimSpace(options.MIMEType), options.Content, &out); err != nil {
		return DriveFile{}, err
	}
	return out, nil
}

func (c Client) ExportDriveFile(ctx context.Context, fileID string, mimeType string) ([]byte, error) {
	values := url.Values{}
	values.Set("mimeType", strings.TrimSpace(mimeType))
	return c.getBytes(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID))+"/export", values)
}

func (c Client) CreateAnyoneReaderPermission(ctx context.Context, fileID string) (DrivePermission, error) {
	values := url.Values{}
	values.Set("fields", "id,type,role")
	values.Set("supportsAllDrives", "true")
	body := map[string]string{
		"type": "anyone",
		"role": "reader",
	}
	var out DrivePermission
	if err := c.postJSONQuery(ctx, "/drive/v3/files/"+url.PathEscape(strings.TrimSpace(fileID))+"/permissions", values, body, &out); err != nil {
		return DrivePermission{}, err
	}
	return out, nil
}
