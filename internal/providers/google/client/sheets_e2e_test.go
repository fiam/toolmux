package client_test

import (
	"context"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/testutil/toolmuxtest"
)

func TestGoogleSheetsDryRunDoesNotRequireCredentials(t *testing.T) {
	t.Parallel()

	upstream := newFakeGoogleUpstream(t)
	deps := googleDeps(t, credentials.NewMemoryStore(), upstream.Server.Client(), upstream.Server.URL)
	out := toolmuxtest.Run(t, deps, "--output", "json", "google", "sheets", "values", "update", "sheet-1",
		"--range", "Sheet1!A1", "--values-json", `[["preview"]]`, "--dry-run",
	)
	toolmuxtest.AssertContains(t, out, `"dry_run": true`)
	toolmuxtest.AssertContains(t, out, `"preview"`)
}

func TestGoogleSheetsRequiresDriveFileScopeBeforeCallingSheets(t *testing.T) {
	t.Parallel()

	upstream := newFakeGoogleUpstream(t)
	store := credentials.NewMemoryStore()
	deps := googleDeps(t, store, upstream.Server.Client(), upstream.Server.URL)
	ref := credentials.ConnectionRef{Profile: "default", Provider: "google", AccountID: "google"}
	if err := store.SaveOAuthTokens(context.Background(), ref, credentials.OAuthTokens{
		AccessToken: "ya29.docs",
		TokenType:   "Bearer",
		Scopes:      []string{googleapi.ScopeDocs},
	}); err != nil {
		t.Fatal(err)
	}

	result := toolmuxtest.RunResult(t, deps, "google", "sheets", "values", "get", "sheet-1", "--range", "Sheet1!A1")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "missing Google OAuth scope") {
		t.Fatalf("expected missing drive.file scope error, got output %q and error %v", result.Output, result.Err)
	}
	request := upstream.lastSheetsRequest(t)
	if request.method != "" {
		t.Fatalf("Sheets API was called before scope validation: %#v", request)
	}
}

