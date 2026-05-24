package client

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

func handleDriveFilesUpload(exec actions.Context, inv actions.Invocation) (any, error) {
	source, err := uploadSource(inv)
	if err != nil {
		return nil, err
	}
	content, err := readLocalFile(exec, source)
	if err != nil {
		return nil, err
	}
	name := firstNonEmpty(inv.String("name"), filepath.Base(source))
	mimeType := uploadMIMEType(inv.String("mime-type"), source, content)
	request := driveUploadDryRun{
		Path:       source,
		Name:       name,
		MIMEType:   mimeType,
		ParentID:   strings.TrimSpace(inv.String("parent-id")),
		Size:       len(content),
		MakePublic: inv.Bool("make-public"),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	file, err := client.UploadDriveFile(exec.Context, googleapi.UploadDriveFileOptions{
		Name:     name,
		ParentID: request.ParentID,
		MIMEType: mimeType,
		Content:  content,
	})
	if err != nil {
		return nil, err
	}
	result := driveUploadResult{File: file, Size: len(content)}
	if inv.Bool("make-public") {
		permission, err := client.CreateAnyoneReaderPermission(exec.Context, file.ID)
		if err != nil {
			return nil, err
		}
		result.Permission = &permission
		result.PublicURI = googleDrivePublicImageURI(file.ID)
	}
	return result, nil
}

func uploadSource(inv actions.Invocation) (string, error) {
	flagValue := strings.TrimSpace(inv.String("file"))
	argValue := ""
	if len(inv.Args) > 0 {
		argValue = strings.TrimSpace(inv.Args[0])
	}
	switch {
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the upload path as either --file or a positional argument, not both")
	case flagValue != "":
		return flagValue, nil
	case argValue != "":
		return argValue, nil
	default:
		return "", fmt.Errorf("local file path is required")
	}
}

func readLocalFile(exec actions.Context, path string) ([]byte, error) {
	readFile := exec.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	return readFile(path)
}

func uploadMIMEType(flagValue, path string, content []byte) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
		if mediaType, _, err := mime.ParseMediaType(extType); err == nil && mediaType != "" {
			return mediaType
		}
		return extType
	}
	if len(content) == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(content)
}

func googleDrivePublicImageURI(fileID string) string {
	query := url.Values{}
	query.Set("export", "view")
	query.Set("id", strings.TrimSpace(fileID))
	return "https://drive.google.com/uc?" + query.Encode()
}

func isDocsInlineImageMIME(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "image/jpg", "image/gif":
		return true
	default:
		return false
	}
}
