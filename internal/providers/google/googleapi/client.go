package googleapi

import (
	"net/http"
)

const (
	DefaultAPIBaseURL     = "https://www.googleapis.com"
	DefaultDocsAPIBaseURL = "https://docs.googleapis.com"
	DefaultAuthURL        = "https://accounts.google.com/o/oauth2/v2/auth"
	// #nosec G101 -- this is Google's public OAuth token endpoint, not a token.
	DefaultTokenURL  = "https://oauth2.googleapis.com/token"
	DefaultRevokeURL = "https://oauth2.googleapis.com/revoke"

	ScopeDocs            = "https://www.googleapis.com/auth/documents"
	ScopeDriveFile       = "https://www.googleapis.com/auth/drive.file"
	ScopeDriveMetadata   = "https://www.googleapis.com/auth/drive.metadata.readonly"
	googleDocsMIME       = "application/vnd.google-apps.document"
	defaultResponseLimit = 16 << 20
)

type Client struct {
	BaseURL     string
	DocsBaseURL string
	AccessToken string
	HTTPClient  *http.Client
}

type OAuthOptions struct {
	AuthURL       string
	TokenURL      string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	Scopes        []string
	CodeChallenge string
	CodeVerifier  string
}

type OAuthTokenResponse struct {
	AccessToken      string `json:"access_token,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	TokenType        string `json:"token_type,omitempty"`
	Scope            string `json:"scope,omitempty"`
	ExpiresIn        int    `json:"expires_in,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type Document struct {
	DocumentID string       `json:"documentId,omitempty"`
	Title      string       `json:"title,omitempty"`
	RevisionID string       `json:"revisionId,omitempty"`
	Body       DocumentBody `json:"body,omitzero"`
}

type DocumentBody struct {
	Content []StructuralElement `json:"content,omitempty"`
}

type StructuralElement struct {
	StartIndex int        `json:"startIndex,omitempty"`
	EndIndex   int        `json:"endIndex,omitempty"`
	Paragraph  *Paragraph `json:"paragraph,omitempty"`
	Table      *Table     `json:"table,omitempty"`
}

type Paragraph struct {
	Elements       []ParagraphElement `json:"elements,omitempty"`
	ParagraphStyle ParagraphStyle     `json:"paragraphStyle,omitzero"`
}

type ParagraphElement struct {
	StartIndex int      `json:"startIndex,omitempty"`
	EndIndex   int      `json:"endIndex,omitempty"`
	TextRun    *TextRun `json:"textRun,omitempty"`
}

type TextRun struct {
	Content string `json:"content,omitempty"`
}

type ParagraphStyle struct {
	HeadingID      string `json:"headingId,omitempty"`
	NamedStyleType string `json:"namedStyleType,omitempty"`
	Alignment      string `json:"alignment,omitempty"`
}

type Table struct {
	Rows      int        `json:"rows,omitempty"`
	Columns   int        `json:"columns,omitempty"`
	TableRows []TableRow `json:"tableRows,omitempty"`
}

type TableRow struct {
	StartIndex int         `json:"startIndex,omitempty"`
	EndIndex   int         `json:"endIndex,omitempty"`
	TableCells []TableCell `json:"tableCells,omitempty"`
}

type TableCell struct {
	StartIndex int                 `json:"startIndex,omitempty"`
	EndIndex   int                 `json:"endIndex,omitempty"`
	Content    []StructuralElement `json:"content,omitempty"`
}

type BatchUpdateDocumentRequest struct {
	Requests     []DocumentRequest `json:"requests"`
	WriteControl *WriteControl     `json:"writeControl,omitempty"`
}

type DocumentRequest struct {
	InsertText     *InsertTextRequest     `json:"insertText,omitempty"`
	DeleteContent  *DeleteContentRequest  `json:"deleteContentRange,omitempty"`
	ReplaceAllText *ReplaceAllTextRequest `json:"replaceAllText,omitempty"`
}

type InsertTextRequest struct {
	Text     string   `json:"text"`
	Location Location `json:"location"`
}

type DeleteContentRequest struct {
	Range Range `json:"range"`
}

type ReplaceAllTextRequest struct {
	ContainsText ContainsText `json:"containsText"`
	ReplaceText  string       `json:"replaceText"`
}

type ContainsText struct {
	Text      string `json:"text"`
	MatchCase bool   `json:"matchCase,omitempty"`
}

type Location struct {
	Index int `json:"index"`
}

type Range struct {
	StartIndex int `json:"startIndex"`
	EndIndex   int `json:"endIndex"`
}

type WriteControl struct {
	RequiredRevisionID string `json:"requiredRevisionId,omitempty"`
	TargetRevisionID   string `json:"targetRevisionId,omitempty"`
}

type BatchUpdateDocumentResponse struct {
	DocumentID      string           `json:"documentId,omitempty"`
	WriteControl    WriteControl     `json:"writeControl,omitzero"`
	Replies         []map[string]any `json:"replies,omitempty"`
	AppliedRequests int              `json:"applied_requests,omitempty"`
}

type DriveFile struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	MIMEType       string `json:"mimeType,omitempty"`
	WebViewLink    string `json:"webViewLink,omitempty"`
	WebContentLink string `json:"webContentLink,omitempty"`
	ModifiedTime   string `json:"modifiedTime,omitempty"`
}

type CopyDriveFileOptions struct {
	Name     string
	ParentID string
}

type UploadDriveFileOptions struct {
	Name     string
	ParentID string
	MIMEType string
	Content  []byte
}

type DriveFilesResponse struct {
	Files         []DriveFile `json:"files,omitempty"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

type DrivePermission struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
	Role string `json:"role,omitempty"`
}