func TestGoogleSheetsCreateGetAndReadValues(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	out := toolmuxtest.Run(t, deps, "google", "sheets", "create",
		"--title", "Quarterly plan", "--sheet", "Summary", "--sheet", "Data",
		"--rows", "200", "--columns", "12", "--locale", "en_US", "--time-zone", "Europe/Lisbon",
	)
	for _, want := range []string{"sheet-new", "Quarterly plan", "Sheet1"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	request := upstream.lastSheetsRequest(t)
	if request.method != "POST" || request.path != "/v4/spreadsheets" || request.token != "ya29.drive" {
		t.Fatalf("unexpected Sheets create request: %#v", request)
	}
	properties, _ := request.body["properties"].(map[string]any)
	if properties["title"] != "Quarterly plan" || properties["timeZone"] != "Europe/Lisbon" {
		t.Fatalf("unexpected Sheets create properties: %#v", properties)
	}

	out = toolmuxtest.Run(t, deps, "google", "sheets", "get", "https://docs.google.com/spreadsheets/d/sheet-1/edit")
	for _, want := range []string{"sheet-1", "Support metrics", "Sheet1", "1000", "26"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	out = toolmuxtest.Run(t, deps, "--output", "json", "google", "sheets", "get", "sheet-1")
	toolmuxtest.AssertContains(t, out, `"sheetId": 0`)

	out = toolmuxtest.Run(t, deps, "google", "sheets", "values", "get", "sheet-1",
		"--range", "Sheet1!A1:B2", "--value-render-option", "UNFORMATTED_VALUE",
	)
	for _, want := range []string{"Sheet1!A1:B2", "Name", "Count", "Open", "12"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	request = upstream.lastSheetsRequest(t)
	if request.method != "GET" || request.query.Get("valueRenderOption") != "UNFORMATTED_VALUE" || request.query.Get("ranges") != "Sheet1!A1:B2" {
		t.Fatalf("unexpected Sheets values read request: %#v", request)
	}
}

func TestGoogleSheetsValueWritesAcceptJSONCSVAndTSV(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	out := toolmuxtest.Run(t, deps, "google", "sheets", "values", "update", "sheet-1",
		"--range", "Sheet 1!A1:B2", "--values-json", `[["Name","Count"],["Open",12]]`,
	)
	for _, want := range []string{"sheet-1", "Sheet 1!A1:B2", "4"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	request := upstream.lastSheetsRequest(t)
	if request.method != "PUT" || request.path != "/v4/spreadsheets/sheet-1/values/Sheet 1!A1:B2" || request.query.Get("valueInputOption") != "RAW" {
		t.Fatalf("unexpected Sheets values update request: %#v", request)
	}
	if request.body["majorDimension"] != "ROWS" {
		t.Fatalf("unexpected update body: %#v", request.body)
	}

	csvPath := filepath.Join(t.TempDir(), "values.csv")
	if err := os.WriteFile(csvPath, []byte("Name,Count\nClosed,8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = toolmuxtest.Run(t, deps, "--output", "json", "google", "sheets", "values", "update", "sheet-1",
		"--range", "Sheet1!A1:B2", "--file", csvPath, "--value-input-option", "USER_ENTERED", "--dry-run",
	)
	for _, want := range []string{`"dry_run": true`, `"Closed"`, `"valueInputOption": "USER_ENTERED"`} {
		toolmuxtest.AssertContains(t, out, want)
	}
	if strings.Contains(out, `"ValueInputOption"`) {
		t.Fatalf("dry-run output exposed a Go field name: %s", out)
	}

	tsvPath := filepath.Join(t.TempDir(), "append.tsv")
	if err := os.WriteFile(tsvPath, []byte("Pending\t3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = toolmuxtest.Run(t, deps, "google", "sheets", "values", "append", "sheet-1",
		"--range", "Sheet1!A:B", "--file", tsvPath,
	)
	for _, want := range []string{"sheet-1", "Sheet1!A3:B3", "2"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	request = upstream.lastSheetsRequest(t)
	if request.method != "POST" || !strings.HasSuffix(request.path, ":append") || request.query.Get("insertDataOption") != "INSERT_ROWS" {
		t.Fatalf("unexpected Sheets values append request: %#v", request)
	}

	out = toolmuxtest.Run(t, deps, "google", "sheets", "values", "clear", "sheet-1",
		"--range", "Sheet1!A2:B2", "--range", "Sheet1!D:D",
	)
	for _, want := range []string{"Sheet1!A2:B2", "Sheet1!D:D"} {
		toolmuxtest.AssertContains(t, out, want)
	}

	out = toolmuxtest.Run(t, deps, "google", "sheets", "values", "batch-update", "sheet-1",
		"--json", `[{"range":"Sheet1!A1:B2","values":[[1,2],[3,4]]}]`,
	)
	for _, want := range []string{"sheet-1", "Updated cells", "4"} {
		toolmuxtest.AssertContains(t, out, want)
	}
	request = upstream.lastSheetsRequest(t)
	if request.body["valueInputOption"] != "RAW" {
		t.Fatalf("expected RAW batch values request, got %#v", request.body)
	}
}

func TestGoogleSheetsStructuralCommandsAndRawBatchUpdate(t *testing.T) {
	t.Parallel()
	upstream := newFakeGoogleUpstream(t)
	deps := googleDriveTokenDeps(t, upstream)

	out := toolmuxtest.Run(t, deps, "google", "sheets", "tabs", "add", "sheet-1", "--title", "Archive")
	toolmuxtest.AssertContains(t, out, "sheet-1")
	toolmuxtest.AssertContains(t, out, "Applied requests")
	request := upstream.lastSheetsRequest(t)
	if request.method != "POST" || request.path != "/v4/spreadsheets/sheet-1:batchUpdate" {
		t.Fatalf("unexpected Sheets structural request: %#v", request)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "delete tab", args: []string{"tabs", "delete", "sheet-1", "--sheet-id", "0"}, want: "deleteSheet"},
		{name: "rename tab", args: []string{"tabs", "rename", "sheet-1", "--sheet-id", "0", "--title", "Renamed"}, want: "updateSheetProperties"},
		{name: "insert rows", args: []string{"rows", "insert", "sheet-1", "--sheet-id", "0", "--start", "2", "--count", "3"}, want: "insertDimension"},
		{name: "delete rows", args: []string{"rows", "delete", "sheet-1", "--sheet-id", "0", "--start", "2", "--count", "3"}, want: "deleteDimension"},
		{name: "insert columns", args: []string{"columns", "insert", "sheet-1", "--sheet-id", "0", "--start", "2"}, want: "insertDimension"},
		{name: "delete columns", args: []string{"columns", "delete", "sheet-1", "--sheet-id", "0", "--start", "2"}, want: "deleteDimension"},
		{name: "format", args: []string{"format-range", "sheet-1", "--sheet-id", "0", "--range", "A1:D5", "--bold", "--background-color", "#336699"}, want: "repeatCell"},
		{name: "merge", args: []string{"merge-cells", "sheet-1", "--sheet-id", "0", "--range", "A1:D1"}, want: "mergeCells"},
		{name: "unmerge", args: []string{"unmerge-cells", "sheet-1", "--sheet-id", "0", "--range", "A1:D1"}, want: "unmergeCells"},
		{name: "protect", args: []string{"protected-ranges", "add", "sheet-1", "--sheet-id", "0", "--range", "A1:D1", "--editor-user", "owner@example.com"}, want: "addProtectedRange"},
		{name: "delete protection", args: []string{"protected-ranges", "delete", "sheet-1", "--protected-range-id", "3"}, want: "deleteProtectedRange"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"--output", "json", "google", "sheets"}, test.args...)
			args = append(args, "--dry-run")
			output := toolmuxtest.Run(t, deps, args...)
			toolmuxtest.AssertContains(t, output, `"dry_run": true`)
			toolmuxtest.AssertContains(t, output, test.want)
		})
	}

	invalidInsert := toolmuxtest.RunResult(t, deps, "google", "sheets", "rows", "insert", "sheet-1",
		"--sheet-id", "0", "--start", "1", "--inherit-from-before", "--dry-run",
	)
	if invalidInsert.Err == nil || !strings.Contains(invalidInsert.Err.Error(), "first row") {
		t.Fatalf("expected first-row inheritance validation, got output %q and error %v", invalidInsert.Output, invalidInsert.Err)
	}

	out = toolmuxtest.Run(t, deps, "google", "sheets", "batch-update", "sheet-1",
		"--json", `[{"updateSpreadsheetProperties":{"properties":{"title":"Renamed workbook"},"fields":"title"}}]`,
	)
	for _, want := range []string{"sheet-1", "Applied requests", "1"} {
		toolmuxtest.AssertContains(t, out, want)
	}
}

type sheetsRequestSnapshot struct {
	method string
	path   string
	query  url.Values
	body   map[string]any
	token  string
}

func (s *fakeGoogleUpstream) lastSheetsRequest(t *testing.T) sheetsRequestSnapshot {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	query := url.Values{}
	for key, values := range s.lastSheetsQuery {
		query[key] = append([]string(nil), values...)
	}
	return sheetsRequestSnapshot{
		method: s.lastSheetsMethod,
		path:   s.lastSheetsPath,
		query:  query,
		body:   cloneAnyMap(s.lastSheetsBody),
		token:  s.lastSheetsAPIToken,
	}
}

func cloneAnyMap(source map[string]any) map[string]any {
	return maps.Clone(source)
}
