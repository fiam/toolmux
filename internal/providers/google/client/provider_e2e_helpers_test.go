package client_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/cli"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/providers/brokers"
	_ "github.com/fiam/toolmux/internal/providers/google/broker"
	"github.com/fiam/toolmux/internal/providers/google/googleapi"
	"github.com/fiam/toolmux/internal/server"
	"github.com/fiam/toolmux/internal/testutil/toolmuxdtest"
)

func googleBrokerDeps(t testing.TB, store credentials.Store, upstream *fakeGoogleUpstream) cli.Dependencies {
	t.Helper()
	toolmuxd := toolmuxdtest.New(t, server.Config{
		Providers: map[actions.ProviderName]brokers.Config{
			"google": {
				ClientID:   "google-client",
				Secret:     "google-secret",
				AuthURL:    upstream.Server.URL + "/oauth/authorize",
				TokenURL:   upstream.Server.URL + "/token",
				RevokeURL:  upstream.Server.URL + "/revoke",
				Scopes:     []string{googleapi.ScopeDriveFile},
				HTTPClient: upstream.Server.Client(),
			},
		},
		HTTPClient: upstream.Server.Client(),
	})
	deps := googleDeps(t, store, toolmuxd.Client(), upstream.Server.URL)
	deps.ToolmuxdURL = toolmuxd.URL
	return deps
}

func googleDeps(t testing.TB, store credentials.Store, client *http.Client, upstreamURL string) cli.Dependencies {
	t.Helper()
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".toolmux"), 0o750); err != nil {
		t.Fatal(err)
	}
	config := []byte("version: 1\ntoolboxes:\n  google:\n    type: internal\n    provider: google\n")
	if err := os.WriteFile(filepath.Join(workDir, ".toolmux", "config.yaml"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	return cli.Dependencies{
		Credentials: store,
		HTTPClient:  client,
		WorkDir:     workDir,
		ProviderURL: map[string]string{
			"google": upstreamURL,
		},
	}
}

func followURL(client *http.Client) func(string) error {
	return func(rawURL string) error {
		resp, err := client.Get(rawURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("browser URL returned status %d", resp.StatusCode)
		}
		return nil
	}
}

func brokeredPickerSelectionBrowser(t testing.TB, client *http.Client) func(string) error {
	t.Helper()
	return func(rawURL string) error {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		query := parsed.Query()
		if query.Get("trigger_onepick") != "true" {
			return fmt.Errorf("brokered picker URL did not enable one-pick")
		}
		if query.Get("scope") != googleapi.ScopeDriveFile {
			return fmt.Errorf("brokered picker URL requested unexpected scope %q", query.Get("scope"))
		}
		redirectURI, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			return err
		}
		if redirectURI.Path != "/oauth/google/callback" {
			return fmt.Errorf("brokered picker URL requested unexpected redirect path %q", redirectURI.Path)
		}
		resp, err := client.Get(rawURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("brokered picker flow returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if !strings.Contains(string(body), "Google Picker selection received") {
			return fmt.Errorf("brokered picker callback page did not confirm selection")
		}
		return nil
	}
}
