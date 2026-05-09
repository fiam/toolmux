package client

import (
	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/notion"
)

const (
	CapabilityReadContent   = notion.CapabilityReadContent
	CapabilityInsertContent = notion.CapabilityInsertContent
	CapabilityUpdateContent = notion.CapabilityUpdateContent
)

const ProviderName actions.ProviderName = notion.ProviderName

const (
	ActionSearch              actions.LocalName = "search"
	ActionPageGet             actions.LocalName = "page.get"
	ActionPageRead            actions.LocalName = "page.read"
	ActionPageLinks           actions.LocalName = "page.links"
	ActionPageOpen            actions.LocalName = "page.open"
	ActionPageChildren        actions.LocalName = "page.children"
	ActionPageTree            actions.LocalName = "page.tree"
	ActionPageDoctor          actions.LocalName = "page.doctor"
	ActionPageMarkdown        actions.LocalName = "page.markdown"
	ActionPageCreate          actions.LocalName = "page.create"
	ActionPageUpdate          actions.LocalName = "page.update"
	ActionPageContentInsert   actions.LocalName = "page.content.insert"
	ActionPageContentReplace  actions.LocalName = "page.content.replace"
	ActionPageContentUpdate   actions.LocalName = "page.content.update"
	ActionPageDelete          actions.LocalName = "page.delete"
	ActionPageRestore         actions.LocalName = "page.restore"
	ActionPageMove            actions.LocalName = "page.move"
	ActionDataSourceQuery     actions.LocalName = "data_source.query"
	ActionDataSourceSchema    actions.LocalName = "data_source.schema"
	ActionDataSourceRowCreate actions.LocalName = "data_source.row.create"
	ActionDataSourceRowUpdate actions.LocalName = "data_source.row.update"
	ActionDatabaseDataSources actions.LocalName = "database.data_sources"
)

const (
	ResourceWorkspace     actions.ResourceName = "workspace"
	ResourcePage          actions.ResourceName = "page"
	ResourcePageLink      actions.ResourceName = "page_link"
	ResourcePageChild     actions.ResourceName = "page_child"
	ResourcePageContent   actions.ResourceName = "page_content"
	ResourceDataSource    actions.ResourceName = "data_source"
	ResourceDataSourceRow actions.ResourceName = "data_source_row"
	ResourceDatabase      actions.ResourceName = "database"
)

