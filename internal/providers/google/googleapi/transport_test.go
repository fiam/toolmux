package googleapi

import "testing"

func TestDocsAPIURLUsesDocsHostForGoogleDefaultBase(t *testing.T) {
	t.Parallel()

	got := docsAPIURL(Client{BaseURL: DefaultAPIBaseURL}, "/v1/documents/doc-1")
	want := DefaultDocsAPIBaseURL + "/v1/documents/doc-1"
	if got != want {
		t.Fatalf("expected Docs API URL %q, got %q", want, got)
	}
}

func TestDocsAPIURLKeepsCustomBase(t *testing.T) {
	t.Parallel()

	got := docsAPIURL(Client{BaseURL: "https://example.test/google"}, "/v1/documents/doc-1")
	want := "https://example.test/google/v1/documents/doc-1"
	if got != want {
		t.Fatalf("expected custom Docs API URL %q, got %q", want, got)
	}
}

func TestSheetsAPIURLUsesSheetsHostForGoogleDefaultBase(t *testing.T) {
	t.Parallel()

	got := sheetsAPIURL(Client{BaseURL: DefaultAPIBaseURL}, "/v4/spreadsheets/sheet-1")
	want := DefaultSheetsBaseURL + "/v4/spreadsheets/sheet-1"
	if got != want {
		t.Fatalf("expected Sheets API URL %q, got %q", want, got)
	}
}

func TestSheetsAPIURLKeepsCustomBaseAndEscapedRange(t *testing.T) {
	t.Parallel()

	got := sheetsAPIURL(Client{BaseURL: "https://example.test/google"}, "/v4/spreadsheets/sheet-1/values/Sheet%201%21A1:B2")
	want := "https://example.test/google/v4/spreadsheets/sheet-1/values/Sheet%201%21A1:B2"
	if got != want {
		t.Fatalf("expected custom Sheets API URL %q, got %q", want, got)
	}
}
