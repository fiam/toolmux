package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
)

type PageRead struct {
	Page              Page         `json:"page"`
	Markdown          PageMarkdown `json:"markdown"`
	FollowLinks       bool         `json:"follow_links,omitempty"`
	IncludeTranscript bool         `json:"include_transcript,omitempty"`
}

type PageFull struct {
	Page     Page         `json:"page"`
	Markdown PageMarkdown `json:"markdown"`
}

type PageLink struct {
	Index        int    `json:"index"`
	Label        string `json:"label"`
	URL          string `json:"url"`
	Kind         string `json:"kind"`
	NotionPageID string `json:"notion_page_id,omitempty"`
}

type PageLinks []PageLink

type PageChild struct {
	Depth       int    `json:"depth"`
	Title       string `json:"title"`
	ID          string `json:"id"`
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	HasChildren bool   `json:"has_children"`
}

type PageChildren struct {
	Children []PageChild `json:"children"`
	Empty    string      `json:"-"`
}

type PageOpen struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	URLOnly bool   `json:"url_only,omitempty"`
}

type DiagnosticResult []actions.Diagnostic

func ActionHandlers() map[string]actions.Handler {
	return map[string]actions.Handler{
		ActionID(ActionSearch):              searchAction,
		ActionID(ActionPageGet):             pageGetAction,
		ActionID(ActionPageRead):            pageReadAction,
		ActionID(ActionPageLinks):           pageLinksAction,
		ActionID(ActionPageOpen):            pageOpenAction,
		ActionID(ActionPageChildren):        pageChildrenAction,
		ActionID(ActionPageTree):            pageTreeAction,
		ActionID(ActionPageDoctor):          pageDoctorAction,
		ActionID(ActionPageMarkdown):        pageMarkdownAction,
		ActionID(ActionPageCreate):          pageCreateAction,
		ActionID(ActionPageUpdate):          pageUpdateAction,
		ActionID(ActionPageContentInsert):   pageContentInsertAction,
		ActionID(ActionPageContentReplace):  pageContentReplaceAction,
		ActionID(ActionPageContentUpdate):   pageContentUpdateAction,
		ActionID(ActionPageDelete):          pageDeleteAction,
		ActionID(ActionPageRestore):         pageRestoreAction,
		ActionID(ActionPageMove):            pageMoveAction,
		ActionID(ActionDataSourceQuery):     dataSourceQueryAction,
		ActionID(ActionDataSourceSchema):    dataSourceSchemaAction,
		ActionID(ActionDataSourceRowCreate): dataSourceRowCreateAction,
		ActionID(ActionDataSourceRowUpdate): dataSourceRowUpdateAction,
		ActionID(ActionDatabaseDataSources): databaseDataSourcesAction,
	}
}

func ActionID(name actions.LocalName) string {
	return string(ProviderName) + "." + string(name)
}

func searchAction(exec actions.Context, inv actions.Invocation) (any, error) {
	query := inv.String("query")
	if query == "" && len(inv.Args) > 0 {
		query = inv.Args[0]
	}
	request := SearchRequest{
		Query:      query,
		ObjectType: inv.String("type"),
	}
	if sortBy := strings.TrimSpace(inv.String("sort")); sortBy != "" && sortBy != "none" {
		request.Sort = &SearchSort{Timestamp: sortBy, Direction: inv.String("direction")}
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.SearchAll(actionContext(exec), request, inv.Int("limit"))
}

func pageGetAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	switch inv.String("format") {
	case "properties", "":
		return client.RetrievePage(actionContext(exec), pageID, inv.StringSlice("filter-property"))
	case "markdown":
		return client.RetrievePageMarkdown(actionContext(exec), pageID, inv.Bool("include-transcript"))
	case "full":
		page, err := client.RetrievePage(actionContext(exec), pageID, inv.StringSlice("filter-property"))
		if err != nil {
			return nil, err
		}
		markdown, err := client.RetrievePageMarkdown(actionContext(exec), pageID, inv.Bool("include-transcript"))
		if err != nil {
			return nil, err
		}
		return PageFull{Page: page, Markdown: markdown}, nil
	default:
		return nil, fmt.Errorf("--format must be properties, markdown, or full")
	}
}

func pageReadAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	result, err := retrievePageRead(actionContext(exec), client, pageID, inv.Bool("include-transcript"))
	if err != nil {
		return nil, err
	}
	if inv.Bool("follow") {
		if !exec.Interactive {
			return nil, fmt.Errorf("--follow requires table output with interactive stdin, stdout, and stderr")
		}
		result.FollowLinks = true
	}
	result.IncludeTranscript = inv.Bool("include-transcript")
	return result, nil
}

func pageLinksAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	markdown, err := client.RetrievePageMarkdown(actionContext(exec), pageID, inv.Bool("include-transcript"))
	if err != nil {
		return nil, err
	}
	return pageLinksFromMarkdown(markdown.Markdown), nil
}

func pageOpenAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	page, err := client.RetrievePage(actionContext(exec), pageID, nil)
	if err != nil {
		return nil, err
	}
	pageURL := firstNonEmpty(page.URL, page.PublicURL, pageWebURL(page.ID))
	if pageURL == "" {
		return nil, fmt.Errorf("notion page %s has no URL", page.ID)
	}
	return PageOpen{ID: page.ID, URL: pageURL, URLOnly: inv.Bool("url-only")}, nil
}

func pageChildrenAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	children, err := collectPageChildren(actionContext(exec), client, pageID, 1, inv.Int("limit"))
	if err != nil {
		return nil, err
	}
	return PageChildren{Children: children, Empty: "no child pages"}, nil
}

func pageTreeAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	children, err := collectPageChildren(actionContext(exec), client, pageID, inv.Int("depth"), inv.Int("limit"))
	if err != nil {
		return nil, err
	}
	return PageChildren{Children: children, Empty: "no child pages"}, nil
}

func pageDoctorAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	result, err := retrievePageRead(actionContext(exec), client, pageID, inv.Bool("include-transcript"))
	if err != nil {
		return nil, err
	}
	return DiagnosticResult(pageDiagnostics(result)), nil
}

func pageMarkdownAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.RetrievePageMarkdown(actionContext(exec), pageID, inv.Bool("include-transcript"))
}