func CommandTree() actions.Spec {
	return group("notion", "Operate Notion pages and data sources",
		spec(ActionSearch, "search", ResourceWorkspace, actions.VerbSearch, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("search [query]"), actions.Short("Search Notion pages and data sources shared with Toolmux"), actions.MaxArgs(1), stringFlag("query", "", "title query"), stringFlag("type", "all", "result type: all, page, data_source"), intFlag("limit", 20, "maximum results"), stringFlag("sort", "", "sort: edited, none"), stringFlag("direction", "desc", "sort direction: asc, desc")),
		group("page", "Read and mutate Notion pages",
			spec(ActionPageGet, "get", ResourcePage, actions.VerbRead, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("get <page>"), actions.Short("Retrieve a Notion page"), actions.MinArgs(1), stringFlag("format", "properties", "format: properties, markdown, full"), boolFlag("include-transcript", false, "include meeting note transcripts in markdown"), stringSliceFlag("filter-property", nil, "page property to include")),
			spec(ActionPageRead, "read", ResourcePage, actions.VerbRead, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("read <page>"), actions.Short("Read a Notion page in the terminal"), actions.MinArgs(1), boolFlag("include-transcript", false, "include meeting note transcripts"), boolFlag("follow", false, "choose a link after reading; Notion links open in toolmux, external links open in the browser")),
			spec(ActionPageLinks, "links", ResourcePageLink, actions.VerbList, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("links <page>"), actions.Short("List links from a Notion page"), actions.MinArgs(1), boolFlag("include-transcript", false, "include meeting note transcripts")),
			spec(ActionPageOpen, "open", ResourcePage, actions.VerbOpen, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("open <page>"), actions.Short("Open a Notion page in the browser"), actions.MinArgs(1), boolFlag("url-only", false, "print the page URL without opening a browser")),
			spec(ActionPageChildren, "children", ResourcePageChild, actions.VerbList, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("children <page>"), actions.Short("List child pages under a Notion page"), actions.MinArgs(1), intFlag("limit", 100, "maximum child pages")),
			spec(ActionPageTree, "tree", ResourcePageChild, actions.VerbList, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("tree <page>"), actions.Short("List nested child pages under a Notion page"), actions.MinArgs(1), intFlag("depth", 3, "maximum child-page depth"), intFlag("limit", 100, "maximum child pages")),
			spec(ActionPageDoctor, "doctor", ResourcePage, actions.VerbDiagnose, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("doctor <page>"), actions.Short("Check Notion page markdown export fidelity"), actions.MinArgs(1), boolFlag("include-transcript", false, "include meeting note transcripts")),
			spec(ActionPageMarkdown, "markdown", ResourcePage, actions.VerbRead, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("markdown <page>"), actions.Short("Export a Notion page as raw markdown"), actions.MinArgs(1), boolFlag("include-transcript", false, "include meeting note transcripts")),
			spec(ActionPageCreate, "create", ResourcePage, actions.VerbCreate, actions.EffectWrite, []string{"content-write"}, []string{CapabilityInsertContent}, actions.Use("create"), actions.Short("Create a Notion page"), stringFlag("parent", "", "parent page id, data source id, workspace, or URL"), stringFlag("parent-type", "page", "parent type: page, data-source, workspace"), stringFlag("title", "", "page title"), stringFlag("title-property", "", "title property name"), stringFlag("markdown", "", "markdown page body"), stringFlag("file", "", "read markdown page body from file"), stringFlag("properties-json", "", "raw Notion properties JSON"), boolFlag("dry-run", false, "show request without creating the page")),
			spec(ActionPageUpdate, "update", ResourcePage, actions.VerbUpdate, actions.EffectWrite, []string{"content-write"}, []string{CapabilityUpdateContent}, actions.Use("update <page>"), actions.Short("Update Notion page properties"), actions.MinArgs(1), stringFlag("title", "", "new page title"), stringFlag("title-property", "", "title property name"), stringFlag("properties-json", "", "raw Notion properties JSON"), boolFlag("dry-run", false, "show request without updating the page")),
			group("content", "Update Notion page markdown content",
				spec(ActionPageContentInsert, "insert", ResourcePageContent, actions.VerbUpdate, actions.EffectWrite, []string{"content-write"}, []string{CapabilityUpdateContent}, actions.Use("insert <page>"), actions.Short("Insert markdown into a Notion page"), actions.MinArgs(1), stringFlag("markdown", "", "markdown to insert"), stringFlag("file", "", "read markdown to insert from file"), stringFlag("after", "", "Notion markdown selection to insert after"), boolFlag("dry-run", false, "show request without updating the page")),
				spec(ActionPageContentReplace, "replace", ResourcePageContent, actions.VerbUpdate, actions.EffectWrite, []string{"content-write", "content-replace"}, []string{CapabilityUpdateContent}, actions.Use("replace <page>"), actions.Short("Replace all markdown content in a Notion page"), actions.MinArgs(1), stringFlag("markdown", "", "replacement markdown"), stringFlag("file", "", "read replacement markdown from file"), boolFlag("allow-deleting-content", false, "allow deleting child pages/databases"), boolFlag("dry-run", false, "show request without updating the page"), boolFlag("yes", false, "confirm replacing all page content")),
				spec(ActionPageContentUpdate, "update", ResourcePageContent, actions.VerbUpdate, actions.EffectWrite, []string{"content-write"}, []string{CapabilityUpdateContent}, actions.Use("update <page>"), actions.Short("Search and replace Notion page markdown content"), actions.MinArgs(1), stringFlag("old", "", "exact markdown text to find"), stringFlag("new", "", "replacement markdown text"), boolFlag("replace-all", false, "replace all matching occurrences"), boolFlag("allow-deleting-content", false, "allow deleting child pages/databases"), boolFlag("dry-run", false, "show request without updating the page"), boolFlag("yes", false, "confirm allowing child content deletion")),
			),
			spec(ActionPageDelete, "delete", ResourcePage, actions.VerbDelete, actions.EffectWrite, []string{"destructive", "trash"}, []string{CapabilityUpdateContent}, actions.Use("delete <page>"), actions.Short("Move a Notion page to trash"), actions.Aliases("trash"), actions.MinArgs(1), boolFlag("yes", false, "confirm moving the page to trash"), boolFlag("dry-run", false, "show request without trashing the page")),
			spec(ActionPageRestore, "restore", ResourcePage, actions.VerbRestore, actions.EffectWrite, []string{"content-write"}, []string{CapabilityUpdateContent}, actions.Use("restore <page>"), actions.Short("Restore a Notion page from trash"), actions.MinArgs(1), boolFlag("dry-run", false, "show request without restoring the page")),
			spec(ActionPageMove, "move", ResourcePage, actions.VerbMove, actions.EffectWrite, []string{"relocate"}, []string{CapabilityUpdateContent}, actions.Use("move <page>"), actions.Short("Move a Notion page to a new parent"), actions.MinArgs(1), stringFlag("parent", "", "new parent page/data source id or URL"), stringFlag("parent-type", "page", "parent type: page, data-source"), boolFlag("dry-run", false, "show request without moving the page")),
		),
		groupWithAliases("data-source", "Operate Notion data sources", []string{"datasource"},
			spec(ActionDataSourceQuery, "query", ResourceDataSource, actions.VerbQuery, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("query <data-source>"), actions.Short("Query a Notion data source"), actions.ExactArgs(1), intFlag("limit", 50, "maximum rows"), stringFlag("filter-json", "", "raw Notion filter JSON"), stringFlag("sorts-json", "", "raw Notion sorts JSON"), stringFlag("result-type", "", "optional result type: page or data_source"), stringSliceFlag("filter-property", nil, "data source property to include")),
			spec(ActionDataSourceSchema, "schema", ResourceDataSource, actions.VerbRead, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("schema <data-source>"), actions.Short("Inspect a Notion data source schema"), actions.ExactArgs(1)),
			group("row", "Create and update Notion data source rows",
				spec(ActionDataSourceRowCreate, "create", ResourceDataSourceRow, actions.VerbCreate, actions.EffectWrite, []string{"content-write"}, []string{CapabilityInsertContent}, actions.Use("create <data-source>"), actions.Short("Create a row in a Notion data source"), actions.ExactArgs(1), stringFlag("title", "", "row title"), stringFlag("title-property", "", "title property name"), stringFlag("markdown", "", "initial row page markdown"), stringFlag("file", "", "read initial row page markdown from file"), stringFlag("properties-json", "", "raw Notion properties JSON"), boolFlag("dry-run", false, "show request without creating the row")),
				spec(ActionDataSourceRowUpdate, "update", ResourceDataSourceRow, actions.VerbUpdate, actions.EffectWrite, []string{"content-write"}, []string{CapabilityUpdateContent}, actions.Use("update <page>"), actions.Short("Update row properties for a Notion data source page"), actions.MinArgs(1), stringFlag("title", "", "row title"), stringFlag("title-property", "", "title property name"), stringFlag("properties-json", "", "raw Notion properties JSON"), boolFlag("dry-run", false, "show request without updating the row")),
			),
		),
		group("database", "Inspect Notion database containers",
			spec(ActionDatabaseDataSources, "data-sources", ResourceDatabase, actions.VerbRead, actions.EffectRead, nil, []string{CapabilityReadContent}, actions.Use("data-sources <database>"), actions.Short("List data sources for a Notion database"), actions.ExactArgs(1)),
		),
	)
}

