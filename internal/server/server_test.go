package server_test

import (
	"net/http"
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