func pageCreateAction(exec actions.Context, inv actions.Invocation) (any, error) {
	body, err := readMarkdownInput(exec, inv.String("markdown"), inv.String("file"))
	if err != nil {
		return nil, err
	}
	parent, err := parseParent(inv.String("parent-type"), inv.String("parent"), true)
	if err != nil {
		return nil, err
	}
	request := CreatePageRequest{
		Parent:        parent,
		Title:         inv.String("title"),
		TitleProperty: inv.String("title-property"),
		Markdown:      body,
		Properties:    json.RawMessage(strings.TrimSpace(inv.String("properties-json"))),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.CreatePage(actionContext(exec), request)
}

func pageUpdateAction(exec actions.Context, inv actions.Invocation) (any, error) {
	request := UpdatePageRequest{
		Title:         inv.String("title"),
		TitleProperty: inv.String("title-property"),
		Properties:    json.RawMessage(strings.TrimSpace(inv.String("properties-json"))),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.UpdatePage(actionContext(exec), pageID, request)
}

func pageContentInsertAction(exec actions.Context, inv actions.Invocation) (any, error) {
	body, err := readMarkdownInput(exec, inv.String("markdown"), inv.String("file"))
	if err != nil {
		return nil, err
	}
	request := InsertMarkdownRequest{Content: body, After: inv.String("after")}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.InsertMarkdown(actionContext(exec), pageID, request)
}

func pageContentReplaceAction(exec actions.Context, inv actions.Invocation) (any, error) {
	body, err := readMarkdownInput(exec, inv.String("markdown"), inv.String("file"))
	if err != nil {
		return nil, err
	}
	request := ReplaceMarkdownRequest{
		NewString:            body,
		AllowDeletingContent: inv.Bool("allow-deleting-content"),
	}
	if !inv.Bool("yes") && !inv.Bool("dry-run") {
		return nil, fmt.Errorf("refusing to replace all Notion page content without --yes")
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.ReplaceMarkdown(actionContext(exec), pageID, request)
}

func pageContentUpdateAction(exec actions.Context, inv actions.Invocation) (any, error) {
	request := UpdateMarkdownRequest{
		ContentUpdates: []ContentUpdate{{
			OldString:         inv.String("old"),
			NewString:         inv.String("new"),
			ReplaceAllMatches: inv.Bool("replace-all"),
		}},
		AllowDeletingContent: inv.Bool("allow-deleting-content"),
	}
	if inv.Bool("allow-deleting-content") && !inv.Bool("yes") && !inv.Bool("dry-run") {
		return nil, fmt.Errorf("refusing to allow deleting child content without --yes")
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.UpdateMarkdown(actionContext(exec), pageID, request)
}

func pageDeleteAction(exec actions.Context, inv actions.Invocation) (any, error) {
	if !inv.Bool("yes") && !inv.Bool("dry-run") {
		return nil, fmt.Errorf("refusing to trash Notion page without --yes")
	}
	inTrash := true
	request := UpdatePageRequest{InTrash: &inTrash}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.UpdatePage(actionContext(exec), pageID, request)
}

func pageRestoreAction(exec actions.Context, inv actions.Invocation) (any, error) {
	inTrash := false
	request := UpdatePageRequest{InTrash: &inTrash}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.UpdatePage(actionContext(exec), pageID, request)
}

func pageMoveAction(exec actions.Context, inv actions.Invocation) (any, error) {
	parent, err := parseParent(inv.String("parent-type"), inv.String("parent"), false)
	if err != nil {
		return nil, err
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, map[string]any{"page": pageArg(inv.Args), "parent": parent}), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.MovePage(actionContext(exec), pageID, parent)
}

func dataSourceQueryAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.QueryDataSourceAll(actionContext(exec), inv.Args[0], QueryDataSourceRequest{
		PageSize:         inv.Int("limit"),
		FilterProperties: inv.StringSlice("filter-property"),
		Filter:           json.RawMessage(strings.TrimSpace(inv.String("filter-json"))),
		Sorts:            json.RawMessage(strings.TrimSpace(inv.String("sorts-json"))),
		ResultType:       inv.String("result-type"),
	}, inv.Int("limit"))
}

func dataSourceSchemaAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.RetrieveDataSource(actionContext(exec), inv.Args[0])
}

func dataSourceRowCreateAction(exec actions.Context, inv actions.Invocation) (any, error) {
	parent, err := DataSourceParent(inv.Args[0])
	if err != nil {
		return nil, err
	}
	body, err := readMarkdownInput(exec, inv.String("markdown"), inv.String("file"))
	if err != nil {
		return nil, err
	}
	request := CreatePageRequest{
		Parent:        parent,
		Title:         inv.String("title"),
		TitleProperty: inv.String("title-property"),
		Markdown:      body,
		Properties:    json.RawMessage(strings.TrimSpace(inv.String("properties-json"))),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.CreatePage(actionContext(exec), request)
}

func dataSourceRowUpdateAction(exec actions.Context, inv actions.Invocation) (any, error) {
	request := UpdatePageRequest{
		Title:         inv.String("title"),
		TitleProperty: inv.String("title-property"),
		Properties:    json.RawMessage(strings.TrimSpace(inv.String("properties-json"))),
	}
	if inv.Bool("dry-run") {
		return actions.NewDryRun(inv.Spec.ID, request), nil
	}
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	pageID, err := resolvePageArg(exec, client, pageArg(inv.Args))
	if err != nil {
		return nil, err
	}
	return client.UpdatePage(actionContext(exec), pageID, request)
}

func databaseDataSourcesAction(exec actions.Context, inv actions.Invocation) (any, error) {
	client, err := actionClient(exec)
	if err != nil {
		return nil, err
	}
	return client.RetrieveDatabase(actionContext(exec), inv.Args[0])
}

func Diagnostics(ctx context.Context, exec actions.Context, status actions.ConnectionStatus) []actions.Diagnostic {
	diagnostics := []actions.Diagnostic{{
		Provider: string(ProviderName),
		Check:    "toolmuxd",
		Status:   "ok",
		Message:  exec.ToolmuxdURL,
	}}
	if !status.Connected {
		return diagnostics
	}
	exec.Context = ctx
	client, err := actionClient(exec)
	if err != nil {
		return append(diagnostics, actions.Diagnostic{
			Provider:    string(ProviderName),
			Check:       "api",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Run `toolmux connect notion` to refresh the local connection.",
		})
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.Search(probeCtx, SearchRequest{PageSize: 1}); err != nil {
		return append(diagnostics, actions.Diagnostic{
			Provider:    string(ProviderName),
			Check:       "api",
			Status:      "fail",
			Message:     err.Error(),
			Remediation: "Check that the Notion connection still has access to at least one selected page or data source.",
		})
	}
	return append(diagnostics, actions.Diagnostic{
		Provider: string(ProviderName),
		Check:    "api",
		Status:   "ok",
		Message:  "search endpoint reachable",
	})
}

func actionClient(exec actions.Context) (*Client, error) {
	if exec.Credentials == nil {
		return nil, fmt.Errorf("credential store is required")
	}
	tokens, err := exec.Credentials.LoadOAuthTokens(actionContext(exec), credentialRef(exec))
	if err != nil {
		return nil, err
	}
	return NewClient(
		tokens.AccessToken,
		WithBaseURL(exec.ProviderURL),
		WithVersion(exec.ProviderAPI),
		WithHTTPClient(exec.HTTPClient),
	), nil
}

func credentialRef(exec actions.Context) credentials.ConnectionRef {
	return credentials.ConnectionRef{
		Profile:   firstNonEmpty(exec.Profile, "default"),
		Provider:  string(ProviderName),
		AccountID: firstNonEmpty(exec.Account, "default"),
	}
}

func actionContext(exec actions.Context) context.Context {
	if exec.Context != nil {
		return exec.Context
	}
	return context.Background()
}

func pageArg(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

func resolvePageArg(exec actions.Context, client *Client, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("notion page is required")
	}
	if id, err := NormalizeID(value); err == nil {
		return id, nil
	}
	results, err := client.Search(actionContext(exec), SearchRequest{
		Query:      value,
		ObjectType: "page",
		PageSize:   10,
	})
	if err != nil {
		return "", fmt.Errorf("search Notion page %q: %w", value, err)
	}
	pages := pageSearchMatches(results.Results)
	switch len(pages) {
	case 0:
		return "", fmt.Errorf("no Notion page matches %q; pass a page ID or URL", value)
	case 1:
		return pages[0].ID, nil
	default:
		if exec.Interactive && exec.SelectString != nil {
			selected, ok, err := exec.SelectString(actionContext(exec), actions.SelectStringRequest{
				Title:       "Select a Notion page",
				Description: "Multiple pages match " + strconv.Quote(value),
				Options:     pageSelectionOptions(pages),
				Height:      min(len(pages)+4, 12),
				Filtering:   len(pages) > 6,
			})
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("page selection cancelled")
			}
			return selected, nil
		}
		return "", multiplePagesError(value, pages)
	}
}

func pageSearchMatches(results []SearchResult) []SearchResult {
	pages := make([]SearchResult, 0, len(results))
	for _, result := range results {
		if result.Object == "page" {
			pages = append(pages, result)
		}
	}
	return pages
}

func pageSelectionOptions(pages []SearchResult) []actions.SelectStringOption {
	options := make([]actions.SelectStringOption, 0, len(pages))
	for _, page := range pages {
		options = append(options, actions.SelectStringOption{
			Label: PageSelectionLabel(page),
			Value: page.ID,
		})
	}
	return options
}

func PageSelectionLabel(page SearchResult) string {
	title := firstNonEmpty(page.Title, "(untitled)")
	detail := ShortPageID(page.ID)
	if detail == "" {
		detail = page.URL
	}
	if detail == "" {
		return title
	}
	return title + "  " + detail
}

func ShortPageID(id string) string {
	id = strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func multiplePagesError(query string, pages []SearchResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "multiple Notion pages match %q; pass a page ID or URL", query)
	for _, page := range pages {
		fmt.Fprintf(&b, "\n- %s (%s)", firstNonEmpty(page.Title, "(untitled)"), page.ID)
	}
	return errors.New(b.String())
}

func parseParent(parentType, parent string, allowWorkspace bool) (Parent, error) {
	parentType = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(parentType, "_", "-")))
	parent = strings.TrimSpace(parent)
	if parentType == "" || parentType == "auto" {
		if strings.EqualFold(parent, "workspace") {
			parentType = "workspace"
		} else {
			parentType = "page"
		}
	}
	switch parentType {
	case "workspace":
		if !allowWorkspace {
			return Parent{}, fmt.Errorf("workspace parent is not supported for this command")
		}
		return WorkspaceParent(), nil
	case "page":
		if parent == "" {
			return Parent{}, fmt.Errorf("--parent is required")
		}
		return PageParent(parent)
	case "data-source", "datasource":
		if parent == "" {
			return Parent{}, fmt.Errorf("--parent is required")
		}
		return DataSourceParent(parent)
	default:
		return Parent{}, fmt.Errorf("unsupported Notion parent type %q", parentType)
	}
}

func readMarkdownInput(exec actions.Context, markdownText, file string) (string, error) {
	if strings.TrimSpace(file) != "" && markdownText != "" {
		return "", fmt.Errorf("use either --markdown or --file, not both")
	}
	if strings.TrimSpace(file) == "" {
		return markdownText, nil
	}
	readFile := exec.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func retrievePageRead(ctx context.Context, client *Client, pageID string, includeTranscript bool) (PageRead, error) {
	page, err := client.RetrievePage(ctx, pageID, nil)
	if err != nil {
		return PageRead{}, err
	}
	markdown, err := client.RetrievePageMarkdown(ctx, pageID, includeTranscript)
	if err != nil {
		return PageRead{}, err
	}
	return PageRead{Page: page, Markdown: markdown}, nil
}

func pageDiagnostics(result PageRead) []actions.Diagnostic {
	pageID := result.Page.ID
	if pageID == "" {
		pageID = result.Markdown.ID
	}
	title := firstNonEmpty(result.Page.Title(), pageID)
	diagnostics := []actions.Diagnostic{{
		Provider: string(ProviderName),
		Check:    "page",
		Status:   "ok",
		Message:  firstNonEmpty(title, "page loaded"),
		Details:  map[string]string{"page_id": pageID},
	}}
	if result.Markdown.Truncated {
		diagnostics = append(diagnostics, actions.Diagnostic{
			Provider:    string(ProviderName),
			Check:       "markdown-truncation",
			Status:      "warn",
			Message:     "Notion returned a truncated markdown export",
			Remediation: "Read the page in Notion before relying on full round-trip edits.",
			Details:     map[string]string{"page_id": pageID},
		})
	} else {
		diagnostics = append(diagnostics, actions.Diagnostic{
			Provider: string(ProviderName),
			Check:    "markdown-truncation",
			Status:   "ok",
			Message:  "markdown export is complete",
			Details:  map[string]string{"page_id": pageID},
		})
	}
	if len(result.Markdown.UnknownBlockIDs) > 0 {
		diagnostics = append(diagnostics, actions.Diagnostic{
			Provider:    string(ProviderName),
			Check:       "unknown-blocks",
			Status:      "warn",
			Message:     fmt.Sprintf("%d block(s) could not be represented as markdown", len(result.Markdown.UnknownBlockIDs)),
			Remediation: "Use --output json on page markdown to inspect unknown_block_ids before editing.",
			Details: map[string]string{
				"page_id":           pageID,
				"unknown_block_ids": strings.Join(result.Markdown.UnknownBlockIDs, ","),
			},
		})
	} else {
		diagnostics = append(diagnostics, actions.Diagnostic{
			Provider: string(ProviderName),
			Check:    "unknown-blocks",
			Status:   "ok",
			Message:  "all exported blocks were represented",
			Details:  map[string]string{"page_id": pageID},
		})
	}
	links := pageLinksFromMarkdown(result.Markdown.Markdown)
	diagnostics = append(diagnostics, actions.Diagnostic{
		Provider: string(ProviderName),
		Check:    "links",
		Status:   "ok",
		Message:  fmt.Sprintf("%d link(s) found", len(links)),
		Details: map[string]string{
			"page_id": pageID,
			"links":   strconv.Itoa(len(links)),
		},
	})
	return diagnostics
}

func pageLinksFromMarkdown(markdown string) PageLinks {
	rawLinks := output.ExtractMarkdownLinks(markdown)
	links := make(PageLinks, 0, len(rawLinks))
	for i, raw := range rawLinks {
		link := PageLink{
			Index: i + 1,
			Label: strings.TrimSpace(raw.Label),
			URL:   strings.TrimSpace(raw.URL),
			Kind:  "external",
		}
		if link.Label == "" {
			link.Label = link.URL
		}
		if id, ok := PageIDFromLink(link.URL); ok {
			link.Kind = "notion"
			link.NotionPageID = id
		}
		links = append(links, link)
	}
	return links
}

func collectPageChildren(ctx context.Context, client *Client, pageID string, maxDepth, limit int) ([]PageChild, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if limit <= 0 {
		limit = 100
	}
	children := make([]PageChild, 0)
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(parentID string, depth int) error {
		if depth > maxDepth || len(children) >= limit {
			return nil
		}
		blocks, err := listAllChildBlocks(ctx, client, parentID, limit-len(children))
		if err != nil {
			return err
		}
		for _, block := range blocks {
			child, ok := pageChildFromBlock(block, depth)
			if !ok || seen[child.ID] {
				continue
			}
			seen[child.ID] = true
			children = append(children, child)
			if len(children) >= limit {
				return nil
			}
			if err := walk(child.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(pageID, 1); err != nil {
		return nil, err
	}
	return children, nil
}

func listAllChildBlocks(ctx context.Context, client *Client, blockID string, limit int) ([]Block, error) {
	if limit <= 0 {
		return nil, nil
	}
	blocks := make([]Block, 0)
	cursor := ""
	for len(blocks) < limit {
		pageSize := min(limit-len(blocks), 100)
		result, err := client.ListBlockChildren(ctx, blockID, ListBlockChildrenRequest{
			StartCursor: cursor,
			PageSize:    pageSize,
		})
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, result.Results...)
		if !result.HasMore || result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if len(blocks) > limit {
		return blocks[:limit], nil
	}
	return blocks, nil
}

func pageChildFromBlock(block Block, depth int) (PageChild, bool) {
	switch block.Type {
	case "child_page":
		title := ""
		if block.ChildPage != nil {
			title = block.ChildPage.Title
		}
		return PageChild{
			Depth:       depth,
			Title:       firstNonEmpty(title, "(untitled)"),
			ID:          block.ID,
			Type:        block.Type,
			URL:         pageWebURL(block.ID),
			HasChildren: block.HasChildren,
		}, true
	case "child_database":
		title := ""
		if block.ChildDatabase != nil {
			title = block.ChildDatabase.Title
		}
		return PageChild{
			Depth:       depth,
			Title:       firstNonEmpty(title, "(untitled)"),
			ID:          block.ID,
			Type:        block.Type,
			HasChildren: block.HasChildren,
		}, true
	default:
		return PageChild{}, false
	}
}

func (result PageRead) Follow(exec actions.Context) (any, bool, error) {
	if !result.FollowLinks {
		return nil, false, nil
	}
	links := output.ExtractMarkdownLinks(result.Markdown.Markdown)
	if len(links) == 0 {
		return nil, false, nil
	}
	if exec.SelectInteger == nil {
		return nil, false, fmt.Errorf("--follow requires an interactive selector")
	}
	selected, ok, err := exec.SelectInteger(actionContext(exec), actions.SelectIntegerRequest{
		Title:       "Follow a link",
		Description: "Choose a link from this page",
		Options:     linkSelectionOptions(links),
		Height:      min(len(links)+5, 14),
		Filtering:   len(links) > 8,
	})
	if err != nil || !ok || selected < 0 || selected >= len(links) {
		return nil, ok, err
	}
	link := links[selected]
	if pageID, ok := PageIDFromLink(link.URL); ok {
		client, err := actionClient(exec)
		if err != nil {
			return nil, false, err
		}
		next, err := retrievePageRead(actionContext(exec), client, pageID, result.IncludeTranscript)
		if err != nil {
			return nil, false, err
		}
		next.FollowLinks = true
		next.IncludeTranscript = result.IncludeTranscript
		return next, true, nil
	}
	if exec.OpenBrowser == nil {
		return nil, false, fmt.Errorf("open browser callback is required")
	}
	if err := exec.OpenBrowser(link.URL); err != nil {
		return nil, false, fmt.Errorf("open link %q: %w", link.URL, err)
	}
	return PageOpen{URL: link.URL, URLOnly: true}, true, nil
}

func linkSelectionOptions(links []output.MarkdownLink) []actions.SelectIntegerOption {
	options := make([]actions.SelectIntegerOption, 0, len(links)+1)
	for i, link := range links {
		options = append(options, actions.SelectIntegerOption{
			Label: LinkSelectionLabel(i, link),
			Value: i,
		})
	}
	options = append(options, actions.SelectIntegerOption{Label: "Done", Value: len(links)})
	return options
}

func LinkSelectionLabel(index int, link output.MarkdownLink) string {
	label := firstNonEmpty(strings.TrimSpace(link.Label), strings.TrimSpace(link.URL))
	detail := LinkSelectionDetail(link.URL)
	if detail == "" || detail == label {
		return fmt.Sprintf("[%d] %s", index+1, label)
	}
	return fmt.Sprintf("[%d] %s  %s", index+1, label, detail)
}

func LinkSelectionDetail(rawURL string) string {
	if id, ok := PageIDFromLink(rawURL); ok {
		return "Notion " + ShortPageID(id)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	detail := parsed.Host
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path != "" {
		detail += path
	}
	if len(detail) > 72 {
		return detail[:69] + "..."
	}
	return detail
}

func PageIDFromLink(rawURL string) (string, bool) {
	id, err := NormalizeID(rawURL)
	if err != nil {
		return "", false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if parsed.Host == "" {
		if parsed.Scheme == "" && bareNotionLink(rawURL, id) {
			return id, true
		}
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "notion.so" || host == "www.notion.so" || strings.HasSuffix(host, ".notion.so") || host == "notion.site" || strings.HasSuffix(host, ".notion.site") {
		return id, true
	}
	return "", false
}

func bareNotionLink(rawURL, id string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(rawURL))
	compact := strings.ReplaceAll(strings.ToLower(id), "-", "")
	return cleaned == strings.ToLower(id) || cleaned == compact || strings.HasSuffix(cleaned, "-"+compact)
}

func pageWebURL(id string) string {
	compact := strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if compact == "" {
		return ""
	}
	return "https://www.notion.so/" + compact
}

func (result PageRead) MarkdownSource() string {
	source := strings.TrimSpace(result.Markdown.Markdown)
	title := strings.TrimSpace(result.Page.Title())
	if title != "" && !strings.HasPrefix(source, "# ") && !strings.HasPrefix(source, "## ") {
		if source == "" {
			source = "# " + title
		} else {
			source = "# " + title + "\n\n" + source
		}
	}
	return output.PrepareReadableMarkdown(source)
}

func (result PageRead) MarkdownTruncated() (bool, int) {
	return result.Markdown.Truncated, len(result.Markdown.UnknownBlockIDs)
}

func (result PageFull) Text() string {
	return fmt.Sprintf("%s %s\n\n%s", result.Page.ID, firstNonEmpty(result.Page.Title(), result.Page.URL), strings.TrimRight(result.Markdown.Markdown, "\n"))
}

func (markdown PageMarkdown) Text() string {
	text := strings.TrimRight(markdown.Markdown, "\n")
	if markdown.Truncated {
		text += fmt.Sprintf("\n\ntruncated: %d unknown blocks", len(markdown.UnknownBlockIDs))
	}
	return text
}

func (page Page) Table(human output.Options) output.Table {
	title := page.Title()
	status := "active"
	if page.InTrash {
		status = "trashed"
	}
	return output.Table{
		Headers: []string{"Title", "ID", "URL", "Status"},
		Rows: [][]string{{
			output.Value(title),
			page.ID,
			output.Value(page.URL),
			output.StatusBadge(human, status),
		}},
	}
}

func (results SearchResponse) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(results.Results))
	for _, result := range results.Results {
		status := "active"
		if result.InTrash {
			status = "trashed"
		}
		rows = append(rows, []string{
			output.Value(result.Title),
			result.ID,
			output.Value(result.URL),
			result.Object,
			output.StatusBadge(human, status),
			output.Value(result.LastEditedTime),
		})
	}
	return output.Table{
		Headers: []string{"Title", "ID", "URL", "Type", "Status", "Last Edited"},
		Rows:    rows,
		Empty:   "no Notion results",
	}
}

func (links PageLinks) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(links))
	for _, link := range links {
		rows = append(rows, []string{
			strconv.Itoa(link.Index),
			output.Value(link.Label),
			output.Value(link.Kind),
			output.Value(link.URL),
			output.Value(link.NotionPageID),
		})
	}
	return output.Table{
		Headers: []string{"#", "Label", "Kind", "URL", "Notion Page"},
		Rows:    rows,
		Empty:   "no links on this page",
	}
}

func (children PageChildren) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(children.Children))
	for _, child := range children.Children {
		rows = append(rows, []string{
			strconv.Itoa(child.Depth),
			strings.Repeat("  ", child.Depth-1) + child.Title,
			child.ID,
			child.Type,
			output.Value(child.URL),
		})
	}
	return output.Table{
		Headers: []string{"Depth", "Title", "ID", "Type", "URL"},
		Rows:    rows,
		Empty:   children.Empty,
	}
}

func (page PageOpen) Text() string {
	return page.URL
}

func (page PageOpen) BrowserURL() string {
	return page.URL
}

func (page PageOpen) BrowserURLOnly() bool {
	return page.URLOnly
}

func (results QueryDataSourceResponse) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(results.Results))
	for _, page := range results.Results {
		status := "active"
		if page.InTrash {
			status = "trashed"
		}
		rows = append(rows, []string{
			output.Value(page.Title()),
			page.ID,
			output.Value(page.URL),
			output.StatusBadge(human, status),
		})
	}
	return output.Table{
		Headers: []string{"Title", "ID", "URL", "Status"},
		Rows:    rows,
		Empty:   "no Notion data source rows",
	}
}

