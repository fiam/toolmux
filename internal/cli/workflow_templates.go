package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

func loadWorkflowTemplate(ctx context.Context, client *http.Client, source string) (workflowFile, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return workflowFile{}, fmt.Errorf("workflow template source is required")
	}
	if template, ok := workflowTemplateByName(source); ok {
		source = template.Source
	}
	if spec, ok := strings.CutPrefix(source, "github:"); ok {
		return loadGitHubWorkflowTemplate(ctx, client, spec)
	}
	if isMCPRemoteURLArgument(source) {
		return loadURLWorkflowTemplate(ctx, client, source)
	}
	return workflowFile{}, fmt.Errorf("unknown workflow template %q", source)
}

func loadGitHubWorkflowTemplate(ctx context.Context, client *http.Client, spec string) (workflowFile, error) {
	ref := "main"
	base, after, found := strings.Cut(spec, "@")
	if found {
		ref = after
	}
	parts := strings.SplitN(base, "/", 3)
	if len(parts) < 3 {
		return workflowFile{}, fmt.Errorf("GitHub workflow template must be github:owner/repo/path[@ref]")
	}
	path := strings.TrimPrefix(parts[2], "/")
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		path = strings.TrimRight(path, "/") + "/workflow.yaml"
	}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), url.PathEscape(ref), path)
	workflow, err := loadURLWorkflowTemplate(ctx, client, rawURL)
	if err != nil {
		return workflowFile{}, err
	}
	workflow.Template = &workflowTemplateProvenance{Source: "github:" + base, Ref: ref}
	return workflow, nil
}

func loadURLWorkflowTemplate(ctx context.Context, client *http.Client, rawURL string) (workflowFile, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return workflowFile{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return workflowFile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return workflowFile{}, fmt.Errorf("workflow template %s returned status %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return workflowFile{}, err
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return workflowFile{}, err
	}
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if workflow.Template == nil {
		workflow.Template = &workflowTemplateProvenance{Source: rawURL}
	}
	return workflow, validateWorkflow(workflow)
}

func workflowTemplateCatalog() []workflowTemplateEntry {
	return []workflowTemplateEntry{
		{
			Name:        "slack-recap",
			Description: "Summarize Slack channel activity since a time",
			Source:      workflowTemplateGitHubSource("slack-recap.yaml"),
		},
		{
			Name:        "test",
			Description: "Minimal smoke test that exits as soon as the agent replies",
			Source:      workflowTemplateGitHubSource("test.yaml"),
		},
		{
			Name:        "my-week",
			Description: "Recap your week across Jira, Notion, and incident.io",
			Source:      workflowTemplateGitHubSource("my-week.yaml"),
		},
		{
			Name:        "yesterday-standup",
			Description: "Three-bullet morning standup from yesterday's activity",
			Source:      workflowTemplateGitHubSource("yesterday-standup.yaml"),
		},
		{
			Name:        "focus-prep",
			Description: "Pick today's priorities and stash a Notion daily note",
			Source:      workflowTemplateGitHubSource("focus-prep.yaml"),
		},
		{
			Name:        "weekly-metrics-digest",
			Description: "Grafana + Jira + incident.io weekly digest in Notion",
			Source:      workflowTemplateGitHubSource("weekly-metrics-digest.yaml"),
		},
		{
			Name:        "error-budget-burn",
			Description: "SLO burn report correlated with incidents over a window",
			Source:      workflowTemplateGitHubSource("error-budget-burn.yaml"),
		},
		{
			Name:        "runbook-from-incident",
			Description: "Turn a resolved incident's resolution into a reusable Notion runbook",
			Source:      workflowTemplateGitHubSource("runbook-from-incident.yaml"),
		},
	}
}

func workflowTemplateByName(name string) (workflowTemplateEntry, bool) {
	name = strings.TrimSpace(name)
	for _, template := range workflowTemplateCatalog() {
		if template.Name == name {
			return template, true
		}
	}
	return workflowTemplateEntry{}, false
}

func workflowTemplateGitHubSource(name string) string {
	path := strings.Trim(strings.TrimSpace(name), "/")
	return fmt.Sprintf("github:%s/%s/%s@%s", workflowTemplateRepo, workflowTemplateFolder, path, workflowTemplateRef)
}
