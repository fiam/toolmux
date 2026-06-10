package client

import (
	"bytes"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

// imageInput is a transport-neutral image source resolved from the tool flags,
// before any hosting decision. Exactly one of URI / DriveFileID / Bytes is set.
type imageInput struct {
	URI         string
	DriveFileID string
	Bytes       []byte
	Name        string
	MIMEType    string
}

// docsPublishMeta describes how an image was made fetchable by the Docs API, for
// the tool result and audit trail.
type docsPublishMeta struct {
	Host             string                     `json:"host" yaml:"host"`
	DriveFileID      string                     `json:"drive_file_id,omitempty" yaml:"drive_file_id,omitempty"`
	UploadedFile     *googleapi.DriveFile       `json:"uploaded_file,omitempty" yaml:"uploaded_file,omitempty"`
	Permission       *googleapi.DrivePermission `json:"permission,omitempty" yaml:"permission,omitempty"`
	TrashAfterInsert bool                       `json:"trash_after_insert,omitempty" yaml:"trash_after_insert,omitempty"`
	Trashed          bool                       `json:"trashed,omitempty" yaml:"trashed,omitempty"`
	PublishCommand   string                     `json:"publish_command,omitempty" yaml:"publish_command,omitempty"`
}

func resolveImageInput(exec actions.Context, inv actions.Invocation) (imageInput, error) {
	uri := strings.TrimSpace(inv.String("uri"))
	driveFileID := strings.TrimSpace(inv.String("drive-file-id"))
	uploadFile := strings.TrimSpace(inv.String("upload-file"))
	contentBase64 := strings.TrimSpace(inv.String("content-base64"))
	sources := 0
	for _, value := range []string{uri, driveFileID, uploadFile, contentBase64} {
		if value != "" {
			sources++
		}
	}
	if sources != 1 {
		return imageInput{}, fmt.Errorf("pass exactly one of --uri, --drive-file-id, --upload-file, or --content-base64")
	}
	switch {
	case uri != "":
		return imageInput{URI: uri}, nil
	case driveFileID != "":
		fileID, err := googleDriveFileID(driveFileID)
		if err != nil {
			return imageInput{}, err
		}
		return imageInput{DriveFileID: fileID}, nil
	case uploadFile != "":
		content, err := readLocalFile(exec, uploadFile)
		if err != nil {
			return imageInput{}, err
		}
		mimeType := uploadMIMEType(inv.String("mime-type"), uploadFile, content)
		if !isDocsInlineImageMIME(mimeType) {
			return imageInput{}, fmt.Errorf("docs inline images must be PNG, JPEG, or GIF; detected %s", mimeType)
		}
		return imageInput{Bytes: content, Name: firstNonEmpty(inv.String("name"), filepathBase(uploadFile)), MIMEType: mimeType}, nil
	default:
		content, err := decodeBase64Content(contentBase64)
		if err != nil {
			return imageInput{}, err
		}
		name := firstNonEmpty(inv.String("name"), "image")
		mimeType := uploadMIMEType(inv.String("mime-type"), name, content)
		if !isDocsInlineImageMIME(mimeType) {
			return imageInput{}, fmt.Errorf("docs inline images must be PNG, JPEG, or GIF; detected %s", mimeType)
		}
		return imageInput{Bytes: content, Name: name, MIMEType: mimeType}, nil
	}
}

// publishImage turns a resolved image source into a publicly fetchable URI for
// the Docs API. The Docs API can only fetch images from a public URL (raw bytes
// are not supported), so byte sources must be hosted somewhere first. It returns
// the URI, metadata, and an optional cleanup to run after the batchUpdate
// succeeds (the Docs API copies the image at insertion, so the hosted copy can
// be removed immediately).
func publishImage(exec actions.Context, inv actions.Invocation, client googleapi.Client, in imageInput) (string, *docsPublishMeta, func() error, error) {
	switch {
	case in.URI != "":
		return in.URI, &docsPublishMeta{Host: "uri"}, nil, nil
	case in.DriveFileID != "":
		meta := &docsPublishMeta{Host: "drive", DriveFileID: in.DriveFileID}
		if inv.Bool("make-public") {
			permission, err := client.CreateAnyoneReaderPermission(exec.Context, in.DriveFileID)
			if err != nil {
				return "", nil, nil, err
			}
			meta.Permission = &permission
		}
		// Never trash a Drive file we did not create.
		return googleDrivePublicImageURI(in.DriveFileID), meta, nil, nil
	}
	host := strings.ToLower(strings.TrimSpace(firstNonEmpty(inv.String("image-host"), "drive")))
	switch host {
	case "drive":
		return publishViaDrive(exec, inv, client, in)
	case "command":
		return publishViaCommand(exec, inv, in)
	default:
		return "", nil, nil, fmt.Errorf("unsupported --image-host %q (use drive or command)", host)
	}
}

func publishViaDrive(exec actions.Context, inv actions.Invocation, client googleapi.Client, in imageInput) (string, *docsPublishMeta, func() error, error) {
	if !inv.Bool("make-public") {
		return "", nil, nil, fmt.Errorf("--make-public is required for --image-host drive because Docs fetches images from public Drive URLs; use --image-host command with --publish-command to avoid Drive public sharing")
	}
	file, err := client.UploadDriveFile(exec.Context, googleapi.UploadDriveFileOptions{
		Name:     in.Name,
		ParentID: strings.TrimSpace(inv.String("parent-id")),
		MIMEType: in.MIMEType,
		Content:  in.Bytes,
	})
	if err != nil {
		return "", nil, nil, err
	}
	permission, err := client.CreateAnyoneReaderPermission(exec.Context, file.ID)
	if err != nil {
		return "", nil, nil, err
	}
	meta := &docsPublishMeta{Host: "drive", UploadedFile: &file, Permission: &permission}
	var cleanup func() error
	if inv.Bool("trash-after-insert") {
		meta.TrashAfterInsert = true
		fileID := file.ID
		cleanup = func() error {
			trashed := true
			_, err := client.UpdateDriveFile(exec.Context, fileID, googleapi.UpdateDriveFileOptions{Trashed: &trashed})
			return err
		}
	}
	return googleDrivePublicImageURI(file.ID), meta, cleanup, nil
}

func publishViaCommand(exec actions.Context, inv actions.Invocation, in imageInput) (string, *docsPublishMeta, func() error, error) {
	command := firstNonEmpty(strings.TrimSpace(inv.String("publish-command")), strings.TrimSpace(envValue(exec, "TOOLMUX_GOOGLE_IMAGE_PUBLISH_COMMAND")))
	if command == "" {
		return "", nil, nil, fmt.Errorf("--publish-command (or TOOLMUX_GOOGLE_IMAGE_PUBLISH_COMMAND) is required for --image-host command")
	}
	tmp, err := os.CreateTemp("", "toolmux-docs-image-*"+imageExtension(in.MIMEType))
	if err != nil {
		return "", nil, nil, err
	}
	tmpPath := tmp.Name()
	cleanupTemp := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(in.Bytes); err != nil {
		_ = tmp.Close()
		cleanupTemp()
		return "", nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		cleanupTemp()
		return "", nil, nil, err
	}
	output, err := runPublishCommand(exec.Context, command, tmpPath, in.Bytes)
	if err != nil {
		cleanupTemp()
		return "", nil, nil, err
	}
	uri, err := validatePublicImageURI(output)
	if err != nil {
		cleanupTemp()
		return "", nil, nil, fmt.Errorf("publish command output: %w", err)
	}
	meta := &docsPublishMeta{Host: "command", PublishCommand: command}
	cleanupCommand := strings.TrimSpace(inv.String("publish-cleanup-command"))
	cleanup := func() error {
		defer cleanupTemp()
		if cleanupCommand == "" {
			return nil
		}
		_, err := runPublishCommand(exec.Context, cleanupCommand, uri, []byte(uri))
		return err
	}
	return uri, meta, cleanup, nil
}

// runPublishCommand runs command through the system shell with the payload path
// as $1 and the payload bytes on stdin, returning trimmed stdout.
func runPublishCommand(ctx context.Context, command, arg string, stdin []byte) (string, error) {
	// #nosec G204 -- the publish command is explicit user configuration.
	cmd := osexec.CommandContext(ctx, "sh", "-c", command, "sh", arg)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("publish command failed: %w: %s", err, message)
		}
		return "", fmt.Errorf("publish command failed: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func validatePublicImageURI(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("expected a public image URL, got empty output")
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return "", fmt.Errorf("expected a public http(s) image URL, got %q", value)
	}
	if len(value) > 2048 {
		return "", fmt.Errorf("image URL exceeds the Docs API 2 kB limit")
	}
	return value, nil
}

