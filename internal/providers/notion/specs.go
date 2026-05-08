package notion

import "github.com/fiam/toolmux/internal/policy"

const (
	CapabilityReadContent   = "read_content"
	CapabilityInsertContent = "insert_content"
	CapabilityUpdateContent = "update_content"
)

func CommandSpecs() []policy.CommandSpec {
	return []policy.CommandSpec{
		spec("notion.status", []string{"status", "notion"}, "connection", "status", "read", nil, nil),
		spec("notion.doctor", []string{"doctor", "notion"}, "connection", "diagnose", "read", nil, nil),
		spec("notion.search", []string{"notion", "search"}, "workspace", "search", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.get", []string{"notion", "page", "get"}, "page", "read", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.read", []string{"notion", "page", "read"}, "page", "read", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.links", []string{"notion", "page", "links"}, "page_link", "list", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.open", []string{"notion", "page", "open"}, "page", "open", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.children", []string{"notion", "page", "children"}, "page_child", "list", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.tree", []string{"notion", "page", "tree"}, "page_child", "list", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.doctor", []string{"notion", "page", "doctor"}, "page", "diagnose", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.markdown", []string{"notion", "page", "markdown"}, "page", "read", "read", nil, []string{CapabilityReadContent}),
		spec("notion.page.create", []string{"notion", "page", "create"}, "page", "create", "write", []string{"content-write"}, []string{CapabilityInsertContent}),
		spec("notion.page.update", []string{"notion", "page", "update"}, "page", "update", "write", []string{"content-write"}, []string{CapabilityUpdateContent}),
		spec("notion.page.content.insert", []string{"notion", "page", "content", "insert"}, "page_content", "update", "write", []string{"content-write"}, []string{CapabilityUpdateContent}),
		spec("notion.page.content.replace", []string{"notion", "page", "content", "replace"}, "page_content", "update", "write", []string{"content-write", "content-replace"}, []string{CapabilityUpdateContent}),
		spec("notion.page.content.update", []string{"notion", "page", "content", "update"}, "page_content", "update", "write", []string{"content-write"}, []string{CapabilityUpdateContent}),
		spec("notion.page.delete", []string{"notion", "page", "delete"}, "page", "delete", "write", []string{"destructive", "trash"}, []string{CapabilityUpdateContent}),
		spec("notion.page.restore", []string{"notion", "page", "restore"}, "page", "restore", "write", []string{"content-write"}, []string{CapabilityUpdateContent}),
		spec("notion.page.move", []string{"notion", "page", "move"}, "page", "move", "write", []string{"relocate"}, []string{CapabilityUpdateContent}),
		spec("notion.data_source.query", []string{"notion", "data-source", "query"}, "data_source", "query", "read", nil, []string{CapabilityReadContent}),
		spec("notion.data_source.schema", []string{"notion", "data-source", "schema"}, "data_source", "read", "read", nil, []string{CapabilityReadContent}),
		spec("notion.data_source.row.create", []string{"notion", "data-source", "row", "create"}, "data_source_row", "create", "write", []string{"content-write"}, []string{CapabilityInsertContent}),
		spec("notion.data_source.row.update", []string{"notion", "data-source", "row", "update"}, "data_source_row", "update", "write", []string{"content-write"}, []string{CapabilityUpdateContent}),
		spec("notion.database.data_sources", []string{"notion", "database", "data-sources"}, "database", "read", "read", nil, []string{CapabilityReadContent}),
	}
}

func DefaultCapabilities() []string {
	return []string{CapabilityReadContent, CapabilityInsertContent, CapabilityUpdateContent}
}

func spec(id string, path []string, resource, action, effect string, risk, scopes []string) policy.CommandSpec {
	return policy.CommandSpec{
		ID:       id,
		Path:     path,
		Provider: "notion",
		Resource: resource,
		Action:   action,
		Effect:   effect,
		Risk:     risk,
		Scopes:   scopes,
	}
}
