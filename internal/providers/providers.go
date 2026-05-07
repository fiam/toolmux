package providers

import (
	"sort"

	"github.com/fiam/toolmux/internal/policy"
	"github.com/fiam/toolmux/internal/providers/linear"
)

type Provider struct {
	ID          string
	DisplayName string
	AuthMode    string
	Aliases     []string
	Specs       []policy.CommandSpec
}

func Initial() []Provider {
	return []Provider{
		{
			ID:          "notion",
			DisplayName: "Notion",
			AuthMode:    "brokered_local_custody",
			Specs: []policy.CommandSpec{
				spec("notion.search", "notion", []string{"notion", "search"}, "workspace", "search", "read", nil, nil),
				spec("notion.page.get", "notion", []string{"notion", "page", "get"}, "page", "read", "read", nil, nil),
				spec("notion.page.create", "notion", []string{"notion", "page", "create"}, "page", "create", "write", []string{"content-write"}, nil),
				spec("notion.database.query", "notion", []string{"notion", "database", "query"}, "database", "read", "read", nil, nil),
			},
		},
		{
			ID:          "jira",
			DisplayName: "Jira",
			AuthMode:    "brokered_local_custody",
			Specs: []policy.CommandSpec{
				spec("jira.sites.list", "jira", []string{"jira", "sites", "ls"}, "site", "list", "read", nil, []string{"read:jira-work"}),
				spec("jira.issues.list", "jira", []string{"jira", "issues", "list"}, "issue", "list", "read", nil, []string{"read:jira-work"}),
				spec("jira.issue.get", "jira", []string{"jira", "issue", "get"}, "issue", "read", "read", nil, []string{"read:jira-work"}),
				spec("jira.issue.create", "jira", []string{"jira", "issue", "create"}, "issue", "create", "write", []string{"ticket-write"}, []string{"write:jira-work"}),
				spec("jira.comment.add", "jira", []string{"jira", "comment", "add"}, "comment", "create", "write", []string{"comment-write"}, []string{"write:jira-work"}),
			},
		},
		{
			ID:          "slack",
			DisplayName: "Slack",
			AuthMode:    "native_pkce",
			Specs: []policy.CommandSpec{
				spec("slack.conversations.list", "slack", []string{"slack", "conversations", "ls"}, "conversation", "list", "read", nil, []string{"channels:read"}),
				spec("slack.message.send", "slack", []string{"slack", "message", "send"}, "message", "send", "write", []string{"external-send"}, []string{"chat:write"}),
				spec("slack.search", "slack", []string{"slack", "search"}, "message", "search", "read", nil, []string{"search:read"}),
			},
		},
		{
			ID:          "linear",
			DisplayName: "Linear",
			AuthMode:    "native_pkce",
			Specs:       linear.CommandSpecs(),
		},
		{
			ID:          "google",
			DisplayName: "Google",
			AuthMode:    "native_pkce",
			Aliases:     []string{"google-docs", "google-drive", "gmail"},
			Specs: []policy.CommandSpec{
				spec("google.docs.create", "google-docs", []string{"google", "docs", "create"}, "document", "create", "write", []string{"content-write"}, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.docs.get", "google-docs", []string{"google", "docs", "get"}, "document", "read", "read", nil, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.docs.export", "google-docs", []string{"google", "docs", "export"}, "document", "read", "read", nil, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.docs.append", "google-docs", []string{"google", "docs", "append"}, "document", "update", "write", []string{"content-write"}, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.drive.upload", "google-drive", []string{"google", "drive", "upload"}, "file", "create", "write", []string{"file-write"}, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.drive.download", "google-drive", []string{"google", "drive", "download"}, "file", "read", "read", nil, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.drive.list", "google-drive", []string{"google", "drive", "ls"}, "file", "list", "read", nil, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("google.drive.folder.create", "google-drive", []string{"google", "drive", "folder", "create"}, "folder", "create", "write", []string{"file-write"}, []string{"https://www.googleapis.com/auth/drive.file"}),
				spec("gmail.labels.list", "gmail", []string{"gmail", "labels", "ls"}, "label", "list", "read", nil, []string{"https://www.googleapis.com/auth/gmail.labels"}),
				spec("gmail.labels.create", "gmail", []string{"gmail", "labels", "create"}, "label", "create", "write", []string{"mailbox-write"}, []string{"https://www.googleapis.com/auth/gmail.labels"}),
				spec("gmail.send", "gmail", []string{"gmail", "send"}, "message", "send", "write", []string{"external-send"}, []string{"https://www.googleapis.com/auth/gmail.send"}),
			},
		},
	}
}

func Lookup(id string) (Provider, bool) {
	for _, provider := range Initial() {
		if provider.ID == id {
			return provider, true
		}
		for _, alias := range provider.Aliases {
			if alias == id {
				return provider, true
			}
		}
	}
	return Provider{}, false
}

func CommandSpecs() []policy.CommandSpec {
	var specs []policy.CommandSpec
	for _, provider := range Initial() {
		specs = append(specs, provider.Specs...)
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func spec(id, provider string, path []string, resource, action, effect string, risk, scopes []string) policy.CommandSpec {
	return policy.CommandSpec{
		ID:       id,
		Path:     path,
		Provider: provider,
		Resource: resource,
		Action:   action,
		Effect:   effect,
		Risk:     risk,
		Scopes:   scopes,
	}
}
