package client

import (
	"encoding/base64"
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
	rows := [][]string{
		{"File ID", result.ID},
		{"Name", result.Name},
		{"MIME type", result.MIMEType},
		{"Modified", result.ModifiedTime},
		{"URL", result.WebViewLink},
	}
	if result.Trashed {
		rows = append(rows, []string{"Trashed", strconv.FormatBool(result.Trashed)})
	}
	return output.Table{Headers: []string{"Field", "Value"}, Rows: rows}
}

type driveUploadDryRun struct {
	Path       string `json:"path" yaml:"path"`
	Name       string `json:"name" yaml:"name"`
	MIMEType   string `json:"mime_type" yaml:"mime_type"`
	TargetMIME string `json:"target_mime_type,omitempty" yaml:"target_mime_type,omitempty"`
	ParentID   string `json:"parent_id,omitempty" yaml:"parent_id,omitempty"`
	Size       int    `json:"size" yaml:"size"`
	FromBase64 bool   `json:"from_base64,omitempty" yaml:"from_base64,omitempty"`
	MakePublic bool   `json:"make_public" yaml:"make_public"`
}

type driveUploadResult struct {
	File       googleapi.DriveFile        `json:"file" yaml:"file"`
	Size       int                        `json:"size" yaml:"size"`
	Permission *googleapi.DrivePermission `json:"permission,omitempty" yaml:"permission,omitempty"`
	PublicURI  string                     `json:"public_uri,omitempty" yaml:"public_uri,omitempty"`
}

func (result driveUploadResult) Table(output.Options) output.Table {
	rows := [][]string{
		{"File ID", result.File.ID},
		{"Name", result.File.Name},
		{"MIME type", result.File.MIMEType},
		{"Size", strconv.Itoa(result.Size)},
		{"URL", result.File.WebViewLink},
	}
	if result.PublicURI != "" {
		rows = append(rows, []string{"Public image URI", result.PublicURI})
	}
	if result.Permission != nil {
		rows = append(rows, []string{"Permission", result.Permission.Type + ":" + result.Permission.Role})
	}
	return output.Table{Headers: []string{"Field", "Value"}, Rows: rows}
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
	Images     []docsStructureMatch    `json:"images,omitempty" yaml:"images,omitempty"`
	Body       *googleapi.DocumentBody `json:"body,omitempty" yaml:"body,omitempty"`
}

func newDocsDocumentResult(document googleapi.Document, includeStructure bool) docsDocumentResult {
	result := docsDocumentResult{
		DocumentID: document.DocumentID,
		Title:      document.Title,
		RevisionID: document.RevisionID,
		PlainText:  document.PlainText(),
	}
	if images := documentImages(document); len(images) > 0 {
		result.Images = images
	}
	if includeStructure {
		result.Body = &document.Body
	}
	return result
}

func (result docsDocumentResult) Table(output.Options) output.Table {
	rows := [][]string{
		{"Document ID", result.DocumentID},
		{"Title", result.Title},
		{"Revision", result.RevisionID},
		{"Plain text", compactPlainText(result.PlainText)},
	}
	if len(result.Images) > 0 {
		rows = append(rows, []string{"Images", strconv.Itoa(len(result.Images))})
	}
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows:    rows,
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

type docsStructureResult struct {
	DocumentID string               `json:"document_id" yaml:"document_id"`
	Title      string               `json:"title" yaml:"title"`
	RevisionID string               `json:"revision_id,omitempty" yaml:"revision_id,omitempty"`
	Matches    []docsStructureMatch `json:"matches" yaml:"matches"`
}

type docsStructureMatch struct {
	Kind           string  `json:"kind" yaml:"kind"`
	StartIndex     int     `json:"start_index" yaml:"start_index"`
	EndIndex       int     `json:"end_index" yaml:"end_index"`
	NamedStyleType string  `json:"named_style_type,omitempty" yaml:"named_style_type,omitempty"`
	HeadingID      string  `json:"heading_id,omitempty" yaml:"heading_id,omitempty"`
	Rows           int     `json:"rows,omitempty" yaml:"rows,omitempty"`
	Columns        int     `json:"columns,omitempty" yaml:"columns,omitempty"`
	Text           string  `json:"text,omitempty" yaml:"text,omitempty"`
	ObjectID       string  `json:"object_id,omitempty" yaml:"object_id,omitempty"`
	ContentURI     string  `json:"content_uri,omitempty" yaml:"content_uri,omitempty"`
	SourceURI      string  `json:"source_uri,omitempty" yaml:"source_uri,omitempty"`
	WidthPt        float64 `json:"width_pt,omitempty" yaml:"width_pt,omitempty"`
	HeightPt       float64 `json:"height_pt,omitempty" yaml:"height_pt,omitempty"`
	Title          string  `json:"title,omitempty" yaml:"title,omitempty"`
	Description    string  `json:"description,omitempty" yaml:"description,omitempty"`
}

func (result docsStructureResult) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		detail := compactPlainText(match.Text)
		if match.ObjectID != "" {
			detail = firstNonEmpty(match.Title, match.ContentURI)
		}
		rows = append(rows, []string{
			match.Kind,
			strconv.Itoa(match.StartIndex),
			strconv.Itoa(match.EndIndex),
			firstNonEmpty(match.NamedStyleType, match.ObjectID, "-"),
			detail,
		})
	}
	return output.Table{
		Headers: []string{"Kind", "Start", "End", "Style/Object", "Text/Image"},
		Rows:    rows,
		Empty:   "no matching structure",
	}
}

