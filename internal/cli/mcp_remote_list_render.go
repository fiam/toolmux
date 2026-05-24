package cli

import (
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/output"
)

func mcpRemoteListItems(opts *options, entries []mcpRemoteServerEntry) []mcpRemoteListItem {
	items := make([]mcpRemoteListItem, 0, len(entries))
	for _, entry := range entries {
		status := "not_synced"
		var tools *int
		if cache, ok, _ := readMCPRemoteCacheIfExists(opts.mcpCacheDir, entry.Name); ok {
			status = "synced"
			count := len(cache.Tools)
			tools = &count
		}
		items = append(items, mcpRemoteListItem{
			Name:      entry.Name,
			Status:    status,
			Scope:     mcpRemoteScopeLabel(entry.Scope),
			Scopes:    mcpRemoteNormalizedScopes(entry.Scopes),
			Path:      entry.Path,
			URL:       entry.Server.URL,
			Command:   mcpRemoteCommandDisplay(entry.Server),
			Transport: entry.Server.Transport,
			Tools:     tools,
		})
	}
	return items
}

func renderMCPRemoteListTable(w io.Writer, cmd *cobra.Command, opts *options, items []mcpRemoteListItem) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		status := output.ToneText(human, output.ToneWarning, "not synced")
		tools := "-"
		if item.Status == "synced" {
			status = output.ToneText(human, output.ToneSuccess, "synced")
		}
		if item.Tools != nil {
			tools = fmt.Sprintf("%d", *item.Tools)
		}
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, item.Name),
			status,
			mcpRemoteScopesLabel(item.Scopes),
			tools,
			mcpRemoteServerSource(mcpRemoteServer{URL: item.URL, Command: item.Command, Transport: item.Transport}),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Name", "Status", "Scope", "Tools", "Source"},
		Rows:    rows,
		Empty:   "no remote MCP servers registered",
	})
}

func renderMCPRemoteToolTable(w io.Writer, cmd *cobra.Command, opts *options, serverName string, tools []mcpRemoteTool, fullDescriptions bool) {
	human := humanOutputOptions(cmd, opts)
	rows := make([][]string, 0, len(tools))
	for _, tool := range tools {
		rows = append(rows, []string{
			output.ToneText(human, output.ToneInfo, tool.Name),
			output.ToneText(human, output.ToneMuted, mcpRemoteToolArgumentsLabel(tool)),
			output.Value(mcpRemoteDisplayDescription(cmd, opts, tool.Description, fullDescriptions)),
		})
	}
	output.RenderTable(w, human, output.Table{
		Headers: []string{"Tool", "Arguments", "Description"},
		Rows:    rows,
		Empty:   "no cached tools for " + serverName,
	})
}

func mcpRemoteTreeItems(entries []mcpRemoteServerEntry, caches map[string]mcpRemoteCache) []mcpRemoteTreeItem {
	items := make([]mcpRemoteTreeItem, 0, len(entries))
	for _, entry := range entries {
		item := mcpRemoteTreeItem{
			Name:      entry.Name,
			Status:    "not_synced",
			Scope:     mcpRemoteScopeLabel(entry.Scope),
			Scopes:    mcpRemoteNormalizedScopes(entry.Scopes),
			Path:      entry.Path,
			URL:       entry.Server.URL,
			Command:   mcpRemoteCommandDisplay(entry.Server),
			Transport: entry.Server.Transport,
		}
		if cache, ok := caches[entry.Name]; ok {
			item.Status = "synced"
			item.Tools = sortedMCPRemoteTools(cache.Tools)
		}
		items = append(items, item)
	}
	return items
}

func renderMCPRemoteTree(w io.Writer, cmd *cobra.Command, opts *options, items []mcpRemoteTreeItem, fullDescriptions bool) {
	if len(items) == 0 {
		fmt.Fprintln(w, output.ToneText(humanOutputOptions(cmd, opts), output.ToneMuted, "no remote MCP servers registered"))
		return
	}
	human := humanOutputOptions(cmd, opts)
	for entryIndex, item := range items {
		suffix := "not synced"
		if item.Status == "synced" {
			suffix = fmt.Sprintf("%d tools", len(item.Tools))
		}
		fmt.Fprintf(w, "%s %s\n", output.ToneText(human, output.ToneInfo, item.Name), output.ToneText(human, output.ToneMuted, "("+suffix+")"))
		if item.Status != "synced" {
			continue
		}
		for toolIndex, tool := range item.Tools {
			connector := "+--"
			if toolIndex == len(item.Tools)-1 {
				connector = "`--"
			}
			args := mcpRemoteToolArgumentsLabel(tool)
			if args != "-" {
				args = " " + output.ToneText(human, output.ToneMuted, "("+args+")")
			} else {
				args = ""
			}
			description := ""
			if strings.TrimSpace(tool.Description) != "" {
				description = " " + output.ToneText(human, output.ToneMuted, "- "+mcpRemoteDisplayDescription(cmd, opts, tool.Description, fullDescriptions))
			}
			fmt.Fprintf(w, "%s %s%s%s\n", connector, output.ToneText(human, output.ToneInfo, tool.Name), args, description)
		}
		if entryIndex != len(items)-1 {
			fmt.Fprintln(w)
		}
	}
}

func sortedMCPRemoteTools(tools []mcpRemoteTool) []mcpRemoteTool {
	sorted := append([]mcpRemoteTool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func mcpRemoteScopesLabel(scopes []string) string {
	labels := mcpRemoteNormalizedScopes(scopes)
	if len(labels) == 0 {
		return "-"
	}
	return output.JoinList(labels)
}

func mcpRemoteNormalizedScopes(scopes []string) []string {
	labels := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		label := mcpRemoteScopeLabel(scope)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

func mcpRemoteScopeLabel(scope string) string {
	switch strings.TrimSpace(scope) {
	case "local", "project":
		return "project"
	case "global":
		return "global"
	default:
		return strings.TrimSpace(scope)
	}
}

func mcpRemoteToolArgumentsLabel(tool mcpRemoteTool) string {
	properties := mcpRemoteSchemaProperties(tool.InputSchema)
	if len(properties) == 0 {
		return "-"
	}
	required := mcpRemoteRequiredSet(tool.InputSchema)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	labels := make([]string, 0, len(names))
	for _, name := range names {
		label := name
		if required[name] {
			label += "*"
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}
