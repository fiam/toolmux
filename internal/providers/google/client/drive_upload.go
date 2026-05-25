package client

import (
	"encoding/base64"
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

type uploadInput struct {
	Path       string
	Name       string
	MIMEType   string
	Content    []byte
	FromBase64 bool
}

func handleDriveFilesUpload(exec actions.Context, inv actions.Invocation) (any, error) {
	input, err := uploadInputFromInvocation(exec, inv, false)
	if err != nil {
		return nil, err
	}
	request := driveUploadDryRun{
		Path:       input.Path,
		Name:       input.Name,
		MIMEType:   input.MIMEType,
		TargetMIME: strings.TrimSpace(inv.String("target-mime-type")),
		ParentID:   strings.TrimSpace(inv.String("parent-id")),
		Size:       len(input.Content),
		FromBase64: input.FromBase64,
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
		Name:           input.Name,
		ParentID:       request.ParentID,
		MIMEType:       input.MIMEType,
		TargetMIMEType: request.TargetMIME,
		Content:        input.Content,
	})
	if err != nil {
		return nil, err
	}
	result := driveUploadResult{File: file, Size: len(input.Content)}
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

func uploadInputFromInvocation(exec actions.Context, inv actions.Invocation, defaultImageName bool) (uploadInput, error) {
	source, err := uploadSourceValueFromInvocation(inv)
	if err != nil {
		return uploadInput{}, err
	}
	if source.ContentBase64 != "" {
		content, err := decodeBase64Content(source.ContentBase64)
		if err != nil {
			return uploadInput{}, err
		}
		name := strings.TrimSpace(inv.String("name"))
		if name == "" {
			if defaultImageName {
				name = "image"
			} else {
				return uploadInput{}, fmt.Errorf("--name is required when using --content-base64")
			}
		}
		return uploadInput{
			Name:       name,
			MIMEType:   uploadMIMEType(inv.String("mime-type"), name, content),
			Content:    content,
			FromBase64: true,
		}, nil
	}
	content, err := readLocalFile(exec, source.Path)
	if err != nil {
		return uploadInput{}, err
	}
	return uploadInput{
		Path:     source.Path,
		Name:     firstNonEmpty(inv.String("name"), filepath.Base(source.Path)),
		MIMEType: uploadMIMEType(inv.String("mime-type"), source.Path, content),
		Content:  content,
	}, nil
}

func decodeBase64Content(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if before, after, ok := strings.Cut(value, ","); ok && strings.Contains(strings.ToLower(before), ";base64") {
		value = after
	}
	value = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		content, err := encoding.DecodeString(value)
		if err == nil {
			return content, nil
		}
	}
	return nil, fmt.Errorf("content-base64 is not valid base64")
}

type uploadSourceValue struct {
	Path          string
	ContentBase64 string
}

func uploadSource(inv actions.Invocation) (string, error) {
	flagValue := strings.TrimSpace(inv.String("file"))
	argValue := ""
	if len(inv.Args) > 0 {
		argValue = strings.TrimSpace(inv.Args[0])
	}
	contentBase64 := strings.TrimSpace(inv.String("content-base64"))
	switch {
	case contentBase64 != "" && (flagValue != "" || argValue != ""):
		return "", fmt.Errorf("pass either --content-base64 or a local file path, not both")
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the upload path as either --file or a positional argument, not both")
	case flagValue != "":
		return flagValue, nil
	case argValue != "":
		return argValue, nil
	case contentBase64 != "":
		return "", nil
	default:
		return "", fmt.Errorf("local file path or --content-base64 is required")
	}
}

func uploadSourceValueFromInvocation(inv actions.Invocation) (uploadSourceValue, error) {
	path, err := uploadSource(inv)
	if err != nil {
		return uploadSourceValue{}, err
	}
	return uploadSourceValue{
		Path:          path,
		ContentBase64: strings.TrimSpace(inv.String("content-base64")),
	}, nil
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