func envValue(exec actions.Context, name string) string {
	if exec.Env != nil {
		return exec.Env(name)
	}
	return os.Getenv(name)
}

func imageExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

func dryRunImageURI(inv actions.Invocation, in imageInput) string {
	switch {
	case in.URI != "":
		return in.URI
	case in.DriveFileID != "":
		return googleDrivePublicImageURI(in.DriveFileID)
	default:
		if strings.EqualFold(strings.TrimSpace(inv.String("image-host")), "command") {
			return "<publish-command-output>"
		}
		return "https://drive.google.com/uc?export=view&id=<uploaded-file-id>"
	}
}

func docsReplaceImageRequest(objectID, uri, method string) map[string]any {
	replace := map[string]any{
		"imageObjectId": objectID,
		"uri":           uri,
	}
	if method != "" {
		replace["imageReplaceMethod"] = method
	}
	return map[string]any{"replaceImage": replace}
}

func handleDocsReplaceImage(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	objectID := strings.TrimSpace(inv.String("object-id"))
	if objectID == "" {
		return nil, fmt.Errorf("--object-id is required (run find-structure --kind image to list image object IDs)")
	}
	in, err := resolveImageInput(exec, inv)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(firstNonEmpty(inv.String("replace-method"), "CENTER_CROP")))
	if inv.Bool("dry-run") {
		request := docsBatchRequest([]map[string]any{docsReplaceImageRequest(objectID, dryRunImageURI(inv, in), method)}, docsWriteControl(inv))
		return actions.NewDryRun(inv.Spec.ID, map[string]any{"batchUpdate": request, "image": in}), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	uri, meta, cleanup, err := publishImage(exec, inv, client, in)
	if err != nil {
		return nil, err
	}
	request := docsBatchRequest([]map[string]any{docsReplaceImageRequest(objectID, uri, method)}, docsWriteControl(inv))
	response, err := client.BatchUpdateDocumentRaw(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	runImageCleanup(exec, cleanup, meta)
	return docsImageMutationResult{
		DocumentID:      response.DocumentID,
		ObjectID:        objectID,
		ReplaceMethod:   method,
		ImageURI:        uri,
		Publish:         meta,
		WriteControl:    response.WriteControl,
		AppliedRequests: response.AppliedRequests,
	}, nil
}

func handleDocsDeleteObject(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	objectID := strings.TrimSpace(inv.String("object-id"))
	start := inv.Int("start-index")
	end := inv.Int("end-index")
	hasRange := start > 0 || end > 0
	var requests []map[string]any
	switch {
	case objectID != "" && hasRange:
		return nil, fmt.Errorf("pass either --object-id (positioned image) or --start-index/--end-index (inline image), not both")
	case objectID != "":
		requests = []map[string]any{{"deletePositionedObject": map[string]any{"objectId": objectID}}}
	case hasRange:
		if start <= 0 || end <= start {
			return nil, fmt.Errorf("start-index and end-index must define a positive non-empty range")
		}
		requests = []map[string]any{{"deleteContentRange": map[string]any{"range": docsRange(start, end)}}}
	default:
		return nil, fmt.Errorf("pass --object-id (positioned image) or --start-index/--end-index (inline image)")
	}
	request := docsBatchRequest(requests, docsWriteControl(inv))
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateDocumentRaw(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return docsBatchUpdateResult(response), nil
}

// runImageCleanup runs the post-insert cleanup (Drive trash or cleanup command)
// best-effort: the image is already in the document, so a cleanup failure is a
// warning, not a tool failure.
func runImageCleanup(exec actions.Context, cleanup func() error, meta *docsPublishMeta) {
	if cleanup == nil {
		return
	}
	if err := cleanup(); err != nil {
		exec.ProgressWarn(fmt.Sprintf("image hosted copy cleanup failed: %v", err))
		return
	}
	if meta != nil {
		meta.Trashed = true
	}
}
