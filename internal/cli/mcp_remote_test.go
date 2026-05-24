//nolint:paralleltest // These tests exercise process-global cwd and environment config discovery.
package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/credentials"
)

// These tests intentionally do not call t.Parallel because they exercise config
// discovery through process-global cwd and environment variables.

type mcpRemoteTestEnv struct {
	Home     string
	Config   string
	CacheDir string
	Store    *credentials.MemoryStore
}

func newMCPRemoteTestEnv(t *testing.T) mcpRemoteTestEnv {
	t.Helper()
	home := t.TempDir()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TOOLMUX_MCP_CACHE_DIR", cacheDir)
	config, err := globalToolmuxConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return mcpRemoteTestEnv{Home: home, Config: config, CacheDir: cacheDir, Store: credentials.NewMemoryStore()}
}

func rootForRemoteTest(env mcpRemoteTestEnv) *cobra.Command {
	return NewRootCommandWithDeps(Dependencies{
		Credentials: env.Store,
		Env: func(name string) string {
			if name == "TOOLMUX_MCP_CACHE_DIR" {
				return env.CacheDir
			}
			return os.Getenv(name)
		},
	})
}

func runRootForRemoteTest(t *testing.T, env mcpRemoteTestEnv, args ...string) string {
	t.Helper()
	return runRootForRemoteTestWithInput(t, env, "", args...)
}

func runRootForRemoteTestWithInput(t *testing.T, env mcpRemoteTestEnv, input string, args ...string) string {
	t.Helper()
	cmd := rootForRemoteTest(env)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	if input != "" {
		cmd.SetIn(strings.NewReader(input + "\n"))
	}
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func runRootForRemoteTestError(t *testing.T, env mcpRemoteTestEnv, args ...string) (string, error) {
	t.Helper()
	cmd := rootForRemoteTest(env)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func runRootForRemoteOAuthTest(t *testing.T, env mcpRemoteTestEnv, client *http.Client, args ...string) string {
	t.Helper()
	cmd := rootForRemoteTest(env)
	out := newOAuthFollowerWriter(t, client)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out.wait()
	return out.String()
}

type oauthFollowerWriter struct {
	t      *testing.T
	client *http.Client

	mu   sync.Mutex
	buf  bytes.Buffer
	once sync.Once
	done chan error
}

func newOAuthFollowerWriter(t *testing.T, client *http.Client) *oauthFollowerWriter {
	t.Helper()
	if client == nil {
		client = http.DefaultClient
	}
	return &oauthFollowerWriter{t: t, client: client, done: make(chan error, 1)}
}

func (w *oauthFollowerWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buf.Write(data)
	text := w.buf.String()
	authURL := firstHTTPURL(text)
	w.mu.Unlock()
	if authURL != "" {
		w.once.Do(func() {
			go func() {
				resp, err := w.client.Get(authURL)
				if err != nil {
					w.done <- err
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				err = resp.Body.Close()
				if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
					err = fmt.Errorf("authorization URL returned status %d", resp.StatusCode)
				}
				w.done <- err
			}()
		})
	}
	return len(data), nil
}

func (w *oauthFollowerWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *oauthFollowerWriter) wait() {
	w.t.Helper()
	select {
	case err := <-w.done:
		if err != nil {
			w.t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		w.t.Fatal("timed out following OAuth authorization URL")
	}
}

func firstHTTPURL(text string) string {
	for field := range strings.FieldsSeq(text) {
		field = strings.TrimRight(field, ".,)")
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			return field
		}
	}
	return ""
}

func writeRemoteTestConfig(t *testing.T, env mcpRemoteTestEnv, servers map[string]mcpRemoteServer) {
	t.Helper()
	if err := writeToolmuxConfigFile(env.Config, toolmuxConfigFile{
		Version: 1,
		MCP: mcpConfig{
			Servers: servers,
		},
	}); err != nil {
		t.Fatal(err)
	}
}