func DefaultCapabilities() []string {
	return []string{CapabilityReadContent, CapabilityInsertContent, CapabilityUpdateContent}
}

func spec(name actions.LocalName, segment string, resource actions.ResourceName, verb actions.Verb, effect actions.Effect, risk, scopes []string, extra ...actions.Option) actions.Spec {
	opts := []actions.Option{actions.RBAC(resource, verb, effect)}
	if len(risk) > 0 {
		opts = append(opts, actions.Risks(risk...))
	}
	if len(scopes) > 0 {
		opts = append(opts, actions.Scopes(scopes...))
	}
	opts = append(opts, extra...)
	return actions.Command(name, segment, opts...)
}

func group(segment, short string, children ...actions.Spec) actions.Spec {
	return actions.Group(segment, actions.Use(segment), actions.Short(short), actions.Children(children...))
}

func groupWithAliases(segment, short string, aliases []string, children ...actions.Spec) actions.Spec {
	return actions.Group(segment, actions.Use(segment), actions.Short(short), actions.Aliases(aliases...), actions.Children(children...))
}

func boolFlag(name string, defaultValue bool, usage string) actions.Option {
	return actions.BoolFlag(name, defaultValue, usage)
}

func intFlag(name string, defaultValue int, usage string) actions.Option {
	return actions.IntFlag(name, defaultValue, usage)
}

func stringFlag(name, defaultValue, usage string) actions.Option {
	return actions.StringFlag(name, defaultValue, usage)
}

func stringSliceFlag(name string, defaultValue []string, usage string) actions.Option {
	return actions.StringSliceFlag(name, defaultValue, usage)
}
