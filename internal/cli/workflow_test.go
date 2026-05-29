//nolint:paralleltest // These tests isolate Toolmux home with process-global HOME.
package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
)

func TestWorkflowRenderSlackRecapTemplate(t *testing.T) {
	env := newWorkflowTestEnv(t)
	template, err := readWorkflowFile(filepath.Join("..", "..", "workflows", "slack-recap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeWorkflowTestFile(t, env.WorkDir, template)

	output, err := runWorkflowRoot(t, env, "workflow", "render", "slack-recap", "--input", "since=8h", "--pager", "never")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"the last 8h0m0s",
		"channel types: public_channel",
		"Do not inspect the\nlocal toolmux CLI",
		"use the available Slack tool descriptions as the source of truth",
		"Send the final recap to yourself as a Slack\nDM",
		"Do not pass a U...\nSlack user ID directly to the message-sending tool",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected rendered prompt to contain %q, got:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"slack.auth_test", "slack.channels_list", "slack.conversations_add_message"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("expected rendered prompt not to contain raw tool name %q, got:\n%s", unwanted, output)
		}
	}

	_, err = runWorkflowRoot(t, env, "workflow", "render", "slack-recap")
	if err == nil || !strings.Contains(err.Error(), "missing required input since") {
		t.Fatalf("expected missing input error, got %v", err)
	}
}

func TestWorkflowTemplatesListRepositorySources(t *testing.T) {
	env := newWorkflowTestEnv(t)
	output, err := runWorkflowRoot(t, env, "-o", "json", "workflow", "templates")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"name": "slack-recap"`) {
		t.Fatalf("expected slack-recap template, got %s", output)
	}
	if !strings.Contains(output, `"source": "github:fiam/toolmux/workflows/slack-recap.yaml@main"`) {
		t.Fatalf("expected GitHub template source, got %s", output)
	}
}

func TestWorkflowInitLoadsTemplateFromURL(t *testing.T) {
	env := newWorkflowTestEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(`version: 1
name: url-template
description: URL template
prompt: |
  hello
`))
	}))
	defer server.Close()
	env.HTTPClient = server.Client()

	output, err := runWorkflowRoot(t, env, "workflow", "init", "url-template", "--project", "--template", server.URL, "--no-setup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "created project workflow url-template") {
		t.Fatalf("unexpected init output: %s", output)
	}
	if _, err := os.Stat(filepath.Join(env.WorkDir, workflowProjectRelDir, "url-template.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRunRequiresAgent(t *testing.T) {
	env := newWorkflowTestEnv(t)
	installWorkflowFixture(t, env.WorkDir, "hello.yaml")

	_, err := runWorkflowRoot(t, env, "workflow", "run", "hello", "--input", "name=world", "--no-setup")
	if err == nil || !strings.Contains(err.Error(), "has no agent") {
		t.Fatalf("expected no-agent error, got %v", err)
	}
}

func TestWorkflowRunAgentFlagAcceptsArguments(t *testing.T) {
	env := newWorkflowTestEnv(t)
	installWorkflowFixture(t, env.WorkDir, "echo.yaml")

	output, err := runWorkflowRoot(t, env,
		"workflow", "run", "echo",
		"--input", "name=world",
		"--agent", `/bin/sh -c 'printf %s "$1"' sh`,
		"--no-setup",
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != "hello world" {
		t.Fatalf("expected --agent args to receive prompt, got %q", output)
	}
}

func TestWorkflowRunAutoSelectsSingleDiscoveredAgent(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.Discoverer = fakeWorkflowAgent(t, `printf 'hello from fake: %s' "$PROMPT"`)
	installWorkflowFixture(t, env.WorkDir, "auto.yaml")

	output, err := runWorkflowRoot(t, env, "workflow", "run", "auto", "--no-setup")
	if err != nil {
		t.Fatal(err)
	}
	if output != "hello from fake: ping" {
		t.Fatalf("expected fake agent output, got %q", output)
	}
}

func TestWorkflowRunMultiStepBindsPreviousOutputs(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.Discoverer = fakeWorkflowAgent(t, `
case "$TOOLMUX_FAKE_STEP" in
1) printf 'fetched 3 items\n{"count": 3}\n' ;;
2) printf 'summary: %s\n' "$PROMPT" ;;
esac
`)
	installWorkflowFixture(t, env.WorkDir, "twostep.yaml")

	output, err := runWorkflowRoot(t, env, "workflow", "run", "twostep", "--no-setup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "items: 3") {
		t.Fatalf("expected previous.outputs.count to be 3 in summary prompt, got %q", output)
	}
}

func TestWorkflowFileRejectsAgentField(t *testing.T) {
	env := newWorkflowTestEnv(t)
	path := filepath.Join(env.WorkDir, workflowProjectRelDir, "rogue.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`version: 1
