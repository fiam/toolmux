package client

import (
	"strconv"

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

type docsDocumentResult struct {
	DocumentID string                  `json:"document_id" yaml:"document_id"`
	Title      string                  `json:"title" yaml:"title"`
	RevisionID string                  `json:"revision_id,omitempty" yaml:"revision_id,omitempty"`
	PlainText  string                  `json:"plain_text" yaml:"plain_text"`
	Body       *googleapi.DocumentBody `json:"body,omitempty" yaml:"body,omitempty"`
}

func newDocsDocumentResult(document googleapi.Document, includeStructure bool) docsDocumentResult {
	result := docsDocumentResult{
		DocumentID: document.DocumentID,
		Title:      document.Title,
		RevisionID: document.RevisionID,
		PlainText:  document.PlainText(),
	}
	if includeStructure {
		result.Body = &document.Body
	}
	return result
}

func (result docsDocumentResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Document ID", result.DocumentID},
			{"Title", result.Title},
			{"Revision", result.RevisionID},
			{"Plain text", compactPlainText(result.PlainText)},
		},
	}
}

type docsBatchUpdateResult googleapi.BatchUpdateDocumentResponse

func (result docsBatchUpdateResult) Table(output.Options) output.Table {
	writeControl := googleapi.BatchUpdateDocumentResponse(result).WriteControl
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Document ID", result.DocumentID},
			{"Applied requests", strconv.Itoa(result.AppliedRequests)},
			{"Required revision", writeControl.RequiredRevisionID},
			{"Target revision", writeControl.TargetRevisionID},
		},
	}
}

var _ actions.TableRenderable = driveFileResult{}
var _ actions.TableRenderable = driveFilesResult{}
var _ actions.TableRenderable = docsDocumentResult{}
var _ actions.TableRenderable = docsBatchUpdateResult{}
var _ actions.TableRenderable = googlePickerResult{}
var _ actions.TableRenderable = googleConfiguredFilesResult{}
var _ actions.TextRenderable = authResult{}
