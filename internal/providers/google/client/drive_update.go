package client

import (
	"fmt"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

type driveUpdateDryRun struct {
	FileID     string `json:"file_id" yaml:"file_id"`
	Name       string `json:"name,omitempty" yaml:"name,omitempty"`
	Trashed    *bool  `json:"trashed,omitempty" yaml:"trashed,omitempty"`
	UploadPath string `json:"upload_path,omitempty" yaml:"upload_path,omitempty"`
	MIMEType   string `json:"mime_type,omitempty" yaml:"mime_type,omitempty"`
	TargetMIME string `json:"target_mime_type,omitempty" yaml:"target_mime_type,omitempty"`
	Size       int    `json:"size,omitempty" yaml:"size,omitempty"`
	FromBase64 bool   `json:"from_base64,omitempty" yaml:"from_base64,omitempty"`
}

func handleDriveFilesUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	fileID, uploadSource, err := driveUpdateInputs(inv)
	if err != nil {
		return nil, err
	}
	request := googleapi.UpdateDriveFileOptions{
		Name:           strings.TrimSpace(inv.String("name")),
		TargetMIMEType: strings.TrimSpace(inv.String("target-mime-type")),
	}
	if inv.Bool("trashed") && inv.Bool("untrash") {
		return nil, fmt.Errorf("pass only one of --trashed or --untrash")
	}
	if inv.Bool("trashed") {
		trashed := true
		request.Trashed = &trashed
	}
	if inv.Bool("untrash") {
		trashed := false
		request.Trashed = &trashed
	}
	if strings.TrimSpace(inv.String("mime-type")) != "" && uploadSource.Path == "" && uploadSource.ContentBase64 == "" {
		return nil, fmt.Errorf("--mime-type requires --upload-file, --content-base64, or an upload path")
	}
	dryRun := driveUpdateDryRun{
		FileID:     fileID,
		Name:       request.Name,
		Trashed:    request.Trashed,
		TargetMIME: request.TargetMIMEType,
	}
	if uploadSource.ContentBase64 != "" {
		content, err := decodeBase64Content(uploadSource.ContentBase64)
		if err != nil {
			return nil, err
		}
		request.Content = content
		request.MIMEType = uploadMIMEType(inv.String("mime-type"), firstNonEmpty(request.Name, fileID), content)
		dryRun.FromBase64 = true
		dryRun.MIMEType = request.MIMEType
		dryRun.Size = len(content)
	} else if uploadSource.Path != "" {
		content, err := readLocalFile(exec, uploadSource.Path)
		if err != nil {
			return nil, err
		}
		request.Content = content
		request.MIMEType = uploadMIMEType(inv.String("mime-type"), uploadSource.Path, content)
		dryRun.UploadPath = uploadSource.Path
		dryRun.MIMEType = request.MIMEType
		dryRun.Size = len(content)
	}
	if request.TargetMIMEType != "" && request.Content == nil {
		return nil, fmt.Errorf("--target-mime-type requires replacement content for files.update; pass --upload-file, --content-base64, or an upload path")
	}
	if request.Name == "" && request.Trashed == nil && request.TargetMIMEType == "" && request.Content == nil {
		return nil, fmt.Errorf("at least one update is required: --name, --trashed, --untrash, --target-mime-type, --upload-file, or --content-base64")
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, dryRun), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	file, err := client.UpdateDriveFile(exec.Context, fileID, request)
	if err != nil {
		return nil, fmt.Errorf("updating Google Drive file %s failed: %w. If this is a shared file, select it first with `toolmux google drive selected add` so the drive.file grant includes it", fileID, err)
	}
	return driveFileResult(file), nil
}

func handleDriveFilesTrash(exec actions.Context, inv actions.Invocation) (any, error) {
	source, err := driveCopySource(inv)
	if err != nil {
		return nil, err
	}
	fileID, err := googleDriveFileID(source)
	if err != nil {
		return nil, err
	}
	trashed := true
	request := googleapi.UpdateDriveFileOptions{Trashed: &trashed}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, driveUpdateDryRun{
			FileID:  fileID,
			Trashed: request.Trashed,
		}), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	file, err := client.UpdateDriveFile(exec.Context, fileID, request)
	if err != nil {
		return nil, fmt.Errorf("trashing Google Drive file %s failed: %w. If this is a shared file, select it first with `toolmux google drive selected add` so the drive.file grant includes it", fileID, err)
	}
	return driveFileResult(file), nil
}

func driveUpdateInputs(inv actions.Invocation) (string, uploadSourceValue, error) {
	fileFlag := strings.TrimSpace(inv.String("file"))
	uploadFlag := strings.TrimSpace(inv.String("upload-file"))
	contentBase64 := strings.TrimSpace(inv.String("content-base64"))
	args := trimmedArgs(inv.Args)

	var fileValue string
	var uploadArg string
	if fileFlag != "" {
		fileValue = fileFlag
		if len(args) > 1 {
			return "", uploadSourceValue{}, fmt.Errorf("pass at most one upload path when --file is set")
		}
		if len(args) == 1 {
			uploadArg = args[0]
		}
	} else {
		if len(args) == 0 {
			return "", uploadSourceValue{}, fmt.Errorf("google drive file ID or URL is required")
		}
		fileValue = args[0]
		if len(args) > 1 {
			uploadArg = args[1]
		}
	}
	uploadPath, err := resolveOptionalUploadPath(uploadFlag, uploadArg, contentBase64)
	if err != nil {
		return "", uploadSourceValue{}, err
	}
	fileID, err := googleDriveFileID(fileValue)
	if err != nil {
		return "", uploadSourceValue{}, err
	}
	return fileID, uploadSourceValue{Path: uploadPath, ContentBase64: contentBase64}, nil
}

func resolveOptionalUploadPath(flagValue, argValue, contentBase64 string) (string, error) {
	switch {
	case contentBase64 != "" && (flagValue != "" || argValue != ""):
		return "", fmt.Errorf("pass either --content-base64 or a local replacement path, not both")
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the replacement upload path as either --upload-file or a positional argument, not both")
	case flagValue != "":
		return flagValue, nil
	default:
		return argValue, nil
	}
}

func trimmedArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}