func (dataSource DataSource) Table(output.Options) output.Table {
	return output.Table{
		Headers: []string{"Property", "ID", "Type"},
		Rows:    dataSourcePropertyRows(dataSource.Properties),
		Empty:   "no data source properties",
	}
}

func (database Database) Table(output.Options) output.Table {
	rows := make([][]string, 0, len(database.DataSources))
	for _, dataSource := range database.DataSources {
		rows = append(rows, []string{dataSource.Name, dataSource.ID})
	}
	return output.Table{
		Headers: []string{"Name", "ID"},
		Rows:    rows,
		Empty:   "no data sources",
	}
}

func (diagnostics DiagnosticResult) Table(human output.Options) output.Table {
	rows := make([][]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		target := firstNonEmpty(diagnostic.Provider, "toolmux")
		rows = append(rows, []string{
			target,
			diagnostic.Check,
			output.StatusBadge(human, diagnostic.Status),
			output.Value(diagnostic.Message),
			output.Value(diagnostic.Remediation),
		})
	}
	return output.Table{
		Headers: []string{"Provider", "Check", "Status", "Message", "Remediation"},
		Rows:    rows,
		Empty:   "no diagnostics",
	}
}

func dataSourcePropertyRows(properties map[string]json.RawMessage) [][]string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		var property struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		_ = json.Unmarshal(properties[name], &property)
		rows = append(rows, []string{name, output.Value(property.ID), output.Value(property.Type)})
	}
	return rows
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
