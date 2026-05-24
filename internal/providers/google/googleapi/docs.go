package googleapi

import (
	"context"
	"net/url"
	"strings"
)

func (c Client) GetDocument(ctx context.Context, documentID string) (Document, error) {
	var out Document
	if err := c.get(ctx, "/docs/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID)), nil, &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

func (c Client) CreateDocument(ctx context.Context, title string) (Document, error) {
	var out Document
	body := map[string]string{"title": strings.TrimSpace(title)}
	if err := c.postJSON(ctx, "/docs/v1/documents", body, &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

func (c Client) BatchUpdateDocument(ctx context.Context, documentID string, request BatchUpdateDocumentRequest) (BatchUpdateDocumentResponse, error) {
	var out BatchUpdateDocumentResponse
	if err := c.postJSON(ctx, "/docs/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID))+":batchUpdate", request, &out); err != nil {
		return BatchUpdateDocumentResponse{}, err
	}
	out.AppliedRequests = len(request.Requests)
	return out, nil
}

func (c Client) BatchUpdateDocumentRaw(ctx context.Context, documentID string, request map[string]any) (BatchUpdateDocumentResponse, error) {
	var out BatchUpdateDocumentResponse
	if err := c.postJSON(ctx, "/docs/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID))+":batchUpdate", request, &out); err != nil {
		return BatchUpdateDocumentResponse{}, err
	}
	if requests, ok := request["requests"].([]any); ok {
		out.AppliedRequests = len(requests)
	}
	return out, nil
}

func (document Document) PlainText() string {
	var builder strings.Builder
	for _, element := range document.Body.Content {
		if element.Paragraph == nil {
			continue
		}
		for _, child := range element.Paragraph.Elements {
			if child.TextRun != nil {
				builder.WriteString(child.TextRun.Content)
			}
		}
	}
	return builder.String()
}

func (document Document) AppendIndex() int {
	index := 1
	for _, element := range document.Body.Content {
		if element.EndIndex > index {
			index = element.EndIndex
		}
	}
	if index > 1 {
		return index - 1
	}
	return index
}

func GoogleDocsMIMEType() string {
	return googleDocsMIME
}
