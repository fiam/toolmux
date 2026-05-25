package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWritePickerDonePageUsesCallbackStyle(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writePickerDonePage(recorder, pickerDonePage{
		Title:   "Google Picker selection received",
		Message: "Toolmux has <ids>.",
		Success: true,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store cache control, got %q", got)
	}
	text := recorder.Body.String()
	for _, want := range []string{
		"color-scheme: dark",
		"toolmux google picker",
		"waiting for Google Picker selection",
		"OK</span> picker selection received",
		"OK</span> selected file IDs handed to Toolmux",
		"Toolmux has &lt;ids&gt;.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected Picker done page to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<ids>") {
		t.Fatalf("Picker done page did not escape dynamic message:\n%s", text)
	}
}
