package client

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

func handleDocsGet(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	document, err := client.GetDocument(exec.Context, documentID)
	if err != nil {
		return nil, err
	}
	return newDocsDocumentResult(document, inv.Bool("include-structure")), nil
}

func handleDocsAppend(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	text, err := requiredFlagValue(inv, "text")
	if err != nil {
		return nil, err
	}
	document, err := client.GetDocument(exec.Context, documentID)
	if err != nil {
		return nil, err
	}
	request := googleapi.BatchUpdateDocumentRequest{
		Requests: []googleapi.DocumentRequest{{
			InsertText: &googleapi.InsertTextRequest{
				Location: googleapi.Location{Index: document.AppendIndex()},
				Text:     text,
			},
		}},
		WriteControl: docsWriteControl(inv),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	response, err := client.BatchUpdateDocument(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return docsBatchUpdateResult(response), nil
}

func handleDocsReplaceAllText(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	text, err := requiredFlagValue(inv, "text")
	if err != nil {
		return nil, err
	}
	request := googleapi.BatchUpdateDocumentRequest{
		Requests: []googleapi.DocumentRequest{{
			ReplaceAllText: &googleapi.ReplaceAllTextRequest{
				ContainsText: googleapi.ContainsText{
					Text:      text,
					MatchCase: inv.Bool("match-case"),
				},
				ReplaceText: inv.String("replace-text"),
			},
		}},
		WriteControl: docsWriteControl(inv),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := googleClient(exec, inv, defaultDriveScopes)
	if err != nil {
		return nil, err
	}
	response, err := client.BatchUpdateDocument(exec.Context, documentID, request)
	if err != nil {
		return nil, err
	}
	return docsBatchUpdateResult(response), nil
}

func handleDocsBatchUpdate(exec actions.Context, inv actions.Invocation) (any, error) {
	documentID, err := googleDocumentID(inv)
	if err != nil {
		return nil, err
	}
	request, err := docsRawBatchUpdateRequest(exec, inv)
	if err != nil {
		return nil, err
	}
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

func googleDocumentID(inv actions.Invocation) (string, error) {
	flagValue := strings.TrimSpace(inv.String("document-id"))
	argValue := ""
	if len(inv.Args) > 0 {
		argValue = strings.TrimSpace(inv.Args[0])
	}
	switch {
	case flagValue != "" && argValue != "" && flagValue != argValue:
		return "", fmt.Errorf("pass the Google Docs document as either --document-id or a positional argument, not both")
	case flagValue != "":
		return googleDriveFileID(flagValue)
	case argValue != "":
		return googleDriveFileID(argValue)
	default:
		return "", fmt.Errorf("google docs document ID or URL is required")
	}
}

func requiredFlagValue(inv actions.Invocation, name string) (string, error) {
	value, _ := inv.Flags[name].(string)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func docsWriteControl(inv actions.Invocation) *googleapi.WriteControl {
	revisionID := strings.TrimSpace(inv.String("required-revision-id"))
	if revisionID == "" {
		return nil
	}
	return &googleapi.WriteControl{RequiredRevisionID: revisionID}
}

func docsRawBatchUpdateRequest(exec actions.Context, inv actions.Invocation) (map[string]any, error) {
	raw, err := docsRawJSON(exec, inv.String("json"))
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode Docs batchUpdate JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("docs batchUpdate JSON must contain one value")
	}
	request, err := docsBatchUpdateObject(decoded)
	if err != nil {
		return nil, err
	}
	if _, ok := request["requests"]; !ok {
		return nil, fmt.Errorf("docs batchUpdate JSON must include requests")
	}
	if revisionID := strings.TrimSpace(inv.String("required-revision-id")); revisionID != "" {
		writeControl, err := docsRawWriteControl(request)
		if err != nil {
			return nil, err
		}
		writeControl["requiredRevisionId"] = revisionID
		request["writeControl"] = writeControl
	}
	return request, nil
}

func docsRawJSON(exec actions.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("json is required")
	}
	if path, ok := strings.CutPrefix(raw, "@"); ok {
		path = strings.TrimSpace(path)
		if path == "" {
			return "", fmt.Errorf("json file path is required after @")
		}
		readFile := exec.ReadFile
		if readFile == nil {
			readFile = os.ReadFile
		}
		data, err := readFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return raw, nil
}

func docsBatchUpdateObject(decoded any) (map[string]any, error) {
	switch value := decoded.(type) {
	case map[string]any:
		return value, nil
	case []any:
		return map[string]any{"requests": value}, nil
	default:
		return nil, fmt.Errorf("docs batchUpdate JSON must be an object or requests array")
	}
}

func docsRawWriteControl(request map[string]any) (map[string]any, error) {
	raw, ok := request["writeControl"]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	writeControl, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("docs batchUpdate writeControl must be an object")
	}
	return writeControl, nil
}

func compactPlainText(value string) string {
	value = strings.TrimRight(value, "\n")
	runes := []rune(value)
	if len(runes) <= 800 {
		return value
	}
	return string(runes[:800]) + "\n... truncated ..."
}
