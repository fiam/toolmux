package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
)

func TestVersionCommand(t *testing.T) {
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() == "" {
		t.Fatal("expected version output")
	}
}

func TestPolicyCatalog(t *testing.T) {
	cmd := NewRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"policy", "catalog"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("gmail.send")) {
		t.Fatalf("expected gmail.send in catalog, got %q", out.String())
	}
}

func TestStatusShowsProviderPermissions(t *testing.T) {
	store := credentials.NewMemoryStore()
	err := store.SaveOAuthTokens(context.Background(), credentials.ConnectionRef{
		Profile:   "default",
		Provider:  "linear",
		AccountID: "default",
	}, credentials.OAuthTokens{
		AccessToken: "linear-access-token",
		TokenType:   "bearer",
		Scopes:      []string{"read", "issues:create"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"status", "linear"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("connected")) || !bytes.Contains(out.Bytes(), []byte("issues:create")) {
		t.Fatalf("expected connected status with permissions, got %q", out.String())
	}
}

func TestDoctorRunsCoreAndProviderDiagnostics(t *testing.T) {
	store := credentials.NewMemoryStore()
	cmd := NewRootCommandWithDeps(Dependencies{Credentials: store})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"doctor", "linear"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("credential-store")) || !bytes.Contains(out.Bytes(), []byte("not connected")) {
		t.Fatalf("expected doctor diagnostics, got %q", out.String())
	}
}
