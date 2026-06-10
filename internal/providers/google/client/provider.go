package client

import (
	"slices"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

const (
	sharedProviderID  = "google"
	googleProviderID  = "google"
	authTypeBroker    = "oauth_broker"
	oauthRefreshSkew  = time.Minute
	fileCacheExtraKey = "configured_files"
)

var defaultDriveScopes = []string{googleapi.ScopeDriveFile}

func init() {
	providers.Register(GoogleDescriptor())
}

func GoogleDescriptor() providers.Provider {
	return providers.Provider{
		ID:               googleProviderID,
		DisplayName:      "Google",
		AuthMode:         "broker",
		ConnectionScopes: slices.Clone(defaultDriveScopes),
		BaseURLEnv:       "TOOLMUX_GOOGLE_API_URL",
		DefaultBaseURL:   googleapi.DefaultAPIBaseURL,
		Tree: actions.Group(googleProviderID,
			actions.Short("Use Google Workspace"),
			actions.Children(
				actions.Group("docs",
					actions.Short("Use Google Docs"),
					actions.Children(
						googleDocsTool("docs.get", "get", "Read a Google Docs document body", actions.VerbRead, actions.EffectRead,
							actions.Use("get [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.BoolFlag("include-structure", false, "include the Google Docs structural body in JSON/YAML output"),
						),
						googleDocsTool("docs.find_structure", "find-structure", "Find Google Docs headings, paragraphs, tables, or text ranges", actions.VerbSearch, actions.EffectRead,
							actions.Use("find-structure [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.StringFlag("kind", "all", "structure kind: all, heading, paragraph, table, text, or image"),
							actions.StringFlag("text", "", "optional text to match"),
							actions.BoolFlag("match-case", false, "match text case-sensitively"),
						),
						googleDocsTool("docs.export", "export", "Export a Google Docs document", actions.VerbRead, actions.EffectRead,
							actions.Use("export [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.StringFlag("format", "markdown", "export format: markdown, txt, pdf, docx, odt, rtf, html, or epub"),
							actions.StringFlag("mime-type", "", "custom Drive export MIME type; overrides --format"),
						),
						googleDocsTool("docs.append", "append", "Append text to a Google Docs document", actions.VerbUpdate, actions.EffectWrite,
							actions.Use("append [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.StringFlag("text", "", "text to append"),
							actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
							actions.BoolFlag("dry-run", false, "show the Docs batchUpdate request without applying it"),
						),
						googleDocsTool("docs.replace_all_text", "replace-all-text", "Replace matching text in a Google Docs document", actions.VerbUpdate, actions.EffectWrite,
							actions.Use("replace-all-text [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.StringFlag("text", "", "text to match"),
							actions.StringFlag("replace-text", "", "replacement text"),
							actions.BoolFlag("match-case", false, "match text case-sensitively"),
							actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
							actions.BoolFlag("dry-run", false, "show the Docs batchUpdate request without applying it"),
						),
						googleDocsTool("docs.style_ranges", "style-ranges", "Style Google Docs text or paragraph ranges", actions.VerbUpdate, actions.EffectWrite,
							actions.Use("style-ranges [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.IntFlag("start-index", 0, "range start index"),
							actions.IntFlag("end-index", 0, "range end index"),
							actions.StringFlag("text", "", "text to match and style"),
							actions.StringFlag("paragraph-style-type", "", "paragraph named style to target, such as HEADING_2"),
							actions.BoolFlag("match-case", false, "match text case-sensitively"),
							actions.BoolFlag("bold", false, "set matched text bold"),
							actions.BoolFlag("italic", false, "set matched text italic"),
							actions.BoolFlag("underline", false, "set matched text underlined"),
							actions.StringFlag("foreground-color", "", "text color as #RRGGBB"),
							actions.StringFlag("background-color", "", "text background color as #RRGGBB"),
							actions.StringFlag("named-style-type", "", "set paragraph named style, such as HEADING_2"),
							actions.StringFlag("alignment", "", "set paragraph alignment: START, CENTER, END, or JUSTIFIED"),
							actions.StringFlag("bullet-preset", "", "create bullets with a Docs bullet preset"),
							actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
							actions.BoolFlag("dry-run", false, "show the Docs batchUpdate request without applying it"),
						),
						googleDocsTool("docs.insert_table", "insert-table", "Insert a table into a Google Docs document", actions.VerbUpdate, actions.EffectWrite,
							actions.Use("insert-table [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.IntFlag("rows", 2, "number of table rows"),
							actions.IntFlag("columns", 2, "number of table columns"),
							actions.IntFlag("index", 0, "insert at this document index; defaults to end of body"),
							actions.StringFlag("cell-background-color", "", "optional background color for all inserted cells as #RRGGBB; requires --index"),
							actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
							actions.BoolFlag("dry-run", false, "show the Docs batchUpdate request without applying it"),
						),
						googleDocsToolWithEffects("docs.insert_image", "insert-image", "Insert an inline image into a Google Docs document", actions.VerbUpdate, actions.EffectWrite, actions.EffectWrite,
							append(docsImageSourceOptions(),
								actions.Use("insert-image [document-id]"),
								actions.MaxArgs(1),
								actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
								actions.IntFlag("index", 0, "insert at this document index; defaults to end of body"),
								actions.IntFlag("width-pt", 0, "display width in points"),
								actions.IntFlag("height-pt", 0, "display height in points"),
								actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
								actions.BoolFlag("dry-run", false, "show the hosting and Docs batchUpdate requests without applying them"),
							)...,
						),
						googleDocsToolWithEffects("docs.replace_image", "replace-image", "Replace an existing Google Docs image by object ID", actions.VerbUpdate, actions.EffectWrite, actions.EffectWrite,
							append(docsImageSourceOptions(),
								actions.Use("replace-image [document-id]"),
								actions.MaxArgs(1),
								actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
								actions.StringFlag("object-id", "", "image object ID to replace (from find-structure --kind image)"),
								actions.StringFlag("replace-method", "CENTER_CROP", "image replace method: CENTER_CROP or CENTER_INSIDE"),
								actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
								actions.BoolFlag("dry-run", false, "show the hosting and Docs batchUpdate requests without applying them"),
							)...,
						),
						googleDocsTool("docs.delete_object", "delete-object", "Delete a Google Docs image by object ID or range", actions.VerbUpdate, actions.EffectWrite,
							actions.Use("delete-object [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.StringFlag("object-id", "", "positioned image object ID to delete (deletePositionedObject)"),
							actions.IntFlag("start-index", 0, "inline image range start index (from find-structure --kind image)"),
							actions.IntFlag("end-index", 0, "inline image range end index"),
							actions.StringFlag("required-revision-id", "", "only apply when the document is at this revision"),
							actions.BoolFlag("dry-run", false, "show the Docs batchUpdate request without applying it"),
						),
						googleDocsTool("docs.batch_update", "batch-update", "Apply raw Google Docs batchUpdate requests", actions.VerbUpdate, actions.EffectWrite,
							actions.Use("batch-update [document-id]"),
							actions.MaxArgs(1),
							actions.StringFlag("document-id", "", "Google Docs document ID or URL"),
							actions.StringFlag("json", "", "Docs batchUpdate JSON object, JSON requests array, or @path"),
							actions.StringFlag("required-revision-id", "", "merge requiredRevisionId into writeControl"),
							actions.BoolFlag("dry-run", false, "show the Docs batchUpdate request without applying it"),
						),
					),
				),
				actions.Group("drive",
					actions.Short("Use Google Drive"),
					actions.Children(
						googleDriveTool("drive.search", "search", "Search Google Drive files visible to Toolmux", actions.VerbSearch, actions.EffectRead,
							actions.StringFlag("query", "", "Drive files.list query"),
							actions.IntFlag("page-size", 20, "maximum files to return"),
							actions.StringFlag("page-token", "", "Drive pagination token"),
						),
						googleDriveTool("drive.get", "get", "Get Google Drive file metadata", actions.VerbRead, actions.EffectRead,
							actions.StringFlag("file-id", "", "Google Drive file ID"),
						),
						googleDriveToolWithEffects("drive.pick", "pick", "Open Google Picker and return selected files", actions.VerbOpen, actions.EffectWrite, actions.EffectWrite,
							actions.StringFlag("mime-type", "", "file MIME type filter"),
							pickerTimeoutFlag(),
						),
						actions.Group("selected",
							actions.Short("Manage Google Drive files selected for Toolmux"),
							actions.Children(
								googleDriveToolWithEffects("drive.selected.add", "add", "Open Google Picker and save selected file IDs", actions.VerbOpen, actions.EffectWrite, actions.EffectWrite,
									actions.StringFlag("mime-type", "", "file MIME type filter"),
									pickerTimeoutFlag(),
								),
								googleDriveToolWithEffects("drive.selected.list", "list", "List saved Google Drive file IDs", actions.VerbList, actions.EffectNone, actions.EffectRead),
								googleDriveToolWithEffects("drive.selected.remove", "remove", "Remove a saved Google Drive file ID", actions.VerbDelete, actions.EffectNone, actions.EffectWrite,
									actions.Use("remove <file-id>"),
									actions.ExactArgs(1),
								),
							),
						),
						actions.Group("files",
							actions.Short("Operate on Google Drive files"),
							actions.Children(
								googleDriveToolWithEffects("drive.files.copy", "copy", "Copy an accessible Google Drive file into My Drive", actions.VerbCreate, actions.EffectWrite, actions.EffectNone,
									actions.Use("copy [file-id-or-url]"),
									actions.MaxArgs(1),
									actions.StringFlag("file", "", "Google Drive file ID or URL to copy"),
									actions.StringFlag("name", "", "new copy name"),
									actions.StringFlag("parent-id", "root", "destination folder ID; use root for My Drive"),
									actions.StringFlag("target-mime-type", "", "Google Workspace MIME type for conversion, such as application/vnd.google-apps.document"),
									actions.BoolFlag("dry-run", false, "show the Drive files.copy request without creating a copy"),
								),
								googleDriveToolWithEffects("drive.files.upload", "upload", "Upload a local file to Google Drive", actions.VerbCreate, actions.EffectWrite, actions.EffectRead,
									actions.Use("upload [path]"),
									actions.MaxArgs(1),
									actions.StringFlag("file", "", "local file path to upload"),
									actions.StringFlag("content-base64", "", "base64-encoded file content for MCP callers without shared filesystem access"),
									actions.StringFlag("name", "", "Drive file name; defaults to the local basename"),
									actions.StringFlag("mime-type", "", "media MIME type; defaults to extension or content detection"),
									actions.StringFlag("target-mime-type", "", "Google Workspace MIME type for conversion, such as application/vnd.google-apps.document"),
									actions.StringFlag("parent-id", "root", "destination folder ID; use root for My Drive"),
									actions.BoolFlag("make-public", false, "create an anyone-reader permission and return a public image URI"),
									actions.BoolFlag("dry-run", false, "show upload metadata without creating a file"),
								),
								googleDriveToolWithEffects("drive.files.update", "update", "Update Google Drive file metadata or content", actions.VerbUpdate, actions.EffectWrite, actions.EffectRead,
									actions.Use("update [file-id-or-url] [path]"),
									actions.MaxArgs(2),
									actions.StringFlag("file", "", "Google Drive file ID or URL to update"),
									actions.StringFlag("name", "", "new Drive file name"),
									actions.BoolFlag("trashed", false, "move the file to trash"),
									actions.BoolFlag("untrash", false, "restore the file from trash"),
									actions.StringFlag("upload-file", "", "local file path to upload as replacement content"),
									actions.StringFlag("content-base64", "", "base64-encoded replacement content for MCP callers without shared filesystem access"),
									actions.StringFlag("mime-type", "", "replacement media MIME type; defaults to extension or content detection"),
									actions.StringFlag("target-mime-type", "", "Google Workspace MIME type for conversion when replacing content, such as application/vnd.google-apps.document"),
									actions.BoolFlag("dry-run", false, "show the Drive files.update request without updating the file"),
								),
								googleDriveToolWithEffects("drive.files.trash", "trash", "Move an accessible Google Drive file to trash", actions.VerbDelete, actions.EffectWrite, actions.EffectNone,
									actions.Use("trash [file-id-or-url]"),
									actions.MaxArgs(1),
									actions.StringFlag("file", "", "Google Drive file ID or URL to move to trash"),
									actions.BoolFlag("dry-run", false, "show the Drive files.update trash request without updating the file"),
								),
							),
						),
						googleDriveTool("drive.available", "available", "List Google Drive files currently available to Toolmux", actions.VerbSearch, actions.EffectRead,
							actions.Aliases("accessible"),
							actions.IntFlag("page-size", 20, "maximum files to return"),
							actions.StringFlag("page-token", "", "Drive pagination token"),
						),
					),
				),
			),
		),
		Handlers: map[string]actions.Handler{
			"google.docs.get":              handleDocsGet,
			"google.docs.find_structure":   handleDocsFindStructure,
			"google.docs.export":           handleDocsExport,
			"google.docs.append":           handleDocsAppend,
			"google.docs.replace_all_text": handleDocsReplaceAllText,
			"google.docs.style_ranges":     handleDocsStyleRanges,
			"google.docs.insert_table":     handleDocsInsertTable,
			"google.docs.insert_image":     handleDocsInsertImage,
			"google.docs.replace_image":    handleDocsReplaceImage,
			"google.docs.delete_object":    handleDocsDeleteObject,
			"google.docs.batch_update":     handleDocsBatchUpdate,
			"google.drive.search":          handleDriveSearch,
			"google.drive.get":             handleDriveGet,
			"google.drive.pick":            handleDrivePick,
			"google.drive.selected.add":    handleDriveSelectedAdd,
			"google.drive.selected.list":   handleDriveSelectedList,
			"google.drive.selected.remove": handleDriveSelectedRemove,
			"google.drive.files.copy":      handleDriveFilesCopy,
			"google.drive.files.upload":    handleDriveFilesUpload,
			"google.drive.files.update":    handleDriveFilesUpdate,
			"google.drive.files.trash":     handleDriveFilesTrash,
			"google.drive.available":       handleDriveAvailable,
		},
		AddHandler:    handleGoogleTopAdd,
		RemoveHandler: handleGoogleRemove,
	}
}

func pickerTimeoutFlag() actions.Option {
	return actions.IntFlag("timeout-seconds", 120, "seconds to wait for Google Picker selection")
}

// docsImageSourceOptions are the flags shared by docs insert-image and
// replace-image: the four mutually-exclusive image sources plus the pluggable
// hosting flags used to make a byte source fetchable by the Docs API.
func docsImageSourceOptions() []actions.Option {
	return []actions.Option{
		actions.StringFlag("uri", "", "public image URI to insert directly"),
		actions.StringFlag("drive-file-id", "", "Google Drive image file ID or URL to insert via public content URI"),
		actions.StringFlag("upload-file", "", "local image file to host before insertion"),
		actions.StringFlag("content-base64", "", "base64-encoded image content to host before insertion"),
		actions.StringFlag("name", "", "Drive file name when hosting via --image-host drive"),
		actions.StringFlag("mime-type", "", "image MIME type when using --upload-file or --content-base64"),
		actions.StringFlag("image-host", "drive", "host for byte sources so Docs can fetch them: drive or command"),
		actions.StringFlag("parent-id", "root", "Drive parent folder for uploaded images (--image-host drive)"),
		actions.BoolFlag("make-public", false, "create an anyone-reader Drive permission so Docs can fetch the image (--image-host drive)"),
		actions.BoolFlag("trash-after-insert", true, "trash the uploaded Drive file after Docs copies the image (--image-host drive)"),
		actions.StringFlag("publish-command", "", "shell command that publishes the image (path as $1, bytes on stdin) and prints a public URL (--image-host command)"),
		actions.StringFlag("publish-cleanup-command", "", "shell command run with the published URL as $1 after insertion (--image-host command)"),
	}
}

func googleDriveTool(localID, segment, short string, verb actions.Verb, remote actions.Effect, opts ...actions.Option) actions.Spec {
	return googleDriveToolWithEffects(localID, segment, short, verb, remote, actions.EffectNone, opts...)
}

func googleDriveToolWithEffects(localID, segment, short string, verb actions.Verb, remote, local actions.Effect, opts ...actions.Option) actions.Spec {
	base := []actions.Option{
		actions.Short(short),
		actions.Description(driveToolDescription(segment, short)),
		actions.RBAC(actions.ResourceName("file"), verb, remote, local),
		actions.Scopes(defaultDriveScopes...),
	}
	base = append(base, opts...)
	return actions.Command(actions.LocalName(localID), segment, base...)
}

func googleDocsTool(localID, segment, short string, verb actions.Verb, remote actions.Effect, opts ...actions.Option) actions.Spec {
	return googleDocsToolWithEffects(localID, segment, short, verb, remote, actions.EffectNone, opts...)
}

func googleDocsToolWithEffects(localID, segment, short string, verb actions.Verb, remote, local actions.Effect, opts ...actions.Option) actions.Spec {
	base := []actions.Option{
		actions.Short(short),
		actions.Description(docsToolDescription(localID, short)),
		actions.RBAC(actions.ResourceName("document"), verb, remote, local),
		actions.Scopes(defaultDriveScopes...),
	}
	base = append(base, opts...)
	return actions.Command(actions.LocalName(localID), segment, base...)
}

func driveToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"search":    "Search Google Drive files visible to the Toolmux app using Drive files.list query syntax. With the default drive.file scope, results are limited to files created by or explicitly opened/shared with Toolmux.",
		"get":       "Read metadata for a Google Drive file visible to the Toolmux app by file ID.",
		"pick":      "Open Google Picker in the browser and return the selected files. This uses the default non-sensitive drive.file OAuth scope and does not save the selection; use toolmux google drive selected add to save file IDs locally.",
		"copy":      "Copy a Google Drive file that is already visible to Toolmux into My Drive. Pass a raw file ID or a Docs/Drive URL. Use --target-mime-type with a Google Workspace MIME type to request conversion during copy.",
		"upload":    "Upload file content to Google Drive using Drive files.create multipart upload. Pass a local path with --file or a positional argument, or pass --content-base64 for MCP callers without shared filesystem access. Use --target-mime-type with a Google Workspace MIME type to request conversion on create.",
		"update":    "Update a Google Drive file that is already visible to Toolmux using Drive files.update. Pass a raw file ID or Docs/Drive URL, then use --name for metadata, --trashed or --untrash for trash state, and --upload-file, --content-base64, or a second positional path to replace content. Use --target-mime-type with a Google Workspace MIME type to request conversion when replacing content.",
		"trash":     "Move a Google Drive file that is already visible to Toolmux to trash using Drive files.update with trashed=true. With the default drive.file scope, the file must have been created by or explicitly opened/shared with Toolmux.",
		"available": "List Google Drive files currently available to Toolmux through the default non-sensitive drive.file scope. This is not a full Drive listing; with drive.file it only returns files created by Toolmux or explicitly opened/shared with the app.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}

func docsToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"docs.get":              "Read a Google Docs document by document ID or Docs URL and return its title, revision ID, and plain text. Toolmux uses the non-sensitive drive.file scope, so the document must be created by or explicitly opened/shared with the Toolmux Google app.",
		"docs.find_structure":   "Read a Google Docs document and return exact Docs ranges for headings, paragraphs, tables, matching text, or images. Use --kind image to list inline and positioned images with their object IDs, body ranges, and content URIs (feed these to replace-image or delete-object). Docs indexes are UTF-16 code unit indexes from the Google Docs API.",
		"docs.export":           "Export a Google Docs document through Drive files.export. Use --format markdown, txt, pdf, docx, odt, rtf, html, or epub, or pass --mime-type for a custom Drive export MIME type.",
		"docs.append":           "Append text to the end of a Google Docs document using Docs batchUpdate. Pass --document-id or a Docs URL and --text. Use --required-revision-id to guard against concurrent edits, and --dry-run to inspect the request.",
		"docs.replace_all_text": "Replace all matching text in a Google Docs document using Docs batchUpdate replaceAllText. Pass --text and --replace-text. Use --match-case for case-sensitive matching, --required-revision-id to guard against concurrent edits, and --dry-run to inspect the request.",
		"docs.style_ranges":     "Apply higher-level Google Docs styling by exact range, text match, or paragraph named style. Supports text style, paragraph named style, alignment, and bullet creation, while generating Docs batchUpdate requests.",
		"docs.insert_table":     "Insert a table into a Google Docs document using Docs batchUpdate insertTable. Use --index for a specific insertion point or omit it to insert at the end of the body.",
		"docs.insert_image":     "Insert an inline image into a Google Docs document using Docs batchUpdate insertInlineImage. The Docs API can only fetch images from a public URL, so byte sources must be hosted first. Pass a public --uri, an already public --drive-file-id, or byte content with --upload-file or --content-base64. Choose hosting with --image-host drive (uploads to Drive with --make-public, trashed afterwards unless --trash-after-insert=false) or --image-host command (runs --publish-command to host off Drive for orgs that block public sharing).",
		"docs.replace_image":    "Replace an existing Google Docs image in place using Docs batchUpdate replaceImage. Pass --object-id (from find-structure --kind image) and a new image via the same source/hosting flags as insert-image. Use --replace-method CENTER_CROP or CENTER_INSIDE.",
		"docs.delete_object":    "Delete a Google Docs image. Pass --object-id for a positioned image (deletePositionedObject) or --start-index/--end-index for an inline image range (deleteContentRange); both come from find-structure --kind image.",
		"docs.batch_update":     "Apply a raw Google Docs batchUpdate request. Pass --json as either the full request object, a requests array that Toolmux wraps as {\"requests\": ...}, or @path. Use --required-revision-id to merge writeControl.requiredRevisionId.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}
