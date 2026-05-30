package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Lazy mode keeps the advertised tool surface tiny: instead of listing every
// native and remote tool (which can be hundreds, flooding an agent's context),
// the server advertises a single search_tools meta-tool. Agents call it to
// discover the tools they need and then invoke those tools by their exact name.
// Dispatch is unchanged: callTool still resolves any real tool by name, whether
// or not it was advertised.

const (
	lazySearchToolName          = "search_tools"
	lazySearchDefaultMaxResults = 10
)

func lazySearchTool() mcpTool {
	maxDefault := lazySearchDefaultMaxResults
	return mcpTool{
		Name: lazySearchToolName,
		Description: "Search Toolmux's full tool catalog and return matching tool definitions " +
			"(name, description, and input schema) on demand. Toolmux is running in lazy mode, so " +
			"only this meta-tool is advertised up front. Call it to discover the tool you need, then " +
			"call that tool directly by its exact name.\n\n" +
			"Query forms:\n" +
			"- \"select:linear.create_issue,slack.send_message\" — fetch these exact tools by name\n" +
			"- \"create issue\" — keyword search ranked by relevance\n" +
			"- \"+slack send\" — require \"slack\" in the tool name, rank by the remaining terms",
		InputSchema: mcpInputSchema{
			Type: "object",
			Properties: map[string]mcpProperty{
				"query": {
					Type:        "string",
					Description: "Keywords, a \"select:a,b\" exact list, or \"+term\" required-substring terms.",
				},
				"max_results": {
					Type:        "integer",
					Description: "Maximum number of tools to return.",
					Default:     maxDefault,
				},
			},
			Required:             []string{"query"},
			AdditionalProperties: false,
		},
	}
}

type lazySearchParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// lazyToolEntry pairs the searchable fields of a tool with the full payload that
// is returned to the caller (an mcpTool for native tools, a map for remote ones).
type lazyToolEntry struct {
	name        string
	description string
	payload     any
}

func (server mcpServer) lazyToolUniverse(ctx context.Context) []lazyToolEntry {
	var entries []lazyToolEntry
	for _, spec := range server.mcpSpecs(ctx) {
		tool := mcpToolFromSpec(spec)
		entries = append(entries, lazyToolEntry{
			name:        tool.Name,
			description: tool.Description,
			payload:     tool,
		})
	}
	for _, ref := range server.remoteMCPToolRefs(ctx) {
		spec := mcpRemoteActionSpecForEntry(ref.Entry, ref.Tool)
		if !server.selector.matches(spec) {
			continue
		}
		entries = append(entries, lazyToolEntry{
			name:        spec.ID,
			description: ref.Tool.Description,
			payload:     mcpRemoteToolForServeWithDefaults(spec.ID, ref.Tool, ref.Entry.Server.DefaultArguments),
		})
	}
	return entries
}

func (server mcpServer) searchTools(ctx context.Context, raw json.RawMessage) (mcpCallToolResult, error) {
	var params lazySearchParams
	if err := decodeMCPParams(raw, &params); err != nil {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: err.Error()}
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return mcpCallToolResult{}, mcpError{Code: -32602, Message: "query is required"}
	}
	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = lazySearchDefaultMaxResults
	}
	universe := server.lazyToolUniverse(ctx)
	matches := rankLazyTools(universe, query, maxResults)
	payloads := make([]any, 0, len(matches))
	for _, match := range matches {
		payloads = append(payloads, match.payload)
	}
	body, err := json.MarshalIndent(map[string]any{"tools": payloads}, "", "  ")
	if err != nil {
		return mcpCallToolResult{}, err
	}
	header := fmt.Sprintf("%d of %d tools matched %q. Call any of these by its exact name.\n\n",
		len(payloads), len(universe), query)
	return mcpCallToolResult{
		Content:           []mcpContent{{Type: "text", Text: header + string(body)}},
		StructuredContent: map[string]any{"tools": payloads},
	}, nil
}

// rankLazyTools selects and orders tools that match a query. A "select:" prefix
// returns the named tools verbatim; otherwise terms are matched against tool
// names and descriptions, with "+term" terms required to appear in the name.
func rankLazyTools(universe []lazyToolEntry, query string, maxResults int) []lazyToolEntry {
	if rest, ok := strings.CutPrefix(query, "select:"); ok {
		wanted := map[string]bool{}
		for name := range strings.SplitSeq(rest, ",") {
			if name = strings.TrimSpace(name); name != "" {
				wanted[name] = true
			}
		}
		var out []lazyToolEntry
		for _, entry := range universe {
			if wanted[entry.name] {
				out = append(out, entry)
			}
		}
		return out
	}

	var required, terms []string
	for field := range strings.FieldsSeq(strings.ToLower(query)) {
		if term, ok := strings.CutPrefix(field, "+"); ok {
			if term != "" {
				required = append(required, term)
			}
			continue
		}
		terms = append(terms, field)
	}

	type scored struct {
		entry lazyToolEntry
		score int
	}
	var results []scored
	for _, entry := range universe {
		name := strings.ToLower(entry.name)
		desc := strings.ToLower(entry.description)
		matchedRequired := true
		for _, req := range required {
			if !strings.Contains(name, req) {
				matchedRequired = false
				break
			}
		}
		if !matchedRequired {
			continue
		}
		score := len(required) * 5
		for _, term := range terms {
			if name == term {
				score += 10
			}
			if strings.Contains(name, term) {
				score += 3
			}
			if strings.Contains(desc, term) {
				score++
			}
		}
		// With scoring terms present a zero score means no match; with only
		// required terms the entry already passed the required-substring filter.
		if len(terms) > 0 && score == 0 {
			continue
		}
		results = append(results, scored{entry: entry, score: score})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].entry.name < results[j].entry.name
	})
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	out := make([]lazyToolEntry, len(results))
	for i, result := range results {
		out[i] = result.entry
	}
	return out
}
