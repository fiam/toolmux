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
