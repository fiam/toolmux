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