type docsExportResult struct {
	DocumentID string `json:"document_id" yaml:"document_id"`
	Format     string `json:"format" yaml:"format"`
	MIMEType   string `json:"mime_type" yaml:"mime_type"`
	Encoding   string `json:"encoding" yaml:"encoding"`
	Size       int    `json:"size" yaml:"size"`
	Content    string `json:"content" yaml:"content"`
}

func newDocsExportResult(documentID, format, mimeType string, content []byte) docsExportResult {
	encoding := "base64"
	value := base64.StdEncoding.EncodeToString(content)
	if isTextExportMIME(mimeType) {
		encoding = "none"
		value = string(content)
	}
	return docsExportResult{
		DocumentID: documentID,
		Format:     format,
		MIMEType:   mimeType,
		Encoding:   encoding,
		Size:       len(content),
		Content:    value,
	}
}

func (result docsExportResult) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Field", "Value"},
		Rows: [][]string{
			{"Document ID", result.DocumentID},
			{"Format", result.Format},
			{"MIME type", result.MIMEType},
			{"Encoding", result.Encoding},
			{"Size", strconv.Itoa(result.Size)},
			{"Content", compactPlainText(result.Content)},
		},
	}
}

type docsInsertImageResult struct {
	DocumentID      string                 `json:"document_id" yaml:"document_id"`
	ImageURI        string                 `json:"image_uri" yaml:"image_uri"`
	Publish         *docsPublishMeta       `json:"publish,omitempty" yaml:"publish,omitempty"`
	WriteControl    googleapi.WriteControl `json:"write_control,omitzero" yaml:"write_control,omitempty"`
	AppliedRequests int                    `json:"applied_requests" yaml:"applied_requests"`
}

func (result docsInsertImageResult) Table(output.Options) output.Table {
	rows := [][]string{
		{"Document ID", result.DocumentID},
		{"Image URI", result.ImageURI},
		{"Applied requests", strconv.Itoa(result.AppliedRequests)},
		{"Required revision", result.WriteControl.RequiredRevisionID},
		{"Target revision", result.WriteControl.TargetRevisionID},
	}
	return output.Table{Headers: []string{"Field", "Value"}, Rows: appendPublishRows(rows, result.Publish)}
}

type docsImageMutationResult struct {
	DocumentID      string                 `json:"document_id" yaml:"document_id"`
	ObjectID        string                 `json:"object_id" yaml:"object_id"`
	ReplaceMethod   string                 `json:"replace_method,omitempty" yaml:"replace_method,omitempty"`
	ImageURI        string                 `json:"image_uri" yaml:"image_uri"`
	Publish         *docsPublishMeta       `json:"publish,omitempty" yaml:"publish,omitempty"`
	WriteControl    googleapi.WriteControl `json:"write_control,omitzero" yaml:"write_control,omitempty"`
	AppliedRequests int                    `json:"applied_requests" yaml:"applied_requests"`
}

func (result docsImageMutationResult) Table(output.Options) output.Table {
	rows := [][]string{
		{"Document ID", result.DocumentID},
		{"Object ID", result.ObjectID},
		{"Image URI", result.ImageURI},
		{"Replace method", result.ReplaceMethod},
		{"Applied requests", strconv.Itoa(result.AppliedRequests)},
		{"Required revision", result.WriteControl.RequiredRevisionID},
	}
	return output.Table{Headers: []string{"Field", "Value"}, Rows: appendPublishRows(rows, result.Publish)}
}

func appendPublishRows(rows [][]string, publish *docsPublishMeta) [][]string {
	if publish == nil {
		return rows
	}
	rows = append(rows, []string{"Image host", publish.Host})
	if publish.UploadedFile != nil {
		rows = append(rows, []string{"Uploaded file", publish.UploadedFile.ID})
	}
	if publish.Permission != nil {
		rows = append(rows, []string{"Permission", publish.Permission.Type + ":" + publish.Permission.Role})
	}
	if publish.TrashAfterInsert {
		rows = append(rows, []string{"Trashed after insert", strconv.FormatBool(publish.Trashed)})
	}
	return rows
}

var _ actions.TableRenderable = driveFileResult{}
var _ actions.TableRenderable = driveFilesResult{}
var _ actions.TableRenderable = driveUploadResult{}
var _ actions.TableRenderable = docsDocumentResult{}
var _ actions.TableRenderable = docsBatchUpdateResult{}
var _ actions.TableRenderable = docsStructureResult{}
var _ actions.TableRenderable = docsExportResult{}
var _ actions.TableRenderable = docsInsertImageResult{}
var _ actions.TableRenderable = googlePickerResult{}
var _ actions.TableRenderable = googleConfiguredFilesResult{}
var _ actions.TextRenderable = authResult{}
