package client

import (
	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

type authResult struct {
	Message string `json:"message"`
}

func (result authResult) Text() string {
	return result.Message
}

type driveFileResult googleapi.DriveFile

func (result driveFileResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"File ID", result.ID},
			{"Name", result.Name},
			{"MIME type", result.MIMEType},
			{"Modified", result.ModifiedTime},
			{"URL", result.WebViewLink},
		},
	}
}

type driveFilesResult googleapi.DriveFilesResponse

func (result driveFilesResult) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(result.Files))
	for _, file := range result.Files {
		rows = append(rows, []string{file.ID, file.Name, file.MIMEType, file.ModifiedTime, file.WebViewLink})
	}
	if result.NextPageToken != "" {
		rows = append(rows, []string{"next page", result.NextPageToken, "", "", ""})
	}
	return output.Table{
		Headers: []string{"ID", "Name", "MIME type", "Modified", "URL"},
		Rows:    rows,
		Empty:   "no files",
	}
}

var _ actions.TableRenderable = driveFileResult{}
var _ actions.TableRenderable = driveFilesResult{}
var _ actions.TableRenderable = googlePickerResult{}
var _ actions.TableRenderable = googleConfiguredFilesResult{}
var _ actions.TextRenderable = authResult{}