name: rogue
agent: codex
prompt: hi
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runWorkflowRoot(t, env, "workflow", "list")
	if err == nil || !strings.Contains(err.Error(), "`agent:` is no longer supported") {
		t.Fatalf("expected rejection of agent field, got %v", err)
	}
}

func TestWorkflowAgentByNameParsesKnownAgentArgs(t *testing.T) {
	agent, err := workflowAgentByName("codex --yolo", workflowConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Command != "codex" || len(agent.Args) != 1 || agent.Args[0] != "--yolo" {
		t.Fatalf("unexpected agent config: %#v", agent)
	}
	_, args, err := workflowAgentCommand(agent, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "--yolo" || args[1] != "prompt" {
		t.Fatalf("expected yolo arg plus appended prompt, got %#v", args)
	}
}

func TestWorkflowAgentByNameInjectsHeadlessDefaults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    []string
		command string
	}{
		{name: "claude", command: "claude", want: []string{"-p", "prompt"}},
		{name: "codex", command: "codex", want: []string{"exec", "prompt"}},
		{name: "claude-code", command: "claude", want: []string{"-p", "prompt"}},
	} {
		agent, err := workflowAgentByName(tc.name, workflowConfig{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if agent.Command != tc.command {
			t.Fatalf("%s: expected command %s, got %s", tc.name, tc.command, agent.Command)
		}
		_, args, err := workflowAgentCommand(agent, "prompt")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(args, tc.want) {
			t.Fatalf("%s: expected args %v, got %v", tc.name, tc.want, args)
		}
	}
}

func TestWorkflowRunAcceptsYAMLPath(t *testing.T) {
	env := newWorkflowTestEnv(t)
	path := workflowFixturePath(t, "ad-hoc.yaml")

	output, err := runWorkflowRoot(t, env,
		"workflow", "run", path,
		"--input", "name=path",
		"--agent", `/bin/sh -c 'printf %s "$1"' sh`,
		"--no-setup",
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != "hello path" {
		t.Fatalf("expected agent stdout to receive prompt from file, got %q", output)
	}
}

func TestWorkflowConfigSetDefaultAgentRequiresAgentOutsideInteractive(t *testing.T) {
	env := newWorkflowTestEnv(t)
	_, err := runWorkflowRoot(t, env, "workflow", "config", "set", "default-agent")
	if err == nil || !strings.Contains(err.Error(), "default workflow agent is required") {
		t.Fatalf("expected default agent error, got %v", err)
	}
}

type workflowTestEnv struct {
	WorkDir    string
	Store      *credentials.MemoryStore
	HTTPClient *http.Client
	Discoverer func(WorkflowConfigSnapshot) []WorkflowAgentCandidate
}

func newWorkflowTestEnv(t *testing.T) workflowTestEnv {
	t.Helper()
	home := t.TempDir()
	workDir := filepath.Join(home, "work")
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return workflowTestEnv{
		WorkDir: workDir,
		Store:   credentials.NewMemoryStore(),
	}
}

func writeWorkflowTestFile(t *testing.T, workDir string, workflow workflowFile) {
	t.Helper()
	if err := writeWorkflowFile(filepath.Join(workDir, workflowProjectRelDir, workflow.Name+".yaml"), workflow); err != nil {
		t.Fatal(err)
	}
}

// installWorkflowFixture copies a YAML file from testdata/workflows into the
// test env's project workflow directory.
func installWorkflowFixture(t *testing.T, workDir, fixtureName string) string {
	t.Helper()
	src := filepath.Join("testdata", "workflows", fixtureName)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(workDir, workflowProjectRelDir, fixtureName)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dest
}

// workflowFixturePath returns the absolute path of a testdata workflow
// without copying it. Use this when a test needs to pass a path directly
// to `workflow run`.
func workflowFixturePath(t *testing.T, fixtureName string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "workflows", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func runWorkflowRoot(t *testing.T, env workflowTestEnv, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommandWithDeps(Dependencies{
		Credentials:             env.Store,
		HTTPClient:              env.HTTPClient,
		WorkDir:                 env.WorkDir,
		WorkflowAgentDiscoverer: env.Discoverer,
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}
