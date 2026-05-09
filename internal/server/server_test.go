package server_test

import (
	"net/http"
	"testing"

	"github.com/fiam/toolmux/internal/server"
	"github.com/fiam/toolmux/internal/testutil/toolmuxdtest"
)

func TestHealthz(t *testing.T) {
	t.Parallel()
	toolmuxd := toolmuxdtest.New(t, server.Config{})

	resp, err := toolmuxd.Client().Get(toolmuxd.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
