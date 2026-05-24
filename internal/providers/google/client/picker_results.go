package client

import (
	"strings"

	"github.com/fiam/toolmux/internal/output"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

type googlePickerResult struct {
	Files []googlePickerFile `json:"files" yaml:"files"`
}

type googlePickerFile struct {
	ID       string `json:"id,omitempty" yaml:"id,omitempty"`
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`
	MIMEType string `json:"mime_type,omitempty" yaml:"mime_type,omitempty"`
}

type googleConfiguredFilesResult struct {
	Files []googlePickerFile `json:"files" yaml:"files"`
}

func (result googleConfiguredFilesResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Name", "Type", "ID"},
		Rows:    googlePickerTableRows(result.Files),
		Empty:   "no files saved",
	}
}

func (result googlePickerResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Name", "Type", "ID"},
		Rows:    googlePickerTableRows(result.Files),
		Empty:   "no files selected",
	}
}

func googlePickerTableRows(files []googlePickerFile) [][]string {
	rows := make([][]string, 0, len(files))
	for _, file := range files {
		rows = append(rows, []string{
			output.Value(file.Name),
			googleFileTypeLabel(file.MIMEType),
			file.ID,
		})
	}
	return rows
}

func googleFileTypeLabel(mimeType string) string {
	switch strings.TrimSpace(mimeType) {
	case googleapi.GoogleDocsMIMEType():
		return "Google Doc"
	case "application/vnd.google-apps.spreadsheet":
		return "Google Sheet"
	case "application/vnd.google-apps.presentation":
		return "Google Slides"
	case "application/vnd.google-apps.folder":
		return "Folder"
	case "application/pdf":
		return "PDF"
	case "image/jpeg":
		return "JPEG"
	case "image/png":
		return "PNG"
	case "text/plain":
		return "Text"
	case "":
		return "-"
	default:
		return mimeType
	}
}
