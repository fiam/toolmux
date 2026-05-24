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
									actions.BoolFlag("dry-run", false, "show the Drive files.copy request without creating a copy"),
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
			"google.docs.append":           handleDocsAppend,
			"google.docs.replace_all_text": handleDocsReplaceAllText,
			"google.docs.batch_update":     handleDocsBatchUpdate,
			"google.drive.search":          handleDriveSearch,
			"google.drive.get":             handleDriveGet,
			"google.drive.pick":            handleDrivePick,
			"google.drive.selected.add":    handleDriveSelectedAdd,
			"google.drive.selected.list":   handleDriveSelectedList,
			"google.drive.selected.remove": handleDriveSelectedRemove,
			"google.drive.files.copy":      handleDriveFilesCopy,
			"google.drive.available":       handleDriveAvailable,
		},
		AddHandler:    handleGoogleTopAdd,
		RemoveHandler: handleGoogleRemove,
	}
}

func pickerTimeoutFlag() actions.Option {
	return actions.IntFlag("timeout-seconds", 120, "seconds to wait for Google Picker selection")
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
	base := []actions.Option{
		actions.Short(short),
		actions.Description(docsToolDescription(localID, short)),
		actions.RBAC(actions.ResourceName("document"), verb, remote, actions.EffectNone),
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
		"copy":      "Copy a Google Drive file that is already visible to Toolmux into My Drive. Pass a raw file ID or a Docs/Drive URL. With the default drive.file scope, a shared source file must first be selected with toolmux google drive selected add unless Toolmux created or opened it before.",
		"available": "List Google Drive files currently available to Toolmux through the default non-sensitive drive.file scope. This is not a full Drive listing; with drive.file it only returns files created by Toolmux or explicitly opened/shared with the app.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}

func docsToolDescription(name, fallback string) string {
	descriptions := map[string]string{
		"docs.get":              "Read a Google Docs document by document ID or Docs URL and return its title, revision ID, and plain text. Toolmux uses the non-sensitive drive.file scope, so the document must be created by or explicitly opened/shared with the Toolmux Google app.",
		"docs.append":           "Append text to the end of a Google Docs document using Docs batchUpdate. Pass --document-id or a Docs URL and --text. Use --required-revision-id to guard against concurrent edits, and --dry-run to inspect the request.",
		"docs.replace_all_text": "Replace all matching text in a Google Docs document using Docs batchUpdate replaceAllText. Pass --text and --replace-text. Use --match-case for case-sensitive matching, --required-revision-id to guard against concurrent edits, and --dry-run to inspect the request.",
		"docs.batch_update":     "Apply a raw Google Docs batchUpdate request. Pass --json as either the full request object, a requests array that Toolmux wraps as {\"requests\": ...}, or @path. Use --required-revision-id to merge writeControl.requiredRevisionId.",
	}
	return firstNonEmpty(descriptions[name], fallback)
}
