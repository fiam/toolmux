package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fiam/supacli/internal/server"
	"github.com/fiam/supacli/internal/testutil/supaclidtest"
)

func TestHealthz(t *testing.T) {
	t.Parallel()
	supaclid := supaclidtest.New(t, server.Config{})

	resp, err := supaclid.Client().Get(supaclid.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBuildInfoJSON(t *testing.T) {
	t.Parallel()
	supaclid := supaclidtest.New(t, server.Config{})

	resp, err := supaclid.Client().Get(supaclid.URL + "/build")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}

	var build struct {
		Service   string `json:"service"`
		Version   string `json:"version"`
		GoVersion string `json:"go_version"`
		Module    string `json:"module"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&build); err != nil {
		t.Fatal(err)
	}
	if build.Service != "supaclid" {
		t.Fatalf("service mismatch: %q", build.Service)
	}
	if build.Version == "" {
		t.Fatal("expected version")
	}
	if build.GoVersion == "" {
		t.Fatal("expected go version")
	}
	if build.Module == "" {
		t.Fatal("expected module path")
	}
}

func TestBuildInfoText(t *testing.T) {
	t.Parallel()
	supaclid := supaclidtest.New(t, server.Config{})

	req, err := http.NewRequest(http.MethodGet, supaclid.URL+"/build", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := supaclid.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("expected text content type, got %q", contentType)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(body)
	if !strings.Contains(rendered, "service: supaclid\n") || !strings.Contains(rendered, "version: ") {
		t.Fatalf("unexpected build info text:\n%s", rendered)
	}
}
