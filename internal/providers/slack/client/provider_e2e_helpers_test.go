package slack_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/cli"
	"github.com/fiam/toolmux/internal/credentials"
	_ "github.com/fiam/toolmux/internal/providers/brokers/all"
)

func slackDeps(store credentials.Store, client *http.Client, upstreamURL string) cli.Dependencies {
	return cli.Dependencies{
		Credentials: store,
		HTTPClient:  client,
		ProviderURL: map[string]string{
			"slack": upstreamURL + "/api",
		},
	}
}

func runToolmuxWithInput(t testing.TB, deps cli.Dependencies, input string, args ...string) string {
	t.Helper()
	cmd := cli.NewRootCommandWithDeps(deps)
	out := &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(input + "\n"))
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("toolmux %s failed: %v\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
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
