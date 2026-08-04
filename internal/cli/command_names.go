package cli

import (
	"strings"

	"github.com/spf13/pflag"

	"github.com/fiam/toolmux/internal/actions"
)

type cliNameMapping struct {
	canonical map[string]string
	aliases   map[string]string
}

func newCLINameMapping(names []string, reserved func(string) bool) cliNameMapping {
	candidates := make(map[string]string, len(names))
	counts := make(map[string]int, len(names))
	for _, name := range names {
		candidate := actions.CLIName(name)
		if candidate == "" {
			candidate = name
		}
		candidates[name] = candidate
		counts[candidate]++
	}
	mapping := cliNameMapping{
		canonical: make(map[string]string, len(names)),
		aliases:   make(map[string]string, len(names)),
	}
	for _, name := range names {
		candidate := candidates[name]
		if counts[candidate] > 1 || (reserved != nil && reserved(candidate)) {
			candidate = name
		}
		mapping.canonical[name] = candidate
		if candidate != name {
			mapping.aliases[name] = candidate
		}
	}
	return mapping
}

func (mapping cliNameMapping) name(raw string) string {
	if name := mapping.canonical[raw]; name != "" {
		return name
	}
	return raw
}

func (mapping cliNameMapping) installFlagAliases(flags *pflag.FlagSet) {
	if len(mapping.aliases) == 0 {
		return
	}
	flags.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if canonical, ok := mapping.aliases[name]; ok {
			return pflag.NormalizedName(canonical)
		}
		return pflag.NormalizedName(name)
	})
}

func mcpRemoteToolCLIBaseName(entry mcpRemoteServerEntry, rawName string) string {
	name := rawName
	if catalogName, _, ok := mcpRemoteCatalogDefinitionForServer(entry.Name, entry.Server); ok {
		for _, separator := range []string{"-", "_"} {
			prefix := catalogName + separator
			if len(name) > len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
				name = name[len(prefix):]
				break
			}
		}
	}
	if canonical := actions.CLIName(name); canonical != "" {
		return canonical
	}
	return rawName
}

func mcpRemoteToolCLINames(entry mcpRemoteServerEntry, tools []mcpRemoteTool) map[string]string {
	preferred := make(map[string]string, len(tools))
	counts := make(map[string]int, len(tools))
	for _, tool := range tools {
		candidate := mcpRemoteToolCLIBaseName(entry, tool.Name)
		preferred[tool.Name] = candidate
		counts[candidate]++
	}
	chosen := make(map[string]string, len(tools))
	chosenCounts := make(map[string]int, len(tools))
	for _, tool := range tools {
		candidate := preferred[tool.Name]
		if counts[candidate] > 1 {
			candidate = actions.CLIName(tool.Name)
		}
		if candidate == "" {
			candidate = tool.Name
		}
		chosen[tool.Name] = candidate
		chosenCounts[candidate]++
	}
	for _, tool := range tools {
		if chosenCounts[chosen[tool.Name]] > 1 {
			chosen[tool.Name] = tool.Name
		}
	}
	return chosen
}
